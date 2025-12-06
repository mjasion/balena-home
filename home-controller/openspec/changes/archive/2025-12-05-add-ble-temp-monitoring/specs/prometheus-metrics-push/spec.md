# Prometheus Metrics Push Specification

## ADDED Requirements

### Requirement: Concurrent Metrics Collection

The system SHALL collect sensor readings in a thread-safe ring buffer that allows multiple goroutines to add readings concurrently.

#### Scenario: Add reading from BLE scanner

- **WHEN** a sensor reading is decoded from BLE advertisements
- **THEN** the reading SHALL be added to the ring buffer without blocking the BLE scanning goroutine
- **AND** SHALL be thread-safe for concurrent access from multiple goroutines

#### Scenario: Ring buffer size limits

- **WHEN** the ring buffer is configured with a maximum size (default: 1000 readings)
- **THEN** the buffer SHALL store at most the configured number of readings
- **AND** SHALL overwrite the oldest readings when the buffer is full (circular buffer behavior)

#### Scenario: Concurrent-safe access

- **WHEN** multiple goroutines attempt to add or read from the buffer simultaneously
- **THEN** the buffer SHALL use synchronization primitives (e.g., sync.RWMutex) to prevent data races
- **AND** SHALL NOT allow corrupted or inconsistent data

### Requirement: Prometheus Remote Write Integration

The system SHALL push sensor readings to Prometheus using the remote_write protocol with authentication support for Grafana Cloud.

#### Scenario: Push metrics at configurable interval

- **WHEN** the configured push interval elapses (default: 15 seconds, via PUSH_INTERVAL_SECONDS)
- **THEN** the system SHALL collect all readings from the ring buffer since the last push
- **AND** SHALL push them to the Prometheus remote_write endpoint using protobuf format with snappy compression

#### Scenario: Grafana Cloud authentication

- **WHEN** pushing metrics to Grafana Cloud
- **THEN** the system SHALL use HTTP Basic Authentication with the configured username and password
- **AND** SHALL set required headers: Content-Type (application/x-protobuf), Content-Encoding (snappy), X-Prometheus-Remote-Write-Version (0.1.0)

#### Scenario: Metric naming and labels

- **WHEN** building Prometheus time series for sensor readings
- **THEN** each sensor SHALL be represented as a separate time series with labels: __name__ (configurable metric name), sensor_id (sensor MAC address)
- **AND** SHALL include timestamp in milliseconds and value (temperature in Celsius) for each sample

#### Scenario: Timestamp rounding

- **WHEN** converting sensor reading timestamps to Prometheus format
- **THEN** the timestamp SHALL be rounded to the nearest second (removing millisecond precision)
- **AND** SHALL be converted to milliseconds for the Prometheus remote_write protocol (e.g., 2025-10-26T17:30:15.123Z becomes 1729962615000)

### Requirement: Metrics Push Reliability

The system SHALL implement retry logic and error handling to ensure reliable metric delivery.

#### Scenario: Retry on push failure

- **WHEN** a push attempt fails due to network or server errors
- **THEN** the system SHALL retry up to 3 times with exponential backoff (1s, 2s, 4s)
- **AND** SHALL log each retry attempt with structured logging

#### Scenario: Exhausted retry attempts

- **WHEN** all retry attempts are exhausted and the push still fails
- **THEN** the system SHALL log an error with details of the failure
- **AND** SHALL continue running without crashing
- **AND** SHALL attempt to push new readings on the next scheduled interval

#### Scenario: Partial data loss on buffer overflow

- **WHEN** the ring buffer fills up before metrics can be pushed
- **THEN** the oldest readings SHALL be dropped (overwritten by new readings)
- **AND** the system SHALL log a warning indicating data loss

### Requirement: Structured Logging with Zap

The system SHALL use zap structured logging for all operational messages with configurable log level and format.

#### Scenario: Configure log format

- **WHEN** the LOG_FORMAT environment variable is set to "json" or "console"
- **THEN** the system SHALL use the corresponding zap logger configuration (JSON for production, console for development)
- **AND** SHALL default to JSON format if not specified

#### Scenario: Configure log level

- **WHEN** the LOG_LEVEL environment variable is set (debug, info, warn, error)
- **THEN** the system SHALL filter log messages at or above the configured level
- **AND** SHALL default to "info" level if not specified

#### Scenario: Structured log fields

- **WHEN** logging operational events (service start, metric pushes, errors)
- **THEN** logs SHALL include structured fields such as: sensor count, data point count, timestamp ranges, error details, retry attempts
- **AND** SHALL enable efficient log parsing and filtering in log aggregation systems

### Requirement: Configuration Flexibility

The system SHALL support configuration via both YAML files and environment variables, with environment variables taking precedence.

#### Scenario: Load Prometheus configuration from YAML

- **WHEN** the service loads configuration from a YAML file
- **THEN** it SHALL read Prometheus settings: push_url, username, password, metric_name, push_interval_seconds, buffer_size
- **AND** SHALL validate required fields (push_url, username, password)

#### Scenario: Override with environment variables

- **WHEN** environment variables are set (PROMETHEUS_URL, PROMETHEUS_USERNAME, PROMETHEUS_PASSWORD, PUSH_INTERVAL_SECONDS, BUFFER_SIZE, START_AT_EVEN_SECOND)
- **THEN** they SHALL override corresponding values from the YAML file
- **AND** SHALL support cleanenv pattern for environment variable parsing

#### Scenario: Start time alignment

- **WHEN** START_AT_EVEN_SECOND is set to true
- **THEN** the system SHALL wait until the next even-numbered second (e.g., 14:23:30) before starting the first push interval
- **AND** SHALL align subsequent pushes to even-second boundaries for cleaner time-series data

### Requirement: Separate Push Routine

The system SHALL run metrics pushing in a dedicated goroutine that operates independently of the BLE scanning routine.

#### Scenario: Independent push cycle

- **WHEN** the metrics push goroutine is started
- **THEN** it SHALL operate on its own schedule (based on PUSH_INTERVAL_SECONDS) without blocking BLE scanning
- **AND** SHALL use a ticker to trigger pushes at regular intervals

#### Scenario: Graceful shutdown

- **WHEN** the service receives a termination signal (SIGINT/SIGTERM)
- **THEN** the push goroutine SHALL complete any in-progress push operation
- **AND** SHALL attempt a final push of any remaining buffered readings before exiting

#### Scenario: Buffer coordination

- **WHEN** the push goroutine reads from the ring buffer
- **THEN** it SHALL collect all available readings since the last push
- **AND** SHALL NOT interfere with the BLE scanner goroutine adding new readings concurrently
