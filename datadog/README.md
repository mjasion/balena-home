# Datadog Agent Configuration

This directory contains Datadog Agent configuration for monitoring the home automation stack.

## Overview

The Datadog Agent is configured to:
- **Collect logs** from all Docker containers via Balena socket
- **Receive traces** via OpenTelemetry Protocol (OTLP)
- **Monitor Docker containers** (CPU, memory, network, I/O) via Docker integration
- **Collect Docker events** (container start, stop, die, etc.)
- **Ship telemetry** to Datadog cloud

## Dual Telemetry Architecture

This project ships telemetry to **both Datadog and Grafana Cloud** using Grafana Alloy as a proxy:

```
┌──────────────────┐
│ home-controller  │
└────────┬─────────┘
         │ OTLP traces (HTTP :4318)
         ▼
┌──────────────────┐
│  Grafana Alloy   │◄─── Also collects Docker logs
│   (OTLP Proxy)   │
└────┬────────┬────┘
     │        │
     │        └──────────────────────┐
     │                               │
     ▼                               ▼
┌──────────────────┐      ┌──────────────────┐
│  Datadog Agent   │      │  Grafana Cloud   │
│   - Traces       │      │   - Traces       │
│   - Logs         │      │   - Logs         │
│   - Infra        │      │   - Metrics      │
└──────────────────┘      └──────────────────┘
```

### Grafana Cloud (via Alloy)
- **Traces**: home-controller → Alloy → Grafana Cloud Tempo (OTLP)
- **Logs**: All containers → Alloy → Grafana Cloud Loki
- **Metrics**: home-controller → Grafana Cloud Prometheus (direct push)

### Datadog (via Agent + Alloy)
- **Traces**: home-controller → Alloy → Datadog Agent → Datadog Cloud (OTLP)
- **Logs**: All containers → Datadog Agent → Datadog Cloud (Docker socket)
- **Infrastructure**: Container metrics, processes, system metrics → Datadog Cloud

## Configuration

### Environment Variables

Set these environment variables (via `.env` file or Balena dashboard):

```bash
# Datadog
DD_API_KEY=<your-datadog-api-key>
DD_SITE=datadoghq.com  # Use datadoghq.eu for EU, etc.

# Grafana Cloud - for Alloy to forward traces
GRAFANA_CLOUD_OTLP_ENDPOINT=https://otlp-gateway-prod-XX-YY.grafana.net/otlp
GRAFANA_CLOUD_INSTANCE_ID=<your-instance-id>
GRAFANA_CLOUD_API_KEY=<your-grafana-cloud-api-key>
```

See `.env.example` in the root directory for a complete list of required environment variables.

### Directory Structure

```
datadog/
├── README.md                           # This file
├── Dockerfile                          # Datadog Agent container
└── conf.d/                             # Check configurations
    ├── docker.d/
    │   └── conf.yaml                   # Docker integration (container metrics)
    └── openmetrics.d/
        └── conf.yaml                   # Prometheus metrics scraping (optional)
```

### Docker Integration Configuration

The `conf.d/docker.d/conf.yaml` file enables Docker monitoring on Balena:

- **Socket**: `unix:///var/run/balena-engine.sock` (via `io.balena.features.balena-socket` label)
- **Metrics**: Container CPU, memory, network, I/O, disk, exit codes
- **Events**: Container lifecycle events (start, stop, die, etc.)
- **Interval**: 15 seconds
- **Filtering**: Excludes `datadog-agent` itself
- **Tags**: Automatic extraction from Docker labels (`com.datadoghq.tags.*`, `io.balena.service.name`)

### OpenMetrics Configuration (Optional)

The `conf.d/openmetrics.d/conf.yaml` file configures Prometheus metrics scraping:

- **Endpoint**: Grafana Alloy metrics at `http://grafana-alloy:12345/metrics`
- **Namespace**: `home_automation` (prefixed to all metrics)
- **Interval**: 10 seconds
- **Tags**: `service:alloy`, `env:production`
- **Status**: Currently disabled (can be enabled by uncommenting)

## Balena-Specific Configuration

### Docker Socket Access

