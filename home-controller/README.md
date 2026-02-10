# Home Controller Service

A comprehensive Go-based service for home automation, monitoring LYWSD03MMC Bluetooth Low Energy temperature sensors, integrating with Netatmo thermostats, monitoring power consumption, and providing intelligent climate control with Prometheus metrics integration.

## Features

### Monitoring
- **Passive BLE Scanning**: Energy-efficient monitoring using BLE advertisements (no active connections)
- **ATC Firmware Support**: Decodes ATC_MiThermometer advertisement format
- **Netatmo Integration**: Fetches thermostat data and room temperatures via OAuth2 API
- **Power Monitoring**: Scrapes energy consumption metrics from power meters
- **Prometheus Integration**: Pushes all metrics to Grafana Cloud via remote_write protocol
- **Concurrent-Safe Buffer**: Ring buffer for collecting sensor readings before push
- **Structured Logging**: Uses zap for configurable JSON or console logging
- **Graceful Shutdown**: Handles SIGINT/SIGTERM with final metrics push

### Intelligent Thermostat Control (NEW)
- **Automatic Temperature Compensation**: Uses Xiaomi BLE sensors as source of truth, automatically adjusts Netatmo thermostat setpoints to compensate for measurement errors
- **Weighted Average Algorithm**: Recent sensor readings have higher influence (linear time decay weighting over 60-second window)
- **Hard Override Schedules**: Time-based temperature overrides (e.g., "warm up bedroom 22:00-07:00") take precedence over normal control
  - **Day-of-Week Support**: Specify different schedules for weekdays vs. weekends (e.g., sleep-in on weekends)
- **External Modification Detection**: Detects manual thermostat changes, pauses control for 24 hours or until schedule mode
- **Auto-Expiring Overrides**: Temporary setpoint changes automatically revert after 10 minutes (configurable) for fail-safe operation
- **Thread-Safe State Management**: Concurrent control of multiple thermostats without race conditions
- **Dual Buffer Architecture**: Separate buffers for metrics collection vs. control algorithm (metrics cleared every 30s, control retains 60s+ history)

## Quick Start

### Prerequisites

- Go 1.19+ for building
- BlueZ 5.48+ on Linux (standard on Raspberry Pi OS)
- LYWSD03MMC sensors with ATC_MiThermometer firmware
- Grafana Cloud account with Prometheus endpoint

### Installation

```bash
# Clone and build
cd thermostats
go build -o ble-temp-monitor .

# Run with default config
./ble-temp-monitor -c config.yaml
```

### Configuration

Edit `config.yaml` to configure:
- Sensor names, IDs, and MAC addresses
- Prometheus endpoint and credentials
- Metric push interval
- Logging format and level

See `example.env` for environment variable overrides.

**Important**: Set `PROMETHEUS_PASSWORD` environment variable instead of storing it in config.yaml.

### Docker Deployment

```bash
# Build image
docker build -t ble-temp-monitor .

# Run with host Bluetooth access
docker run --rm \
  --network host \
  --cap-add=NET_ADMIN \
  -e PROMETHEUS_PASSWORD=your-api-key \
  -v $(pwd)/config.yaml:/app/config.yaml \
  ble-temp-monitor
```

## Architecture

```
┌─────────────────────────────────────────────┐
│  Main Goroutine                              │
│  - Config loading                            │
│  - Signal handling                           │
│  - Graceful shutdown                         │
└─────────────────────────────────────────────┘
         │                           │
         ▼                           ▼
┌──────────────────┐      ┌──────────────────┐
│  BLE Scanner      │      │  Metrics Pusher  │
│  Goroutine        │──────│  Goroutine       │
│                   │      │                  │
│  - Passive scan   │      │  - Ticker (15s)  │
│  - Filter by MAC  │      │  - Batch push    │
│  - Decode ATC     │      │  - Retry logic   │
│  - Add to buffer  │      │  - Protobuf      │
│  - Log readings   │      │  - Snappy        │
└──────────────────┘      └──────────────────┘
         │                           │
         └─────────┬─────────────────┘
                   ▼
          ┌─────────────────┐
          │   Ring Buffer    │
          │  (concurrent)    │
          └─────────────────┘
```

## File Structure

```
thermostats/
├── main.go               # Entry point, orchestration
├── scanner.go            # BLE scanning (tinygo.org/x/bluetooth)
├── decoder.go            # ATC advertisement decoder
├── types.go              # Data structures
├── config/
│   └── config.go         # Configuration & zap logger
├── buffer/
│   ├── buffer.go         # Thread-safe ring buffer
│   └── buffer_test.go    # Unit tests
├── metrics/
│   └── pusher.go         # Prometheus remote_write client
├── config.yaml           # Default configuration
├── example.env           # Environment variable examples
├── Dockerfile            # Multi-stage Docker build
├── go.mod                # Go module dependencies
└── README.md             # This file
```

