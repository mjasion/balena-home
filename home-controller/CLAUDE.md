# Home Controller Service

## Important Instructions for AI Assistants

**CRITICAL: Git Commit Policy**

- **NEVER commit changes automatically** without explicit user request
- **NEVER create commits** unless the user specifically asks you to commit
- **ALWAYS ask for permission** before creating any git commits
- Only create commits when the user explicitly says "commit", "create a commit", "git commit", or similar direct requests
- Making changes to files does NOT imply the user wants those changes committed
- This is a production system controlling home heating - commits must be intentional and reviewed

**When User Requests a Commit:**
- Follow the standard git commit workflow (see root CLAUDE.md)
- Include proper commit messages with context
- Add Co-Authored-By footer for Claude contributions

## Overview

The **home-controller** service is a comprehensive home automation and climate monitoring system that:
- Monitors BLE temperature sensors (LYWSD03MMC with ATC firmware)
- Integrates with Netatmo thermostats for climate data
- Monitors energy consumption from power meters
- Pushes all metrics to Prometheus/Grafana Cloud
- Provides foundation for future climate control automation

This service consolidates multiple data sources into a unified monitoring platform, designed to run on Raspberry Pi via Balena.

## Architecture

### Components

```
┌──────────────────────────────────────────────────────────┐
│  Main Orchestrator                                        │
│  - Config loading                                         │
│  - Signal handling (SIGINT/SIGTERM)                      │
│  - Graceful shutdown with final metrics push             │
│  - Pyroscope profiling (optional)                        │
│  - OpenTelemetry tracing (optional)                      │
└──────────────────────────────────────────────────────────┘
         │
         ├──────────────┬──────────────┬──────────────┬──────────────┬──────────────┐
         ▼              ▼              ▼              ▼              ▼              ▼
┌──────────────┐ ┌──────────────┐ ┌──────────────┐ ┌──────────────┐ ┌──────────────┐ ┌──────────────┐
│ BLE Scanner  │ │ BLE          │ │ Netatmo      │ │ Power Meter  │ │ Thermostat   │ │ Metrics      │
│ (scanner/)   │ │ Aggregator   │ │ Client       │ │ Scraper      │ │ Controller   │ │ Pusher       │
│              │ │ (aggregator/)│ │ (netatmo/)   │ │ (power/)     │ │ (control/)   │ │ (metrics/)   │
│ - Passive    │ │ - Weighted   │ │ - OAuth2     │ │ - HTTP       │ │ - Decision   │ │ - Protobuf   │
│   BLE scan   │ │   averages   │ │ - Rate limit │ │   polling    │ │   engine     │ │ - Snappy     │
│ - ATC decode │ │ - 60s window │ │ - Retry      │ │ - Energy     │ │ - External   │ │ - Batch push │
│ - MAC filter │ │ - Per sensor │ │ - Thermostat │ │   metrics    │ │   mod detect │ │ - Remote     │
│              │ │ - Scheduled  │ │ - HomeStatus │ │ - Scheduled  │ │ - Scheduled  │ │   write API  │
└──────────────┘ └──────────────┘ └──────────────┘ └──────────────┘ └──────────────┘ └──────────────┘
         │              │              │              │              │              │
         └──────────────┴──────────────┴──────────────┴──────────────┴──────────────┘
                        │                             │              │
                        ▼                             ▼              ▼
               ┌─────────────────┐          ┌─────────────────┐ ┌─────────────────┐
               │  Dual Buffers    │          │  Netatmo API    │ │ OpenTelemetry   │
               │  (buffer/)       │          │  - SetThermpoint│ │ (otel/)         │
               │                  │          │  - Manual mode  │ │ - Tracing       │
               │ Control Buffer:  │          │  - Schedule mode│ │ - Sampling      │
               │ - BLE readings   │          │  - HomesData    │ │ - OTLP/HTTP     │
               │ - Real-time data │          │  - HomeStatus   │ │ - Tempo export  │
               │                  │          └─────────────────┘ └─────────────────┘
               │ Metrics Buffer:  │
               │ - Aggregates     │
               │ - Prometheus     │
               │ - 100K capacity  │
               └─────────────────┘
                        │
                        ▼
               ┌─────────────────┐
               │   Pyroscope      │
               │  (pyroscope/)    │
               │  - CPU profiling │
               │  - Memory        │
               │  - Goroutines    │
               │  - Mutex/Block   │
               └─────────────────┘
                        │
                        ▼
               ┌─────────────────┐
               │  Grafana Cloud   │
               │  - Metrics       │
               │  - Profiles      │
               │  - Traces (Tempo)│
               └─────────────────┘
```

