# Implementation Tasks

## 0. Buffer Package Modifications

- [ ] 0.1 Add GetReadingsByTimeWindow(startTime, endTime) method to buffer/buffer.go (generic, accepts any time range)
- [ ] 0.2 Implement time-based filtering using RLock for thread-safe non-destructive read
- [ ] 0.3 Return readings where reading.Timestamp >= startTime AND reading.Timestamp <= endTime
- [ ] 0.4 Handle time.Time conversion from interface{} in Timestamp field
- [ ] 0.5 Keep GetAll() method (needed by metrics pusher for non-destructive reads)
- [ ] 0.6 Add tests for GetReadingsByTimeWindow with various time windows (1 min, 5 min, 1 hour)
- [ ] 0.7 Add tests for edge cases (empty buffer, no matches, all matches)
- [ ] 0.8 Add tests for concurrent GetReadingsByTimeWindow and GetAll calls
- [ ] 0.9 Update buffer documentation: GetAll() used by metrics pusher (non-destructive), GetReadingsByTimeWindow() for time-filtered access

## 0.5. Dual Buffer Setup

- [ ] 0.5.1 Create second ring buffer in main.go for control loop (controlBuffer := buffer.New(10000, logger))
- [ ] 0.5.2 Pass controlBuffer to control.Controller constructor
- [ ] 0.5.3 Update BLE scanner to accept TWO buffers: metricsBuffer and controlBuffer
- [ ] 0.5.4 Modify BLE scanner to write each reading to BOTH buffers (double Add() calls)
- [ ] 0.5.5 Keep metrics pusher using metricsBuffer with GetAllAndClear() (no changes to pusher)
- [ ] 0.5.6 Ensure Netatmo poller and Power scraper ONLY write to metricsBuffer (not controlBuffer)
- [ ] 0.5.7 Add tests: verify BLE scanner writes to both buffers
- [ ] 0.5.8 Add tests: verify metrics pusher clearing metricsBuffer doesn't affect controlBuffer
- [ ] 0.5.9 Update configuration: add controlBufferSize parameter (default: 10000)

## 1. Configuration and Types

- [ ] 1.1 Add ThermostatControlConfig struct to config/config.go with all fields (enabled, threshold, intervals, mappings, hardOverrides)
- [ ] 1.2 Add ThermostatMapping struct (roomName, sensorMAC, roomID)
- [ ] 1.3 Add HardOverride struct (roomName, schedule with startTime, endTime, targetTemperature)
- [ ] 1.4 Add validation for thermostatControl config in config.Validate() method
- [ ] 1.5 Validate room names exist in Netatmo home data (fetch homes during startup)
- [ ] 1.6 Validate sensor MACs exist in BLE sensor configuration
- [ ] 1.7 Validate temperature threshold range (0.1°C to 5.0°C)
- [ ] 1.8 Validate hard override time formats (HH:MM) and logical ordering (start < end)
- [ ] 1.9 Add configuration example to config.yaml with comments explaining each field
- [ ] 1.10 Update config.PrintConfig() to log thermostat control settings (without sensitive data)

## 2. Netatmo Write API

- [ ] 2.1 Add SetRoomThermpoint request type to netatmo/types.go (HomeID, RoomID, Mode, Temperature, EndTime)
- [ ] 2.2 Add SetRoomThermpoint response type to netatmo/types.go (Status, TimeExec, TimeServer)
- [ ] 2.3 Implement SetRoomThermpoint(ctx, homeID, roomID, temp, mode, endTime) method in netatmo/client.go
- [ ] 2.4 Use POST /api/setroomthermpoint endpoint with form-encoded parameters
- [ ] 2.5 Handle OAuth2 token refresh in SetRoomThermpoint (reuse existing ensureToken logic)
- [ ] 2.6 Parse JSON response and check status field
- [ ] 2.7 Return descriptive errors for API failures (401, 403, 429, 5xx)
- [ ] 2.8 Add timeout handling (reuse existing HTTP client with 30s timeout)
- [ ] 2.9 Add tests for SetRoomThermpoint success case (mock HTTP response)
- [ ] 2.10 Add tests for SetRoomThermpoint error cases (401, 429, network timeout)

## 3. Control Package - Core Types (Thread-Safe)

- [ ] 3.1 Create control/ package directory
- [ ] 3.2 Define ThermostatState struct in control/state.go (LastSetpoint, LastSetpointTime, NextCheckTime, ExternallyModified)
- [ ] 3.3 Define ControllerState struct with sync.RWMutex and map[roomID]*ThermostatState
- [ ] 3.4 Implement NewControllerState() constructor initializing mutex and empty map
- [ ] 3.5 Implement GetState(roomID) method using RLock() and returning a copy of state (not pointer)
- [ ] 3.6 Implement UpdateState(roomID, state) method using Lock() for exclusive write access
- [ ] 3.7 Ensure no I/O operations are performed while holding locks
- [ ] 3.8 Add tests for state initialization
- [ ] 3.9 Add concurrent access tests (multiple goroutines reading/writing state)
- [ ] 3.10 Add test verifying GetState returns copy, not pointer to internal state

