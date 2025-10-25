# Implementation Tasks

## 1. Project Setup
- [x] 1.1 Create Go module with `go mod init` in pstryk_metric directory
- [x] 1.2 Add required dependencies (cleanenv, prometheus client, YAML/TOML parsers)
- [x] 1.3 Create basic project structure (main.go, config/, scraper/, metrics/)
- [x] 1.4 Create example config.yaml with all parameters and comments

## 2. Configuration Module
- [x] 2.1 Define Config struct with all required fields and struct tags for cleanenv
- [x] 2.2 Implement configuration loading from YAML/TOML file with `-c` flag support
- [x] 2.3 Add file format detection based on extension (.yaml, .yml, .toml)
- [x] 2.4 Add environment variable override support using cleanenv
- [x] 2.5 Implement configuration validation (required fields, valid URLs, positive intervals)
- [x] 2.6 Add secure logging that redacts passwords and credentials
- [ ] 2.7 Write unit tests for config loading and validation with both YAML and TOML

## 3. JSON Data Structures
- [x] 3.1 Define Go structs matching energy meter JSON schema (MultiSensor, Sensor)
- [x] 3.2 Add JSON struct tags for unmarshaling
- [x] 3.3 Implement helper function to filter sensors by type "activePower"
- [ ] 3.4 Write unit tests for JSON parsing with sample data

## 4. HTTP Scraper Implementation
- [x] 4.1 Create HTTP client with configurable timeout
- [x] 4.2 Implement scrape function that fetches and parses JSON from endpoint
- [x] 4.3 Add retry logic with exponential backoff (max 3 attempts)
- [x] 4.4 Implement error handling for network failures and JSON parsing errors
- [x] 4.5 Add logging for scrape success/failure with appropriate detail
- [ ] 4.6 Write unit tests using mock HTTP server

## 5. Data Buffer Implementation
- [x] 5.1 Create ring buffer structure for storing scrape results (capacity: 10)
- [x] 5.2 Implement thread-safe add/read operations using mutex
- [x] 5.3 Add buffer overflow detection and logging
- [ ] 5.4 Write unit tests for concurrent buffer operations

## 6. Scheduling and Timing
- [x] 6.1 Implement even-second start logic with time synchronization
- [x] 6.2 Create scraper ticker goroutine with configurable interval
- [x] 6.3 Create metrics push ticker goroutine with configurable interval
- [x] 6.4 Ensure tickers use time.Ticker for drift-free scheduling
- [x] 6.5 Add graceful shutdown handling (signal capture, ticker cleanup)
- [ ] 6.6 Write integration tests for timing precision

## 7. Prometheus Integration
- [x] 7.1 Set up Prometheus client with remote_write configuration
- [x] 7.2 Implement metric creation with sensor_id labels
- [x] 7.3 Create batch push function that sends buffered metrics
- [x] 7.4 Add basic auth support for Grafana Cloud authentication
- [x] 7.5 Implement retry logic for failed pushes
- [x] 7.6 Add logging for push success/failure with metric counts
- [ ] 7.7 Write integration tests with mock Prometheus endpoint

## 8. Health Check Endpoint
- [x] 8.1 Create HTTP server listening on configurable port (default :8080)
- [x] 8.2 Implement `/health` endpoint returning JSON status
- [x] 8.3 Add logic to determine healthy/unhealthy based on last scrape time
- [x] 8.4 Include diagnostic information (last scrape, last push, buffer size)
- [ ] 8.5 Write unit tests for health check logic

## 9. Main Application Flow
- [x] 9.1 Implement main() function with initialization sequence
- [x] 9.2 Add command-line flag parsing for config file path
- [x] 9.3 Wire up configuration, scraper, buffer, and metrics components
- [x] 9.4 Start goroutines for scraping and pushing with proper coordination
- [x] 9.5 Add graceful shutdown on SIGINT/SIGTERM
- [x] 9.6 Implement structured logging throughout

## 10. Testing and Validation
- [ ] 10.1 Run all unit tests and achieve >80% code coverage
- [ ] 10.2 Test with real energy meter endpoint (if available)
- [ ] 10.3 Test with mock energy meter serving sample JSON
- [ ] 10.4 Verify metrics appear in Grafana Cloud with correct labels and timestamps
- [ ] 10.5 Test configuration via environment variables
- [ ] 10.6 Test error scenarios (network failures, invalid JSON, auth errors)
- [ ] 10.7 Validate even-second start behavior
- [ ] 10.8 Load test with various scrape/push interval combinations

## 11. Documentation
- [x] 11.1 Create README.md with service description and usage instructions
- [x] 11.2 Document all configuration parameters with examples
- [x] 11.3 Add example config.yaml with detailed comments
- [x] 11.4 Add example config.toml as alternative format
- [x] 11.5 Document Grafana Cloud setup steps
- [x] 11.6 Add troubleshooting section for common issues

## 12. Containerization (Optional)
- [ ] 12.1 Create Dockerfile for Go binary
- [ ] 12.2 Add to parent docker-compose.yml if needed
- [ ] 12.3 Configure environment variables in compose file
- [ ] 12.4 Test Docker container deployment
- [ ] 12.5 Document container usage and networking requirements

## Verification Checklist

After implementation, verify:
- [ ] Service starts at even second when configured
- [ ] Scraping occurs at exact configured intervals
- [ ] All activePower sensors are extracted from JSON
- [ ] Metrics pushed to Grafana Cloud every 15 seconds (or configured interval)
- [ ] Metrics visible in Grafana Cloud with sensor_id labels
- [ ] Configuration via environment variables works
- [ ] Service handles network failures gracefully
- [ ] Health check endpoint returns correct status
- [ ] Logs provide useful debugging information
- [ ] No memory leaks during extended operation (test 24h run)
