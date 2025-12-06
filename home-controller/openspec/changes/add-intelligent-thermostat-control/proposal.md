# Proposal: Add Intelligent Thermostat Control

## Why

Netatmo thermostats have inaccurate temperature measurements with deviations up to 4 degrees Celsius, causing incorrect heating behavior. By using Xiaomi LYWSD03MMC BLE thermometers (already monitored by the system) as the source of truth for room temperature, we can achieve more accurate climate control while maintaining fail-safe rollback to Netatmo's native schedule.

## What Changes

- **Intelligent temperature control algorithm**: Compare Xiaomi sensor temperature against Netatmo's scheduled target and automatically adjust thermostat setpoint to achieve desired temperature
- **Sensor-to-thermostat mapping**: Configure which Xiaomi sensor controls each thermostat (supporting 3 sensors for 4 thermostats)
- **Configurable temperature threshold**: Define minimum temperature difference (e.g., ±0.2°C or ±0.5°C) required to trigger thermostat adjustment
- **Automatic 10-minute overrides**: Set temporary thermostat adjustments that auto-expire, ensuring fail-safe rollback if the service crashes
- **5-minute re-evaluation**: Check temperatures again 5 minutes after adjustment to handle persistent temperature differences
- **Hard override schedules**: Allow time-based temperature overrides in config (e.g., 6:00-6:20 achieve specific temperature) with precedence over algorithm
- **External modification detection**: Track our last setpoint command and pause algorithm control when thermostat is manually changed externally
- **Netatmo schedule integration**: Fetch current scheduled temperature from Netatmo API to determine target temperature
- **Cron-based control loop**: Run control logic every minute for predictable, reliable operation
- **Netatmo write API**: Implement SetRoomThermpoint API call to adjust thermostat temperatures with duration-based overrides

## Impact

- **Affected specs**:
  - `thermostat-control` (NEW): Core control algorithm, sensor mapping, override management
  - `netatmo-integration` (MODIFIED): Add write capabilities (SetRoomThermpoint), schedule fetching, duration-based setpoint modes

- **Affected code**:
  - `config/config.go`: Add ThermostatControl configuration section
  - `netatmo/client.go`: Add SetRoomThermpoint() method for write operations
  - `netatmo/fetcher.go`: Add GetSchedule() to fetch current scheduled temperatures
  - `netatmo/types.go`: Add schedule and setpoint request/response types
  - `control/` (NEW): New package for thermostat control algorithm
  - `main.go`: Launch control loop goroutine with 1-minute ticker

- **Configuration changes**:
  - New `thermostatControl` section in config.yaml
  - Sensor-to-thermostat mappings
  - Temperature difference threshold
  - Optional hard override time windows

- **Dependencies**: No new external dependencies required

- **Breaking changes**: None - this is additive functionality with opt-in configuration
