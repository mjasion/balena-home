package aggregator

import (
	"context"
	"testing"
	"time"

	"github.com/mjasion/balena-home/thermostats/buffer"
	"github.com/mjasion/balena-home/thermostats/scanner"
	"go.uber.org/zap"
)

func TestRun_WritesWeightedAvgToMetricsBuffer(t *testing.T) {
	logger := zap.NewNop()
	ctx := context.Background()

	sensors := []scanner.SensorConfig{
		{Name: "Salon", ID: 2, MACAddress: "a4:c1:38:26:e2:4c"},
		{Name: "Sypialnia", ID: 1, MACAddress: "A4:C1:38:ED:C0:21"},
	}

	controlBuffer := buffer.NewWithAutoCleanup(100, logger)
	metricsBuffer := buffer.New(100, logger)

	now := time.Now()
	for _, r := range []struct {
		mac  string
		age  time.Duration
		temp float64
	}{
		{"A4:C1:38:26:E2:4C", 40 * time.Second, 24.0},
		{"A4:C1:38:26:E2:4C", 10 * time.Second, 25.0},
		{"A4:C1:38:ED:C0:21", 20 * time.Second, 22.0},
	} {
		controlBuffer.Add(ctx, &buffer.Reading{
			Type: buffer.ReadingTypeBLE,
			BLE: &buffer.SensorReading{
				Timestamp:          now.Add(-r.age),
				MAC:                r.mac,
				TemperatureCelsius: r.temp,
				HumidityPercent:    45,
			},
		})
	}

	New(sensors, controlBuffer, metricsBuffer, logger).Run(ctx)

	got := make(map[string]*buffer.WeightedAvgReading)
	for _, r := range metricsBuffer.GetAll() {
		if r.Type != buffer.ReadingTypeBLEWeightedAvg || r.WeightedAvg == nil {
			t.Fatalf("unexpected reading type in metrics buffer: %s", r.Type)
		}
		got[r.WeightedAvg.RoomName] = r.WeightedAvg
	}

	if len(got) != 2 {
		t.Fatalf("expected weighted averages for 2 sensors, got %d", len(got))
	}

	salon := got["Salon"]
	if salon == nil || salon.ReadingCount != 2 || salon.SensorID != 2 {
		t.Fatalf("unexpected Salon reading: %+v", salon)
	}
	if salon.TemperatureCelsius < 24.0 || salon.TemperatureCelsius > 25.0 {
		t.Errorf("Salon weighted avg %.2f outside of [24, 25]", salon.TemperatureCelsius)
	}

	if s := got["Sypialnia"]; s == nil || s.TemperatureCelsius != 22.0 || s.HumidityPercent != 45 {
		t.Errorf("unexpected Sypialnia reading: %+v", s)
	}
}

func TestRun_NoReadings(t *testing.T) {
	logger := zap.NewNop()
	metricsBuffer := buffer.New(10, logger)

	sensors := []scanner.SensorConfig{{Name: "Salon", ID: 2, MACAddress: "A4:C1:38:26:E2:4C"}}
	New(sensors, buffer.NewWithAutoCleanup(10, logger), metricsBuffer, logger).Run(context.Background())

	if n := metricsBuffer.Size(); n != 0 {
		t.Errorf("expected empty metrics buffer, got %d readings", n)
	}
}
