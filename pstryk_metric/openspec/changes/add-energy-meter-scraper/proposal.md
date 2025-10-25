# Energy Meter Scraper Service

## Why

The home automation system needs to collect real-time energy metrics from smart energy meter devices and forward them to Grafana Cloud for monitoring and analysis. Currently, there is no automated way to scrape, parse, and push energy meter data to observability platforms.

## What Changes

- **New Go service**: Creates a standalone service that scrapes multi-sensor energy meter JSON endpoints
- **Configurable scraping**: Scrapes HTTP endpoints at configurable intervals (default: every 2 seconds)
- **Metric extraction**: Parses JSON response and extracts all "activePower" sensor values
- **Prometheus integration**: Pushes metrics to Grafana Cloud Prometheus remote_write endpoint at configurable intervals (default: every 15 seconds)
- **Precise timing**: Service starts at even seconds and maintains precise scheduling
- **Flexible configuration**: Supports configuration via JSON file and environment variable overrides

## Impact

- Affected specs: **NEW** - `scraper`, `config`, `metrics`
- Affected code: **NEW** - All new code in `pstryk_metric/` directory
- Dependencies: Prometheus client library for Go
- Infrastructure: Requires network access to energy meter device and Grafana Cloud
