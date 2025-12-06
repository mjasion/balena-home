# Netatmo Integration Specification

## ADDED Requirements

### Requirement: SetRoomThermpoint API
The system SHALL implement the Netatmo SetRoomThermpoint API to programmatically adjust thermostat setpoint temperatures with duration-based manual overrides.

#### Scenario: Set manual override with duration
- **WHEN** the control algorithm needs to adjust a thermostat
- **THEN** the system SHALL call POST /api/setroomthermpoint with parameters:
  - home_id: Netatmo home identifier
  - room_id: Netatmo room identifier
  - mode: "manual"
  - temp: Desired setpoint temperature in Celsius
  - endtime: Unix timestamp when override should expire

#### Scenario: Successful setpoint change
- **WHEN** SetRoomThermpoint API call succeeds (HTTP 200)
- **THEN** the system SHALL log the successful change with room name, new setpoint, and expiration time
- **AND** SHALL update internal state with the sent setpoint value and timestamp

#### Scenario: API authentication failure
- **WHEN** SetRoomThermpoint API call fails with 401 or 403 status
- **THEN** the system SHALL refresh the OAuth2 access token
- **AND** SHALL retry the API call once
- **AND** SHALL log an error if the retry fails

#### Scenario: API error response
- **WHEN** SetRoomThermpoint API call fails with 4xx or 5xx status (excluding 401/403)
- **THEN** the system SHALL log the error with HTTP status code and response body
- **AND** SHALL NOT update internal state
- **AND** SHALL NOT crash or halt the control loop

#### Scenario: Network timeout
- **WHEN** SetRoomThermpoint API call times out (exceeds HTTP client timeout)
- **THEN** the system SHALL log the timeout error
- **AND** SHALL continue to process other thermostats
- **AND** SHALL retry on the next control loop iteration

### Requirement: Schedule Data Retrieval
The system SHALL retrieve the currently scheduled temperature for each room by parsing the therm_setpoint_temperature and therm_setpoint_mode fields from the existing homestatus API response.

#### Scenario: Room in schedule mode
- **WHEN** Netatmo homestatus API returns a room with therm_setpoint_mode = "schedule"
- **THEN** the system SHALL use the therm_setpoint_temperature value as the scheduled target temperature for control algorithm calculations

#### Scenario: Room in away mode
- **WHEN** Netatmo homestatus API returns a room with therm_setpoint_mode = "away"
- **THEN** the system SHALL respect the away mode and NOT send thermostat control commands for that room
- **AND** SHALL log that the room is in away mode

#### Scenario: Room in frost guard mode
- **WHEN** Netatmo homestatus API returns a room with therm_setpoint_mode = "hg" (frost guard)
- **THEN** the system SHALL respect the frost guard mode and NOT send thermostat control commands for that room
- **AND** SHALL log that the room is in frost guard mode

#### Scenario: Room in manual mode
- **WHEN** Netatmo homestatus API returns a room with therm_setpoint_mode = "manual"
- **THEN** the system SHALL check if this is our own override or an external modification
- **AND** SHALL apply external modification detection logic

### Requirement: OAuth2 Token Management for Write Operations
The system SHALL reuse the existing OAuth2 client implementation for SetRoomThermpoint API calls, ensuring valid access tokens with write scope.

#### Scenario: Token refresh before write
- **WHEN** the control loop needs to call SetRoomThermpoint
- **AND** the current access token is expired or about to expire (within 5 minutes)
- **THEN** the system SHALL automatically refresh the OAuth2 token before making the API call

#### Scenario: Write scope validation
- **WHEN** the system initializes the Netatmo client
- **THEN** the OAuth2 refresh token SHALL have been granted with read_thermostat and write_thermostat scopes
- **AND** the system SHALL document required scopes in configuration examples

### Requirement: Request/Response Types for Write API
The system SHALL define Go types for SetRoomThermpoint request parameters and response structure.

#### Scenario: SetRoomThermpoint request type
- **WHEN** constructing a SetRoomThermpoint API call
- **THEN** the system SHALL use a SetRoomThermpoint request type containing:
  - HomeID (string)
  - RoomID (string)
  - Mode (string, "manual")
  - Temperature (float64, in Celsius)
  - EndTime (int64, Unix timestamp)

#### Scenario: SetRoomThermpoint response type
- **WHEN** receiving a response from SetRoomThermpoint API
- **THEN** the system SHALL parse a response type containing:
  - Status (string, "ok" or error code)
  - TimeExec (float64, API execution time)
  - TimeServer (int64, server timestamp)

### Requirement: API Rate Limiting and Backoff
The system SHALL implement basic rate limit handling for Netatmo write API to prevent exceeding API quotas.

#### Scenario: Rate limit exceeded
- **WHEN** SetRoomThermpoint API call returns HTTP 429 (Too Many Requests)
- **THEN** the system SHALL log the rate limit error
- **AND** SHALL skip sending further commands for that control loop iteration
- **AND** SHALL retry on the next control loop iteration (1 minute later)

#### Scenario: Rate limit monitoring
- **WHEN** the system successfully sends SetRoomThermpoint commands
- **THEN** the system SHALL track the count of API calls per room in Prometheus metrics
- **AND** SHALL allow operators to monitor API usage via Grafana dashboards
