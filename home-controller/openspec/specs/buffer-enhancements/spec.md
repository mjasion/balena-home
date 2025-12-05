# buffer-enhancements Specification

## Purpose
TBD - created by archiving change add-intelligent-thermostat-control. Update Purpose after archive.
## Requirements
### Requirement: Dual Buffer Architecture
The system SHALL maintain two separate ring buffers: one for metrics collection (existing) and one dedicated to control loop historical data access (new).

#### Scenario: Separate buffers for different purposes
- **WHEN** BLE scanner receives a temperature reading
- **THEN** the system SHALL write the reading to BOTH the metrics buffer AND the control buffer
- **AND** each buffer SHALL operate independently with its own capacity and lifecycle

#### Scenario: Metrics buffer cleared periodically
- **WHEN** metrics pusher calls GetAllAndClear() on the metrics buffer
- **THEN** the system SHALL clear ONLY the metrics buffer
- **AND** SHALL NOT affect the control buffer contents

#### Scenario: Control buffer retains history
- **WHEN** control loop reads from the control buffer using GetReadingsByTimeWindow()
- **THEN** the system SHALL return readings from the control buffer's retained history
- **AND** SHALL NOT be affected by metrics pusher clearing the metrics buffer

#### Scenario: Control buffer capacity and overflow
- **WHEN** the control buffer reaches its capacity (e.g., 10K readings)
- **THEN** the system SHALL overwrite the oldest reading with new data (ring buffer behavior)
- **AND** SHALL maintain at least 60 seconds of recent history under normal operation

**Rationale**: Control loop requires 60 seconds of historical data for weighted average calculation, but metrics pusher clears its buffer every 15 seconds. Dual buffers provide clean separation: metrics buffer optimized for push-and-discard, control buffer optimized for historical retention.

**Impact**: BLE scanner must write to two buffers instead of one. Memory footprint increases by ~10K readings. No changes needed to existing metrics pusher code.

