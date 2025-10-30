# Energy Meter Scraper

A Go service that scrapes energy meter HTTP endpoints, extracts active power metrics, and pushes them to Grafana Cloud Prometheus for monitoring and visualization.

## Features

- **Periodic HTTP scraping**: Fetches JSON data from energy meter devices at configurable intervals (default: 2 seconds)
- **Active power extraction**: Parses multi-sensor JSON responses and extracts all "activePower" readings
- **Prometheus integration**: Pushes metrics to Grafana Cloud using remote_write protocol (default: every 30 seconds)
- **OpenTelemetry instrumentation**: Full distributed tracing and metrics collection with Grafana Cloud Tempo integration
- **Context-aware logging**: Automatic trace ID correlation in logs for better observability
- **Precise timing**: Optional even-second start for predictable scheduling
- **Flexible configuration**: YAML configuration with environment variable overrides
- **Health check endpoint**: HTTP endpoint for monitoring service health
- **Resilient operation**: Automatic retries with exponential backoff for network failures
- **In-memory buffering**: Ring buffer stores recent readings for batched pushing

## Installation

### Prerequisites

- Go 1.19 or later
- Access to an energy meter HTTP endpoint returning JSON data
- Grafana Cloud account with Prometheus instance

### Build from Source

```bash
cd pstryk_metric
go build -o pstryk_metric .
```

## Configuration

The service supports both YAML and TOML configuration formats. Create a `config.yaml` or `config.toml` file in the working directory, or specify a custom path with the `-c` flag.

### Configuration Parameters

#### Core Parameters

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `scrapeUrl` | string | Yes | - | HTTP endpoint to scrape energy meter data from |
| `scrapeIntervalSeconds` | int | No | 2 | How often to scrape the endpoint (in seconds) |
| `scrapeTimeoutSeconds` | float | No | 1.5 | HTTP request timeout for scraping |
| `pushIntervalSeconds` | int | No | 30 | How often to push metrics to Prometheus |
| `prometheusUrl` | string | Yes | - | Prometheus remote_write endpoint URL |
| `prometheusUsername` | string | Yes | - | Prometheus basic auth username |
| `prometheusPassword` | string | Yes | - | Prometheus basic auth password |
| `metricName` | string | No | active_power_watts | Name of the metric in Prometheus |
| `startAtEvenSecond` | bool | No | true | Start scraping at an even second (0, 2, 4, etc.) |
| `bufferSize` | int | No | 5000 | Ring buffer size for storing readings |
| `healthCheckPort` | int | No | 8080 | Port for health check HTTP server |

#### Logging Parameters

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `logging.logFormat` | string | No | console | Log format: "console", "json", or "logfmt" |
| `logging.logLevel` | string | No | info | Log level: "debug", "info", "warn", "error" |

#### OpenTelemetry Parameters

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `opentelemetry.enabled` | bool | No | false | Enable OpenTelemetry instrumentation |
| `opentelemetry.serviceName` | string | No | pstryk-metric | Service name for traces/metrics |
| `opentelemetry.serviceVersion` | string | No | 1.0.0 | Service version |
| `opentelemetry.environment` | string | No | production | Deployment environment |
| `opentelemetry.traces.enabled` | bool | No | true | Enable trace collection |
| `opentelemetry.traces.endpoint` | string | No* | - | OTLP traces endpoint (e.g., otlp-gateway-prod-us-central-0.grafana.net) |
| `opentelemetry.traces.samplingRatio` | float | No | 1.0 | Trace sampling ratio (0.0-1.0) |
| `opentelemetry.metrics.enabled` | bool | No | true | Enable OpenTelemetry metrics |
| `opentelemetry.metrics.endpoint` | string | No* | - | OTLP metrics endpoint |
| `opentelemetry.metrics.intervalMillis` | int | No | 30000 | Metrics collection interval |
| `opentelemetry.metrics.enableRuntimeMetrics` | bool | No | true | Enable Go runtime metrics |

*Required when OpenTelemetry is enabled, or set via environment variables

### Example Configuration (YAML)

```yaml
# Energy Meter Scraper Configuration
scrapeUrl: "http://192.168.1.100/api/sensor"
scrapeIntervalSeconds: 2
scrapeTimeoutSeconds: 1.5
pushIntervalSeconds: 15

# Prometheus / Grafana Cloud settings
prometheusUrl: "https://prometheus-prod-01-eu-west-0.grafana.net/api/prom/push"
prometheusUsername: "123456"
prometheusPassword: "secret"

# Metric configuration
metricName: "active_power_watts"
startAtEvenSecond: true
healthCheckPort: 8080
```

### Example Configuration (TOML)

