# BLE Temperature Monitoring Service

A Go-based service for monitoring LYWSD03MMC Bluetooth Low Energy temperature sensors with Prometheus metrics push integration.

## Features

- **Passive BLE Scanning**: Energy-efficient monitoring using BLE advertisements (no active connections)
- **ATC Firmware Support**: Decodes ATC_MiThermometer advertisement format
- **Prometheus Integration**: Pushes metrics to Grafana Cloud via remote_write protocol
- **Concurrent-Safe Buffer**: Ring buffer for collecting sensor readings before push
- **Structured Logging**: Uses zap for configurable JSON or console logging
- **Graceful Shutdown**: Handles SIGINT/SIGTERM with final metrics push

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
- Sensor MAC addresses
- Prometheus endpoint and credentials
- Scan and push intervals
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
- `scanIntervalSeconds`: Interval between scan cycles (default: 60)
- `sensors`: Array of sensor MAC addresses (format: XX:XX:XX:XX:XX:XX)

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

## Prometheus Metrics

The service pushes metrics with the following structure:

- **Metric name**: Configurable (default: `ble_temperature_celsius`)
- **Labels**: `sensor_id` (MAC address of sensor)
- **Values**: Temperature in Celsius
- **Timestamps**: Rounded to nearest second, converted to milliseconds

Example query in Grafana:
```promql
ble_temperature_celsius{sensor_id="A4:C1:38:XX:XX:XX"}
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
- Run with sudo: `sudo ./ble-temp-monitor`
- Or grant capabilities: `sudo setcap cap_net_admin+eip ./ble-temp-monitor`

### Prometheus Push Failures
- Verify credentials in logs
- Check network connectivity to Grafana Cloud
- Review HTTP status codes in error messages

### No Sensor Readings
- Verify sensors have ATC firmware (not stock Xiaomi)
- Check MAC addresses in config match sensors
- Ensure sensors are in range (< 10m typically)
- Monitor RSSI values in logs

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

- [OpenSpec Proposal](./openspec/changes/add-ble-temp-monitoring/proposal.md)
- [Design Document](./openspec/changes/add-ble-temp-monitoring/design.md)
- [Implementation Tasks](./openspec/changes/add-ble-temp-monitoring/tasks.md)
