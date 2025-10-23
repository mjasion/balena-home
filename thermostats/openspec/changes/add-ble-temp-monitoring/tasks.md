# Implementation Tasks

## 1. Project Setup

- [ ] 1.1 Initialize Go module in thermostats directory (`go mod init`)
- [ ] 1.2 Add go-bluetooth dependency (`go get github.com/muka/go-bluetooth`)
- [ ] 1.3 Add cleanenv dependency (`go get github.com/ilyakaznacheev/cleanenv`)
- [ ] 1.4 Create initial file structure (main.go, config.go, scanner.go, decoder.go, store.go, output.go, types.go)
- [ ] 1.5 Create sample config.json with placeholder MAC addresses

## 2. Configuration Module

- [ ] 2.1 Define Config struct in types.go (scan_interval_seconds, output_format, sensors array)
- [ ] 2.2 Implement config loading in config.go using cleanenv (JSON + env var support)
- [ ] 2.3 Add command-line flag parsing for `-c` (config file path)
- [ ] 2.4 Add MAC address validation (format: XX:XX:XX:XX:XX:XX)
- [ ] 2.5 Test config loading with sample config.json and environment variable overrides

## 3. Data Structures

- [ ] 3.1 Define SensorReading struct in types.go (timestamp, MAC, temp, humidity, battery %, battery mV, RSSI)
- [ ] 3.2 Define output JSON schema matching SensorReading fields
- [ ] 3.3 Add String() method for human-readable text format

## 4. BLE Scanner Implementation

- [ ] 4.1 Initialize BlueZ adapter in scanner.go using go-bluetooth
- [ ] 4.2 Implement passive BLE scanning (start discovery with no filter)
- [ ] 4.3 Add advertisement event handler to receive BLE advertisements
- [ ] 4.4 Filter advertisements by UUID 0x181A (ATC format)
- [ ] 4.5 Filter advertisements by configured sensor MAC addresses
- [ ] 4.6 Extract service data payload from advertisement
- [ ] 4.7 Pass raw payload to decoder
- [ ] 4.8 Handle scanner errors (adapter not found, permission denied)

## 5. ATC Decoder Implementation

- [ ] 5.1 Implement ATC advertisement format parser in decoder.go
- [ ] 5.2 Parse MAC address (6 bytes, big endian)
- [ ] 5.3 Parse temperature (2 bytes, little endian signed int16, divide by 10)
- [ ] 5.4 Parse humidity (1 byte, unsigned int8, percentage)
- [ ] 5.5 Parse battery percentage (1 byte, unsigned int8)
- [ ] 5.6 Parse battery voltage (2 bytes, little endian unsigned int16, millivolts)
- [ ] 5.7 Parse frame counter (1 byte, unsigned int8)
- [ ] 5.8 Add payload validation (check length, return errors for malformed data)
- [ ] 5.9 Test decoder with sample ATC advertisement payloads

## 6. In-Memory Data Store

- [ ] 6.1 Implement store.go with thread-safe sensor reading storage
- [ ] 6.2 Use sync.RWMutex for concurrent access protection
- [ ] 6.3 Store readings in map[MAC][]SensorReading (circular buffer per sensor)
- [ ] 6.4 Limit stored readings to last 5 minutes per sensor
- [ ] 6.5 Implement AddReading(reading SensorReading) method
- [ ] 6.6 Implement GetLatestReadings() map[MAC]SensorReading method
- [ ] 6.7 Implement GetSensorHistory(mac string) []SensorReading method (for future use)

## 7. Output Writer

- [ ] 7.1 Implement JSON formatter in output.go
- [ ] 7.2 Implement human-readable text formatter
- [ ] 7.3 Add OutputReadings function that writes to stdout
- [ ] 7.4 Handle missing sensors (output warning when configured sensor has no recent data)
- [ ] 7.5 Write logs to stderr (separate from data output)
- [ ] 7.6 Include timestamp in ISO 8601 format for all readings

## 8. Main Orchestration

- [ ] 8.1 Implement main.go entry point
- [ ] 8.2 Parse command-line flags (`-c` for config file path)
- [ ] 8.3 Load configuration using config module
- [ ] 8.4 Initialize in-memory data store
- [ ] 8.5 Initialize BLE scanner and start scanning in goroutine
- [ ] 8.6 Set up ticker for periodic output (based on scan_interval_seconds)
- [ ] 8.7 Implement graceful shutdown on SIGINT/SIGTERM
- [ ] 8.8 Log startup info to stderr (version, config summary, sensor count)

## 9. Error Handling and Logging

- [ ] 9.1 Add structured logging to stderr for operational events
- [ ] 9.2 Handle BLE adapter not found error (exit with helpful message)
- [ ] 9.3 Handle permission denied error (suggest running with sudo or CAP_NET_ADMIN)
- [ ] 9.4 Log warnings for advertisement parsing errors (continue scanning)
- [ ] 9.5 Log sensor timeout warnings when no data received within 2x scan interval
- [ ] 9.6 Add debug mode flag for verbose logging (optional)

## 10. Testing

- [ ] 10.1 Test on Raspberry Pi with actual LYWSD03MMC sensors (ATC firmware)
- [ ] 10.2 Verify JSON output format is valid and parseable
- [ ] 10.3 Test with 4 sensors simultaneously
- [ ] 10.4 Test graceful handling of sensors going out of range
- [ ] 10.5 Test config loading from file and environment variables
- [ ] 10.6 Verify 60-second output interval timing
- [ ] 10.7 Test signal handling (SIGINT/SIGTERM) for clean shutdown

## 11. Documentation

- [ ] 11.1 Write README.md with setup instructions
- [ ] 11.2 Document ATC firmware flashing process (link to atc1441/ATC_MiThermometer)
- [ ] 11.3 Document configuration options (JSON schema and environment variables)
- [ ] 11.4 Document deployment on Raspberry Pi (native and Docker)
- [ ] 11.5 Provide example config.json
- [ ] 11.6 Document Bluetooth permissions requirements
- [ ] 11.7 Add troubleshooting section (common errors and solutions)

## 12. Docker/Balena Integration

- [ ] 12.1 Create Dockerfile for Go binary build
- [ ] 12.2 Update docker-compose.yml to include ble-temp-monitor service
- [ ] 12.3 Configure host Bluetooth access (network_mode: host or device mapping)
- [ ] 12.4 Add environment variable configuration in docker-compose.yml
- [ ] 12.5 Test build and deployment on Balena platform
- [ ] 12.6 Verify service restarts automatically on failure
- [ ] 12.7 Document Docker deployment in README.md

## 13. Validation and Review

- [ ] 13.1 Run `go fmt` on all source files
- [ ] 13.2 Run `go vet` for static analysis
- [ ] 13.3 Test binary on Raspberry Pi with 4 real sensors for 24 hours
- [ ] 13.4 Review code for consistency with wolweb patterns
- [ ] 13.5 Verify all requirements from spec.md are implemented
- [ ] 13.6 Update proposal.md if any scope changes occurred during implementation
