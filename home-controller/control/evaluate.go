package control

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/mjasion/balena-home/thermostats/config"
	"github.com/mjasion/balena-home/thermostats/netatmo"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// evaluateAndExecuteRooms evaluates and executes control decisions for all rooms (sequential loop)
func (c *Controller) evaluateAndExecuteRooms(ctx context.Context, roomStatusMap map[string]*netatmo.RoomStatus) (skipCount, adjustCount, noAdjustCount int) {
	for _, mapping := range c.config.Mappings {
		decision := c.evaluateRoom(ctx, mapping, roomStatusMap)

		// Track decision types
		switch decision.Action {
		case "skip":
			skipCount++
		case "set_manual_override":
			adjustCount++
		case "no_adjustment_needed":
			noAdjustCount++
		}

		// Push metrics for this decision
		c.stateMu.RLock()
		state := c.stateByRoom[mapping.RoomID]
		externallyModified := false
		if state != nil {
			externallyModified = state.ExternallyModified
		}
		c.stateMu.RUnlock()

		// Check if hard override is active
		hardOverrideActive := c.isHardOverrideActive(mapping.RoomName)

		c.pushControlMetrics(decision, hardOverrideActive, externallyModified)
		c.executeDecision(ctx, decision)
	}

	return skipCount, adjustCount, noAdjustCount
}

