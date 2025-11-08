package scheduler

import (
	"time"

	"go.uber.org/zap"
)

// WaitForAlignedInterval calculates the duration until the next interval that aligns with clock time
// For example, with a 5-minute (300s) interval, it will align to :00, :05, :10, :15, etc.
// Returns the duration to wait before the next aligned interval
func WaitForAlignedInterval(interval time.Duration, logger *zap.Logger) time.Duration {
	now := time.Now()
	intervalSeconds := int(interval.Seconds())

	// Calculate seconds since the start of current hour
	secondsSinceHour := now.Minute()*60 + now.Second()

	// Calculate seconds until next aligned interval
	secondsUntilNext := intervalSeconds - (secondsSinceHour % intervalSeconds)

	// If we're very close to the next interval (within 2 seconds), wait for the one after
	if secondsUntilNext < 2 {
		secondsUntilNext += intervalSeconds
	}

	waitDuration := time.Duration(secondsUntilNext) * time.Second

	if logger != nil {
		logger.Info("waiting for next aligned interval",
			zap.Duration("interval", interval),
			zap.Duration("wait_duration", waitDuration),
			zap.Time("next_run", now.Add(waitDuration)),
		)
	}

	return waitDuration
}
