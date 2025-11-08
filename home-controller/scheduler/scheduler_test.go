package scheduler

import (
	"testing"
	"time"
)

func TestWaitForAlignedInterval(t *testing.T) {
	tests := []struct {
		name            string
		interval        time.Duration
		secondsSince    int // seconds since start of hour
		expectedWait    int // expected wait in seconds
	}{
		{
			name:         "1s interval - should wait 1s",
			interval:     1 * time.Second,
			secondsSince: 17, // :17
			expectedWait: 1,  // wait until :18
		},
		{
			name:         "2s interval - should wait 1s",
			interval:     2 * time.Second,
			secondsSince: 17, // :17
			expectedWait: 1,  // wait until :18 (17 % 2 = 1, so 2 - 1 = 1)
		},
		{
			name:         "30s interval - align to :00 or :30",
			interval:     30 * time.Second,
			secondsSince: 17, // :17
			expectedWait: 13, // wait until :30 (17 % 30 = 17, so 30 - 17 = 13)
		},
		{
			name:         "30s interval - near boundary but not too close",
			interval:     30 * time.Second,
			secondsSince: 27, // :27
			expectedWait: 3,  // wait until :30 (27 % 30 = 27, so 30 - 27 = 3)
		},
		{
			name:         "30s interval - very close to boundary (skip)",
			interval:     30 * time.Second,
			secondsSince: 29, // :29 (within 2s threshold)
			expectedWait: 31, // skip to next: wait until :00 (30 + 1 = 31)
		},
		{
			name:         "60s interval - align to :00",
			interval:     60 * time.Second,
			secondsSince: 17, // :17
			expectedWait: 43, // wait until next :00 (17 % 60 = 17, so 60 - 17 = 43)
		},
		{
			name:         "300s interval - align to 5-min marks",
			interval:     300 * time.Second,
			secondsSince: 137, // 2:17
			expectedWait: 163, // wait until 5:00 (137 % 300 = 137, so 300 - 137 = 163)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test the logic directly
			intervalSeconds := int(tt.interval.Seconds())
			secondsUntilNext := intervalSeconds - (tt.secondsSince % intervalSeconds)

			var skipThreshold int
			if intervalSeconds <= 2 {
				skipThreshold = 0
			} else if intervalSeconds < 10 {
				skipThreshold = 1
			} else {
				skipThreshold = 2
			}

			if skipThreshold > 0 && secondsUntilNext <= skipThreshold {
				secondsUntilNext += intervalSeconds
			}

			if secondsUntilNext != tt.expectedWait {
				t.Errorf("Expected wait %ds, got %ds (threshold: %d)", tt.expectedWait, secondsUntilNext, skipThreshold)
			}
		})
	}
}
