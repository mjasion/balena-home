package scanner

import (
	"context"
	"fmt"
	"strings"

	"github.com/mjasion/balena-home/pkg/buffer"
	"github.com/mjasion/balena-home/pkg/types"
	"github.com/mjasion/balena-home/thermostats/decoder"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"tinygo.org/x/bluetooth"
)

// UUID 0x181A is used by ATC_MiThermometer firmware
var serviceUUID = bluetooth.New16BitUUID(0x181A)

// SensorInfo contains metadata about a sensor
type SensorInfo struct {
	Name string
	ID   int
}

// SensorConfig represents configuration for a single sensor
type SensorConfig struct {
	Name       string
	ID         int
	MACAddress string
}

// Scanner handles BLE scanning for temperature sensors
type Scanner struct {
	adapter    *bluetooth.Adapter
	sensorMACs map[string]SensorInfo // Map of MAC address to sensor info
	buffer     *buffer.RingBuffer[*types.Reading]
	logger     *zap.Logger
}

// New creates a new BLE scanner
func New(sensors []SensorConfig, buf *buffer.RingBuffer[*types.Reading], logger *zap.Logger) *Scanner {
	// Convert sensor list to map for fast lookup
	macMap := make(map[string]SensorInfo)
	for _, sensor := range sensors {
		// Normalize to uppercase for comparison
		mac := strings.ToUpper(strings.TrimSpace(sensor.MACAddress))
		macMap[mac] = SensorInfo{
			Name: sensor.Name,
			ID:   sensor.ID,
		}
	}

	return &Scanner{
		adapter:    bluetooth.DefaultAdapter,
		sensorMACs: macMap,
		buffer:     buf,
		logger:     logger,
	}
}

// Start initializes the BLE adapter and starts scanning
func (s *Scanner) Start(ctx context.Context) error {
	tracer := otel.Tracer("scanner")
	ctx, span := tracer.Start(ctx, "scanner.Start",
		trace.WithAttributes(
			attribute.Int("scanner.sensor_count", len(s.sensorMACs)),
		),
	)
	defer span.End()

	s.logger.Info("initializing BLE adapter")

	// Enable the BLE stack
	err := s.adapter.Enable()
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to enable BLE adapter")
		return fmt.Errorf("failed to enable BLE adapter: %w", err)
	}

	span.AddEvent("BLE adapter enabled")
	s.logger.Info("BLE adapter initialized successfully")
	s.logger.Info("starting BLE scan", zap.Int("sensor_count", len(s.sensorMACs)), zap.Any("sensors", s.sensorMACs))

	// Start scanning
	err = s.adapter.Scan(func(adapter *bluetooth.Adapter, result bluetooth.ScanResult) {
		// Check if context is cancelled
		select {
		case <-ctx.Done():
			// Stop scanning
			s.adapter.StopScan()
			return
		default:
		}

		// Filter by configured sensor MAC addresses
		mac := strings.ToUpper(result.Address.String())
		sensorInfo, found := s.sensorMACs[mac]
		if !found {
			return
		}

		// Create a span for this sensor reading
		_, readingSpan := tracer.Start(ctx, "scanner.ProcessAdvertisement",
			trace.WithAttributes(
				attribute.String("ble.mac", mac),
				attribute.String("ble.sensor_name", sensorInfo.Name),
				attribute.Int("ble.sensor_id", sensorInfo.ID),
			),
		)
		defer readingSpan.End()

		s.logger.Debug("BLE scan",
			zap.String("mac", mac),
			zap.String("sensor_name", sensorInfo.Name),
			zap.Int("sensor_id", sensorInfo.ID),
			zap.Any("result", result.ServiceData()))

		// Look for service data with UUID 0x181A
		serviceData := result.ServiceData()
		for _, sd := range serviceData {
			if sd.UUID == serviceUUID {
				// Decode ATC advertisement
				reading, err := decoder.DecodeATCAdvertisement(sd.Data, result.RSSI)
				if err != nil {
					s.logger.Warn("failed to decode ATC advertisement",
						zap.String("mac", mac),
						zap.Error(err),
					)
					readingSpan.RecordError(err)
					readingSpan.SetStatus(codes.Error, "failed to decode")
					continue
				}

				// Add to buffer
				bufReading := &types.Reading{
					Type: types.ReadingTypeBLE,
					BLE: &types.BLEReading{
						Timestamp:          reading.Timestamp,
						MAC:                reading.MAC,
						SensorName:         sensorInfo.Name,
						SensorID:           sensorInfo.ID,
						TemperatureCelsius: reading.TemperatureCelsius,
						HumidityPercent:    reading.HumidityPercent,
						BatteryPercent:     reading.BatteryPercent,
						BatteryVoltageMV:   reading.BatteryVoltageMV,
						FrameCounter:       reading.FrameCounter,
						RSSI:               reading.RSSI,
					},
				}
				s.buffer.Add(bufReading)

				// Add sensor reading attributes to span
				readingSpan.SetAttributes(
					attribute.Float64("ble.temperature_celsius", reading.TemperatureCelsius),
					attribute.Int("ble.humidity_percent", reading.HumidityPercent),
					attribute.Int("ble.battery_percent", reading.BatteryPercent),
					attribute.Int("ble.rssi_dbm", int(reading.RSSI)),
				)
				readingSpan.SetStatus(codes.Ok, "sensor reading processed")

				// Log sensor reading
				s.logger.Info("Read sensor data",
					zap.String("sensor_name", sensorInfo.Name),
					zap.Int("sensor_id", sensorInfo.ID),
					zap.String("mac", reading.MAC),
					zap.Float64("temperature_celsius", reading.TemperatureCelsius),
					zap.Int("humidity_percent", reading.HumidityPercent),
					zap.Int("battery_percent", reading.BatteryPercent),
					zap.Int("battery_voltage_mv", reading.BatteryVoltageMV),
					zap.Int16("rssi_dbm", reading.RSSI),
				)
			}
		}
	})

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to start BLE scan")
		return fmt.Errorf("failed to start BLE scan: %w", err)
	}

	span.SetStatus(codes.Ok, "BLE scan started")
	return nil
}

// Stop stops the BLE scanner
func (s *Scanner) Stop() error {
	s.logger.Info("stopping BLE scan")
	err := s.adapter.StopScan()
	if err != nil {
		return fmt.Errorf("failed to stop BLE scan: %w", err)
	}
	return nil
}
