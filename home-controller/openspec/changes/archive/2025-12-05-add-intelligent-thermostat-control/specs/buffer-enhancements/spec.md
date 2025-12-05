# Buffer Enhancements Specification

## ADDED Requirements

### Requirement: Time-Window Filtered Reading Retrieval
The system SHALL provide a generic method to retrieve readings from the ring buffer within any caller-specified time window without clearing the buffer, enabling non-destructive historical data access.

#### Scenario: Retrieve readings in time window
- **WHEN** calling GetReadingsByTimeWindow(startTime, endTime) on the ring buffer with any valid time range
- **THEN** the system SHALL return all readings where Timestamp >= startTime AND Timestamp <= endTime
- **AND** SHALL NOT modify or clear the buffer contents
- **AND** SHALL NOT enforce any specific time window duration (caller determines window size)

#### Scenario: Thread-safe non-destructive read
- **WHEN** GetReadingsByTimeWindow is called while other goroutines are adding or clearing readings
- **THEN** the system SHALL use RLock to allow concurrent non-destructive reads
- **AND** SHALL return a copy of matching readings to prevent external mutation

#### Scenario: Empty time window
- **WHEN** calling GetReadingsByTimeWindow with a time range containing no matching readings
- **THEN** the system SHALL return an empty slice (not nil)
- **AND** SHALL NOT log errors or warnings

#### Scenario: Handle timestamp type conversion
- **WHEN** filtering readings by timestamp
- **THEN** the system SHALL correctly convert the interface{} Timestamp field to time.Time
- **AND** SHALL handle any type conversion errors gracefully

#### Scenario: Concurrent with GetAllAndClear
- **WHEN** GetReadingsByTimeWindow is called concurrently with GetAllAndClear from the metrics pusher
- **THEN** both operations SHALL complete without data races or deadlocks
- **AND** GetReadingsByTimeWindow MAY return fewer readings if GetAllAndClear executes first

## ADDED Requirements

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
