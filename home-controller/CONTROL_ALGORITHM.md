# Thermostat Control Algorithm

## Overview

The thermostat control system uses **Xiaomi BLE temperature sensors** as the authoritative source for room temperature, while delegating actual heating control to **Netatmo thermostats**. The algorithm monitors temperature deviations and only intervenes when necessary.

## Key Principles

1. **Xiaomi sensors are authoritative** - Better placement than built-in thermostat sensors
2. **Netatmo schedule is respected** - The system reads the current scheduled temperature
3. **Minimal intervention** - Only override when temperature differs significantly from target
4. **Fail-safe design** - All overrides auto-expire after 10 minutes

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

The controller uses **two separate jobs** running at different times within each minute:

### 1. Home Status Fetch Job (Every minute at :00)
- Fetches current home status from Netatmo API
- Stores status in cache with timestamp
- Adds Netatmo readings to metrics buffer
- Pushes all metrics to Prometheus

### 2. Control Loop Job (Every minute at :30)
- Runs at 30th second of each minute
- Uses cached home status from the same minute (fetched at :00)
- If no cached status available or fetch failed, skips control
- Evaluates all rooms and executes control decisions

**Timeline Example:**
```
00:00:00 - Home Status Fetch Job runs
00:00:01 - Fetch completes, metrics pushed
00:00:30 - Control Loop Job runs (uses cached status from 00:00:00)
00:01:00 - Home Status Fetch Job runs again
00:01:01 - Fetch completes, metrics pushed
00:01:30 - Control Loop Job runs (uses cached status from 00:01:00)
```

**Benefits:**
- Ensures control decisions are based on fresh data (< 30 seconds old)
- Separates concerns: fetching vs. control logic
- Metrics are pushed consistently every minute
- If fetch fails, control is safely skipped
- Reduces API calls during control evaluation

## Configuration Parameters

| Parameter | Default | Description |
|-----------|---------|-------------|
| `temperatureThreshold` | 0.5°C | Minimum difference to trigger action |
| `overrideDurationMinutes` | 10 min | Auto-expire time for overrides |
| `recheckDelayMinutes` | 2 min | Wait time after adjustment |
| `externalModificationResetMinutes` | 5 min | Pause time after manual change |
| `minSetpointCelsius` | 10.0°C | Safety minimum |
| `maxSetpointCelsius` | 30.0°C | Safety maximum |

**Note:** `controlIntervalSeconds` is no longer used. Jobs are scheduled with cron expressions (see Job Scheduling Architecture section).

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

The system now has **improved detection logic** for external changes:

#### a) Home Mode Change Detection (Away/HG)
When the home mode changes to or from "away" or "hg" (frost guard):
- External modification flag is **automatically reset**
- Control starts fresh with new mode
- Past overrides are **ignored**

**Example:**
```
09:00 - Normal mode, algorithm controls temperature
10:00 - User enables "Away" mode
10:01 - Algorithm detects mode change to "away"
      - Clears external modification flag
      - Starts fresh control from new baseline
```

#### b) Manual Setpoint Change Detection
When user manually changes setpoint or override duration:
- System detects change after 2-minute grace period
- Respects user's new setpoint and end time
- Marks room as externally modified
- Skips automation until user returns to schedule mode

**Detection Algorithm:**
```mermaid
sequenceDiagram
    participant System
    participant Netatmo
    participant User

    System->>Netatmo: Set Override to 22°C, endtime 10:00
    Note over System: Record: lastManualSetpoint=22°C<br/>lastManualEndTime=10:00
    System->>System: Wait 2 minutes (grace period)

    User->>Netatmo: Change to 25°C, endtime 11:00

    System->>Netatmo: Fetch Status
    Netatmo-->>System: Current: 25°C, endtime 11:00

    Note over System: Expected: 22°C, endtime 10:00<br/>Actual: 25°C, endtime 11:00<br/>Change detected!
    System->>System: Mark Externally Modified
    System->>System: Update baseline to 25°C, 11:00
    System->>System: Skip Control (respect user intent)
```

#### c) Control Mode Decision Logic

The algorithm decides whether to control a room based on:

1. **Schedule Mode** → Control allowed
2. **Manual Mode set by algorithm** (recently, setpoint matches) → Control allowed (can extend)
3. **Away/HG Mode without manual override** → Control allowed
4. **Manual Mode (external)** → Skip control, respect user intent
5. **Away/HG Mode with manual override** → Skip control, respect user intent

**Example Scenarios:**

| Thermostat Mode | Last Set By | Time Since | Decision |
|----------------|-------------|------------|----------|
| schedule | - | - | **Control** (normal operation) |
| manual | Algorithm | 5 min | **Control** (can extend our override) |
| manual | User | 5 min | **Skip** (respect user intent) |
| away | System | - | **Control** (adjust for sensor differences) |
| away + manual override | User | - | **Skip** (respect user override on away mode) |
| hg | System | - | **Control** (adjust for sensor differences) |
| hg + manual override | User | - | **Skip** (respect user override on hg mode) |

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

### Solution: Periodic Schedule Sync

Every 15 minutes (configurable via `scheduleSyncIntervalMinutes`), the controller performs a "schedule sync":

