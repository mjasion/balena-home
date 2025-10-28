package buffer

import (
	"sync"

	"go.uber.org/zap"
)

// ReadingType identifies the type of reading
type ReadingType string

const (
	ReadingTypeBLE     ReadingType = "ble"
	ReadingTypeNetatmo ReadingType = "netatmo"
)

// SensorReading represents a single temperature sensor reading from BLE
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

// ThermostatReading represents a thermostat reading from Netatmo
type ThermostatReading struct {
	Timestamp           interface{} // time.Time
	HomeID              string
	HomeName            string
	RoomID              string
	RoomName            string
	MeasuredTemperature float64
	SetpointTemperature float64
	SetpointMode        string
	HeatingPowerRequest int
	OpenWindow          bool
	Reachable           bool
}

// Reading is a union type that can hold either BLE sensor or Netatmo thermostat readings
type Reading struct {
	Type       ReadingType
	BLE        *SensorReading
	Thermostat *ThermostatReading
}

// RingBuffer is a thread-safe circular buffer for sensor readings
type RingBuffer struct {
	data     []*Reading
	capacity int
	size     int
	head     int
	mu       sync.RWMutex
	logger   *zap.Logger
}

// New creates a new ring buffer with the specified capacity
func New(capacity int, logger *zap.Logger) *RingBuffer {
	return &RingBuffer{
		data:     make([]*Reading, capacity),
		capacity: capacity,
		size:     0,
		head:     0,
		logger:   logger,
	}
}

// Add adds a new reading to the buffer
// If the buffer is full, it overwrites the oldest entry
func (rb *RingBuffer) Add(reading *Reading) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	// Check if we're about to overwrite data
	if rb.size == rb.capacity {
		overwrittenType := rb.data[rb.head].Type
		rb.logger.Warn("ring buffer full, overwriting oldest data",
			zap.Int("capacity", rb.capacity),
			zap.String("overwritten_type", string(overwrittenType)),
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
func (rb *RingBuffer) GetAll() []*Reading {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	if rb.size == 0 {
		return nil
	}

	result := make([]*Reading, rb.size)

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

// GetAllAndClear atomically returns all buffered readings and clears the buffer
// This prevents race conditions where data is added between GetAll() and Clear()
// The returned slice is a copy, so it's safe to use after the call
func (rb *RingBuffer) GetAllAndClear() []*Reading {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	if rb.size == 0 {
		return nil
	}

	result := make([]*Reading, rb.size)

	if rb.size < rb.capacity {
		// Buffer is not full yet, readings are from 0 to head-1
		copy(result, rb.data[:rb.size])
	} else {
		// Buffer is full, readings are from head to end, then 0 to head-1
		n := copy(result, rb.data[rb.head:])
		copy(result[n:], rb.data[:rb.head])
	}

	// Clear the buffer atomically
	rb.size = 0
	rb.head = 0
	rb.data = make([]*Reading, rb.capacity)

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
	rb.data = make([]*Reading, rb.capacity)
}
