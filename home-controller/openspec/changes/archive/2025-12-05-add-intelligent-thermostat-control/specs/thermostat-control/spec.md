# Thermostat Control Specification

## ADDED Requirements

### Requirement: Sensor-to-Thermostat Mapping
The system SHALL allow configuration of which Xiaomi BLE temperature sensor controls each Netatmo thermostat room, supporting multiple thermostats sharing the same sensor.

#### Scenario: Single sensor per thermostat
- **WHEN** configuration defines one sensor MAC address per room name
- **THEN** the control algorithm SHALL use that sensor's temperature readings for that thermostat

#### Scenario: Shared sensor for multiple thermostats
- **WHEN** configuration maps the same sensor MAC address to multiple room names
- **THEN** the control algorithm SHALL use that sensor's readings for all mapped thermostats independently

#### Scenario: Invalid room name
- **WHEN** configuration specifies a room name that does not exist in Netatmo home data
- **THEN** the system SHALL fail validation at startup with a clear error message

#### Scenario: Invalid sensor MAC
- **WHEN** configuration specifies a sensor MAC address not present in BLE sensor configuration
- **THEN** the system SHALL fail validation at startup with a clear error message

### Requirement: Temperature Difference Threshold
The system SHALL only trigger thermostat adjustments when the absolute difference between Xiaomi sensor temperature and scheduled temperature exceeds a configurable threshold.

#### Scenario: Difference below threshold
- **WHEN** the absolute temperature difference is less than the configured threshold (e.g., 0.3°C when threshold is 0.5°C)
- **THEN** the system SHALL NOT adjust the thermostat setpoint

#### Scenario: Difference at or above threshold
- **WHEN** the absolute temperature difference is greater than or equal to the configured threshold (e.g., 0.5°C when threshold is 0.5°C)
- **THEN** the system SHALL calculate and apply a new thermostat setpoint

#### Scenario: Threshold configuration validation
- **WHEN** the configured temperature threshold is less than 0.1°C or greater than 5.0°C
- **THEN** the system SHALL fail validation at startup with a clear error message

### Requirement: Sensor Reading Aggregation with Weighted Average
The system SHALL use a time-weighted average temperature from all Xiaomi sensor readings received within the last 60 seconds for control algorithm calculations, giving more weight to recent readings.

#### Scenario: Weighted average with multiple readings
- **WHEN** a Xiaomi sensor has 3 readings in the last 60 seconds at timestamps t1=59s ago (24.4°C), t2=30s ago (24.5°C), t3=1s ago (24.6°C)
- **THEN** the system SHALL calculate weights using formula: weight = (timestamp - cutoffTime) / (now - cutoffTime)
- **AND** SHALL compute weighted average: (24.4 × w1 + 24.5 × w2 + 24.6 × w3) / (w1 + w2 + w3)
- **AND** SHALL use the weighted average for control decisions (closer to 24.6°C due to higher weight on recent reading)

#### Scenario: Single reading in window
- **WHEN** a Xiaomi sensor has only 1 reading in the last 60 seconds: 24.5°C
- **THEN** the system SHALL use 24.5°C as the weighted average (weight=1.0)

#### Scenario: No readings in window
- **WHEN** a Xiaomi sensor has no readings in the last 60 seconds
- **THEN** the system SHALL log a warning indicating sensor data is stale or unavailable
- **AND** SHALL skip thermostat control for rooms mapped to that sensor for this cycle

#### Scenario: Readings exactly at 60-second boundary
- **WHEN** determining which readings fall within the last 60 seconds
- **THEN** the system SHALL include readings where timestamp >= (currentTime - 60 seconds) AND timestamp <= currentTime

#### Scenario: Weight calculation
- **WHEN** calculating weight for a reading
- **THEN** the system SHALL use formula: weight = (reading.Timestamp - cutoffTime) / (now - cutoffTime)
- **AND** SHALL ensure weight is in range [0.0, 1.0]
- **AND** readings closer to now SHALL have weights approaching 1.0
- **AND** readings closer to cutoffTime SHALL have weights approaching 0.0

