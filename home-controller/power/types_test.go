package power

import "testing"

func TestFilterActivePower(t *testing.T) {
	response := &MultiSensorResponse{
		MultiSensor: MultiSensor{
			Sensors: []Sensor{
				{ID: 0, Type: "activePower", Value: 100},
				{ID: 1, Type: "voltage", Value: 230},
				{ID: 2, Type: "activePower", Value: 50},
				{ID: 3, Type: "current", Value: 5},
			},
		},
	}

	readings := response.FilterActivePower()

	if len(readings) != 2 {
		t.Errorf("Expected 2 activePower readings, got %d", len(readings))
	}

	// Verify the readings are correct
	expectedIDs := map[int]bool{0: true, 2: true}
	for _, reading := range readings {
		if !expectedIDs[reading.SensorID] {
			t.Errorf("Unexpected sensor ID: %d", reading.SensorID)
		}
	}
}

func TestFilterActivePower_NoActivePower(t *testing.T) {
	response := &MultiSensorResponse{
		MultiSensor: MultiSensor{
			Sensors: []Sensor{
				{ID: 1, Type: "voltage", Value: 230},
				{ID: 3, Type: "current", Value: 5},
			},
		},
	}

	readings := response.FilterActivePower()

	if len(readings) != 0 {
		t.Errorf("Expected 0 activePower readings, got %d", len(readings))
	}
}

func TestFilterActivePower_EmptySensors(t *testing.T) {
	response := &MultiSensorResponse{
		MultiSensor: MultiSensor{
			Sensors: []Sensor{},
		},
	}

	readings := response.FilterActivePower()

	if len(readings) != 0 {
		t.Errorf("Expected 0 activePower readings, got %d", len(readings))
	}
}
