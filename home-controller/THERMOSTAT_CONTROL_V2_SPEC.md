# Thermostat Control Algorithm V2 - Specification

## Overview

This specification defines a new thermostat control algorithm that eliminates the override extension complexity by:
1. Tracking the original schedule temperature throughout manual mode
2. Setting override duration to match the schedule end time (not fixed duration)
3. Using aggressive heating when thermostat reaches setpoint but room is still cold
4. Properly handling manual mode takeover for externally-set overrides

## Problems with V1 (Current)

1. **Lost schedule context**: When thermostat is in manual mode (our override), we can't read the underlying schedule from Netatmo API
2. **Extension complexity**: Had to track when overrides expire and extend them, causing unnecessary logic
3. **Comparison against override**: In manual mode, compared against our own override value instead of original schedule
4. **Brief mode switches**: Override expiration caused thermostat to switch to schedule mode briefly before new override

## V2 Solution

### Core Principle
**Track the original schedule temperature when entering manual mode, use it for all control decisions until schedule period ends.**

### State Tracking

Add to `ThermostatState`:
```go
OriginalScheduledTemp    float64   // Original schedule temperature when override was sent
ScheduleEndTime          time.Time // When the current schedule period ends
ManualModeSince          time.Time // When thermostat entered manual mode (for takeover detection)
```

### Configuration Changes

Remove:
- `overrideDurationMinutes` (10 min) - no longer needed
- `extensionThresholdMinutes` (2 min) - no longer needed

Add:
- `manualModeTakeoverMinutes` (default: 60) - how long manual mode must persist before taking control

Keep:
- `temperatureThreshold` (0.2°C) - minimum difference to trigger action
- `controlIntervalSeconds` (60s) - how often to evaluate
- `externalModificationResetMinutes` (5 min) - for detecting user changes (unused currently)
- `minSetpointCelsius` / `maxSetpointCelsius` - safety limits

### Control Algorithm

#### Phase 1: Determine Scheduled Temperature

**Case 1: Hard Override Active**
```
scheduledTemp = hard override temperature
scheduleEndTime = current time + 24 hours
```

**Case 2: Thermostat in Schedule Mode**
```
scheduledTemp = roomStatus.ThermSetpointTemperature
scheduleEndTime = time.Unix(roomStatus.ThermSetpointEndTime, 0)
```

**Case 3: Thermostat in Manual Mode - Our Control**
```
if state.OriginalScheduledTemp > 0:
    scheduledTemp = state.OriginalScheduledTemp (use stored)
    scheduleEndTime = state.ScheduleEndTime (use stored)
else:
    goto Case 4 (manual mode takeover)
```

**Case 4: Thermostat in Manual Mode - External/Unknown**
```
if state.ManualModeSince.IsZero():
    # First time seeing manual mode
    state.ManualModeSince = now()
    return SKIP (wait to see if it's user's manual change)

if time.Since(state.ManualModeSince) < manualModeTakeoverMinutes:
    return SKIP (waiting for takeover threshold)

# Take over control
log "taking over manual mode"
scheduledTemp = roomStatus.ThermSetpointTemperature (use current as baseline)
scheduleEndTime = now() + 1 hour (default)
```

#### Phase 2: Check External Modification

**If state.ExternallyModified == true:**
```
if roomStatus.ThermSetpointMode == "schedule":
    log "external modification cleared"
    clearExternalModification()
    state.ManualModeSince = zero
    continue evaluation
else:
    return SKIP (respect user's manual override)
```

#### Phase 3: Calculate Action

**Get values:**
```
xiaomiTemp = weighted average from BLE sensor (60s window)
scheduledTemp = from Phase 1
thermostatMeasured = roomStatus.ThermMeasuredTemperature
currentSetpoint = roomStatus.ThermSetpointTemperature
tempDiff = xiaomiTemp - scheduledTemp
```

**Decision Tree:**

**CASE A: Room Too Warm**
```
if tempDiff > temperatureThreshold:
    ACTION: cancel_override
    calculatedSetpoint = scheduledTemp
    overrideEndTime = now() + 1 minute
    reason = "room too warm: xiaomi={xiaomi}°C > scheduled={scheduled}°C"
```

**CASE B: Temperature Within Threshold (Maintain)**
```
if |tempDiff| <= temperatureThreshold:
    ACTION: set_manual_override
    calculatedSetpoint = thermostatMeasured (maintain current)
    overrideEndTime = scheduleEndTime
    reason = "temperature OK: xiaomi={xiaomi}°C ≈ scheduled={scheduled}°C, maintaining"
```

