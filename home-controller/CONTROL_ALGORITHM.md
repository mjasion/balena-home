# Thermostat Control Algorithm

## Overview

The thermostat control system uses **Xiaomi BLE temperature sensors** as the authoritative source for room temperature, while delegating actual heating control to **Netatmo thermostats**. The algorithm monitors temperature deviations and only intervenes when necessary.

## Key Principles

1. **Xiaomi sensors are authoritative** - Better placement than built-in thermostat sensors
2. **Netatmo schedule is respected** - The system reads the current scheduled temperature
3. **Minimal intervention** - Only override when temperature differs significantly from target
4. **Fail-safe design** - All overrides auto-expire after 10 minutes
5. **User intent is paramount** - Manual user changes are detected and respected

## Recent Implementation Changes

This document reflects the current implementation as of recent commits. Key changes include:

### Architecture Improvements (commit 963c5a9)
- **Separated jobs**: Home status fetching (runs at :00) and control loop (runs at :30) are now separate jobs
- **Cached status**: Control loop uses cached home status from fetch job (<30s old)
- **Enhanced mode detection**: Three-layer detection system for external changes:
  - `detectHomeModeChange()`: Detects away/hg mode transitions, resets state
  - `detectExternalManualChange()`: Detects user manual changes with 2-minute grace period
  - `shouldControlRoom()`: Decides control eligibility (15-min window, ±0.3°C tolerance)

### Schedule Sync Improvements (commit 28950f3, 0d90441)
- **Cron-like timing**: Syncs at specific minutes (e.g., :00, :15, :30, :45 for 15-min interval)
- **User-set manual mode protection**: Distinguishes algorithm-set vs user-set manual modes
  - Algorithm overrides: Recent (<15 min) + matching setpoint (±0.3°C) → reset during sync
  - User manual modes: Never reset during sync, respects user intent
- **Smart eligibility**: Only switches rooms that are algorithm-controlled or in schedule/away/hg modes

### State Tracking Enhancements
- New fields in `ThermostatState`:
  - `LastHomeMode`: Tracks mode transitions (away, hg, etc.)
  - `LastManualSetpoint`: Baseline for detecting external changes
  - `LastManualEndTime`: Baseline for detecting override duration changes

### Implementation Files
- `control/evaluate.go`: Core evaluation logic with three-zone control
- `control/mode_detection.go`: New mode detection functions
- `control/sync.go`: Schedule sync with user mode protection
- `control/execute.go`: Execution with state tracking
- `control/home_status_fetcher.go`: Separate home status fetching job

## Temperature Sources

The system monitors **two temperature sources** but only one drives control decisions:

### 1. Xiaomi BLE Sensors (Authoritative - Used for Control)
- **Type**: LYWSD03MMC with ATC firmware
- **Usage**: Primary input for all control decisions
- **Rationale**: Better placement than built-in thermostat sensors (typically mounted at optimal height and location in the room)
- **Processing**: Weighted average over 60-second window (see "Sensor Data Processing" section)
- **Accuracy**: Recent readings weighted more heavily than older ones

### 2. Netatmo Built-in Sensors (Monitoring Only - NOT Used for Control)
- **Type**: Built-in temperature sensor on Netatmo thermostat
- **Usage**: Captured for logging, monitoring, and metrics only
- **NOT used for control**: Only Xiaomi sensors drive temperature-based decisions
- **Known Issue**: Netatmo sensors are **systematically inaccurate**, often reading 2-3°C higher than actual room temperature
- **Purpose**:
  - Comparison and validation (detecting sensor drift)
  - Debugging and troubleshooting
  - Historical analysis and metrics
  - Future enhancement opportunities

**Why Two Sources?**

The Netatmo thermostat's built-in sensor is read from the API (`ThermMeasuredTemperature`) but intentionally **not used** for control because:
- **Hardware inaccuracy**: Netatmo sensors consistently read higher than actual temperature (known defect)
- Thermostats are often mounted near doors, corners, or heat sources (poor placement)
- Xiaomi sensors can be positioned at optimal locations (center of room, away from drafts)
- Built-in sensors may be influenced by the thermostat's own electronics

**This is why the controller exists**: To compensate for Netatmo's faulty sensors by using accurate Xiaomi sensors as the source of truth.

**Example Decision Log:**
```
room_name=Łazienka
xiaomi_temp=25.5          ← Used for decision
scheduled_temp=22.0       ← Target temperature
thermostat_measured=24.8  ← Logged but NOT used
action=no_adjustment_needed
```

Both temperatures are pushed to Prometheus for analysis, allowing you to monitor the delta between sensors over time.

## Control Flow

```mermaid
flowchart TD
    Start[Control Loop Iteration] --> FetchNetatmo[Fetch Netatmo Status]
    FetchNetatmo --> CheckExternal{Externally<br/>Modified?}

    CheckExternal -->|Yes| CheckReset{Reset<br/>Conditions<br/>Met?}
    CheckReset -->|Schedule Mode| ClearFlag[Clear External Flag]
    CheckReset -->|Timeout Expired| ClearFlag
    CheckReset -->|No| Skip1[Skip: Wait for Reset]

    CheckExternal -->|No| CheckRecheck{Before Next<br/>Recheck Time?}
    ClearFlag --> CheckRecheck

    CheckRecheck -->|Yes| Skip2[Skip: In Recheck Delay]
    CheckRecheck -->|No| CheckReachable{Thermostat<br/>Reachable?}

    CheckReachable -->|No| Skip3[Skip: Not Reachable]
    CheckReachable -->|Yes| GetSensor[Get Xiaomi Sensor Data<br/>Weighted Avg, Last 60s]

    GetSensor --> SensorAvail{Sensor Data<br/>Available?}
    SensorAvail -->|No| Skip4[Skip: No Sensor Data]
    SensorAvail -->|Yes| GetSchedule[Get Scheduled Temperature<br/>from Netatmo or Hard Override]

    GetSchedule --> CalcDiff[Calculate Temperature Difference<br/>tempDiff = xiaomiTemp - scheduledTemp]
    CalcDiff --> CheckThreshold{abs tempDiff ><br/>threshold?}

    CheckThreshold -->|No| NoAction[No Adjustment Needed]
    CheckThreshold -->|Yes| CalcSetpoint[Calculate Setpoint<br/>setpoint = scheduledTemp]

    CalcSetpoint --> CheckMatch{Setpoint ==<br/>Schedule?}
    CheckMatch -->|Yes| NoAction2[No Adjustment Needed:<br/>Already at Target]
    CheckMatch -->|No| ApplyLimits[Apply Safety Limits<br/>10°C - 30°C]

    ApplyLimits --> CheckExtMod{Previous<br/>Setpoint<br/>Changed?}
    CheckExtMod -->|Yes| MarkExternal[Mark Externally Modified]
    MarkExternal --> Skip5[Skip: External Modification]

    CheckExtMod -->|No| DryRun{Dry Run<br/>Mode?}
    DryRun -->|Yes| LogOnly[Log Decision Only]
    DryRun -->|No| SetOverride[Set Manual Override<br/>Duration: 10 min]

    SetOverride --> UpdateState[Update State:<br/>- Last Setpoint<br/>- Recheck Time +2 min]
    LogOnly --> UpdateState2[Update State<br/>for Testing]

    Skip1 --> End[End Iteration]
    Skip2 --> End
    Skip3 --> End
    Skip4 --> End
    Skip5 --> End
    NoAction --> End
    NoAction2 --> End
    UpdateState --> End
    UpdateState2 --> End
```

