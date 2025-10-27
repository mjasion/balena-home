# BLE Sensor Monitor Specification

## ADDED Requirements

### Requirement: BLE Advertisement Scanning

The system SHALL passively scan for Bluetooth Low Energy advertisements from LYWSD03MMC temperature sensors without establishing active connections.

#### Scenario: Continuous passive scanning

- **WHEN** the service starts
- **THEN** it SHALL initialize the BLE adapter and begin listening for advertisement packets

#### Scenario: Energy-efficient operation

- **WHEN** scanning for BLE advertisements
- **THEN** the system SHALL use passive scanning mode to minimize power consumption
- **AND** SHALL NOT initiate connections to sensor devices

### Requirement: ATC Advertisement Format Support

The system SHALL decode temperature data from LYWSD03MMC sensors running ATC_MiThermometer custom firmware using the ATC advertisement format (UUID 0x181A).

#### Scenario: Parse ATC format advertisement

- **WHEN** an advertisement packet with UUID 0x181A is received
- **THEN** the system SHALL extract MAC address (6 bytes), temperature (int16, °C × 10), humidity (uint8, %), battery level (uint8, %), battery voltage (uint16, mV), and frame counter (uint8)

#### Scenario: Temperature conversion

- **WHEN** raw temperature value is extracted
- **THEN** the system SHALL divide by 10 to convert to degrees Celsius with 0.1°C precision

### Requirement: Multi-Sensor Configuration

The system SHALL support monitoring multiple LYWSD03MMC sensors simultaneously through configuration.

#### Scenario: Configure sensor list

- **WHEN** the service loads configuration
- **THEN** it SHALL read a list of expected sensor MAC addresses from the config file or environment variables

#### Scenario: Filter advertisements

- **WHEN** BLE advertisements are received
- **THEN** the system SHALL only process advertisements from configured sensor MAC addresses
- **AND** SHALL ignore advertisements from other devices

### Requirement: Sensor Reading Logging

The system SHALL log sensor readings using zap structured logging for monitoring and debugging.

#### Scenario: Log sensor readings

- **WHEN** sensor readings are decoded from BLE advertisements
- **THEN** the system SHALL log readings using zap with structured fields: timestamp, mac_address, temperature_celsius, humidity_percent, battery_percent, battery_voltage_mv, rssi
- **AND** SHALL use appropriate log levels (info for successful readings, warn for sensor timeouts)

#### Scenario: Missing sensor data

- **WHEN** a configured sensor has not sent advertisements within the expected interval
- **THEN** the system SHALL log a warning with the sensor MAC address and last seen timestamp

### Requirement: Configuration Management

The system SHALL load configuration from a YAML file with support for environment variable overrides following the project's cleanenv pattern.

#### Scenario: Load from config file

- **WHEN** the service starts with a config file path specified via `-c` flag
- **THEN** it SHALL load configuration from the specified YAML file
- **AND** SHALL use default values for any missing fields

#### Scenario: Environment variable overrides

- **WHEN** environment variables are set (e.g., SCAN_INTERVAL_SECONDS, SENSORS, PROMETHEUS_URL)
- **THEN** they SHALL override corresponding values from the config file

#### Scenario: Sensor MAC list configuration

- **WHEN** configuring sensor MAC addresses
- **THEN** the system SHALL accept a comma-separated list via SENSORS environment variable or sensors array in config.yaml

### Requirement: Raspberry Pi Deployment

The system SHALL run on Raspberry Pi hardware with Bluetooth support and be compatible with Balena deployment.

#### Scenario: Bluetooth adapter access

- **WHEN** deployed on Raspberry Pi
- **THEN** the system SHALL access the built-in Bluetooth adapter (e.g., via BlueZ on Linux)
- **AND** SHALL require appropriate permissions (typically running as root or with CAP_NET_ADMIN capability)

#### Scenario: Containerized deployment

- **WHEN** deployed in a Docker container on Balena
- **THEN** the system SHALL function with host Bluetooth adapter access via privileged mode or device mapping
- **AND** SHALL be compatible with Docker Compose network configurations

### Requirement: Error Handling and Logging

The system SHALL handle BLE scanning errors gracefully and log operational information to stderr.

#### Scenario: BLE adapter initialization failure

- **WHEN** the Bluetooth adapter cannot be initialized
- **THEN** the system SHALL log an error to stderr and exit with a non-zero status code

#### Scenario: Advertisement parsing errors

- **WHEN** an advertisement packet cannot be parsed
- **THEN** the system SHALL log a warning to stderr and continue scanning
- **AND** SHALL NOT crash or stop the service

#### Scenario: Operational logging

- **WHEN** the service is running
- **THEN** it SHALL log informational messages to stderr including service start, sensor discoveries, and configuration details
- **AND** SHALL separate data output (stdout) from logs (stderr)

### Requirement: Extensibility for Future Thermostat Integration

The system architecture SHALL support future extension to integrate with Netatmo thermostats for temperature-based control.

#### Scenario: Modular design

- **WHEN** implementing the BLE monitoring service
- **THEN** sensor data collection SHALL be separated from output formatting
- **AND** SHALL expose an internal API or data structure that can be consumed by future thermostat control modules

#### Scenario: Data retention for control logic

- **WHEN** sensor readings are received
- **THEN** the system SHALL maintain a recent history of readings in memory
- **AND** SHALL make this data available for future control logic (e.g., averaging temperatures across sensors)