```toml
# Energy Meter Scraper Configuration
scrapeUrl = "http://192.168.1.100/api/sensor"
scrapeIntervalSeconds = 2
scrapeTimeoutSeconds = 1.5
pushIntervalSeconds = 15

# Prometheus / Grafana Cloud settings
prometheusUrl = "https://prometheus-prod-01-eu-west-0.grafana.net/api/prom/push"
prometheusUsername = "123456"
prometheusPassword = "secret"

# Metric configuration
metricName = "active_power_watts"
startAtEvenSecond = true
healthCheckPort = 8080
```

### Environment Variable Overrides

All configuration parameters can be overridden using environment variables. See `example.env` for a complete list.

#### Core Variables
- `SCRAPE_URL`
- `SCRAPE_INTERVAL_SECONDS`
- `SCRAPE_TIMEOUT_SECONDS`
- `PUSH_INTERVAL_SECONDS`
- `PROMETHEUS_URL`
- `PROMETHEUS_USERNAME`
- `PROMETHEUS_PASSWORD` (recommended for production)
- `METRIC_NAME`
- `START_AT_EVEN_SECOND`
- `BUFFER_SIZE`
- `HEALTH_CHECK_PORT`

#### Logging Variables
- `LOG_FORMAT` (console, json, logfmt)
- `LOG_LEVEL` (debug, info, warn, error)

#### OpenTelemetry Variables
- `OTEL_ENABLED`
- `OTEL_SERVICE_NAME`
- `OTEL_SERVICE_VERSION`
- `OTEL_ENVIRONMENT`
- `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`
- `OTEL_EXPORTER_OTLP_TRACES_HEADERS`
- `OTEL_TRACES_SAMPLING_RATIO`
- `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`
- `OTEL_EXPORTER_OTLP_METRICS_HEADERS`
- `OTEL_METRICS_INTERVAL`
- `OTEL_ENABLE_RUNTIME_METRICS`

Example:
```bash
export PROMETHEUS_PASSWORD="your-grafana-api-key"
export OTEL_ENABLED=true
export OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=otlp-gateway-prod-us-central-0.grafana.net
export OTEL_EXPORTER_OTLP_TRACES_HEADERS="Authorization=Basic $(echo -n 'instanceId:token' | base64)"
./pstryk_metric -c config.yaml
```

## Usage

### Basic Usage

```bash
./pstryk_metric
```

This loads configuration from `config.yaml` in the current directory.

### Custom Configuration File

```bash
./pstryk_metric -c /path/to/custom-config.yaml
```

### With Environment Variables

```bash
export PROMETHEUS_PASSWORD="your-secret-key"
export SCRAPE_INTERVAL_SECONDS=5
./pstryk_metric -c config.yaml
```

## Grafana Cloud Setup

### Prometheus Metrics Setup

1. **Create a Grafana Cloud account** at https://grafana.com/
2. **Get your Prometheus credentials**:
   - Navigate to your Grafana instance
   - Go to "Connections" → "Add new connection" → "Hosted Prometheus metrics"
   - Note your remote_write endpoint URL
   - Generate an API key (this is your password)
   - Your instance ID is the username
3. **Configure the service** with these credentials in `config.yaml` or environment variables

Example Prometheus URL:
```
https://prometheus-prod-01-eu-west-0.grafana.net/api/prom/push
```

### OpenTelemetry Traces Setup (Optional)

Enable distributed tracing to visualize request flows and identify performance bottlenecks:

1. **Enable Tempo in Grafana Cloud**:
   - Go to your Grafana Cloud portal
   - Navigate to "Tempo" or "Traces"
   - Note your OTLP endpoint (e.g., `otlp-gateway-prod-us-central-0.grafana.net`)

2. **Generate Access Token**:
   - Go to "Access Policies" or "API Keys"
   - Create a new token with traces write permissions
   - Copy the token value

3. **Configure OpenTelemetry**:

   Add to `config.yaml`:
   ```yaml
   opentelemetry:
     enabled: true
     serviceName: "pstryk-metric"
     serviceVersion: "1.0.0"
     environment: "production"
     traces:
       enabled: true
       endpoint: "otlp-gateway-prod-us-central-0.grafana.net"
   ```

   Or use environment variables:
   ```bash
   export OTEL_ENABLED=true
   export OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=otlp-gateway-prod-us-central-0.grafana.net
   export OTEL_EXPORTER_OTLP_TRACES_HEADERS="Authorization=Basic $(echo -n 'instanceId:token' | base64)"
   ```

4. **Verify Traces**:
   - Start the service
   - Check logs for "OpenTelemetry providers initialized successfully"
   - View traces in Grafana Cloud → Tempo

### What You'll See in Grafana

With OpenTelemetry enabled, you'll get:
- **Distributed traces** showing scrape and push operations
- **Trace-to-logs correlation** (trace IDs in log entries)
- **Performance metrics** (operation durations, error rates)
- **Service topology** visualization
- **Go runtime metrics** (goroutines, memory, GC stats)