## 4. Control Package - Algorithm Logic

- [ ] 4.1 Create control/controller.go with Controller struct (config, netatmo client, buffer, logger, state)
- [ ] 4.2 Implement NewController(config, netatmoClient, buffer, logger) constructor
- [ ] 4.3 Implement RunControlLoop(ctx) method with 1-minute ticker
- [ ] 4.4 Implement processAllThermostats(ctx) to iterate over configured mappings
- [ ] 4.5 Implement processRoomThermostat(ctx, mapping) for single thermostat logic
- [ ] 4.6 Implement isExternallyModified(roomID) check
- [ ] 4.7 Implement getActiveHardOverride(roomName, currentTime) to find matching time window
- [ ] 4.8 Implement getScheduledTemperature(roomStatus) to extract target from Netatmo
- [ ] 4.9 Implement getSensorReadingsLastMinute(sensorMAC) - calculates 60-second window and calls buffer.GetReadingsByTimeWindow()
- [ ] 4.10 Ensure getSensorReadingsLastMinute uses cutoffTime = now.Add(-60*time.Second) consistently
- [ ] 4.11 Implement calculateWeightedAverageTemperature(readings, now) using linear time decay weighting
- [ ] 4.12 Implement calculateWeight(timestamp, cutoffTime, now) helper: (timestamp - cutoffTime) / (now - cutoffTime)
- [ ] 4.13 Implement calculateNewSetpoint(avgXiaomiTemp, scheduledTemp, thermostatMeasured) algorithm
- [ ] 4.14 Implement shouldAdjustSetpoint(difference, threshold) check
- [ ] 4.15 Implement sendSetpointCommand(ctx, homeID, roomID, newSetpoint) wrapper
- [ ] 4.16 Implement detectExternalModification(roomStatus, state) logic
- [ ] 4.17 Implement checkResetConditions(roomStatus, state) for external mod flag reset
- [ ] 4.18 Add comprehensive logging for all control decisions (info level with reading count, weighted average, weights)
- [ ] 4.19 Add error logging for API failures, missing data, validation errors

## 5. Control Package - Helpers and Utilities

- [ ] 5.1 Implement hasSufficientSensorData(readings) to check if readings list is non-empty
- [ ] 5.2 Implement filterReadingsBySensorMAC(readings, macAddress) to extract readings for specific sensor
- [ ] 5.3 Implement shouldSkipControlLoop(state, currentTime) for re-check delay logic
- [ ] 5.4 Implement parseHardOverrideTime(timeStr) to convert "HH:MM" to time.Time
- [ ] 5.5 Implement isTimeInWindow(currentTime, startTime, endTime) for hard override matching
- [ ] 5.6 Implement shouldRespectNetatmoMode(mode) to check for away/hg/etc.
- [ ] 5.7 Add unit tests for all helper functions with edge cases
- [ ] 5.8 Add tests for weighted averaging with 0, 1, 3, 5, 10 readings at different timestamps
- [ ] 5.9 Add tests for weight calculation edge cases (reading at exactly cutoffTime, exactly now)

## 6. Main Integration

- [ ] 6.1 Add thermostat control initialization in main.go (check config.ThermostatControl.Enabled)
- [ ] 6.2 Create control.Controller instance with dependencies
- [ ] 6.3 Launch control loop goroutine with context
- [ ] 6.4 Add control loop to graceful shutdown logic (cancel context, wait for goroutine)
- [ ] 6.5 Ensure control loop starts AFTER Netatmo client initialization and initial home data fetch
- [ ] 6.6 Add logging for control loop startup and shutdown

## 7. Metrics and Observability

- [ ] 7.1 Add thermostat_setpoint_changes_total counter metric (labels: room, home)
- [ ] 7.2 Add thermostat_temperature_difference gauge metric (labels: room, sensor)
- [ ] 7.3 Add thermostat_external_modification gauge metric (labels: room, value: 0 or 1)
- [ ] 7.4 Add thermostat_api_errors_total counter metric (labels: room, error_code)
- [ ] 7.5 Add thermostat_control_loop_duration_seconds histogram metric
- [ ] 7.6 Integrate metrics into existing metrics pusher (reuse Prometheus remote_write)
- [ ] 7.7 Add metric collection calls in control loop (increment counters, set gauges)
- [ ] 7.8 Document metrics in README or CLAUDE.md for Grafana dashboard creation

## 8. Testing - Unit Tests