On Balena, the Docker socket is accessed via the Balena Engine socket at `/var/run/balena-engine.sock`. This is configured using:

1. **Label in docker-compose.yml**:
   ```yaml
   labels:
     io.balena.features.balena-socket: '1'
   ```
   This automatically mounts the Balena socket into the container.

2. **Environment variable**:
   ```yaml
   environment:
     - DOCKER_HOST=unix:///var/run/balena-engine.sock
   ```
   This tells the Datadog Agent where to find the Docker socket.

**Important**: Balena has restrictions on volume mounts. Instead of manually mounting `/var/run/docker.sock`, use the `io.balena.features.balena-socket` label which handles socket access properly on Balena devices.

### Why Host Network Mode?

The Datadog Agent uses `network_mode: host` to:
- Access the Balena Engine socket on the host
- Receive OTLP traces from Alloy on localhost:14317/14318
- Monitor host-level network metrics
- Avoid network namespace isolation issues

## How It Works

### Trace Collection

Traces flow through the system in this path:

1. **home-controller** generates OpenTelemetry traces during operation
2. Sends traces via **OTLP HTTP** to `localhost:4318` (Grafana Alloy)
3. **Grafana Alloy** receives traces and forwards to **two destinations**:
   - **Datadog Agent** at `datadog-agent:4317` (OTLP gRPC)
   - **Grafana Cloud Tempo** via OTLP with authentication
4. Both Datadog and Grafana Cloud receive the same traces

This architecture provides:
- **Single instrumentation** in home-controller
- **Dual visibility** in both platforms
- **No vendor lock-in** - can add/remove backends easily
- **Centralized trace routing** via Alloy

### Log Collection

Logs are automatically collected from all containers via Balena Engine socket:
- **Source**: Balena Engine API via `/var/run/balena-engine.sock`
- **Processing**: Datadog Agent parses container stdout/stderr
- **Tagging**: Automatic extraction from Docker labels
- **Filtering**: Excludes `datadog-agent` container itself

**Configuration via Environment Variables**:
```yaml
environment:
  - DD_LOGS_ENABLED=true                              # Enable log collection
  - DD_LOGS_CONFIG_CONTAINER_COLLECT_ALL=true         # Collect from all containers
  - DD_CONTAINER_EXCLUDE="name:datadog-agent"         # Exclude agent itself
  - DD_DOCKER_LABELS_AS_TAGS='{"com.datadoghq.tags.service":"service",...}'  # Extract tags from labels
```

**Note**: The `DD_DOCKER_LABELS_AS_TAGS` environment variable applies to both logs and metrics, automatically extracting tags from Docker container labels.

**Per-Service Configuration via Docker Labels**:
Services can add custom log configuration via Docker labels:
```yaml
labels:
  com.datadoghq.ad.logs: '[{"source": "app-name", "service": "app-name"}]'
  com.datadoghq.ad.tags: '["env:production", "team:engineering"]'
```

Example (already configured for `home-controller`):
```yaml
home-controller:
  labels:
    com.datadoghq.ad.logs: '[{"source": "home-controller", "service": "home-controller"}]'
    com.datadoghq.ad.tags: '["env:production", "service:home-automation"]'
```

### Docker Integration (Container Metrics)

The Docker integration monitors container performance metrics:

**What's Collected**:
- **Container metrics**: CPU, memory, network, I/O per container
- **Container counts**: Number of running/stopped containers
- **Image stats**: Image sizes, count
- **Volume stats**: Volume count and usage
- **Disk stats**: Container disk usage
- **Exit codes**: Container exit status codes
- **Events**: Container lifecycle events (start, stop, die, kill, etc.)

**Configuration**:
- **File**: `conf.d/docker.d/conf.yaml`
- **Socket**: `unix:///var/run/balena-engine.sock` (via `DOCKER_HOST` env var)
- **Interval**: 15 seconds
- **Label extraction**: Automatic tagging from `com.datadoghq.tags.*` and `io.balena.service.name` labels

**Tags Applied**:
All Docker metrics are tagged with:
- `env:production`
- `platform:balena`
- `device:balena-home`
- Container-specific labels (service, env, balena_service)

