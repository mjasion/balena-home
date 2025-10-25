package buffer

import (
	"sync"
	"testing"
	"time"

	"github.com/mjasion/balena-home/pstryk_metric/scraper"
	"go.uber.org/zap"
)

func TestNew(t *testing.T) {
	buf := New(10, zap.NewNop())
	if buf == nil {
		t.Fatal("Expected buffer to be created, got nil")
	}

	if buf.capacity != 10 {
		t.Errorf("Expected capacity of 10, got %d", buf.capacity)
	}

	if buf.size != 0 {
		t.Errorf("Expected initial size of 0, got %d", buf.size)
	}
}

func TestAdd_Single(t *testing.T) {
	buf := New(10, zap.NewNop())
	result := &scraper.ScrapeResult{
		Timestamp: time.Now(),
		Readings: []scraper.ActivePowerReading{
			{SensorID: 0, Value: 100, Timestamp: time.Now()},
		},
	}

	buf.Add(result)

	if buf.Size() != 1 {
		t.Errorf("Expected size of 1, got %d", buf.Size())
	}
}

func TestAdd_Multiple(t *testing.T) {
	buf := New(10, zap.NewNop())

	for i := 0; i < 5; i++ {
		result := &scraper.ScrapeResult{
			Timestamp: time.Now(),
			Readings: []scraper.ActivePowerReading{
				{SensorID: i, Value: float64(i * 10), Timestamp: time.Now()},
			},
		}
		buf.Add(result)
	}

	if buf.Size() != 5 {
		t.Errorf("Expected size of 5, got %d", buf.Size())
	}
}

func TestAdd_Overflow(t *testing.T) {
	buf := New(3, zap.NewNop())

	for i := 0; i < 5; i++ {
		result := &scraper.ScrapeResult{
			Timestamp: time.Now().Add(time.Duration(i) * time.Second),
			Readings: []scraper.ActivePowerReading{
				{SensorID: i, Value: float64(i * 10), Timestamp: time.Now()},
			},
		}
		buf.Add(result)
	}

	// Should only keep the last 3
	if buf.Size() != 3 {
		t.Errorf("Expected size of 3 after overflow, got %d", buf.Size())
	}

	results := buf.GetAll()
	if len(results) != 3 {
		t.Errorf("Expected 3 results from GetAll, got %d", len(results))
	}

	// Verify oldest entries were dropped (should have IDs 2, 3, 4)
	for i, result := range results {
		expectedID := i + 2
		if len(result.Readings) > 0 && result.Readings[0].SensorID != expectedID {
			t.Errorf("Expected sensor ID %d at position %d, got %d", expectedID, i, result.Readings[0].SensorID)
		}
	}
}

func TestGetAll_Empty(t *testing.T) {
	buf := New(10, zap.NewNop())
	results := buf.GetAll()

	if results != nil {
		t.Errorf("Expected nil for empty buffer, got %d results", len(results))
	}
}

func TestGetAll_Ordering(t *testing.T) {
	buf := New(10, zap.NewNop())

	timestamps := make([]time.Time, 5)
	for i := 0; i < 5; i++ {
		timestamps[i] = time.Now().Add(time.Duration(i) * time.Second)
		result := &scraper.ScrapeResult{
			Timestamp: timestamps[i],
			Readings: []scraper.ActivePowerReading{
				{SensorID: i, Value: float64(i), Timestamp: timestamps[i]},
			},
		}
		buf.Add(result)
	}

	results := buf.GetAll()

	// Verify ordering (oldest to newest)
	for i, result := range results {
		if !result.Timestamp.Equal(timestamps[i]) {
			t.Errorf("Expected timestamp at position %d to be %v, got %v", i, timestamps[i], result.Timestamp)
		}
	}
}

func TestClear(t *testing.T) {
	buf := New(10, zap.NewNop())

	for i := 0; i < 5; i++ {
		result := &scraper.ScrapeResult{
			Timestamp: time.Now(),
			Readings:  []scraper.ActivePowerReading{{SensorID: i, Value: float64(i)}},
		}
		buf.Add(result)
	}

	if buf.Size() != 5 {
		t.Errorf("Expected size of 5 before clear, got %d", buf.Size())
	}

	buf.Clear()

	if buf.Size() != 0 {
		t.Errorf("Expected size of 0 after clear, got %d", buf.Size())
	}

	results := buf.GetAll()
	if results != nil {
		t.Errorf("Expected nil after clear, got %d results", len(results))
	}
}

func TestGetLastScrapeTime(t *testing.T) {
	buf := New(10, zap.NewNop())

	// Empty buffer should return zero time
	lastTime := buf.GetLastScrapeTime()
	if !lastTime.IsZero() {
		t.Error("Expected zero time for empty buffer")
	}

	// Add some results
	times := []time.Time{
		time.Now().Add(-3 * time.Second),
		time.Now().Add(-2 * time.Second),
		time.Now().Add(-1 * time.Second),
	}

	for _, ts := range times {
		result := &scraper.ScrapeResult{
			Timestamp: ts,
			Readings:  []scraper.ActivePowerReading{{SensorID: 0, Value: 100}},
		}
		buf.Add(result)
	}

	lastTime = buf.GetLastScrapeTime()
	if !lastTime.Equal(times[2]) {
		t.Errorf("Expected last scrape time to be %v, got %v", times[2], lastTime)
	}
}

func TestConcurrentAccess(t *testing.T) {
	buf := New(100, zap.NewNop())
	var wg sync.WaitGroup

	// Concurrent writes
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				result := &scraper.ScrapeResult{
					Timestamp: time.Now(),
					Readings: []scraper.ActivePowerReading{
						{SensorID: id, Value: float64(j), Timestamp: time.Now()},
					},
				}
				buf.Add(result)
			}
		}(i)
	}

	// Concurrent reads
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				buf.GetAll()
				buf.Size()
				buf.GetLastScrapeTime()
			}
		}()
	}

	wg.Wait()

	finalSize := buf.Size()
	if finalSize != 100 {
		t.Errorf("Expected final size of 100, got %d", finalSize)
	}
}

func TestAddAfterClear(t *testing.T) {
	buf := New(10, zap.NewNop())

	// Add, clear, add again
	buf.Add(&scraper.ScrapeResult{
		Timestamp: time.Now(),
		Readings:  []scraper.ActivePowerReading{{SensorID: 0, Value: 100}},
	})

	buf.Clear()

	buf.Add(&scraper.ScrapeResult{
		Timestamp: time.Now(),
		Readings:  []scraper.ActivePowerReading{{SensorID: 1, Value: 200}},
	})

	if buf.Size() != 1 {
		t.Errorf("Expected size of 1 after clear and add, got %d", buf.Size())
	}

	results := buf.GetAll()
	if len(results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results))
	}

	if results[0].Readings[0].SensorID != 1 {
		t.Errorf("Expected sensor ID 1, got %d", results[0].Readings[0].SensorID)
	}
}