// evaluateRoom evaluates whether a room needs thermostat adjustment
func (c *Controller) evaluateRoom(
	ctx context.Context,
	mapping config.ThermostatMapping,
	roomStatusMap map[string]*netatmo.RoomStatus,
) ControlDecision {
	ctx, span := c.tracer.Start(ctx, "evaluate_room_"+mapping.RoomName,
		trace.WithAttributes(
			attribute.String("room_name", mapping.RoomName),
			attribute.String("room_id", mapping.RoomID),
			attribute.String("sensor_mac", mapping.SensorMAC),
		),
	)

	decision := ControlDecision{
		RoomID:   mapping.RoomID,
		RoomName: mapping.RoomName,
		Action:   "skip",
	}

	defer func() {
		// Record decision attributes before span ends
		span.SetAttributes(
			attribute.String("decision_action", decision.Action),
			attribute.String("decision_reason", decision.Reason),
			attribute.Float64("xiaomi_temperature", decision.XiaomiTemperature),
			attribute.Float64("scheduled_temperature", decision.ScheduledTemp),
			attribute.Float64("thermostat_measured", decision.ThermostatMeasured),
			attribute.Float64("setpoint_temperature", decision.SetpointTemperature),
			attribute.String("thermostat_mode", decision.ThermostatMode),
		)
		if decision.Action == "set_manual_override" {
			span.SetAttributes(attribute.Float64("calculated_setpoint", decision.CalculatedSetpoint))
		}
		span.End()
	}()

	// Get current state (defensive copy)
	c.stateMu.RLock()
	state, exists := c.stateByRoom[mapping.RoomID]
	if !exists {
		c.stateMu.RUnlock()
		decision.Reason = "room state not initialized"
		return decision
	}
	stateCopy := state.Copy()
	c.stateMu.RUnlock()

	// Get room status early to populate temperature fields in all cases
	roomStatus, roomExists := roomStatusMap[mapping.RoomID]
	if roomExists && roomStatus.Reachable {
		decision.ThermostatMeasured = roomStatus.ThermMeasuredTemperature
		decision.SetpointTemperature = roomStatus.ThermSetpointTemperature
		decision.ThermostatMode = roomStatus.ThermSetpointMode

		// Determine scheduled temperature
		decision.ScheduledTemp = c.determineScheduledTemp(mapping.RoomName, &stateCopy, roomStatus)

		// Get Xiaomi sensor readings (last 60 seconds, weighted average)
		sensorMAC := strings.ToUpper(strings.TrimSpace(mapping.SensorMAC))
		xiaomiTemp, err := c.getWeightedAverageTemperature(ctx, sensorMAC)
		if err == nil {
			decision.XiaomiTemperature = xiaomiTemp
		}
	}

	// Detect home mode changes (away, hg) and reset state if needed
	if roomExists {
		c.detectHomeModeChange(&stateCopy, roomStatus)
		// Get fresh state copy after potential reset
		c.stateMu.RLock()
		stateCopy = c.stateByRoom[mapping.RoomID].Copy()
		c.stateMu.RUnlock()
	}

	// Detect external manual changes (user changed setpoint or endtime)
	if roomExists && c.detectExternalManualChange(&stateCopy, roomStatus) {
		decision.Reason = "external manual change detected, respecting user intent"
		return decision
	}

	// Check if we should control this room based on its mode
	if roomExists {
		shouldControl, skipReason := c.shouldControlRoom(&stateCopy, roomStatus)
		if !shouldControl {
			decision.Reason = skipReason
			return decision
		}
	}

	// Check if external modification flag is set (from old detection logic)
	if stateCopy.ExternallyModified {
		if roomExists && roomStatus.ThermSetpointMode == "schedule" {
			c.logger.Info("external modification cleared: thermostat returned to schedule mode",
				zap.String("room_name", mapping.RoomName),
			)
			c.clearExternalModification(mapping.RoomID)
			// Get fresh state copy after clearing flag
			c.stateMu.RLock()
			stateCopy = c.stateByRoom[mapping.RoomID].Copy()
			c.stateMu.RUnlock()
		} else {
			decision.Reason = "externally modified (legacy), respecting manual override"
			return decision
		}
	}

	// Check if we should extend an existing override
	shouldExtend, timeUntilExpiry := c.shouldExtendOverride(&stateCopy)

	// Validate room status is available and reachable
	if !roomExists {
		decision.Reason = "room not found in Netatmo status"
		c.logger.Warn("room not found in Netatmo home status",
			zap.String("room_name", mapping.RoomName),
			zap.String("room_id", mapping.RoomID),
		)
		return decision
	}

	if !roomStatus.Reachable {
		decision.Reason = "thermostat not reachable"
		return decision
	}

	// Validate Xiaomi sensor data is available
	if decision.XiaomiTemperature == 0 {
		decision.Reason = "sensor data unavailable"
		c.logger.Warn("sensor data unavailable for control",
			zap.String("room_name", mapping.RoomName),
			zap.String("sensor_mac", strings.ToUpper(strings.TrimSpace(mapping.SensorMAC))),
		)
		return decision
	}

	// Calculate temperature difference
	xiaomiTemp := decision.XiaomiTemperature
	scheduledTemp := decision.ScheduledTemp
	tempDiff := xiaomiTemp - scheduledTemp

	// Calculate sensor offset (how much Netatmo sensor differs from reality)
	// Positive offset means Netatmo reads higher than actual
	// Negative offset means Netatmo reads lower than actual
	sensorOffset := decision.ThermostatMeasured - xiaomiTemp

	// Calculate compensated setpoint to account for sensor inaccuracy
	// If Netatmo reads 1°C too low, we need to set setpoint 1°C lower
	// so when Netatmo reaches that setpoint, actual temp will be at target
	rawSetpoint := scheduledTemp + sensorOffset
	calculatedSetpoint := roundToHalfDegree(rawSetpoint)

	c.logger.Debug("calculated compensated setpoint",
		zap.String("room_name", mapping.RoomName),
		zap.Float64("xiaomi_temp", xiaomiTemp),
		zap.Float64("thermostat_measured", decision.ThermostatMeasured),
		zap.Float64("sensor_offset", sensorOffset),
		zap.Float64("scheduled_temp", scheduledTemp),
		zap.Float64("raw_setpoint", rawSetpoint),
		zap.Float64("calculated_setpoint", calculatedSetpoint),
	)

	// Check runaway protection (backup safety layer)
	if halted, reason := c.checkRunawayProtection(mapping, &stateCopy, calculatedSetpoint); halted {
		decision.Reason = reason
		return decision
	}

	// Check if calculated setpoint matches schedule (means no sensor offset to compensate)
	// AND actual temperature is within threshold (room is at correct temp)
	noSensorOffset := math.Abs(calculatedSetpoint-scheduledTemp) < 0.1
	tempWithinThreshold := math.Abs(tempDiff) < c.config.TemperatureThreshold

	if noSensorOffset && tempWithinThreshold && !shouldExtend {
		decision.Action = "no_adjustment_needed"
		decision.Reason = fmt.Sprintf("no sensor offset detected and temperature within threshold (%.2f°C)", math.Abs(tempDiff))
		return decision
	}

	if shouldExtend {
		c.logger.Debug("extending override",
			zap.String("room_name", mapping.RoomName),
			zap.Float64("calculated_setpoint", calculatedSetpoint),
			zap.Duration("time_until_expiry", timeUntilExpiry),
		)
	}

	// Apply safety bounds
	calculatedSetpoint = c.applySafetyBounds(calculatedSetpoint)

	// Check if current setpoint already matches target
	currentSetpoint := roomStatus.ThermSetpointTemperature
	if math.Abs(currentSetpoint-calculatedSetpoint) < 0.1 && !shouldExtend {
		c.clearPendingSetpoint(mapping.RoomID, mapping.RoomName)
		decision.Action = "no_adjustment_needed"
		decision.Reason = fmt.Sprintf("setpoint already at target (%.1f°C), mode=%s", calculatedSetpoint, roomStatus.ThermSetpointMode)
		return decision
	}

	// Check delayed execution (primary feedback loop prevention)
	// Exception: When extending an existing override, execute immediately
	if !shouldExtend {
		if shouldDelay, reason := c.checkDelayedExecution(mapping, &stateCopy, calculatedSetpoint); shouldDelay {
			decision.Action = "skip"
			decision.Reason = reason
			return decision
		}
	}

	decision.Action = "set_manual_override"
	decision.CalculatedSetpoint = calculatedSetpoint
	decision.OverrideEndTime = time.Now().Add(time.Duration(c.config.OverrideDurationMinutes) * time.Minute).Unix()

	// Build reason message
	reasonParts := []string{
		fmt.Sprintf("xiaomi=%.1f°C", xiaomiTemp),
		fmt.Sprintf("target=%.1f°C", scheduledTemp),
		fmt.Sprintf("netatmo=%.1f°C", decision.ThermostatMeasured),
		fmt.Sprintf("offset=%.2f°C", sensorOffset),
		fmt.Sprintf("setpoint=%.1f°C", calculatedSetpoint),
	}

	if c.isHardOverrideActive(mapping.RoomName) {
		reasonParts = append(reasonParts, "hard override")
	} else if shouldExtend {
		reasonParts = append(reasonParts, fmt.Sprintf("extending, %.0fm left", timeUntilExpiry.Minutes()))
	}

	decision.Reason = strings.Join(reasonParts, ", ")

	// Detect external modification
	if c.detectExternalModification(&stateCopy, roomStatus) {
		c.markExternallyModified(mapping.RoomID)
		decision.Action = "skip"
		decision.Reason = "external modification detected"
	}

	return decision
}