- [ ] 8.1 Write tests for config validation (valid config, invalid threshold, invalid room name, etc.)
- [ ] 8.2 Write tests for SetRoomThermpoint API client (success, errors, retries)
- [ ] 8.3 Write tests for calculateNewSetpoint algorithm (all scenarios from user table)
- [ ] 8.4 Write tests for shouldAdjustSetpoint threshold logic
- [ ] 8.5 Write tests for getActiveHardOverride with multiple time windows
- [ ] 8.6 Write tests for detectExternalModification logic
- [ ] 8.7 Write tests for checkResetConditions (mode change, timeout)
- [ ] 8.8 Write tests for calculateWeightedAverageTemperature with edge cases (empty, single, multiple readings)
- [ ] 8.9 Write tests for weight calculation formula with various timestamps
- [ ] 8.10 Write tests verifying recent readings have higher weight than old readings
- [ ] 8.11 Write tests for getSensorReadingsLastMinute using buffer.GetReadingsByTimeWindow()
- [ ] 8.12 Write tests for shouldSkipControlLoop re-check delay logic
- [ ] 8.13 Write tests for state management (concurrent access, updates)
- [ ] 8.14 Write tests for weighted averaging accuracy with floating point precision
- [ ] 8.15 Write race condition tests: run with `go test -race` to detect data races
- [ ] 8.16 Write tests for concurrent GetState calls (multiple goroutines reading simultaneously)
- [ ] 8.17 Write tests for concurrent UpdateState calls (multiple goroutines writing)
- [ ] 8.18 Write tests verifying GetState returns copies, not references to internal state
- [ ] 8.19 Write tests ensuring no locks held during I/O operations (mock I/O with delays)

## 9. Testing - Integration Tests

- [ ] 9.1 Create integration test with mocked Netatmo API server
- [ ] 9.2 Test full control loop cycle: read status, calculate setpoint, send command
- [ ] 9.3 Test hard override precedence over algorithm
- [ ] 9.4 Test external modification detection and pause
- [ ] 9.5 Test multiple thermostats with shared sensor
- [ ] 9.6 Test sensor data staleness handling (no readings in last 60 seconds, skip control)
- [ ] 9.7 Test weighted averaging with varying reading frequencies (1/min vs 10/min, verify recent readings dominate)
- [ ] 9.8 Test buffer isolation: control loop reads while metrics pusher clears buffer
- [ ] 9.9 Test API error recovery (continue to next thermostat)
- [ ] 9.10 Test control loop graceful shutdown

## 10. Documentation

- [ ] 10.1 Update home-controller README.md with thermostat control section
- [ ] 10.2 Document configuration schema with all fields and defaults
- [ ] 10.3 Add example configuration for 4 thermostats with 3 sensors
- [ ] 10.4 Document hard override schedule syntax and use cases
- [ ] 10.5 Explain external modification detection behavior and reset conditions
- [ ] 10.6 Document required Netatmo OAuth2 scopes (read_thermostat, write_thermostat)
- [ ] 10.7 Add troubleshooting section (API errors, sensor offline, external modification)
- [ ] 10.8 Update home-controller CLAUDE.md with control algorithm details
- [ ] 10.9 Add Grafana dashboard examples for metrics (setpoint changes, temperature differences)
- [ ] 10.10 Document monitoring and alerting recommendations

## 11. Safety and Validation

- [ ] 11.1 Add startup validation to verify Netatmo OAuth2 token has write_thermostat scope
- [ ] 11.2 Implement dry-run mode (log what would be sent but don't call API) for testing
- [ ] 11.3 Add maximum setpoint temperature safety limit (e.g., 30°C) to prevent overheating
- [ ] 11.4 Add minimum setpoint temperature safety limit (e.g., 10°C) to prevent freezing
- [ ] 11.5 Log warning if calculated setpoint exceeds safety limits and clamp to limit
- [ ] 11.6 Add circuit breaker for API failures (pause control after N consecutive errors)
- [ ] 11.7 Implement API call rate tracking and warning if approaching limits
- [ ] 11.8 Add state sanity checks (detect drift, corrupted state) and reset if needed
- [ ] 11.9 Run all control package tests with -race flag in CI/CD pipeline
- [ ] 11.10 Add integration test for goroutine cleanup on shutdown (no goroutine leaks)

## 12. Deployment and Rollout

- [ ] 12.1 Add thermostatControl.enabled: false to default config.yaml (feature off by default)
- [ ] 12.2 Create example.thermostat.config.yaml with full configuration example
- [ ] 12.3 Update docker-compose.yml documentation if needed (no changes expected)
- [ ] 12.4 Add migration guide for users enabling the feature
- [ ] 12.5 Test deployment on Balena platform (Raspberry Pi)
- [ ] 12.6 Verify control loop runs correctly in container environment
- [ ] 12.7 Verify control loop goroutine starts and stops cleanly with service lifecycle
- [ ] 12.8 Test fail-safe: stop container, verify thermostats revert after 10 minutes
- [ ] 12.9 Test external modification detection with Netatmo mobile app
- [ ] 12.10 Monitor logs and metrics for first 24 hours
- [ ] 12.11 Run production service with GODEBUG=gctrace=1 to monitor goroutine count (ensure no leaks)
- [ ] 12.12 Document lessons learned and edge cases discovered
