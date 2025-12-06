# Design: Intelligent Thermostat Control

## Context

The home-controller service currently monitors temperature via:
- **Xiaomi LYWSD03MMC BLE sensors** (4 sensors): Accurate temperature/humidity readings via passive BLE scanning
- **Netatmo thermostats** (4 thermostats): Climate control with API access for reading status

**Problem**: Netatmo thermostats measure temperature inaccurately (up to 4°C deviation), causing poor heating decisions.

**Solution**: Use Xiaomi sensors as source of truth, automatically adjusting Netatmo setpoints to compensate for measurement error.

**Constraints**:
- 3 Xiaomi sensors for 4 thermostats (one sensor must control two thermostats)
- Netatmo schedule runs on 15-minute intervals (6:00, 6:15, 6:30, etc.)
- Service may crash - need fail-safe rollback to Netatmo's native behavior
- Users may manually override thermostats - algorithm must not fight user input

## Goals / Non-Goals

### Goals
- Accurate climate control using reliable Xiaomi temperature sensors
- Automatic compensation for Netatmo thermostat measurement inaccuracy
- Fail-safe operation with 10-minute auto-expiring overrides
- Configurable temperature thresholds and sensor mappings
- Support for hard override schedules and external modification detection
- Predictable 1-minute control loop for reliable operation

### Non-Goals
- Learning/ML-based temperature prediction
- Multi-zone optimization or energy cost awareness
- Occupancy detection or weather integration
- Real-time reactive control (sub-minute response times)
- Support for non-Netatmo thermostats

## Decisions

### 1. Control Algorithm: Setpoint Compensation

**Decision**: Adjust thermostat setpoint by (XiaomiTemp - ScheduledTemp) to compensate for measurement error.

**Logic**:
```
ScheduledTemp = Netatmo schedule target (e.g., 25°C)
XiaomiTemp = Actual room temperature from sensor (e.g., 24.5°C)
ThermostatMeasured = Netatmo's reported temperature (e.g., 25°C - inaccurate!)

Difference = XiaomiTemp - ScheduledTemp = 24.5 - 25 = -0.5°C

If |Difference| >= Threshold (e.g., 0.5°C):
  NewSetpoint = ThermostatMeasured + Difference
  NewSetpoint = 25 + (-0.5) = 24.5°C

SetRoomThermpoint(room, NewSetpoint, duration=10 minutes, mode=manual)
```

