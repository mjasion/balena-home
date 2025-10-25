# Energy Meter Scraper

A Go service that scrapes energy meter HTTP endpoints, extracts active power metrics, and pushes them to Grafana Cloud Prometheus for monitoring and visualization.

## Features

- **Periodic HTTP scraping**: Fetches JSON data from energy meter devices at configurable intervals (default: 2 seconds)
- **Active power extraction**: Parses multi-sensor JSON responses and extracts all "activePower" readings
- **Prometheus integration**: Pushes metrics to Grafana Cloud using remote_write protocol (default: every 15 seconds)
- **Precise timing**: Optional even-second start for predictable scheduling
- **Flexible configuration**: YAML or TOML configuration with environment variable overrides
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

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `scrapeUrl` | string | Yes | - | HTTP endpoint to scrape energy meter data from |
| `scrapeIntervalSeconds` | int | No | 2 | How often to scrape the endpoint (in seconds) |
| `scrapeTimeoutSeconds` | float | No | 1.5 | HTTP request timeout for scraping |
| `pushIntervalSeconds` | int | No | 15 | How often to push metrics to Prometheus |
| `prometheusUrl` | string | Yes | - | Prometheus remote_write endpoint URL |
| `prometheusUsername` | string | Yes | - | Prometheus basic auth username |
| `prometheusPassword` | string | Yes | - | Prometheus basic auth password |
| `metricName` | string | No | active_power_watts | Name of the metric in Prometheus |
| `startAtEvenSecond` | bool | No | true | Start scraping at an even second (0, 2, 4, etc.) |
| `healthCheckPort` | int | No | 8080 | Port for health check HTTP server |

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

All configuration parameters can be overridden using environment variables:

- `SCRAPE_URL`
- `SCRAPE_INTERVAL_SECONDS`
- `SCRAPE_TIMEOUT_SECONDS`
- `PUSH_INTERVAL_SECONDS`
- `PROMETHEUS_URL`
- `PROMETHEUS_USERNAME`
- `PROMETHEUS_PASSWORD` (recommended for production)
- `METRIC_NAME`
- `START_AT_EVEN_SECOND`
- `HEALTH_CHECK_PORT`

Example:
```bash
export PROMETHEUS_PASSWORD="your-grafana-api-key"
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

1. **Create a Grafana Cloud account** at https://grafana.com/
2. **Get your Prometheus credentials**:
   - Navigate to your Grafana instance
   - Go to "Connections" → "Add new connection" → "Hosted Prometheus metrics"
   - Note your remote_write endpoint URL
   - Generate an API key (this is your password)
   - Your instance ID is the username
3. **Configure the service** with these credentials in `config.yaml` or environment variables
4. **Start the service** and verify metrics appear in Grafana Cloud

### Example Prometheus URL

```
https://prometheus-prod-01-eu-west-0.grafana.net/api/prom/push
```

Replace the region (`eu-west-0`) with your actual Grafana Cloud region.

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

The service logs to stdout with timestamps. Example log output:

```
2025/10/24 21:30:00 Loading configuration from config.yaml
2025/10/24 21:30:00 Configuration loaded successfully
2025/10/24 21:30:00 Starting health check server on :8080
2025/10/24 21:30:00 Waiting 1.2s to start at even second...
2025/10/24 21:30:02 Starting at second 2
2025/10/24 21:30:02 Service started - scraping every 2s, pushing every 15s
2025/10/24 21:30:02 Scrape successful in 123ms: 4 active power readings
2025/10/24 21:30:17 Successfully pushed metrics for 4 sensors
```

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