// determineScheduledTemp determines the scheduled temperature for a room
func (c *Controller) determineScheduledTemp(roomName string, state *ThermostatState, roomStatus *netatmo.RoomStatus) float64 {
	// Check for hard overrides first (highest precedence)
	for _, override := range c.config.HardOverrides {
		if override.RoomName == roomName {
			if temp, active := c.getHardOverrideTemp(override); active {
				return temp
			}
		}
	}

	// Prefer synced schedule temperature (if available and recent)
	if state.SyncedScheduledTemp > 0 && !state.SyncedScheduledTime.IsZero() {
		timeSinceSync := time.Since(state.SyncedScheduledTime)
		if timeSinceSync < 1*time.Hour {
			c.logger.Debug("using synced schedule temperature",
				zap.String("room_name", roomName),
				zap.Float64("synced_temp", state.SyncedScheduledTemp),
				zap.Duration("time_since_sync", timeSinceSync),
			)
			return state.SyncedScheduledTemp
		}
		c.logger.Debug("synced schedule temperature too old, using current setpoint",
			zap.String("room_name", roomName),
			zap.Duration("time_since_sync", timeSinceSync),
		)
	}

	// Fallback: use current setpoint temperature
	return roomStatus.ThermSetpointTemperature
}


// roundToHalfDegree rounds a temperature to the nearest 0.5°C
// Netatmo thermostats only accept setpoints in 0.5°C increments
func roundToHalfDegree(temp float64) float64 {
	return math.Round(temp*2.0) / 2.0
}