### Requirement: Setpoint Compensation Algorithm
The system SHALL calculate thermostat setpoint adjustments by adding the temperature difference (Average Xiaomi - Scheduled) to the thermostat's measured temperature.

#### Scenario: Room cooler than target
- **WHEN** average Xiaomi temperature is 24.5°C, scheduled temperature is 25°C, and thermostat measures 25°C
- **THEN** the system SHALL calculate new setpoint as 25 + (24.5 - 25) = 24.5°C
- **AND** SHALL send SetRoomThermpoint command with 24.5°C to enable heating

#### Scenario: Room at target temperature
- **WHEN** average Xiaomi temperature is 25°C and scheduled temperature is 25°C
- **THEN** the system SHALL NOT adjust the thermostat setpoint (difference = 0, below threshold)

#### Scenario: Room warmer than target
- **WHEN** average Xiaomi temperature is 25.5°C, scheduled temperature is 25°C, and thermostat measures 24°C
- **THEN** the system SHALL calculate new setpoint as 24 + (25.5 - 25) = 24.5°C
- **AND** SHALL send SetRoomThermpoint command with 24.5°C to disable heating

#### Scenario: Thermostat overheating
- **WHEN** average Xiaomi temperature is 24.5°C, scheduled temperature is 25°C, and thermostat measures 28°C (indicating malfunction)
- **THEN** the system SHALL calculate new setpoint as 28 + (24.5 - 25) = 27.5°C
- **AND** SHALL send SetRoomThermpoint command to prevent further heating

### Requirement: Auto-Expiring Overrides
The system SHALL set all thermostat setpoint adjustments as temporary manual overrides with a configurable duration, ensuring automatic rollback to Netatmo schedule if the service fails.

#### Scenario: Set duration-based override
- **WHEN** the control algorithm calculates a new setpoint
- **THEN** the system SHALL call SetRoomThermpoint with mode "manual" and endtime set to current time plus override duration (default 10 minutes)

#### Scenario: Override expiration
- **WHEN** the override duration expires and the service has not sent a new command
- **THEN** the Netatmo thermostat SHALL automatically revert to its scheduled temperature

#### Scenario: Service crash recovery
- **WHEN** the home-controller service crashes or stops
- **THEN** all active thermostat overrides SHALL expire within the configured duration (maximum 10 minutes)
- **AND** thermostats SHALL revert to Netatmo's native schedule without manual intervention

### Requirement: Re-Evaluation After Adjustment
The system SHALL re-check temperature and re-apply setpoint override after a configurable delay (default 5 minutes) following an initial adjustment, to handle persistent temperature differences.

#### Scenario: Temperature still different after delay
- **WHEN** the system sets an override at minute 0
- **AND** at minute 5 the Xiaomi temperature still differs from scheduled by more than threshold
- **THEN** the system SHALL calculate and send a new 10-minute override (expiring at minute 15)

#### Scenario: Temperature corrected within delay
- **WHEN** the system sets an override at minute 0
- **AND** at minute 5 the Xiaomi temperature is within threshold of scheduled temperature
- **THEN** the system SHALL NOT send a new override

#### Scenario: Skip control during wait period
- **WHEN** the system has set an override at minute 0 with 5-minute re-check delay
- **AND** the control loop runs at minute 1, 2, 3, or 4
- **THEN** the system SHALL skip adjustment for that thermostat until minute 5

### Requirement: Hard Override Schedules
The system SHALL support time-based temperature overrides configured in the config file, which take precedence over the automatic control algorithm.

#### Scenario: Hard override active
- **WHEN** current time falls within a configured hard override time window (e.g., 06:00-06:20)
- **THEN** the system SHALL use the override's target temperature instead of Netatmo's scheduled temperature
- **AND** SHALL apply the control algorithm using the override target