## Decision Logic

### 1. Precedence Hierarchy

```mermaid
flowchart LR
    Start[Evaluate Room] --> P1{External<br/>Modification?}
    P1 -->|Yes| Pause[Pause Control<br/>Wait for Reset]
    P1 -->|No| P2{Recheck<br/>Delay Active?}
    P2 -->|Yes| Wait[Wait for<br/>Recheck Time]
    P2 -->|No| P3{Hard Override<br/>Schedule?}
    P3 -->|Yes| UseHard[Use Hard Override<br/>Temperature]
    P3 -->|No| UseSchedule[Use Netatmo<br/>Schedule]

    UseHard --> Control[Normal Control]
    UseSchedule --> Control
```

### 2. Temperature Comparison

```mermaid
graph TD
    subgraph Inputs
        A[Xiaomi Sensor<br/>25.5°C]
        B[Scheduled Target<br/>22.0°C]
        C[Threshold<br/>0.5°C]
    end

    subgraph Calculation
        D[tempDiff = 25.5 - 22.0<br/>= 3.5°C]
        E{abs 3.5 > 0.5?}
    end

    subgraph Decision
        F[Action Required]
        G[No Action]
    end

    A --> D
    B --> D
    D --> E
    C --> E
    E -->|Yes| F
    E -->|No| G
```

### 3. Setpoint Calculation

**Current Implementation:**

The algorithm uses **three-zone control** to compensate for the difference between the Xiaomi sensor (authoritative) and the Netatmo's built-in sensor (which the thermostat uses for control).

**Zone 1: Room too cold** (tempDiff < -temperatureThreshold):
```
calculatedSetpoint = max(scheduledTemp, thermostatMeasured + 0.5°C)
```
Adds 0.5°C offset to ensure heating starts

**Zone 2: Within acceptable range** (-threshold ≤ tempDiff ≤ +threshold):
```
If not extending override:
  → Skip action (no_adjustment_needed)
If extending override:
  → calculatedSetpoint = thermostatMeasured (maintain without offset)
```
Maintains current temperature or skips action when temperature is acceptable

**Zone 3: Room too warm** (tempDiff > +temperatureThreshold):
```
calculatedSetpoint = min(scheduledTemp, thermostatMeasured - 0.5°C)
```
Subtracts 0.5°C offset to ensure heating stops

**Rationale:**
- The Netatmo uses its **own built-in sensor** to decide when to heat/cool
- The built-in sensor often reads differently than the Xiaomi sensor (different location)
- **Netatmo sensors are known to be faulty**, consistently reading 2-3°C higher than actual temperature
- We must set a setpoint that triggers the correct action **on the Netatmo's sensor**
- The **0.5°C offset** ensures the thermostat actually starts/stops heating
- **Within threshold zone**: Prevents unnecessary interventions when temperature is acceptable
- **Large offsets (2-5°C) are expected and normal** due to Netatmo's sensor inaccuracy - the algorithm compensates without limits

**Example 1: Room Too Cold (Sensor Offset)**
- Xiaomi reads: 23.6°C (too cold)
- Scheduled target: 24.0°C
- Thermostat's sensor: 25.0°C (reads warmer due to location)
- Temperature difference: 23.6 - 24.0 = -0.4°C (exceeds threshold)
- Calculated setpoint: max(24.0, 25.0 + 0.5) = **25.5°C**
- **Result**: Netatmo sees 25.0°C < 25.5°C → **starts heating**
- Room heats until Netatmo sensor reaches 25.5°C
- By then, Xiaomi sensor should read ~24.0°C (target reached)

**Example 2: Within Threshold (New Zone)**
- Xiaomi reads: 24.1°C (close to target)
- Scheduled target: 24.0°C
- Thermostat's sensor: 24.5°C
- Temperature difference: 24.1 - 24.0 = 0.1°C
- Threshold: 0.5°C
- abs(0.1) < 0.5 ✓ (within threshold)
- **Decision**: No adjustment needed (temperature acceptable)
- **Result**: No API call made, thermostat continues with current settings
- Note: If override was about to expire, would maintain with calculatedSetpoint = 24.5°C (no offset)

**Example 3: Room Too Warm**
- Xiaomi reads: 25.5°C (too warm)
- Scheduled target: 22.0°C
- Thermostat's sensor: 24.5°C
- Temperature difference: 25.5 - 22.0 = 3.5°C (exceeds threshold)
- Calculated setpoint: min(22.0, 24.5 - 0.5) = **22.0°C**
- **Result**: Netatmo sees 24.5°C > 22.0°C → **stops heating**
- Room cools until target is reached

## State Management

```mermaid
stateDiagram-v2
    [*] --> Normal

    Normal --> Adjusting: Temperature diff > threshold
    Adjusting --> RecheckDelay: Override sent
    RecheckDelay --> Normal: 2 minutes elapsed

    Normal --> ExternalMod: Manual change detected
    Adjusting --> ExternalMod: Manual change detected
    RecheckDelay --> ExternalMod: Manual change detected

    ExternalMod --> Normal: Schedule mode activated
    ExternalMod --> Normal: 1 hour timeout

    note right of RecheckDelay
        Prevents oscillation
        and excessive API calls
    end note

    note right of ExternalMod
        User manually changed
        thermostat - pause control
    end note
```

## Job Scheduling Architecture

The controller uses **three separate jobs** running independently:

### 1. Home Status Fetch Job (Default: every minute at :00)
- Fetches current home status from Netatmo API
- Stores status in cache with timestamp
- Adds Netatmo readings to metrics buffer
- Configured via `homeStatusFetchCron`

### 2. Control Loop Job (Default: every minute at :30)
- Runs at 30th second of each minute
- Uses cached home status from the same minute (fetched at :00)
- If no cached status available or fetch failed, skips control
- Evaluates all rooms and executes control decisions
- Configured via `controlLoopCron`

