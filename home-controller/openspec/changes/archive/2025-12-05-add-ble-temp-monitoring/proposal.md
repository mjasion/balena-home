# Proposal: Add BLE Temperature Monitoring

## Why

Enable passive monitoring of LYWSD03MMC Bluetooth Low Energy temperature sensors for home automation. The service needs to collect temperature data from 4 sensors deployed around the home and output readings to stdout for integration with other services. This forms the foundation for future thermostat control automation based on distributed temperature sensing.

## What Changes

- Create a new Go-based BLE scanner service using `tinygo.org/x/bluetooth` that passively listens for temperature sensor advertisements
- Support LYWSD03MMC sensors with custom ATC_MiThermometer firmware (BLE advertisement mode)
- Implement configurable scanning intervals (minimum 1-minute frequency)
- Use energy-efficient passive BLE scanning (no active connections required)
- Deploy as a standalone service on Raspberry Pi (Balena-compatible)
- Configuration via YAML file with environment variable overrides (following pstryk_metric pattern)
- Add Prometheus remote_write integration for pushing metrics to Grafana Cloud
- Implement concurrent-safe ring buffer for metrics collection with configurable size
- Use zap structured logging for all output (sensor readings, operational events, errors)
- Support both console (human-readable) and JSON log formats via LOG_FORMAT configuration
- Run metrics pushing in separate goroutine with configurable interval

## Impact

- **Affected specs**: Creates new `ble-sensor-monitor` and `prometheus-metrics-push` capabilities
- **Affected code**: New service in thermostats directory
  - `main.go` - Entry point with BLE scanning loop and metrics pushing coordination
  - `config/config.go` - Configuration management (cleanenv) with Prometheus and zap settings
  - `scanner.go` - BLE passive scanning logic, logs sensor readings via zap
  - `decoder.go` - ATC advertisement format decoder
  - `metrics/pusher.go` - Prometheus remote_write client (adapted from pstryk_metric)
  - `buffer/buffer.go` - Concurrent-safe ring buffer for sensor readings
  - `types.go` - Data structures
  - `config.yaml` - Configuration file (YAML format)
  - `example.env` - Example environment variables
- **New dependencies**:
  - `go.uber.org/zap` - Structured logging
  - `github.com/prometheus/prometheus/prompb` - Prometheus protobuf
  - `github.com/gogo/protobuf/proto` - Protocol buffer marshaling
  - `github.com/golang/snappy` - Snappy compression
- **Infrastructure**:
  - Requires host Bluetooth adapter access on Raspberry Pi
  - Requires network access to Grafana Cloud Prometheus endpoint
- **Future extensibility**: Designed to support future Netatmo thermostat integration
