# Thermostat Runaway Incident Analysis

**Date**: 2025-11-18 06:33:30 - 06:43:30 (10-minute incident)
**Room**: Łazienka (Bathroom)
**Severity**: CRITICAL - Temperature increased from 24°C to 29°C uncontrollably

## Executive Summary

A critical bug in the thermostat control algorithm caused a runaway feedback loop that increased the bathroom temperature by 5°C in 10 minutes. The user was unable to manually override the temperature due to the algorithm overriding their changes every 60 seconds, forcing a system reset.

## What Happened

### Timeline

| Time | Scheduled Temp | Setpoint | Action | Status |
|------|----------------|----------|--------|--------|
| 06:29:30-06:32:30 | 24.5-25.5°C | 24.5°C | Skip (external modification) | ✅ Correct |
| 06:33:30 | 24.0°C | 24.5°C | Resume control | ❌ **BUG TRIGGERED** |
| 06:34:30 | 24.5°C | 25.0°C | Set override | ❌ Feedback loop started |
| 06:35:30 | 25.0°C | 25.5°C | Set override | ❌ Runaway |
| 06:36:30 | 25.5°C | 26.0°C | Set override | ❌ Runaway |
| 06:37:30 | 26.0°C | 26.5°C | Set override | ❌ Runaway |
| 06:38:30 | 26.5°C | 27.0°C | Set override | ❌ Runaway |
| 06:39:30 | 27.0°C | 27.5°C | Set override | ❌ Runaway |
| 06:40:30 | 27.5°C | 28.0°C | Set override | ❌ Runaway |
| 06:41:30 | 28.0°C | 28.5°C | Set override | ❌ Runaway |
| 06:42:30 | 28.5°C | 29.0°C | Set override | ❌ **Near max limit** |
| 06:43:30 | - | - | System reinitialized | 🔧 User reset |

### Pattern

- **Rate of increase**: +0.5°C every minute
- **Total increase**: +5.0°C in 10 minutes
- **Sensor offset**: 0.7°C (Netatmo reads 26.0°C vs Xiaomi 25.3°C)
- **Xiaomi temp**: 25.3°C (constant throughout)
- **Control loop frequency**: Every 60 seconds

## Root Cause Analysis

### Primary Bug: Feedback Loop in `determineScheduledTemp`

**Location**: `home-controller/control/evaluate.go:303`

**The Bug**:
```go
func (c *Controller) determineScheduledTemp(...) float64 {
    // Check for hard overrides first
    if hardOverrideActive {
        return hardOverrideTemp  // ✅ Safe
    }

    // Prefer synced schedule temperature (if available and recent)
    if state.SyncedScheduledTemp > 0 && !state.SyncedScheduledTime.IsZero() {
        timeSinceSync := time.Since(state.SyncedScheduledTime)
        if timeSinceSync < 1*time.Hour {
            return state.SyncedScheduledTemp  // ✅ Safe
        }
    }

    // ❌ FATAL BUG: Fallback uses current setpoint as "schedule"
    return roomStatus.ThermSetpointTemperature
}
```

**The Feedback Loop**:

1. **Iteration N**:
   - scheduled_temp = 24.0°C (from setpoint)
   - sensor_offset = 0.7°C
   - calculated_setpoint = 24.0 + 0.7 = 24.7 → rounds to 24.5°C
   - **Sets setpoint to 24.5°C**

2. **Iteration N+1** (60 seconds later):
   - ❌ Reads setpoint of 24.5°C
   - ❌ Uses 24.5°C as the "scheduled temperature"
   - sensor_offset = 0.7°C
   - calculated_setpoint = 24.5 + 0.7 = 25.2 → rounds to 25.0°C
   - **Sets setpoint to 25.0°C**

3. **Iteration N+2**:
   - ❌ Reads setpoint of 25.0°C
   - ❌ Uses 25.0°C as the "scheduled temperature"
   - ...continues increasing...

**Formula**:
```
setpoint[N+1] = setpoint[N] + sensor_offset
```

This creates an unbounded increase limited only by:
- Maximum safety limit (30°C)
- User intervention (reset)

### Contributing Factors

1. **Schedule Sync Stale**:
   - Sync interval: 30 minutes
   - Synced schedule temperature was either never set or >1 hour old
   - Triggered the buggy fallback logic

