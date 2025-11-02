package power

import "time"

// MultiSensorResponse represents the JSON response from the energy meter
type MultiSensorResponse struct {
	MultiSensor MultiSensor `json:"multiSensor"`
}

// MultiSensor contains the sensor configuration and readings
type MultiSensor struct {
	PeriodicCounter PeriodicCounter `json:"periodicCounter"`
	EnergyMeter     EnergyMeter     `json:"energyMeter"`
	Sensors         []Sensor        `json:"sensors"`
}

// PeriodicCounter contains periodic counter settings
type PeriodicCounter struct {
	Enabled   int `json:"enabled"`
	ResetType int `json:"resetType"`
}

// EnergyMeter contains energy meter configuration
type EnergyMeter struct {
	MeasureReverseEnergy MeasureReverseEnergy `json:"measureReverseEnergy"`
}

// MeasureReverseEnergy contains reverse energy measurement settings
type MeasureReverseEnergy struct {
	Enabled int `json:"enabled"`
}

// Sensor represents a single sensor reading
type Sensor struct {
	ID      int     `json:"id"`
	Type    string  `json:"type"`
	Name    string  `json:"name"`
	Value   float64 `json:"value"`
	State   int     `json:"state"`
	IconSet int     `json:"iconSet,omitempty"`
}

// SensorReading represents a single sensor measurement with metadata
type SensorReading struct {
	SensorID   int
	SensorType string
	SensorName string
	Value      float64
	Timestamp  time.Time
}

// ScrapeResult contains the results of a scraping operation
type ScrapeResult struct {
	Readings  []SensorReading
	Timestamp time.Time
	Error     error
}

// ConvertCamelToSnake converts camelCase strings to snake_case (lowercase)
func ConvertCamelToSnake(s string) string {
	var result []rune
	for i, r := range s {
		// Add underscore before uppercase letters (except the first character)
		if i > 0 && r >= 'A' && r <= 'Z' {
			result = append(result, '_')
		}
		// Convert to lowercase
		if r >= 'A' && r <= 'Z' {
			result = append(result, r+32) // Convert to lowercase
		} else {
			result = append(result, r)
		}
	}
	return string(result)
}

// GetAllSensors extracts all sensors from the response
func (r *MultiSensorResponse) GetAllSensors() []SensorReading {
	var readings []SensorReading
	now := time.Now()

	for _, sensor := range r.MultiSensor.Sensors {
		// Convert sensor type from camelCase to snake_case
		sensorType := ConvertCamelToSnake(sensor.Type)

		readings = append(readings, SensorReading{
			SensorID:   sensor.ID,
			SensorType: sensorType,
			SensorName: sensor.Name,
			Value:      sensor.Value,
			Timestamp:  now,
		})
	}

	return readings
}
