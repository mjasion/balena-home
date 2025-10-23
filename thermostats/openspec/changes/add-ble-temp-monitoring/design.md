# Design: BLE Temperature Monitoring Service

## Context

The home automation platform needs to collect temperature data from multiple LYWSD03MMC Xiaomi Bluetooth Low Energy sensors distributed throughout the home. These sensors will run custom ATC_MiThermometer firmware that broadcasts temperature, humidity, and battery data via BLE advertisements. The service must be energy-efficient (using passive scanning), run on Raspberry Pi hardware, and output data to stdout for integration with other systems. This is the foundation for future smart thermostat control based on multi-zone temperature sensing.

### Constraints

- Raspberry Pi platform with limited CPU/memory resources
- Must work within Balena containerized deployment
- BLE passive scanning requires root or CAP_NET_ADMIN capabilities
- LYWSD03MMC sensors use custom ATC firmware (UUID 0x181A advertisement format)
- Minimum output frequency: once per minute
- Must support 4 sensors initially, but be extensible
- Output to stdout for pipeline integration (logging, processing)
- Logs to stderr to separate operational info from data

### Stakeholders

- End user: Home automation system operator
- Future integration: Netatmo thermostat control system
- Infrastructure: Balena platform, Docker Compose orchestration

## Goals / Non-Goals

### Goals

- Passively scan for BLE advertisements from LYWSD03MMC sensors
- Decode ATC_MiThermometer advertisement format (temperature, humidity, battery)
- Support multiple sensors via configuration (MAC address filtering)
- Output structured data (JSON) to stdout at configurable intervals
- Minimize power consumption using passive scanning (no connections)
- Handle missing sensors gracefully (timeouts, out-of-range)
- Deploy on Raspberry Pi via Balena
- Follow project conventions (Go, cleanenv config, gorilla/mux pattern for future HTTP if needed)

### Non-Goals

- Active BLE connections to sensors (passive only for energy efficiency)
- Web UI or HTTP API (future phase if needed)
- Data persistence to files/database (stdout only for now)
- Support for other sensor types (focus on LYWSD03MMC with ATC firmware)
- Netatmo thermostat integration (deferred to future change)
- Historical data analysis or graphing
- Sensor firmware flashing tools

## Decisions

### Decision 1: Go with tinygo/bluetooth vs Python bluepy/bleak

**Choice**: Use Go with `tinygo.org/x/bluetooth`

**Rationale**:
- User preference for Go ("I prefer golang but python can also be ok")
- Consistency with existing wolweb service (Go 1.19+)
- Better resource efficiency and binary deployment (single static binary)
- Strong concurrency support for multi-sensor monitoring
- Cross-compilation support for ARM (Raspberry Pi)

**Alternatives considered**:
- Python with `bluepy` or `bleak`: Easier BLE libraries, but requires Python runtime, dependencies, and more memory
- Node.js with `noble`: Good BLE support but adds another runtime to the stack

**Trade-off**: Go BLE libraries are less mature than Python's, but `tinygo.org/x/bluetooth` is actively maintained (August 2025) and production-ready for passive scanning on Linux.

### Decision 2: Passive BLE Scanning (No Connections)

**Choice**: Use passive BLE advertisement scanning only, never establish connections to sensors

**Rationale**:
- ATC_MiThermometer firmware broadcasts all data in advertisements
- Passive scanning is significantly more energy-efficient
- Supports monitoring 4+ sensors without connection overhead
- No pairing or authentication required
- Reduces complexity and potential connection failures

**Alternatives considered**:
- Active connections with GATT characteristic reads: More reliable but much higher power consumption and complexity
- Mixed mode: Connect only when advertisements are missing - adds complexity without clear benefit for ATC firmware

**Trade-off**: Relies on sensors using ATC custom firmware. Stock Xiaomi firmware requires connections/encryption, but user can flash ATC firmware easily.

### Decision 3: Output Format - JSON to stdout

**Choice**: Output one JSON object per line to stdout, logs to stderr

**Rationale**:
- Structured data is easily parseable by other tools (jq, log aggregators)
- Separating stdout (data) from stderr (logs) follows Unix philosophy
- Easy integration with Docker logging, syslog, or file redirection
- Future pipeline integration (e.g., `service | jq | logger`)
- Optional human-readable mode via flag for debugging

**Alternatives considered**:
- CSV format: Less flexible for nested data or future fields
- Plain text: Harder to parse reliably
- Direct database writes: Out of scope; prefer decoupled architecture

**Trade-off**: Requires downstream processing for visualization, but maximizes flexibility.

### Decision 4: Configuration - JSON file + Environment Variables