## Configuration Reference

### BLE Settings
- `sensors`: Array of sensor configurations with:
  - `name`: Friendly sensor name (e.g., "Bedroom", "Living Room")
  - `id`: Unique numeric identifier (starting from 1)
  - `macAddress`: BLE MAC address (format: XX:XX:XX:XX:XX:XX)

**Note**: BLE scanning runs continuously. Sensors broadcast advertisements every 2-5 seconds, and all readings are collected in the ring buffer until pushed to Prometheus.

### Prometheus Settings
- `pushIntervalSeconds`: Interval between metric pushes (default: 15)
- `prometheusUrl`: Grafana Cloud remote_write endpoint
- `prometheusUsername`: Grafana Cloud instance ID
- `prometheusPassword`: Grafana Cloud API key (use env var)
- `metricName`: Prometheus metric name (default: ble_temperature_celsius)
- `startAtEvenSecond`: Align pushes to even second boundaries (default: true)
- `bufferSize`: Ring buffer capacity (default: 1000)

### Logging Settings
- `logFormat`: "console" (human-readable) or "json" (structured)
- `logLevel`: "debug", "info", "warn", or "error"

### Thermostat Control Settings (NEW)

The thermostat control feature uses **three independent cron jobs**, all disabled by default. Enable specific jobs by setting their corresponding `*JobEnabled` flags to `true`.

```yaml
thermostatControl:
  # Dry-run mode (logs decisions without sending commands)
  dryRun: false

  # Temperature threshold to trigger action
  temperatureThreshold: 0.2  # Minimum difference (°C) to trigger adjustment

  # Job enable flags (all disabled by default)
  metricJobEnabled: true      # Fetch home status from Netatmo API
  controlJobEnabled: true     # Evaluate and apply control decisions
  hardOverrideJobEnabled: true  # Apply time-based overrides

  # Cron schedules (6-field with seconds)
  metricJobCron: "0 * * * * *"              # Every minute at :00
  controlJobCron: "5 * * * * *"             # Every minute at :05
  hardOverrideJobCron: "0 * * * * *"        # Every minute at :00

  # Safety limits
  overrideDurationMinutes: 10
  minSetpointCelsius: 10.0
  maxSetpointCelsius: 30.0

  # Map rooms to sensors
  mappings:
    - roomName: "Living Room"
      sensorMAC: "A4:C1:38:XX:XX:XX"
      roomID: "1234567890abcdef"  # Netatmo room ID (auto-populated at startup)

    - roomName: "Bedroom"
      sensorMAC: "A4:C1:38:YY:YY:YY"
      roomID: ""

  # Optional: Hard override schedules (time-based temperature overrides)
  hardOverrides:
    - roomName: "Bedroom"
      schedule:
        - startTime: "22:00"  # HH:MM format
          endTime: "07:00"
          targetTemperature: 19.0
          days: ["Mon", "Tue", "Wed", "Thu", "Fri"]  # Optional: specify days of week
        - startTime: "23:00"  # Weekend schedule
          endTime: "09:00"
          targetTemperature: 18.5
          days: ["Sat", "Sun"]  # Only on weekends
        - startTime: "06:30"  # Every day (omit days field)
          endTime: "08:00"
          targetTemperature: 22.0
```

**Required Netatmo OAuth2 Scopes**: `read_thermostat`, `write_thermostat`

**Configuration Notes**:
- `temperatureThreshold`: Minimum temperature difference (Xiaomi vs. scheduled) to trigger adjustment (0.1-5.0°C, default: 0.5°C)
- `mappings`: Associates each Netatmo room with a Xiaomi BLE sensor (one sensor can be shared by multiple rooms)
- `roomID`: Optional - automatically populated at startup by fetching Netatmo home data
- `hardOverrides`: Time-based temperature overrides that take precedence over normal control algorithm
  - Each schedule window can optionally specify `days` (array of day names)
  - Supported day formats: Short form (`Mon`, `Tue`, `Wed`, `Thu`, `Fri`, `Sat`, `Sun`) or full names (`Monday`, `Tuesday`, etc.)
  - If `days` is omitted or empty, the override applies to all days of the week
  - Use different schedule windows for weekday vs. weekend temperature preferences

**Control Algorithm**:
1. Read Xiaomi BLE sensor (weighted average of last 60 seconds, recent readings weighted higher)
2. Compare with scheduled temperature (from Netatmo or hard override)
3. If difference exceeds threshold, calculate compensated setpoint: `newSetpoint = thermostatMeasured + (xiaomiTemp - scheduledTemp)`
4. Send temporary 10-minute override to Netatmo (automatically reverts for fail-safe)
5. Wait `recheckDelayMinutes` before re-evaluating (prevents oscillation)

