# Thermostat Control Oscillation Fix - Solution Proposal

## Problem Summary

The thermostat control system experiences oscillations due to:
1. **Dynamic sensor offset** - Netatmo's measured temperature changes as room heats/cools, causing offset recalculation
2. **False schedule change detection** - Schedule sync reads automation-set setpoints as "schedule temperature"
3. **Lack of hysteresis** - Small offset changes (0.5°C) trigger immediate setpoint adjustments

## Proposed Solutions

### Solution 1: Stabilize Sensor Offset (RECOMMENDED)

**Approach:** Track and stabilize the sensor offset instead of recalculating it every iteration.

**Implementation:**
1. Calculate offset only when thermostat is in **natural schedule mode** (not during our overrides)
2. Store the baseline offset in `ThermostatState`
3. Use stored offset for calculations during manual mode
4. Re-learn offset periodically (e.g., when returning to schedule mode)

**Benefits:**
- Eliminates oscillations caused by changing `thermostat_measured`
- More predictable behavior
- Offset represents true sensor calibration

**Code changes:**

```go
// types.go - Add to ThermostatState
type ThermostatState struct {
    // ... existing fields ...

    // Sensor offset tracking
    BaselineSensorOffset     float64   // Calibrated offset (Netatmo - Xiaomi)
    BaselineSensorOffsetTime time.Time // When offset was last calculated
}

// evaluate.go - Modify offset calculation
func (c *Controller) evaluateRoom(...) ControlDecision {
    // ... existing code ...

    // Determine sensor offset to use
    var sensorOffset float64

    // Only recalculate offset when in natural schedule mode
    // (not during our own manual overrides)
    if roomStatus.ThermSetpointMode == "schedule" && !c.hasRecentOverride(&stateCopy) {
        // Calculate fresh offset from current readings
        sensorOffset = decision.ThermostatMeasured - xiaomiTemp

        // Store as baseline for future use
        c.updateBaselineOffset(mapping.RoomID, sensorOffset)

        c.logger.Debug("updated baseline sensor offset",
            zap.String("room_name", mapping.RoomName),
            zap.Float64("sensor_offset", sensorOffset),
        )
    } else {
        // Use stored baseline offset (stable)
        sensorOffset = stateCopy.BaselineSensorOffset

        // If no baseline yet, calculate from current (bootstrap)
        if sensorOffset == 0 {
            sensorOffset = decision.ThermostatMeasured - xiaomiTemp
            c.updateBaselineOffset(mapping.RoomID, sensorOffset)
        }

        c.logger.Debug("using baseline sensor offset",
            zap.String("room_name", mapping.RoomName),
            zap.Float64("baseline_offset", sensorOffset),
            zap.Float64("current_offset", decision.ThermostatMeasured - xiaomiTemp),
        )
    }

    // Calculate setpoint using stable offset
    rawSetpoint := scheduledTemp + sensorOffset
    calculatedSetpoint := roundToHalfDegree(rawSetpoint)

    // ... rest of logic ...
}

// Helper functions
func (c *Controller) hasRecentOverride(state *ThermostatState) bool {
    if state.LastSetpointTime.IsZero() {
        return false
    }
    return time.Since(state.LastSetpointTime) < 15*time.Minute
}

func (c *Controller) updateBaselineOffset(roomID string, offset float64) {
    c.stateMu.Lock()
    defer c.stateMu.Unlock()

    if state, exists := c.stateByRoom[roomID]; exists {
        state.BaselineSensorOffset = offset
        state.BaselineSensorOffsetTime = time.Now()
    }
}
```

---

### Solution 2: Add Hysteresis/Deadband

**Approach:** Only change setpoint if new calculated value differs significantly from current.

**Implementation:**

```go
// evaluate.go - Add hysteresis check
const SetpointChangeThreshold = 0.3 // Don't change if within 0.3°C

// Check if new setpoint is significantly different
setpointDelta := math.Abs(calculatedSetpoint - currentSetpoint)

if setpointDelta < SetpointChangeThreshold && !shouldExtend {
    c.clearPendingSetpoint(mapping.RoomID, mapping.RoomName)
    decision.Action = "no_adjustment_needed"
    decision.Reason = fmt.Sprintf("setpoint within deadband (%.1f°C vs %.1f°C, delta=%.2f°C)",
        currentSetpoint, calculatedSetpoint, setpointDelta)
    return decision
}
```

**Benefits:**
- Prevents minor oscillations
- Reduces API calls
- Simpler than offset stabilization

**Drawbacks:**
- Doesn't fix root cause
- Room temperature may drift slightly within deadband

---

### Solution 3: Fix Schedule Sync False Positives

**Approach:** Don't treat automation-set setpoints as "schedule temperature" during sync.

**Implementation:**

```go
// sync.go - Modify schedule sync logic
func (c *Controller) pollUntilAllRoomsSynced(...) {
    // ... existing code ...

    for i := range homeStatus.Body.Home.Rooms {
        roomStatus := &homeStatus.Body.Home.Rooms[i]
        if roomStatus.ThermSetpointMode == "schedule" && !roomsSynced[roomStatus.ID] {
            c.stateMu.Lock()
            if state, exists := c.stateByRoom[roomStatus.ID]; exists {
                // Check if we recently switched this room to schedule mode for sync
                justSwitchedForSync := time.Since(state.LastSetpointTime) < 10*time.Second

                if justSwitchedForSync {
                    // Skip syncing this iteration - wait for true schedule setpoint
                    c.logger.Debug("skipping sync reading - just switched to schedule mode",
                        zap.String("room_name", state.RoomName),
                    )
                    c.stateMu.Unlock()
                    continue
                }

                // ... rest of sync logic ...
            }
            c.stateMu.Unlock()
        }
    }
}
```

---

### Solution 4: Increase Schedule Sync Interval (QUICK FIX)

**Approach:** Reduce frequency of schedule sync to avoid triggering false changes.

**Change config.yaml:**
```yaml
thermostatControl:
  # Increase from 15 to 30 or 60 minutes
  scheduleSyncIntervalMinutes: 30  # or 60
```

**Benefits:**
- Immediate fix without code changes
- Reduces API calls

**Drawbacks:**
- Slower response to actual schedule changes
- Doesn't fix root cause

---

## Recommended Implementation Plan

### Phase 1: Immediate Relief
1. ✅ **Increase schedule sync interval to 30 minutes** (config change only)
2. ✅ **Add hysteresis/deadband of 0.3°C** (small code change, high impact)

### Phase 2: Root Cause Fix
3. ✅ **Implement stable baseline offset** (Solution 1)
4. ✅ **Fix schedule sync false positives** (Solution 3)

### Phase 3: Validation
5. Monitor logs for oscillations
6. Verify stable setpoints over 24-hour period
7. Ensure proper response to actual schedule changes

---

## Testing Strategy

1. **Before changes:**
   - Document current oscillation frequency
   - Capture baseline metrics

2. **After Phase 1:**
   - Verify setpoint changes reduce from every 3min to every 30min+
   - Check temperature stays within ±0.5°C of target

3. **After Phase 2:**
   - Verify NO oscillations over 6-hour period
   - Test manual schedule change response time
   - Confirm offset stability in logs

---

## Risk Assessment

**Low Risk Changes:**
- Increase schedule sync interval ✅
- Add hysteresis deadband ✅

**Medium Risk Changes:**
- Baseline offset tracking (requires thorough testing)
- Schedule sync fix (may affect sync timing)

**Mitigation:**
- Keep `dryRun: true` during initial testing
- Monitor logs closely for first 24 hours
- Have rollback plan (git revert)
