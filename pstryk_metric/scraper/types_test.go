package scraper

import (
	"encoding/json"
	"testing"
	"time"
)

const sampleJSON = `{
    "multiSensor": {
        "periodicCounter": {
            "enabled": 0,
            "resetType": 3
        },
        "energyMeter": {
            "measureReverseEnergy": {
                "enabled": 1
            }
        },
        "sensors": [
            {
                "id": 0,
                "type": "forwardActiveEnergy",
                "name": "",
                "value": 194959,
                "state": 2,
                "iconSet": 89
            },
            {
                "id": 0,
                "type": "activePower",
                "name": "",
                "value": 89,
                "state": 2,
                "iconSet": 96
            },
            {
                "id": 0,
                "type": "voltage",
                "name": "",
                "value": 2417,
                "state": 2
            },
            {
                "id": 1,
                "type": "activePower",
                "value": -1,
                "state": 2,
                "name": ""
            },
            {
                "id": 2,
                "type": "activePower",
                "value": 73,
                "state": 2,
                "name": ""
            },
            {
                "id": 3,
                "type": "activePower",
                "value": 17,
                "state": 2,
                "name": ""
            },
            {
                "id": 3,
                "type": "voltage",
                "value": 2414,
                "state": 2,
                "name": ""
            }
        ]
    }
}`

func TestMultiSensorResponse_Unmarshal(t *testing.T) {
	var response MultiSensorResponse
	err := json.Unmarshal([]byte(sampleJSON), &response)
	if err != nil {
		t.Fatalf("Failed to unmarshal JSON: %v", err)
	}

	if response.MultiSensor.PeriodicCounter.Enabled != 0 {
		t.Errorf("Expected periodicCounter.enabled to be 0, got %d", response.MultiSensor.PeriodicCounter.Enabled)
	}

	if response.MultiSensor.PeriodicCounter.ResetType != 3 {
		t.Errorf("Expected periodicCounter.resetType to be 3, got %d", response.MultiSensor.PeriodicCounter.ResetType)
	}

	if response.MultiSensor.EnergyMeter.MeasureReverseEnergy.Enabled != 1 {
		t.Errorf("Expected energyMeter.measureReverseEnergy.enabled to be 1, got %d", response.MultiSensor.EnergyMeter.MeasureReverseEnergy.Enabled)
	}

	expectedSensors := 7
	if len(response.MultiSensor.Sensors) != expectedSensors {
		t.Errorf("Expected %d sensors, got %d", expectedSensors, len(response.MultiSensor.Sensors))
	}
}

func TestFilterActivePower(t *testing.T) {
	var response MultiSensorResponse
	err := json.Unmarshal([]byte(sampleJSON), &response)
	if err != nil {
		t.Fatalf("Failed to unmarshal JSON: %v", err)
	}

	readings := response.FilterActivePower()

	expectedReadings := 4
	if len(readings) != expectedReadings {
		t.Fatalf("Expected %d active power readings, got %d", expectedReadings, len(readings))
	}

	// Verify sensor IDs and values
	expectedValues := map[int]float64{
		0: 89,
		1: -1,
		2: 73,
		3: 17,
	}

	for _, reading := range readings {
		expectedValue, exists := expectedValues[reading.SensorID]
		if !exists {
			t.Errorf("Unexpected sensor ID: %d", reading.SensorID)
			continue
		}

		if reading.Value != expectedValue {
			t.Errorf("For sensor %d, expected value %f, got %f", reading.SensorID, expectedValue, reading.Value)
		}

		if reading.Timestamp.IsZero() {
			t.Errorf("Expected timestamp to be set for sensor %d", reading.SensorID)
		}
	}
}

func TestFilterActivePower_NoActivePower(t *testing.T) {
	jsonWithoutActivePower := `{
		"multiSensor": {
			"periodicCounter": {
				"enabled": 0,
				"resetType": 3
			},
			"energyMeter": {
				"measureReverseEnergy": {
					"enabled": 1
				}
			},
			"sensors": [
				{
					"id": 0,
					"type": "voltage",
					"name": "",
					"value": 2417,
					"state": 2
				},
				{
					"id": 0,
					"type": "current",
					"name": "",
					"value": 580,
					"state": 2
				}
			]
		}
	}`

	var response MultiSensorResponse
	err := json.Unmarshal([]byte(jsonWithoutActivePower), &response)
	if err != nil {
		t.Fatalf("Failed to unmarshal JSON: %v", err)
	}

	readings := response.FilterActivePower()

	if len(readings) != 0 {
		t.Errorf("Expected 0 active power readings, got %d", len(readings))
	}
}

func TestFilterActivePower_EmptySensors(t *testing.T) {
	jsonEmpty := `{
		"multiSensor": {
			"periodicCounter": {
				"enabled": 0,
				"resetType": 3
			},
			"energyMeter": {
				"measureReverseEnergy": {
					"enabled": 1
				}
			},
			"sensors": []
		}
	}`

	var response MultiSensorResponse
	err := json.Unmarshal([]byte(jsonEmpty), &response)
	if err != nil {
		t.Fatalf("Failed to unmarshal JSON: %v", err)
	}

	readings := response.FilterActivePower()

	if len(readings) != 0 {
		t.Errorf("Expected 0 active power readings from empty sensors, got %d", len(readings))
	}
}

func TestSensorTypes(t *testing.T) {
	var response MultiSensorResponse
	err := json.Unmarshal([]byte(sampleJSON), &response)
	if err != nil {
		t.Fatalf("Failed to unmarshal JSON: %v", err)
	}

	typeCount := make(map[string]int)
	for _, sensor := range response.MultiSensor.Sensors {
		typeCount[sensor.Type]++
	}

	if typeCount["activePower"] != 4 {
		t.Errorf("Expected 4 activePower sensors, got %d", typeCount["activePower"])
	}

	if typeCount["voltage"] != 2 {
		t.Errorf("Expected 2 voltage sensors, got %d", typeCount["voltage"])
	}

	if typeCount["forwardActiveEnergy"] != 1 {
		t.Errorf("Expected 1 forwardActiveEnergy sensor, got %d", typeCount["forwardActiveEnergy"])
	}
}

func TestActivePowerReading_Fields(t *testing.T) {
	reading := ActivePowerReading{
		SensorID:  5,
		Value:     123.45,
		Timestamp: testTime(),
	}

	if reading.SensorID != 5 {
		t.Errorf("Expected SensorID to be 5, got %d", reading.SensorID)
	}

	if reading.Value != 123.45 {
		t.Errorf("Expected Value to be 123.45, got %f", reading.Value)
	}

	if reading.Timestamp.IsZero() {
		t.Error("Expected Timestamp to be set")
	}
}

func testTime() time.Time {
	return time.Date(2025, 10, 24, 21, 30, 0, 0, time.UTC)
}