// applySafetyBounds applies safety limits to the calculated setpoint
func (c *Controller) applySafetyBounds(setpoint float64) float64 {
	if setpoint < 7.0 {
		return 7.0
	} else if setpoint > 30.0 {
		return 30.0
	}
	return setpoint
}

// shouldExtendOverride checks if an existing override should be extended
func (c *Controller) shouldExtendOverride(state *ThermostatState) (bool, time.Duration) {
	if state.OverrideEndTime.IsZero() {
		return false, 0
	}

	timeUntilExpiry := time.Until(state.OverrideEndTime)
	extensionThreshold := time.Duration(c.config.ExtensionThresholdMinutes) * time.Minute

	if timeUntilExpiry > 0 && timeUntilExpiry < extensionThreshold {
		return true, timeUntilExpiry
	}

	return false, timeUntilExpiry
}

// detectExternalModification detects if the thermostat was manually changed
func (c *Controller) detectExternalModification(state *ThermostatState, roomStatus *netatmo.RoomStatus) bool {
	// Only check if we previously sent a command
	if state.LastSetpointTime.IsZero() {
		return false
	}

	timeSinceLastCommand := time.Since(state.LastSetpointTime)
	overrideExpired := !state.OverrideEndTime.IsZero() && time.Now().After(state.OverrideEndTime)

	// Only detect if: command was sent >2min ago, override not expired, not in schedule mode
	if timeSinceLastCommand <= 2*time.Minute || overrideExpired || roomStatus.ThermSetpointMode == "schedule" {
		return false
	}

	// Check if current setpoint differs from what we sent
	delta := roomStatus.ThermSetpointTemperature - state.LastSetpoint
	if math.Abs(delta) > 0.1 {
		c.logger.Warn("external modification detected - backing off from automation indefinitely",
			zap.String("room_name", state.RoomName),
			zap.Float64("expected_setpoint", state.LastSetpoint),
			zap.Float64("actual_setpoint", roomStatus.ThermSetpointTemperature),
			zap.Float64("delta", delta),
			zap.Duration("time_since_last_command", timeSinceLastCommand),
			zap.Time("last_command_sent_at", state.LastSetpointTime.In(c.warsawLocation())),
			zap.String("thermostat_mode", roomStatus.ThermSetpointMode),
			zap.String("resume_condition", "automation will resume only when thermostat is switched back to 'schedule' mode"),
		)
		return true
	}

	return false
}

// isHardOverrideActive checks if a hard override is currently active for a room
func (c *Controller) isHardOverrideActive(roomName string) bool {
	for _, override := range c.config.HardOverrides {
		if override.RoomName == roomName {
			_, active := c.getHardOverrideTemp(override)
			return active
		}
	}
	return false
}

