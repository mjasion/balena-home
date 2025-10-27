package scanner

import (
	"context"
	"fmt"
	"strings"

	"github.com/mjasion/balena-home/thermostats/buffer"
	"github.com/mjasion/balena-home/thermostats/decoder"
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
	adapter     *bluetooth.Adapter
	sensorMACs  map[string]SensorInfo // Map of MAC address to sensor info
	buffer      *buffer.RingBuffer
	logger      *zap.Logger
}

// New creates a new BLE scanner
func New(sensors []SensorConfig, buf *buffer.RingBuffer, logger *zap.Logger) *Scanner {
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
		adapter:     bluetooth.DefaultAdapter,
		sensorMACs:  macMap,
		buffer:      buf,
		logger:      logger,
	}
}

// Start initializes the BLE adapter and starts scanning
func (s *Scanner) Start(ctx context.Context) error {
	s.logger.Info("initializing BLE adapter")

	// Enable the BLE stack
	err := s.adapter.Enable()
	if err != nil {
		return fmt.Errorf("failed to enable BLE adapter: %w", err)
	}

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

		// Get MAC address and normalize
		mac := strings.ToUpper(result.Address.String())
		// Filter by configured sensor MAC addresses
		sensorInfo, found := s.sensorMACs[mac]
		if !found {
			return
		}
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
					continue
				}

				// Add to buffer
				bufReading := &buffer.SensorReading{
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
				}
				s.buffer.Add(bufReading)

				// Log sensor reading
				s.logger.Info("sensor_reading",
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
		return fmt.Errorf("failed to start BLE scan: %w", err)
	}

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
