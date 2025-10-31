package config

import "fmt"

// ProfilingConfig contains Pyroscope profiling configuration
type ProfilingConfig struct {
	Enabled         bool              `yaml:"enabled" env:"PYROSCOPE_ENABLED" env-default:"false"`
	ApplicationName string            `yaml:"applicationName" env:"PYROSCOPE_APPLICATION_NAME"`
	ServerAddress   string            `yaml:"serverAddress" env:"PYROSCOPE_SERVER_ADDRESS"`
	BasicAuthUser   string            `yaml:"basicAuthUser" env:"PYROSCOPE_BASIC_AUTH_USER"`
	BasicAuthPassword string          `yaml:"basicAuthPassword" env:"PYROSCOPE_BASIC_AUTH_PASSWORD"`
	TenantID        string            `yaml:"tenantID" env:"PYROSCOPE_TENANT_ID"`
	Tags            map[string]string `yaml:"tags"`

	// Profile types to enable
	CPUProfile           bool `yaml:"cpuProfile" env:"PYROSCOPE_CPU_PROFILE" env-default:"true"`
	AllocObjectsProfile  bool `yaml:"allocObjectsProfile" env:"PYROSCOPE_ALLOC_OBJECTS_PROFILE" env-default:"true"`
	AllocSpaceProfile    bool `yaml:"allocSpaceProfile" env:"PYROSCOPE_ALLOC_SPACE_PROFILE" env-default:"true"`
	InuseObjectsProfile  bool `yaml:"inuseObjectsProfile" env:"PYROSCOPE_INUSE_OBJECTS_PROFILE" env-default:"true"`
	InuseSpaceProfile    bool `yaml:"inuseSpaceProfile" env:"PYROSCOPE_INUSE_SPACE_PROFILE" env-default:"true"`
	GoroutineProfile     bool `yaml:"goroutineProfile" env:"PYROSCOPE_GOROUTINE_PROFILE" env-default:"false"`
	MutexProfile         bool `yaml:"mutexProfile" env:"PYROSCOPE_MUTEX_PROFILE" env-default:"false"`
	BlockProfile         bool `yaml:"blockProfile" env:"PYROSCOPE_BLOCK_PROFILE" env-default:"false"`

	// Profile rates
	MutexProfileRate int `yaml:"mutexProfileRate" env:"PYROSCOPE_MUTEX_PROFILE_RATE" env-default:"5"`
	BlockProfileRate int `yaml:"blockProfileRate" env:"PYROSCOPE_BLOCK_PROFILE_RATE" env-default:"5"`

	// Additional options
	DisableGCRuns bool `yaml:"disableGCRuns" env:"PYROSCOPE_DISABLE_GC_RUNS" env-default:"false"`
}

// ValidateProfiling validates profiling configuration if enabled
func ValidateProfiling(cfg *ProfilingConfig) error {
	if !cfg.Enabled {
		return nil
	}

	// Validate application name
	if cfg.ApplicationName == "" {
		return fmt.Errorf("profiling application name is required when profiling is enabled")
	}

	// Validate server address
	if cfg.ServerAddress == "" {
		return fmt.Errorf("profiling server address is required when profiling is enabled")
	}

	// Validate profile rates
	if cfg.MutexProfile && cfg.MutexProfileRate < 0 {
		return fmt.Errorf("profiling mutex profile rate must be >= 0")
	}

	if cfg.BlockProfile && cfg.BlockProfileRate < 0 {
		return fmt.Errorf("profiling block profile rate must be >= 0")
	}

	// At least one profile type should be enabled
	if !cfg.CPUProfile && !cfg.AllocObjectsProfile && !cfg.AllocSpaceProfile &&
		!cfg.InuseObjectsProfile && !cfg.InuseSpaceProfile && !cfg.GoroutineProfile &&
		!cfg.MutexProfile && !cfg.BlockProfile {
		return fmt.Errorf("at least one profile type must be enabled")
	}

	return nil
}
