package buffer

import (
	"context"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestRingBuffer_AddAndGet(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	rb := New(5, logger)
	ctx := context.Background()

	// Add some readings
	for i := 0; i < 3; i++ {
		reading := &Reading{
			Type: ReadingTypeBLE,
			BLE: &SensorReading{
				MAC:                "A4:C1:38:00:00:00",
				TemperatureCelsius: float64(20 + i),
			},
		}
		rb.Add(ctx, reading)
	}

	// Check size
	if rb.Size() != 3 {
		t.Errorf("expected size 3, got %d", rb.Size())
	}

	// Get all readings
	readings := rb.GetAll()
	if len(readings) != 3 {
		t.Errorf("expected 3 readings, got %d", len(readings))
	}

	// Verify readings are in correct order
	for i, reading := range readings {
		expectedTemp := float64(20 + i)
		if reading.BLE.TemperatureCelsius != expectedTemp {
			t.Errorf("reading %d: expected temp %.1f, got %.1f", i, expectedTemp, reading.BLE.TemperatureCelsius)
		}
	}
}

func TestRingBuffer_Overflow(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	rb := New(3, logger)
	ctx := context.Background()

	// Add more readings than capacity
	for i := 0; i < 5; i++ {
		reading := &Reading{
			Type: ReadingTypeBLE,
			BLE: &SensorReading{
				MAC:                "A4:C1:38:00:00:00",
				TemperatureCelsius: float64(20 + i),
			},
		}
		rb.Add(ctx, reading)
	}

	// Check size (should be capped at capacity)
	if rb.Size() != 3 {
		t.Errorf("expected size 3, got %d", rb.Size())
	}

	// Get all readings (should have the last 3)
	readings := rb.GetAll()
	if len(readings) != 3 {
		t.Errorf("expected 3 readings, got %d", len(readings))
	}

	// Verify we kept the newest readings (temp 22, 23, 24)
	expectedTemps := []float64{22, 23, 24}
	for i, reading := range readings {
		if reading.BLE.TemperatureCelsius != expectedTemps[i] {
			t.Errorf("reading %d: expected temp %.1f, got %.1f", i, expectedTemps[i], reading.BLE.TemperatureCelsius)
		}
	}
}

func TestRingBuffer_GetAllAndClear(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	rb := New(5, logger)
	ctx := context.Background()

	// Add some readings
	for i := 0; i < 3; i++ {
		reading := &Reading{
			Type: ReadingTypeBLE,
			BLE: &SensorReading{
				MAC:                "A4:C1:38:00:00:00",
				TemperatureCelsius: float64(20 + i),
			},
		}
		rb.Add(ctx, reading)
	}

	// Check size before
	if rb.Size() != 3 {
		t.Errorf("expected size 3 before GetAllAndClear, got %d", rb.Size())
	}

	// Get all and clear atomically
	readings := rb.GetAllAndClear(ctx)

	// Check returned readings
	if len(readings) != 3 {
		t.Errorf("expected 3 readings from GetAllAndClear, got %d", len(readings))
	}

	// Verify readings are correct
	for i, reading := range readings {
		expectedTemp := float64(20 + i)
		if reading.BLE.TemperatureCelsius != expectedTemp {
			t.Errorf("reading %d: expected temp %.1f, got %.1f", i, expectedTemp, reading.BLE.TemperatureCelsius)
		}
	}

	// Check size after - should be 0
	if rb.Size() != 0 {
		t.Errorf("expected size 0 after GetAllAndClear, got %d", rb.Size())
	}

	// Verify buffer is truly empty
	readingsAfter := rb.GetAll()
	if len(readingsAfter) != 0 {
		t.Errorf("expected 0 readings after GetAllAndClear, got %d", len(readingsAfter))
	}
}

func TestRingBuffer_Concurrent(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	rb := New(100, logger)
	ctx := context.Background()

	var wg sync.WaitGroup

	// Spawn multiple goroutines writing
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				reading := &Reading{
					Type: ReadingTypeBLE,
					BLE: &SensorReading{
						MAC:                "A4:C1:38:00:00:00",
						TemperatureCelsius: float64(id*10 + j),
					},
				}
				rb.Add(ctx, reading)
			}
		}(i)
	}

	// Spawn multiple goroutines reading
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				_ = rb.GetAll()
				_ = rb.Size()
			}
		}()
	}

	wg.Wait()

	// Final size should be 100 (capacity)
	if rb.Size() != 100 {
		t.Errorf("expected size 100 after concurrent operations, got %d", rb.Size())
	}
}

