## Why

The current thermostat control algorithm has grown complex with multiple safety mechanisms (delayed execution, schedule sync, runaway protection, extension logic) that make it difficult to understand and debug. The timing is also imprecise - overrides have fixed durations that don't align with natural 15-minute schedule boundaries.

This refactor simplifies the architecture by:
1. Splitting control into three distinct jobs with clear responsibilities
2. Aligning override timing with 15-minute schedule boundaries
3. Using a simpler manual override detection based on duration (>= 60 min = human)
4. Removing complex safety mechanisms in favor of simpler, predictable behavior

## What Changes

### Architecture Changes
- **Split into 3 jobs**: Metric Job (every 1 min), Control Job (every 15 min), Hard Override Job (every 1 min)
- **Channel-based communication**: Metric Job sends home status to Control Job via channel
- **Non-blocking per-room processing**: Rooms waiting for manual mode to expire run in separate goroutines
- **Fixed 15-minute override windows**: Overrides expire at `:14:59, :29:59, :44:59, :59:59`

### Simplified Algorithm
- **Temperature threshold**: Changed default from 0.5°C to 0.2°C
- **Three-zone control**:
  - Too cold (diff <= -0.2°C): `setpoint = netatmo_measured + 0.5`
  - Within range: `setpoint = netatmo_measured` (maintain)
  - Too warm (diff >= 0.2°C): `setpoint = netatmo_measured - 0.5`

### Manual Override Detection
- **New logic**: Override duration >= 60 minutes = human override (skip)
- **Calculation**: `therm_setpoint_end_time - therm_setpoint_start_time`
- **REMOVED**: Old external modification detection based on tracking LastSetpoint/LastSetpointTime

### Removed Features
- **REMOVED**: Delayed execution (pending setpoint confirmation)
- **REMOVED**: Schedule sync process (switching to schedule mode to read temps)
- **REMOVED**: Runaway protection (consecutive increase detection)
- **REMOVED**: Override extension logic
- **REMOVED**: State fields: `LastSetpoint`, `LastSetpointTime`, `SyncedScheduledTemp`, `SyncedScheduledTime`, `PendingSetpoint`, `PendingSetpointTime`, `ConsecutiveIncreases`, `LastCalculatedSetpoint`, `RunawayHaltUntil`, `ScheduleJustChanged`

### New Behavior
- **Control Job at `:00, :15, :30, :45`**: Waits for Metric Job data, processes rooms concurrently
- **Per-room waiting**: If room in manual mode expiring within window, wait and make own API call
- **Skip conditions**: Manual override >= 60 min, hard override active, thermostat unreachable

## Impact

- **Affected specs**: `thermostat-control`
- **Affected code**:
  - `control/controller.go` - Main orchestration, job scheduling
  - `control/types.go` - Simplified state struct
  - `control/evaluate.go` - Simplified algorithm
  - `control/mode_detection.go` - New duration-based detection
  - `control/sync.go` - **REMOVED** (no longer needed)
  - `control/execute.go` - Simplified execution
  - `control/home_status_fetcher.go` - Now Metric Job with channel
  - `control/hard_override_job.go` - **NEW** separate job
  - `config/config.go` - New cron configs, removed old fields
  - `CONTROL_ALGORITHM.md` - Updated documentation
