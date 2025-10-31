# Multi-stage Dockerfile for BLE temperature monitoring service

# Builder stage
FROM golang:1.25.3-bookworm AS builder

# Install build dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
    git \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Set working directory
WORKDIR /build

# Copy pkg directory first (dependency)
COPY pkg/ ./pkg/

# Copy thermostats go module files
COPY thermostats/go.mod thermostats/go.sum ./thermostats/

# Set working directory to thermostats
WORKDIR /build/thermostats

# Download dependencies
RUN go mod download

# Copy thermostats source code
COPY thermostats/ .

# Build the binary (from thermostats directory)
RUN CGO_ENABLED=1 GOOS=linux go build -o ble-temp-monitor .

# Runtime stage
FROM debian:bookworm-slim

# Install runtime dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    tzdata \
    bluez \
    && rm -rf /var/lib/apt/lists/*

# Set working directory
WORKDIR /app

# Copy binary from builder
COPY --from=builder /build/thermostats/ble-temp-monitor .

# Copy default config
COPY thermostats/config.yaml .

# Expose no ports (this service doesn't have HTTP API)

# Set default command
ENTRYPOINT ["/app/ble-temp-monitor"]
CMD ["-c", "/app/config.yaml"]
