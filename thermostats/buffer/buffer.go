package buffer

import (
	"sync"

	"go.uber.org/zap"
)

// SensorReading represents a single temperature sensor reading
// This is duplicated here to avoid circular imports
type SensorReading struct {
	Timestamp          interface{} // time.Time
	MAC                string
	SensorName         string // Friendly name from config
	SensorID           int    // Numeric ID from config
	TemperatureCelsius float64
	HumidityPercent    int
	BatteryPercent     int
	BatteryVoltageMV   int
	FrameCounter       int
	RSSI               int16
}

// RingBuffer is a thread-safe circular buffer for sensor readings
type RingBuffer struct {
	data     []*SensorReading
	capacity int
	size     int
	head     int
	mu       sync.RWMutex
	logger   *zap.Logger
}

// New creates a new ring buffer with the specified capacity
func New(capacity int, logger *zap.Logger) *RingBuffer {
	return &RingBuffer{
		data:     make([]*SensorReading, capacity),
		capacity: capacity,
		size:     0,
		head:     0,
		logger:   logger,
	}
}

// Add adds a new sensor reading to the buffer
// If the buffer is full, it overwrites the oldest entry
func (rb *RingBuffer) Add(reading *SensorReading) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	// Check if we're about to overwrite data
	if rb.size == rb.capacity {
		rb.logger.Warn("ring buffer full, overwriting oldest data",
			zap.Int("capacity", rb.capacity),
			zap.String("overwritten_mac", rb.data[rb.head].MAC),
		)
	}

	// Add the reading
	rb.data[rb.head] = reading
	rb.head = (rb.head + 1) % rb.capacity

	// Update size
	if rb.size < rb.capacity {
		rb.size++
	}
}

// GetAll returns all buffered readings
// The returned slice is a copy, so it's safe to use after the call
func (rb *RingBuffer) GetAll() []*SensorReading {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	if rb.size == 0 {
		return nil
	}

	result := make([]*SensorReading, rb.size)

	if rb.size < rb.capacity {
		// Buffer is not full yet, readings are from 0 to head-1
		copy(result, rb.data[:rb.size])
	} else {
		// Buffer is full, readings are from head to end, then 0 to head-1
		n := copy(result, rb.data[rb.head:])
		copy(result[n:], rb.data[:rb.head])
	}

	return result
}

// Size returns the current number of readings in the buffer
func (rb *RingBuffer) Size() int {
	rb.mu.RLock()
	defer rb.mu.RUnlock()
	return rb.size
}

// Clear removes all readings from the buffer
func (rb *RingBuffer) Clear() {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	rb.size = 0
	rb.head = 0
	rb.data = make([]*SensorReading, rb.capacity)
}
