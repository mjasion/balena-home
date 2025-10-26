# Design: BLE Temperature Monitoring Service

## Context

The home automation platform needs to collect temperature data from multiple LYWSD03MMC Xiaomi Bluetooth Low Energy sensors distributed throughout the home. These sensors will run custom ATC_MiThermometer firmware that broadcasts temperature, humidity, and battery data via BLE advertisements. The service must be energy-efficient (using passive scanning), run on Raspberry Pi hardware, and output data to stdout for integration with other systems. This is the foundation for future smart thermostat control based on multi-zone temperature sensing.

### Constraints

- Raspberry Pi platform with limited CPU/memory resources
- Must work within Balena containerized deployment
- BLE passive scanning requires root or CAP_NET_ADMIN capabilities
- LYWSD03MMC sensors use custom ATC firmware (UUID 0x181A advertisement format)
- Minimum output frequency: once per minute
- Must support 4 sensors initially, but be extensible
- Output to stdout for pipeline integration (logging, processing)
- Logs to stderr to separate operational info from data

### Stakeholders

- End user: Home automation system operator
- Future integration: Netatmo thermostat control system
- Infrastructure: Balena platform, Docker Compose orchestration

## Goals / Non-Goals

### Goals

- Passively scan for BLE advertisements from LYWSD03MMC sensors
- Decode ATC_MiThermometer advertisement format (temperature, humidity, battery)
- Support multiple sensors via configuration (MAC address filtering)
- Output structured data (JSON) to stdout at configurable intervals
- Minimize power consumption using passive scanning (no connections)
- Handle missing sensors gracefully (timeouts, out-of-range)
- Deploy on Raspberry Pi via Balena
- Follow project conventions (Go, cleanenv config, gorilla/mux pattern for future HTTP if needed)

### Non-Goals

- Active BLE connections to sensors (passive only for energy efficiency)
- Web UI or HTTP API (future phase if needed)
- Data persistence to files/database (Prometheus only)
- Separate stdout output formatting (zap logging handles all output)
- Support for other sensor types (focus on LYWSD03MMC with ATC firmware)
- Netatmo thermostat integration (deferred to future change)
- Historical data analysis or graphing (delegated to Grafana)
- Sensor firmware flashing tools
- Complex alerting logic (delegated to Grafana Cloud)

## Decisions

### Decision 1: Go with tinygo/bluetooth vs Python bluepy/bleak

**Choice**: Use Go with `tinygo.org/x/bluetooth`

**Rationale**:
- User preference for Go ("I prefer golang but python can also be ok")
- Consistency with existing wolweb service (Go 1.19+)
- Better resource efficiency and binary deployment (single static binary)
- Strong concurrency support for multi-sensor monitoring
- Cross-compilation support for ARM (Raspberry Pi)

**Alternatives considered**:
- Python with `bluepy` or `bleak`: Easier BLE libraries, but requires Python runtime, dependencies, and more memory
- Node.js with `noble`: Good BLE support but adds another runtime to the stack

**Trade-off**: Go BLE libraries are less mature than Python's, but `tinygo.org/x/bluetooth` is actively maintained (August 2025) and production-ready for passive scanning on Linux.

### Decision 2: Passive BLE Scanning (No Connections)

**Choice**: Use passive BLE advertisement scanning only, never establish connections to sensors

**Rationale**:
- ATC_MiThermometer firmware broadcasts all data in advertisements
- Passive scanning is significantly more energy-efficient
- Supports monitoring 4+ sensors without connection overhead
- No pairing or authentication required
- Reduces complexity and potential connection failures

**Alternatives considered**:
- Active connections with GATT characteristic reads: More reliable but much higher power consumption and complexity
- Mixed mode: Connect only when advertisements are missing - adds complexity without clear benefit for ATC firmware

**Trade-off**: Relies on sensors using ATC custom firmware. Stock Xiaomi firmware requires connections/encryption, but user can flash ATC firmware easily.

### Decision 3: Logging-Only Output with Zap

**Choice**: Use zap structured logging exclusively for all output, no separate stdout formatting

**Rationale**:
- Zap provides both human-readable (console) and structured (JSON) formats based on LOG_FORMAT
- Eliminates duplicate output logic (no need for separate stdout writer)
- Console format: Clear, human-readable logs for real-time monitoring via `docker logs`
- JSON format: Structured logs for log aggregation systems (if needed)
- Prometheus push provides the primary data pipeline for metrics and analysis
- Simpler architecture with fewer components