**Choice**: Use cleanenv pattern with JSON config file and environment variable overrides

**Rationale**:
- Consistency with wolweb service architecture
- Balena deployment relies on environment variables
- JSON file provides defaults and documentation
- cleanenv handles merging and validation

**Configuration fields**:
```json
{
  "scan_interval_seconds": 60,
  "output_format": "json",
  "sensors": [
    "A4:C1:38:XX:XX:XX",
    "A4:C1:38:YY:YY:YY",
    "A4:C1:38:ZZ:ZZ:ZZ",
    "A4:C1:38:WW:WW:WW"
  ]
}
```

Environment variables:
- `BLESCANINTERVAL`: Override scan_interval_seconds
- `BLEOUTPUTFORMAT`: Override output_format (json|text)
- `BLESENSORS`: Comma-separated MAC addresses

**Alternatives considered**:
- Command-line flags only: Less convenient for Docker/Balena
- Environment variables only: Harder to document defaults

**Trade-off**: Small config file to maintain, but flexible deployment options.

### Decision 5: BLE Library - TinyGo Bluetooth

**Choice**: Use `tinygo.org/x/bluetooth` (cross-platform BLE API)

**Rationale**:
- Actively maintained (last update August 2025, 337+ packages using it)
- Native Go implementation with BlueZ D-Bus support on Linux
- Works on Raspberry Pi with BlueZ 5.48+
- Clean, simple API for passive scanning via `adapter.Scan()`
- Cross-platform (Linux, macOS, Windows, bare metal)
- No CGo required for Linux D-Bus backend
- BSD-3-Clause license (permissive)
- Good documentation and examples in repository

**Alternatives considered**:
- `github.com/muka/go-bluetooth`: Previously considered but no longer actively maintained (user feedback)
- `github.com/go-ble/ble`: Requires CGo and has cgocheck issues, less active development
- `github.com/paypal/gatt`: Older library, less active maintenance
- Python with `bluepy`/`bleak`: More mature BLE support but requires Python runtime and dependencies
- CGo bindings to BlueZ C library: More control but increases complexity and build dependencies

**Trade-off**: Requires BlueZ to be installed on the host (standard on Raspberry Pi OS). D-Bus adds a small overhead, but simplifies the implementation significantly. The library is well-maintained and production-ready for Linux BLE scanning use cases.

### Decision 6: Sensor Data Model - In-Memory Recent Readings

**Choice**: Maintain last 5 minutes of readings per sensor in memory (circular buffer)

**Rationale**:
- Future thermostat control logic may need recent history (e.g., temperature trends)
- Enables averaging or smoothing if needed
- Small memory footprint (4 sensors × 5 readings/minute × 5 minutes = ~100 readings)
- No persistence required for this phase

**Alternatives considered**:
- Only store latest reading: Simplest but limits future control logic
- Persist to file/database: Out of scope for initial implementation

**Trade-off**: Slight memory overhead, but enables future use cases without architectural changes.

## Architecture Overview

```
┌─────────────────────────────────────────────────┐
│  BLE Temperature Monitor (Go)                   │
│                                                  │
│  ┌──────────────┐         ┌─────────────────┐  │
│  │ Config       │         │  BLE Scanner    │  │
│  │ (cleanenv)   │────────▶│  (tinygo/ble)   │  │
│  │              │         │                 │  │
│  │ - Sensors    │         │ - Passive scan  │  │
│  │ - Interval   │         │ - Filter MACs   │  │
│  └──────────────┘         │ - Parse UUID    │  │
│                           │   0x181A        │  │
│                           └────────┬────────┘  │
│                                    │            │
│                                    ▼            │
│                           ┌─────────────────┐  │
│                           │  ATC Decoder    │  │
│                           │                 │  │
│                           │ - Extract data  │  │
│                           │ - Convert temp  │  │
│                           └────────┬────────┘  │
│                                    │            │
│                                    ▼            │
│                           ┌─────────────────┐  │
│                           │  Data Store     │  │
│                           │  (in-memory)    │  │
│                           │                 │  │
│                           │ - Recent        │  │
│                           │   readings      │  │
│                           └────────┬────────┘  │
│                                    │            │
│                                    ▼            │
│  ┌──────────────┐         ┌─────────────────┐  │
│  │  Ticker      │────────▶│  Output Writer  │  │
│  │  (interval)  │         │                 │  │
│  │              │         │ - Format JSON   │  │
│  │              │         │ - Write stdout  │  │
│  └──────────────┘         └─────────────────┘  │
│                                    │            │
└────────────────────────────────────┼───────────┘
                                     │
                                     ▼
                                  stdout (JSON)
```

### Component Responsibilities

