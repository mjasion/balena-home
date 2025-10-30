package scanner

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mjasion/balena-home/pkg/buffer"
	"github.com/mjasion/balena-home/pkg/types"
	"go.uber.org/zap"
)

func TestNewScanner(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	ringBuffer := buffer.New[*types.Reading](100, logger)

	sensors := []SensorConfig{
		{Name: "Sensor1", ID: 1, MACAddress: "A4:C1:38:00:00:01"},
		{Name: "Sensor2", ID: 2, MACAddress: "A4:C1:38:00:00:02"},
		{Name: "Sensor3", ID: 3, MACAddress: "a4:c1:38:00:00:03"}, // lowercase
	}

	scanner := New(sensors, ringBuffer, logger)

	if scanner == nil {
		t.Fatal("Expected scanner to be created, got nil")
	}

	if scanner.buffer != ringBuffer {
		t.Error("Scanner buffer not set correctly")
	}

	if scanner.logger != logger {
		t.Error("Scanner logger not set correctly")
	}

	if scanner.adapter == nil {
		t.Error("Expected adapter to be initialized")
	}

	// Verify MAC addresses are normalized to uppercase
	expectedMACCount := 3
	if len(scanner.sensorMACs) != expectedMACCount {
		t.Errorf("Expected %d sensor MACs, got %d", expectedMACCount, len(scanner.sensorMACs))
	}

	// Check uppercase normalization and sensor info
	sensorInfo1, ok := scanner.sensorMACs["A4:C1:38:00:00:01"]
	if !ok {
		t.Error("Expected A4:C1:38:00:00:01 to be in sensor map")
	} else if sensorInfo1.Name != "Sensor1" || sensorInfo1.ID != 1 {
		t.Errorf("Expected Sensor1 with ID 1, got %s with ID %d", sensorInfo1.Name, sensorInfo1.ID)
	}

	sensorInfo2, ok := scanner.sensorMACs["A4:C1:38:00:00:02"]
	if !ok {
		t.Error("Expected A4:C1:38:00:00:02 to be in sensor map")
	} else if sensorInfo2.Name != "Sensor2" || sensorInfo2.ID != 2 {
		t.Errorf("Expected Sensor2 with ID 2, got %s with ID %d", sensorInfo2.Name, sensorInfo2.ID)
	}

	sensorInfo3, ok := scanner.sensorMACs["A4:C1:38:00:00:03"]
	if !ok {
		t.Error("Expected A4:C1:38:00:00:03 (uppercase) to be in sensor map")
	} else if sensorInfo3.Name != "Sensor3" || sensorInfo3.ID != 3 {
		t.Errorf("Expected Sensor3 with ID 3, got %s with ID %d", sensorInfo3.Name, sensorInfo3.ID)
	}

	// Verify lowercase version is not in map (should be normalized)
	if _, ok := scanner.sensorMACs["a4:c1:38:00:00:03"]; ok {
		t.Error("Did not expect lowercase MAC to be in sensor map")
	}
}

func TestNewScanner_EmptyMACList(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	ringBuffer := buffer.New[*types.Reading](100, logger)

	scanner := New([]SensorConfig{}, ringBuffer, logger)

	if scanner == nil {
		t.Fatal("Expected scanner to be created, got nil")
	}

	if len(scanner.sensorMACs) != 0 {
		t.Errorf("Expected 0 sensor MACs, got %d", len(scanner.sensorMACs))
	}
}

func TestNewScanner_DuplicateMACs(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	ringBuffer := buffer.New[*types.Reading](100, logger)

	// Include duplicates and case variations
	sensors := []SensorConfig{
		{Name: "Sensor1", ID: 1, MACAddress: "A4:C1:38:00:00:01"},
		{Name: "Sensor1Dup", ID: 2, MACAddress: "A4:C1:38:00:00:01"}, // exact duplicate MAC
		{Name: "Sensor1Lower", ID: 3, MACAddress: "a4:c1:38:00:00:01"}, // case variation
		{Name: "Sensor2", ID: 4, MACAddress: "A4:C1:38:00:00:02"},
	}

	scanner := New(sensors, ringBuffer, logger)

	// Should only have 2 unique MACs (duplicates removed, last one wins)
	expectedCount := 2
	if len(scanner.sensorMACs) != expectedCount {
		t.Errorf("Expected %d unique sensor MACs, got %d", expectedCount, len(scanner.sensorMACs))
	}

	// Verify both unique MACs are present (last value for duplicate MAC wins)
	if _, ok := scanner.sensorMACs["A4:C1:38:00:00:01"]; !ok {
		t.Error("Expected A4:C1:38:00:00:01 to be in sensor map")
	}

	if _, ok := scanner.sensorMACs["A4:C1:38:00:00:02"]; !ok {
		t.Error("Expected A4:C1:38:00:00:02 to be in sensor map")
	}
}

