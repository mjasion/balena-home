<!-- OPENSPEC:START -->
# OpenSpec Instructions

These instructions are for AI assistants working in this project.

Always open `@/openspec/AGENTS.md` when the request:
- Mentions planning or proposals (words like proposal, spec, change, plan)
- Introduces new capabilities, breaking changes, architecture shifts, or big performance/security work
- Sounds ambiguous and you need the authoritative spec before coding

Use `@/openspec/AGENTS.md` to learn:
- How to create and apply change proposals
- Spec format and conventions
- Project structure and guidelines

Keep this managed block so 'openspec update' can refresh the instructions.

<!-- OPENSPEC:END -->

# Thermostats BLE Monitoring Service

## Overview

This is a Go-based IoT monitoring service that collects temperature and humidity data from BLE (Bluetooth Low Energy) sensors and Netatmo thermostats, then pushes metrics to Prometheus/Grafana Cloud. The service includes comprehensive OpenTelemetry instrumentation for distributed tracing and metrics.

## Architecture

### Core Components

```
thermostats/
├── main.go                    # Entry point with OTel initialization
├── types.go                   # Core data types
├── config/
│   ├── config.go             # Configuration loading and validation
│   └── config_test.go        # Config tests
├── scanner/
│   ├── scanner.go            # BLE scanning (instrumented)
│   └── scanner_test.go
├── decoder/
│   ├── decoder.go            # ATC advertisement decoder
│   └── decoder_test.go
├── buffer/
│   ├── buffer.go             # Thread-safe ring buffer
│   └── buffer_test.go
├── metrics/
│   ├── pusher.go             # Prometheus remote_write (instrumented)
│   └── pusher_test.go
├── netatmo/
│   ├── client.go             # Netatmo API client (instrumented)
│   ├── fetcher.go            # Thermostat data fetcher
│   ├── poller.go             # Periodic polling
│   └── types.go              # API response types
├── telemetry/
│   ├── telemetry.go          # OpenTelemetry initialization
│   └── logger.go             # Trace context logger integration
├── config.yaml               # Main configuration
└── config.grafana-cloud.example.yaml  # Grafana Cloud example
```

### Data Flow

```
BLE Sensors → Scanner → Decoder → Ring Buffer → Pusher → Prometheus
Netatmo API → Fetcher → Poller → Ring Buffer → Pusher → Prometheus
                                                    ↓
                                          OpenTelemetry Traces
                                                    ↓
                                            Grafana Cloud Tempo
```

## OpenTelemetry Instrumentation

### Overview

The service has **deep OpenTelemetry instrumentation** across all major components:

1. **HTTP Clients**: Automatic instrumentation using `otelhttp`
2. **Business Operations**: Manual span instrumentation for key operations
3. **Context Propagation**: Trace context flows through all goroutines
4. **Runtime Metrics**: Go runtime statistics (goroutines, memory, GC)
5. **Custom Attributes**: Rich span attributes for debugging

### Instrumented Components

#### 1. HTTP Clients

**Netatmo Client** (`netatmo/client.go`)
- Automatic HTTP instrumentation with custom span names
- Spans for: token refresh, API requests, homes data, home status
- Attributes: HTTP method, URL, status code, token expiry
- Error recording with detailed context

**Prometheus Pusher** (`metrics/pusher.go`)
- Automatic HTTP instrumentation for remote_write
- Spans for: push operations, batch preparation, retries
- Attributes: reading counts, compression ratios, batch sizes
- Retry tracking with exponential backoff events

#### 2. BLE Scanner (`scanner/scanner.go`)

- Span for scanner initialization and start
- Child spans for each BLE advertisement processed
- Attributes: sensor MAC, name, ID, temperature, humidity, battery, RSSI
- Error tracking for decode failures

#### 3. Metrics Pusher (`metrics/pusher.go`)

- Spans for: Push, buildWriteRequest, buildBLETimeSeries, buildNetatmoTimeSeries, pushOnce
- Attributes: BLE vs Netatmo counts, time series counts, protobuf sizes, compression ratios
- Retry attempts tracked with events
- HTTP status codes recorded

#### 4. Context Propagation (`main.go`)

- Root span created in main: `main.run`
- Context propagated to all goroutines (scanner, poller, pusher)
- Graceful shutdown with trace context preserved

#### 5. Logger Integration (`telemetry/logger.go`)

- Helper functions to add trace IDs to log entries
- `InfoWithTrace`, `DebugWithTrace`, `WarnWithTrace`, `ErrorWithTrace`
- Automatic correlation between logs and traces

### Configuration Module API

The `config` package provides a unified configuration interface consistent across all services:

#### Loading Configuration
```go
cfg, err := config.Load("config.yaml")
```

#### Creating a Logger
```go
logger, err := cfg.NewLogger()
```

#### Logging Configuration (Secure)
```go
// Returns map with sensitive fields redacted
redacted := cfg.Redacted()

// Or print directly with structured logging
cfg.PrintConfig(logger)
```