// getHardOverrideTemp checks if a hard override is currently active and returns the target temperature
func (c *Controller) getHardOverrideTemp(override config.HardOverride) (float64, bool) {
	now := time.Now().In(c.warsawLocation())
	currentTime := now.Format("15:04")
	currentDay := now.Weekday().String()[:3] // "Mon", "Tue", etc.

	for _, window := range override.Schedule {
		// Check if time matches
		if currentTime >= window.StartTime && currentTime <= window.EndTime {
			// If days are specified, check if current day matches
			if len(window.Days) > 0 {
				dayMatches := false
				for _, day := range window.Days {
					normalizedDay := day
					if len(day) > 3 {
						normalizedDay = day[:3]
					}
					if normalizedDay == currentDay {
						dayMatches = true
						break
					}
				}
				if !dayMatches {
					continue
				}
			}
			return window.TargetTemperature, true
		}
	}
	return 0, false
}

// checkRunawayProtection checks if control should be halted due to runaway detection
// Returns (halted, reason)
func (c *Controller) checkRunawayProtection(mapping config.ThermostatMapping, state *ThermostatState, calculatedSetpoint float64) (bool, string) {
	// Check if control is currently halted
	if !state.RunawayHaltUntil.IsZero() && time.Now().Before(state.RunawayHaltUntil) {
		timeRemaining := time.Until(state.RunawayHaltUntil)
		c.logger.Warn("runaway protection active",
			zap.String("room_name", mapping.RoomName),
			zap.Duration("time_remaining", timeRemaining),
		)
		return true, fmt.Sprintf("runaway protection: control halted for %.0fm (%.0fs remaining)",
			timeRemaining.Minutes(), timeRemaining.Seconds())
	}

	// Track consecutive increases
	consecutiveIncreases := state.ConsecutiveIncreases
	lastCalculated := state.LastCalculatedSetpoint

	if calculatedSetpoint > lastCalculated+0.1 {
		consecutiveIncreases++
		c.logger.Debug("consecutive increase detected",
			zap.String("room_name", mapping.RoomName),
			zap.Int("consecutive_count", consecutiveIncreases),
			zap.Float64("last_calculated", lastCalculated),
			zap.Float64("current_calculated", calculatedSetpoint),
			zap.Float64("increase", calculatedSetpoint-lastCalculated),
		)
	} else {
		if consecutiveIncreases > 0 {
			c.logger.Debug("consecutive increases reset",
				zap.String("room_name", mapping.RoomName),
				zap.Int("previous_count", consecutiveIncreases),
			)
		}
		consecutiveIncreases = 0
	}

	// Halt if 3+ consecutive increases detected
	if consecutiveIncreases >= 3 {
		haltDuration := 5 * time.Minute
		haltUntil := time.Now().Add(haltDuration)

		c.logger.Error("RUNAWAY DETECTED: 3+ consecutive increases, halting control",
			zap.String("room_name", mapping.RoomName),
			zap.Float64("last_calculated_setpoint", lastCalculated),
			zap.Float64("current_calculated_setpoint", calculatedSetpoint),
			zap.Int("consecutive_increases", consecutiveIncreases),
			zap.Duration("halt_duration", haltDuration),
			zap.Time("halt_until", haltUntil),
		)

		c.stateMu.Lock()
		if roomState, exists := c.stateByRoom[mapping.RoomID]; exists {
			roomState.RunawayHaltUntil = haltUntil
			roomState.ConsecutiveIncreases = 0
			roomState.LastCalculatedSetpoint = 0
		}
		c.stateMu.Unlock()

		return true, fmt.Sprintf("RUNAWAY DETECTED: 3 consecutive increases (%.1f→%.1f→%.1f°C), halting for %dm",
			lastCalculated-1.0, lastCalculated-0.5, calculatedSetpoint, int(haltDuration.Minutes()))
	}

	// Update tracking state
	c.stateMu.Lock()
	if roomState, exists := c.stateByRoom[mapping.RoomID]; exists {
		roomState.ConsecutiveIncreases = consecutiveIncreases
		roomState.LastCalculatedSetpoint = calculatedSetpoint
	}
	c.stateMu.Unlock()

	return false, ""
}