**Log format examples**:
```
# Console format (LOG_FORMAT=console, default for development)
2025-10-26T17:30:15.123Z  INFO  sensor_reading  mac=A4:C1:38:XX:XX:XX temp=22.5°C humidity=45% battery=85% voltage=2.9V rssi=-65dBm

# JSON format (LOG_FORMAT=json, for production)
{"level":"info","ts":"2025-10-26T17:30:15.123Z","msg":"sensor_reading","mac":"A4:C1:38:XX:XX:XX","temperature_celsius":22.5,"humidity_percent":45,"battery_percent":85,"battery_voltage_mv":2900,"rssi_dbm":-65}
```

**Alternatives considered**:
- Separate stdout writer with custom formatting: Duplicate logic, more code to maintain
- Stdout for data, stderr for logs: Unnecessary complexity when Prometheus handles data pipeline
- Printf-style logging: No structured fields, harder to parse

**Trade-off**: All output goes to logs (no separate data stream), but Prometheus provides the primary data pipeline anyway, and zap gives us flexibility for both human and machine-readable formats.

### Decision 4: Configuration - YAML file + Environment Variables

**Choice**: Use cleanenv pattern with YAML config file and environment variable overrides (following pstryk_metric pattern)

**Rationale**:
- Consistency with pstryk_metric service architecture in the balena-home project
- Balena deployment relies heavily on environment variables
- YAML provides better readability and comment support compared to JSON
- cleanenv handles merging and validation
- Environment variables enable secure credential management (e.g., PROMETHEUS_PASSWORD)

**Configuration fields**:
```yaml
# BLE scanning configuration
scanIntervalSeconds: 60
sensors:
  - "A4:C1:38:XX:XX:XX"
  - "A4:C1:38:YY:YY:YY"
  - "A4:C1:38:ZZ:ZZ:ZZ"
  - "A4:C1:38:WW:WW:WW"

# Prometheus metrics push configuration
pushIntervalSeconds: 15
prometheusUrl: "https://prometheus-prod-01-eu-west-0.grafana.net/api/prom/push"
prometheusUsername: "123456"
prometheusPassword: ""  # Use PROMETHEUS_PASSWORD env var
metricName: "ble_temperature_celsius"
startAtEvenSecond: true
bufferSize: 1000

# Logging configuration
logFormat: "json"  # json or console
logLevel: "info"   # debug, info, warn, error
```

Environment variables:
- `SCAN_INTERVAL_SECONDS`: Override scanIntervalSeconds
- `SENSORS`: Comma-separated MAC addresses (overrides sensors list)
- `PUSH_INTERVAL_SECONDS`: Override pushIntervalSeconds
- `PROMETHEUS_URL`: Override prometheusUrl
- `PROMETHEUS_USERNAME`: Override prometheusUsername
- `PROMETHEUS_PASSWORD`: Override prometheusPassword (REQUIRED for Grafana Cloud)
- `METRIC_NAME`: Override metricName
- `START_AT_EVEN_SECOND`: Override startAtEvenSecond (true/false)
- `BUFFER_SIZE`: Override bufferSize
- `LOG_FORMAT`: Override logFormat (json|console)
- `LOG_LEVEL`: Override logLevel (debug|info|warn|error)

**Alternatives considered**:
- JSON config: Less readable, no native comment support
- Command-line flags only: Less convenient for Docker/Balena
- Environment variables only: Harder to document defaults, verbose for lists

**Trade-off**: YAML requires proper indentation but provides much better readability for complex configurations.

### Decision 5: BLE Library - TinyGo Bluetooth

**Choice**: Use `tinygo.org/x/bluetooth` (cross-platform BLE API)

**Rationale**:
- Actively maintained (last update August 2025, 337+ packages using it)
- Native Go implementation with BlueZ D-Bus support on Linux
- Works on Raspberry Pi with BlueZ 5.48+
- Clean, simple API for passive scanning via `adapter.Scan()`
- Cross-platform (Linux, macOS, Windows, bare metal)
- No CGo required for Linux D-Bus backend
- BSD-3-Clause license (permissive)
- Good documentation and examples in repository