**CASE C: Room Too Cold - Aggressive Heating**
```
if tempDiff < -temperatureThreshold:
    if |currentSetpoint - thermostatMeasured| < 0.1:
        # Thermostat reached setpoint but room still cold - raise aggressively
        ACTION: set_manual_override
        calculatedSetpoint = currentSetpoint + 0.5
        overrideEndTime = scheduleEndTime
        reason = "aggressive heating: setpoint reached but room cold, raising by +0.5°C"
    else:
        # Normal heating - calculate compensation
        ACTION: set_manual_override
        calculatedSetpoint = max(scheduledTemp, thermostatMeasured + 0.5)
        overrideEndTime = scheduleEndTime
        reason = "heating: xiaomi={xiaomi}°C < scheduled={scheduled}°C"
```

**Apply Safety Limits:**
```
calculatedSetpoint = clamp(calculatedSetpoint, minSetpointCelsius, maxSetpointCelsius)
```

#### Phase 4: Execute Decision

**For set_manual_override or cancel_override:**

1. **Call Netatmo API:**
```go
SetRoomThermpoint(
    homeID,
    roomID,
    mode: "manual",
    temp: calculatedSetpoint,
    endtime: overrideEndTime,
)
```

2. **Update State:**
```go
state.LastSetpoint = calculatedSetpoint
state.LastSetpointTime = now()
state.OverrideEndTime = time.Unix(overrideEndTime, 0)
state.OriginalScheduledTemp = scheduledTemp (store for next iteration)
state.ScheduleEndTime = scheduleEndTime (store for next iteration)
state.ManualModeSince = now() if entering manual, else keep
```

3. **Detect External Modification:**
```
if we sent a command before AND
   time since > 2 minutes AND
   thermostat still in manual mode AND
   current setpoint != last setpoint we sent:

   state.ExternallyModified = true
   return SKIP
```

## Examples

### Example 1: Normal Heating Cycle

**Initial State:**
- Thermostat in schedule mode
- Schedule: 24°C until 17:00 (currently 14:00)
- Xiaomi reads: 23.0°C
- Thermostat reads: 25.5°C (2.5°C offset)

**Iteration 1 (14:00):**
```
scheduledTemp = 24.0°C (from schedule mode)
scheduleEndTime = 17:00
tempDiff = 23.0 - 24.0 = -1.0°C (room cold)
|currentSetpoint - thermostatMeasured| = |24.0 - 25.5| = 1.5°C (not reached)

ACTION: set_manual_override
calculatedSetpoint = max(24.0, 25.5 + 0.5) = 26.0°C
overrideEndTime = 17:00

Store: OriginalScheduledTemp=24.0, ScheduleEndTime=17:00
```

**Iteration 2 (14:01):**
```
thermostat now in manual mode at 26.0°C
scheduledTemp = 24.0°C (from stored OriginalScheduledTemp)
xiaomiTemp = 23.2°C
thermostatMeasured = 25.8°C
tempDiff = 23.2 - 24.0 = -0.8°C (room still cold)
|currentSetpoint - thermostatMeasured| = |26.0 - 25.8| = 0.2°C (still heating)

ACTION: set_manual_override
calculatedSetpoint = max(24.0, 25.8 + 0.5) = 26.3°C
overrideEndTime = 17:00
```

**Iteration 3 (14:10 - thermostat reached setpoint):**
```
thermostatMeasured = 26.2°C
xiaomiTemp = 23.8°C (room still cold)
currentSetpoint = 26.3°C
|currentSetpoint - thermostatMeasured| = |26.3 - 26.2| = 0.1°C (reached!)

ACTION: set_manual_override (aggressive)
calculatedSetpoint = 26.3 + 0.5 = 26.8°C
reason = "aggressive heating: setpoint reached but room cold"
```

**Iteration 10 (14:45 - room reaches target):**
```
xiaomiTemp = 24.1°C
scheduledTemp = 24.0°C
tempDiff = 0.1°C (within threshold)

ACTION: set_manual_override (maintain)
calculatedSetpoint = 26.8°C (current thermostat reading)
reason = "temperature OK, maintaining"
```

**At 17:00:**
```
overrideEndTime expires
thermostat returns to schedule mode
next iteration reads new schedule (e.g., 22°C evening temp)
```

### Example 2: Manual Mode Takeover

**Initial State:**
- User manually set thermostat to 26°C at 10:00
- System starts at 11:30
- manualModeTakeoverMinutes = 60

**Iteration 1 (11:30):**
```
thermostat in manual mode at 26°C
state.OriginalScheduledTemp = 0 (no stored value)
state.ManualModeSince = zero

ACTION: SKIP
state.ManualModeSince = 11:30
reason = "manual mode detected, tracking duration"
```

**Iteration 2-59 (11:31-12:29):**
```
time.Since(ManualModeSince) = 1-59 minutes

ACTION: SKIP
reason = "manual mode for X min, waiting for 60 min to takeover"
```

