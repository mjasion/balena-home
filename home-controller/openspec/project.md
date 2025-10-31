# Project Context

## Purpose
Thermostats service for the balena-home automation platform. This service provides smart thermostat control and monitoring capabilities, designed to integrate with the existing home automation infrastructure running on Balena.

Part of the larger balena-home multi-service platform that includes WolWeb (Wake-on-LAN), nginx reverse proxy, and Cloudflare tunnel integration.

## Tech Stack
- **Language**: Go 1.19+
- **HTTP Framework**: gorilla/mux (router), gorilla/handlers (middleware)
- **Configuration**: cleanenv (JSON config with env var overrides)
- **Compression**: gziphandler
- **UI**: Bootstrap with JS Grid for CRUD operations
- **Containerization**: Docker with Docker Compose v2.1
- **Reverse Proxy**: nginx
- **Tunnel**: Cloudflare tunnel
- **Platform**: Balena (ARM/x64 multi-arch support)

## Project Conventions

### Code Style
- **Go**: Follow standard Go conventions (gofmt, effective Go)
- **File organization**:
  - `main.go` - Entry point with embedded static files (Go 1.16+ embed)
  - Separate files for: HTTP handlers, business logic, data persistence, types
  - Static files embedded in binary using `//go:embed`
- **Naming**:
  - Handlers: descriptive function names (e.g., `handleWake`, `handleDeviceList`)
  - Config env vars: Uppercase with service prefix (e.g., `THERMOSTATSHOST`, `THERMOSTATSPORT`)
- **Error handling**: Use gorilla/handlers recovery middleware
- **Logging**: Use gorilla/handlers logging middleware

### Architecture Patterns
- **Embedded static files**: Use Go 1.16+ embed directive for UI assets
- **Configuration**: JSON file with environment variable overrides using cleanenv
- **Data persistence**: JSON file storage for device/config data
- **HTTP middleware chain**: Recovery → Logging → Compression → Router
- **Virtual directory support**: Configurable URL prefix for reverse proxy paths
- **Read-only mode**: Optional flag to disable data modification endpoints
- **Network mode**: Consider host vs bridge networking based on protocol requirements
  - WolWeb uses host mode for broadcast packets
  - Evaluate thermostat service network requirements

### Testing Strategy
- Currently minimal test coverage in the codebase
- When adding tests:
  - HTTP endpoint testing
  - Business logic unit tests
  - Integration tests for external device communication
  - Configuration loading tests

### Git Workflow
- **Main branch**: `main`
- **Feature branches**: Descriptive names (e.g., `thermostats`, current branch)
- **Commits**: Clear, descriptive messages
- **CI/CD**: GitHub Actions for Balena build testing and secret scanning
  - Balena build testing workflow
  - TruffleHog for secret scanning

## Domain Context

### Home Automation
- Service runs on local network with direct device access
- May require broadcast/multicast capabilities (like WolWeb's Wake-on-LAN)
- Services exposed via nginx reverse proxy with Cloudflare tunnel
- Designed for 24/7 operation on Balena IoT platform

### Thermostat Domain
- Temperature monitoring and control
- HVAC system integration (heating/cooling/fan)
- Schedule management
- Remote access via web interface
- Real-time sensor data collection
- Integration with smart home protocols (consider: Zigbee, Z-Wave, local APIs)

### Balena Platform
- Multi-architecture support (ARM, x64)
- Container-based deployment
- Environment variable configuration
- Persistent storage considerations
- Service restart policies

## Important Constraints

### Technical Constraints
- **Go version**: Minimum Go 1.19+ (matching wolweb)
- **Docker Compose**: Version 2.1 format
- **Network access**: May need host network mode for local device discovery
- **Resource limits**: IoT platform - optimize for low memory/CPU footprint
- **Concurrent access**: Web UI must handle multiple simultaneous users
- **Data persistence**: JSON file storage must be atomic and crash-safe

### Deployment Constraints
- **Balena platform**: Must build on Balena's multi-arch builders
- **No manual intervention**: Service must auto-start and recover from failures
- **Configuration**: All config via files or environment variables (no interactive setup)
- **Reverse proxy**: Service must work behind nginx with configurable virtual directory

### Security Constraints
- **Secret scanning**: TruffleHog checks prevent credential commits
- **Local network only**: Service designed for private network use
- **Read-only mode**: Option to disable writes for production/demo environments

## External Dependencies

### Infrastructure Services
- **nginx**: Reverse proxy routing traffic to backend services
- **Cloudflare tunnel**: Secure external access via Cloudflare network
- **Balena**: Container orchestration and deployment platform

### Go Libraries (following wolweb pattern)
- `github.com/gorilla/mux` - HTTP router
- `github.com/gorilla/handlers` - HTTP middleware (logging, recovery)
- `github.com/ilyakaznacheev/cleanenv` - Configuration management
- `github.com/NYTimes/gziphandler` - Response compression

### Thermostat-Specific (to be determined)
- Temperature sensor APIs/libraries
- HVAC control protocols
- Smart home integration SDKs (if needed)

### Development Tools
- Docker and Docker Compose
- Go toolchain (1.19+)
- Balena CLI (for deployment)
