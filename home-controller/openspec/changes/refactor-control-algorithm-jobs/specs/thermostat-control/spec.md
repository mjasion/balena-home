## MODIFIED Requirements

### Requirement: Temperature Difference Threshold
The system SHALL only trigger thermostat adjustments when the absolute difference between Xiaomi sensor temperature and scheduled temperature exceeds a configurable threshold (default 0.2°C).

#### Scenario: Difference below threshold
- **WHEN** the absolute temperature difference is less than the configured threshold (e.g., 0.1°C when threshold is 0.2°C)
- **THEN** the system SHALL set the thermostat setpoint to the current Netatmo measured temperature to maintain stability

#### Scenario: Difference at or above threshold
- **WHEN** the absolute temperature difference is greater than or equal to the configured threshold (e.g., 0.2°C when threshold is 0.2°C)
- **THEN** the system SHALL calculate and apply a new thermostat setpoint using the simplified three-zone algorithm

#### Scenario: Threshold configuration validation
- **WHEN** the configured temperature threshold is less than 0.1°C or greater than 5.0°C
- **THEN** the system SHALL fail validation at startup with a clear error message

### Requirement: Setpoint Compensation Algorithm
The system SHALL calculate thermostat setpoint adjustments using a simplified three-zone algorithm based on the Netatmo measured temperature with fixed 0.5°C adjustments.

#### Scenario: Room cooler than target (too cold)
- **WHEN** average Xiaomi temperature minus scheduled temperature is less than or equal to negative threshold (e.g., -0.2°C)
- **THEN** the system SHALL calculate new setpoint as netatmo_measured + 0.5°C
- **AND** SHALL send SetRoomThermpoint command to trigger heating

#### Scenario: Room within acceptable range
- **WHEN** average Xiaomi temperature minus scheduled temperature is between negative threshold and positive threshold (e.g., -0.2°C < diff < 0.2°C)
- **THEN** the system SHALL calculate new setpoint as netatmo_measured (maintain current)
- **AND** SHALL send SetRoomThermpoint command to maintain stability

#### Scenario: Room warmer than target (too warm)
- **WHEN** average Xiaomi temperature minus scheduled temperature is greater than or equal to positive threshold (e.g., 0.2°C)
- **THEN** the system SHALL calculate new setpoint as netatmo_measured - 0.5°C
- **AND** SHALL send SetRoomThermpoint command to stop heating

### Requirement: Auto-Expiring Overrides
The system SHALL set all thermostat setpoint adjustments as temporary manual overrides with end times aligned to 15-minute schedule boundaries, ensuring automatic rollback to Netatmo schedule.

#### Scenario: Set boundary-aligned override
- **WHEN** the control algorithm calculates a new setpoint
- **THEN** the system SHALL call SetRoomThermpoint with mode "manual" and endtime set to the second before the next 15-minute boundary (e.g., :14:59, :29:59, :44:59, :59:59)

#### Scenario: Override expiration at boundary
- **WHEN** the override end time is reached (e.g., 12:14:59)
- **THEN** the Netatmo thermostat SHALL automatically revert to its scheduled temperature
- **AND** the Control Job SHALL read fresh scheduled temperature at the next 15-minute mark

#### Scenario: Service crash recovery
- **WHEN** the home-controller service crashes or stops
- **THEN** all active thermostat overrides SHALL expire at the next 15-minute boundary (maximum 15 minutes)
- **AND** thermostats SHALL revert to Netatmo's native schedule without manual intervention

### Requirement: External Modification Detection
The system SHALL detect when a thermostat has been manually adjusted by a human (override duration >= 60 minutes) and skip automatic control for that thermostat.

#### Scenario: Detect human override by duration
- **WHEN** evaluating a room in manual mode
- **AND** the override duration (therm_setpoint_end_time - therm_setpoint_start_time) is greater than or equal to 60 minutes
- **THEN** the system SHALL treat this as a human override
- **AND** SHALL skip control for this room