**Example scenarios** (as per user's table):

| Scheduled | Xiaomi | Thermostat Measured | Difference | New Setpoint | Effect |
|-----------|--------|---------------------|------------|--------------|---------|
| 25°C | 24.5°C | 25°C | -0.5°C | 24.5°C | Heat to reach 25°C (Xiaomi will show 25°C when done) |
| 25°C | 25°C | 24°C | 0°C | No change | Within threshold, no action |
| 25°C | 25.5°C | 24°C | +0.5°C | 24.5°C | Turn off heating (room too warm) |
| 25°C | 24.5°C | 28°C | -0.5°C | 27.5°C | Turn off heating (thermostat overheating) |
| 25°C | 25°C | 26°C | 0°C | No change | Within threshold |

**Rationale**:
- Simple, deterministic algorithm (no complex state machines)
- Works regardless of thermostat measurement accuracy
- Directly addresses the core problem: thermostat's bad temperature sensor

**Alternatives considered**:
- ❌ Set setpoint to Xiaomi temperature: Doesn't work because thermostat makes heating decisions based on its own (inaccurate) sensor
- ❌ PID controller: Overcomplicated for this use case; simple offset works

### 2. Fail-Safe: 10-Minute Auto-Expiring Overrides

**Decision**: All setpoint changes use Netatmo's duration-based manual mode (10 minutes), NOT permanent changes.

**Implementation**:
```go
SetRoomThermpoint(ctx, homeID, roomID, temp, mode="manual", endtime=now+10min)
```

**Rationale**:
- If service crashes, thermostats revert to schedule after 10 minutes
- Prevents stuck thermostats in wrong state
- Netatmo API natively supports timed overrides

**Trade-offs**:
- ✅ Safety: Automatic rollback without external intervention
- ❌ API calls: More frequent calls (every 5-10 minutes if temperature persists)
- Mitigated: 5-minute re-check reduces unnecessary API calls

### 3. Re-Evaluation: 5-Minute Check After Adjustment

**Decision**: After setting a 10-minute override, re-check temperature after 5 minutes.

**State tracking**:
```go
type ThermostatState struct {
    LastSetpoint      float64
    LastSetpointTime  time.Time
    NextCheckTime     time.Time  // LastSetpointTime + 5min
}
```

**Flow**:
1. Minute 0: Set override to 24.5°C (expires minute 10)
2. Minutes 1-4: Skip control (NextCheckTime not reached)
3. Minute 5: Re-evaluate temperature
   - If still different: Set new 10-minute override (expires minute 15)
   - If now correct: No action, wait for next minute

**Rationale**:
- Handles persistent temperature differences (slow heating)
- Avoids excessive API calls during stable periods
- Keeps override active while temperature converges

**Alternatives considered**:
- ❌ Check every minute: Too many API calls, fights heating lag
- ❌ Only at 9-minute mark: Risks 1-minute gap if override expires

### 4. Schedule Source: Netatmo API

**Decision**: Fetch scheduled temperature from Netatmo's schedule API.

**API endpoint**: `GET /api/homestatus` returns `therm_setpoint_temperature` and `therm_setpoint_mode`.

**Modes**:
- `schedule`: Use `therm_setpoint_temperature` as scheduled target
- `away`, `hg` (frost guard), etc.: Respect these modes (don't override)
- `manual`: Indicates user/algorithm override

**Implementation**:
```go
// In control loop
status := fetcher.GetHomeStatus(ctx, homeID)
for _, room := range status.Rooms {
    if room.ThermSetpointMode == "schedule" {
        scheduledTemp := room.ThermSetpointTemperature
        // Use this as target for comparison
    }
}
```

**Rationale**:
- Single source of truth: Netatmo manages schedule complexity
- User configures schedule in Netatmo app (familiar UI)
- No need to duplicate schedule logic in config file

**Alternatives considered**:
- ❌ Custom config schedule: Duplicate schedule management, confusing for users
- ❌ Hybrid approach: Added complexity without clear benefit

### 5. External Modification Detection

**Decision**: Track last setpoint command we sent; if Netatmo shows different value, pause algorithm for that room.

**State tracking**:
```go
type ThermostatState struct {
    LastSetpoint      float64      // What we last commanded
    LastSetpointTime  time.Time    // When we sent it
    ExternallyModified bool        // Pause flag
}

// In control loop
current := netatmo.GetRoomStatus(roomID)
if state.LastSetpoint != 0 &&
   time.Since(state.LastSetpointTime) > 2*time.Minute &&
   math.Abs(current.ThermSetpointTemperature - state.LastSetpoint) > 0.1 {
    state.ExternallyModified = true
    logger.Info("Thermostat externally modified, pausing algorithm",
                zap.String("room", roomName))
}
```

**Reset conditions**:
- Thermostat returns to `schedule` mode (user canceled override)
- Configured reset timeout (e.g., 24 hours)
- Manual config flag: `resetExternalModifications: true`

**Rationale**:
- Respects user intent when manually adjusting thermostats
- Prevents algorithm fighting user input
- Simple to implement with state tracking

**Trade-offs**:
- ✅ User-friendly: Algorithm doesn't interfere with manual control
- ❌ Edge case: If user sets exact same temperature we calculated, won't detect
- Mitigated: Unlikely given floating-point precision and user input granularity

### 6. Hard Override Schedules

**Decision**: Allow config-based time windows with explicit temperature targets, taking precedence over algorithm.

**Configuration**:
```yaml
thermostatControl:
  enabled: true
  hardOverrides:
    - roomName: "Living Room"
      schedule:
        - startTime: "06:00"
          endTime: "06:20"
          targetTemperature: 23.0
        - startTime: "22:00"
          endTime: "23:00"
          targetTemperature: 19.0
```

**Precedence order** (highest to lowest):
1. **External modification**: User manually changed thermostat
2. **Hard override schedule**: Config-defined time window active
3. **Algorithm control**: Xiaomi-based compensation
4. **Netatmo schedule**: Default behavior (no override)

**Rationale**:
- Flexibility for special situations (morning warm-up, night cooldown)
- Declarative config (easier to understand than code)
- Clear precedence prevents conflicts

**Alternatives considered**:
- ❌ Modify Netatmo schedule directly: Loses user's original schedule
- ❌ Complex rule engine: Over-engineering for simple time-based overrides

### 7. Control Loop Timing: Cron-Based (Every Minute)

**Decision**: Use 1-minute ticker for control loop, independent of sensor updates.

**Implementation**:
```go
ticker := time.NewTicker(1 * time.Minute)
for {
    select {
    case <-ticker.C:
        RunControlLoop(ctx)
    case <-ctx.Done():
        return
    }
}
```

**Rationale**:
- **Predictable**: Always runs at known interval
- **Reliable**: Not dependent on BLE sensor updates (which may fail)
- **Simple**: No event coordination logic
- **Adequate**: 1-minute response time sufficient for heating (thermal mass lag >> 1 minute)

**Alternatives considered**:
- ❌ Event-driven on sensor update: Complex coordination, failure if sensor offline
- ❌ Hybrid approach: Added complexity, minimal benefit for this use case

### 8. Configuration Structure

**Decision**: Add `thermostatControl` section to existing config.yaml.

**Schema**:
```yaml
thermostatControl:
  enabled: true  # Feature flag
  temperatureThreshold: 0.5  # Minimum °C difference to trigger action
  controlIntervalSeconds: 60  # Control loop frequency
  overrideDurationMinutes: 10  # How long each setpoint override lasts
  recheckDelayMinutes: 5  # Re-evaluate after this delay
  externalModificationResetMinutes: 5  # Auto-reset external mod detection

  mappings:  # Which Xiaomi sensor controls which thermostat
    - roomName: "Living Room"  # Must match Netatmo room name
      sensorMAC: "A4:C1:38:XX:XX:XX"  # Xiaomi sensor MAC address
    - roomName: "Bedroom"
      sensorMAC: "A4:C1:38:YY:YY:YY"
    - roomName: "Kitchen"
      sensorMAC: "A4:C1:38:ZZ:ZZ:ZZ"
    - roomName: "Office"
      sensorMAC: "A4:C1:38:ZZ:ZZ:ZZ"  # Shared sensor with Kitchen

  hardOverrides:  # Optional time-based temperature targets
    - roomName: "Living Room"
      schedule:
        - startTime: "06:00"
          endTime: "06:20"
          targetTemperature: 23.0
```

**Validation rules**:
- All `roomName` values must exist in Netatmo home
- All `sensorMAC` values must be configured in `ble.sensors`
- `temperatureThreshold` >= 0.1°C and <= 5.0°C
- `recheckDelayMinutes` < `overrideDurationMinutes`
- Hard override times must be valid HH:MM format
- Hard override `startTime` < `endTime`

**Rationale**:
- Self-documenting with clear field names
- Reuses existing Netatmo room names (no ID mapping needed)
- Uses MAC addresses from existing BLE sensor config
- Centralized validation catches config errors at startup

## Architecture

### Concurrency Model

**Thread Safety Requirements**:
- Control loop runs in **dedicated goroutine** (separate from BLE scanner, Netatmo poller, metrics pusher)
- All shared data structures MUST be **thread-safe** using mutexes
- All operations MUST be **non-blocking** (no locks held across I/O or network calls)
- Ring buffer reads MUST NOT block control loop (already thread-safe in existing implementation)

**Goroutine Structure**:
```
main.go
├── BLE Scanner Goroutine         (existing, writes to ring buffer)
├── Netatmo Poller Goroutine      (existing, reads Netatmo status)
├── Metrics Pusher Goroutine      (existing, pushes to Prometheus)
└── NEW: Control Loop Goroutine   (reads buffer, calls Netatmo write API)
```

**No shared mutable state** between goroutines except:
- Metrics ring buffer (thread-safe, used by metrics pusher with GetAllAndClear)
- **NEW: Control ring buffer** (thread-safe, dedicated to control loop, separate from metrics)
- Thermostat state store (NEW, needs mutex)

**Critical: Dual Buffer Architecture**

**Problem**: Metrics pusher clears buffer every 15s, but control loop needs 60s of history.

**Solution**: Use **two separate ring buffers** with different purposes:

1. **Metrics Buffer** (existing):
   - Used by: BLE scanner, Netatmo poller, Power scraper → Metrics pusher
   - Capacity: 100K readings (configured via `prometheus.bufferSize`)
   - Lifecycle: Filled by data sources, cleared by metrics pusher every 15s via `GetAllAndClear()`
   - Purpose: Accumulate readings for Prometheus push, then discard

2. **Control Buffer** (NEW):
   - Used by: BLE scanner (writes) → Control loop (reads)
   - Capacity: 10K readings (sufficient for ~2.5 hours at 1 reading/sec × 4 sensors)
   - Lifecycle: Filled by BLE scanner, naturally overflows (ring buffer), NEVER cleared
   - Purpose: Retain 60+ seconds of BLE temperature history for weighted average calculation
   - **Only stores BLE readings** (control doesn't need Netatmo/Power data)

**Data Flow**:
```
BLE Scanner → writes to BOTH buffers:
  ├─> Metrics Buffer (for Prometheus)
  └─> Control Buffer (for thermostat control, 60s history)

Netatmo Poller → writes to Metrics Buffer only
Power Scraper → writes to Metrics Buffer only

Metrics Pusher: GetAllAndClear(metricsBuffer) every 15s
Control Loop: GetReadingsByTimeWindow(controlBuffer, now-60s, now) every 60s
```

**Why This Works**:
- **Simple**: No state tracking, no timestamp coordination needed
- **Clean separation**: Metrics and control don't interfere
- **Efficient**: Control buffer is smaller (10K vs 100K), only BLE data
- **Guaranteed 60s**: Control buffer never cleared, ring overflow handles cleanup
- **No code changes to metrics pusher**: Continues using GetAllAndClear() as today

### Component Overview with Dual Buffers

```
┌─────────────────────────────────────────────────────────────┐
│  Main Orchestrator (main.go)                                 │
│  - Creates TWO ring buffers: metrics (100K) + control (10K)  │
│  - Launches 4 independent goroutines                         │
└─────────────────────────────────────────────────────────────┘
         │
         ├──────────────┬──────────────┬──────────────┬──────────────┐
         ▼              ▼              ▼              ▼              ▼
┌──────────────┐ ┌──────────────┐ ┌──────────────┐ ┌──────────────┐ ┌──────────────┐
│ BLE Scanner  │ │ Netatmo      │ │ Power        │ │ NEW:         │ │ Metrics      │
│              │ │ Poller       │ │ Scraper      │ │ Control Loop │ │ Pusher       │
│ Writes to:   │ │ Writes to:   │ │ Writes to:   │ │ Reads from:  │ │ Reads from:  │
│ • Metrics    │ │ • Metrics    │ │ • Metrics    │ │ • Control    │ │ • Metrics    │
│   Buffer     │ │   Buffer     │ │   Buffer     │ │   Buffer     │ │   Buffer     │
│ • Control    │ │   (only)     │ │   (only)     │ │   (only)     │ │   (only)     │
│   Buffer     │ │              │ │              │ │              │ │              │
│ (double      │ │              │ │              │ │ - GetReadings│ │ - GetAllAnd  │
│  write)      │ │              │ │              │ │   ByTime     │ │   Clear()    │
│              │ │              │ │              │ │   Window()   │ │              │
└──────┬───────┘ └──────┬───────┘ └──────┬───────┘ │ - Weighted   │ └──────┬───────┘
       │                │                │         │   average    │        │
       │                │                │         │ - Netatmo    │        │
       │                │                │         │   write API  │        │
       ▼                ▼                ▼         └──────┬───────┘        ▼
┌──────────────────────────────────────────────┐         │         ┌──────────────┐
│        METRICS BUFFER (100K capacity)        │         │         │ Prometheus   │
│  Thread-safe ring buffer                     │         │         │ Remote Write │
│  - BLE readings                              │         │         │              │
│  - Netatmo readings                          │         │         │ Every 15s    │
│  - Power readings                            │         │         └──────────────┘
│  - CLEARED every 15s by metrics pusher       │         │
└──────────────────────────────────────────────┘         │
                                                          │
┌──────────────────────────────────────────────┐         │
│        CONTROL BUFFER (10K capacity)         │         │
│  Thread-safe ring buffer                     │         │
│  - BLE readings ONLY                         │         │
│  - NEVER cleared (ring overflow only)        │◄────────┘
│  - Retains 60+ seconds of history            │
└──────────────────────────────────────────────┘
                                  │
                                  ▼
                           ┌──────────────┐
                           │ State Store  │
                           │ (in-memory)  │
                           │ sync.RWMutex │
                           │ - Last cmds  │
                           │ - Next check │
                           │ - Ext. mod   │
                           └──────────────┘
```

### Detailed Component Overview

```
┌─────────────────────────────────────────────────────────────┐
│  Main Orchestrator (main.go)                                 │
│  - Existing: BLE scanner, Netatmo poller, metrics pusher     │
│  - NEW: Control loop goroutine (1-minute ticker)             │
│  - All goroutines are independent and non-blocking           │
└─────────────────────────────────────────────────────────────┘
         │
         ├──────────────┬──────────────┬──────────────┐
         ▼              ▼              ▼              ▼
┌──────────────┐ ┌──────────────┐ ┌──────────────┐ ┌──────────────┐
│ BLE Scanner  │ │ Netatmo      │ │ NEW:         │ │ Ring Buffer  │
│ (unchanged)  │ │ Poller       │ │ Control Loop │ │ (unchanged)  │
│              │ │ (MODIFIED)   │ │ (control/)   │ │              │
│ - Passive    │ │ - Fetch      │ │ - Read       │ │ - Thread-    │
│   scan       │ │   status     │ │   latest     │ │   safe       │
│ - Decode     │ │ - NEW: Fetch │ │   temps      │ │ - Mutex      │
│   ATC        │ │   schedule   │ │ - Compare    │ │   protected  │
│ - Push to    │ │ - NEW: Set   │ │   to sched   │ │ - Stores     │
│   buffer     │ │   setpoint   │ │ - Calc new   │ │   readings   │
│              │ │   (write)    │ │   setpoint   │ │              │
└──────────────┘ └──────────────┘ │ - Call       │ └──────────────┘
                                  │   Netatmo    │
                                  │   write API  │
                                  └──────────────┘
                                         │
                                         ▼
                                  ┌──────────────┐
                                  │ State Store  │
                                  │ (in-memory)  │
                                  │ - sync.RWMutex│
                                  │ - Last cmds  │
                                  │ - Next check │
                                  │ - Ext. mod   │
                                  └──────────────┘
```

### Thread Safety Implementation

**State Store Structure**:
```go
type ControllerState struct {
    mu     sync.RWMutex  // Protects all state maps
    states map[string]*ThermostatState  // key: roomID
}

type ThermostatState struct {
    LastSetpoint      float64
    LastSetpointTime  time.Time
    NextCheckTime     time.Time
    ExternallyModified bool
}

// Thread-safe read
func (cs *ControllerState) GetState(roomID string) *ThermostatState {
    cs.mu.RLock()
    defer cs.mu.RUnlock()
    // Return copy to avoid race conditions
    if state, exists := cs.states[roomID]; exists {
        copy := *state
        return &copy
    }
    return nil
}

// Thread-safe write
func (cs *ControllerState) UpdateState(roomID string, state *ThermostatState) {
    cs.mu.Lock()
    defer cs.mu.Unlock()
    cs.states[roomID] = state
}
```

**Non-Blocking Guarantees**:
1. **Ring buffer reads**: Use NEW thread-safe `GetReadingsByTimeWindow()` (non-destructive), no blocking
2. **State reads**: Use `RLock()` for read-only access (allows concurrent reads)
3. **State writes**: Use `Lock()` only for brief updates (no I/O under lock)
4. **Netatmo API calls**: NEVER hold mutex during network I/O
5. **Control loop**: Each iteration is independent, no blocking between cycles
6. **Buffer isolation**: Control loop uses `GetReadingsByTimeWindow()`, metrics pusher uses `GetAllAndClear()` - no interference

**Lock Ordering** (to prevent deadlocks):
- Only one mutex in control package (ControllerState.mu)
- Ring buffer has its own independent mutex
- No nested locks required

### Data Flow

**Control Loop Cycle** (every 1 minute):

1. **Read Netatmo Status**:
   ```go
   status := netatmo.GetHomeStatus(ctx, homeID)
   ```
   Get current setpoint, mode, scheduled temp for each room.

2. **Read Xiaomi Temperatures from Last 60 Seconds**:
   ```go
   // Non-destructive read with time filtering - does NOT clear buffer
   cutoffTime := time.Now().Add(-60 * time.Second)
   now := time.Now()
   recentReadings := buffer.GetReadingsByTimeWindow(cutoffTime, now)

   // Calculate WEIGHTED average per sensor (recent readings weighted higher)
   avgTemps := calculateWeightedAverageTemperaturesBySensor(recentReadings, now)
   ```
   Use new buffer method to get only readings from last 60 seconds (non-destructive), then calculate weighted average per sensor.

3. **For each thermostat**:
   ```go
   for _, mapping := range config.Mappings {
       // a) Check if external modification detected
       if state.ExternallyModified {
           continue  // Skip this room
       }

       // b) Check hard override schedule
       override := getHardOverride(mapping.RoomName, now)
       if override != nil {
           targetTemp = override.TargetTemperature
       } else {
           // c) Use Netatmo scheduled temperature
           targetTemp = netatmoRoom.ThermSetpointTemperature
       }

       // d) Get Xiaomi sensor average temperature from last 60 seconds
       xiaomiTemp := getAverageTemperatureLastMinute(mapping.SensorMAC)

       // e) Calculate difference
       diff := xiaomiTemp - targetTemp

       // f) Check if action needed
       if abs(diff) >= config.TemperatureThreshold {
           // g) Calculate new setpoint
           newSetpoint := netatmoRoom.ThermMeasuredTemperature + diff

           // h) Send command to Netatmo
           netatmo.SetRoomThermpoint(ctx,
               homeID, mapping.RoomID,
               newSetpoint, "manual",
               time.Now().Add(10*time.Minute))

           // i) Update state
           state.LastSetpoint = newSetpoint
           state.LastSetpointTime = now
           state.NextCheckTime = now.Add(5*time.Minute)
       }
   }
   ```

4. **Detect External Modifications**:
   ```go
   // Check if setpoint changed unexpectedly
   if time.Since(state.LastSetpointTime) > 2*time.Minute {
       if abs(netatmoRoom.ThermSetpointTemperature - state.LastSetpoint) > 0.1 {
           state.ExternallyModified = true
       }
   }
   ```

### Error Handling

**Netatmo API failures**:
- Log error and continue to next room
- Retry on next control loop iteration (1 minute later)
- Do not clear state (preserves last known good values)

**Missing sensor data**:
- Check if no readings in last 60 seconds (sensor offline or BLE scan issues)
- Log warning and skip that thermostat
- Prevents acting on stale data
- Requires at least one reading in the 60-second window

**Configuration errors**:
- Validate at startup (before entering control loop)
- Fail-fast if room names don't exist in Netatmo
- Fail-fast if sensor MACs not configured

**State corruption**:
- Use atomic operations for state updates
- Consider periodic state reset (daily?) to prevent drift
- Log state changes for debugging

## Risks / Trade-offs

### Risk 1: API Rate Limits
- **Risk**: Netatmo may rate-limit write API calls
- **Impact**: Setpoint changes fail, heating not controlled
- **Mitigation**:
  - 5-minute re-check delay reduces call frequency
  - Only call when temperature difference exceeds threshold
  - Monitor API response codes, back off on 429 errors
- **Likelihood**: Low (1 call per minute per room = 4 calls/min total)

### Risk 2: Sensor-Thermostat Drift
- **Risk**: Xiaomi sensor and Netatmo in different locations, measuring different microclimates
- **Impact**: Algorithm compensates for wrong temperature, over/under-heats room
- **Mitigation**:
  - User configures sensor-to-thermostat mapping (knows room layout)
  - Temperature threshold prevents tiny adjustments
  - External modification detection lets user override
- **Likelihood**: Medium (depends on sensor placement)

### Risk 3: Override Expiration Gaps
- **Risk**: 10-minute override expires, but service hasn't re-evaluated yet
- **Impact**: 1-minute window where thermostat reverts to schedule (incorrect temperature)
- **Mitigation**: 5-minute re-check ensures overlap (override at 0min and 5min)
- **Likelihood**: Low (mitigated by design)

### Risk 4: State Loss on Restart
- **Risk**: Service crashes, loses in-memory state (last setpoints, external mod flags)
- **Impact**: May re-trigger overrides immediately after restart, or miss external modifications
- **Mitigation**:
  - State reset timeout (24 hours) limits impact
  - Can add persistent state file in future if needed
- **Likelihood**: Low (rare crashes, bounded impact)

### Risk 5: Race Conditions and Deadlocks
- **Risk**: Concurrent access to shared state causes data races or deadlocks
- **Impact**: Corrupted state, service hangs, incorrect control decisions
- **Mitigation**:
  - Use `sync.RWMutex` for all state access
  - Never hold locks during I/O operations
  - Return copies of state structs to prevent external mutation
  - Single mutex design (no nested locks, no deadlock possible)
  - Run with `-race` flag in tests to detect data races
- **Likelihood**: Low (prevented by design)

### Trade-off: Complexity vs. Accuracy
- **Choice**: Simple offset algorithm vs. PID/ML
- **Decision**: Simple offset
- **Rationale**:
  - ✅ Easy to understand, debug, and explain
  - ✅ Predictable behavior
  - ❌ Doesn't model heating dynamics
- **Acceptable**: Heating thermal mass >> 1-minute control loop, simple works

### Trade-off: API Calls vs. Responsiveness
- **Choice**: 1-minute control loop vs. real-time reactive
- **Decision**: 1-minute timer
- **Rationale**:
  - ✅ Reduces API calls (important for rate limits)
  - ✅ Heating dynamics are slow (minutes to hours)
  - ❌ Max 1-minute lag to react to temperature changes
- **Acceptable**: 1-minute response time adequate for climate control

## Migration Plan

### Phase 1: Implementation (No User Impact)
1. Add Netatmo write API methods (SetRoomThermpoint)
2. Add control loop package with algorithm logic
3. Add configuration schema and validation
4. Feature flag: `thermostatControl.enabled: false` (default off)

### Phase 2: Testing (Opt-In)
1. Enable on single thermostat: `enabled: true`, configure one mapping
2. Monitor logs for API errors, state transitions
3. Verify 10-minute auto-expiration works (manually trigger by stopping service)
4. Test external modification detection (manually adjust thermostat)
5. Validate hard override schedules

### Phase 3: Gradual Rollout
1. Enable for all thermostats
2. Monitor Grafana metrics: setpoint changes, temperature differences
3. Adjust `temperatureThreshold` based on observed behavior
4. Fine-tune `recheckDelayMinutes` if needed

### Rollback Plan
- Set `thermostatControl.enabled: false` in config
- Restart service
- All thermostats revert to Netatmo schedule (10 minutes max delay)

### Monitoring
- **Metrics to track**:
  - `thermostat_setpoint_changes_total{room}`: Count of API calls
  - `thermostat_temperature_difference{room}`: |Xiaomi - Scheduled|
  - `thermostat_external_modification{room}`: Boolean flag
  - `thermostat_api_errors_total{room, error_code}`: API failures
- **Logs to watch**:
  - Setpoint changes with before/after values
  - External modification detections
  - API errors (rate limits, auth failures)

## Open Questions

1. **Persistent state storage**: Should we save state to disk to survive restarts?
   - Leaning: No, start with in-memory, add later if needed

2. **API rate limit specifics**: What are Netatmo's actual rate limits for write API?
   - Action: Test and document, implement backoff if needed

3. **Sensor reading age threshold**: How old can a Xiaomi reading be before we consider it stale?
   - Decision: 60 seconds - control algorithm uses weighted average of all readings from last minute
   - If no readings in last 60 seconds, skip that thermostat for this cycle
   - Weighting: Linear decay from now (weight=1.0) to 60 seconds ago (weight=0.0)
   - Formula: `weight = (timestamp - cutoffTime) / (now - cutoffTime)`

4. **Weighted vs simple average**: Why weighted average?
   - More recent readings reflect current room state better
   - Reduces lag from stale readings
   - Smooth transition as old readings age out
   - Example: Reading at t=59s ago gets weight=0.02, reading at t=1s ago gets weight=0.98

5. **External modification auto-reset**: Should it be time-based (24h) or mode-based (return to schedule)?
   - Leaning: Both - reset if thermostat returns to schedule mode OR 24h timeout
