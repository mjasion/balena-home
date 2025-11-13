package scheduler

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/go-co-op/gocron-ui/server"
	"github.com/go-co-op/gocron/v2"
	"go.uber.org/zap"
)

// Manager wraps gocron scheduler and provides UI server
type Manager struct {
	scheduler gocron.Scheduler
	logger    *zap.Logger
	uiServer  *http.Server
	uiEnabled bool
}

// Config holds scheduler configuration
type Config struct {
	UIEnabled bool
	UIPort    int
	UIHost    string
}

// New creates a new scheduler manager
func New(cfg *Config, logger *zap.Logger) (*Manager, error) {
	// Create scheduler with location set to local timezone
	s, err := gocron.NewScheduler(
		gocron.WithLocation(time.Local),
		gocron.WithLogger(&gocronLogger{logger: logger}),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create scheduler: %w", err)
	}

	m := &Manager{
		scheduler: s,
		logger:    logger,
		uiEnabled: cfg.UIEnabled,
	}

	// Setup UI server if enabled
	if cfg.UIEnabled {
		uiServer := server.NewServer(s, cfg.UIPort)
		addr := fmt.Sprintf("%s:%d", cfg.UIHost, cfg.UIPort)
		m.uiServer = &http.Server{
			Addr:    addr,
			Handler: uiServer.Router,
		}
		logger.Info("gocron UI configured", zap.String("address", addr))
	}

	return m, nil
}

// AddJobWithRandomDuration adds a new job with a random duration between min and max
func (m *Manager) AddJobWithRandomDuration(name string, minDuration, maxDuration time.Duration, task func(context.Context)) error {
	_, err := m.scheduler.NewJob(
		gocron.DurationRandomJob(minDuration, maxDuration),
		gocron.NewTask(task, context.Background()),
		gocron.WithName(name),
		gocron.WithStartAt(gocron.WithStartImmediately()),
	)
	if err != nil {
		return fmt.Errorf("failed to add random duration job %s: %w", name, err)
	}

	m.logger.Info("scheduled job with random duration",
		zap.String("name", name),
		zap.Duration("min_duration", minDuration),
		zap.Duration("max_duration", maxDuration))
	return nil
}

// AddCronJobWithSeconds adds a new job to the scheduler with cron-based scheduling including seconds
func (m *Manager) AddCronJobWithSeconds(name string, cronExpr string, task func(context.Context)) error {
	_, err := m.scheduler.NewJob(
		gocron.CronJob(cronExpr, true), // true = using WithSeconds (6-field cron with seconds)
		gocron.NewTask(task, context.Background()),
		gocron.WithName(name),
		gocron.WithStartAt(gocron.WithStartImmediately()),
	)
	if err != nil {
		return fmt.Errorf("failed to add cron job with seconds %s with expression %s: %w", name, cronExpr, err)
	}

	m.logger.Info("scheduled job with cron expression (with seconds)",
		zap.String("name", name),
		zap.String("cron", cronExpr))
	return nil
}

// Start starts the scheduler and UI server
func (m *Manager) Start() error {
	// Start the scheduler
	m.scheduler.Start()
	m.logger.Info("scheduler started")

	// Start UI server if enabled
	if m.uiEnabled && m.uiServer != nil {
		go func() {
			m.logger.Info("starting gocron UI server", zap.String("address", m.uiServer.Addr))
			if err := m.uiServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				m.logger.Error("gocron UI server error", zap.Error(err))
			}
		}()
	}

	return nil
}

// Shutdown gracefully shuts down the scheduler and UI server
func (m *Manager) Shutdown(ctx context.Context) error {
	m.logger.Info("shutting down scheduler")

	// Shutdown scheduler
	if err := m.scheduler.Shutdown(); err != nil {
		m.logger.Error("error shutting down scheduler", zap.Error(err))
		return err
	}

	// Shutdown UI server if running
	if m.uiEnabled && m.uiServer != nil {
		m.logger.Info("shutting down gocron UI server")
		if err := m.uiServer.Shutdown(ctx); err != nil {
			m.logger.Error("error shutting down UI server", zap.Error(err))
			return err
		}
	}

	return nil
}

// gocronLogger adapts zap.Logger to gocron.Logger interface
type gocronLogger struct {
	logger *zap.Logger
}

func (l *gocronLogger) Debug(msg string, args ...any) {
	l.logger.Debug(msg, argsToFields(args)...)
}

func (l *gocronLogger) Info(msg string, args ...any) {
	l.logger.Info(msg, argsToFields(args)...)
}

func (l *gocronLogger) Warn(msg string, args ...any) {
	l.logger.Warn(msg, argsToFields(args)...)
}

func (l *gocronLogger) Error(msg string, args ...any) {
	l.logger.Error(msg, argsToFields(args)...)
}

// argsToFields converts variadic args to zap fields
func argsToFields(args []any) []zap.Field {
	if len(args) == 0 {
		return nil
	}

	fields := make([]zap.Field, 0, len(args)/2)
	for i := 0; i < len(args)-1; i += 2 {
		key, ok := args[i].(string)
		if !ok {
			continue
		}
		fields = append(fields, zap.Any(key, args[i+1]))
	}
	return fields
}
