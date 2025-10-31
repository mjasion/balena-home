package metrics

import (
	"context"
	"fmt"
	"time"

	"github.com/mjasion/balena-home/pkg/types"
	"github.com/prometheus/prometheus/prompb"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// BuildBLETimeSeries builds Prometheus time series for BLE sensor readings
// This creates 3 metrics per sensor: temperature, humidity, battery
func BuildBLETimeSeries(ctx context.Context, readings []*types.Reading) ([]prompb.TimeSeries, error) {
	_, span := otel.Tracer("metrics").Start(ctx, "metrics.BuildBLETimeSeries")
	defer span.End()

	// Extract BLE readings
	var bleReadings []*types.BLEReading
	for _, r := range readings {
		if r.Type == types.ReadingTypeBLE && r.BLE != nil {
			bleReadings = append(bleReadings, r.BLE)
		}
	}

	if len(bleReadings) == 0 {
		span.SetStatus(codes.Ok, "no BLE readings")
		return nil, nil
	}

	// Group readings by sensor
	type sensorKey struct {
		name string
		id   int
		mac  string
	}
	sensorReadings := make(map[sensorKey][]*types.BLEReading)
	for _, reading := range bleReadings {
		key := sensorKey{name: reading.SensorName, id: reading.SensorID, mac: reading.MAC}
		sensorReadings[key] = append(sensorReadings[key], reading)
	}

	var timeSeries []prompb.TimeSeries

	// Create time series for each sensor
	for key, readings := range sensorReadings {
		// Common labels for this sensor
		labels := []prompb.Label{
			{Name: "sensor_name", Value: key.name},
			{Name: "sensor_id", Value: fmt.Sprintf("%d", key.id)},
			{Name: "mac", Value: key.mac},
		}

		// Temperature time series
		var tempSamples []prompb.Sample
		for _, r := range readings {
			tempSamples = append(tempSamples, prompb.Sample{
				Value:     r.TemperatureCelsius,
				Timestamp: r.Timestamp.UnixMilli(),
			})
		}
		if len(tempSamples) > 0 {
			timeSeries = append(timeSeries, prompb.TimeSeries{
				Labels: append(labels, prompb.Label{
					Name:  "__name__",
					Value: "ble_temperature_celsius",
				}),
				Samples: tempSamples,
			})
		}

		// Humidity time series
		var humSamples []prompb.Sample
		for _, r := range readings {
			humSamples = append(humSamples, prompb.Sample{
				Value:     float64(r.HumidityPercent),
				Timestamp: r.Timestamp.UnixMilli(),
			})
		}
		if len(humSamples) > 0 {
			timeSeries = append(timeSeries, prompb.TimeSeries{
				Labels: append(labels, prompb.Label{
					Name:  "__name__",
					Value: "ble_humidity_percent",
				}),
				Samples: humSamples,
			})
		}

		// Battery time series
		var battSamples []prompb.Sample
		for _, r := range readings {
			battSamples = append(battSamples, prompb.Sample{
				Value:     float64(r.BatteryPercent),
				Timestamp: r.Timestamp.UnixMilli(),
			})
		}
		if len(battSamples) > 0 {
			timeSeries = append(timeSeries, prompb.TimeSeries{
				Labels: append(labels, prompb.Label{
					Name:  "__name__",
					Value: "ble_battery_percent",
				}),
				Samples: battSamples,
			})
		}
	}

	span.SetAttributes(
		attribute.Int("metrics.ble_time_series_count", len(timeSeries)),
	)
	span.SetStatus(codes.Ok, "BLE time series built")

	return timeSeries, nil
}

// BuildThermostatTimeSeries builds Prometheus time series for Netatmo thermostat readings
func BuildThermostatTimeSeries(ctx context.Context, readings []*types.Reading) ([]prompb.TimeSeries, error) {
	_, span := otel.Tracer("metrics").Start(ctx, "metrics.BuildThermostatTimeSeries")
	defer span.End()

	// Extract thermostat readings
	var thermostatReadings []*types.ThermostatReading
	for _, r := range readings {
		if r.Type == types.ReadingTypeThermostat && r.Thermostat != nil {
			thermostatReadings = append(thermostatReadings, r.Thermostat)
		}
	}

	if len(thermostatReadings) == 0 {
		span.SetStatus(codes.Ok, "no thermostat readings")
		return nil, nil
	}

	// Group by room
	type roomKey struct {
		homeID   string
		homeName string
		roomID   string
		roomName string
	}
	roomReadings := make(map[roomKey][]*types.ThermostatReading)
	for _, reading := range thermostatReadings {
		key := roomKey{
			homeID:   reading.HomeID,
			homeName: reading.HomeName,
			roomID:   reading.RoomID,
			roomName: reading.RoomName,
		}
		roomReadings[key] = append(roomReadings[key], reading)
	}

	var timeSeries []prompb.TimeSeries

	// Create time series for each room
	for key, readings := range roomReadings {
		// Common labels for this room
		labels := []prompb.Label{
			{Name: "home_id", Value: key.homeID},
			{Name: "home_name", Value: key.homeName},
			{Name: "room_id", Value: key.roomID},
			{Name: "room_name", Value: key.roomName},
		}

		// Measured temperature
		var measuredSamples []prompb.Sample
		for _, r := range readings {
			measuredSamples = append(measuredSamples, prompb.Sample{
				Value:     r.MeasuredTemperature,
				Timestamp: roundToTenSeconds(r.Timestamp).UnixMilli(),
			})
		}
		if len(measuredSamples) > 0 {
			timeSeries = append(timeSeries, prompb.TimeSeries{
				Labels: append(labels, prompb.Label{
					Name:  "__name__",
					Value: "netatmo_measured_temperature_celsius",
				}),
				Samples: measuredSamples,
			})
		}

		// Setpoint temperature
		var setpointSamples []prompb.Sample
		for _, r := range readings {
			setpointSamples = append(setpointSamples, prompb.Sample{
				Value:     r.SetpointTemperature,
				Timestamp: roundToTenSeconds(r.Timestamp).UnixMilli(),
			})
		}
		if len(setpointSamples) > 0 {
			timeSeries = append(timeSeries, prompb.TimeSeries{
				Labels: append(labels, prompb.Label{
					Name:  "__name__",
					Value: "netatmo_setpoint_temperature_celsius",
				}),
				Samples: setpointSamples,
			})
		}

		// Heating power request
		var heatingPowerSamples []prompb.Sample
		for _, r := range readings {
			heatingPowerSamples = append(heatingPowerSamples, prompb.Sample{
				Value:     float64(r.HeatingPowerRequest),
				Timestamp: roundToTenSeconds(r.Timestamp).UnixMilli(),
			})
		}
		if len(heatingPowerSamples) > 0 {
			timeSeries = append(timeSeries, prompb.TimeSeries{
				Labels: append(labels, prompb.Label{
					Name:  "__name__",
					Value: "netatmo_heating_power_request_percent",
				}),
				Samples: heatingPowerSamples,
			})
		}
	}

	span.SetAttributes(
		attribute.Int("metrics.thermostat_time_series_count", len(timeSeries)),
	)
	span.SetStatus(codes.Ok, "thermostat time series built")

	return timeSeries, nil
}

// BuildMetricTimeSeries builds Prometheus time series for generic metric readings
func BuildMetricTimeSeries(ctx context.Context, readings []*types.Reading) ([]prompb.TimeSeries, error) {
	_, span := otel.Tracer("metrics").Start(ctx, "metrics.BuildMetricTimeSeries")
	defer span.End()

	// Extract metric readings
	var metricReadings []*types.MetricReading
	for _, r := range readings {
		if r.Type == types.ReadingTypeMetric && r.Metric != nil {
			metricReadings = append(metricReadings, r.Metric)
		}
	}

	if len(metricReadings) == 0 {
		span.SetStatus(codes.Ok, "no metric readings")
		return nil, nil
	}

	// Group by metric name and labels
	type metricKey struct {
		name   string
		labels string // Serialized labels for grouping
	}
	groupedReadings := make(map[metricKey][]*types.MetricReading)
	for _, reading := range metricReadings {
		key := metricKey{
			name:   reading.Name,
			labels: serializeLabels(reading.Labels),
		}
		groupedReadings[key] = append(groupedReadings[key], reading)
	}

	var timeSeries []prompb.TimeSeries

	// Create time series for each metric
	for key, readings := range groupedReadings {
		// Build labels
		labels := []prompb.Label{
			{Name: "__name__", Value: key.name},
		}

		// Add custom labels from first reading (all should be the same)
		if len(readings) > 0 && readings[0].Labels != nil {
			for k, v := range readings[0].Labels {
				labels = append(labels, prompb.Label{
					Name:  k,
					Value: v,
				})
			}
		}

		// Build samples
		var samples []prompb.Sample
		for _, r := range readings {
			samples = append(samples, prompb.Sample{
				Value:     r.Value,
				Timestamp: r.Timestamp.UnixMilli(),
			})
		}

		if len(samples) > 0 {
			timeSeries = append(timeSeries, prompb.TimeSeries{
				Labels:  labels,
				Samples: samples,
			})
		}
	}

	span.SetAttributes(
		attribute.Int("metrics.generic_time_series_count", len(timeSeries)),
	)
	span.SetStatus(codes.Ok, "generic time series built")

	return timeSeries, nil
}

// CombineBuilders combines multiple time series builders into one
func CombineBuilders(builders ...TimeSeriesBuilder) TimeSeriesBuilder {
	return func(ctx context.Context, readings []*types.Reading) ([]prompb.TimeSeries, error) {
		var allTimeSeries []prompb.TimeSeries

		for _, builder := range builders {
			if builder == nil {
				continue
			}

			timeSeries, err := builder(ctx, readings)
			if err != nil {
				return nil, err
			}

			allTimeSeries = append(allTimeSeries, timeSeries...)
		}

		return allTimeSeries, nil
	}
}

// Helper functions

func roundToTenSeconds(t time.Time) time.Time {
	seconds := t.Second()
	remainder := seconds % 10

	if remainder < 5 {
		// Round down
		return t.Add(-time.Duration(remainder) * time.Second).Truncate(time.Second)
	}
	// Round up
	return t.Add(time.Duration(10-remainder) * time.Second).Truncate(time.Second)
}

func serializeLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}

	result := ""
	for k, v := range labels {
		result += k + "=" + v + ","
	}
	return result
}
