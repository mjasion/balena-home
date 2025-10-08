# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

This is a multi-service repository containing two independent Go applications designed to run together using Docker Compose:

- **HellPot**: An HTTP honeypot that traps web bots by sending infinite streams of generated content
- **WolWeb**: A web interface for sending Wake-on-LAN magic packets to devices on the local network

Both services are containerized and exposed via nginx reverse proxy with Cloudflare tunnel integration.

## Project Structure

The repository contains two separate Go modules:

```
hellpot/          # HellPot honeypot service
  cmd/HellPot/    # Main application entry point
  heffalump/      # Markov chain text generation engine
  internal/
    config/       # TOML configuration with koanf
    http/         # FastHTTP router and handlers
    extra/        # Banner and utilities

wolweb/           # Wake-on-LAN web service
  main.go         # Entry point with embedded static files
  *.go            # HTTP handlers, WoL logic, data persistence
```

## Building and Running

### HellPot

```bash
cd hellpot
make              # Runs: deps, check, build
make deps         # Run go mod tidy
make check        # Run go vet
make build        # Build with version from git tags
make run          # Run directly without building
make format       # Format all Go files with gofmt
```

The Makefile sets version via linker flags: `-ldflags "-s -w -X main.version=$(git tag --sort=-version:refname | head -n 1)"`

### WolWeb

```bash
cd wolweb
go build -o wolweb .              # Build binary
go run main.go -c config.json -d devices.json  # Run with custom config
```

WolWeb accepts command-line flags:
- `-c`: Path to config.json (default: config.json)
- `-d`: Path to devices.json (default: devices.json)

### Docker Compose

```bash
docker-compose up -d     # Start all services
```

Services:
- `hellpot`: Listens on port 8080
- `wolweb`: Uses host network mode for WoL packet broadcasting
- `nginx`: Reverse proxy on ports 80/443
- `tunnel`: Cloudflare tunnel (configured with TUNNEL_TOKEN)

## Configuration

### HellPot Configuration

HellPot uses TOML configuration with koanf library. Config locations (in priority order):
1. Custom path via `-c` flag
2. `/etc/HellPot/config.toml`
3. `$HOME/.config/HellPot/config.toml`
4. `./config.toml`

Environment variable overrides use `HELLPOT_` prefix with double underscores for nested keys:
```bash
HELLPOT_HTTP_BIND__ADDR="0.0.0.0"
HELLPOT_LOGGER_DEBUG="true"
```

Key configuration sections:
- `[http]`: Bind address/port, Unix socket options, real IP header, catchall/robots settings
- `[performance]`: Worker concurrency limits
- `[logger]`: Debug/trace levels, log directory, color output
- `[deception]`: Server header spoofing

### WolWeb Configuration

Uses cleanenv library to load config.json with environment variable overrides:

Environment variables:
- `WOLWEBHOST`: HTTP host (default: 0.0.0.0)
- `WOLWEBPORT`: HTTP port (default: 8089)
- `WOLWEBVDIR`: Virtual directory prefix (default: /wolweb)
- `WOLWEBBCASTIP`: Broadcast IP and port (default: 192.168.1.255:9)
- `WOLWEBREADONLY`: Read-only mode for UI (default: false)

## Architecture Notes

### HellPot

- Uses **fasthttp** for high-performance HTTP serving
- Implements a **Markov chain engine** (heffalump) that generates infinite text streams based on Nietzsche's "The Birth of Tragedy"
- **zerolog** for structured JSON logging
- Supports both TCP and Unix socket listeners
- Concurrent request handling with optional worker pool restrictions
- robots.txt generation and honeypot path configuration

Entry point: `hellpot/cmd/HellPot/HellPot.go`
- Initializes config, logger, and banner
- Starts HTTP server in goroutine
- Handles SIGINT/SIGTERM for graceful shutdown

### WolWeb

- Uses **gorilla/mux** router with embedded static files (Go 1.16+ embed)
- **gorilla/handlers** for recovery and logging
- **gziphandler** for response compression
- Bootstrap UI with JS Grid for CRUD operations
- Persistent device storage in devices.json
- Direct WoL via HTTP GET: `/wolweb/wake/{deviceName}`

Entry point: `wolweb/main.go`
- Sets working directory to executable location
- Loads config and devices data
- Maps static files from embedded FS
- Conditionally enables data modification endpoints based on read-only flag

### Docker Network Configuration

WolWeb requires **host network mode** because Wake-on-LAN magic packets must be broadcast on the local network. Docker's bridged network mode blocks broadcasts. See: https://github.com/docker/for-linux/issues/637

## Testing

Neither project currently includes test files. When adding tests:
- HellPot: Test markov generation, config loading, HTTP routing
- WolWeb: Test WoL packet sending, device CRUD, HTTP endpoints

## Go Versions

- **HellPot**: Requires Go 1.24.0+ (toolchain 1.25.2)
- **WolWeb**: Requires Go 1.19+

## Common Development Tasks

When modifying HellPot configuration handling, remember that koanf uses dot-separated keys and environment variables require double underscores for nested keys (see `hellpot/internal/config/config.go:203`).

When modifying WolWeb routes, ensure virtual directory handling is consistent - basePath is empty when VDir is "/" to avoid redirect loops (see `wolweb/main.go:70-76`).