### 3. Prometheus Pusher Job (Default: every 30 seconds)
- Runs independently to push all buffered metrics
- Pushes BLE sensor data, Netatmo data, power data, and control decisions
- Configured via `prometheus.cron` or `prometheus.pushIntervalSeconds`

**Timeline Example:**
```
00:00:00 - Home Status Fetch Job runs
00:00:01 - Fetch completes, data added to metrics buffer
00:00:15 - Prometheus Pusher Job runs (pushes all buffered metrics)
00:00:30 - Control Loop Job runs (uses cached status from 00:00:00)
00:00:45 - Prometheus Pusher Job runs again
00:01:00 - Home Status Fetch Job runs again
00:01:01 - Fetch completes, data added to metrics buffer
00:01:15 - Prometheus Pusher Job runs
00:01:30 - Control Loop Job runs (uses cached status from 00:01:00)
```

**Benefits:**
- Ensures control decisions are based on fresh data (< 30 seconds old)
- Separates concerns: fetching vs. control logic vs. metrics pushing
- Metrics are pushed consistently at configured intervals
- If fetch fails, control is safely skipped
- Reduces API calls during control evaluation
- Flexible configuration of all timing parameters

## Configuration Parameters

| Parameter | Default | Description |
|-----------|---------|-------------|
| `temperatureThreshold` | 0.5°C | Minimum difference to trigger action |
| `overrideDurationMinutes` | 10 min | Auto-expire time for overrides |
| `extensionThresholdMinutes` | 2 min | Time before expiry to extend override |
| `externalModificationResetMinutes` | 5 min | Legacy parameter (not actively used) |
| `minSetpointCelsius` | 10.0°C | Safety minimum (config default, code uses 7.0°C) |
| `maxSetpointCelsius` | 30.0°C | Safety maximum |
| `homeStatusFetchCron` | "0 * * * * *" | Cron expression for home status fetch job (runs at :00) |
| `controlLoopCron` | "30 * * * * *" | Cron expression for control loop job (runs at :30) |
| `scheduleSyncIntervalMinutes` | 15 | How often to sync schedule (0 = disabled) |
| `scheduleSyncPollIntervalSeconds` | 2 | Poll interval during schedule sync |
| `scheduleSyncPollTimeoutSeconds` | 30 | Timeout for schedule sync polling |

**Note:** `controlIntervalSeconds` is no longer used. Jobs are scheduled with cron expressions (see Job Scheduling Architecture section).

**Safety Limits Note**: The code applies a **hard-coded minimum of 7.0°C** in `applySafetyBounds()` (evaluate.go:305), overriding the configured `minSetpointCelsius` value. The maximum of 30.0°C is applied from configuration.

## Sensor Data Processing

```mermaid
flowchart LR
    subgraph "60 Second Window"
        T1[t-60s] --> T2[t-45s] --> T3[t-30s] --> T4[t-15s] --> T5[t-0s<br/>now]
    end

    subgraph "Readings"
        R1[24.8°C] --> R2[25.0°C] --> R3[25.2°C] --> R4[25.4°C] --> R5[25.5°C]
    end

    subgraph "Weighting"
        W1[weight: 0.0] --> W2[weight: 0.25] --> W3[weight: 0.5] --> W4[weight: 0.75] --> W5[weight: 1.0]
    end

    T1 -.-> R1
    T2 -.-> R2
    T3 -.-> R3
    T4 -.-> R4
    T5 -.-> R5

    R1 -.-> W1
    R2 -.-> W2
    R3 -.-> W3
    R4 -.-> W4
    R5 -.-> W5

    W1 --> Avg[Weighted Average<br/>≈ 25.3°C]
    W2 --> Avg
    W3 --> Avg
    W4 --> Avg
    W5 --> Avg
```

**Formula:**
```
weight = (timestamp - cutoffTime) / (now - cutoffTime)
weightedAvg = Σ(temperature × weight) / Σ(weight)
```

Recent readings have more influence than older ones.

## Safety Features

### 1. Setpoint Clamping

```mermaid
graph LR
    A[Calculated<br/>Setpoint] --> B{< 10°C?}
    B -->|Yes| C[Clamp to 10°C]
    B -->|No| D{> 30°C?}
    D -->|Yes| E[Clamp to 30°C]
    D -->|No| F[Use as-is]

    C --> G[Final Setpoint]
    E --> G
    F --> G
```

### 2. Auto-Expire Override

All manual overrides automatically expire after 10 minutes, reverting to schedule mode. This ensures the system cannot get "stuck" in an override state.

### 3. External Modification Detection

The system has **three-layer detection logic** for external changes (mode_detection.go):

#### a) Home Mode Change Detection (detectHomeModeChange)
When the home mode changes to or from "away" or "hg" (frost guard):
- External modification flag is **automatically reset**
- Control starts fresh with new mode
- Past overrides are **ignored**
- Tracks mode changes in `ThermostatState.LastHomeMode`

**Transition Detection:**
- TO special mode (away/hg): Reset control state, clear external modification flag
- FROM special mode: Reset control state, clear external modification flag
- Ensures clean slate when mode changes

**Example:**
```
09:00 - Normal mode, algorithm controls temperature
10:00 - User enables "Away" mode
10:01 - detectHomeModeChange() detects transition to "away"
      - Clears external modification flag
      - Updates LastHomeMode to "away"
      - Starts fresh control from new baseline
```

**Implementation:** mode_detection.go:13-74

#### b) Manual Setpoint Change Detection (detectExternalManualChange)
Detects when user manually changes setpoint or override duration (not algorithm):
- Only checks when thermostat is in manual mode
- 2-minute grace period after algorithm's last command
- Compares current setpoint/endtime with tracked baseline
- Marks room as externally modified if changed
- Tracks changes in `ThermostatState.LastManualSetpoint` and `LastManualEndTime`

**Detection Logic:**
- Skip if no previous commands sent (initialize baseline)
- Skip if < 2 minutes since last algorithm command (API propagation)
- Compare current vs. tracked setpoint (>0.1°C difference)
- Compare current vs. tracked end time (any difference)
- If changed: mark externally modified, update baseline

**Detection Algorithm:**
```mermaid
sequenceDiagram
    participant System
    participant Netatmo
    participant User

    System->>Netatmo: Set Override to 22°C, endtime 10:00
    Note over System: Record: lastManualSetpoint=22°C<br/>lastManualEndTime=10:00<br/>lastSetpointTime=now
    System->>System: Wait 2 minutes (grace period)

    User->>Netatmo: Change to 25°C, endtime 11:00

    System->>Netatmo: Fetch Status
    Netatmo-->>System: Current: 25°C, endtime 11:00

    Note over System: Expected: 22°C, endtime 10:00<br/>Actual: 25°C, endtime 11:00<br/>timeSinceLastCommand > 2 min<br/>Change detected!
    System->>System: Mark Externally Modified
    System->>System: Update baseline to 25°C, 11:00
    System->>System: Skip Control (respect user intent)
```

