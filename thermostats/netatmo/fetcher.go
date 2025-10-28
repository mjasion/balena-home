package netatmo

import (
	"context"
	"fmt"
	"time"
)

// Fetcher fetches thermostat data from Netatmo API
type Fetcher struct {
	client *Client
}

// NewFetcher creates a new Netatmo data fetcher
func NewFetcher(clientID, clientSecret, refreshToken string) *Fetcher {
	return &Fetcher{
		client: NewClient(clientID, clientSecret, refreshToken),
	}
}

// FetchAllThermostats fetches thermostat data from all homes and rooms
func (f *Fetcher) FetchAllThermostats(ctx context.Context) ([]ThermostatReading, error) {
	// First, get homes data to know the topology
	homesData, err := f.client.GetHomesData(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get homes data: %w", err)
	}

	if homesData.Status != "ok" {
		return nil, fmt.Errorf("homes data request returned status: %s", homesData.Status)
	}

	var readings []ThermostatReading

	// For each home, get the current status
	for _, home := range homesData.Body.Homes {
		homeStatus, err := f.client.GetHomeStatus(ctx, home.ID)
		if err != nil {
			return nil, fmt.Errorf("failed to get status for home %s: %w", home.Name, err)
		}

		if homeStatus.Status != "ok" {
			return nil, fmt.Errorf("home status request for %s returned status: %s", home.Name, homeStatus.Status)
		}

		// Create a map of room ID to room name from topology
		roomNames := make(map[string]string)
		for _, room := range home.Rooms {
			roomNames[room.ID] = room.Name
		}

		// Process each room's thermostat data
		timestamp := time.Now().Unix()
		for _, roomStatus := range homeStatus.Body.Home.Rooms {
			roomName, ok := roomNames[roomStatus.ID]
			if !ok {
				roomName = roomStatus.ID // Fallback to ID if name not found
			}

			reading := ThermostatReading{
				Timestamp:           timestamp,
				HomeID:              home.ID,
				HomeName:            home.Name,
				RoomID:              roomStatus.ID,
				RoomName:            roomName,
				MeasuredTemperature: roomStatus.ThermMeasuredTemperature,
				SetpointTemperature: roomStatus.ThermSetpointTemperature,
				SetpointMode:        roomStatus.ThermSetpointMode,
				HeatingPowerRequest: roomStatus.HeatingPowerRequest,
				OpenWindow:          roomStatus.OpenWindow,
				Reachable:           roomStatus.Reachable,
			}

			readings = append(readings, reading)
		}
	}

	return readings, nil
}
