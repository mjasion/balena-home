# Proposal: Add BLE Temperature Monitoring

## Why

Enable passive monitoring of LYWSD03MMC Bluetooth Low Energy temperature sensors for home automation. The service needs to collect temperature data from 4 sensors deployed around the home and output readings to stdout for integration with other services. This forms the foundation for future thermostat control automation based on distributed temperature sensing.

## What Changes

- Create a new Go-based BLE scanner service using `tinygo.org/x/bluetooth` that passively listens for temperature sensor advertisements
- Support LYWSD03MMC sensors with custom ATC_MiThermometer firmware (BLE advertisement mode)
- Implement configurable scanning intervals (minimum 1-minute frequency)
- Output structured temperature readings to stdout with timestamp, sensor MAC, temperature, humidity, and battery data
- Use energy-efficient passive BLE scanning (no active connections required)
- Deploy as a standalone script on Raspberry Pi (Balena-compatible)
- Configuration via JSON file with environment variable overrides (following wolweb pattern)

## Impact

- **Affected specs**: Creates new `ble-sensor-monitor` capability
- **Affected code**: New service in thermostats directory
  - `main.go` - Entry point with BLE scanning loop
  - `config.go` - Configuration management (cleanenv)
  - `scanner.go` - BLE passive scanning logic
  - `decoder.go` - ATC advertisement format decoder
  - `types.go` - Data structures
  - `config.json` - Configuration file
- **Infrastructure**: Requires host Bluetooth adapter access on Raspberry Pi
- **Future extensibility**: Designed to support future Netatmo thermostat integration