**Alternatives considered**:
- `github.com/muka/go-bluetooth`: Previously considered but no longer actively maintained (user feedback)
- `github.com/go-ble/ble`: Requires CGo and has cgocheck issues, less active development
- `github.com/paypal/gatt`: Older library, less active maintenance
- Python with `bluepy`/`bleak`: More mature BLE support but requires Python runtime and dependencies
- CGo bindings to BlueZ C library: More control but increases complexity and build dependencies

**Trade-off**: Requires BlueZ to be installed on the host (standard on Raspberry Pi OS). D-Bus adds a small overhead, but simplifies the implementation significantly. The library is well-maintained and production-ready for Linux BLE scanning use cases.

### Decision 6: Ring Buffer Package - Separate Concurrent-Safe Buffer

**Choice**: Create a dedicated `buffer` package with thread-safe ring buffer implementation (following pstryk_metric pattern)

**Rationale**:
- Consistency with pstryk_metric architecture which already has a proven ring buffer implementation
- Reusable component that can be shared across services
- Encapsulates concurrency concerns (sync.RWMutex) in a well-tested module
- Clean separation of concerns: buffer management is independent of BLE scanning or metrics pushing
- Configurable capacity via BUFFER_SIZE (default: 1000 readings)
- Circular buffer behavior: overwrites oldest entries when full

**Buffer behavior**:
- Thread-safe Add() for BLE scanner goroutine to insert readings
- Thread-safe GetAll() for metrics pusher goroutine to retrieve all buffered readings
- Handles wrap-around automatically
- Logs warnings when buffer is full and dropping old data

**Alternatives considered**:
- Inline map-based storage in main: Less reusable, harder to test, error-prone concurrency
- Channel-based buffer: More complex, potential blocking issues
- Time-based retention (5 minutes): Size-based is simpler and more predictable for memory usage

**Trade-off**: Slight abstraction overhead, but significantly improves code quality and testability.

### Decision 7: Prometheus Remote Write Integration

**Choice**: Push metrics to Grafana Cloud using Prometheus remote_write protocol with protobuf + snappy compression

**Rationale**:
- Grafana Cloud provides managed Prometheus instance (no self-hosting required)
- Remote_write is the standard protocol for Prometheus metric ingestion
- Proven implementation in pstryk_metric (can reuse pusher.go with minimal changes)
- Supports authentication via HTTP Basic Auth (required for Grafana Cloud)
- Snappy compression reduces bandwidth usage
- Configurable push interval (default: 15 seconds) balances data freshness with network overhead

**Metrics structure**:
- Metric name: Configurable via `metricName` (default: "ble_temperature_celsius")
- Labels: `sensor_id` (MAC address of sensor)
- Values: Temperature in Celsius (converted from raw sensor data)
- Timestamps: Rounded to the nearest second, then converted to milliseconds for remote_write protocol

**Alternatives considered**:
- InfluxDB: Different ecosystem, would require new knowledge
- Direct Prometheus exposition: Requires Prometheus to scrape the service (push is simpler for home IoT)
- CSV/JSON file export: Not suitable for real-time monitoring

**Trade-off**: Dependency on Grafana Cloud availability, but provides robust visualization and alerting capabilities.

### Decision 8: Structured Logging with Zap

**Choice**: Use `go.uber.org/zap` for structured logging with configurable format and level

**Rationale**:
- Consistency with pstryk_metric which already uses zap
- High performance (minimal allocation overhead)
- Structured JSON logs for production (easy to parse, search, and alert on)
- Console logs for development (human-readable)
- Log level filtering (debug, info, warn, error) helps reduce noise in production

**Logging strategy**:
- Operational events: service start/stop, configuration loaded, BLE adapter initialized
- Sensor readings: Log each sensor reading with structured fields for monitoring (clear messages like "sensor_reading")
- Metrics events: buffer status, push attempts, push success/failure with data point counts
- Errors: BLE scanning errors, advertisement parsing errors, network errors with retries
- Warnings: buffer full (data loss), sensor timeouts, push retry attempts
- Use clear, descriptive log messages that explain what the process is doing at each step

**Alternatives considered**:
- Standard log package: No structure, harder to parse programmatically
- logrus: Heavier, less performant than zap
- zerolog: Similar performance, but less adoption in the project