2. **High Control Frequency**:
   - Control loop runs every 60 seconds
   - User had no time to react between overrides
   - Made manual override impossible

3. **No Runaway Detection**:
   - No safeguards against consecutive increases
   - No rate limiting on setpoint changes
   - No anomaly detection

4. **External Modification Handling**:
   - When user manually changed thermostat, it was correctly detected
   - But when thermostat returned to schedule mode (06:33:30), control resumed
   - No valid synced schedule was available at that moment

## Impact

### User Impact
- ❌ **Loss of Control**: User unable to manually set temperature
- ❌ **Overheating**: 5°C increase in 10 minutes
- ❌ **Forced Reset**: Only way to stop was system restart
- ❌ **Loss of Trust**: Algorithm behaved dangerously

### Safety Concerns
- Temperature reached 29°C (only 1°C from max safety limit)
- Could have continued if safety limit not in place
- User was locked out of manual control
- Heating system ran at full power unnecessarily

## Proposed Solutions

### 1. **FIX THE FALLBACK LOGIC** (Critical)

**Current Code** (evaluate.go:303):
```go
// Fallback: use current setpoint temperature
return roomStatus.ThermSetpointTemperature  // ❌ CREATES FEEDBACK LOOP
```

**Proposed Fix**:
```go
// Fallback: Only use setpoint as schedule if thermostat is in schedule mode
// AND we haven't set a recent manual override
if roomStatus.ThermSetpointMode == "schedule" {
    // Only trust setpoint if we haven't sent a command recently
    if state.LastSetpointTime.IsZero() || time.Since(state.LastSetpointTime) > 15*time.Minute {
        return roomStatus.ThermSetpointTemperature
    }
}

// If no valid schedule available and we're in manual mode, SKIP control
c.logger.Warn("no valid schedule available, skipping control",
    zap.String("room_name", state.RoomName),
    zap.String("thermostat_mode", roomStatus.ThermSetpointMode),
)
return 0  // Return 0 to signal "skip control"
```

**Why This Works**:
- Only uses setpoint as schedule when thermostat is truly in schedule mode
- Doesn't trust setpoint if we recently set a manual override
- Returns 0 to signal the control loop should skip this iteration
- Breaks the feedback loop

### 2. **ADD RUNAWAY DETECTION** (Critical)

**Add to ThermostatState** (types.go):
```go
type ThermostatState struct {
    // ... existing fields ...

    // Runaway detection
    ConsecutiveIncreases int      // Count of consecutive setpoint increases
    LastCalculatedSetpoint float64 // Last setpoint we calculated (not sent)
}
```

**Add to evaluate.go**:
```go
// Check for runaway condition BEFORE setting override
c.stateMu.RLock()
state := c.stateByRoom[mapping.RoomID]
consecutiveIncreases := state.ConsecutiveIncreases
lastCalculated := state.LastCalculatedSetpoint
c.stateMu.RUnlock()

// Detect if setpoint is increasing consecutively
if calculatedSetpoint > lastCalculated+0.1 {
    consecutiveIncreases++
} else {
    consecutiveIncreases = 0  // Reset counter
}

// RUNAWAY PROTECTION: Halt control if 3+ consecutive increases
if consecutiveIncreases >= 3 {
    c.logger.Error("RUNAWAY DETECTED: 3+ consecutive increases, halting control",
        zap.String("room_name", mapping.RoomName),
        zap.Float64("last_setpoint", lastCalculated),
        zap.Float64("calculated_setpoint", calculatedSetpoint),
        zap.Int("consecutive_increases", consecutiveIncreases),
    )

    // Mark as externally modified to pause control indefinitely
    c.markExternallyModified(mapping.RoomID)

    decision.Action = "skip"
    decision.Reason = "RUNAWAY DETECTED: consecutive increases halted"
    return decision
}

// Update state before returning
c.stateMu.Lock()
state.ConsecutiveIncreases = consecutiveIncreases
state.LastCalculatedSetpoint = calculatedSetpoint
c.stateMu.Unlock()
```

**Why This Works**:
- Detects when setpoint increases 3+ times in a row
- Immediately halts control and marks room as externally modified
- Requires user to switch thermostat to schedule mode to resume (manual intervention)
- Prevents runaway from continuing even if the bug isn't fully fixed

