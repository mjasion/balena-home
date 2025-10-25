# Service Configuration

## ADDED Requirements

### Requirement: Configuration File Loading
The service SHALL load configuration from a YAML or TOML file with support for environment variable overrides.

#### Scenario: Load default configuration file
- **WHEN** the service starts without specifying a config file path
- **THEN** the service attempts to load configuration from "config.yaml" in the working directory
- **AND** applies environment variable overrides to any configured values

#### Scenario: Load custom configuration file
- **WHEN** the service starts with a `-c /path/to/config.yaml` or `-c /path/to/config.toml` command-line flag
- **THEN** the service loads configuration from the specified file path
- **AND** detects the format based on file extension (.yaml, .yml, or .toml)
- **AND** applies environment variable overrides

#### Scenario: Missing configuration file
- **WHEN** the specified configuration file does not exist
- **THEN** the service logs a fatal error with the file path
- **AND** exits with a non-zero status code

#### Scenario: Invalid YAML or TOML syntax
- **WHEN** the configuration file contains invalid YAML or TOML syntax
- **THEN** the service logs a detailed parsing error with line number if available
- **AND** exits with a non-zero status code

### Requirement: Environment Variable Overrides
The service SHALL support environment variable overrides for all configuration parameters.

#### Scenario: Override scrape URL
- **WHEN** the SCRAPE_URL environment variable is set
- **THEN** the service uses that value instead of the config file value
- **AND** logs which configuration source was used

#### Scenario: Override sensitive credentials
- **WHEN** PROMETHEUS_PASSWORD environment variable is set
- **THEN** the service uses the environment variable value
- **AND** does not log the password value in any output

#### Scenario: Multiple overrides
- **WHEN** multiple environment variables are set (e.g., SCRAPE_INTERVAL_SECONDS, PUSH_INTERVAL_SECONDS)
- **THEN** the service applies all overrides
- **AND** uses config file values for parameters without environment variables

### Requirement: Configuration Validation
The service SHALL validate all configuration parameters at startup before beginning operation.

#### Scenario: Valid configuration
- **WHEN** all required configuration parameters are provided and valid
- **THEN** the service logs "Configuration loaded successfully"
- **AND** proceeds to start scraping and pushing

#### Scenario: Missing required parameters
- **WHEN** required parameters like scrapeUrl or prometheusUrl are empty
- **THEN** the service logs a validation error listing missing parameters
- **AND** exits with a non-zero status code

#### Scenario: Invalid interval values
- **WHEN** scrapeIntervalSeconds is less than or equal to 0
- **THEN** the service logs a validation error
- **AND** exits with a non-zero status code

#### Scenario: Invalid URL format
- **WHEN** scrapeUrl or prometheusUrl is not a valid HTTP/HTTPS URL
- **THEN** the service logs a URL validation error
- **AND** exits with a non-zero status code

### Requirement: Configuration Schema
The service SHALL support the following configuration parameters in YAML or TOML format.

#### Scenario: Complete YAML configuration
- **WHEN** a YAML configuration file includes all parameters
- **THEN** the service accepts the following structure:
  - `scrapeUrl` (string, required): HTTP endpoint to scrape
  - `scrapeIntervalSeconds` (int, default: 2): How often to scrape
  - `scrapeTimeoutSeconds` (float, default: 1.5): HTTP request timeout
  - `pushIntervalSeconds` (int, default: 15): How often to push metrics
  - `prometheusUrl` (string, required): Prometheus remote_write endpoint
  - `prometheusUsername` (string, required): Basic auth username
  - `prometheusPassword` (string, required): Basic auth password
  - `metricName` (string, default: "active_power_watts"): Prometheus metric name
  - `startAtEvenSecond` (bool, default: true): Whether to start at even second

#### Scenario: TOML format support
- **WHEN** a configuration file with .toml extension is provided
- **THEN** the service parses it as TOML format
- **AND** accepts the same parameter structure as YAML
- **AND** supports TOML-style comments with #

### Requirement: Secure Credential Handling
The service SHALL handle sensitive credentials securely.

#### Scenario: Redacted logging
- **WHEN** the service logs configuration at startup
- **THEN** password fields are redacted (shown as "***")
- **AND** URLs with embedded credentials are sanitized

#### Scenario: Memory security
- **WHEN** the service reads credentials from configuration or environment
- **THEN** credentials are stored in memory only as long as needed
- **AND** not written to disk or temporary files
