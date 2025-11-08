# Implementation Guide: Intelligent Thermostat Control

## Quick Reference

**Status**: ✅ Proposal validated and ready for implementation
**Task count**: 151 tasks across 12 sections
**Estimated effort**: 2-3 weeks for full implementation and testing
**Breaking changes**: None (opt-in via configuration)

## Core Concepts

### Problem Statement
Netatmo thermostats measure temperature inaccurately (up to 4°C deviation), causing poor heating decisions. Xiaomi LYWSD03MMC BLE sensors provide accurate readings but aren't used for control.

### Solution Overview
Use Xiaomi sensors as source of truth, automatically adjust Netatmo thermostat setpoints to compensate for measurement error while maintaining fail-safe rollback.

## Key Design Decisions

### 1. Dual Buffer Architecture
**Why**: Control loop needs 60s of historical data, but metrics pusher clears buffer every 15s.

**Solution**: Two separate ring buffers:
- **Metrics Buffer** (100K): BLE/Netatmo/Power → GetAllAndClear() every 15s → Prometheus
- **Control Buffer** (10K): BLE only → Never cleared → Control loop reads 60s window

**Implementation**:
```go
// In main.go
metricsBuffer := buffer.New(cfg.Prometheus.BufferSize, logger)  // 100K
controlBuffer := buffer.New(10000, logger)                       // 10K (NEW)

// BLE scanner writes to BOTH
bleScanner.Start(ctx, metricsBuffer, controlBuffer)  // Double write

// Metrics pusher uses metrics buffer (no changes)
pusher := metrics.New(metricsBuffer, ...)

// Control loop uses control buffer
controller := control.NewController(controlBuffer, ...)
```

### 2. Weighted Average Temperature
**Why**: Recent readings more accurately reflect current room state.

**Formula**:
```go
weight = (timestamp - cutoffTime) / (now - cutoffTime)
weightedAvg = Σ(temp × weight) / Σ(weight)
```

**Example**:
- Reading 1s ago: weight = 0.98
- Reading 30s ago: weight = 0.50
- Reading 59s ago: weight = 0.02

### 3. Setpoint Compensation Algorithm
**Logic**:
```go
scheduledTemp = 25°C  // From Netatmo schedule
xiaomiTemp = 24.5°C   // Weighted average from last 60s
thermostatMeasured = 25°C  // What thermostat thinks

difference = xiaomiTemp - scheduledTemp = -0.5°C

if abs(difference) >= threshold {
    newSetpoint = thermostatMeasured + difference
    newSetpoint = 25 + (-0.5) = 24.5°C

    SetRoomThermpoint(roomID, 24.5, mode="manual", duration=10min)
}
```

**Result**: Thermostat sees room at 25°C (incorrect), sets target to 24.5°C, heating turns ON, room reaches actual 25°C.

### 4. Thread Safety
**All state access** uses `sync.RWMutex`:
```go
type ControllerState struct {
    mu     sync.RWMutex
    states map[string]*ThermostatState
}

func (cs *ControllerState) GetState(roomID string) *ThermostatState {
    cs.mu.RLock()
    defer cs.mu.RUnlock()
    // Return COPY to prevent external mutation
    copy := *cs.states[roomID]
    return &copy
}
```

**No I/O under locks**: Always release mutex before network calls.

### 5. Fail-Safe Mechanisms
1. **10-minute auto-expiring overrides**: Thermostats revert to schedule if service crashes
2. **5-minute re-evaluation**: Persistent temperature differences handled automatically
3. **External modification detection**: Pause control when user manually adjusts thermostat
4. **Temperature safety limits**: Clamp setpoints to 10-30°C range
5. **Graceful degradation**: If sensor data missing, skip control for that cycle

## Implementation Phases

### Phase 0: Buffer Infrastructure (9 tasks)
1. Add `GetReadingsByTimeWindow(startTime, endTime)` to ring buffer
2. Create dual buffer setup in main.go
3. Modify BLE scanner to write to both buffers
4. Test buffer isolation

**Deliverable**: Control buffer retains 60s of data while metrics pusher clears metrics buffer

### Phase 1: Configuration (10 tasks)
1. Define `ThermostatControlConfig` struct
2. Add sensor-to-thermostat mappings
3. Add hard override schedules
4. Implement config validation

**Deliverable**: Full configuration schema with validation

### Phase 2: Netatmo Write API (10 tasks)
1. Add `SetRoomThermpoint()` method
2. Implement duration-based manual mode
3. Handle OAuth2 token refresh
4. Add error handling and retries

**Deliverable**: Working Netatmo write API with tests

### Phase 3: Control Core (28 tasks)
1. Thread-safe state management
2. Weighted average calculation
3. Setpoint compensation algorithm
4. External modification detection
5. Hard override precedence

**Deliverable**: Complete control algorithm with unit tests

### Phase 4: Integration (6 tasks)
1. Launch control loop goroutine
2. Graceful shutdown handling
3. Error recovery
4. Logging

**Deliverable**: Integrated control loop running in production

### Phase 5: Metrics & Monitoring (8 tasks)
1. Prometheus metrics (setpoint changes, temperature differences)
2. External modification tracking
3. API error counters
4. Grafana dashboards

**Deliverable**: Full observability into control decisions

### Phase 6: Testing (29 tasks)
1. Unit tests for all components
2. Race detection (`go test -race`)
3. Integration tests with mocked Netatmo API
4. Buffer isolation tests
5. Concurrent access tests

**Deliverable**: >90% test coverage, all tests passing

### Phase 7: Documentation (10 tasks)
1. README updates
2. Configuration examples
3. Troubleshooting guide
4. Grafana dashboard examples

**Deliverable**: Complete user and operator documentation