#### Scenario: Hard override inactive
- **WHEN** current time is outside all configured hard override time windows
- **THEN** the system SHALL use Netatmo's scheduled temperature as the target

#### Scenario: Multiple hard overrides for same room
- **WHEN** multiple hard override time windows are configured for a single room
- **AND** time windows do not overlap
- **THEN** each override SHALL be applied during its respective time window

#### Scenario: Invalid hard override time format
- **WHEN** hard override configuration contains invalid time strings (not HH:MM format)
- **OR** start time is after end time
- **THEN** the system SHALL fail validation at startup with a clear error message

### Requirement: External Modification Detection
The system SHALL detect when a thermostat has been manually adjusted outside of the control algorithm and pause automatic control for that thermostat until reset conditions are met.

#### Scenario: Detect external change
- **WHEN** the system has sent a setpoint command with value X at time T
- **AND** at least 2 minutes have elapsed since time T
- **AND** Netatmo reports a setpoint temperature that differs from X by more than 0.1°C
- **THEN** the system SHALL mark that thermostat as externally modified
- **AND** SHALL log the detection with room name and setpoint values

#### Scenario: Pause control for externally modified thermostat
- **WHEN** a thermostat is marked as externally modified
- **THEN** the system SHALL NOT send any SetRoomThermpoint commands for that thermostat
- **AND** SHALL continue normal operation for other thermostats

#### Scenario: Reset on return to schedule mode
- **WHEN** a thermostat is marked as externally modified
- **AND** Netatmo reports therm_setpoint_mode as "schedule"
- **THEN** the system SHALL clear the externally modified flag
- **AND** SHALL resume automatic control on the next loop iteration

#### Scenario: Reset on timeout
- **WHEN** a thermostat is marked as externally modified
- **AND** the configured reset timeout (e.g., 24 hours) has elapsed
- **THEN** the system SHALL clear the externally modified flag
- **AND** SHALL resume automatic control on the next loop iteration

### Requirement: Control Loop Execution
The system SHALL run the thermostat control algorithm on a fixed time interval (default every 1 minute) independent of sensor data arrival.

#### Scenario: Normal loop execution
- **WHEN** the control interval timer expires (e.g., every 60 seconds)
- **THEN** the system SHALL execute one complete control loop iteration
- **AND** SHALL process all configured thermostat mappings

#### Scenario: Handle missing sensor data
- **WHEN** the control loop runs and there are no readings for a required sensor within the last 60 seconds
- **THEN** the system SHALL log a warning indicating sensor data unavailable
- **AND** SHALL skip control for thermostats mapped to that sensor
- **AND** SHALL continue processing other thermostats

#### Scenario: Handle Netatmo API errors
- **WHEN** the control loop encounters an error fetching Netatmo home status or setting a setpoint
- **THEN** the system SHALL log the error with details
- **AND** SHALL continue to the next thermostat without crashing
- **AND** SHALL retry on the next loop iteration

### Requirement: Precedence Hierarchy
The system SHALL apply thermostat control decisions according to a strict precedence hierarchy, with higher-priority conditions overriding lower-priority ones.

#### Scenario: Precedence order
- **WHEN** evaluating whether to control a thermostat
- **THEN** the system SHALL apply the following precedence (highest to lowest):
  1. External modification detected: Skip control entirely
  2. Hard override schedule active: Use override target temperature
  3. Normal algorithm control: Use Netatmo scheduled temperature
  4. Netatmo native schedule: No override sent (default behavior)

#### Scenario: External modification overrides hard schedule
- **WHEN** a thermostat is marked as externally modified
- **AND** a hard override schedule is active for that room
- **THEN** the system SHALL NOT send any setpoint commands (external modification takes precedence)

#### Scenario: Hard override overrides algorithm
- **WHEN** a hard override time window is active
- **AND** Netatmo scheduled temperature differs from hard override target
- **THEN** the system SHALL use the hard override target temperature for control calculations

### Requirement: State Tracking
The system SHALL maintain in-memory state for each controlled thermostat to support re-evaluation timing, external modification detection, and algorithm decisions.

