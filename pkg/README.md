# Common Library (pkg/)

Shared packages for Balena Home services providing unified observability, configuration, and data structures.

## Packages

### buffer

Generic thread-safe ring buffer implementation using Go generics.

**Features:**
- Type-safe with zero runtime overhead
- Thread-safe with `sync.RWMutex`
- Circular buffer with automatic overflow handling
- Atomic `GetAllAndClear()` operation

**Usage:**
```go
import "github.com/mjasion/balena-home/pkg/buffer"

// Create buffer for any type
buf := buffer.New[*MyStruct](1000, logger)
buf.Add(item)
items := buf.GetAllAndClear()  // Atomically retrieve and clear
```

### config

Common configuration structures and utilities.

**Modules:**
- `otel.go` - OpenTelemetry configuration structs
- `logging.go` - Logging configuration + logger creation

**Usage:**
```go
import "github.com/mjasion/balena-home/pkg/config"

// Embed in your service config
type ServiceConfig struct {
    // Service-specific fields
    ScrapeURL string

    // Common configs
    config.OpenTelemetryConfig `yaml:"opentelemetry"`
    config.LoggingConfig       `yaml:"logging"`
}

// Create logger
logger, err := config.NewLogger(&cfg.LoggingConfig)

// Validate OpenTelemetry config
err = config.ValidateOpenTelemetry(&cfg.OpenTelemetryConfig)
```

### telemetry

OpenTelemetry providers initialization and context-aware logging.

**Features:**
- Tracer provider with OTLP HTTP exporter
- Meter provider with runtime metrics
- Context-aware logging helpers
- Automatic trace ID injection

**Usage:**
```go
import "github.com/mjasion/balena-home/pkg/telemetry"

// Initialize providers
providers, err := telemetry.InitProviders(ctx, &cfg.OpenTelemetryConfig, logger)
defer providers.Shutdown(ctx)

// Use context-aware logging
telemetry.InfoWithTrace(ctx, logger, "message", zap.String("key", "value"))
```

## Design Principles

1. **Generic where possible** - Use Go generics to eliminate duplication
2. **Minimal dependencies** - Service configs embed common types
3. **Type-safe** - Compile-time validation
4. **Zero business logic** - Pure infrastructure code
5. **Well-tested** - Comprehensive test coverage

## Integration with Services

Services use Go module replace directives for local development:

```go
// go.mod in service directory
module github.com/mjasion/balena-home/pstryk_metric

require github.com/mjasion/balena-home/pkg v0.0.0

replace github.com/mjasion/balena-home/pkg => ../pkg
```

## Benefits

- **-1000 lines** of duplicated code eliminated
- **Single source of truth** for observability
- **Consistent patterns** across all services
- **Easy testing** - Test once, use everywhere
- **Future-proof** - New services just import and use

## Testing

```bash
cd pkg
go test ./...
```

## Version

All packages follow semantic versioning. Breaking changes require major version bump.
