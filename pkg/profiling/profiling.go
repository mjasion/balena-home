package profiling

import (
	"fmt"
	"runtime"

	"github.com/grafana/pyroscope-go"
	"go.uber.org/zap"

	"github.com/mjasion/balena-home/pkg/config"
)

// Profiler wraps the Pyroscope profiler
type Profiler struct {
	profiler *pyroscope.Profiler
	logger   *zap.Logger
}

// Start initializes and starts the Pyroscope profiler in push mode
func Start(cfg *config.ProfilingConfig, logger *zap.Logger) (*Profiler, error) {
	if !cfg.Enabled {
		logger.Info("profiling is disabled")
		return nil, nil
	}

	logger.Info("initializing Pyroscope profiler")

	// Build profile types list
	var profileTypes []pyroscope.ProfileType
	if cfg.CPUProfile {
		profileTypes = append(profileTypes, pyroscope.ProfileCPU)
	}
	if cfg.AllocObjectsProfile {
		profileTypes = append(profileTypes, pyroscope.ProfileAllocObjects)
	}
	if cfg.AllocSpaceProfile {
		profileTypes = append(profileTypes, pyroscope.ProfileAllocSpace)
	}
	if cfg.InuseObjectsProfile {
		profileTypes = append(profileTypes, pyroscope.ProfileInuseObjects)
	}
	if cfg.InuseSpaceProfile {
		profileTypes = append(profileTypes, pyroscope.ProfileInuseSpace)
	}
	if cfg.GoroutineProfile {
		profileTypes = append(profileTypes, pyroscope.ProfileGoroutines)
	}
	if cfg.MutexProfile {
		profileTypes = append(profileTypes, pyroscope.ProfileMutexCount, pyroscope.ProfileMutexDuration)
		// Enable mutex profiling
		runtime.SetMutexProfileFraction(cfg.MutexProfileRate)
	}
	if cfg.BlockProfile {
		profileTypes = append(profileTypes, pyroscope.ProfileBlockCount, pyroscope.ProfileBlockDuration)
		// Enable block profiling
		runtime.SetBlockProfileRate(cfg.BlockProfileRate)
	}

	// Build tags from config
	tags := make(map[string]string)
	for k, v := range cfg.Tags {
		tags[k] = v
	}

	// Configure Pyroscope
	pyroConfig := pyroscope.Config{
		ApplicationName: cfg.ApplicationName,
		ServerAddress:   cfg.ServerAddress,
		Logger:          nil, // Disable Pyroscope's internal logging
		Tags:            tags,
		ProfileTypes:    profileTypes,
		DisableGCRuns:   cfg.DisableGCRuns,
	}

	// Add authentication if provided
	if cfg.BasicAuthUser != "" && cfg.BasicAuthPassword != "" {
		pyroConfig.BasicAuthUser = cfg.BasicAuthUser
		pyroConfig.BasicAuthPassword = cfg.BasicAuthPassword
	}

	// Add tenant ID if provided
	if cfg.TenantID != "" {
		pyroConfig.TenantID = cfg.TenantID
	}

	// Start the profiler
	profiler, err := pyroscope.Start(pyroConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to start Pyroscope profiler: %w", err)
	}

	logger.Info("Pyroscope profiler started",
		zap.String("server_address", cfg.ServerAddress),
		zap.String("application_name", cfg.ApplicationName),
		zap.Int("profile_types_count", len(profileTypes)),
		zap.Any("tags", tags),
	)

	return &Profiler{
		profiler: profiler,
		logger:   logger,
	}, nil
}

// Stop gracefully stops the profiler
func (p *Profiler) Stop() error {
	if p == nil || p.profiler == nil {
		return nil
	}

	p.logger.Info("stopping Pyroscope profiler")

	if err := p.profiler.Stop(); err != nil {
		p.logger.Error("failed to stop profiler", zap.Error(err))
		return fmt.Errorf("profiler stop: %w", err)
	}

	p.logger.Info("Pyroscope profiler stopped")
	return nil
}
