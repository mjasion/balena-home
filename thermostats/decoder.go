package main

import (
	"encoding/binary"
	"fmt"
	"time"
)

// DecodeATCAdvertisement decodes the ATC_MiThermometer advertisement format
// Format (13 bytes):
// - Bytes 0-5: MAC address (big endian)
// - Bytes 6-7: Temperature in 0.1°C (little endian signed int16)
// - Byte 8: Humidity in % (unsigned int8)
// - Byte 9: Battery percentage (unsigned int8)
// - Bytes 10-11: Battery voltage in mV (little endian unsigned int16)
// - Byte 12: Frame counter (unsigned int8)
func DecodeATCAdvertisement(data []byte, rssi int16) (*SensorReading, error) {
	if len(data) < 13 {
		return nil, fmt.Errorf("invalid ATC advertisement length: expected at least 13 bytes, got %d", len(data))
	}

	// Extract MAC address (bytes 0-5, big endian)
	mac := fmt.Sprintf("%02X:%02X:%02X:%02X:%02X:%02X",
		data[0], data[1], data[2], data[3], data[4], data[5])

	// Extract temperature (bytes 6-7, little endian signed int16, divide by 10)
	tempRaw := int16(binary.LittleEndian.Uint16(data[6:8]))
	temperature := float64(tempRaw) / 10.0

	// Extract humidity (byte 8, unsigned int8)
	humidity := int(data[8])

	// Extract battery percentage (byte 9, unsigned int8)
	batteryPercent := int(data[9])

	// Extract battery voltage (bytes 10-11, little endian unsigned int16)
	batteryVoltageMV := int(binary.LittleEndian.Uint16(data[10:12]))

	// Extract frame counter (byte 12, unsigned int8)
	frameCounter := int(data[12])

	reading := &SensorReading{
		Timestamp:          time.Now(),
		MAC:                mac,
		TemperatureCelsius: temperature,
		HumidityPercent:    humidity,
		BatteryPercent:     batteryPercent,
		BatteryVoltageMV:   batteryVoltageMV,
		FrameCounter:       frameCounter,
		RSSI:               rssi,
	}

	return reading, nil
}