### Data Flow

1. **BLE Scanner**: Continuously scans for ATC_MiThermometer advertisements, decodes temperature/humidity/battery data
   - Pushes raw readings to **Control Buffer** (real-time data for control decisions)
2. **BLE Aggregator**: Scheduled job (cron) that calculates weighted averages
   - Reads last 60 seconds of BLE readings from Control Buffer
   - Calculates time-weighted average per sensor (recent readings weighted higher)
   - Pushes aggregated readings to **Metrics Buffer** for Prometheus
3. **Power Meter Scraper**: Polls HTTP endpoints for energy consumption metrics
   - Pushes readings to Metrics Buffer
4. **Thermostat Controller**: Scheduled job (cron) for intelligent climate control
   - **Metric Job**: Fetches Netatmo home status, calculates metrics, pushes to Metrics Buffer
   - **Control Job**: Reads BLE sensor data from Control Buffer (weighted average over 60s)
     - Fetches thermostat state from Netatmo API
     - Evaluates control decisions based on temperature difference
     - Sends manual override commands to Netatmo API when needed
     - Respects external modifications (manual user changes)
   - **Hard Override Job**: Applies time-based temperature overrides based on schedule
5. **Dual Ring Buffers** (thread-safe, 100K capacity each):
   - **Control Buffer**: Real-time BLE readings for control decisions
   - **Metrics Buffer**: Aggregated data for Prometheus push
6. **Metrics Pusher**: Batch pushes to Prometheus every 30 seconds from Metrics Buffer
7. **Pyroscope Profiler** (optional): Continuous profiling of CPU, memory, goroutines to Grafana Cloud
8. **OpenTelemetry Tracer** (optional): Distributed tracing exported to Grafana Tempo via OTLP/HTTP

## Project Structure

```
home-controller/
├── main.go                # Entry point, orchestration, goroutine management
├── types.go               # Shared data structures
├── config/
│   ├── config.go          # Configuration loading (cleanenv)
│   └── config_test.go     # Config tests
├── scanner/
│   ├── scanner.go         # BLE scanning (tinygo.org/x/bluetooth)
│   └── scanner_test.go
├── decoder/
│   ├── decoder.go         # ATC advertisement decoder
│   └── decoder_test.go
├── aggregator/
│   └── aggregator.go      # BLE weighted average calculation
├── scheduler/
│   ├── scheduler.go       # Aligned interval scheduling utilities
│   ├── manager.go         # Job scheduler management
│   └── scheduler_test.go
├── netatmo/
│   ├── client.go          # OAuth2 client with rate limiting and retry
│   ├── types.go           # Netatmo API types
│   └── client_test.go     # Client tests
├── control/
│   ├── controller.go      # Thermostat controller orchestration
│   ├── types.go           # Control decision types
│   ├── algorithm.go       # Core control algorithm
│   ├── evaluate.go        # Decision evaluation logic
│   ├── execute.go         # Command execution
│   ├── hard_override_job.go  # Hard override job
│   ├── home_status_fetcher.go  # Netatmo home status fetching
│   ├── mode_detection.go  # External modification detection
│   ├── sensors.go         # Sensor data retrieval
│   ├── room_processor.go  # Room-level processing
│   ├── metrics.go         # Control metrics push
│   ├── observability.go   # Logging and tracing helpers
│   ├── helpers.go         # Helper functions
│   └── *_test.go          # Control tests
├── power/
│   ├── scraper.go         # HTTP scraper for power meters
│   ├── poller.go          # Periodic polling logic
│   ├── types.go           # Power meter data types
│   └── *_test.go          # Tests
├── pyroscope/
│   └── profiler.go        # Pyroscope continuous profiling
├── otel/
│   ├── tracer.go          # OpenTelemetry tracer initialization
│   └── sampler.go         # Custom sampling logic
├── buffer/
│   ├── buffer.go          # Thread-safe ring buffer (dual buffers)
│   └── buffer_test.go
├── metrics/
│   ├── pusher.go          # Prometheus remote_write client
│   └── pusher_test.go
├── config.yaml            # Default configuration
├── example.env            # Environment variable examples
├── Dockerfile             # Multi-stage Docker build
├── Makefile               # Build and test commands
├── go.mod                 # Go module (requires 1.19+)
├── CLAUDE.md              # This file (service-level instructions)
└── README.md              # Detailed documentation
```

