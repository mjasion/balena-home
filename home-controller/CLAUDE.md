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

# Home Controller Service

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
┌─────────────────────────────────────────────────────┐
│  Main Orchestrator                                   │
│  - Config loading                                    │
│  - Signal handling (SIGINT/SIGTERM)                 │
│  - Graceful shutdown with final metrics push        │
└─────────────────────────────────────────────────────┘
         │
         ├──────────────┬──────────────┬──────────────┐
         ▼              ▼              ▼              ▼
┌──────────────┐ ┌──────────────┐ ┌──────────────┐ ┌──────────────┐
│ BLE Scanner  │ │ Netatmo      │ │ Power Meter  │ │ Metrics      │
│ (scanner/)   │ │ Poller       │ │ Scraper      │ │ Pusher       │
│              │ │ (netatmo/)   │ │ (power/)     │ │ (metrics/)   │
│ - Passive    │ │ - OAuth2     │ │ - HTTP       │ │ - Protobuf   │
│   BLE scan   │ │ - Fetch API  │ │   polling    │ │ - Snappy     │
│ - ATC decode │ │ - Thermostat │ │ - Energy     │ │ - Batch push │
│ - MAC filter │ │   data       │ │   metrics    │ │ - Remote     │
│              │ │ - Room temps │ │              │ │   write API  │
└──────────────┘ └──────────────┘ └──────────────┘ └──────────────┘
         │              │              │              │
         └──────────────┴──────────────┴──────────────┘
                        ▼
               ┌─────────────────┐
               │   Ring Buffer    │
               │  (buffer/)       │
               │  - Thread-safe   │
               │  - Concurrent    │
               │  - 100K capacity │
               └─────────────────┘
```

### Data Flow

1. **BLE Scanner**: Continuously scans for ATC_MiThermometer advertisements, decodes temperature/humidity/battery data
2. **Netatmo Poller**: Periodically fetches thermostat data via OAuth2 API
3. **Power Meter Scraper**: Polls HTTP endpoints for energy consumption metrics
4. **All readings** → Ring buffer (thread-safe, 100K capacity)
5. **Metrics Pusher**: Batch pushes to Prometheus every 30 seconds

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
├── netatmo/
│   ├── client.go          # OAuth2 client
│   ├── fetcher.go         # API data fetching
│   ├── poller.go          # Periodic polling logic
│   └── types.go           # Netatmo API types
├── power/
│   ├── scraper.go         # HTTP scraper for power meters
│   ├── poller.go          # Periodic polling logic
│   ├── types.go           # Power meter data types
│   └── *_test.go          # Tests
├── buffer/
│   ├── buffer.go          # Thread-safe ring buffer
│   └── buffer_test.go
├── metrics/
│   ├── pusher.go          # Prometheus remote_write client
│   └── pusher_test.go
├── config.yaml            # Default configuration
├── example.env            # Environment variable examples
├── Dockerfile             # Multi-stage Docker build
├── go.mod                 # Go module (requires 1.19+)
└── README.md              # Detailed documentation
```

## Configuration

The service uses `config.yaml` with environment variable overrides via cleanenv:

### Key Settings

**BLE Sensors**: List of LYWSD03MMC sensors with MAC addresses
**Netatmo**: OAuth2 credentials, fetch interval (60s default)
**Power Meter**: HTTP endpoint, scrape interval
**Prometheus**: Push interval (30s), endpoint URL, credentials, buffer/batch sizes
**Logging**: Format (console/json), level (debug/info/warn/error)

### Environment Variables

Critical secrets should be set via environment variables:
- `PROMETHEUS_PASSWORD`: Grafana Cloud API key
- `NETATMO_CLIENT_ID`: Netatmo OAuth2 client ID
- `NETATMO_CLIENT_SECRET`: Netatmo OAuth2 client secret
- `NETATMO_REFRESH_TOKEN`: Netatmo OAuth2 refresh token

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
- Go 1.25.3
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

## Future Plans

This service is designed as the foundation for intelligent climate control:
1. **Current**: Monitoring only (BLE sensors, Netatmo, power meters)
2. **Next**: Decision-making logic to open/close thermostats based on:
   - Temperature targets
   - Energy prices
   - Occupancy detection
   - Weather forecasts
3. **Future**: ML-based optimization for comfort vs. energy efficiency

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

## Related Documentation

- [README.md](./README.md): Detailed setup and troubleshooting
- [OpenSpec Changes](./openspec/changes/): Design documents and proposals
- [Root CLAUDE.md](../CLAUDE.md): Project-level instructions