func TestRingBuffer_GetReadingsByTimeWindow(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	rb := New(10, logger)
	ctx := context.Background()

	now := time.Now()

	// Add readings with different timestamps
	testData := []struct {
		offsetSeconds int
		temp          float64
	}{
		{-90, 20.0}, // 90 seconds ago
		{-60, 21.0}, // 60 seconds ago
		{-45, 22.0}, // 45 seconds ago
		{-30, 23.0}, // 30 seconds ago
		{-15, 24.0}, // 15 seconds ago
		{-5, 25.0},  // 5 seconds ago
		{-1, 26.0},  // 1 second ago
	}

	for _, td := range testData {
		reading := &Reading{
			Type: ReadingTypeBLE,
			BLE: &SensorReading{
				Timestamp:          now.Add(time.Duration(td.offsetSeconds) * time.Second),
				MAC:                "A4:C1:38:00:00:00",
				TemperatureCelsius: td.temp,
			},
		}
		rb.Add(ctx, reading)
	}

	// Test: Get readings from last 60 seconds
	cutoff := now.Add(-60 * time.Second)
	readings := rb.GetReadingsByTimeWindow(ctx, cutoff, now)

	// Should have 6 readings (all except the one at -90s)
	if len(readings) != 6 {
		t.Errorf("expected 6 readings in 60-second window, got %d", len(readings))
	}

	// Verify first reading is at -60s (21.0°C)
	if len(readings) > 0 && readings[0].BLE.TemperatureCelsius != 21.0 {
		t.Errorf("expected first reading temp 21.0, got %.1f", readings[0].BLE.TemperatureCelsius)
	}

	// Verify last reading is at -1s (26.0°C)
	if len(readings) > 0 && readings[len(readings)-1].BLE.TemperatureCelsius != 26.0 {
		t.Errorf("expected last reading temp 26.0, got %.1f", readings[len(readings)-1].BLE.TemperatureCelsius)
	}
}

func TestRingBuffer_GetReadingsByTimeWindow_EmptyBuffer(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	rb := New(10, logger)
	ctx := context.Background()

	now := time.Now()
	cutoff := now.Add(-60 * time.Second)

	readings := rb.GetReadingsByTimeWindow(ctx, cutoff, now)

	// Should return empty slice, not nil
	if readings == nil {
		t.Error("expected empty slice, got nil")
	}

	if len(readings) != 0 {
		t.Errorf("expected 0 readings from empty buffer, got %d", len(readings))
	}
}

func TestRingBuffer_GetReadingsByTimeWindow_NoMatches(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	rb := New(10, logger)
	ctx := context.Background()

	now := time.Now()

	// Add readings from 120-90 seconds ago (outside our query window)
	for i := 0; i < 5; i++ {
		reading := &Reading{
			Type: ReadingTypeBLE,
			BLE: &SensorReading{
				Timestamp:          now.Add(time.Duration(-120+i*5) * time.Second),
				MAC:                "A4:C1:38:00:00:00",
				TemperatureCelsius: float64(20 + i),
			},
		}
		rb.Add(ctx, reading)
	}

	// Query for last 60 seconds (should find nothing)
	cutoff := now.Add(-60 * time.Second)
	readings := rb.GetReadingsByTimeWindow(ctx, cutoff, now)

	if len(readings) != 0 {
		t.Errorf("expected 0 readings (no matches), got %d", len(readings))
	}
}

func TestRingBuffer_GetReadingsByTimeWindow_AllMatches(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	rb := New(10, logger)
	ctx := context.Background()

	now := time.Now()

	// Add 5 readings all within the last 30 seconds
	for i := 0; i < 5; i++ {
		reading := &Reading{
			Type: ReadingTypeBLE,
			BLE: &SensorReading{
				Timestamp:          now.Add(time.Duration(-30+i*5) * time.Second),
				MAC:                "A4:C1:38:00:00:00",
				TemperatureCelsius: float64(20 + i),
			},
		}
		rb.Add(ctx, reading)
	}

	// Query for last 60 seconds (should match all)
	cutoff := now.Add(-60 * time.Second)
	readings := rb.GetReadingsByTimeWindow(ctx, cutoff, now)

	if len(readings) != 5 {
		t.Errorf("expected 5 readings (all matches), got %d", len(readings))
	}
}

func TestRingBuffer_GetReadingsByTimeWindow_ConcurrentAccess(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	rb := New(100, logger)
	ctx := context.Background()

	now := time.Now()
	var wg sync.WaitGroup

	// Spawn goroutines writing
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				reading := &Reading{
					Type: ReadingTypeBLE,
					BLE: &SensorReading{
						Timestamp:          now.Add(time.Duration(-j) * time.Second),
						MAC:                "A4:C1:38:00:00:00",
						TemperatureCelsius: float64(id*10 + j),
					},
				}
				rb.Add(ctx, reading)
			}
		}(i)
	}

	// Spawn goroutines reading with time window
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cutoff := now.Add(-60 * time.Second)
			for j := 0; j < 10; j++ {
				_ = rb.GetReadingsByTimeWindow(ctx, cutoff, now)
			}
		}()
	}

	// Spawn goroutines using GetAll (original method)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				_ = rb.GetAll()
			}
		}()
	}

	wg.Wait()

	// Should complete without deadlocks
	if rb.Size() != 100 {
		t.Errorf("expected size 100 after concurrent operations, got %d", rb.Size())
	}
}