### 3. **ADD RATE LIMITING** (Important)

**Add configuration** (config.yaml):
```yaml
thermostatControl:
  # Maximum setpoint change per iteration (°C)
  # Prevents runaway by limiting how much setpoint can change
  maxSetpointChangeCelsius: 1.0  # Default: 1.0°C
```

**Add to evaluate.go**:
```go
// Rate limiting: Don't allow setpoint to change by more than maxChange per iteration
maxChange := c.config.MaxSetpointChangeCelsius
currentSetpoint := roomStatus.ThermSetpointTemperature
maxAllowedSetpoint := currentSetpoint + maxChange
minAllowedSetpoint := currentSetpoint - maxChange

if calculatedSetpoint > maxAllowedSetpoint {
    c.logger.Warn("rate limiting: clamping excessive setpoint increase",
        zap.String("room_name", mapping.RoomName),
        zap.Float64("calculated_setpoint", calculatedSetpoint),
        zap.Float64("max_allowed_setpoint", maxAllowedSetpoint),
        zap.Float64("max_change", maxChange),
    )
    calculatedSetpoint = maxAllowedSetpoint
} else if calculatedSetpoint < minAllowedSetpoint {
    c.logger.Warn("rate limiting: clamping excessive setpoint decrease",
        zap.String("room_name", mapping.RoomName),
        zap.Float64("calculated_setpoint", calculatedSetpoint),
        zap.Float64("min_allowed_setpoint", minAllowedSetpoint),
        zap.Float64("max_change", maxChange),
    )
    calculatedSetpoint = minAllowedSetpoint
}
```

**Why This Works**:
- Limits maximum change to 1°C per iteration
- In the incident, would have limited increase to 1°C instead of 5°C
- Gives user more time to react and intervene
- Reduces impact even if runaway occurs

### 4. **IMPROVE SCHEDULE SYNC RELIABILITY** (Important)

**Current Issue**:
- Schedule sync runs every 30 minutes
- If sync fails or doesn't run at the right time, can become stale
- No guarantee that synced temperature is available when needed

**Proposed Fix**:
```go
// Add to evaluate.go - before evaluating any room
func (c *Controller) ensureValidSchedule(roomID string) bool {
    c.stateMu.RLock()
    state := c.stateByRoom[roomID]
    hasSyncedSchedule := state.SyncedScheduledTemp > 0 &&
                        !state.SyncedScheduledTime.IsZero() &&
                        time.Since(state.SyncedScheduledTime) < 1*time.Hour
    c.stateMu.RUnlock()

    return hasSyncedSchedule
}

// In evaluateRoom():
if !c.ensureValidSchedule(mapping.RoomID) {
    // No valid synced schedule - trigger sync now or skip control
    c.logger.Warn("no valid synced schedule for room, skipping control",
        zap.String("room_name", mapping.RoomName),
    )
    decision.Reason = "no valid schedule synced, skipping for safety"
    return decision
}
```

**Why This Works**:
- Checks if synced schedule is valid before evaluating room
- Skips control if no valid schedule available
- Prevents fallback logic from ever being used
- Forces schedule sync to happen or control is skipped

### 5. **ADD MONITORING ALERTS** (Recommended)

**Add Prometheus metrics**:
```go
// Add to metrics package
var (
    consecutiveIncreasesGauge = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "thermostat_control_consecutive_increases",
            Help: "Number of consecutive setpoint increases for runaway detection",
        },
        []string{"room_name"},
    )

    setpointChangeGauge = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "thermostat_control_setpoint_change_celsius",
            Help: "Change in setpoint from previous iteration (positive = increase)",
        },
        []string{"room_name"},
    )
)
```

**Alert rules** (Grafana):
```yaml
- alert: ThermostatRunawayDetected
  expr: thermostat_control_consecutive_increases >= 3
  for: 1m
  annotations:
    summary: "Thermostat runaway detected in {{ $labels.room_name }}"
    description: "{{ $labels.room_name }} has {{ $value }} consecutive increases"

- alert: ThermostatLargeChange
  expr: abs(thermostat_control_setpoint_change_celsius) > 1.5
  for: 1m
  annotations:
    summary: "Large setpoint change in {{ $labels.room_name }}"
    description: "{{ $labels.room_name }} changed by {{ $value }}°C"
```

