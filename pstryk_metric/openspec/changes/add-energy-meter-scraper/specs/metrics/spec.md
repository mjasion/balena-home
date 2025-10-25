# Prometheus Metrics Export

## ADDED Requirements

### Requirement: Prometheus Remote Write Integration
The service SHALL push metrics to Grafana Cloud using the Prometheus remote_write protocol.

#### Scenario: Successful metric push
- **WHEN** the push interval timer triggers and buffered metrics exist
- **THEN** the service batches all buffered active power readings
- **AND** sends them to the configured Prometheus endpoint using remote_write protocol
- **AND** authenticates using basic auth credentials
- **AND** clears the buffer after successful push

#### Scenario: Push authentication failure
- **WHEN** the Prometheus endpoint returns 401 Unauthorized
- **THEN** the service logs an authentication error
- **AND** retries with exponential backoff up to 3 times
- **AND** keeps metrics in buffer if all retries fail

#### Scenario: Push network failure
- **WHEN** the network connection to Prometheus fails
- **THEN** the service logs the connection error
- **AND** retries the push operation
- **AND** maintains metrics in buffer for the next push attempt

### Requirement: Metric Format and Labels
The service SHALL format metrics according to Prometheus conventions with appropriate labels.

#### Scenario: Active power metric with sensor labels
- **WHEN** exporting active power readings for multiple sensors
- **THEN** each metric includes the base metric name (e.g., "active_power_watts")
- **AND** includes a "sensor_id" label with the sensor's ID value
- **AND** includes a timestamp from when the reading was scraped
- **AND** includes the numeric value in watts

#### Scenario: Example metric format
- **WHEN** sensor 0 reports 237 watts at timestamp T
- **THEN** the metric is formatted as:
  ```
  active_power_watts{sensor_id="0"} 237 T
  active_power_watts{sensor_id="1"} 30 T
  active_power_watts{sensor_id="2"} 188 T
  active_power_watts{sensor_id="3"} 19 T
  ```

### Requirement: Configurable Push Interval
The service SHALL support configurable intervals for pushing metrics to Prometheus.

#### Scenario: Custom push interval
- **WHEN** the service is configured with pushIntervalSeconds of 30
- **THEN** the service pushes metrics every 30 seconds
- **AND** accumulates multiple scrape results between pushes

#### Scenario: Default push interval
- **WHEN** no push interval is specified in configuration
- **THEN** the service defaults to pushing metrics every 15 seconds

#### Scenario: Push interval larger than scrape interval
- **WHEN** pushInterval (15s) is larger than scrapeInterval (2s)
- **THEN** the service accumulates approximately 7-8 scrape results
- **AND** pushes them in a single batch
- **AND** each scrape result maintains its original timestamp

### Requirement: Metric Buffering and Batching
The service SHALL batch multiple metric samples before pushing to reduce network overhead.

#### Scenario: Batch multiple time series
- **WHEN** multiple scrapes occur between push intervals
- **THEN** the service collects all active power readings
- **AND** organizes them by timestamp and sensor_id
- **AND** sends them in a single remote_write request

#### Scenario: Empty buffer
- **WHEN** the push interval triggers but no new scrapes have succeeded
- **THEN** the service skips the push operation
- **AND** logs a warning about lack of fresh data

### Requirement: Health Check Endpoint
The service SHALL expose an HTTP health check endpoint for monitoring.

#### Scenario: Healthy service
- **WHEN** a request is made to `/health`
- **AND** the last scrape succeeded within 2x the scrape interval
- **THEN** the endpoint returns HTTP 200 OK
- **AND** returns JSON with status and last scrape timestamp

#### Scenario: Unhealthy service
- **WHEN** a request is made to `/health`
- **AND** scraping has been failing for more than 2x the scrape interval
- **THEN** the endpoint returns HTTP 503 Service Unavailable
- **AND** returns JSON with error details

#### Scenario: Health check response format
- **WHEN** the health endpoint is queried
- **THEN** the response is JSON formatted as:
  ```json
  {
    "status": "healthy",
    "lastScrapeTime": "2025-10-24T21:30:00Z",
    "lastPushTime": "2025-10-24T21:30:00Z",
    "bufferedSamples": 5
  }
  ```

### Requirement: Push Failure Handling
The service SHALL handle push failures gracefully without losing recent data.

#### Scenario: Temporary push failure with buffer retention
- **WHEN** a push to Prometheus fails due to network issues
- **THEN** the service keeps the buffered metrics
- **AND** retries on the next push interval
- **AND** continues accepting new scrape data (up to buffer limit)

#### Scenario: Persistent push failures
- **WHEN** push operations fail repeatedly for 5+ consecutive attempts
- **THEN** the service logs a critical error
- **AND** continues scraping and buffering up to the buffer limit
- **AND** drops oldest samples when buffer is full

### Requirement: Timestamp Accuracy
The service SHALL use accurate timestamps reflecting when each measurement was scraped.

#### Scenario: Preserve scrape timestamps
- **WHEN** a scrape occurs at timestamp T1
- **AND** the metric is pushed at timestamp T2 (where T2 > T1)
- **THEN** the metric uses timestamp T1 (scrape time)
- **AND** Prometheus stores the metric with the original scrape time

#### Scenario: Clock skew handling
- **WHEN** system time changes during operation
- **THEN** the service uses monotonic time for internal scheduling
- **AND** uses wall clock time for metric timestamps
- **AND** logs a warning if it detects significant time jumps