**Trade-off**: Learning curve for zap API, but significant benefits for operational visibility.

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────────┐
│  BLE Temperature Monitor (Go)                                    │
│                                                                   │
│  ┌──────────────┐         ┌─────────────────┐                   │
│  │ Config       │         │  BLE Scanner    │                   │
│  │ (cleanenv    │────────▶│  (tinygo/ble)   │                   │
│  │  YAML)       │         │                 │                   │
│  │              │         │ - Passive scan  │                   │
│  │ - Sensors    │         │ - Filter MACs   │                   │
│  │ - Intervals  │         │ - Parse UUID    │                   │
│  │ - Prometheus │         │   0x181A        │                   │
│  │ - Logging    │         └────────┬────────┘                   │
│  └──────────────┘                  │                             │
│                                    ▼                             │
│                           ┌─────────────────┐                   │
│                           │  ATC Decoder    │                   │
│                           │                 │                   │
│                           │ - Extract data  │                   │
│                           │ - Convert temp  │                   │
│                           └────────┬────────┘                   │
│                                    │                             │
│                                    ▼                             │
│                           ┌─────────────────┐                   │
│                           │  Ring Buffer    │                   │
│                           │  (concurrent)   │                   │
│                           │                 │◀─────────┐        │
│                           │ - sync.RWMutex  │          │        │
│                           │ - Circular      │          │        │
│                           │ - Log readings  │          │        │
│                           └────────┬────────┘          │        │
│                                    │                   │        │
│                                    ▼                   │        │
│                           ┌────────────────────────┐   │        │
│                           │  Prometheus Pusher      │   │        │
│                           │  (goroutine)            │   │        │
│                           │                         │   │        │
│                           │ - Ticker (push interval)│   │        │
│                           │ - GetAll() from buffer  │───┘        │
│                           │ - Protobuf + snappy     │            │
│                           │ - HTTP Basic Auth       │            │
│                           │ - Retry logic (3x)      │            │
│                           └────────┬────────────────┘            │
│                                    │                             │
│  ┌─────────────────────────────────┼─────────────────────────┐  │
│  │  Zap Logger (all output)        │                         │  │
│  │  - Console or JSON format       │                         │  │
│  │  - Sensor readings              │                         │  │
│  │  - Operational events           │                         │  │
│  └─────────────────────────────────┘                         │  │
└───────────────────────────────────────────────────────────────┘
                                     │
                                     ▼
                          Grafana Cloud Prometheus
                          (remote_write endpoint)
