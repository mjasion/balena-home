# Home Controller Service

## Important Instructions for AI Assistants

**CRITICAL: Git Commit Policy**

- **NEVER commit changes automatically** without explicit user request
- **NEVER create commits** unless the user specifically asks you to commit
- **ALWAYS ask for permission** before creating any git commits
- Only create commits when the user explicitly says "commit", "create a commit", "git commit", or similar direct requests
- Making changes to files does NOT imply the user wants those changes committed
- This is a production home monitoring system - commits must be intentional and reviewed

**When User Requests a Commit:**
- Follow the standard git commit workflow (see root CLAUDE.md)
- Include proper commit messages with context
- Add Co-Authored-By footer for Claude contributions

## Overview

The **home-controller** service is a comprehensive home climate and energy monitoring system that:
- Monitors BLE temperature sensors (LYWSD03MMC with ATC firmware)
- Monitors energy consumption from power meters
- Pushes all metrics to Prometheus/Grafana Cloud

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
         ├──────────────┬──────────────┬──────────────┐
         ▼              ▼              ▼              ▼
┌──────────────┐ ┌──────────────┐ ┌──────────────┐ ┌──────────────┐
│ BLE Scanner  │ │ BLE          │ │ Power Meter  │ │ Metrics      │
│ (scanner/)   │ │ Aggregator   │ │ Scraper      │ │ Pusher       │
│              │ │ (aggregator/)│ │ (power/)     │ │ (metrics/)   │
│ - Passive    │ │ - Weighted   │ │ - HTTP       │ │ - Protobuf   │
│   BLE scan   │ │   averages   │ │   polling    │ │ - Snappy     │
│ - ATC decode │ │ - 60s window │ │ - Energy     │ │ - Batch push │
│ - MAC filter │ │ - Per sensor │ │   metrics    │ │ - Remote     │
│              │ │ - Scheduled  │ │ - Scheduled  │ │   write API  │
└──────────────┘ └──────────────┘ └──────────────┘ └──────────────┘
         │              │              │              │
         └──────────────┴──────────────┴──────────────┘
                        │
                        ▼
               ┌─────────────────┐          ┌─────────────────┐
               │  Dual Buffers    │          │ OpenTelemetry   │
               │  (buffer/)       │          │ (otel/)         │
               │                  │          │ - Tracing       │
               │ Control Buffer:  │          │ - Sampling      │
               │ - BLE readings   │          │ - OTLP/HTTP     │
               │ - Real-time data │          │ - Tempo export  │
               │                  │          └─────────────────┘
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
   - Pushes raw readings to **Control Buffer** (raw real-time data for the aggregator)
2. **BLE Aggregator**: Scheduled job (cron) that calculates weighted averages
   - Reads last 60 seconds of BLE readings from Control Buffer
   - Calculates time-weighted average per sensor (recent readings weighted higher)
   - Pushes aggregated readings to **Metrics Buffer** for Prometheus
3. **Power Meter Scraper**: Polls HTTP endpoints for energy consumption metrics
   - Pushes readings to Metrics Buffer
4. **Dual Ring Buffers** (thread-safe, 100K capacity each):
   - **Control Buffer**: Real-time BLE readings for the aggregator
   - **Metrics Buffer**: Aggregated data for Prometheus push
5. **Metrics Pusher**: Batch pushes to Prometheus every 30 seconds from Metrics Buffer
6. **Pyroscope Profiler** (optional): Continuous profiling of CPU, memory, goroutines to Grafana Cloud
7. **OpenTelemetry Tracer** (optional): Distributed tracing exported to Grafana Tempo via OTLP/HTTP

## Project Structure

```
home-controller/
├── main.go                # Entry point, orchestration, goroutine management
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

## Configuration

The service uses `config.yaml` with environment variable overrides via cleanenv:

### Key Settings

**BLE Sensors**: List of LYWSD03MMC sensors with MAC addresses
**Power Meter**: HTTP endpoint, scrape interval
**Pyroscope**: Continuous profiling configuration (CPU, memory, goroutines, mutex, block)
**OpenTelemetry**: Distributed tracing configuration (endpoint, protocol, sampling rates, OTLP/HTTP)
**Prometheus**: Push interval (30s), endpoint URL, credentials, buffer/batch sizes
**Logging**: Format (console/json/logfmt), level (debug/info/warn/error)

### Environment Variables

Critical secrets should be set via environment variables:
- `PROMETHEUS_PASSWORD`: Grafana Cloud API key

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

- **Request Flow Visualization**: Track requests across multiple components (BLE scanner, aggregator, power poller, metrics push)
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
- **Default sampling** (10%): Applied to scheduled jobs and other operations
- **Metrics sampling** (1%): Applied to high-frequency metrics operations to reduce overhead
- **Parent-based**: Child spans inherit parent's sampling decision for complete traces

### Traced Operations

Traces are automatically created for:
- **BLE Aggregator Job**: Weighted average calculations
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
- ✅ **Climate Monitoring**: BLE sensors, power meters
- ✅ **BLE Aggregator**: Weighted average calculation with 60-second time window
- ✅ **Dual Ring Buffers**: Separate buffers for raw BLE readings and metrics
- ✅ **Scheduled Jobs**: Cron-based job system for power polling, BLE aggregation, and metrics push
- ✅ **Continuous Profiling**: Pyroscope integration for performance monitoring
- ✅ **Distributed Tracing**: OpenTelemetry integration with Grafana Tempo
- ✅ **Metrics Push**: All data to Prometheus/Grafana Cloud

## Common Development Tasks

### Adding a New Sensor Type

1. Create package in `home-controller/<sensor-type>/`
2. Implement poller/scanner with readings → ring buffer
3. Update `main.go` to launch goroutine
4. Update `config.yaml` and `config/config.go`
5. Add tests

### Modifying Metrics Format

1. Update reading types in `buffer/buffer.go` for new fields
2. Modify `metrics/pusher.go` to encode new fields
3. Test with actual Prometheus endpoint
4. Update Grafana dashboards

### Debugging BLE Issues

- Check BlueZ: `hciconfig`
- Grant capabilities: `sudo setcap cap_net_admin+eip ./home-controller`
- Verify MAC addresses match ATC firmware sensors
- Check RSSI values in logs for signal strength

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
- Span attributes include job type, sensor info

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
