# Energy Meter Scraper - Technical Design

## Context

Smart energy meters expose HTTP endpoints that return JSON data containing multiple sensors (voltage, current, power, energy counters, etc.). We need to periodically scrape these endpoints, extract specific metrics (activePower values), and push them to Grafana Cloud for visualization and alerting.

The service will run continuously in a Docker container alongside other home automation services (wolweb, nginx, tunnel).

## Goals / Non-Goals

### Goals
- Reliably scrape energy meter JSON endpoints at configurable intervals
- Extract activePower metrics from all sensors (identified by sensor id)
- Push metrics to Grafana Cloud Prometheus with proper labels
- Start at even seconds for predictable timing
- Support configuration via files and environment variables
- Handle temporary network failures gracefully

### Non-Goals
- Historical data backfilling
- Local metric storage (rely on Grafana Cloud)
- Advanced metric transformations (push raw values)
- Web UI for configuration
- Multi-device support in first iteration (single endpoint)

## Decisions

### Architecture: Single Binary Service
**Decision**: Implement as a standalone Go binary with embedded scheduling logic.

**Rationale**:
- Simple deployment model (single process)
- Low resource overhead (important for home automation)
- Consistent with existing wolweb service pattern
- No need for external schedulers

**Alternatives considered**:
- Cron + script: More moving parts, harder to manage precise timing
- Prometheus scraper: Doesn't support remote HTTP endpoints with custom parsing

### Scheduling: Two Independent Tickers
**Decision**: Use separate goroutines with ticker for scraping (2s) and pushing (15s).

**Rationale**:
- Decouples data collection from metric export
- Allows buffering multiple scrape results before push
- Scrape interval can be very short without overwhelming Prometheus
- Each ticker can be independently configured

**Alternatives considered**:
- Single timer with GCD: More complex, less flexible
- Scrape-on-push: Would lose high-resolution data

### Configuration: cleanenv Library with YAML/TOML
**Decision**: Use cleanenv library (same as wolweb) for config loading with env overrides, supporting YAML or TOML format.

**Rationale**:
- Consistency with existing codebase
- Supports YAML and TOML config files with environment variable overrides
- YAML/TOML are more human-friendly than JSON (comments, better readability)
- Simple struct-based configuration
- Well-tested library

**Format choice**: Default to YAML for better readability and comment support, with TOML as alternative.

### Metric Labels: Sensor ID
**Decision**: Add `sensor_id` label to distinguish between multiple sensors reporting activePower.

**Rationale**:
- Multiple sensors (id: 0, 1, 2, 3) report activePower in sample data
- Labels enable per-circuit monitoring and alerting
- Standard Prometheus practice

**Example metric**:
```
active_power_watts{sensor_id="0"} 237
active_power_watts{sensor_id="1"} 30
active_power_watts{sensor_id="2"} 188
active_power_watts{sensor_id="3"} 19
```

### Prometheus Integration: Remote Write
**Decision**: Use Prometheus remote_write protocol via official Go client.

**Rationale**:
- Native Grafana Cloud support
- Efficient batching of metrics
- Standard protocol with good library support
- Built-in retry logic

**Library**: `github.com/prometheus/client_golang/prometheus`

### Even-Second Start: Time Synchronization
**Decision**: At startup, sleep until the next even second using `time.Until()`.

**Implementation**:
```go
now := time.Now()
nextEven := now.Truncate(time.Second).Add(time.Second)
if now.Second()%2 == 0 {
    nextEven = nextEven.Add(time.Second)
}
time.Sleep(time.Until(nextEven))
```

## Risks / Trade-offs

### Risk: Scrape Interval Drift
**Risk**: If HTTP requests take longer than scrape interval, timing drift may occur.

**Mitigation**:
- Use `time.Ticker` which accounts for processing time
- Log warnings if scrape duration exceeds 80% of interval
- Make scrape timeout configurable (default: 1.5s for 2s interval)

### Risk: Network Failures
**Risk**: Temporary network issues could cause metric gaps.

**Mitigation**:
- Retry failed scrapes with exponential backoff (max 3 attempts)
- Continue pushing last-known-good values with stale flag
- Log errors for monitoring

