package power

import "testing"

func TestGetAllSensors(t *testing.T) {
	response := &MultiSensorResponse{
		MultiSensor: MultiSensor{
			Sensors: []Sensor{
				{ID: 0, Type: "activePower", Name: "Total", Value: 100},
				{ID: 1, Type: "voltage", Name: "", Value: 230},
				{ID: 2, Type: "apparentPower", Name: "", Value: 50},
				{ID: 3, Type: "current", Name: "", Value: 5},
			},
		},
	}

	readings := response.GetAllSensors()

	if len(readings) != 4 {
		t.Errorf("Expected 4 sensor readings, got %d", len(readings))
	}

	// Verify the readings are correct and converted to snake_case
	expectedTypes := map[int]string{
		0: "active_power",
		1: "voltage",
		2: "apparent_power",
		3: "current",
	}
	for _, reading := range readings {
		expectedType, exists := expectedTypes[reading.SensorID]
		if !exists {
			t.Errorf("Unexpected sensor ID: %d", reading.SensorID)
		}
		if reading.SensorType != expectedType {
			t.Errorf("For sensor ID %d, expected type %q, got %q", reading.SensorID, expectedType, reading.SensorType)
		}
	}

	// Verify sensor name is preserved
	if readings[0].SensorName != "Total" {
		t.Errorf("Expected sensor name 'Total', got %q", readings[0].SensorName)
	}
}

func TestGetAllSensors_EmptySensors(t *testing.T) {
	response := &MultiSensorResponse{
		MultiSensor: MultiSensor{
			Sensors: []Sensor{},
		},
	}

	readings := response.GetAllSensors()

	if len(readings) != 0 {
		t.Errorf("Expected 0 sensor readings, got %d", len(readings))
	}
}

func TestConvertCamelToSnake(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"activePower", "active_power"},
		{"apparentPower", "apparent_power"},
		{"apparentEnergy", "apparent_energy"},
		{"forwardActiveEnergy", "forward_active_energy"},
		{"forwardReactiveEnergy", "forward_reactive_energy"},
		{"reverseActiveEnergy", "reverse_active_energy"},
		{"reverseReactiveEnergy", "reverse_reactive_energy"},
		{"reactivePower", "reactive_power"},
		{"current", "current"},
		{"voltage", "voltage"},
		{"frequency", "frequency"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := ConvertCamelToSnake(tt.input)
			if result != tt.expected {
				t.Errorf("ConvertCamelToSnake(%q) = %q, expected %q", tt.input, result, tt.expected)
			}
		})
	}
}