**Implementation:** mode_detection.go:76-138

#### c) Control Mode Decision Logic (shouldControlRoom)

Determines whether to control a room based on its mode and state:

**Decision Tree:**
1. **Schedule Mode** → Control allowed (return true)
2. **Manual Mode:**
   - Check if algorithm-set override (within 15 min AND setpoint matches ±0.3°C)
     - YES → Control allowed (can extend)
     - NO → Skip (respect manual override)
3. **Away/HG Mode:**
   - Check for manual override on top (ThermSetpointEndTime > 0)
     - YES → Skip (respect user override)
     - NO → Control allowed (adjust for sensor differences)
4. **Unknown Mode** → Skip (safety)

**Example Scenarios:**

| Thermostat Mode | Last Set By | Time Since | Setpoint Delta | Decision |
|----------------|-------------|------------|----------------|----------|
| schedule | - | - | - | **Control** (normal operation) |
| manual | Algorithm | 5 min | 0.1°C | **Control** (can extend our override) |
| manual | Algorithm | 20 min | 0.1°C | **Skip** (too long ago, treat as external) |
| manual | Algorithm | 5 min | 0.5°C | **Skip** (setpoint changed externally) |
| manual | User | 5 min | - | **Skip** (respect user intent) |
| away | System | - | - | **Control** (adjust for sensor differences) |
| away + manual override | User | - | - | **Skip** (ThermSetpointEndTime > 0) |
| hg | System | - | - | **Control** (adjust for sensor differences) |
| hg + manual override | User | - | - | **Skip** (ThermSetpointEndTime > 0) |

**Algorithm-Set Override Detection:**
```go
// From shouldControlRoom() - mode_detection.go:152-159
if !state.LastSetpointTime.IsZero() && time.Since(state.LastSetpointTime) < 15*time.Minute {
    setpointDelta := math.Abs(roomStatus.ThermSetpointTemperature - state.LastSetpoint)
    if setpointDelta < 0.3 {
        // This is our override - we can extend it
        return true, ""
    }
}
```

**Implementation:** mode_detection.go:140-186

## Example Scenarios

### Scenarios Summary Table

This table shows all possible combinations of sensor readings and their outcomes:

| Scenario | Xiaomi Temp | Thermo Temp | Schedule Temp | Threshold | Calculated Setpoint | Override Needed? | Outcome |
|----------|-------------|-------------|---------------|-----------|---------------------|------------------|---------|
| 1. Room too warm, thermo reads warm too<br/>(No intervention) | 25.5°C | 24.8°C | 22.0°C | 0.5°C | 22.0°C | NO | Thermo naturally stops heating (24.8 > 22.0) |
| 2. Room too cold, thermo reads cold too<br/>(No intervention) | 18.5°C | 19.2°C | 22.0°C | 0.5°C | 22.0°C | NO | Thermo naturally heats (19.2 < 22.0) |
| 3. Room too cold, thermo reads WARM<br/>(CRITICAL - Must override) | 23.6°C | 25.0°C | 24.0°C | 0.3°C | 25.5°C | **YES** | **CRITICAL:** Must force heating via override |
| 4. Room too cold, large thermo offset<br/>(Override required) | 20.0°C | 24.0°C | 22.0°C | 0.5°C | 24.5°C | **YES** | Large offset requires significant adjustment |
| 5. Room too warm, thermo reads COLD<br/>(CRITICAL - Must override) | 24.0°C | 21.0°C | 22.0°C | 0.5°C | 20.5°C | **YES** | **CRITICAL:** Must stop heating via override |
| 6. Room too warm, moderate thermo offset<br/>(No intervention) | 25.5°C | 23.0°C | 22.0°C | 0.5°C | 22.0°C | NO | Schedule sufficient, thermo will stop |
| 7. Room too warm, large thermo offset<br/>(No intervention) | 26.0°C | 25.0°C | 22.0°C | 0.5°C | 22.0°C | NO | Schedule sufficient even with large offset |
| 8. Within threshold - no action<br/>(Three-zone control) | 24.1°C | 24.5°C | 24.0°C | 0.5°C | N/A | NO | Difference too small (0.1°C < 0.5°C) - skip action |
| 9. Within threshold - extending override<br/>(Three-zone control) | 24.1°C | 24.5°C | 24.0°C | 0.5°C | 24.5°C | YES | Override about to expire - maintain without offset |

**Key Patterns:**
- **CRITICAL cases** (scenarios 3 & 5): Sensors disagree on temperature direction → **override required**
- **No intervention** (scenarios 1, 2, 6, 7, 8): Schedule alone will work → **no override needed**
- **Large offsets** (scenario 4): Even if both sensors agree on direction, large offset needs compensation
- **Three-zone control** (scenarios 8 & 9): Within threshold either skips or maintains without offset

**Algorithm Logic:**
```
When room TOO COLD (tempDiff < -threshold):
  → setpoint = max(schedule, thermoMeasured + 0.5°C)

When WITHIN THRESHOLD (abs(tempDiff) < threshold):
  → If not extending: Skip action (no_adjustment_needed)
  → If extending: setpoint = thermoMeasured (maintain without offset)

When room TOO WARM (tempDiff > +threshold):
  → setpoint = min(schedule, thermoMeasured - 0.5°C)

Override sent if: abs(setpoint - schedule) >= 0.1°C
```

### Detailed Scenario Descriptions

### Scenario 1: Room Too Warm

```
Input:
- Xiaomi sensor: 25.5°C (authoritative - used for control)
- Netatmo thermostat sensor: 24.8°C (used to calculate setpoint)
- Scheduled temperature: 22.0°C
- Threshold: 0.5°C

Calculation:
- tempDiff = 25.5 - 22.0 = 3.5°C (using Xiaomi only)
- abs(3.5) > 0.5 ✓ (exceeds threshold, intervention needed)
- Room is too warm, so: calculatedSetpoint = min(22.0, 24.8 - 0.5)
- calculatedSetpoint = min(22.0, 24.3) = 22.0°C
- calculatedSetpoint == scheduledTemp ✓ (within 0.1°C)

Decision: No adjustment needed (already scheduled to 22°C, which is below thermostat's reading)
Note: The thermostat will naturally stop heating since its sensor (24.8°C) is above schedule (22.0°C)
```

### Scenario 2: Room Too Cold (No Sensor Offset)