### Risk: Invalid JSON Parsing
**Risk**: Energy meter may return malformed JSON or unexpected schema.

**Mitigation**:
- Strict JSON unmarshaling with validation
- Gracefully handle missing fields (skip that scrape)
- Log parsing errors with sample data for debugging

### Trade-off: In-Memory Buffer vs Disk Persistence
**Decision**: Use in-memory ring buffer (size: 10 scrapes).

**Rationale**:
- Simple implementation
- Low latency
- Acceptable data loss on crash (max 20 seconds at 2s interval)
- Disk I/O adds complexity and wear for embedded systems

**Consequence**: Brief service restarts lose recent data points.

## Data Flow

```
┌─────────────┐      ┌──────────────┐      ┌──────────────┐
│ Energy      │─────▶│ Scraper      │─────▶│ Ring Buffer  │
│ Meter API   │ 2s   │ (HTTP GET)   │      │ (in-memory)  │
└─────────────┘      └──────────────┘      └──────────────┘
                                                    │
                                                    │
                                                    ▼
                                            ┌──────────────┐
                                            │ Prometheus   │
                                            │ Push (15s)   │
                                            └──────────────┘
                                                    │
                                                    ▼
                                            ┌──────────────┐
                                            │ Grafana      │
                                            │ Cloud        │
                                            └──────────────┘
```

## Configuration Schema

### YAML Format (Recommended)
```yaml
# Energy Meter Scraper Configuration
scrapeUrl: "http://192.168.1.100/api/sensor"
scrapeIntervalSeconds: 2
scrapeTimeoutSeconds: 1.5
pushIntervalSeconds: 15

# Prometheus / Grafana Cloud settings
prometheusUrl: "https://prometheus-prod-01-eu-west-0.grafana.net/api/prom/push"
prometheusUsername: "123456"
prometheusPassword: "secret"  # Prefer environment variable for production

# Metric configuration
metricName: "active_power_watts"
startAtEvenSecond: true
```

### TOML Format (Alternative)
```toml
# Energy Meter Scraper Configuration
scrapeUrl = "http://192.168.1.100/api/sensor"
scrapeIntervalSeconds = 2
scrapeTimeoutSeconds = 1.5
pushIntervalSeconds = 15

# Prometheus / Grafana Cloud settings
prometheusUrl = "https://prometheus-prod-01-eu-west-0.grafana.net/api/prom/push"
prometheusUsername = "123456"
prometheusPassword = "secret"  # Prefer environment variable for production

# Metric configuration
metricName = "active_power_watts"
startAtEvenSecond = true
```

### Environment Variable Overrides
- `SCRAPE_URL`
- `SCRAPE_INTERVAL_SECONDS`
- `SCRAPE_TIMEOUT_SECONDS`
- `PUSH_INTERVAL_SECONDS`
- `PROMETHEUS_URL`
- `PROMETHEUS_USERNAME`
- `PROMETHEUS_PASSWORD` (sensitive - prefer env var)
- `METRIC_NAME`
- `START_AT_EVEN_SECOND`

## Migration Plan

Not applicable - this is a new service with no existing state.

### Deployment Steps
1. Build Go binary
2. Create Docker image (if containerized)
3. Add to docker-compose.yml
4. Configure environment variables or config file
5. Start service
6. Verify metrics appear in Grafana Cloud

### Rollback
Simply stop the service. No data persistence means clean rollback.

## Open Questions

1. **Should we support multiple scrape endpoints in first iteration?**
   - Proposal: Start with single endpoint, add multi-endpoint support later if needed

2. **What should happen if Prometheus push fails repeatedly?**
   - Proposal: Log errors, continue scraping, drop oldest buffered metrics when buffer full

3. **Should we expose health check endpoint?**
   - Proposal: Yes, simple HTTP endpoint on `:8080/health` returning 200 OK if last scrape succeeded

4. **Value scaling: Are sensor values in correct units?**
   - Sample shows `"value": 237` for activePower
   - Assumption: Values are in watts (not milliwatts)
   - **Needs confirmation from user**