These utility methods ensure:
- Consistent logger creation across services
- Secure logging of configuration (passwords/tokens masked)
- Easy debugging with structured log output

### OpenTelemetry Configuration

OpenTelemetry is configured in `config.yaml`:

```yaml
opentelemetry:
  enabled: true
  serviceName: "thermostats-ble"
  serviceVersion: "1.0.0"
  environment: "production"

  traces:
    enabled: true
    endpoint: ""  # Set via OTEL_EXPORTER_OTLP_TRACES_ENDPOINT
    headers: {}   # Set via OTEL_EXPORTER_OTLP_TRACES_HEADERS
    samplingRatio: 1.0
    batch:
      scheduleDelayMillis: 5000
      maxQueueSize: 2048
      maxExportBatchSize: 512

  metrics:
    enabled: true
    endpoint: ""  # Set via OTEL_EXPORTER_OTLP_METRICS_ENDPOINT
    headers: {}   # Set via OTEL_EXPORTER_OTLP_METRICS_HEADERS
    intervalMillis: 30000
    enableRuntimeMetrics: true

  resourceAttributes:
    deployment.datacenter: "home"
    host.type: "raspberry-pi"
```

### Environment Variables

**Traces Endpoint:**
```bash
export OTEL_EXPORTER_OTLP_TRACES_ENDPOINT="https://otlp-gateway-prod-us-central-0.grafana.net/otlp"
```

**Traces Authentication:**
```bash
# Generate base64 token:
echo -n "INSTANCE_ID:ACCESS_TOKEN" | base64

# Set header:
export OTEL_EXPORTER_OTLP_TRACES_HEADERS="Authorization=Basic YOUR_BASE64_TOKEN"
```

**Metrics (same format):**
```bash
export OTEL_EXPORTER_OTLP_METRICS_ENDPOINT="https://otlp-gateway-prod-us-central-0.grafana.net/otlp"
export OTEL_EXPORTER_OTLP_METRICS_HEADERS="Authorization=Basic YOUR_BASE64_TOKEN"
```

**Simplified (both traces and metrics):**
```bash
export OTEL_EXPORTER_OTLP_ENDPOINT="https://otlp-gateway-prod-us-central-0.grafana.net/otlp"
export OTEL_EXPORTER_OTLP_HEADERS="Authorization=Basic YOUR_BASE64_TOKEN"
```

## Configuration Note

**Important**: Configuration structure and environment variables are **unified across all services** (thermostats, pstryk_metric, etc.) for consistency and ease of deployment. The OpenTelemetry configuration follows the same pattern in all services.

## Building and Running

### Local Development

```bash
# Build
go build -o thermostats .

# Run with default config
./thermostats

# Run with custom config
./thermostats -c config.grafana-cloud.yaml

# Run with OpenTelemetry enabled
export OTEL_EXPORTER_OTLP_ENDPOINT="https://otlp-gateway-prod-us-central-0.grafana.net/otlp"
export OTEL_EXPORTER_OTLP_HEADERS="Authorization=Basic $(echo -n 'ID:TOKEN' | base64)"
./thermostats
```

### Docker

```bash
# Build
docker build -t thermostats .

# Run
docker run --network host \
  -e OTEL_EXPORTER_OTLP_ENDPOINT="..." \
  -e OTEL_EXPORTER_OTLP_HEADERS="..." \
  -v ./config.yaml:/app/config.yaml \
  thermostats
```

**Note**: `--network host` is required for BLE scanning (broadcasts need host network access).

### Docker Compose

```yaml
services:
  thermostats:
    build: .
    network_mode: host
    environment:
      - OTEL_EXPORTER_OTLP_ENDPOINT
      - OTEL_EXPORTER_OTLP_HEADERS
      - PROMETHEUS_PASSWORD
      - NETATMO_CLIENT_ID
      - NETATMO_CLIENT_SECRET
      - NETATMO_REFRESH_TOKEN
    volumes:
      - ./config.yaml:/app/config.yaml
    restart: unless-stopped
```

## Observability

### Viewing Traces in Grafana Cloud

1. Navigate to Grafana Cloud → Explore → Tempo
2. Search by service: `service.name="thermostats-ble"`
3. Filter by operation: `name="netatmo.GetHomeStatus"`
4. View trace details with all spans and attributes

### Key Traces to Monitor

- `main.run` - Full service execution
- `scanner.Start` - BLE initialization
- `scanner.ProcessAdvertisement` - Individual sensor readings
- `netatmo.refreshAccessToken` - OAuth token refresh
- `netatmo.GetHomeStatus` - Netatmo API calls
- `metrics.Push` - Prometheus push with retries
- `metrics.pushOnce` - Individual push attempts

### Span Attributes

**BLE Sensor:**
- `ble.mac`, `ble.sensor_name`, `ble.sensor_id`
- `ble.temperature_celsius`, `ble.humidity_percent`
- `ble.battery_percent`, `ble.rssi_dbm`