**Precedence Hierarchy** (highest to lowest):
1. External modification detected → Pause control for 24 hours or until schedule mode
2. Hard override active → Use hard override temperature as target
3. Normal control → Use Netatmo schedule temperature as target

**Safety Features**:
- Auto-expiring overrides (default 10 minutes) prevent runaway heating/cooling
- External modification detection pauses control when user manually changes thermostat
- Setpoint compensation formula ensures bounded adjustments
- Thread-safe concurrent control of multiple thermostats
- Graceful degradation when sensor data unavailable

## Prometheus Metrics

The service pushes the following metrics:

### BLE Temperature Sensor Metrics

**Raw sensor readings:**
- **`ble_temperature_celsius`**: Temperature in Celsius
  - Labels: `sensor_id`, `sensor_name`, `mac`
  - Timestamps rounded to nearest 10 seconds

- **`ble_humidity_percent`**: Relative humidity (0-100%)
  - Labels: `sensor_id`, `sensor_name`, `mac`

- **`ble_battery_percent`**: Battery level (0-100%)
  - Labels: `sensor_id`, `sensor_name`, `mac`

**Aggregated metrics:**
- **`ble_temperature_weighted_avg_celsius`**: Weighted average temperature over 60-second window (recent readings weighted higher)
  - Labels: `sensor_id`, `sensor_name`, `mac`
  - Used by thermostat control algorithm for more stable readings

Example query:
```promql
ble_temperature_celsius{sensor_name="Bedroom"}
ble_humidity_percent{sensor_name="Living Room"}
ble_temperature_weighted_avg_celsius{sensor_name="Bedroom"}
```

### Netatmo Thermostat Metrics

- **`netatmo_measured_temperature_celsius`**: Temperature measured by Netatmo thermostat
  - Labels: `home_id`, `room_id`, `room_name`

- **`netatmo_setpoint_temperature_celsius`**: Current setpoint temperature
  - Labels: `home_id`, `room_id`, `room_name`

- **`netatmo_heating_power_request`**: Heating power request (0-100%)
  - Labels: `home_id`, `room_id`, `room_name`

Example query:
```promql
netatmo_measured_temperature_celsius{room_name="Living Room"}
netatmo_setpoint_temperature_celsius{room_name="Bedroom"}
```

### Power Consumption Metrics

Dynamic metric names based on sensor type: `<sensor_type>` (no `power_meter_` prefix)

**Common sensor types:**
- **`active_power`**: Active power (watts)
- **`voltage`**: Voltage (volts)
- **`current`**: Current (amperes)
- **`apparent_power`**: Apparent power (VA)
- **`reactive_power`**: Reactive power (VAR)
- **`frequency`**: Line frequency (Hz)
- **`apparent_energy`**: Apparent energy (VAh)
- **`forward_active_energy`**: Forward active energy (Wh)
- **`forward_reactive_energy`**: Forward reactive energy (VARh)
- **`reverse_active_energy`**: Reverse active energy (Wh)
- **`reverse_reactive_energy`**: Reverse reactive energy (VARh)

**Labels:** `sensor_id`, `sensor_name` (if available)

Example queries:
```promql
active_power{sensor_id="0"}
voltage{sensor_id="1"}
forward_active_energy{sensor_name="Total"}
```

### Thermostat Control Metrics

These metrics are generated by the intelligent thermostat control algorithm (when enabled):

- **`thermostat_control_xiaomi_temperature_celsius`**: Weighted average temperature from Xiaomi BLE sensor
  - Labels: `room_name`
  - Source of truth for room temperature

- **`thermostat_control_scheduled_temperature_celsius`**: Target temperature from schedule or hard override
  - Labels: `room_name`
  - What the room temperature should be

- **`thermostat_control_measured_temperature_celsius`**: Temperature measured by Netatmo thermostat
  - Labels: `room_name`
  - Often differs from Xiaomi sensor due to thermostat placement

- **`thermostat_control_calculated_setpoint_celsius`**: Compensated setpoint sent to thermostat
  - Labels: `room_name`
  - Formula: `thermostatMeasured + (xiaomiTemp - scheduledTemp)`

- **`thermostat_control_temperature_difference_celsius`**: Temperature difference
  - Labels: `room_name`
  - Formula: `xiaomiTemp - scheduledTemp`
  - Should stay near 0°C if control is working correctly

- **`thermostat_control_setpoint_adjustment_celsius`**: Setpoint adjustment applied
  - Labels: `room_name`
  - Formula: `calculatedSetpoint - thermostatMeasured`

