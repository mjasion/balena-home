# Datadog Agent Configuration

This directory contains Datadog Agent configuration for monitoring the home automation stack.

## Overview

The Datadog Agent is configured to:
- **Collect logs** from all Docker containers
- **Receive traces** via OpenTelemetry Protocol (OTLP)
- **Monitor container performance** (CPU, memory, network)
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
└── conf.d/                             # Check configurations
    └── openmetrics.d/
        └── conf.yaml                   # Prometheus metrics scraping
```

### OpenMetrics Configuration

The `conf.d/openmetrics.d/conf.yaml` file configures Prometheus metrics scraping:

- **Endpoint**: Grafana Alloy metrics at `http://grafana-alloy:12345/metrics`
- **Namespace**: `home_automation` (prefixed to all metrics)
- **Interval**: 10 seconds
- **Tags**: `service:alloy`, `env:production`

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

Logs are automatically collected from all containers via Docker socket:
- **Source**: `/var/lib/docker/containers` (mounted read-only)
- **Processing**: Datadog Agent parses JSON logs
- **Tagging**: Automatic container, image, and service tags
- **Filtering**: Excludes `datadog-agent` container itself

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