1. **Config Module** (`config.go`)
   - Load JSON config file (via `-c` flag)
   - Merge environment variable overrides
   - Validate sensor MAC addresses
   - Provide configuration to other modules

2. **BLE Scanner** (`scanner.go`)
   - Initialize BLE adapter via tinygo.org/x/bluetooth
   - Start passive BLE scanning using `adapter.Scan()`
   - Filter advertisements by configured MAC addresses
   - Extract service data for UUID 0x181A
   - Pass raw advertisement data to decoder

3. **ATC Decoder** (`decoder.go`)
   - Parse ATC advertisement format binary data
   - Extract: MAC, temperature, humidity, battery %, battery mV, counter
   - Convert temperature (divide by 10)
   - Return structured sensor reading

4. **Data Store** (`store.go`)
   - Maintain in-memory map of recent readings per sensor
   - Store last 5 minutes per sensor (circular buffer)
   - Thread-safe access (mutex)
   - Provide latest reading for each sensor

5. **Output Writer** (`output.go`)
   - Format sensor readings as JSON or text
   - Write to stdout (data) or stderr (logs)
   - Handle missing sensor data (timeouts)

6. **Main** (`main.go`)
   - Parse command-line flags
   - Initialize config, scanner, store
   - Start BLE scanning goroutine
   - Run ticker for periodic output
   - Handle signals (SIGINT/SIGTERM) for graceful shutdown

## File Structure

```
thermostats/
├── main.go           # Entry point, orchestration, signal handling
├── config.go         # Configuration loading (cleanenv)
├── scanner.go        # BLE scanning (tinygo.org/x/bluetooth)
├── decoder.go        # ATC advertisement format parser
├── store.go          # In-memory data store with mutex
├── output.go         # JSON/text formatting and stdout writer
├── types.go          # Data structures (Config, Reading, etc.)
├── config.json       # Default configuration file
├── go.mod            # Go module dependencies
├── go.sum            # Dependency checksums
└── README.md         # Usage documentation
```

## Risks / Trade-offs

### Risk: BlueZ/D-Bus dependency

**Mitigation**: BlueZ is standard on Raspberry Pi OS and most Linux distributions. Document setup requirements clearly. Test on Balena platform early.

### Risk: ATC firmware requirement

**Mitigation**: Document firmware flashing process. Provide link to ATC_MiThermometer project. Initial setup step, but one-time effort.

### Risk: BLE interference or missed advertisements

**Mitigation**: Store last 5 minutes of readings and output warnings when sensors go missing. Consider adjusting scan window/interval if needed.

### Risk: Go BLE library maturity

**Mitigation**: Use tinygo.org/x/bluetooth which is actively maintained (August 2025) and wraps mature BlueZ stack via D-Bus. The library has 337+ packages using it and proven production use. Fall back to Python implementation if blockers found (unlikely based on research).

### Trade-off: No data persistence

**Impact**: If service crashes, recent data is lost. For this use case (real-time monitoring), acceptable. Future phases can add optional persistence if needed.

### Trade-off: Root/privileged execution

**Impact**: BLE scanning requires elevated privileges. Acceptable for IoT device where service runs in controlled environment. Document CAP_NET_ADMIN capability as alternative to full root.

## Migration Plan

Not applicable - this is a new service with no existing data or users to migrate.

### Deployment Steps

1. Flash ATC_MiThermometer firmware to all 4 LYWSD03MMC sensors (one-time setup)
2. Note MAC addresses of each sensor
3. Create `config.json` with sensor MAC addresses
4. Build Go binary: `go build -o ble-temp-monitor .`
5. Deploy to Raspberry Pi via Balena or direct installation
6. Configure Docker Compose with host Bluetooth access
7. Start service and verify JSON output to stdout
8. Pipe output to logging system or file as needed

### Rollback

Stop the service. No database or persistent state to clean up. Remove binary if needed.

## Open Questions

1. **Should we support multiple advertisement formats (stock Xiaomi, pvvx, BTHome)?**
   - Initial scope: ATC format only. Can extend later if needed.

2. **Do we need a health check endpoint or signal?**
   - Out of scope for initial implementation. Stdout output itself serves as health indicator.

3. **How should we handle sensor MAC address changes (e.g., replacing a sensor)?**
   - Edit config.json and restart service. Simple and sufficient for low-change environment.

4. **Should we support RSSI-based filtering (ignore sensors too far away)?**
   - Not initially. RSSI reporting is useful, but filtering adds complexity. Output RSSI and let downstream decide.

5. **Is 60-second interval sufficient, or do we need faster sampling?**
   - Start with 60 seconds (user requirement: "minimum once per minute"). Make it configurable so users can adjust.