**Iteration 60 (12:30):**
```
time.Since(ManualModeSince) = 60 minutes

Log: "taking over manual mode thermostat"
scheduledTemp = 26°C (use current as baseline)
scheduleEndTime = 13:30
xiaomiTemp = 25.5°C
tempDiff = 25.5 - 26.0 = -0.5°C

ACTION: set_manual_override
calculatedSetpoint = max(26.0, thermostatMeasured + 0.5)
Store: OriginalScheduledTemp=26.0, ScheduleEndTime=13:30
```

### Example 3: External Modification Detection

**State:**
- We sent setpoint=26.0°C at 14:00
- User changes to 28.0°C at 14:05

**Iteration at 14:07:**
```
time.Since(LastSetpointTime) = 7 minutes
thermostat in manual mode
currentSetpoint = 28.0°C
state.LastSetpoint = 26.0°C
delta = |28.0 - 26.0| = 2.0°C > 0.1°C

Log: "external modification detected"
state.ExternallyModified = true

ACTION: SKIP
reason = "external modification detected"
```

**Later - User switches back to schedule:**
```
thermostat mode = "schedule"

Log: "external modification cleared"
state.ExternallyModified = false
state.ManualModeSince = zero
Continue normal evaluation
```

## Migration from V1 to V2

### Breaking Changes

1. **Configuration:**
   - Remove `overrideDurationMinutes` from config.yaml
   - Remove `extensionThresholdMinutes` from config.yaml
   - Add `manualModeTakeoverMinutes: 60` to config.yaml

2. **State Fields:**
   - `OverrideEndTime` still exists but now set to schedule end time
   - Add `OriginalScheduledTemp` field
   - Add `ScheduleEndTime` field
   - Add `ManualModeSince` field

3. **Decision Actions:**
   - Add new action: `cancel_override` (for room too warm)
   - Keep `set_manual_override` (for heating and maintaining)
   - Keep `skip` (for external modification, waiting for takeover)
   - Remove `no_adjustment_needed` (replaced by maintain logic in set_manual_override)

### Log Message Changes

**Before (V1):**
```
"extending override even though setpoint matches schedule"
"(extending, 1m left)"
```

**After (V2):**
```
"taking over manual mode thermostat"
"aggressive heating: setpoint reached but room cold, raising by +0.5°C"
"temperature OK: xiaomi=24.1°C ≈ scheduled=24.0°C, maintaining"
"room too warm: xiaomi=25.5°C > scheduled=24.0°C"
```

### Testing Requirements

1. **Test aggressive heating:**
   - setpoint == thermostat_measured AND room cold → raises by 0.5°C
   - Verify it raises every minute until room warms up

2. **Test maintain mode:**
   - |xiaomi - scheduled| <= threshold → sets to thermostat_measured
   - Doesn't change setpoint unnecessarily

3. **Test cancel override:**
   - xiaomi > scheduled + threshold → sets endtime to 1 minute
   - Thermostat returns to schedule mode quickly

4. **Test manual mode takeover:**
   - Manual mode < 60 min → skips
   - Manual mode >= 60 min → takes control
   - Uses current setpoint as baseline

5. **Test schedule tracking:**
   - Enters manual mode from schedule → stores original schedule
   - Uses stored schedule for all comparisons until schedule end time
   - At schedule end, thermostat returns to schedule, reads new values

6. **Test external modification:**
   - User changes setpoint → detects and pauses
   - User switches to schedule → resumes control
   - Clears ManualModeSince when resuming

## Implementation Checklist

- [x] Update `ThermostatState` with new fields
- [x] Remove `overrideDurationMinutes` and `extensionThresholdMinutes` from config
- [x] Add `manualModeTakeoverMinutes` to config
- [x] Rewrite `evaluateRoom()` with new algorithm
- [ ] Update `executeDecision()` to:
  - Handle `cancel_override` action
  - Store `OriginalScheduledTemp`, `ScheduleEndTime`, `ManualModeSince`
  - Update external modification detection
- [ ] Update tests:
  - Remove extension tests
  - Add aggressive heating tests
  - Add maintain mode tests
  - Add cancel override tests
  - Add manual takeover tests
  - Add schedule tracking tests
- [ ] Update config.yaml with new parameters
- [ ] Update CLAUDE.md documentation
- [ ] Update config usage notes

## Open Questions

1. **Schedule end time fallback:** If Netatmo doesn't provide `ThermSetpointEndTime`, use 1 hour as default?
   - **Answer:** Yes, use 1 hour as safe default

2. **Manual mode tracking reset:** When should we reset `ManualModeSince`?
   - **Answer:** Reset when thermostat switches to schedule mode OR when we take control

3. **Aggressive heating limit:** Should we limit how many times we raise by 0.5°C?
   - **Answer:** No limit, trust safety bounds (maxSetpointCelsius)

4. **Cancel override:** Should we set mode to "schedule" or just expire override?
   - **Answer:** Set endtime to 1 minute in manual mode, thermostat returns to schedule naturally