1. **Switch all rooms to schedule mode** (`"home"` in Netatmo API) in parallel
2. **Poll until confirmed** - Single `GetHomeStatus` call per poll iteration checks all rooms
3. **Read current setpoint** - This reflects the actual schedule temperature at this time
4. **Store synced temperature** - Cached in `ThermostatState.SyncedScheduledTemp`
5. **Pre-calculate weighted averages** - While polling, calculate sensor data in parallel
6. **Evaluate and execute** - Make control decisions with fresh schedule data

### Optimized Architecture

```mermaid
flowchart TB
    Start[Control Loop: Every 1 minute] --> Check{15 minutes<br/>since last sync?}

    Check -->|No| Normal[NORMAL MODE:<br/>Fetch Status Once<br/>Evaluate All Rooms]
    Check -->|Yes| Sync[SYNC MODE]

    Sync --> Step1[Step 1: Switch all rooms<br/>to schedule mode<br/>in parallel]
    Step1 --> Step2[Step 2: Poll with single<br/>GetHomeStatus<br/>Check all rooms at once]
    Step2 --> Step3[Step 3: Pre-calculate<br/>weighted averages<br/>in parallel]
    Step3 --> Step4[Step 4: Fetch final<br/>status for all rooms]
    Step4 --> Step5[Step 5: Evaluate & execute<br/>each room in parallel]

    Normal --> End[End Iteration]
    Step5 --> End
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
// Step 1: Switch all rooms in parallel
for each room in parallel {
    SetRoomThermpoint("home")  // N API calls (unavoidable)
}

// Step 2: Single poll checks all rooms
for up to timeout {
    GetHomeStatus()  // 1 API call total!
    check ALL rooms in response
    if all synced: break
}
```

**API Calls per Sync** (3 rooms example):
- Switch to schedule: 3 calls (one per room, parallel)
- Polling (15 polls @ 2s interval): **15 calls total** (not 45!)
- Final status: 1 call
- **Total: ~19 calls** (vs ~49 with per-room polling)

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

**First Run (Never Synced)**:
- `lastSyncTime` is zero → Triggers sync immediately
- Switches to schedule mode, reads current schedule
- Stores synced temperature for all rooms

**Every 15 Minutes**:
- Timer check: `time.Since(lastSyncTime) >= 15 minutes`
- Triggers SYNC MODE instead of NORMAL MODE
- Fetches fresh schedule data before making decisions

**Between Syncs (Minutes 1-14)**:
- Uses NORMAL MODE (single `GetHomeStatus`, no switching)
- Uses cached `SyncedScheduledTemp` from last sync
- Falls back to `ThermSetpointTemperature` if sync is stale (>1 hour)

**Externally Modified Rooms**:
- Skipped during sync (respects manual user changes)
- Automation pauses until user switches back to schedule mode

### Example Timeline

```
00:00 - Control loop (15 min since last sync)
        ├─ SYNC MODE
        ├─ Switch all rooms to schedule in parallel
        │  ├─ Salon: SetRoomThermpoint("home")
        │  ├─ Hol: SetRoomThermpoint("home")
        │  └─ Sypialnia: SetRoomThermpoint("home")
        │
        ├─ Poll until all confirmed (single GetHomeStatus per poll)
        │  ├─ Poll 1 (2s): Salon=schedule ✓, Hol=manual, Sypialnia=manual
        │  ├─ Poll 2 (4s): Salon=schedule ✓, Hol=schedule ✓, Sypialnia=manual
        │  └─ Poll 3 (6s): All in schedule mode ✓
        │
        ├─ Store synced temperatures
        │  ├─ Salon: 24.0°C
        │  ├─ Hol: 24.0°C
        │  └─ Sypialnia: 22.0°C
        │
        ├─ Pre-calculate averages (parallel)
        ├─ Fetch final status
        └─ Evaluate all rooms with fresh schedule data
           ├─ Salon: needs override → 24.5°C
           ├─ Hol: no action needed
           └─ Sypialnia: needs override → 22.5°C

01:00 - Control loop (1 min since last sync)
        ├─ NORMAL MODE (sync not needed)
        ├─ Fetch status once
        └─ Use cached synced temperatures from 00:00

15:00 - Control loop (15 min since last sync)
        ├─ SYNC MODE (timer expired)
        └─ Read new schedule: 22.0°C (schedule changed!)
           Now using correct target temperature ✓
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

Comprehensive unit tests verify:
- ✅ Sync triggers every 15 minutes exactly
- ✅ Sync skipped when < 15 minutes elapsed
- ✅ `lastSyncTime` updated after sync
- ✅ Externally modified rooms skipped
- ✅ Normal mode uses single `GetHomeStatus` call
- ✅ Sync disabled when `scheduleSyncIntervalMinutes = 0`

See `control/sync_timing_test.go` for full test coverage.

## Future Enhancements

Potential improvements to the algorithm:

1. **PID Controller** - Proportional-Integral-Derivative control for smoother adjustments
2. **Weather Integration** - Adjust based on outdoor temperature and forecast
3. **Occupancy Detection** - Lower temperature when room is unoccupied
4. **Learning Algorithm** - Adapt to thermal characteristics of each room
5. **Multi-Sensor Averaging** - Use multiple sensors per room for accuracy