## Thermostat Control (Active Feature)

The service includes **intelligent thermostat control** that compensates for inaccurate Netatmo sensors by using accurate Xiaomi BLE sensors as the source of truth.

### How It Works

1. **Monitoring**: Reads accurate temperature from Xiaomi BLE sensors
2. **Comparison**: Compares against target temperature from schedule
3. **Compensation**: Calculates adjusted setpoint to compensate for Netatmo sensor offset
4. **Override**: Sends manual override to Netatmo API to achieve desired room temperature

### External Modification Detection

The system detects and respects manual thermostat changes to avoid fighting with user preferences:

**Detection Logic** - External modification is detected when **ALL** of these are true:
1. A command was previously sent (not first run)
2. At least 2 minutes have passed (allows API propagation)
3. Override duration has NOT expired (within expected override window)
4. Thermostat is in **manual mode** (not schedule mode)
5. Current setpoint differs from what was sent (>0.1°C)

**Safeguards**:
- ✅ **Schedule changes ignored**: When thermostat is in schedule mode, setpoint changes are expected (schedule changes throughout the day)
- ✅ **Expired overrides ignored**: After override duration expires, thermostat naturally returns to schedule without triggering detection
- ✅ **Manual changes respected**: When user manually changes temperature (switches to manual mode), automation pauses indefinitely

**Resume Condition**: Automation only resumes when thermostat is switched back to "schedule" mode

### Configuration Options

**Thermostat Control** (in `config.yaml`):
- `temperatureThreshold`: Minimum temperature difference to trigger action (default: 0.2°C)
- `overrideDurationMinutes`: How long manual overrides last (default: 10 minutes)
- `minSetpointCelsius`/`maxSetpointCelsius`: Safety limits (default: 10-30°C)
- `mappings`: List of room-to-sensor mappings (RoomName, SensorMAC, RoomID)
- `hardOverrides`: Time-based temperature overrides (schedule, days, targetTemperature)
- `dryRun`: Test mode without actually sending API commands (default: false)
- `metricJobEnabled`: Enable/disable metric job cron (default: false)
- `metricJobCron`: Cron schedule for metric job (default: "0 * * * * *")
- `controlJobEnabled`: Enable/disable control job cron (default: false)
- `controlJobCron`: Cron schedule for control job (default: "5 0,15,30,45 * * * *")
- `hardOverrideJobEnabled`: Enable/disable hard override job cron (default: false)
- `hardOverrideJobCron`: Cron schedule for hard override job (default: "0 * * * * *")

**Note**: At least one job must be enabled for thermostat control to function. Typically you want all three enabled.

### Metrics and Observability

All control decisions are logged with:
- `thermostat_mode`: Current mode (schedule/manual/away/hg)
- `xiaomi_temp`: Accurate temperature from BLE sensor
- `scheduled_temp`: Target temperature from schedule
- `setpoint_temp`: Current thermostat setpoint
- `thermostat_measured`: Temperature reported by Netatmo sensor
- `action`: Decision taken (skip/no_adjustment_needed/set_manual_override)
- `reason`: Human-readable explanation

Prometheus metrics include all temperature readings plus:
- `thermostat_control_action`: Control action taken (0=skip, 1=no_adjustment, 2=override)
- `thermostat_control_temperature_difference_celsius`: Xiaomi vs scheduled temp
- `thermostat_control_setpoint_adjustment_celsius`: Calculated adjustment
- Labels: `room_name`

## Configuration

The service uses `config.yaml` with environment variable overrides via cleanenv:

### Key Settings

**BLE Sensors**: List of LYWSD03MMC sensors with MAC addresses
**Netatmo**: OAuth2 credentials, fetch interval (60s default)
**Thermostat Control**: Temperature thresholds, control intervals, room mappings (see Thermostat Control section)
**Power Meter**: HTTP endpoint, scrape interval
**Pyroscope**: Continuous profiling configuration (CPU, memory, goroutines, mutex, block)
**OpenTelemetry**: Distributed tracing configuration (endpoint, protocol, sampling rates, OTLP/HTTP)
**Prometheus**: Push interval (30s), endpoint URL, credentials, buffer/batch sizes
**Logging**: Format (console/json/logfmt), level (debug/info/warn/error)