// checkDelayedExecution implements delayed execution pattern to prevent feedback loops
// Returns (shouldDelay, reason)
func (c *Controller) checkDelayedExecution(mapping config.ThermostatMapping, state *ThermostatState, calculatedSetpoint float64) (bool, string) {
	// Bypass delayed execution if schedule just changed during sync
	// This allows immediate response to schedule changes
	if state.ScheduleJustChanged {
		c.logger.Info("delayed execution: bypassing for schedule change",
			zap.String("room_name", mapping.RoomName),
			zap.Float64("calculated_setpoint", calculatedSetpoint),
		)
		// Clear the flag and pending setpoint
		c.stateMu.Lock()
		if s, exists := c.stateByRoom[mapping.RoomID]; exists {
			s.ScheduleJustChanged = false
			s.PendingSetpoint = 0
			s.PendingSetpointTime = time.Time{}
		}
		c.stateMu.Unlock()
		return false, "" // Don't delay, execute immediately
	}

	pendingSetpoint := state.PendingSetpoint
	pendingTime := state.PendingSetpointTime

	if pendingSetpoint != 0 && !pendingTime.IsZero() {
		// Pending setpoint exists - check if target changed
		if math.Abs(calculatedSetpoint-pendingSetpoint) < 0.1 {
			// Same change needed twice → EXECUTE
			c.logger.Info("delayed execution: confirmed - executing override",
				zap.String("room_name", mapping.RoomName),
				zap.Float64("pending_setpoint", pendingSetpoint),
				zap.Float64("calculated_setpoint", calculatedSetpoint),
				zap.Duration("pending_age", time.Since(pendingTime)),
			)
			c.clearPendingSetpoint(mapping.RoomID, mapping.RoomName)
			return false, ""
		}

		// Different setpoint needed → UPDATE PENDING
		c.logger.Info("delayed execution: target changed - updating pending",
			zap.String("room_name", mapping.RoomName),
			zap.Float64("previous_pending", pendingSetpoint),
			zap.Float64("new_pending", calculatedSetpoint),
			zap.Float64("change", calculatedSetpoint-pendingSetpoint),
		)
		c.setPendingSetpoint(mapping.RoomID, calculatedSetpoint)
		return true, fmt.Sprintf("delayed execution: target changed from %.1f°C to %.1f°C, awaiting confirmation", pendingSetpoint, calculatedSetpoint)
	}

	// No pending setpoint → SET PENDING
	c.logger.Info("delayed execution: marking for confirmation",
		zap.String("room_name", mapping.RoomName),
		zap.Float64("calculated_setpoint", calculatedSetpoint),
	)
	c.setPendingSetpoint(mapping.RoomID, calculatedSetpoint)
	return true, fmt.Sprintf("delayed execution: marked %.1f°C for confirmation (will execute next iteration if still needed)", calculatedSetpoint)
}

// clearPendingSetpoint clears the pending setpoint for a room
func (c *Controller) clearPendingSetpoint(roomID, roomName string) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()

	if state, exists := c.stateByRoom[roomID]; exists && state.PendingSetpoint != 0 {
		c.logger.Debug("clearing pending setpoint",
			zap.String("room_name", roomName),
			zap.Float64("pending_setpoint", state.PendingSetpoint),
		)
		state.PendingSetpoint = 0
		state.PendingSetpointTime = time.Time{}
	}
}

// setPendingSetpoint sets the pending setpoint for a room
func (c *Controller) setPendingSetpoint(roomID string, setpoint float64) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()

	if state, exists := c.stateByRoom[roomID]; exists {
		state.PendingSetpoint = setpoint
		state.PendingSetpointTime = time.Now()
	}
}