```
Input:
- Xiaomi sensor: 18.5°C (authoritative - used for control)
- Netatmo thermostat sensor: 19.2°C (used to calculate setpoint)
- Scheduled temperature: 22.0°C
- Threshold: 0.5°C

Calculation:
- tempDiff = 18.5 - 22.0 = -3.5°C (using Xiaomi only)
- abs(-3.5) > 0.5 ✓ (exceeds threshold, intervention needed)
- Room is too cold, so: calculatedSetpoint = max(22.0, 19.2 + 0.5)
- calculatedSetpoint = max(22.0, 19.7) = 22.0°C
- calculatedSetpoint == scheduledTemp ✓ (within 0.1°C)

Decision: No adjustment needed (already scheduled to 22°C, which is above thermostat's reading)
Note: The thermostat will naturally heat since its sensor (19.2°C) is below schedule (22.0°C)
```

### Scenario 3: Room Too Cold WITH Sensor Offset (Manual Override Required)

This is the critical case where the algorithm must intervene.

```
Input:
- Xiaomi sensor: 23.6°C (authoritative - room is actually cold)
- Netatmo thermostat sensor: 25.0°C (reads warmer, poor placement)
- Scheduled temperature: 24.0°C
- Threshold: 0.3°C

Calculation:
- tempDiff = 23.6 - 24.0 = -0.4°C (using Xiaomi only)
- abs(-0.4) > 0.3 ✓ (exceeds threshold, intervention needed)
- Room is too cold, so: calculatedSetpoint = max(24.0, 25.0 + 0.5)
- calculatedSetpoint = max(24.0, 25.5) = 25.5°C
- calculatedSetpoint != scheduledTemp (25.5 != 24.0)

Decision: Set manual override to 25.5°C for 10 minutes
Reason: "xiaomi=23.6°C, target=24.0°C, diff=-0.40°C"

Result:
- Override sent: 25.5°C
- Netatmo sees: its sensor reads 25.0°C, target is 25.5°C
- Netatmo decision: 25.0 < 25.5 → START HEATING ✓
- Room heats up
- When Netatmo sensor reaches 25.5°C, Xiaomi should read ~24.0°C
- Override expires after 10 minutes, returns to schedule

Why this works:
- Without override: Netatmo sees 25.0°C > 24.0°C → won't heat (room stays cold)
- With override: Netatmo sees 25.0°C < 25.5°C → heats (room reaches target)
```

### Scenario 4: Room Too Warm WITH Sensor Offset (Manual Override Required)

This demonstrates the critical case where the room is warm but the thermostat sensor reads cold.

```
Input:
- Xiaomi sensor: 24.0°C (authoritative - room is actually too warm)
- Netatmo thermostat sensor: 21.0°C (reads cold, maybe near open window/draft)
- Scheduled temperature: 22.0°C
- Threshold: 0.5°C

The Problem:
Without our algorithm, if we naively set setpoint = scheduledTemp = 22.0°C:
- Netatmo sees: 21.0°C < 22.0°C
- Netatmo decision: KEEP HEATING ❌
- Result: Room gets even warmer (disaster!)

Our Algorithm's Solution:
- tempDiff = 24.0 - 22.0 = 2.0°C
- abs(2.0) > 0.5 ✓ (exceeds threshold, intervention needed)
- Room is too warm, so: calculatedSetpoint = min(22.0, 21.0 - 0.5)
- calculatedSetpoint = min(22.0, 20.5) = 20.5°C ← BELOW schedule!
- calculatedSetpoint != scheduledTemp (20.5 != 22.0)

Decision: Set manual override to 20.5°C for 10 minutes
Reason: "xiaomi=24.0°C, target=22.0°C, diff=2.00°C"

Result:
- Override sent: 20.5°C
- Netatmo sees: its sensor reads 21.0°C, target is 20.5°C
- Netatmo decision: 21.0 > 20.5 → STOP HEATING ✓
- Room cools down naturally
- When Netatmo sensor drops to 20.5°C, Xiaomi should read ~22.0°C
- Override expires after 10 minutes, returns to schedule

Why this works:
- The 0.5°C offset below thermostat reading guarantees heating will stop
- Without override: Thermostat would keep heating a room that's already too warm
- With override: Thermostat stops heating, room cools to target
```

### Scenario 5: Room Too Warm, No Override Needed

Not all warm room scenarios require intervention.

```
Input:
- Xiaomi sensor: 25.5°C (authoritative - room is warm)
- Netatmo thermostat sensor: 23.0°C (reads cooler than reality)
- Scheduled temperature: 22.0°C
- Threshold: 0.5°C

Calculation:
- tempDiff = 25.5 - 22.0 = 3.5°C
- abs(3.5) > 0.5 ✓ (exceeds threshold, intervention considered)
- Room is too warm, so: calculatedSetpoint = min(22.0, 23.0 - 0.5)
- calculatedSetpoint = min(22.0, 22.5) = 22.0°C
- calculatedSetpoint == scheduledTemp ✓ (within 0.1°C)

Decision: No adjustment needed
Reason: "calculated setpoint (22.0°C) matches schedule, no override needed"

Why no override is needed:
- The thermostat's sensor reads 23.0°C
- The scheduled target is 22.0°C
- Netatmo sees: 23.0 > 22.0 → will naturally STOP HEATING ✓
- Even though the room is actually warmer (25.5°C), the thermostat will do the right thing
- The schedule is sufficient; no manual intervention required
```

### Scenario 6: Room Too Cold, Large Sensor Offset (Manual Override Required)

This demonstrates a large offset where both sensors agree on direction (cold) but the magnitude requires intervention. **This is a common scenario** because Netatmo sensors are known to read 2-3°C higher than actual temperature.

```
Input:
- Xiaomi sensor: 20.0°C (authoritative - room is cold, accurate reading)
- Netatmo thermostat sensor: 24.0°C (reads 4°C warmer - FAULTY SENSOR)
- Scheduled temperature: 22.0°C
- Threshold: 0.5°C

Calculation:
- tempDiff = 20.0 - 22.0 = -2.0°C
- abs(-2.0) > 0.5 ✓ (exceeds threshold, intervention needed)
- Room is too cold, so: calculatedSetpoint = max(22.0, 24.0 + 0.5)
- calculatedSetpoint = max(22.0, 24.5) = 24.5°C
- calculatedSetpoint != scheduledTemp (24.5 != 22.0)

Decision: Set manual override to 24.5°C for 10 minutes
Reason: "xiaomi=20.0°C, target=22.0°C, diff=-2.00°C"

Result:
- Override sent: 24.5°C
- Netatmo sees: its sensor reads 24.0°C, target is 24.5°C
- Netatmo decision: 24.0 < 24.5 → START HEATING ✓
- Room heats up
- When Netatmo sensor reaches 24.5°C, Xiaomi should read ~22.0°C
- Override expires after 10 minutes, returns to schedule

Note:
- Large sensor offset (4°C difference) is **EXPECTED** due to Netatmo's faulty sensors
- This is exactly why this controller exists - to compensate for broken Netatmo sensors
- The algorithm has **no upper limit** on offset compensation because large offsets are normal
- Without override: Schedule would be 22°C, but thermostat reads 24°C → no heating (room stays cold)
```

