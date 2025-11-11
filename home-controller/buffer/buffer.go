package buffer

import (
	"sync"
	"time"

	"go.uber.org/zap"
)

// ReadingType identifies the type of reading
type ReadingType string

const (
	ReadingTypeBLE            ReadingType = "ble"
	ReadingTypeNetatmo        ReadingType = "netatmo"
	ReadingTypePower          ReadingType = "power"
	ReadingTypeControl        ReadingType = "control"
	ReadingTypeBLEWeightedAvg ReadingType = "ble_weighted_avg"
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

// PowerReading represents a power/energy sensor measurement from energy meter
type PowerReading struct {
	Timestamp  interface{} // time.Time
	SensorID   int
	SensorType string // snake_case type: active_power, apparent_power, voltage, current, etc.
	SensorName string // Optional friendly name
	Value      float64
}

// ControlReading represents a control loop decision and metrics
type ControlReading struct {
	Timestamp              interface{} // time.Time
	RoomName               string
	Action                 string  // "skip", "no_adjustment_needed", "set_manual_override"
	XiaomiTemperature      float64
	ScheduledTemperature   float64
	ThermostatMeasured     float64
	ThermostatMode         string  // "schedule", "manual", "away", "hg" (frost guard), etc.
	CalculatedSetpoint     float64
	TemperatureDifference  float64 // xiaomiTemp - scheduledTemp
	SetpointAdjustment     float64 // calculatedSetpoint - thermostatMeasured
	ExternallyModified     bool
	HardOverrideActive     bool
}

// WeightedAvgReading represents a weighted average temperature reading from BLE sensors
type WeightedAvgReading struct {
	Timestamp          interface{} // time.Time
	MAC                string
	SensorName         string
	SensorID           int
	TemperatureCelsius float64 // Weighted average over last 60 seconds
	ReadingCount       int     // Number of readings used in calculation
}

// Reading is a union type that can hold BLE sensor, Netatmo thermostat, power, control, or weighted average readings
type Reading struct {
	Type          ReadingType
	BLE           *SensorReading
	Thermostat    *ThermostatReading
	Power         *PowerReading
	Control       *ControlReading
	WeightedAvg   *WeightedAvgReading
}

// RingBuffer is a thread-safe circular buffer for sensor readings
type RingBuffer struct {
	data              []*Reading
	capacity          int
	size              int
	head              int
	mu                sync.RWMutex
	logger            *zap.Logger
	enableAutoCleanup bool // If true, automatically cleanup readings older than 5 minutes
}

// New creates a new ring buffer with the specified capacity
func New(capacity int, logger *zap.Logger) *RingBuffer {
	return &RingBuffer{
		data:              make([]*Reading, capacity),
		capacity:          capacity,
		size:              0,
		head:              0,
		logger:            logger,
		enableAutoCleanup: false,
	}
}

// NewWithAutoCleanup creates a new ring buffer with automatic cleanup of old readings (>5 minutes)
func NewWithAutoCleanup(capacity int, logger *zap.Logger) *RingBuffer {
	return &RingBuffer{
		data:              make([]*Reading, capacity),
		capacity:          capacity,
		size:              0,
		head:              0,
		logger:            logger,
		enableAutoCleanup: true,
	}
}

// Add adds a new reading to the buffer
// If the buffer is full, it overwrites the oldest entry
// If auto-cleanup is enabled, removes readings older than 5 minutes
func (rb *RingBuffer) Add(reading *Reading) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	// Cleanup old readings (>5 minutes) if auto-cleanup is enabled
	if rb.enableAutoCleanup {
		rb.cleanupOldReadings()
	}

	// Check if we're about to overwrite data
	if rb.size == rb.capacity {
		overwrittenType := rb.data[rb.head].Type
		rb.logger.Debug("ring buffer full, overwriting oldest data",
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

// cleanupOldReadings removes readings older than 5 minutes
// Must be called with lock held
func (rb *RingBuffer) cleanupOldReadings() {
	if rb.size == 0 {
		return
	}

	cutoff := time.Now().Add(-5 * time.Minute)
	removed := 0

	// Iterate through buffer from oldest to newest
	// In a ring buffer, the oldest item is at (head - size) position
	for i := 0; i < rb.size; i++ {
		idx := (rb.head - rb.size + i + rb.capacity) % rb.capacity
		r := rb.data[idx]

		if r == nil {
			continue
		}

		// Extract timestamp based on reading type
		var timestamp time.Time
		switch r.Type {
		case ReadingTypeBLE:
			if r.BLE != nil {
				if ts, ok := r.BLE.Timestamp.(time.Time); ok {
					timestamp = ts
				}
			}
		case ReadingTypeNetatmo:
			if r.Thermostat != nil {
				if ts, ok := r.Thermostat.Timestamp.(time.Time); ok {
					timestamp = ts
				}
			}
		case ReadingTypePower:
			if r.Power != nil {
				if ts, ok := r.Power.Timestamp.(time.Time); ok {
					timestamp = ts
				}
			}
		case ReadingTypeControl:
			if r.Control != nil {
				if ts, ok := r.Control.Timestamp.(time.Time); ok {
					timestamp = ts
				}
			}
		case ReadingTypeBLEWeightedAvg:
			if r.WeightedAvg != nil {
				if ts, ok := r.WeightedAvg.Timestamp.(time.Time); ok {
					timestamp = ts
				}
			}
		}

		// If timestamp is older than cutoff, remove this entry
		if !timestamp.IsZero() && timestamp.Before(cutoff) {
			rb.data[idx] = nil
			removed++
		} else {
			// Once we hit a recent reading, all subsequent ones will be recent too
			// (because we're iterating from oldest to newest)
			break
		}
	}

	// Compact the buffer by removing nil entries
	if removed > 0 {
		// Create new slice of valid readings
		validReadings := make([]*Reading, 0, rb.size-removed)
		for i := 0; i < rb.size; i++ {
			idx := (rb.head - rb.size + i + rb.capacity) % rb.capacity
			if rb.data[idx] != nil {
				validReadings = append(validReadings, rb.data[idx])
			}
		}

		// Reset buffer state
		rb.size = len(validReadings)
		rb.head = rb.size % rb.capacity

		// Clear the data array and reinsert valid readings
		for i := range rb.data {
			rb.data[i] = nil
		}
		for i, r := range validReadings {
			rb.data[i] = r
		}

		rb.logger.Debug("cleaned up old readings from buffer",
			zap.Int("removed_count", removed),
			zap.Int("remaining_count", rb.size),
			zap.Duration("older_than", 5*time.Minute),
		)
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

// GetReadingsByTimeWindow returns all readings within the specified time window
// This is a non-destructive read that doesn't clear the buffer
// startTime and endTime should be time.Time values
// Returns readings where reading.Timestamp >= startTime AND reading.Timestamp <= endTime
func (rb *RingBuffer) GetReadingsByTimeWindow(startTime, endTime interface{}) []*Reading {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	if rb.size == 0 {
		return []*Reading{} // Return empty slice, not nil
	}

	// Type assert the input parameters
	start, okStart := startTime.(time.Time)
	end, okEnd := endTime.(time.Time)
	if !okStart || !okEnd {
		rb.logger.Error("GetReadingsByTimeWindow: invalid time parameters",
			zap.Any("startTime", startTime),
			zap.Any("endTime", endTime),
		)
		return []*Reading{}
	}

	result := make([]*Reading, 0, rb.size) // Pre-allocate with capacity

	// Iterate through all readings in order
	if rb.size < rb.capacity {
		// Buffer is not full yet, readings are from 0 to head-1
		for i := 0; i < rb.size; i++ {
			if matchesTimeWindow(rb.data[i], start, end) {
				result = append(result, rb.data[i])
			}
		}
	} else {
		// Buffer is full, readings are from head to end, then 0 to head-1
		for i := rb.head; i < rb.capacity; i++ {
			if matchesTimeWindow(rb.data[i], start, end) {
				result = append(result, rb.data[i])
			}
		}
		for i := 0; i < rb.head; i++ {
			if matchesTimeWindow(rb.data[i], start, end) {
				result = append(result, rb.data[i])
			}
		}
	}

	return result
}

// matchesTimeWindow checks if a reading's timestamp falls within the specified time window
func matchesTimeWindow(reading *Reading, start, end time.Time) bool {
	var timestamp time.Time
	var ok bool

	// Extract timestamp based on reading type
	switch reading.Type {
	case ReadingTypeBLE:
		if reading.BLE != nil {
			timestamp, ok = reading.BLE.Timestamp.(time.Time)
		}
	case ReadingTypeNetatmo:
		if reading.Thermostat != nil {
			timestamp, ok = reading.Thermostat.Timestamp.(time.Time)
		}
	case ReadingTypePower:
		if reading.Power != nil {
			timestamp, ok = reading.Power.Timestamp.(time.Time)
		}
	case ReadingTypeControl:
		if reading.Control != nil {
			timestamp, ok = reading.Control.Timestamp.(time.Time)
		}
	case ReadingTypeBLEWeightedAvg:
		if reading.WeightedAvg != nil {
			timestamp, ok = reading.WeightedAvg.Timestamp.(time.Time)
		}
	}

	if !ok {
		return false // Skip readings with invalid timestamps
	}

	// Check if timestamp is within window (inclusive on both ends)
	return (timestamp.Equal(start) || timestamp.After(start)) &&
	       (timestamp.Equal(end) || timestamp.Before(end))
}

// Size returns the current number of readings in the buffer
func (rb *RingBuffer) Size() int {
	rb.mu.RLock()
	defer rb.mu.RUnlock()
	return rb.size
}

// AddMultiple adds multiple readings to the buffer at once
// Useful for re-adding readings after a failed push attempt
func (rb *RingBuffer) AddMultiple(readings []*Reading) {
	if len(readings) == 0 {
		return
	}

	rb.mu.Lock()
	defer rb.mu.Unlock()

	for _, reading := range readings {
		// Check if we're about to overwrite data
		if rb.size == rb.capacity {
			rb.logger.Warn("ring buffer full during AddMultiple, overwriting oldest data",
				zap.Int("capacity", rb.capacity),
				zap.String("overwritten_type", string(rb.data[rb.head].Type)),
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
}
