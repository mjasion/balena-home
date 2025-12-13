package control

import (
	"fmt"
	"math"
	"time"
)

// AlgorithmResult contains the result of the three-zone algorithm
type AlgorithmResult struct {
	CalculatedSetpoint float64
	Zone               string
	Adjustment         float64
}

// calculateThreeZoneSetpoint implements the three-zone temperature control algorithm
// Uses sensor offset (difference between Xiaomi and Netatmo) to determine zones
// Returns the calculated setpoint, zone name, and adjustment amount
func calculateThreeZoneSetpoint(xiaomiTemp, scheduledTemp, thermostatMeasured, threshold float64) AlgorithmResult {
	// Calculate sensor offset: difference between accurate Xiaomi and less accurate Netatmo
	sensorOffset := xiaomiTemp - thermostatMeasured

	switch {
	case sensorOffset <= -threshold:
		// Zone 1: Xiaomi reads colder than Netatmo - add 0.5°C to trigger heating
		return AlgorithmResult{
			CalculatedSetpoint: thermostatMeasured + 0.5,
			Zone:               "too_cold",
			Adjustment:         0.5,
		}

	case sensorOffset >= threshold:
		// Zone 3: Xiaomi reads warmer than Netatmo - subtract 0.5°C to stop heating
		return AlgorithmResult{
			CalculatedSetpoint: thermostatMeasured - 0.5,
			Zone:               "too_warm",
			Adjustment:         -0.5,
		}

	default:
		// Zone 2: Sensors agree within threshold - maintain current reading
		return AlgorithmResult{
			CalculatedSetpoint: thermostatMeasured,
			Zone:               "within_range",
			Adjustment:         0.0,
		}
	}
}

// roundToHalfDegree rounds a temperature to the nearest 0.5°C
// Netatmo thermostats only accept setpoints in 0.5°C increments
func roundToHalfDegree(temp float64) float64 {
	return math.Round(temp*2.0) / 2.0
}

// applySafetyBounds applies safety limits to the calculated setpoint
func applySafetyBounds(setpoint float64) float64 {
	const minSetpoint = 7.0
	const maxSetpoint = 30.0

	if setpoint < minSetpoint {
		return minSetpoint
	} else if setpoint > maxSetpoint {
		return maxSetpoint
	}
	return setpoint
}

// applyConfiguredSafetyLimits applies user-configured safety limits
// Returns the safe setpoint and whether it was clamped
func applyConfiguredSafetyLimits(setpoint, minLimit, maxLimit float64) (float64, bool) {
	clamped := false
	safeSetpoint := setpoint

	if safeSetpoint < minLimit {
		safeSetpoint = minLimit
		clamped = true
	} else if safeSetpoint > maxLimit {
		safeSetpoint = maxLimit
		clamped = true
	}

	return safeSetpoint, clamped
}

// needsAdjustment checks if the setpoint needs adjustment
// Returns true if the difference between current and calculated exceeds threshold
func needsAdjustment(currentSetpoint, calculatedSetpoint float64) bool {
	return math.Abs(calculatedSetpoint-currentSetpoint) >= 0.1
}

// calculateBoundaryAlignedEndTime returns the next 15-minute boundary minus 1 second
// For example: 12:00:03 → 12:14:59, 12:15:30 → 12:29:59
func calculateBoundaryAlignedEndTime() time.Time {
	now := time.Now()
	minute := now.Minute()

	// Determine which boundary this falls into
	var nextBoundary int
	if minute < 15 {
		nextBoundary = 15
	} else if minute < 30 {
		nextBoundary = 30
	} else if minute < 45 {
		nextBoundary = 45
	} else {
		nextBoundary = 0 // Next hour
	}

	// Calculate end time: next boundary minus 1 second (e.g., 14:59:59)
	if nextBoundary == 0 {
		// Next hour
		return now.Add(time.Hour).Truncate(time.Hour).Add(-1 * time.Second)
	}

	// Same hour
	endTime := now.Truncate(time.Hour).Add(time.Duration(nextBoundary) * time.Minute).Add(-1 * time.Second)
	return endTime
}

// buildDecisionReason creates a human-readable reason string for the decision
func buildDecisionReason(xiaomiTemp, scheduledTemp, thermostatMeasured, calculatedSetpoint float64, hardOverrideActive bool) string {
	reason := fmt.Sprintf("xiaomi=%.1f°C, target=%.1f°C, measured=%.1f°C, setpoint=%.1f°C",
		xiaomiTemp, scheduledTemp, thermostatMeasured, calculatedSetpoint)

	if hardOverrideActive {
		reason += ", hard override"
	}

	return reason
}

// calculateOverrideDuration calculates the duration until override end time
func calculateOverrideDuration(endTime int64) time.Duration {
	return time.Until(time.Unix(endTime, 0))
}

// calculateOverrideDurationMinutes calculates override duration in minutes for API
func calculateOverrideDurationMinutes(endTime int64) int64 {
	durationMinutes := int64((time.Unix(endTime, 0).Sub(time.Now())).Minutes())
	if durationMinutes <= 0 {
		durationMinutes = 1 // Minimum 1 minute
	}
	return durationMinutes
}
