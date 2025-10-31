# Implementation Tasks

## 1. Project Setup

- [x] 1.1 Initialize Go module in thermostats directory (`go mod init`)
- [x] 1.2 Add tinygo bluetooth dependency (`go get tinygo.org/x/bluetooth`)
- [x] 1.3 Add cleanenv dependency (`go get github.com/ilyakaznacheev/cleanenv`)
- [x] 1.4 Add zap logging dependency (`go get go.uber.org/zap`)
- [x] 1.5 Add Prometheus dependencies (`go get github.com/prometheus/prometheus/prompb github.com/gogo/protobuf/proto github.com/golang/snappy`)
- [x] 1.6 Create initial file structure (main.go, scanner.go, decoder.go, types.go)
- [x] 1.7 Create package directories (config/, buffer/, metrics/)
- [x] 1.8 Create sample config.yaml with placeholder MAC addresses and Prometheus settings

## 2. Configuration Module

- [x] 2.1 Define Config struct in config/config.go with nested structs for BLE, Prometheus, and Logging sections
- [x] 2.2 Add BLE fields: scanIntervalSeconds, sensors array
- [x] 2.3 Add Prometheus fields: pushIntervalSeconds, prometheusUrl, prometheusUsername, prometheusPassword, metricName, startAtEvenSecond, bufferSize
- [x] 2.4 Add Logging fields: logFormat (console|json), logLevel (debug|info|warn|error)
- [x] 2.5 Implement config loading in config/config.go using cleanenv (YAML + env var support)
- [x] 2.6 Add command-line flag parsing for `-c` (config file path, default: config.yaml)
- [x] 2.7 Add MAC address validation (format: XX:XX:XX:XX:XX:XX)
- [x] 2.8 Add Prometheus URL validation (required field)
- [x] 2.9 Implement zap logger initialization based on logFormat and logLevel
- [x] 2.10 Test config loading with sample config.yaml and environment variable overrides
- [x] 2.11 Create example.env file documenting all environment variables

## 3. Data Structures

- [x] 3.1 Define SensorReading struct in types.go (timestamp, MAC, temp, humidity, battery %, battery mV, RSSI)
- [x] 3.2 Add appropriate json tags for structured logging fields

## 4. BLE Scanner Implementation

- [x] 4.1 Initialize BlueZ adapter in scanner.go using go-bluetooth
- [x] 4.2 Implement passive BLE scanning (start discovery with no filter)
- [x] 4.3 Add advertisement event handler to receive BLE advertisements
- [x] 4.4 Filter advertisements by UUID 0x181A (ATC format)
- [x] 4.5 Filter advertisements by configured sensor MAC addresses
- [x] 4.6 Extract service data payload from advertisement
- [x] 4.7 Pass raw payload to decoder
- [x] 4.8 Handle scanner errors (adapter not found, permission denied)

## 5. ATC Decoder Implementation

- [x] 5.1 Implement ATC advertisement format parser in decoder.go
- [x] 5.2 Parse MAC address (6 bytes, big endian)
- [x] 5.3 Parse temperature (2 bytes, little endian signed int16, divide by 10)
- [x] 5.4 Parse humidity (1 byte, unsigned int8, percentage)
- [x] 5.5 Parse battery percentage (1 byte, unsigned int8)
- [x] 5.6 Parse battery voltage (2 bytes, little endian unsigned int16, millivolts)
- [x] 5.7 Parse frame counter (1 byte, unsigned int8)
- [x] 5.8 Add payload validation (check length, return errors for malformed data)
- [x] 5.9 Test decoder with sample ATC advertisement payloads

## 6. Ring Buffer Implementation

- [x] 6.1 Create buffer/buffer.go with RingBuffer struct (following pstryk_metric pattern)
- [x] 6.2 Add fields: data slice, capacity, size, head index, sync.RWMutex, zap.Logger
- [x] 6.3 Implement New(capacity int, logger *zap.Logger) constructor
- [x] 6.4 Implement Add(reading *SensorReading) method with mutex locking
- [x] 6.5 Add circular buffer logic: overwrite oldest entry when full
- [x] 6.6 Log warning when buffer is full and dropping data
- [x] 6.7 Implement GetAll() method to retrieve all buffered readings with read lock
- [x] 6.8 Implement Size() method to return current buffer size
- [x] 6.9 Implement Clear() method for buffer reset (optional, for testing)
- [x] 6.10 Create buffer/buffer_test.go with unit tests for concurrent access
- [x] 6.11 Test buffer overflow behavior and wrap-around logic

