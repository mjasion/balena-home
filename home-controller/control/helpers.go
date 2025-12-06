package control

import (
	"time"

	"go.uber.org/zap"
)

// markExternallyModified marks a thermostat as externally modified
func (c *Controller) markExternallyModified(roomID string) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()

	if state, exists := c.stateByRoom[roomID]; exists {
		state.ExternallyModified = true
		state.ExternalModificationTime = time.Now()
	}
}

// clearExternalModification clears the external modification flag
func (c *Controller) clearExternalModification(roomID string) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()

	if state, exists := c.stateByRoom[roomID]; exists {
		state.ExternallyModified = false
		state.ExternalModificationTime = time.Time{}
	}
}

// warsawLocation returns the Europe/Warsaw timezone location
func (c *Controller) warsawLocation() *time.Location {
	loc, err := time.LoadLocation("Europe/Warsaw")
	if err != nil {
		// Fallback to UTC if Warsaw timezone cannot be loaded
		c.logger.Warn("failed to load Europe/Warsaw timezone, using UTC", zap.Error(err))
		return time.UTC
	}
	return loc
}

// getMapKeys returns the keys of a map as a slice (helper function)
func getMapKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