### Scenario 6b: Extreme Large Sensor Offset (Real-World Example)

This is a real scenario that was logged in production, demonstrating why unlimited offset compensation is necessary.

```
Input:
- Xiaomi sensor: 23.5°C (authoritative - room slightly cold)
- Netatmo thermostat sensor: 26.0°C (reads 2.5°C warmer - FAULTY SENSOR)
- Scheduled temperature: 24.0°C
- Threshold: 0.3°C

The Problem (Before Fix):
The system previously had a "safety check" that would skip adjustment when sensors disagreed about
whether the room was above/below target. This caused the following bad behavior:
- Xiaomi: 23.5°C < 24.0°C (room is cold, needs heating)
- Netatmo: 26.0°C > 24.0°C (thermostat thinks room is warm)
- Old logic: SKIP adjustment (sensors disagree)
- Result: Room stays cold ❌

Our Algorithm's Solution (After Fix):
- tempDiff = 23.5 - 24.0 = -0.5°C
- abs(-0.5) > 0.3 ✓ (exceeds threshold)
- Room is too cold, so: calculatedSetpoint = max(24.0, 26.0 + 0.5)
- calculatedSetpoint = max(24.0, 26.5) = 26.5°C
- calculatedSetpoint != scheduledTemp (26.5 != 24.0)

Decision: Set manual override to 26.5°C for 10 minutes
Reason: "xiaomi=23.5°C, target=24.0°C, diff=-0.50°C"

Result:
- Override sent: 26.5°C
- Netatmo sees: its sensor reads 26.0°C, target is 26.5°C
- Netatmo decision: 26.0 < 26.5 → START HEATING ✓
- Room heats up from actual 23.5°C
- When Netatmo sensor reaches 26.5°C, Xiaomi should read ~24.0°C ✓
- Override expires after 10 minutes, returns to schedule

Why This Works:
- Xiaomi is ALWAYS the source of truth
- Netatmo's faulty sensor reading is irrelevant for decision-making
- The algorithm compensates for ANY size offset (no limits)
- This is the entire purpose of this controller
```

### Scenario 7: Within Threshold - No Action Required

This demonstrates the temperature threshold preventing unnecessary interventions.

```
Input:
- Xiaomi sensor: 24.2°C (slightly above target)
- Netatmo thermostat sensor: 24.5°C
- Scheduled temperature: 24.0°C
- Threshold: 0.3°C

Calculation:
- tempDiff = 24.2 - 24.0 = 0.2°C
- abs(0.2) < 0.3 ✗ (below threshold)

Decision: No action - skip evaluation
Reason: "temperature difference 0.20°C below threshold 0.30°C"

Why no action:
- The 0.2°C difference is within acceptable range
- Prevents constant micro-adjustments
- Reduces API calls and override churn
- Allows natural temperature fluctuation

The temperatureThreshold parameter controls this behavior:
- Lower values (0.2-0.3°C): More responsive but more interventions
- Higher values (0.5-1.0°C): Less responsive but fewer interventions
- Recommended: 0.3-0.5°C for home heating
```

### Scenario 7b: Within Threshold - Extending Override (Three-Zone Control)

This demonstrates the new three-zone behavior when temperature is acceptable but an override is about to expire.

```
Input:
- Xiaomi sensor: 24.1°C (close to target)
- Netatmo thermostat sensor: 24.5°C
- Scheduled temperature: 24.0°C
- Threshold: 0.5°C
- Override end time: 1 minute from now (extension needed)
- Extension threshold: 2 minutes

Calculation:
- tempDiff = 24.1 - 24.0 = 0.1°C
- abs(0.1) < 0.5 ✓ (within threshold)
- Time until override expires: 1 minute
- Extension threshold: 2 minutes
- shouldExtend = true (override about to expire)

Decision: Set manual override to maintain current temperature
- calculatedSetpoint = thermostatMeasured = 24.5°C (no offset added)
- Override sent with 10 minute duration

Reason: "xiaomi=24.1°C, target=24.0°C, diff=0.10°C (extending, 1m left)"

Why extend without offset:
- Temperature is acceptable (within threshold)
- Override is about to expire, need to maintain control
- No heating/cooling action needed, just maintain current state
- Setpoint = thermostat measured (24.5°C) WITHOUT adding 0.5°C offset
- This prevents oscillation while keeping override active
- After 10 minutes, override expires and returns to schedule

Contrast with Zone 1 (Too Cold) or Zone 3 (Too Warm):
- Zone 1 (too cold): calculatedSetpoint = thermostatMeasured + 0.5°C (trigger heating)
- Zone 2 (within threshold): calculatedSetpoint = thermostatMeasured (maintain only)
- Zone 3 (too warm): calculatedSetpoint = thermostatMeasured - 0.5°C (stop heating)
```

### Scenario 8: Hard Override Active

```
Input:
- Xiaomi sensor: 20.5°C
- Netatmo schedule: 22.0°C
- Hard override: 19.0°C (time-based)
- Threshold: 0.5°C

Calculation:
- Use hard override as target
- tempDiff = 20.5 - 19.0 = 1.5°C
- abs(1.5) > 0.5 ✓
- calculatedSetpoint = 19.0°C
- calculatedSetpoint == scheduledTemp (19.0) ✓

Decision: No adjustment needed
```

## Monitoring Metrics

The system pushes these metrics to Prometheus:

- `thermostat_control_action` - Action taken (0=skip, 1=adjust, 2=no_adjustment)
- `thermostat_control_temperature_difference_celsius` - Xiaomi vs scheduled temp (xiaomiTemp - scheduledTemp)
- `thermostat_control_xiaomi_temperature_celsius` - Current Xiaomi reading (authoritative)
- `thermostat_control_thermostat_measured_celsius` - Netatmo built-in sensor (monitoring only)
- `thermostat_control_scheduled_temperature_celsius` - Target temperature
- `thermostat_control_calculated_setpoint_celsius` - What would be sent to thermostat
- `thermostat_control_setpoint_adjustment_celsius` - Calculated setpoint vs thermostat measured (calculatedSetpoint - thermostatMeasured)
- `thermostat_control_hard_override_active` - Hard override flag
- `thermostat_control_externally_modified` - External modification flag

**Note**: Both `xiaomi_temperature` and `thermostat_measured` are exported to Prometheus, allowing you to:
- Monitor sensor drift over time (compare xiaomi_temperature vs thermostat_measured)
- Compare sensor accuracy and detect systematic bias
- Detect sensor failures (large divergence between sensors)
- Validate Xiaomi sensor placement decisions
- Analyze how often and by how much the sensors disagree