### Phase 8: Safety & Validation (10 tasks)
1. Dry-run mode
2. Temperature safety limits
3. Circuit breaker for API failures
4. Rate limit monitoring

**Deliverable**: Production-ready safety mechanisms

### Phase 9: Deployment (12 tasks)
1. Balena platform testing
2. Goroutine lifecycle verification
3. Fail-safe testing (stop container, verify 10-min rollback)
4. Production monitoring

**Deliverable**: Successfully deployed to Raspberry Pi

## Configuration Example

```yaml
thermostatControl:
  enabled: true
  temperatureThreshold: 0.5  # °C
  controlIntervalSeconds: 60
  overrideDurationMinutes: 10
  recheckDelayMinutes: 5
  externalModificationResetMinutes: 5

  mappings:
    - roomName: "Living Room"
      sensorMAC: "A4:C1:38:XX:XX:XX"
    - roomName: "Bedroom"
      sensorMAC: "A4:C1:38:YY:YY:YY"
    - roomName: "Kitchen"
      sensorMAC: "A4:C1:38:ZZ:ZZ:ZZ"
    - roomName: "Office"
      sensorMAC: "A4:C1:38:ZZ:ZZ:ZZ"  # Shared with Kitchen

  hardOverrides:
    - roomName: "Living Room"
      schedule:
        - startTime: "06:00"
          endTime: "06:20"
          targetTemperature: 23.0
```

## Testing Strategy

### Unit Tests
```bash
# Run with race detection
go test -race -v ./control/...
go test -race -v ./netatmo/...

# Coverage
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

### Integration Tests
```bash
# Mock Netatmo API
go test -v ./control/... -tags=integration

# Buffer isolation
go test -v ./buffer/... -run TestDualBuffer
```

### Manual Testing
1. Enable dry-run mode: `dryRun: true`
2. Monitor logs: `docker-compose logs -f home-controller`
3. Verify no actual API calls
4. Test external modification: Use Netatmo app to change setpoint
5. Verify control loop pauses

## Monitoring Checklist

### Metrics to Watch
- `thermostat_setpoint_changes_total{room}`: API call frequency
- `thermostat_temperature_difference{room}`: Xiaomi vs Scheduled
- `thermostat_external_modification{room}`: Manual override detection
- `thermostat_api_errors_total{room, code}`: API failures

### Grafana Queries
```promql
# Temperature difference over time
thermostat_temperature_difference{room="Living Room"}

# Setpoint change rate
rate(thermostat_setpoint_changes_total[5m])

# API error rate
rate(thermostat_api_errors_total[5m]) > 0
```

### Alerts to Configure
1. **High API error rate**: > 10% errors in 5 minutes
2. **No setpoint changes**: 0 changes in 2 hours (may indicate stuck control loop)
3. **Extreme temperature difference**: > 3°C for > 10 minutes (sensor or thermostat failure)

## Troubleshooting Guide

### Issue: Control loop not adjusting thermostats
**Check**:
1. `thermostatControl.enabled: true` in config?
2. Sensor-to-thermostat mappings correct?
3. External modification flag set? (Check logs)
4. Temperature difference > threshold?

### Issue: Too many API calls
**Check**:
1. Temperature threshold too low? (Increase from 0.2 to 0.5°C)
2. Re-check delay too short? (Increase from 5 to 10 minutes)
3. Sensor readings fluctuating? (Check sensor RSSI, battery)

### Issue: Thermostats not responding
**Check**:
1. Netatmo OAuth2 tokens valid? (Check token refresh logs)
2. API rate limits hit? (Check 429 errors)
3. Network connectivity to Netatmo API?

### Issue: Missing sensor data
**Check**:
1. Control buffer has readings? (Should have 60s of data)
2. BLE scanner writing to control buffer? (Check dual write logs)
3. Sensor battery level? (Low battery = intermittent readings)

## Security Considerations

1. **OAuth2 tokens**: Store in environment variables, never commit to git
2. **API credentials**: Use Balena secrets for production deployment
3. **Rate limiting**: Respect Netatmo API limits (implement exponential backoff)
4. **Input validation**: Validate all config values at startup
5. **Temperature bounds**: Hard limits (10-30°C) prevent extreme setpoints

## Performance Characteristics

### Memory Usage
- Control buffer: ~2MB (10K readings × 200 bytes)
- State store: <1KB (4 thermostats × ~200 bytes state)
- Total overhead: ~3MB

### CPU Usage
- Control loop: <1% CPU (runs 1x per minute)
- Weighted average calc: O(n) where n = readings in 60s (~240 readings max)
- Buffer reads: O(n) with time filtering

### Network Usage
- Netatmo API calls:
  - Read: 1 call/minute (homestatus)
  - Write: 0-4 calls/minute (setpoint changes, only when needed)
- Prometheus: Existing metrics flow (no change)

## Next Steps

1. **Review this proposal** with team/stakeholders
2. **Get approval** before starting implementation
3. **Create feature branch**: `git checkout -b feature/intelligent-thermostat-control`
4. **Start with Phase 0** (buffer infrastructure)
5. **Implement sequentially** following task list in tasks.md
6. **Test thoroughly** at each phase
7. **Deploy to staging** (test Raspberry Pi) before production
8. **Monitor closely** for first 48 hours after deployment

## Success Criteria

✅ Control loop runs every 60 seconds without errors
✅ Thermostats maintain target temperature within ±0.5°C
✅ No duplicate metric pushes to Prometheus
✅ Fail-safe works (service stop → thermostats revert in 10 minutes)
✅ External modifications detected and respected
✅ All tests pass with `-race` flag
✅ API error rate < 1%
✅ Memory usage < 50MB total for service

---

**Ready to implement!** Follow tasks.md for detailed step-by-step instructions.
