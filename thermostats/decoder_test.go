package main

import (
	"testing"
	"time"
)

func TestDecodeATCAdvertisement_Valid(t *testing.T) {
	// Sample valid ATC advertisement data
	// MAC: A4:C1:38:12:34:56
	// Temperature: 22.5°C (225 in 0.1°C = 0x00E1 little endian = E1 00)
	// Humidity: 65% (0x41)
	// Battery: 95% (0x5F)
	// Battery voltage: 3000mV (0x0BB8 little endian = B8 0B)
	// Frame counter: 42 (0x2A)
	data := []byte{
		0xA4, 0xC1, 0x38, 0x12, 0x34, 0x56, // MAC address
		0xE1, 0x00, // Temperature: 225 * 0.1 = 22.5°C
		0x41,       // Humidity: 65%
		0x5F,       // Battery: 95%
		0xB8, 0x0B, // Battery voltage: 3000mV
		0x2A, // Frame counter: 42
	}
	rssi := int16(-65)

	reading, err := DecodeATCAdvertisement(data, rssi)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	expectedMAC := "A4:C1:38:12:34:56"
	if reading.MAC != expectedMAC {
		t.Errorf("Expected MAC %s, got %s", expectedMAC, reading.MAC)
	}

	expectedTemp := 22.5
	if reading.TemperatureCelsius != expectedTemp {
		t.Errorf("Expected temperature %v, got %v", expectedTemp, reading.TemperatureCelsius)
	}

	expectedHumidity := 65
	if reading.HumidityPercent != expectedHumidity {
		t.Errorf("Expected humidity %d, got %d", expectedHumidity, reading.HumidityPercent)
	}

	expectedBattery := 95
	if reading.BatteryPercent != expectedBattery {
		t.Errorf("Expected battery %d, got %d", expectedBattery, reading.BatteryPercent)
	}

	expectedVoltage := 3000
	if reading.BatteryVoltageMV != expectedVoltage {
		t.Errorf("Expected voltage %d, got %d", expectedVoltage, reading.BatteryVoltageMV)
	}

	expectedFrame := 42
	if reading.FrameCounter != expectedFrame {
		t.Errorf("Expected frame counter %d, got %d", expectedFrame, reading.FrameCounter)
	}

	if reading.RSSI != rssi {
		t.Errorf("Expected RSSI %d, got %d", rssi, reading.RSSI)
	}

	// Verify timestamp is recent (within last second)
	if time.Since(reading.Timestamp) > time.Second {
		t.Errorf("Timestamp is too old: %v", reading.Timestamp)
	}
}

func TestDecodeATCAdvertisement_NegativeTemperature(t *testing.T) {
	// Test with negative temperature: -10.5°C
	// -10.5 * 10 = -105 = 0xFF97 (two's complement)
	data := []byte{
		0xA4, 0xC1, 0x38, 0x12, 0x34, 0x56, // MAC
		0x97, 0xFF, // Temperature: -105 * 0.1 = -10.5°C
		0x50,       // Humidity: 80%
		0x64,       // Battery: 100%
		0xE8, 0x0B, // Voltage: 3048mV
		0x01, // Frame counter: 1
	}

	reading, err := DecodeATCAdvertisement(data, -70)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	expectedTemp := -10.5
	if reading.TemperatureCelsius != expectedTemp {
		t.Errorf("Expected temperature %v, got %v", expectedTemp, reading.TemperatureCelsius)
	}
}

func TestDecodeATCAdvertisement_ZeroTemperature(t *testing.T) {
	// Test with zero temperature
	data := []byte{
		0xA4, 0xC1, 0x38, 0x12, 0x34, 0x56,
		0x00, 0x00, // Temperature: 0°C
		0x50,
		0x64,
		0xE8, 0x0B,
		0x00,
	}

	reading, err := DecodeATCAdvertisement(data, -60)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if reading.TemperatureCelsius != 0.0 {
		t.Errorf("Expected temperature 0.0, got %v", reading.TemperatureCelsius)
	}
}

func TestDecodeATCAdvertisement_InvalidLength_TooShort(t *testing.T) {
	// Test with data too short (only 12 bytes instead of 13)
	data := []byte{
		0xA4, 0xC1, 0x38, 0x12, 0x34, 0x56,
		0x00, 0x00,
		0x50,
		0x64,
		0xE8, 0x0B,
		// Missing frame counter byte
	}

	_, err := DecodeATCAdvertisement(data, -60)

	if err == nil {
		t.Fatal("Expected error for invalid data length, got nil")
	}

	expectedError := "invalid ATC advertisement length: expected at least 13 bytes, got 12"
	if err.Error() != expectedError {
		t.Errorf("Expected error message '%s', got '%s'", expectedError, err.Error())
	}
}

func TestDecodeATCAdvertisement_EmptyData(t *testing.T) {
	data := []byte{}

	_, err := DecodeATCAdvertisement(data, -60)

	if err == nil {
		t.Fatal("Expected error for empty data, got nil")
	}
}

func TestDecodeATCAdvertisement_ExtremeRSSI(t *testing.T) {
	data := []byte{
		0xA4, 0xC1, 0x38, 0x12, 0x34, 0x56,
		0xE1, 0x00,
		0x41,
		0x5F,
		0xB8, 0x0B,
		0x2A,
	}

	// Test with very weak signal
	reading1, err := DecodeATCAdvertisement(data, -100)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if reading1.RSSI != -100 {
		t.Errorf("Expected RSSI -100, got %d", reading1.RSSI)
	}

	// Test with very strong signal
	reading2, err := DecodeATCAdvertisement(data, -20)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if reading2.RSSI != -20 {
		t.Errorf("Expected RSSI -20, got %d", reading2.RSSI)
	}
}

func TestDecodeATCAdvertisement_BoundaryValues(t *testing.T) {
	tests := []struct {
		name        string
		data        []byte
		expectedMac string
	}{
		{
			name: "All zeros",
			data: []byte{
				0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
				0x00, 0x00,
				0x00,
				0x00,
				0x00, 0x00,
				0x00,
			},
			expectedMac: "00:00:00:00:00:00",
		},
		{
			name: "All max values",
			data: []byte{
				0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
				0xFF, 0xFF,
				0xFF,
				0xFF,
				0xFF, 0xFF,
				0xFF,
			},
			expectedMac: "FF:FF:FF:FF:FF:FF",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reading, err := DecodeATCAdvertisement(tt.data, -60)
			if err != nil {
				t.Fatalf("Expected no error, got: %v", err)
			}
			if reading.MAC != tt.expectedMac {
				t.Errorf("Expected MAC %s, got %s", tt.expectedMac, reading.MAC)
			}
		})
	}
}

func TestDecodeATCAdvertisement_LongerData(t *testing.T) {
	// Test with data longer than 13 bytes (should still work)
	data := []byte{
		0xA4, 0xC1, 0x38, 0x12, 0x34, 0x56,
		0xE1, 0x00,
		0x41,
		0x5F,
		0xB8, 0x0B,
		0x2A,
		0xFF, 0xFF, // Extra bytes (should be ignored)
	}

	reading, err := DecodeATCAdvertisement(data, -65)
	if err != nil {
		t.Fatalf("Expected no error for longer data, got: %v", err)
	}

	if reading.MAC != "A4:C1:38:12:34:56" {
		t.Errorf("Expected MAC A4:C1:38:12:34:56, got %s", reading.MAC)
	}
}
