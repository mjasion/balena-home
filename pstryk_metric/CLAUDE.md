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

# pstryk_metric - Energy Meter Scraper

## Overview

A Go service that scrapes energy meter HTTP endpoints and pushes metrics to Grafana Cloud Prometheus. The service is instrumented with OpenTelemetry for distributed tracing and metrics collection.

## Project Structure

```
pstryk_metric/
  main.go           # Entry point with OpenTelemetry initialization
  config/
    config.go       # Configuration loading and validation
  scraper/
    scraper.go      # HTTP scraping logic
  metrics/
    pusher.go       # Prometheus remote_write client
    health.go       # Health check HTTP server
  buffer/
    buffer.go       # Ring buffer for metric batching
  telemetry/
    telemetry.go    # OpenTelemetry providers (traces & metrics)
    logger.go       # Context-aware logging with trace correlation
```

## Key Features

- **HTTP Scraping**: Periodically scrapes JSON endpoints for energy meter data
- **Prometheus Integration**: Pushes metrics to Grafana Cloud via remote_write
- **OpenTelemetry**: Full instrumentation for traces and metrics
- **Health Checks**: HTTP endpoint for service health monitoring
- **Ring Buffer**: In-memory buffering for batched metric pushing

## Configuration

The service uses YAML configuration with environment variable overrides (via cleanenv).

**Note**: Configuration structure and environment variables are **unified across all services** (pstryk_metric, thermostats, etc.) for consistency and ease of deployment.

### Core Configuration
- `scrapeUrl`: HTTP endpoint to scrape
- `scrapeIntervalSeconds`: How often to scrape (default: 2s)
- `pushIntervalSeconds`: How often to push metrics (default: 30s)
- `prometheusUrl`: Grafana Cloud Prometheus endpoint
- `prometheusUsername` / `prometheusPassword`: Grafana Cloud credentials

### Logging Configuration
- `logging.logFormat`: "console", "json", or "logfmt"
- `logging.logLevel`: "debug", "info", "warn", "error"

### OpenTelemetry Configuration
- `opentelemetry.enabled`: Enable/disable OpenTelemetry (default: false)
- `opentelemetry.serviceName`: Service name for traces/metrics
- `opentelemetry.serviceVersion`: Version tag
- `opentelemetry.environment`: Environment (production/staging/dev)

#### Traces Configuration
- `opentelemetry.traces.enabled`: Enable trace collection
- `opentelemetry.traces.endpoint`: OTLP endpoint (e.g., `https://otlp-gateway-prod-us-central-0.grafana.net/otlp`)
- `opentelemetry.traces.headers`: Authentication headers (or use `OTEL_EXPORTER_OTLP_TRACES_HEADERS` env var)
- `opentelemetry.traces.samplingRatio`: Sampling rate (1.0 = 100%)
- `opentelemetry.traces.batch.*`: Batch processor configuration

#### Metrics Configuration
- `opentelemetry.metrics.enabled`: Enable OTel metrics collection
- `opentelemetry.metrics.endpoint`: OTLP metrics endpoint
- `opentelemetry.metrics.intervalMillis`: Collection interval
- `opentelemetry.metrics.enableRuntimeMetrics`: Go runtime metrics

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

## OpenTelemetry Instrumentation

### Traces

The service creates spans for:
- **scrape**: Each scrape operation with attributes:
  - `duration_ms`: Scrape duration
  - `reading_count`: Number of readings extracted
- **push**: Each push operation with attributes:
  - `buffer_size`: Number of results in buffer
  - `cleared_results`: Number of results pushed

### Logging with Trace Context

The service uses context-aware logging that automatically adds `trace_id` and `span_id` to log entries when tracing is active:

```go
// Logs include trace context automatically
telemetry.InfoWithTrace(ctx, logger, "Scrape successful", zap.Int("count", len(results)))
```

### Metrics

When enabled, OpenTelemetry automatically collects:
- Go runtime metrics (goroutines, memory, GC)
- Custom application metrics via the global meter provider

## Grafana Cloud Setup

### For Prometheus Metrics
1. Get credentials from Grafana Cloud → Prometheus
2. Set `prometheusUrl`, `prometheusUsername`, `prometheusPassword` in config

### For OpenTelemetry Traces
1. Go to Grafana Cloud → Tempo
2. Get OTLP endpoint (format: `https://otlp-gateway-{region}.grafana.net/otlp`)
3. Generate access policy token
4. Set via environment variable:
   ```bash
   export OTEL_EXPORTER_OTLP_TRACES_ENDPOINT="https://otlp-gateway-prod-us-central-0.grafana.net/otlp"
   export OTEL_EXPORTER_OTLP_TRACES_HEADERS="Authorization=Basic $(echo -n 'instanceId:token' | base64)"
   ```

### For OpenTelemetry Metrics (Optional)
Similar to traces, but using `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` and `OTEL_EXPORTER_OTLP_METRICS_HEADERS`.

## Development

### Building
```bash
go build -o pstryk_metric .
```

### Running
```bash
./pstryk_metric -c config.yaml
```

### Testing
The service includes health checks at `http://localhost:8080/health` for monitoring.

## Common Development Tasks

### Adding New Spans
Use the tracer from `otel.Tracer("pstryk-metric")`:

```go
ctx, span := tracer.Start(ctx, "operation-name")
defer span.End()

// Add attributes
span.SetAttributes(attribute.String("key", "value"))

// Record errors
if err != nil {
    span.RecordError(err)
    span.SetStatus(codes.Error, err.Error())
}
```

### Context-Aware Logging
Always use the telemetry logging functions to include trace context:

```go
telemetry.InfoWithTrace(ctx, logger, "message", zap.String("field", value))
telemetry.ErrorWithTrace(ctx, logger, "error occurred", zap.Error(err))
```

### Disabling OpenTelemetry
Set `opentelemetry.enabled: false` in config.yaml, or:
```bash
export OTEL_ENABLED=false
```