# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

This is a home automation service repository containing a Go application for Wake-on-LAN functionality, designed to run using Docker Compose.

- **WolWeb**: A web interface for sending Wake-on-LAN magic packets to devices on the local network

The service is containerized and exposed via nginx reverse proxy with Cloudflare tunnel integration.

## Project Structure

```
wolweb/           # Wake-on-LAN web service
  main.go         # Entry point with embedded static files
  *.go            # HTTP handlers, WoL logic, data persistence
```

## Building and Running

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
- `wolweb`: Uses host network mode for WoL packet broadcasting
- `nginx`: Reverse proxy on ports 80/443
- `tunnel`: Cloudflare tunnel (configured with TUNNEL_TOKEN)

## Configuration

### WolWeb Configuration

Uses cleanenv library to load config.json with environment variable overrides:

Environment variables:
- `WOLWEBHOST`: HTTP host (default: 0.0.0.0)
- `WOLWEBPORT`: HTTP port (default: 8089)
- `WOLWEBVDIR`: Virtual directory prefix (default: /wolweb)
- `WOLWEBBCASTIP`: Broadcast IP and port (default: 192.168.1.255:9)
- `WOLWEBREADONLY`: Read-only mode for UI (default: false)

## Architecture Notes

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

The project currently does not include test files. When adding tests:
- WolWeb: Test WoL packet sending, device CRUD, HTTP endpoints

## Go Versions

- **WolWeb**: Requires Go 1.19+

## Common Development Tasks

When modifying WolWeb routes, ensure virtual directory handling is consistent - basePath is empty when VDir is "/" to avoid redirect loops (see `wolweb/main.go:70-76`).