#### Scenario: Track last setpoint command
- **WHEN** the system sends a SetRoomThermpoint command
- **THEN** the system SHALL store the setpoint value and timestamp in that thermostat's state

#### Scenario: Track next re-check time
- **WHEN** the system sends a setpoint override
- **THEN** the system SHALL calculate and store next re-check time as current time plus re-check delay (e.g., 5 minutes)

#### Scenario: Track external modification flag
- **WHEN** external modification is detected for a thermostat
- **THEN** the system SHALL set a boolean flag in that thermostat's state
- **AND** SHALL preserve the flag across control loop iterations until reset conditions occur

#### Scenario: State initialization on startup
- **WHEN** the service starts
- **THEN** the system SHALL initialize empty state for all configured thermostat mappings
- **AND** SHALL NOT assume any previous setpoint commands

### Requirement: Thread Safety
The system SHALL implement thread-safe state management using mutexes to prevent race conditions when the control loop goroutine accesses shared state concurrently with other service components.

#### Scenario: Concurrent state reads
- **WHEN** multiple goroutines need to read thermostat state simultaneously
- **THEN** the system SHALL use `sync.RWMutex.RLock()` to allow concurrent read access without blocking

#### Scenario: State modification
- **WHEN** the control loop updates thermostat state (last setpoint, next check time, external modification flag)
- **THEN** the system SHALL use `sync.RWMutex.Lock()` to ensure exclusive write access
- **AND** SHALL release the lock immediately after updating state (no I/O under lock)

#### Scenario: State access returns copies
- **WHEN** the control loop reads state for a thermostat
- **THEN** the system SHALL return a copy of the state struct to prevent external mutation
- **AND** SHALL NOT return pointers to internal state data

#### Scenario: No blocking I/O under lock
- **WHEN** the control loop needs to call Netatmo API or read from ring buffer
- **THEN** the system SHALL NOT hold any mutex locks during network I/O or blocking operations
- **AND** SHALL read state, release lock, then perform I/O operations

#### Scenario: Data race detection
- **WHEN** running tests for the control package
- **THEN** tests SHALL execute with Go's race detector enabled (`go test -race`)
- **AND** SHALL pass without any race condition warnings

### Requirement: Non-Blocking Operation
The system SHALL ensure the control loop operates without blocking, maintaining responsive operation even under high load or slow network conditions.

#### Scenario: Independent goroutine execution
- **WHEN** the control loop goroutine is running
- **THEN** it SHALL NOT block or interfere with other goroutines (BLE scanner, Netatmo poller, metrics pusher)
- **AND** SHALL handle errors independently without crashing other components

#### Scenario: Control loop iteration independence
- **WHEN** a control loop iteration encounters an error (API failure, missing data)
- **THEN** the system SHALL log the error and continue to the next iteration
- **AND** SHALL NOT block future iterations waiting for recovery

#### Scenario: Timeout on network operations
- **WHEN** calling Netatmo API from the control loop
- **THEN** the system SHALL use context with timeout (inherited from existing HTTP client, 30 seconds)
- **AND** SHALL NOT wait indefinitely for API responses

### Requirement: Configuration Schema
The system SHALL define a thermostatControl configuration section with validation rules to ensure correct setup before entering the control loop.

#### Scenario: Feature flag
- **WHEN** thermostatControl.enabled is set to false
- **THEN** the system SHALL NOT start the control loop goroutine
- **AND** SHALL operate in monitoring-only mode (BLE sensors and Netatmo polling without control)

#### Scenario: Required configuration fields
- **WHEN** thermostatControl.enabled is true
- **AND** any required field is missing (mappings, temperatureThreshold, etc.)
- **THEN** the system SHALL fail validation at startup with a descriptive error message

#### Scenario: Configuration example
- **WHEN** a user needs to configure the thermostat control feature
- **THEN** the system documentation SHALL provide a complete example configuration showing all fields and their default values