## 7. Prometheus Metrics Pusher

- [x] 7.1 Create metrics/pusher.go (adapt from pstryk_metric/metrics/pusher.go)
- [x] 7.2 Define Pusher struct with fields: url, username, password, metricName, client, lastPush, logger
- [x] 7.3 Implement New(url, username, password, metricName string, logger *zap.Logger) constructor
- [x] 7.4 Implement Push(ctx context.Context, readings []*SensorReading) error method
- [x] 7.5 Implement buildWriteRequest() to convert SensorReadings to prompb.WriteRequest
- [x] 7.6 Group readings by sensor MAC address and create separate TimeSeries for each
- [x] 7.7 Add labels: __name__ (metric name), sensor_id (MAC address)
- [x] 7.8 Round timestamps to the nearest second before converting to milliseconds for Prometheus
- [x] 7.9 Convert temperature values to Prometheus format
- [x] 7.10 Implement pushOnce() for single push attempt with protobuf marshaling and snappy compression
- [x] 7.11 Set required HTTP headers: Content-Type, Content-Encoding, X-Prometheus-Remote-Write-Version
- [x] 7.12 Add HTTP Basic Auth with username/password
- [x] 7.13 Implement retry logic with exponential backoff (1s, 2s, 4s for up to 3 attempts)
- [x] 7.14 Log push attempts, successes, and failures with structured fields (sensor count, data points, timestamp ranges)
- [x] 7.15 Return descriptive errors on failure with HTTP status codes

## 8. Main Orchestration

- [x] 8.1 Implement main.go entry point
- [x] 8.2 Parse command-line flags (`-c` for config file path, default: config.yaml)
- [x] 8.3 Load configuration using config module and initialize zap logger with configured format (console or JSON)
- [x] 8.4 Log service startup with clear message explaining what the service does
- [x] 8.5 Create ring buffer instance with configured capacity
- [x] 8.6 Initialize Prometheus pusher with config values
- [x] 8.7 Implement optional START_AT_EVEN_SECOND alignment (wait until next even second before starting push cycle)
- [x] 8.8 Start BLE scanner goroutine (logs sensor readings as they arrive)
- [x] 8.9 Start Prometheus pusher goroutine with ticker (pushIntervalSeconds)
- [x] 8.10 Implement graceful shutdown on SIGINT/SIGTERM with context cancellation
- [x] 8.11 On shutdown, log shutdown message and attempt final metrics push with remaining buffered data
- [x] 8.12 Log all important process steps with clear, descriptive messages
- [x] 8.13 Use sync.WaitGroup to coordinate goroutine shutdown

## 9. Logging Strategy

- [x] 9.1 Use zap structured logging throughout for all output (sensor readings, operational events, errors)
- [x] 9.2 Log each sensor reading with structured fields: mac, temperature_celsius, humidity_percent, battery_percent, battery_voltage_mv, rssi_dbm
- [x] 9.3 Use clear log message text that explains the process (e.g., "sensor_reading", "starting_ble_scan", "pushing_metrics")
- [x] 9.4 Log BLE adapter initialization with success/failure messages
- [x] 9.5 Handle BLE adapter not found error (exit with helpful message suggesting permissions)
- [x] 9.6 Handle permission denied error (suggest running with sudo or CAP_NET_ADMIN)
- [x] 9.7 Log warnings for advertisement parsing errors (continue scanning)
- [x] 9.8 Log sensor timeout warnings when no data received within expected interval
- [x] 9.9 Log buffer overflow warnings with data loss indication
- [x] 9.10 Log Prometheus push attempts, successes, and failures with retry information
- [x] 9.11 Log network errors with context (URL, status code, error message)
- [x] 9.12 Ensure all log messages are clear and explain what the process is doing at each step
- [x] 9.13 Use appropriate log levels: info for normal operations, warn for recoverable issues, error for failures

## 10. Testing

