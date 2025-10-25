package scraper

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.uber.org/zap"
)

const fullSampleJSON = `{
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
                "type": "reverseActiveEnergy",
                "name": "",
                "value": 0,
                "state": 2,
                "iconSet": 91
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
            }
        ]
    }
}`

func TestScrape_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(fullSampleJSON))
	}))
	defer server.Close()

	logger := zap.NewNop()
	scraper := New(server.URL, 5*time.Second, logger)
	ctx := context.Background()

	result, err := scraper.Scrape(ctx)
	if err != nil {
		t.Fatalf("Expected successful scrape, got error: %v", err)
	}

	if result.Error != nil {
		t.Errorf("Expected no error in result, got: %v", result.Error)
	}

	if len(result.Readings) != 4 {
		t.Errorf("Expected 4 active power readings, got %d", len(result.Readings))
	}

	if result.Timestamp.IsZero() {
		t.Error("Expected timestamp to be set")
	}

	// Verify readings
	expectedValues := map[int]float64{
		0: 89,
		1: -1,
		2: 73,
		3: 17,
	}

	for _, reading := range result.Readings {
		expectedValue, exists := expectedValues[reading.SensorID]
		if !exists {
			t.Errorf("Unexpected sensor ID: %d", reading.SensorID)
			continue
		}

		if reading.Value != expectedValue {
			t.Errorf("For sensor %d, expected value %f, got %f", reading.SensorID, expectedValue, reading.Value)
		}
	}
}

func TestScrape_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Internal Server Error"))
	}))
	defer server.Close()

	scraper := New(server.URL, 5*time.Second, zap.NewNop())
	ctx := context.Background()

	result, err := scraper.Scrape(ctx)
	if err == nil {
		t.Error("Expected error for HTTP 500, got nil")
	}

	if result.Error == nil {
		t.Error("Expected error in result for HTTP 500")
	}
}

func TestScrape_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"invalid": json}`))
	}))
	defer server.Close()

	scraper := New(server.URL, 5*time.Second, zap.NewNop())
	ctx := context.Background()

	result, err := scraper.Scrape(ctx)
	if err == nil {
		t.Error("Expected error for invalid JSON, got nil")
	}

	if result.Error == nil {
		t.Error("Expected error in result for invalid JSON")
	}
}

func TestScrape_NoActivePowerSensors(t *testing.T) {
	jsonNoActivePower := `{
		"multiSensor": {
			"periodicCounter": {"enabled": 0, "resetType": 3},
			"energyMeter": {"measureReverseEnergy": {"enabled": 1}},
			"sensors": [
				{"id": 0, "type": "voltage", "value": 2417, "state": 2, "name": ""}
			]
		}
	}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(jsonNoActivePower))
	}))
	defer server.Close()

	scraper := New(server.URL, 5*time.Second, zap.NewNop())
	ctx := context.Background()

	result, err := scraper.Scrape(ctx)
	if err != nil {
		t.Fatalf("Expected successful scrape, got error: %v", err)
	}

	if len(result.Readings) != 0 {
		t.Errorf("Expected 0 active power readings, got %d", len(result.Readings))
	}
}

func TestScrape_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	scraper := New(server.URL, 100*time.Millisecond, zap.NewNop())
	ctx := context.Background()

	result, err := scraper.Scrape(ctx)
	if err == nil {
		t.Error("Expected timeout error, got nil")
	}

	if result.Error == nil {
		t.Error("Expected error in result for timeout")
	}
}

func TestScrape_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	scraper := New(server.URL, 5*time.Second, zap.NewNop())
	ctx, cancel := context.WithCancel(context.Background())

	// Cancel context immediately
	cancel()

	_, err := scraper.Scrape(ctx)
	if err == nil {
		t.Error("Expected context cancellation error, got nil")
	}
}

func TestScrape_RetryLogic(t *testing.T) {
	attemptCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attemptCount++
		if attemptCount < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(fullSampleJSON))
	}))
	defer server.Close()

	scraper := New(server.URL, 5*time.Second, zap.NewNop())
	ctx := context.Background()

	result, err := scraper.Scrape(ctx)
	if err != nil {
		t.Fatalf("Expected successful scrape after retries, got error: %v", err)
	}

	if attemptCount != 3 {
		t.Errorf("Expected 3 attempts, got %d", attemptCount)
	}

	if len(result.Readings) != 4 {
		t.Errorf("Expected 4 active power readings, got %d", len(result.Readings))
	}
}

func TestScrape_ExhaustedRetries(t *testing.T) {
	attemptCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attemptCount++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	scraper := New(server.URL, 5*time.Second, zap.NewNop())
	ctx := context.Background()

	result, err := scraper.Scrape(ctx)
	if err == nil {
		t.Error("Expected error after exhausted retries, got nil")
	}

	if attemptCount != 3 {
		t.Errorf("Expected exactly 3 retry attempts, got %d", attemptCount)
	}

	if result.Error == nil {
		t.Error("Expected error in result after exhausted retries")
	}
}