```

**Key goroutines**:
1. **Main goroutine**: Configuration loading, initialization, signal handling
2. **BLE Scanner goroutine**: Continuous BLE advertisement scanning, decoding, buffer writes, logging readings
3. **Prometheus Pusher goroutine**: Periodic batch push of buffered readings to Grafana Cloud

### Component Responsibilities

1. **Config Module** (`config/config.go`)
   - Load YAML config file (via `-c` flag, default: config.yaml)
   - Merge environment variable overrides using cleanenv
   - Validate sensor MAC addresses and required Prometheus fields
   - Provide configuration struct to other modules
   - Initialize zap logger based on logFormat and logLevel

2. **BLE Scanner** (`scanner.go`)
   - Initialize BLE adapter via tinygo.org/x/bluetooth
   - Start passive BLE scanning using `adapter.Scan()`
   - Filter advertisements by configured MAC addresses
   - Extract service data for UUID 0x181A
   - Pass raw advertisement data to decoder
   - Add decoded readings to ring buffer

3. **ATC Decoder** (`decoder.go`)
   - Parse ATC advertisement format binary data
   - Extract: MAC, temperature, humidity, battery %, battery mV, counter
   - Convert temperature (divide by 10)
   - Return structured sensor reading with timestamp

4. **Ring Buffer** (`buffer/buffer.go`)
   - Thread-safe circular buffer using sync.RWMutex
   - Add() method for inserting sensor readings (called by BLE scanner)
   - GetAll() method for retrieving all buffered readings (called by Prometheus pusher)
   - Configurable capacity (BUFFER_SIZE)
   - Automatic overwrite of oldest entries when full
   - Log warnings on buffer overflow

5. **Prometheus Pusher** (`metrics/pusher.go`)
   - Build Prometheus WriteRequest from sensor readings
   - Group readings by sensor ID (MAC address)
   - Round timestamps to the nearest second before converting to milliseconds
   - Marshal to protobuf and compress with snappy
   - Push to Grafana Cloud remote_write endpoint with HTTP Basic Auth
   - Implement retry logic with exponential backoff (3 attempts)
   - Log push success/failure with structured data (sensor count, data points, timestamp ranges)

6. **Main** (`main.go`)
   - Parse command-line flags (`-c` for config file)
   - Load configuration and initialize zap logger
   - Log service startup with clear, descriptive messages
   - Create ring buffer instance
   - Start BLE scanning goroutine (logs sensor readings as they arrive)
   - Start Prometheus pusher goroutine
   - Handle signals (SIGINT/SIGTERM) for graceful shutdown with final push attempt
   - Log all important process steps with structured fields

## File Structure

```
thermostats/
├── main.go               # Entry point, orchestration, goroutine coordination, signal handling
├── scanner.go            # BLE scanning (tinygo.org/x/bluetooth), logs sensor readings
├── decoder.go            # ATC advertisement format parser
├── types.go              # Data structures (SensorReading, etc.)
├── config/
│   └── config.go         # Configuration loading (cleanenv, YAML parsing, zap initialization)
├── buffer/
│   ├── buffer.go         # Thread-safe ring buffer implementation
│   └── buffer_test.go    # Ring buffer unit tests
├── metrics/
│   └── pusher.go         # Prometheus remote_write client (adapted from pstryk_metric)
├── config.yaml           # Default configuration file with comments
├── example.env           # Example environment variables for documentation
├── go.mod                # Go module dependencies
├── go.sum                # Dependency checksums
├── Dockerfile            # Multi-stage build for Balena deployment
└── README.md             # Usage documentation, deployment instructions
```

## Risks / Trade-offs

### Risk: BlueZ/D-Bus dependency

**Mitigation**: BlueZ is standard on Raspberry Pi OS and most Linux distributions. Document setup requirements clearly. Test on Balena platform early.

### Risk: ATC firmware requirement

**Mitigation**: Document firmware flashing process. Provide link to ATC_MiThermometer project. Initial setup step, but one-time effort.

### Risk: BLE interference or missed advertisements

**Mitigation**: Store last 5 minutes of readings and output warnings when sensors go missing. Consider adjusting scan window/interval if needed.

### Risk: Go BLE library maturity

**Mitigation**: Use tinygo.org/x/bluetooth which is actively maintained (August 2025) and wraps mature BlueZ stack via D-Bus. The library has 337+ packages using it and proven production use. Fall back to Python implementation if blockers found (unlikely based on research).

### Trade-off: In-memory buffering only

**Impact**: If service crashes before pushing to Prometheus, buffered data (up to BUFFER_SIZE readings) is lost. For this use case (real-time monitoring with 15-second push intervals), acceptable. Prometheus provides long-term persistence.

### Trade-off: Root/privileged execution

**Impact**: BLE scanning requires elevated privileges. Acceptable for IoT device where service runs in controlled environment. Document CAP_NET_ADMIN capability as alternative to full root.

## Migration Plan

Not applicable - this is a new service with no existing data or users to migrate.

### Deployment Steps

1. Flash ATC_MiThermometer firmware to all 4 LYWSD03MMC sensors (one-time setup)
2. Note MAC addresses of each sensor
3. Create `config.json` with sensor MAC addresses
4. Build Go binary: `go build -o ble-temp-monitor .`
5. Deploy to Raspberry Pi via Balena or direct installation
6. Configure Docker Compose with host Bluetooth access
7. Start service and verify JSON output to stdout
8. Pipe output to logging system or file as needed

### Rollback

Stop the service. No database or persistent state to clean up. Remove binary if needed.

## Open Questions

1. **Should we support multiple advertisement formats (stock Xiaomi, pvvx, BTHome)?**
   - Initial scope: ATC format only. Can extend later if needed.

2. **Do we need a health check endpoint or signal?**
   - Out of scope for initial implementation. Stdout output itself serves as health indicator.

3. **How should we handle sensor MAC address changes (e.g., replacing a sensor)?**
   - Edit config.json and restart service. Simple and sufficient for low-change environment.

4. **Should we support RSSI-based filtering (ignore sensors too far away)?**
   - Not initially. RSSI reporting is useful, but filtering adds complexity. Output RSSI and let downstream decide.

5. **Is 60-second interval sufficient, or do we need faster sampling?**
   - Start with 60 seconds (user requirement: "minimum once per minute"). Make it configurable so users can adjust.