## Dry Run Mode

When `dryRun: true`:
- All control logic executes normally
- Decisions are logged with `[DRY-RUN]` prefix
- **No API calls are made** to Netatmo
- State is still updated (for testing recheck delays)

**Example log:**
```
[DRY-RUN] WOULD set temperature (not actually sent)
  room_name=Łazienka
  new_setpoint=22
  xiaomi_temp=25.5
  scheduled_temp=22
  reason="xiaomi=25.5°C, target=22.0°C, diff=3.50°C"
```

## Schedule Sync Feature

### Problem

The Netatmo API **does not expose the schedule** when the thermostat is in manual mode. This creates a critical issue:

- At 16:40, schedule says 25°C → Controller sets manual override to 25°C for 30 minutes
- At 17:00, schedule changes to 24°C → But controller doesn't know!
- Controller keeps trying to reach 25°C for the remaining 10 minutes
- Energy waste and fighting against the schedule

### Solution: Periodic Schedule Sync with Hybrid Approach

At specific minute intervals (e.g., :00, :15, :30, :45 for 15-minute interval), the controller performs a "schedule sync":

1. **Store previous override state** - For rooms with active algorithm-set overrides, store setpoint and end time
2. **Switch rooms to schedule mode** - Only switches rooms that:
   - Are NOT externally modified by user
   - Are in manual mode SET BY ALGORITHM (within 15 min, setpoint matches ±0.3°C)
   - User-set manual modes are NEVER reset (respects user intent)
   - Already in schedule mode (skipped, no switch needed)
3. **Poll until confirmed** - Single `GetHomeStatus` call per poll iteration checks all rooms
4. **Read and compare schedule** - Compares new schedule with previously synced temperature
5. **Hybrid decision** - For each room:
   - **Schedule UNCHANGED** → Restore previous override immediately (< 2 sec interruption)
   - **Schedule CHANGED** → Skip restore, set bypass flag, re-evaluate with new schedule
6. **Evaluate and execute** - Rooms with schedule changes bypass delayed execution for immediate response

**Key Improvements**:
1. **Minimal Interruption**: Overrides only interrupted for ~2 seconds when schedule unchanged
2. **Immediate Response**: Schedule changes trigger immediate re-evaluation (bypass delayed execution)
3. **Intelligent**: Automatically detects whether to restore or re-evaluate based on schedule changes
4. **User Respect**: User-set manual modes are never touched during sync

### Hybrid Sync Architecture

```mermaid
flowchart TB
    Start[Control Loop: Every 3 minutes] --> Check{15 minutes<br/>since last sync?}

    Check -->|No| Normal[NORMAL MODE:<br/>Fetch Status Once<br/>Evaluate All Rooms]
    Check -->|Yes| Sync[SYNC MODE]

    Sync --> Step1[Step 1: Store previous<br/>override state]
    Step1 --> Step2[Step 2: Switch eligible rooms<br/>to schedule mode]
    Step2 --> Step3[Step 3: Poll until confirmed<br/>Single GetHomeStatus per poll]
    Step3 --> Step4[Step 4: Compare schedule<br/>with previous sync]
    Step4 --> Decision{Schedule<br/>changed?}

    Decision -->|No| Restore[Restore previous override<br/>with remaining duration<br/>~2 sec interruption]
    Decision -->|Yes| Flag[Skip restore<br/>Set ScheduleJustChanged flag]

    Restore --> Step5[Step 5: Fetch final status]
    Flag --> Step5

    Step5 --> Step6[Step 6: Evaluate & execute]
    Step6 --> Bypass{ScheduleJustChanged<br/>flag set?}

    Bypass -->|Yes| Execute[Bypass delayed execution<br/>Execute immediately]
    Bypass -->|No| NormalEval[Normal evaluation<br/>with delayed execution]

    Normal --> End[End Iteration]
    Execute --> End
    NormalEval --> End
```

### API Call Optimization

**Key Optimization**: Single `GetHomeStatus` call per poll iteration checks **all rooms**:

```go
// OLD: Per-room polling (N rooms × M polls = many API calls)
for each room {
    SetRoomThermpoint("home")  // 1 API call
    for up to timeout {
        GetHomeStatus()  // 1 API call per room per poll!
        check this room only
    }
}

// NEW: Batch polling (1 call per poll iteration)
// Step 1: Switch eligible rooms (sequential to avoid rate limiting)
for each room {
    // Skip if:
    // - Externally modified by user
    // - Already in schedule mode
    // - User-set manual mode (NOT set by algorithm)

    // Only switch if:
    // - Algorithm-set manual mode (within 15 min, setpoint matches ±0.3°C)
    // - Away/HG mode

    if shouldSwitch {
        SetRoomThermpoint("home")  // 1 API call per eligible room
    }
}

// Step 2: Single poll checks all rooms
for up to timeout {
    GetHomeStatus()  // 1 API call total!
    check ALL rooms in response
    if all synced: break
}
```

**API Calls per Sync** (3 rooms example, all eligible for switching):
- Switch to schedule: 3 calls (one per room, sequential to avoid rate limiting)
- Polling (15 polls @ 2s interval): **15 calls total** (not 45!)
- Final status: 1 call
- **Total: ~19 calls** (vs ~49 with per-room polling)

**API Calls with User Manual Modes** (1 algorithm override, 2 user-set manual):
- Switch to schedule: 1 call (only algorithm override)
- Polling: **15 calls total** (checks all rooms)
- Final status: 1 call
- **Total: ~17 calls** (user-set manual modes skipped)

**API Calls with Hybrid Restore** (schedule unchanged):
- Switch to schedule: 3 calls
- Polling: 15 calls
- **Restore overrides: 3 calls** (extra calls to restore previous state)
- Final status: 1 call
- **Total: ~22 calls** (slightly more than before, but prevents 3-minute gaps)

**Trade-off Analysis**: The hybrid approach adds a few extra API calls to restore overrides, but provides significantly better user experience by eliminating 3-minute gaps when schedule is unchanged (95% of syncs).

### Configuration

```yaml
thermostatControl:
  # Schedule sync interval (0 = disabled)
  scheduleSyncIntervalMinutes: 15

  # Poll every 2 seconds after switching to schedule mode
  scheduleSyncPollIntervalSeconds: 2

  # Timeout after 30 seconds if mode not confirmed
  scheduleSyncPollTimeoutSeconds: 30
```

### Behavior