### Environment Variables

Critical secrets should be set via environment variables:
- `PROMETHEUS_PASSWORD`: Grafana Cloud API key
- `NETATMO_CLIENT_ID`: Netatmo OAuth2 client ID
- `NETATMO_CLIENT_SECRET`: Netatmo OAuth2 client secret
- `NETATMO_REFRESH_TOKEN`: Netatmo OAuth2 refresh token

**Pyroscope Configuration**:
- `PYROSCOPE_ENABLED`: Enable/disable Pyroscope profiling (true/false)
- `PYROSCOPE_SERVER_URL`: Pyroscope server URL (e.g., https://profiles-prod-XXX.grafana.net)
- `PYROSCOPE_BASIC_AUTH_USER`: Pyroscope basic auth username (Grafana Cloud instance ID)
- `PYROSCOPE_BASIC_AUTH_PASSWORD`: Pyroscope basic auth password (Grafana Cloud API key)

**OpenTelemetry Configuration**:
- `OTEL_ENABLED`: Enable/disable OpenTelemetry tracing (true/false)
- `OTEL_EXPORTER_OTLP_ENDPOINT`: OTLP endpoint URL (e.g., https://tempo-prod-XXX.grafana.net/otlp)
- `OTEL_EXPORTER_OTLP_PROTOCOL`: Protocol (http/https, default: https)
- `OTEL_EXPORTER_OTLP_HEADERS`: Headers in format "key1=value1,key2=value2" (for auth)
- `OTEL_SERVICE_NAME`: Service name for traces (default: home-controller)
- `OTEL_SAMPLING_RATE`: Default sampling rate 0.0-1.0 (default: 0.1)
- `OTEL_METRICS_SAMPLING_RATE`: Sampling rate for metrics operations (default: 0.01)

**Thermostat Control Job Flags**:
- `METRIC_JOB_ENABLED`: Enable/disable metric job cron (default: false)
- `CONTROL_JOB_ENABLED`: Enable/disable control job cron (default: false)
- `HARD_OVERRIDE_JOB_ENABLED`: Enable/disable hard override job cron (default: false)

These flags control individual thermostat control cron jobs. Set to `true` to enable specific jobs. At least one job must be enabled for thermostat control to function. All jobs are disabled by default for safety.

## Building and Running

### Local Development

```bash
cd home-controller
go build -o home-controller .
./home-controller -c config.yaml
```

### Docker Deployment

```bash
docker build -t home-controller .
docker run --rm \
  --network host \
  --privileged \
  -e DBUS_SYSTEM_BUS_ADDRESS=unix:path=/host/run/dbus/system_bus_socket \
  -e PROMETHEUS_PASSWORD=your-key \
  -v $(pwd)/config.yaml:/app/config.yaml \
  home-controller
```

**Important**:
- `--network host`: Required for BLE broadcasting and local network access
- `--privileged`: Required for BLE adapter access
- D-Bus socket: Required for BlueZ communication

### Docker Compose

Defined in project root `docker-compose.yml`:

```yaml
home-controller:
  build: home-controller
  network_mode: host
  privileged: true
  environment:
    - DBUS_SYSTEM_BUS_ADDRESS=unix:path=/host/run/dbus/system_bus_socket
  labels:
    io.balena.features.dbus: '1'
```

## Testing

```bash
# Run all tests
go test ./...

# Run with coverage
go test -v -race -coverprofile=coverage.out -covermode=atomic ./...

# Generate coverage report
go tool cover -func=coverage.out

# Format and vet
go fmt ./...
go vet ./...
```

## GitHub Workflow

CI/CD is configured via `.github/workflows/home-controller-test.yml`:
- Runs on PR to main and pushes to feature branches
- Go 1.25
- Runs tests, vet, generates coverage
- Uploads coverage artifacts

## Dependencies

Key external dependencies:
- `tinygo.org/x/bluetooth`: BLE scanning (passive mode)
- `github.com/ilyakaznacheev/cleanenv`: Config loading with env overrides
- `go.uber.org/zap`: Structured logging
- `github.com/prometheus/prometheus`: Protobuf/snappy for remote_write
- `github.com/gogo/protobuf`: Protobuf encoding
- `github.com/golang/snappy`: Compression
- `github.com/grafana/pyroscope-go`: Continuous profiling (CPU, memory, goroutines)
- `go.opentelemetry.io/otel`: OpenTelemetry distributed tracing
- `go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp`: OTLP/HTTP exporter

## Pyroscope Continuous Profiling

The service supports optional continuous profiling via Pyroscope (Grafana Cloud Profiles):

### Benefits

- **Performance Monitoring**: Track CPU usage, memory allocations, and goroutine counts
- **Memory Leak Detection**: Identify memory leaks through heap profiling over time
- **Production Debugging**: Understand production performance without impacting users
- **Historical Analysis**: Compare profiles across different time periods

### Configuration

Enable profiling by setting `pyroscope.enabled: true` in `config.yaml` and providing:
- Server URL (Grafana Cloud Profiles endpoint)
- Application name (defaults to "home-controller")
- Basic auth credentials for Grafana Cloud
- Profile types to collect (CPU, memory, goroutines, mutex, block)
- Optional: Mutex/block profiling rates for concurrency analysis

### Profile Types

- **cpu**: CPU time consumed by each function
- **alloc_objects/alloc_space**: Memory allocation tracking (objects and bytes)
- **inuse_objects/inuse_space**: Current memory usage (objects and bytes)
- **goroutines**: Number of goroutines over time
- **mutex**: Mutex contention (requires `mutexProfileRate > 0`)
- **block**: Blocking events (requires `blockProfileRate > 0`)

### Best Practices

- Start with CPU and memory profiling (alloc/inuse) for most use cases
- Enable mutex/block profiling only when debugging concurrency issues (adds overhead)
- Set `disableGCRuns: true` for high-volume memory tracking (reduces CPU overhead)
- Use tags (hostname, environment, version) for filtering in Pyroscope UI
- Monitor overhead in production (typically <5% with default settings)

## OpenTelemetry Distributed Tracing

The service supports optional distributed tracing via OpenTelemetry (Grafana Cloud Tempo):

### Benefits

- **Request Flow Visualization**: Track requests across multiple components (BLE scanner, aggregator, control jobs, Netatmo API)
- **Performance Analysis**: Identify bottlenecks and slow operations with span timing
- **Debugging**: Understand execution flow and component interactions
- **Service Health**: Monitor error rates and latency across the system

### Configuration

Enable tracing by setting `opentelemetry.enabled: true` in `config.yaml` and providing:
- OTLP endpoint (Grafana Cloud Tempo or other OTLP-compatible backend)
- Protocol (http/https)
- Service name (defaults to "home-controller")
- Sampling rates (default 10% for general operations, 1% for metrics operations)
- Optional: Custom resource attributes for filtering
- Optional: Authentication headers

### Sampling Strategy

The service uses a custom dual-rate sampler:
- **Default sampling** (10%): Applied to control jobs, Netatmo API calls, and other operations
- **Metrics sampling** (1%): Applied to high-frequency metrics operations to reduce overhead
- **Parent-based**: Child spans inherit parent's sampling decision for complete traces

### Traced Operations

Traces are automatically created for:
- **BLE Aggregator Job**: Weighted average calculations
- **Thermostat Control Jobs**: Metric job, control job, hard override job
- **Netatmo API Calls**: OAuth token refresh, home status fetch, setpoint commands
- **Ring Buffer Operations**: Reading and writing sensor data
- **Metrics Push**: Prometheus remote_write operations

### Best Practices

- Use low sampling rates (1-10%) in production to minimize overhead
- Increase sampling temporarily when debugging specific issues
- Use different sampling rates for high-frequency vs. low-frequency operations
- Add custom resource attributes (environment, version, hostname) for filtering in Tempo UI
- Monitor trace export overhead (typically <2% with 10% sampling)
- Correlate traces with logs using trace_id and span_id fields

## Current Status and Future Plans

### Implemented (Active)
- ✅ **Climate Monitoring**: BLE sensors, Netatmo, power meters
- ✅ **BLE Aggregator**: Weighted average calculation with 60-second time window
- ✅ **Dual Ring Buffers**: Separate buffers for control decisions and metrics
- ✅ **Thermostat Control**: Automated temperature control with sensor offset compensation
- ✅ **External Modification Detection**: Respects manual user overrides
- ✅ **Scheduled Jobs**: Cron-based job system for control, metrics, and hard overrides
- ✅ **Continuous Profiling**: Pyroscope integration for performance monitoring
- ✅ **Distributed Tracing**: OpenTelemetry integration with Grafana Tempo
- ✅ **Metrics Push**: All data to Prometheus/Grafana Cloud

### Future Enhancements
1. **Smart Scheduling**:
   - Energy price integration (adjust heating based on electricity costs)
   - Occupancy detection (reduce heating when rooms are empty)
   - Weather forecasts (pre-heat before cold snaps)
2. **ML-Based Optimization**:
   - Learn heating patterns and thermal characteristics of rooms
   - Predict optimal pre-heating times
   - Balance comfort vs. energy efficiency
3. **Multi-Room Coordination**:
   - Zone-based heating strategies
   - Heat distribution optimization across rooms

## Common Development Tasks

### Adding a New Sensor Type

1. Create package in `home-controller/<sensor-type>/`
2. Implement poller/scanner with readings → ring buffer
3. Update `main.go` to launch goroutine
4. Update `config.yaml` and `config/config.go`
5. Add tests

### Modifying Metrics Format

1. Update `types.go` for new fields
2. Modify `metrics/pusher.go` to encode new fields
3. Test with actual Prometheus endpoint
4. Update Grafana dashboards

### Debugging BLE Issues

- Check BlueZ: `hciconfig`
- Grant capabilities: `sudo setcap cap_net_admin+eip ./home-controller`
- Verify MAC addresses match ATC firmware sensors
- Check RSSI values in logs for signal strength

### Debugging Thermostat Control Issues

**Automation not taking action:**
1. Check if external modification detected:
   - Look for "external modification detected" in logs
   - Resume by switching thermostat to "schedule" mode in Netatmo app
2. Verify sensor data available:
   - Check logs for "sensor data unavailable"
   - Ensure BLE sensor is in range and broadcasting
3. Check temperature threshold:
   - Temperature difference must exceed `temperatureThreshold` (default 0.5°C)
   - View logs: `xiaomi_temp`, `scheduled_temp`, `diff`

**Automation incorrectly pausing:**
- Check `thermostat_mode` in logs (should show schedule/manual/away/hg)
- If mode is "schedule" and still detecting external mod → bug (report issue)
- If override duration expired → automation should resume automatically

**Understanding control decisions:**
- All decisions logged with full context:
  - `action`: skip/no_adjustment_needed/set_manual_override
  - `reason`: Human-readable explanation
  - `thermostat_mode`: Current mode
  - `xiaomi_temp`: Accurate sensor reading
  - `scheduled_temp`: Target from schedule
  - `thermostat_measured`: Netatmo sensor reading (often inaccurate)
  - `setpoint_temp`: What thermostat is currently set to

### Debugging OpenTelemetry Tracing

**Traces not appearing:**
1. Check if tracing is enabled:
   - Verify `OTEL_ENABLED=true` in environment
   - Check logs for "OpenTelemetry tracer initialized successfully"
2. Verify endpoint configuration:
   - Check `OTEL_EXPORTER_OTLP_ENDPOINT` is correct
   - Ensure headers include authentication (e.g., for Grafana Cloud)
   - Test endpoint connectivity
3. Check sampling rate:
   - Low sampling rates mean few traces are exported (expected)
   - Temporarily increase `OTEL_SAMPLING_RATE` to 1.0 for testing

**Understanding trace context:**
- All logs include `trace_id` and `span_id` when tracing is enabled
- Use trace_id to correlate logs with traces in Tempo UI
- Parent-child span relationships show operation flow
- Span attributes include job type, sensor info, room names

### Understanding BLE Aggregator

**How it works:**
- Runs on schedule (cron-based, typically every 60 seconds)
- Reads last 60 seconds of BLE readings from Control Buffer
- Calculates time-weighted average (recent readings weighted higher)
- Pushes aggregated readings to Metrics Buffer for Prometheus

**Debugging aggregator issues:**
1. Check logs for "completed BLE aggregation":
   - `sensors_processed`: Number of sensors with data
   - `reading_count`: Number of readings used per sensor
   - `weighted_average`: Calculated temperature
2. Verify sensors have recent readings:
   - Look for "no readings found for sensor in last 60 seconds"
   - Ensure BLE scanner is running and sensors are in range
3. Check trace_id for detailed execution flow

## Related Documentation

- [README.md](./README.md): Detailed setup and troubleshooting
- [Root CLAUDE.md](../CLAUDE.md): Project-level instructions
