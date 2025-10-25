# Energy Meter Data Scraping

## ADDED Requirements

### Requirement: HTTP Endpoint Scraping
The service SHALL periodically fetch JSON data from a configured HTTP endpoint representing an energy meter device.

#### Scenario: Successful scrape
- **WHEN** the scrape interval timer triggers
- **THEN** the service performs an HTTP GET request to the configured endpoint
- **AND** parses the JSON response into structured data
- **AND** extracts sensor readings for further processing

#### Scenario: Scrape timeout
- **WHEN** an HTTP request exceeds the configured timeout duration
- **THEN** the service cancels the request
- **AND** logs a timeout warning
- **AND** retries up to 3 times with exponential backoff

#### Scenario: Invalid JSON response
- **WHEN** the HTTP response contains malformed JSON
- **THEN** the service logs a parsing error with a sample of the response
- **AND** skips that scrape cycle
- **AND** continues with the next scheduled scrape

### Requirement: Configurable Scrape Interval
The service SHALL support configurable scraping intervals with a default of 2 seconds.

#### Scenario: Custom interval configuration
- **WHEN** the service starts with a custom scrape interval (e.g., 5 seconds)
- **THEN** the service scrapes the endpoint every 5 seconds
- **AND** maintains consistent intervals regardless of processing time

#### Scenario: Default interval
- **WHEN** no scrape interval is specified in configuration
- **THEN** the service defaults to scraping every 2 seconds

### Requirement: Active Power Extraction
The service SHALL extract all "activePower" sensor readings from the multi-sensor JSON response.

#### Scenario: Multiple active power sensors
- **WHEN** the JSON response contains multiple sensors with type "activePower"
- **THEN** the service extracts the value from each sensor
- **AND** associates each value with its corresponding sensor ID
- **AND** stores all readings for metric export

#### Scenario: Missing active power sensors
- **WHEN** the JSON response contains no sensors with type "activePower"
- **THEN** the service logs a warning
- **AND** does not produce any metrics for that scrape

#### Scenario: Mixed sensor types
- **WHEN** the JSON response contains sensors with types like "activePower", "voltage", "current"
- **THEN** the service extracts only sensors where type equals "activePower"
- **AND** ignores all other sensor types

### Requirement: Even Second Start
The service SHALL start its scraping schedule at an even second timestamp when configured to do so.

#### Scenario: Start at even second enabled
- **WHEN** the service starts with startAtEvenSecond set to true
- **THEN** the service waits until the next even second (0, 2, 4, 6, etc.)
- **AND** begins scraping at that precise moment
- **AND** maintains the schedule from that starting point

#### Scenario: Start at even second disabled
- **WHEN** the service starts with startAtEvenSecond set to false
- **THEN** the service begins scraping immediately without waiting

### Requirement: Error Handling and Resilience
The service SHALL continue operating despite temporary failures in scraping.

#### Scenario: Network failure recovery
- **WHEN** the energy meter endpoint is temporarily unreachable
- **THEN** the service logs the connection error
- **AND** retries the request up to 3 times with exponential backoff (1s, 2s, 4s)
- **AND** continues with the next scheduled scrape after retry exhaustion

#### Scenario: Continuous operation during errors
- **WHEN** multiple consecutive scrapes fail
- **THEN** the service continues attempting scrapes on schedule
- **AND** does not exit or crash
- **AND** logs error counts for monitoring

### Requirement: In-Memory Data Buffering
The service SHALL maintain a ring buffer of recent scrape results in memory.

#### Scenario: Buffer management
- **WHEN** new scrape data arrives and the buffer is not full
- **THEN** the service adds the data to the buffer
- **AND** makes it available for metric pushing

#### Scenario: Buffer overflow
- **WHEN** new scrape data arrives and the buffer is full (10 entries)
- **THEN** the service removes the oldest entry
- **AND** adds the new entry
- **AND** logs a warning if this happens frequently (indicates push lag)
