## Context

The thermostat control system compensates for inaccurate Netatmo sensors by using Xiaomi BLE sensors as the source of truth. Over time, the algorithm accumulated complexity:
- Delayed execution to prevent feedback loops
- Schedule sync to read scheduled temps while in manual mode
- Runaway protection against consecutive setpoint increases
- Override extension logic
- Multiple layers of external modification detection

This complexity makes the system hard to understand and debug. The new design simplifies by:
1. Aligning with natural 15-minute schedule boundaries
2. Using duration-based manual override detection
3. Splitting responsibilities into focused jobs

## Goals / Non-Goals

**Goals:**
- Simplify control algorithm to be easily understood
- Align override timing with Netatmo's 15-minute schedule blocks
- Clear separation of concerns (metrics, control, hard overrides)
- Predictable behavior based on timing rather than complex state tracking

**Non-Goals:**
- Adding new features
- Changing the fundamental temperature compensation approach
- Modifying Xiaomi sensor reading or weighted average logic

## Decisions

### Decision 1: Three Separate Jobs

**What**: Split control into Metric Job, Control Job, and Hard Override Job.

**Why**:
- Clear separation of concerns
- Different execution frequencies (1 min vs 15 min)
- Hard overrides need faster response to time window boundaries

**Alternatives considered**:
- Single monolithic job: Rejected - too complex, hard to test
- Two jobs (metrics + combined control): Rejected - hard overrides need independent timing

### Decision 2: Fixed 15-Minute Override Windows

**What**: Overrides always expire at `:14:59, :29:59, :44:59, :59:59` regardless of when set.

**Why**:
- Aligns with Netatmo's internal schedule timing
- Predictable expiration allows reading fresh schedule temps
- Simplifies manual override detection (algorithm overrides are always < 15 min)

**Trade-offs**:
- Variable override duration (0-15 min) depending on when within window
- Override set at `:14` only lasts ~1 minute

### Decision 3: Duration-Based Manual Override Detection

**What**: Override with duration >= 60 minutes is treated as human-set.

**Why**:
- Simple calculation: `end_time - start_time`
- No state tracking needed (no LastSetpoint comparison)
- Netatmo app defaults: 1h, 3h, 6h (all >= 60 min)
- Algorithm overrides: always < 15 min

**Alternatives considered**:
- Track LastSetpoint and compare: Rejected - requires state, complex edge cases
- Use setpoint value comparison: Rejected - human might set same temp as algorithm

### Decision 4: Channel-Based Communication

**What**: Metric Job sends `HomeStatusResponse` to Control Job via Go channel.

**Why**:
- Control Job always uses fresh data from current minute
- No race conditions with cached data
- Clear data flow

**Implementation**:
```go
type MetricJob struct {
    homeStatusChan chan<- *netatmo.HomeStatusResponse
}

type ControlJob struct {
    homeStatusChan <-chan *netatmo.HomeStatusResponse
}
```

### Decision 5: Non-Blocking Per-Room Processing

**What**: Rooms processed concurrently in goroutines. Rooms waiting for manual mode spawn separate goroutines.

**Why**:
- Room A shouldn't wait for Room B's manual mode to expire
- Each room can make independent API calls when ready
- Parallel execution within 15-minute window

**Flow**:
```
Control Job receives HomeStatus
├─ Room A (schedule): goroutine → decide immediately → execute
├─ Room B (manual, expires :45): goroutine → wait → own API call → decide → execute
└─ Room C (manual, expires :25:00): goroutine → skip (>= 60 min override)
```

### Decision 6: Simplified Algorithm

**What**: Three-zone control with fixed 0.5°C adjustments.

| Zone | Condition | Setpoint |
|------|-----------|----------|
| Too cold | diff <= -0.2°C | netatmo_measured + 0.5 |
| Within range | -0.2 < diff < 0.2 | netatmo_measured |
| Too warm | diff >= 0.2°C | netatmo_measured - 0.5 |

**Why**:
- Simpler than offset-based calculation
- Fixed adjustments are predictable
- 0.5°C matches Netatmo's setpoint granularity

### Decision 7: Remove Safety Mechanisms

**What**: Remove delayed execution, runaway protection, extension logic.

**Why**:
- 15-minute timing provides natural rate limiting
- Duration-based detection replaces external modification tracking
- Simpler system is easier to debug and understand

**Risk mitigation**:
- Hard 7-30°C safety bounds remain
- API rate limiting in Netatmo client remains

## Risks / Trade-offs

| Risk | Mitigation |
|------|------------|
| Short override duration near window end | Acceptable - thermostat returns to schedule, re-evaluated next window |
| More API calls (per-room fetches) | Netatmo client has rate limiting; only rooms waiting for manual mode make extra calls |
| Loss of runaway protection | 15-min timing limits how fast setpoint can change; safety bounds (7-30°C) remain |
| Concurrent goroutines complexity | Use sync.WaitGroup for coordination; each goroutine is independent |

## Migration Plan

1. Implement new job structure alongside existing code
2. Add feature flag to switch between old and new algorithm
3. Test in dry-run mode
4. Switch to new algorithm
5. Remove old code after validation period

## Open Questions

None - all clarified during design discussion.
