# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

This is a home automation service repository containing multiple Go applications for home monitoring and control, designed to run on Raspberry Pi using Docker Compose and Balena.

### Services

- **home-controller**: Climate monitoring and automation service
  - BLE temperature sensor monitoring (LYWSD03MMC with ATC firmware)
  - Netatmo thermostat integration
  - Power meter monitoring
  - Prometheus metrics push
  - Future: Intelligent climate control automation

- **wolweb**: Wake-on-LAN web interface
  - Web UI for sending magic packets to devices
  - Device management with persistent storage
  - Direct HTTP GET endpoints for automation

- **alloy**: Grafana Alloy (observability agent)
  - Log collection and forwarding
  - Metrics scraping
  - Integration with Grafana Cloud

- **nginx**: Reverse proxy
  - Exposes services via HTTPS
  - Cloudflare tunnel integration

- **tunnel**: Cloudflare tunnel
  - Secure remote access without port forwarding

## Project Structure

```
balena-home/
├── docker-compose.yml           # Service orchestration
├── CLAUDE.md                    # This file (project-level instructions)
├── home-controller/             # Climate monitoring & automation
│   ├── CLAUDE.md                # Service-specific instructions
│   ├── main.go                  # Entry point
│   ├── scanner/                 # BLE scanning
│   ├── netatmo/                 # Netatmo API integration
│   ├── power/                   # Power meter scraping
│   ├── metrics/                 # Prometheus push
│   ├── buffer/                  # Ring buffer
│   └── config.yaml              # Configuration
├── wolweb/                      # Wake-on-LAN service
│   ├── main.go                  # Entry point with embedded UI
│   ├── devices.json             # Device database
│   └── config.json              # Configuration
├── alloy/                       # Grafana Alloy configuration
│   └── config.alloy             # Alloy config
├── nginx/                       # Nginx reverse proxy
│   └── *.conf                   # Nginx configurations
└── .github/workflows/           # CI/CD
    ├── home-controller-test.yml # Tests for home-controller
    └── *.yml                    # Other workflows
```

## Building and Running

### Individual Services

Each service can be built independently:

```bash
# Home Controller
cd home-controller
go build -o home-controller .
./home-controller -c config.yaml

# WolWeb
cd wolweb
go build -o wolweb .
go run main.go -c config.json -d devices.json
```

### Docker Compose

Start all services:

```bash
docker-compose up -d
```

Service configurations:
- `home-controller`: Host network + privileged (for BLE and D-Bus)
- `wolweb`: Host network (for WoL broadcast)
- `nginx`: Ports 80/443 exposed
- `tunnel`: Requires `TUNNEL_TOKEN` environment variable
- `alloy`: Port 12345, Balena socket access

## Configuration

### Environment Variables

Critical secrets managed via environment variables:
- `TUNNEL_TOKEN`: Cloudflare tunnel token
- `PROMETHEUS_PASSWORD`: Grafana Cloud API key
- `NETATMO_CLIENT_ID`: Netatmo OAuth2 client ID
- `NETATMO_CLIENT_SECRET`: Netatmo OAuth2 client secret
- `NETATMO_REFRESH_TOKEN`: Netatmo OAuth2 refresh token

### Service-Specific Configuration

Each service has its own configuration file:
- `home-controller/config.yaml`: BLE sensors, Netatmo, Prometheus
- `wolweb/config.json`: WoL settings, virtual directory
- `wolweb/devices.json`: Device database
- `alloy/config.alloy`: Grafana Alloy pipeline
- `nginx/*.conf`: Reverse proxy routes

## Architecture Notes

### Network Modes

**Host Network Mode** (home-controller, wolweb):
- Required for BLE broadcasting and WoL magic packets
- Docker's bridged network blocks broadcast traffic
- Reference: https://github.com/docker/for-linux/issues/637

**Privileged Mode** (home-controller):
- Required for BLE adapter access via BlueZ
- Needs D-Bus socket for system bus communication

### Service Communication

```
Internet → Cloudflare Tunnel → nginx → Services
                                 ↓
                          ┌──────┼──────┐
                          ▼      ▼      ▼
                       wolweb  home-  alloy
                              controller
                                 ↓
                          Grafana Cloud
```

## Testing

### Go Services

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

### GitHub Workflows

CI/CD runs automatically:
- `home-controller-test.yml`: Tests on PR to main
- Other workflows for additional services

## Go Versions

All Go services require **Go 1.19+** (tested with 1.25.3 in CI)

## Common Development Tasks

### Adding a New Service

1. Create service directory with Dockerfile
2. Add service to `docker-compose.yml`
3. Create service-specific CLAUDE.md for documentation
4. Add GitHub workflow for testing (if applicable)
5. Update this root CLAUDE.md

### Modifying Network Configuration

When changing service networking:
- Services needing broadcast (BLE, WoL): Use `network_mode: host`
- Services needing BLE: Add `privileged: true` and D-Bus labels
- Services needing external access: Expose via nginx

### Working with Secrets

- **Never** commit secrets to `config.yaml` or `config.json`
- Always use environment variables for sensitive data
- Reference secrets in docker-compose.yml via `${VAR}` syntax
- Use `.env` file locally (git-ignored)

### Debugging Services

```bash
# View logs
docker-compose logs -f [service-name]

# Restart specific service
docker-compose restart [service-name]

# Rebuild and restart
docker-compose up -d --build [service-name]

# Check service status
docker-compose ps
```

## Service-Specific Documentation

For detailed information about each service, see:
- [home-controller/CLAUDE.md](./home-controller/CLAUDE.md): Climate monitoring details
- Individual service README files where available

## Deployment

This project is designed for:
- **Raspberry Pi** (tested on Pi 4)
- **Balena.io** platform for easy deployment and management
- **Docker Compose** for local development

Balena-specific labels are used for:
- `io.balena.features.dbus`: D-Bus access
- `io.balena.features.balena-socket`: Balena API access

## Related Resources

- Grafana Cloud: Metrics and logs
- Cloudflare Tunnel: Secure remote access
- Netatmo API: https://dev.netatmo.com/
- ATC Firmware: https://github.com/atc1441/ATC_MiThermometer
