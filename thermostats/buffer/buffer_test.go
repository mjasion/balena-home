package buffer

import (
	"sync"
	"testing"

	"go.uber.org/zap"
)

func TestRingBuffer_AddAndGet(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	rb := New(5, logger)

	// Add some readings
	for i := 0; i < 3; i++ {
		reading := &SensorReading{
			MAC:                "A4:C1:38:00:00:00",
			TemperatureCelsius: float64(20 + i),
		}
		rb.Add(reading)
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
		if reading.TemperatureCelsius != expectedTemp {
			t.Errorf("reading %d: expected temp %.1f, got %.1f", i, expectedTemp, reading.TemperatureCelsius)
		}
	}
}

func TestRingBuffer_Overflow(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	rb := New(3, logger)

	// Add more readings than capacity
	for i := 0; i < 5; i++ {
		reading := &SensorReading{
			MAC:                "A4:C1:38:00:00:00",
			TemperatureCelsius: float64(20 + i),
		}
		rb.Add(reading)
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
		if reading.TemperatureCelsius != expectedTemps[i] {
			t.Errorf("reading %d: expected temp %.1f, got %.1f", i, expectedTemps[i], reading.TemperatureCelsius)
		}
	}
}

func TestRingBuffer_Clear(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	rb := New(5, logger)

	// Add some readings
	for i := 0; i < 3; i++ {
		reading := &SensorReading{
			MAC:                "A4:C1:38:00:00:00",
			TemperatureCelsius: float64(20 + i),
		}
		rb.Add(reading)
	}

	// Clear buffer
	rb.Clear()

	// Check size
	if rb.Size() != 0 {
		t.Errorf("expected size 0 after clear, got %d", rb.Size())
	}

	// Get all readings (should be empty)
	readings := rb.GetAll()
	if len(readings) != 0 {
		t.Errorf("expected 0 readings after clear, got %d", len(readings))
	}
}

func TestRingBuffer_Concurrent(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	rb := New(100, logger)

	var wg sync.WaitGroup

	// Spawn multiple goroutines writing
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				reading := &SensorReading{
					MAC:                "A4:C1:38:00:00:00",
					TemperatureCelsius: float64(id*10 + j),
				}
				rb.Add(reading)
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
