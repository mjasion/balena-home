package pyroscope

import (
	"fmt"
	"os"
	"runtime"

	"github.com/grafana/pyroscope-go"
	"github.com/mjasion/balena-home/thermostats/config"
	"go.uber.org/zap"
)

// Profiler manages Pyroscope continuous profiling
type Profiler struct {
	profiler *pyroscope.Profiler
	logger   *zap.Logger
}

// New creates a new Pyroscope profiler based on the configuration
func New(cfg *config.PyroscopeConfig, logger *zap.Logger) (*Profiler, error) {
	if !cfg.Enabled {
		logger.Info("pyroscope profiling is disabled")
		return &Profiler{logger: logger}, nil
	}

	logger.Info("initializing pyroscope profiler",
		zap.String("server_url", cfg.ServerURL),
		zap.String("application_name", cfg.ApplicationName),
		zap.Strings("profile_types", cfg.ProfileTypes),
		zap.Int("mutex_profile_rate", cfg.MutexProfileRate),
		zap.Int("block_profile_rate", cfg.BlockProfileRate),
		zap.Bool("disable_gc_runs", cfg.DisableGCRuns),
	)

	// Set mutex profiling rate if configured
	if cfg.MutexProfileRate > 0 {
		runtime.SetMutexProfileFraction(cfg.MutexProfileRate)
		logger.Info("mutex profiling enabled", zap.Int("rate", cfg.MutexProfileRate))
	}

	// Set block profiling rate if configured
	if cfg.BlockProfileRate > 0 {
		runtime.SetBlockProfileRate(cfg.BlockProfileRate)
		logger.Info("block profiling enabled", zap.Int("rate_ns", cfg.BlockProfileRate))
	}

	// Build Pyroscope configuration
	pyroscopeConfig := pyroscope.Config{
		ApplicationName: cfg.ApplicationName,
		ServerAddress:   cfg.ServerURL,
		Logger:          newZapLogger(logger),
		Tags:            cfg.Tags,
	}

	// Add basic auth if configured
	if cfg.BasicAuthUser != "" {
		pyroscopeConfig.BasicAuthUser = cfg.BasicAuthUser
		pyroscopeConfig.BasicAuthPassword = cfg.BasicAuthPassword
		logger.Info("pyroscope basic auth configured", zap.String("user", cfg.BasicAuthUser))
	}

	// Add hostname tag if not already present
	if pyroscopeConfig.Tags == nil {
		pyroscopeConfig.Tags = make(map[string]string)
	}
	if _, exists := pyroscopeConfig.Tags["hostname"]; !exists {
		if hostname, err := os.Hostname(); err == nil {
			pyroscopeConfig.Tags["hostname"] = hostname
		}
	}

	// Configure profile types if specified
	if len(cfg.ProfileTypes) > 0 {
		profileTypes := make([]pyroscope.ProfileType, 0, len(cfg.ProfileTypes))
		for _, pt := range cfg.ProfileTypes {
			switch pt {
			case "cpu":
				profileTypes = append(profileTypes, pyroscope.ProfileCPU)
			case "alloc_objects":
				profileTypes = append(profileTypes, pyroscope.ProfileAllocObjects)
			case "alloc_space":
				profileTypes = append(profileTypes, pyroscope.ProfileAllocSpace)
			case "inuse_objects":
				profileTypes = append(profileTypes, pyroscope.ProfileInuseObjects)
			case "inuse_space":
				profileTypes = append(profileTypes, pyroscope.ProfileInuseSpace)
			case "goroutines":
				profileTypes = append(profileTypes, pyroscope.ProfileGoroutines)
			case "mutex":
				profileTypes = append(profileTypes, pyroscope.ProfileMutexCount)
			case "block":
				profileTypes = append(profileTypes, pyroscope.ProfileBlockCount)
			}
		}
		pyroscopeConfig.ProfileTypes = profileTypes
	}

	// Set DisableGCRuns for more efficient memory profiling
	if cfg.DisableGCRuns {
		pyroscopeConfig.DisableGCRuns = true
		logger.Info("pyroscope GC runs disabled for more efficient memory profiling")
	}

	// Start Pyroscope profiler
	profiler, err := pyroscope.Start(pyroscopeConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to start pyroscope profiler: %w", err)
	}

	logger.Info("pyroscope profiler started successfully")

	return &Profiler{
		profiler: profiler,
		logger:   logger,
	}, nil
}

// Stop stops the Pyroscope profiler
func (p *Profiler) Stop() error {
	if p.profiler == nil {
		return nil
	}

	p.logger.Info("stopping pyroscope profiler")
	if err := p.profiler.Stop(); err != nil {
		return fmt.Errorf("failed to stop pyroscope profiler: %w", err)
	}

	p.logger.Info("pyroscope profiler stopped")
	return nil
}

// zapLogger adapts zap.Logger to pyroscope.Logger interface
type zapLogger struct {
	logger *zap.Logger
}

func newZapLogger(logger *zap.Logger) *zapLogger {
	return &zapLogger{logger: logger}
}

func (z *zapLogger) Infof(format string, args ...interface{}) {
	z.logger.Sugar().Infof(format, args...)
}

func (z *zapLogger) Debugf(format string, args ...interface{}) {
	z.logger.Sugar().Debugf(format, args...)
}

func (z *zapLogger) Errorf(format string, args ...interface{}) {
	z.logger.Sugar().Errorf(format, args...)
}