### Metrics Collection (Optional - Currently Disabled)

The OpenMetrics configuration is available but currently disabled to focus on logs and traces first. To enable Prometheus metrics scraping, uncomment the configuration in `conf.d/openmetrics.d/conf.yaml`.

When enabled, Datadog Agent scrapes:
1. **OpenMetrics Check**: Prometheus `/metrics` endpoints
2. **Container Metrics**: CPU, memory, network from Docker (always enabled)
3. **System Metrics**: Host-level metrics from `/proc` and `/sys/fs/cgroup` (always enabled)

### Infrastructure Monitoring

The agent monitors:
- **Containers**: All Docker containers (CPU, memory, I/O)
- **Processes**: Running processes on the host
- **System**: Host CPU, memory, disk, network

## Adding New Metrics Endpoints

To scrape metrics from additional services:

1. **Add service to `conf.d/openmetrics.d/conf.yaml`**:
```yaml
instances:
  - openmetrics_endpoint: 'http://service-name:port/metrics'
    namespace: 'home_automation'
    metrics:
      - '.*'  # Or specific metric patterns
    min_collection_interval: 10
    tags:
      - 'service:service-name'
      - 'env:production'
```

2. **Ensure service exposes Prometheus metrics**:
   - HTTP endpoint at `/metrics`
   - Prometheus text format
   - Accessible from `datadog-agent` container

## Datadog vs Grafana Cloud

### When to Use Datadog
- **APM/Tracing**: Distributed tracing with advanced features (service maps, flame graphs)
- **Log Search**: Advanced log parsing, indexing, and search
- **Alerting**: Complex alert rules and notifications
- **Dashboards**: Pre-built dashboards for Docker, Go, nginx
- **Infrastructure Map**: Visual representation of services and dependencies
- **Real-time Monitoring**: Live tail logs, real-time trace search

### When to Use Grafana Cloud
- **Traces**: Same OTLP traces as Datadog (dual visibility)
- **Custom Metrics**: Prometheus-native metrics from home-controller
- **Pyroscope Profiling**: Continuous profiling data (already integrated)
- **Custom Dashboards**: Existing Grafana dashboards
- **Long-term Storage**: Prometheus metrics retention
- **Cost-effective**: Better for long-term metric storage

### Recommended Workflow
- **Datadog**: Primary for logs, infrastructure monitoring, APM, and alerting
- **Grafana Cloud**: Primary for application metrics (climate, power, etc.), profiling, and long-term trends
- **Both**: Traces are available in both platforms for redundancy and comparison

## Troubleshooting

### Check Agent Status
```bash
docker exec datadog-agent agent status
```

### View Agent Logs
```bash
docker logs datadog-agent
```

### Test OpenMetrics Scraping
```bash
docker exec datadog-agent agent check openmetrics
```

### Verify Metrics Endpoint
```bash
curl http://grafana-alloy:12345/metrics
```

### Test Docker Integration
```bash
# Check Docker integration status
docker exec datadog-agent agent check docker

# Verify Docker socket access
docker exec datadog-agent ls -la /var/run/balena-engine.sock

# Check DOCKER_HOST environment variable
docker exec datadog-agent env | grep DOCKER_HOST
```

### Check Log Collection
```bash
# View log agent status
docker exec datadog-agent agent status | grep -A 20 "Logs Agent"

# Check which containers are being monitored
docker exec datadog-agent agent status | grep -A 50 "Log Sources"
```

## Security Notes

- **API Key**: Never commit `DD_API_KEY` to git (use `.env` or Balena secrets)
- **Read-only Mounts**: All host mounts are read-only for security
- **Container Isolation**: Agent runs in its own container
- **Network Access**: Agent needs network access to Datadog API endpoints

## Resources

- [Datadog Docker Integration](https://docs.datadoghq.com/containers/docker/)
- [Datadog OpenMetrics Integration](https://docs.datadoghq.com/integrations/openmetrics/)
- [Datadog Log Collection](https://docs.datadoghq.com/logs/log_collection/docker/)
- [Grafana Alloy Documentation](https://grafana.com/docs/alloy/)
