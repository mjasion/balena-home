package types

import "time"

// ReadingType identifies the type of metric reading
type ReadingType string

const (
	ReadingTypeBLE        ReadingType = "ble"
	ReadingTypeThermostat ReadingType = "thermostat"
	ReadingTypeMetric     ReadingType = "metric"
)

// Reading is a union type that can hold different types of metric readings
type Reading struct {
	Type       ReadingType
	BLE        *BLEReading
	Thermostat *ThermostatReading
	Metric     *MetricReading
}

// BLEReading represents a temperature/humidity sensor reading from BLE
type BLEReading struct {
	Timestamp          time.Time
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
	Timestamp           time.Time
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

// MetricReading represents a generic metric reading (e.g., power consumption)
type MetricReading struct {
	Timestamp time.Time
	Name      string
	Value     float64
	Labels    map[string]string
}

// GetTimestamp returns the timestamp of the reading regardless of type
func (r *Reading) GetTimestamp() time.Time {
	switch r.Type {
	case ReadingTypeBLE:
		return r.BLE.Timestamp
	case ReadingTypeThermostat:
		return r.Thermostat.Timestamp
	case ReadingTypeMetric:
		return r.Metric.Timestamp
	default:
		return time.Time{}
	}
}