- **`thermostat_control_action`**: Control decision taken
  - Labels: `room_name`
  - Values: `0` = skip (external modification detected), `1` = no adjustment needed, `2` = setpoint override applied

Example Grafana queries:
```promql
# Temperature difference (should stay near 0 if working correctly)
thermostat_control_temperature_difference_celsius{room_name="Living Room"}

# Setpoint adjustments over time
thermostat_control_setpoint_adjustment_celsius{room_name="Living Room"}

# Control actions (2 = thermostat adjusted)
thermostat_control_action{room_name="Living Room"}
```

## Logging

### Console Format (Development)
```
2025-10-26T18:16:15.123Z  INFO  sensor_reading  mac=A4:C1:38:XX:XX:XX temp=22.5°C humidity=45% battery=85% ...
```

### JSON Format (Production)
```json
{"level":"info","ts":"2025-10-26T18:16:15.123Z","msg":"sensor_reading","mac":"A4:C1:38:XX:XX:XX","temperature_celsius":22.5,...}
```

## Testing

```bash
# Run unit tests
go test ./...

# Run with verbose output
go test -v ./buffer

# Format and vet
go fmt ./...
go vet ./...
```

## Troubleshooting

### BLE Adapter Not Found
- Ensure BlueZ is installed: `apt-get install bluez`
- Check adapter status: `hciconfig`

### Permission Denied
- Run with sudo: `sudo ./home-controller`
- Or grant capabilities: `sudo setcap cap_net_admin+eip ./home-controller`

### Prometheus Push Failures
- Verify credentials in logs
- Check network connectivity to Grafana Cloud
- Review HTTP status codes in error messages

### No Sensor Readings
- Verify sensors have ATC firmware (not stock Xiaomi)
- Check MAC addresses in config match sensors
- Ensure sensors are in range (< 10m typically)
- Monitor RSSI values in logs

### Thermostat Control Not Working (NEW)
**Symptom**: Control loop running but no adjustments made

**Check logs for**:
- "sensor data unavailable" → Xiaomi BLE sensor not broadcasting or out of range
- "room not found in Netatmo status" → Room name mismatch or Netatmo API issue
- "externally modified, waiting for reset" → Manual thermostat change detected, control paused
- "waiting until next recheck time" → Normal behavior after adjustment (prevents oscillation)

**Common issues**:
1. **Sensor MAC mismatch**: Verify `sensorMAC` in config matches actual Xiaomi sensor
2. **Room name mismatch**: Ensure `roomName` in config matches Netatmo room name exactly
3. **Insufficient sensor data**: Need at least one BLE reading in last 60 seconds for weighted average
4. **External modification detected**: Check if thermostat was manually changed, wait 24h or switch to schedule mode
5. **Temperature difference below threshold**: If `|xiaomiTemp - scheduledTemp| < 0.5°C`, no adjustment needed

**Verify configuration**:
```bash
# Check Netatmo OAuth2 token has write_thermostat scope
grep "write_thermostat" logs

# Verify room IDs populated at startup
grep "initialized room ID" logs

# Monitor control decisions
grep "control decision" logs | tail -20
```

**Monitoring recommendations**:
- Alert if `thermostat_control_temperature_difference_celsius` consistently > 1.0°C
- Alert if `thermostat_control_action == 0 (skip)` for > 1 hour during occupied periods
- Dashboard showing Xiaomi temp vs. Netatmo measured temp (should converge over time)

## ATC Firmware

Sensors must run ATC_MiThermometer custom firmware for advertisement-based monitoring.

Firmware repository: https://github.com/atc1441/ATC_MiThermometer

Flashing tools: Use TelinkFlasher.html via Chrome/Edge browser

## License

Part of the balena-home project. See project root for license information.

## Next Steps

- Deploy to Raspberry Pi
- Configure actual sensor MAC addresses
- Set up Grafana Cloud dashboards
- Test with 4 sensors for 24 hours
- Monitor logs and metrics

## Related Documentation

### BLE Temperature Monitoring
- [OpenSpec Proposal](./openspec/changes/add-ble-temp-monitoring/proposal.md)
- [Design Document](./openspec/changes/add-ble-temp-monitoring/design.md)
- [Implementation Tasks](./openspec/changes/add-ble-temp-monitoring/tasks.md)

### Intelligent Thermostat Control (NEW)
- [OpenSpec Proposal](./openspec/changes/add-intelligent-thermostat-control/proposal.md)
- [Design Document](./openspec/changes/add-intelligent-thermostat-control/design.md)
- [Implementation Tasks](./openspec/changes/add-intelligent-thermostat-control/tasks.md)
- [CLAUDE.md](./CLAUDE.md) - Service-specific AI assistant instructions