#### Scenario: Algorithm override detected
- **WHEN** evaluating a room in manual mode
- **AND** the override duration (therm_setpoint_end_time - therm_setpoint_start_time) is less than 60 minutes
- **THEN** the system SHALL treat this as an algorithm-set override
- **AND** SHALL proceed with control (wait for expiration or override)

#### Scenario: Resume after human override expires
- **WHEN** a thermostat was skipped due to human override (>= 60 min)
- **AND** the override expires and thermostat returns to schedule mode
- **THEN** the system SHALL resume automatic control on the next Control Job iteration

### Requirement: Control Loop Execution
The system SHALL run the thermostat control algorithm at fixed 15-minute intervals (:00, :15, :30, :45) using data provided by the Metric Job via channel.

#### Scenario: Control Job receives data from Metric Job
- **WHEN** Control Job starts at a 15-minute boundary (e.g., 12:00)
- **THEN** it SHALL wait for HomeStatusResponse from the Metric Job channel
- **AND** SHALL use this data for control decisions

#### Scenario: Concurrent per-room processing
- **WHEN** Control Job receives home status data
- **THEN** it SHALL spawn a goroutine for each configured room
- **AND** rooms in schedule mode SHALL be processed immediately
- **AND** rooms waiting for manual mode to expire SHALL wait independently

#### Scenario: Room waiting for manual mode expiration
- **WHEN** a room is in manual mode with algorithm-set override (< 60 min duration)
- **AND** the override expires within the current 15-minute window
- **THEN** the room's goroutine SHALL calculate wait time from therm_setpoint_end_time
- **AND** SHALL sleep until expiration + 1 second
- **AND** SHALL make its own GetHomeStatus API call to read fresh schedule temperature
- **AND** SHALL proceed with control decision

#### Scenario: Room with override expiring outside window
- **WHEN** a room is in manual mode
- **AND** the override expires after the current 15-minute window ends
- **THEN** the system SHALL skip this room for this cycle

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
  1. Human override detected (duration >= 60 min): Skip control entirely
  2. Hard override schedule active: Control Job skips, Hard Override Job handles
  3. Thermostat unreachable: Skip control
  4. Manual mode expiring outside window: Skip control
  5. Normal algorithm control: Use Netatmo scheduled temperature

#### Scenario: Hard override skips Control Job
- **WHEN** a hard override time window is active for a room
- **THEN** the Control Job SHALL skip this room entirely
- **AND** the Hard Override Job SHALL handle temperature control for this room

### Requirement: State Tracking
The system SHALL maintain minimal in-memory state for each controlled thermostat to support room identification only.

#### Scenario: State initialization on startup
- **WHEN** the service starts
- **THEN** the system SHALL initialize state with RoomID and RoomName for all configured thermostat mappings
- **AND** SHALL NOT track setpoint history or timing state

#### Scenario: Minimal state structure
- **WHEN** the control algorithm evaluates a room
- **THEN** it SHALL use only RoomID and RoomName from state
- **AND** SHALL derive all other information from current API response (mode, setpoint, end_time)

### Requirement: Configuration Schema
The system SHALL define a thermostatControl configuration section with validation rules to ensure correct setup before entering the control loop.

#### Scenario: Feature flag
- **WHEN** thermostatControl.enabled is set to false
- **THEN** the system SHALL NOT start Metric Job, Control Job, or Hard Override Job
- **AND** SHALL operate in monitoring-only mode (BLE sensors without control)

#### Scenario: Required configuration fields
- **WHEN** thermostatControl.enabled is true
- **AND** any required field is missing (mappings, temperatureThreshold, etc.)
- **THEN** the system SHALL fail validation at startup with a descriptive error message

#### Scenario: New cron configuration fields
- **WHEN** configuring thermostat control
- **THEN** the system SHALL accept metricJobCron (default: every minute), controlJobCron (default: at :00, :15, :30, :45), and hardOverrideJobCron (default: every minute)

