# Build stage
FROM golang:1.25.3-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git

# Set working directory
WORKDIR /build

# Copy pkg directory first (dependency)
COPY pkg/ ./pkg/

# Copy pstryk_metric go module files
COPY pstryk_metric/go.mod pstryk_metric/go.sum ./pstryk_metric/

# Set working directory to pstryk_metric
WORKDIR /build/pstryk_metric

# Download dependencies
RUN go mod download

# Copy pstryk_metric source code
COPY pstryk_metric/ .

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o pstryk_metric .

# Runtime stage
FROM alpine:latest

# Install ca-certificates for HTTPS and tzdata for timezone support
RUN apk --no-cache add ca-certificates tzdata

# Create app directory
WORKDIR /app

# Copy binary from builder
COPY --from=builder /build/pstryk_metric/pstryk_metric .

# Copy default config (can be overridden with volume mount)
COPY pstryk_metric/config.yaml ./config.yaml

# Expose health check port
EXPOSE 8080

# Run as non-root user
RUN addgroup -g 1000 appuser && \
    adduser -D -u 1000 -G appuser appuser && \
    chown -R appuser:appuser /app

USER appuser

# Run the application
CMD ["./pstryk_metric"]