**Why This Works**:
- Detects runaway conditions in real-time
- Alerts user before temperature gets too high
- Provides early warning system

## Implementation Priority

### Phase 1: Critical Fixes (IMMEDIATE - Deploy Today)
1. ✅ Fix the fallback logic in `determineScheduledTemp` (evaluate.go:303)
2. ✅ Add runaway detection (3+ consecutive increases)
3. ✅ Add rate limiting (max 1°C change per iteration)

### Phase 2: Reliability (Deploy This Week)
4. ⚠️ Improve schedule sync reliability
5. ⚠️ Add monitoring alerts

### Phase 3: Testing (Before Enabling)
6. 🧪 Test with dry-run mode enabled
7. 🧪 Verify all scenarios:
   - Normal operation
   - Manual override
   - Schedule sync failure
   - Sensor data unavailable
   - Runaway attempts

## Testing Plan

### Unit Tests
```bash
cd home-controller/control
go test -v -run TestRunawayDetection
go test -v -run TestRateLimiting
go test -v -run TestFallbackLogic
```

### Integration Tests
1. **Scenario 1: No synced schedule**
   - Start with no SyncedScheduledTemp
   - Verify control is skipped
   - Verify no setpoint is sent

2. **Scenario 2: Runaway attempt**
   - Simulate 3 consecutive increases
   - Verify control halts on 3rd increase
   - Verify external modification flag is set

3. **Scenario 3: Rate limiting**
   - Calculate setpoint 3°C higher than current
   - Verify clamped to +1°C max
   - Verify warning logged

### Manual Testing (Dry-Run)
```yaml
thermostatControl:
  dryRun: true  # Enable dry-run for safe testing
```

1. Monitor logs for 24 hours
2. Verify no runaway patterns
3. Check all rooms have valid synced schedules
4. Verify manual overrides are respected

## Recommendations

### Short-term (This Week)
1. ✅ **Deploy Phase 1 fixes immediately**
2. ⚠️ Keep `dryRun: true` until all fixes tested
3. 📊 Add monitoring alerts
4. 📋 Review logs daily for anomalies

### Medium-term (This Month)
1. 🔍 Consider reducing control frequency to every 5 minutes (instead of every minute)
2. 📈 Add dashboard showing:
   - Consecutive increases per room
   - Setpoint changes over time
   - Schedule sync status
3. 🛡️ Add user-configurable "emergency stop" endpoint

### Long-term (Next Quarter)
1. 🧠 Implement predictive control (anticipate temperature changes)
2. 🎯 Add PID controller instead of simple offset compensation
3. 📚 Add automated regression tests for all runaway scenarios

## Lessons Learned

1. **Never use algorithm output as algorithm input** - The fallback logic created a feedback loop by using the setpoint (algorithm output) as the schedule (algorithm input)

2. **Always have multiple safety layers** - A single bug should never cause catastrophic failure:
   - ✅ Safety limits (10-30°C) - helped but not enough
   - ❌ Missing: Runaway detection
   - ❌ Missing: Rate limiting
   - ❌ Missing: Change monitoring

3. **High-frequency control is risky** - Running every 60 seconds left no time for user intervention

4. **Fallback logic must be safe** - When primary data unavailable, fail-safe (skip) rather than guess

5. **Monitor everything** - Should have had alerts for:
   - Consecutive increases
   - Large setpoint changes
   - Missing synced schedules

## Related Files

- Bug location: `home-controller/control/evaluate.go:303`
- State tracking: `home-controller/control/types.go`
- Schedule sync: `home-controller/control/sync.go`
- Configuration: `home-controller/config.yaml`
- Documentation: `home-controller/CONTROL_ALGORITHM.md`

## References

- Original incident logs: Provided by user on 2025-11-18
- Control algorithm documentation: `CONTROL_ALGORITHM.md`
- Recent fixes:
  - `63ae958` - Round thermostat setpoints to 0.5°C increments
  - `ba1c2fb` - Apply sensor offset compensation
  - `0d90441` - Prevent sync mode from resetting user-set manual thermostats