## Energy Meter JSON Format

The service expects the energy meter to return JSON in this format:

```json
{
  "multiSensor": {
    "sensors": [
      {
        "id": 0,
        "type": "activePower",
        "value": 237,
        "state": 2
      },
      {
        "id": 1,
        "type": "activePower",
        "value": 30,
        "state": 2
      }
    ]
  }
}
```

Only sensors with `type: "activePower"` are extracted and pushed to Prometheus.

## Metrics

The service creates Prometheus metrics in the following format:

```
active_power_watts{sensor_id="0"} 237
active_power_watts{sensor_id="1"} 30
active_power_watts{sensor_id="2"} 188
active_power_watts{sensor_id="3"} 19
```

Each sensor is identified by its `sensor_id` label, allowing you to:
- Monitor individual circuits
- Create alerts for specific sensors
- Aggregate total power consumption
- Visualize per-circuit trends

## Health Check

The service exposes a health check endpoint on `/health` (default port: 8080).

### Health Check Response

```json
{
  "status": "healthy",
  "lastScrapeTime": "2025-10-24T21:30:00Z",
  "lastPushTime": "2025-10-24T21:30:00Z",
  "bufferedSamples": 5
}
```

### Health Status Determination

- **Healthy (HTTP 200)**: Last scrape succeeded within 2x the scrape interval
- **Unhealthy (HTTP 503)**: Scraping has been failing for more than 2x the scrape interval

### Check Health

```bash
curl http://localhost:8080/health
```

## Logging

The service supports multiple log formats and includes trace correlation when OpenTelemetry is enabled.

### Log Formats

- **console**: Human-readable (default for development)
- **json**: Structured JSON logs (recommended for production)
- **logfmt**: Key-value format compatible with tools like Promtail

### Example Log Output (Console)

```
2025-10-30T12:00:00.000Z  INFO  Loading configuration  path=config.yaml
2025-10-30T12:00:00.001Z  INFO  Configuration loaded successfully
2025-10-30T12:00:00.002Z  INFO  OpenTelemetry providers initialized successfully
2025-10-30T12:00:00.003Z  INFO  Service started  scrapeIntervalSeconds=2  pushIntervalSeconds=30
2025-10-30T12:00:02.123Z  INFO  Scrape successful  duration=123ms  readingCount=4  trace_id=a1b2c3d4e5f6  span_id=1234567890ab
2025-10-30T12:00:32.456Z  INFO  Push operation completed  clearedResults=60  trace_id=f6e5d4c3b2a1  span_id=9876543210cd
```

### Trace Correlation

When OpenTelemetry is enabled, all logs automatically include:
- `trace_id`: Distributed trace identifier
- `span_id`: Current span identifier

This allows you to:
- Jump from logs to traces in Grafana
- See all logs for a specific request
- Correlate errors across services

## Troubleshooting

### Connection Refused to Energy Meter

**Symptom**: `HTTP request failed: connection refused`

**Solutions**:
- Verify the energy meter IP address and port
- Check network connectivity: `ping 192.168.1.100`
- Ensure the energy meter HTTP interface is enabled
- Check firewall rules

### Authentication Failed to Grafana Cloud

**Symptom**: `push failed with status 401`

**Solutions**:
- Verify your Prometheus username (instance ID) is correct
- Check that your API key (password) is valid
- Regenerate API key in Grafana Cloud if expired
- Ensure you're using environment variable for password

### No Active Power Sensors Found

**Symptom**: `Warning: no activePower sensors found in response`

**Solutions**:
- Verify the energy meter JSON response format
- Check that sensors have `type: "activePower"` (exact spelling)
- Test the endpoint manually: `curl http://192.168.1.100/api/sensor`

### Service Starts at Wrong Time

**Symptom**: Scraping doesn't start at even seconds

**Solutions**:
- Ensure `startAtEvenSecond: true` in config
- Check system clock is synchronized (use NTP)
- Review logs for "Starting at second X" message

### Buffer Full Warning

**Symptom**: `Warning: ring buffer full, dropping oldest entry`

**Solutions**:
- Decrease scrape interval
- Increase push interval
- Check Prometheus push is succeeding
- Verify network connectivity to Grafana Cloud

## Docker Deployment

Create a `Dockerfile`:

```dockerfile
FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY . .
RUN go build -o pstryk_metric .

FROM alpine:latest
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /app
COPY --from=builder /app/pstryk_metric .
COPY config.yaml .
CMD ["./pstryk_metric"]
```

Build and run:

```bash
docker build -t pstryk_metric .
docker run -d \
  -e PROMETHEUS_PASSWORD="your-api-key" \
  -p 8080:8080 \
  --name pstryk_metric \
  pstryk_metric
```

## License

MIT License - see LICENSE file for details

## Contributing

Contributions are welcome! Please open an issue or pull request.
