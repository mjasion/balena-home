package scraper

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

// ActivePowerReading represents a single active power measurement with metadata
type ActivePowerReading struct {
	SensorID  int
	Value     float64
	Timestamp time.Time
}

// ScrapeResult contains the results of a scraping operation
type ScrapeResult struct {
	Readings  []ActivePowerReading
	Timestamp time.Time
	Error     error
}

// FilterActivePower extracts all sensors with type "activePower" from the response
func (r *MultiSensorResponse) FilterActivePower() []ActivePowerReading {
	var readings []ActivePowerReading
	now := time.Now()

	for _, sensor := range r.MultiSensor.Sensors {
		if sensor.Type == "activePower" {
			readings = append(readings, ActivePowerReading{
				SensorID:  sensor.ID,
				Value:     sensor.Value,
				Timestamp: now,
			})
		}
	}

	return readings
}
