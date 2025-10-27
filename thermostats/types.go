package main

import (
	"time"
)

// SensorReading represents a single temperature sensor reading
type SensorReading struct {
	Timestamp          time.Time `json:"timestamp"`
	MAC                string    `json:"mac"`
	TemperatureCelsius float64   `json:"temperature_celsius"`
	HumidityPercent    int       `json:"humidity_percent"`
	BatteryPercent     int       `json:"battery_percent"`
	BatteryVoltageMV   int       `json:"battery_voltage_mv"`
	FrameCounter       int       `json:"frame_counter"`
	RSSI               int16     `json:"rssi_dbm"`
}