**Cron-Like Timing** (New Implementation):
- Sync triggers at specific minutes of the hour based on `scheduleSyncIntervalMinutes`
- For interval=15: syncs at :00, :15, :30, :45
- For interval=30: syncs at :00, :30
- For interval=60: syncs at :00
- Check: `currentMinute % scheduleSyncIntervalMinutes == 0`
- Prevents multiple syncs in the same minute: `time.Since(lastSyncTime) < 1 minute`

**First Run (Never Synced)**:
- At first sync point (e.g., :00), triggers sync
- Switches eligible rooms to schedule mode, reads current schedule
- Stores synced temperature for all rooms

**At Sync Points** (e.g., :00, :15, :30, :45):
- Triggers SYNC MODE instead of NORMAL MODE
- Switches eligible rooms (respects user-set manual modes)
- Fetches fresh schedule data before making decisions

**Between Syncs**:
- Uses NORMAL MODE (single `GetHomeStatus`, no switching)
- Uses cached `SyncedScheduledTemp` from last sync
- Falls back to `ThermSetpointTemperature` if sync is stale (>1 hour)

**Externally Modified Rooms**:
- Skipped during sync (respects manual user changes)
- Automation pauses until user switches back to schedule mode

**User-Set Manual Modes**:
- Detected if manual mode NOT set by algorithm (>15 min ago OR setpoint differs by >0.3°C)
- Never reset during sync (respects user intent)
- Marked as externally modified, skipped from automation

### Example Timeline: Hybrid Sync Behavior

#### Scenario 1: Schedule Unchanged (Most Common - 95% of syncs)

```
10:57:30 - Override active: 24°C (expires 11:12:30)
11:00:00 - SYNC MODE triggered
11:00:02 - Store previous override: 24°C, expires 11:12:30
11:00:02 - Switch to schedule mode
11:00:04 - Read schedule: 24.5°C (same as last sync ✓)
11:00:04 - Compare: 24.5 == 24.5 (no change detected)
11:00:04 - Restore override: 24°C with 12 min remaining
11:00:30 - Evaluation: "no adjustment needed" (already at 24°C)
Result: 2-second interruption, override continues seamlessly
```

#### Scenario 2: Schedule Changed (Rare but Important - 5% of syncs)

```
10:57:30 - Override active: 24°C for schedule of 24.5°C
11:00:00 - SYNC MODE triggered
11:00:02 - Store previous override: 24°C, expires 11:12:30
11:00:02 - Switch to schedule mode
11:00:04 - Read schedule: 22.0°C (CHANGED from 24.5°C!)
11:00:04 - Compare: 22.0 != 24.5 (change detected ✓)
11:00:04 - Skip restore, set ScheduleJustChanged=true
11:00:30 - Evaluation: Bypass delayed execution
11:00:30 - Calculate new override: 22.5°C
11:00:30 - Execute immediately (no 3-minute delay)
Result: Immediate response to schedule change
```

#### Scenario 3: Multiple Rooms with Mixed Changes

```
00:00:00 - SYNC MODE at :00 (15-min interval)
00:00:02 - Store previous overrides:
           ├─ Salon: 24°C, expires 00:12:00
           ├─ Hol: 21°C, expires 00:10:00
           └─ Sypialnia: Schedule mode (no override)

00:00:02 - Switch eligible rooms to schedule:
           ├─ Salon: algorithm override → switch ✓
           ├─ Hol: algorithm override → switch ✓
           └─ Sypialnia: already schedule → skip

00:00:06 - Read and compare schedules:
           ├─ Salon: 24.5°C (unchanged) → restore 24°C
           ├─ Hol: 19.0°C (changed from 20.0°C!) → skip restore, set flag
           └─ Sypialnia: 22.0°C (first sync) → no restore needed

00:00:30 - Evaluation:
           ├─ Salon: Already at 24°C → "no adjustment needed"
           ├─ Hol: Bypass delayed execution → execute new override 19.5°C
           └─ Sypialnia: Normal evaluation → mark pending or execute
```

### Fail-Safe Behavior

**If Sync Fails**:
- `lastSyncTime` is updated **before** final status fetch
- Prevents retrying sync immediately if API fails
- Waits full 15 minutes before next attempt
- Falls back to `ThermSetpointTemperature` for decisions

**If API Calls Fail**:
- Logs errors but continues
- Switches to normal mode for remaining rooms
- Next sync attempt in 15 minutes

### Testing

Comprehensive unit tests verify all recent changes:

**Schedule Sync Tests** (`control/sync_timing_test.go`):
- ✅ Sync triggers at cron-like intervals (e.g., :00, :15, :30, :45)
- ✅ Sync skipped between interval points
- ✅ `lastSyncTime` updated after sync
- ✅ Externally modified rooms skipped during sync
- ✅ User-set manual modes NEVER reset during sync
- ✅ Algorithm-set manual overrides (recent + matching) reset during sync
- ✅ Algorithm-set manual overrides (old or mismatched) treated as user-set
- ✅ Rooms already in schedule mode skipped (no redundant API calls)
- ✅ Normal mode uses single `GetHomeStatus` call
- ✅ Sync disabled when `scheduleSyncIntervalMinutes = 0`
- ✅ Multiple syncs within same minute prevented

**Hybrid Schedule Sync Tests** (`control/sync_hybrid_test.go`):
- ✅ Schedule unchanged → Previous override restored immediately
- ✅ Schedule changed → Override NOT restored, delayed execution bypassed
- ✅ First sync (no previous) → Treated as "unchanged", override restored
- ✅ `ScheduleJustChanged` flag set/cleared correctly
- ✅ Schedule comparison threshold (0.1°C) works correctly

**Mode Detection Tests** (`control/mode_detection_test.go`):
- ✅ Home mode change detection (away/hg transitions)
- ✅ External modification flag reset on mode change
- ✅ External manual change detection (setpoint and endtime)
- ✅ 2-minute grace period for API propagation
- ✅ shouldControlRoom logic for all modes
- ✅ Algorithm-set override detection (15-min window, ±0.3°C tolerance)

**Home Status Fetching Tests** (`control/home_status_fetcher_test.go`):
- ✅ Status caching with timestamps
- ✅ Metrics buffering for Netatmo readings
- ✅ Error handling and logging

**Setpoint Calculation Tests** (`control/controller_setpoint_test.go`):
- ✅ Three-zone control logic
- ✅ Safety bounds application
- ✅ Temperature threshold behavior
- ✅ Override extension logic

See test files for full coverage details and edge cases.

## Future Enhancements

Potential improvements to the algorithm:

1. **PID Controller** - Proportional-Integral-Derivative control for smoother adjustments
2. **Weather Integration** - Adjust based on outdoor temperature and forecast
3. **Occupancy Detection** - Lower temperature when room is unoccupied
4. **Learning Algorithm** - Adapt to thermal characteristics of each room
5. **Multi-Sensor Averaging** - Use multiple sensors per room for accuracy