- [ ] 10.1 Test on Raspberry Pi with actual LYWSD03MMC sensors (ATC firmware)
- [ ] 10.2 Verify log messages are clear and explain what the process is doing
- [ ] 10.3 Test LOG_FORMAT=console produces human-readable logs with sensor readings
- [ ] 10.4 Test LOG_FORMAT=json produces structured JSON logs for log aggregation
- [ ] 10.5 Test with 4 sensors simultaneously
- [ ] 10.6 Test graceful handling of sensors going out of range (verify warning logs)
- [ ] 10.7 Test config loading from YAML file and environment variable overrides
- [ ] 10.8 Verify push interval timing (default 15 seconds)
- [ ] 10.9 Test signal handling (SIGINT/SIGTERM) for clean shutdown with final push
- [ ] 10.10 Verify metrics appear in Grafana Cloud with correct labels and values
- [ ] 10.11 Test Prometheus authentication (correct username/password)
- [ ] 10.12 Test retry logic by temporarily disconnecting network
- [ ] 10.13 Test buffer overflow behavior (generate readings faster than push interval)
- [ ] 10.14 Test START_AT_EVEN_SECOND alignment behavior
- [ ] 10.15 Run buffer unit tests (buffer_test.go)

## 11. Documentation

- [ ] 11.1 Write README.md with setup instructions
- [ ] 11.2 Document ATC firmware flashing process (link to atc1441/ATC_MiThermometer)
- [ ] 11.3 Document configuration options (YAML schema and environment variables)
- [ ] 11.4 Document Grafana Cloud setup (getting Prometheus URL, username, password/API key)
- [ ] 11.5 Document deployment on Raspberry Pi (native and Docker)
- [ ] 11.6 Provide example config.yaml with comments for all fields
- [ ] 11.7 Create example.env file with all environment variable options
- [ ] 11.8 Document Bluetooth permissions requirements (root or CAP_NET_ADMIN)
- [ ] 11.9 Add troubleshooting section (common errors: BLE adapter, auth failures, network issues)
- [ ] 11.10 Document zap logging configuration (LOG_FORMAT, LOG_LEVEL) with examples of console vs JSON output
- [ ] 11.11 Add architecture diagram showing goroutines and data flow
- [ ] 11.12 Document that all output is via zap logging (no separate stdout formatting)

## 12. Docker/Balena Integration

- [x] 12.1 Create Dockerfile with multi-stage build (builder + runtime, following pstryk_metric pattern)
- [x] 12.2 Use golang:alpine as builder stage
- [x] 12.3 Use alpine:latest as runtime stage with ca-certificates and tzdata
- [x] 12.4 Copy config.yaml as default config in image
- [x] 12.5 Run as non-root user (appuser) for security
- [ ] 12.6 Update docker-compose.yml to include ble-temp-monitor service
- [ ] 12.7 Configure host Bluetooth access (network_mode: host or privileged mode)
- [ ] 12.8 Add environment variable configuration in docker-compose.yml (PROMETHEUS_PASSWORD, LOG_FORMAT, LOG_LEVEL, etc.)
- [ ] 12.9 Mount config.yaml as volume for easy configuration changes
- [ ] 12.10 Test build and deployment on Balena platform
- [ ] 12.11 Verify service restarts automatically on failure
- [ ] 12.12 Document Docker deployment in README.md with docker-compose example

## 13. Validation and Review

- [x] 13.1 Run `go fmt` on all source files
- [x] 13.2 Run `go vet` for static analysis
- [x] 13.3 Run `go test ./...` to execute all unit tests
- [ ] 13.4 Test binary on Raspberry Pi with 4 real sensors for 24 hours
- [ ] 13.5 Monitor logs to verify clear, descriptive messages at each process step
- [ ] 13.6 Monitor Grafana Cloud to verify continuous metric ingestion
- [x] 13.7 Review code for consistency with pstryk_metric patterns (config, buffer, metrics)
- [x] 13.8 Verify all requirements from both spec.md files are implemented (ble-sensor-monitor and prometheus-metrics-push)
- [ ] 13.9 Verify OpenSpec proposal with `openspec validate add-ble-temp-monitoring --strict`
- [ ] 13.10 Update proposal.md if any scope changes occurred during implementation
- [x] 13.11 Check for proper error handling and logging throughout