func TestNewScanner_MixedCaseMACs(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	ringBuffer := buffer.New[*types.Reading](100, logger)

	sensors := []SensorConfig{
		{Name: "Sensor1", ID: 1, MACAddress: "a4:c1:38:00:00:01"}, // all lowercase
		{Name: "Sensor2", ID: 2, MACAddress: "A4:C1:38:00:00:02"}, // all uppercase
		{Name: "Sensor3", ID: 3, MACAddress: "A4:c1:38:00:00:03"}, // mixed case
	}

	scanner := New(sensors, ringBuffer, logger)

	// All should be normalized to uppercase
	if _, ok := scanner.sensorMACs["A4:C1:38:00:00:01"]; !ok {
		t.Error("Expected A4:C1:38:00:00:01 (normalized) to be in sensor map")
	}

	if _, ok := scanner.sensorMACs["A4:C1:38:00:00:02"]; !ok {
		t.Error("Expected A4:C1:38:00:00:02 to be in sensor map")
	}

	if _, ok := scanner.sensorMACs["A4:C1:38:00:00:03"]; !ok {
		t.Error("Expected A4:C1:38:00:00:03 (normalized) to be in sensor map")
	}
}

func TestNewScanner_LargeMACList(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	ringBuffer := buffer.New[*types.Reading](100, logger)

	// Create a large list of sensor configs
	var sensors []SensorConfig
	for i := 0; i < 100; i++ {
		// Generate unique MACs by varying the last byte
		mac := fmt.Sprintf("A4:C1:38:00:%02X:%02X", i/256, i%256)
		sensors = append(sensors, SensorConfig{
			Name:       fmt.Sprintf("Sensor%d", i),
			ID:         i,
			MACAddress: mac,
		})
	}

	scanner := New(sensors, ringBuffer, logger)

	if scanner == nil {
		t.Fatal("Expected scanner to be created, got nil")
	}

	// Should handle large MAC lists
	if len(scanner.sensorMACs) != 100 {
		t.Errorf("Expected scanner to have 100 sensor MACs, got %d", len(scanner.sensorMACs))
	}
}

// Note: Full integration tests for Start() and Stop() are difficult without
// actual BLE hardware or a comprehensive BLE mock. The scanner.go implementation
// uses tinygo.org/x/bluetooth which requires either real hardware or a complex mock.
// Below are basic structural tests that can be expanded with proper mocking.

func TestScanner_Structure(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	ringBuffer := buffer.New[*types.Reading](100, logger)
	sensors := []SensorConfig{
		{Name: "Sensor1", ID: 1, MACAddress: "A4:C1:38:00:00:01"},
	}

	scanner := New(sensors, ringBuffer, logger)

	// Verify scanner has all required fields
	if scanner.adapter == nil {
		t.Error("Expected adapter to be set")
	}

	if scanner.sensorMACs == nil {
		t.Error("Expected sensorMACs map to be initialized")
	}

	if scanner.buffer == nil {
		t.Error("Expected buffer to be set")
	}

	if scanner.logger == nil {
		t.Error("Expected logger to be set")
	}
}

func TestScanner_MACFiltering(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	ringBuffer := buffer.New[*types.Reading](100, logger)

	// Only allow specific MACs
	sensors := []SensorConfig{
		{Name: "Sensor1", ID: 1, MACAddress: "A4:C1:38:00:00:01"},
		{Name: "Sensor2", ID: 2, MACAddress: "A4:C1:38:00:00:02"},
	}

	scanner := New(sensors, ringBuffer, logger)

	// Test that allowed MACs are in the map
	testCases := []struct {
		mac      string
		expected bool
	}{
		{"A4:C1:38:00:00:01", true},
		{"A4:C1:38:00:00:02", true},
		{"a4:c1:38:00:00:01", true}, // lowercase should work (gets normalized)
		{"A4:C1:38:00:00:03", false},
		{"B4:C1:38:00:00:01", false},
		{"", false},
	}

	for _, tc := range testCases {
		mac := strings.ToUpper(tc.mac)
		_, result := scanner.sensorMACs[mac]
		if result != tc.expected {
			t.Errorf("For MAC %s (normalized: %s), expected %v, got %v",
				tc.mac, mac, tc.expected, result)
		}
	}
}