**Netatmo:**
- `netatmo.api`, `netatmo.home_id`, `netatmo.operation`
- `netatmo.homes_count`, `netatmo.rooms_count`
- `netatmo.token_needs_refresh`, `netatmo.token_expires_in_seconds`

**Metrics:**
- `metrics.total_readings`, `metrics.ble_readings`, `metrics.netatmo_readings`
- `metrics.time_series_count`, `metrics.successful_attempt`
- `metrics.protobuf_size_bytes`, `metrics.compressed_size_bytes`
- `metrics.compression_ratio`

### Runtime Metrics

When `enableRuntimeMetrics: true`:
- `runtime.go.goroutines` - Number of goroutines
- `runtime.go.mem.heap_alloc` - Heap memory allocated
- `runtime.go.gc.count` - GC runs
- `runtime.go.gc.pause_ns` - GC pause time

## Grafana Cloud Setup

See `config.grafana-cloud.example.yaml` for detailed setup instructions.

**Quick Start:**

1. Get credentials from Grafana Cloud
2. Set environment variables:
   ```bash
   export OTEL_EXPORTER_OTLP_ENDPOINT="https://otlp-gateway-prod-us-central-0.grafana.net/otlp"
   export OTEL_EXPORTER_OTLP_HEADERS="Authorization=Basic $(echo -n 'INSTANCE:TOKEN' | base64)"
   ```
3. Run service with `opentelemetry.enabled: true`
4. View traces in Grafana Cloud Tempo
5. View metrics in Grafana Cloud Prometheus

## Development Guidelines

### Adding New Instrumentation

When adding new features, follow these patterns:

**1. HTTP Clients:**
```go
import "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

client := &http.Client{
    Transport: otelhttp.NewTransport(http.DefaultTransport),
}
```

**2. Manual Spans:**
```go
import "go.opentelemetry.io/otel"

tracer := otel.Tracer("package-name")
ctx, span := tracer.Start(ctx, "operation.name")
defer span.End()

span.SetAttributes(attribute.String("key", "value"))
span.RecordError(err)
span.SetStatus(codes.Error, "error message")
```

**3. Context Propagation:**
```go
// Always pass context to child operations
go func(ctx context.Context) {
    // Child spans will inherit trace context
}(ctx)
```

**4. Logging with Traces:**
```go
import "github.com/mjasion/balena-home/thermostats/telemetry"

telemetry.InfoWithTrace(ctx, logger, "message", zap.String("key", "value"))
```

### Testing

```bash
# Run all tests
go test ./...

# Run with coverage
go test -cover ./...

# Run specific package tests
go test ./scanner
go test ./netatmo
go test ./metrics
```

### Configuration Validation

The configuration is validated on startup:
- BLE sensor MAC addresses format
- Unique sensor IDs and MACs
- Required Prometheus credentials
- OpenTelemetry endpoint when enabled
- Sampling ratio between 0 and 1

## Troubleshooting

**OpenTelemetry not sending traces:**
1. Check `opentelemetry.enabled: true` in config
2. Verify `OTEL_EXPORTER_OTLP_ENDPOINT` is set
3. Verify `OTEL_EXPORTER_OTLP_HEADERS` authentication is correct
4. Check logs for "OpenTelemetry providers initialized successfully"
5. Ensure no firewall blocks HTTPS to OTLP endpoint

**BLE scanning not working:**
1. Ensure Bluetooth adapter is available
2. Check BLE service is not claimed by other processes
3. Verify MAC addresses in config match sensor MACs
4. Use `hcitool lescan` to verify sensors are broadcasting

**Netatmo API failures:**
1. Check OAuth credentials are correct
2. Verify refresh token is not expired
3. Check network connectivity to api.netatmo.com
4. Review token refresh traces in Grafana

## Dependencies

### Key Libraries

- `go.uber.org/zap` - Structured logging
- `go.opentelemetry.io/otel` - OpenTelemetry SDK
- `go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp` - HTTP instrumentation
- `go.opentelemetry.io/contrib/instrumentation/runtime` - Runtime metrics
- `tinygo.org/x/bluetooth` - BLE scanning
- `github.com/prometheus/prometheus/prompb` - Prometheus remote_write
- `github.com/ilyakaznacheev/cleanenv` - Configuration loading

### Go Version

Requires Go 1.19 or later (uses generics and modern standard library features).

## Performance Considerations

- **Sampling**: Adjust `samplingRatio` for high-volume environments (e.g., 0.1 for 10%)
- **Batch Size**: Configure `batch.maxExportBatchSize` based on network conditions
- **Buffer Size**: Ring buffer capacity set via `prometheus.bufferSize`
- **Runtime Metrics**: Minimal overhead with `enableRuntimeMetrics: true`

## Security

- **Credentials**: Use environment variables for sensitive data
- **Authentication**: Basic auth for Prometheus, OAuth2 for Netatmo, Bearer for OTLP
- **Network**: HTTPS enforced for all external communications
- **Validation**: All configuration validated before service start