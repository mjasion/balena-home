package main

import (
	"time"
)

// ReadingType identifies the type of reading
type ReadingType string

const (
	ReadingTypeBLE     ReadingType = "ble"
	ReadingTypeNetatmo ReadingType = "netatmo"
	ReadingTypePower   ReadingType = "power"
)

// SensorReading represents a single temperature sensor reading from BLE sensors
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

// ThermostatReading represents a thermostat reading from Netatmo
type ThermostatReading struct {
	Timestamp           time.Time `json:"timestamp"`
	HomeID              string    `json:"home_id"`
	HomeName            string    `json:"home_name"`
	RoomID              string    `json:"room_id"`
	RoomName            string    `json:"room_name"`
	MeasuredTemperature float64   `json:"measured_temperature"`
	SetpointTemperature float64   `json:"setpoint_temperature"`
	SetpointMode        string    `json:"setpoint_mode"`
	HeatingPowerRequest int       `json:"heating_power_request"`
	OpenWindow          bool      `json:"open_window"`
	Reachable           bool      `json:"reachable"`
}

// PowerReading represents an active power measurement from energy meter
type PowerReading struct {
	Timestamp time.Time `json:"timestamp"`
	SensorID  int       `json:"sensor_id"`
	Value     float64   `json:"value_watts"`
}

// Reading is a union type that can hold BLE sensor, Netatmo thermostat, or power readings
type Reading struct {
	Type       ReadingType
	BLE        *SensorReading
	Thermostat *ThermostatReading
	Power      *PowerReading
}