#### Scenario: Configuration example
- **WHEN** a user needs to configure the thermostat control feature
- **THEN** the system documentation SHALL provide a complete example configuration showing all fields and their default values

## ADDED Requirements

### Requirement: Metric Job
The system SHALL run a dedicated Metric Job that fetches Netatmo home status every minute and provides data to Control Job via channel.

#### Scenario: Metric Job execution
- **WHEN** the Metric Job cron triggers (every minute)
- **THEN** the system SHALL call GetHomeStatus API
- **AND** SHALL send the response to the homeStatusChannel
- **AND** SHALL add Netatmo readings to the metrics buffer for Prometheus

#### Scenario: Metric Job priority
- **WHEN** both Metric Job and Control Job need API access
- **THEN** Metric Job SHALL complete its fetch before Control Job processes
- **AND** Control Job SHALL wait for data on the channel

#### Scenario: Channel communication
- **WHEN** Metric Job completes a fetch
- **THEN** it SHALL send HomeStatusResponse to a buffered channel
- **AND** Control Job SHALL read from this channel at 15-minute boundaries

### Requirement: Hard Override Job
The system SHALL run a dedicated Hard Override Job that handles time-based temperature overrides independently from the Control Job.

#### Scenario: Hard Override Job execution
- **WHEN** the Hard Override Job cron triggers (every minute)
- **THEN** the system SHALL check if any hard override time window is currently active
- **AND** SHALL apply temperature control using the same three-zone algorithm

#### Scenario: Hard override target temperature
- **WHEN** a hard override time window is active (e.g., 06:00-06:20, target 25°C)
- **THEN** the Hard Override Job SHALL use the configured target temperature instead of Netatmo's schedule
- **AND** SHALL calculate setpoint using the same three-zone algorithm

#### Scenario: Hard override boundary-aligned expiration
- **WHEN** the Hard Override Job sets an override
- **THEN** the override end time SHALL be set to the second before the next 15-minute boundary
- **AND** SHALL not extend beyond the hard override time window end

#### Scenario: Skip if human override active
- **WHEN** a room has a human override (duration >= 60 min)
- **THEN** the Hard Override Job SHALL skip this room
- **AND** SHALL respect user intent

#### Scenario: Own cron configuration
- **WHEN** configuring the Hard Override Job
- **THEN** the system SHALL accept hardOverrideJobCron configuration
- **AND** SHALL default to every minute execution

### Requirement: Job Coordination
The system SHALL coordinate the three jobs (Metric, Control, Hard Override) to ensure proper data flow and avoid conflicts.

#### Scenario: Control Job waits for Metric Job
- **WHEN** Control Job starts at a 15-minute boundary
- **THEN** it SHALL block on the homeStatusChannel until Metric Job provides data
- **AND** SHALL only use data from the current minute (not stale cached data)

#### Scenario: Hard Override Job independent execution
- **WHEN** Hard Override Job runs
- **THEN** it SHALL make its own API calls as needed
- **AND** SHALL NOT depend on the homeStatusChannel

#### Scenario: No concurrent setpoint commands
- **WHEN** Control Job and Hard Override Job both want to control a room
- **THEN** Control Job SHALL skip rooms with active hard override windows
- **AND** only Hard Override Job SHALL send setpoint commands for those rooms

## REMOVED Requirements

### Requirement: Re-Evaluation After Adjustment
**Reason**: The new 15-minute boundary-aligned timing eliminates the need for re-evaluation delays. Control runs at fixed intervals and overrides expire at boundaries.
**Migration**: Control Job runs at :00, :15, :30, :45 - natural re-evaluation happens at each boundary.

### Requirement: Sensor Reading Aggregation with Weighted Average
**Reason**: NOT REMOVED - this requirement remains unchanged. Keeping weighted average logic.
**Note**: This entry is a clarification only - requirement stays in spec.