func TestRingBuffer_GetReadingsByTimeWindow_WithGetAllAndClear(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	rb := New(10, logger)
	ctx := context.Background()

	now := time.Now()

	// Add readings
	for i := 0; i < 5; i++ {
		reading := &Reading{
			Type: ReadingTypeBLE,
			BLE: &SensorReading{
				Timestamp:          now.Add(time.Duration(-i*10) * time.Second),
				MAC:                "A4:C1:38:00:00:00",
				TemperatureCelsius: float64(20 + i),
			},
		}
		rb.Add(ctx, reading)
	}

	cutoff := now.Add(-60 * time.Second)

	// Get readings with time window (non-destructive)
	readingsBefore := rb.GetReadingsByTimeWindow(ctx, cutoff, now)
	if len(readingsBefore) != 5 {
		t.Errorf("expected 5 readings before GetAllAndClear, got %d", len(readingsBefore))
	}

	// Buffer should still have data
	if rb.Size() != 5 {
		t.Errorf("expected size 5 after GetReadingsByTimeWindow, got %d", rb.Size())
	}

	// Now clear the buffer
	readingsCleared := rb.GetAllAndClear(ctx)
	if len(readingsCleared) != 5 {
		t.Errorf("expected 5 readings from GetAllAndClear, got %d", len(readingsCleared))
	}

	// Buffer should be empty
	if rb.Size() != 0 {
		t.Errorf("expected size 0 after GetAllAndClear, got %d", rb.Size())
	}

	// GetReadingsByTimeWindow on empty buffer should return empty slice
	readingsAfter := rb.GetReadingsByTimeWindow(ctx, cutoff, now)
	if len(readingsAfter) != 0 {
		t.Errorf("expected 0 readings after GetAllAndClear, got %d", len(readingsAfter))
	}
}

func TestRingBuffer_GetReadingsByTimeWindow_InvalidTimeParams(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	rb := New(10, logger)
	ctx := context.Background()

	now := time.Now()

	// Add a reading
	reading := &Reading{
		Type: ReadingTypeBLE,
		BLE: &SensorReading{
			Timestamp:          now,
			MAC:                "A4:C1:38:00:00:00",
			TemperatureCelsius: 25.0,
		},
	}
	rb.Add(ctx, reading)

	// Call with invalid parameters (not time.Time)
	readings := rb.GetReadingsByTimeWindow(ctx, "invalid", "params")

	// Should return empty slice and log error
	if len(readings) != 0 {
		t.Errorf("expected 0 readings with invalid params, got %d", len(readings))
	}
}

func TestRingBuffer_GetReadingsByTimeWindow_MultipleReadingTypes(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	rb := New(10, logger)
	ctx := context.Background()

	now := time.Now()

	// Add BLE reading
	bleReading := &Reading{
		Type: ReadingTypeBLE,
		BLE: &SensorReading{
			Timestamp:          now.Add(-30 * time.Second),
			MAC:                "A4:C1:38:00:00:00",
			TemperatureCelsius: 25.0,
		},
	}
	rb.Add(ctx, bleReading)

	// Add Netatmo reading
	netatmoReading := &Reading{
		Type: ReadingTypeNetatmo,
		Thermostat: &ThermostatReading{
			Timestamp:           now.Add(-20 * time.Second),
			RoomName:            "Living Room",
			MeasuredTemperature: 24.0,
		},
	}
	rb.Add(ctx, netatmoReading)

	// Add Power reading
	powerReading := &Reading{
		Type: ReadingTypePower,
		Power: &PowerReading{
			Timestamp:  now.Add(-10 * time.Second),
			SensorType: "active_power",
			Value:      1500.0,
		},
	}
	rb.Add(ctx, powerReading)

	// Query for last 60 seconds (should match all 3)
	cutoff := now.Add(-60 * time.Second)
	readings := rb.GetReadingsByTimeWindow(ctx, cutoff, now)

	if len(readings) != 3 {
		t.Errorf("expected 3 readings (all types), got %d", len(readings))
	}

	// Verify we have one of each type
	var bleCount, netatmoCount, powerCount int
	for _, r := range readings {
		switch r.Type {
		case ReadingTypeBLE:
			bleCount++
		case ReadingTypeNetatmo:
			netatmoCount++
		case ReadingTypePower:
			powerCount++
		}
	}

	if bleCount != 1 || netatmoCount != 1 || powerCount != 1 {
		t.Errorf("expected 1 of each type, got BLE=%d, Netatmo=%d, Power=%d", bleCount, netatmoCount, powerCount)
	}
}
