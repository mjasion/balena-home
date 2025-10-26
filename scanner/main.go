package main

import (
	"fmt"
	"strings"
	"time"

	"tinygo.org/x/bluetooth"
)

var adapter = bluetooth.DefaultAdapter

var targetMACs = map[string]bool{
	"A4:C1:38:ED:C0:21": true,
	"A4:C1:38:26:E2:4C": true,
}

func main() {
	// Enable BLE interface.
	must("enable BLE stack", adapter.Enable())

	// Track seen devices to print only unique ones.
	seenDevices := make(map[string]bool)

	// Start scanning.
	fmt.Println(strings.Repeat("=", 80))
	fmt.Println("BLE Scanner Started")
	fmt.Println("Filtering for:")
	fmt.Println("  - Devices with 'ATC' or 'LYWSD03MMC' in name")
	fmt.Println("  - MAC: A4:C1:38:ED:C0:21")
	fmt.Println("  - MAC: A4:C1:38:26:E2:4C")
	fmt.Println(strings.Repeat("=", 80))
	fmt.Println()

	err := adapter.Scan(func(adapter *bluetooth.Adapter, device bluetooth.ScanResult) {
		address := device.Address.String()
		name := device.LocalName()

		// Filter: only process if name contains "ATC" or "LYWSD03MMC" or MAC is in target list
		nameUpper := strings.ToUpper(name)
		nameMatch := strings.Contains(nameUpper, "ATC") || strings.Contains(nameUpper, "LYWSD03MMC")
		macMatch := targetMACs[address]

		if nameMatch || macMatch {
			if !seenDevices[address] {
				seenDevices[address] = true
				printDevice(device, address, name, nameMatch, macMatch)
			}
		}
	})
	must("start scan", err)
}

func printDevice(device bluetooth.ScanResult, address, name string, nameMatch, macMatch bool) {
	timestamp := time.Now().Format("2006-01-02 15:04:05")

	fmt.Println("┌─────────────────────────────────────────────────────────")
	fmt.Printf("│ Timestamp: %s\n", timestamp)
	fmt.Printf("│ Device:    %s\n", name)
	fmt.Printf("│ MAC:       %s", address)
	if macMatch {
		fmt.Print(" ✓ (target)")
	}
	fmt.Println()

	// Signal strength with visual indicator
	rssi := device.RSSI
	strength := getSignalStrength(rssi)
	fmt.Printf("│ RSSI:      %d dBm [%s] %s\n", rssi, strength.bar, strength.label)

	// Match reason
	fmt.Print("│ Matched:   ")
	if nameMatch && macMatch {
		fmt.Println("Name pattern + Target MAC")
	} else if nameMatch {
		fmt.Println("Name contains 'ATC' or 'LYWSD03MMC'")
	} else if macMatch {
		fmt.Println("Target MAC address")
	}

	fmt.Println("└─────────────────────────────────────────────────────────")
	fmt.Println()
}

type signalStrength struct {
	bar   string
	label string
}

func getSignalStrength(rssi int16) signalStrength {
	// RSSI typically ranges from -100 (weak) to -30 (strong)
	switch {
	case rssi >= -50:
		return signalStrength{"████████", "Excellent"}
	case rssi >= -60:
		return signalStrength{"██████  ", "Good"}
	case rssi >= -70:
		return signalStrength{"████    ", "Fair"}
	case rssi >= -80:
		return signalStrength{"██      ", "Weak"}
	default:
		return signalStrength{"        ", "Very Weak"}
	}
}

func must(action string, err error) {
	if err != nil {
		panic("failed to " + action + ": " + err.Error())
	}
}
