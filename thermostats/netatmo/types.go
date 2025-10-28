package netatmo

// HomesDataResponse represents the response from /api/homesdata
type HomesDataResponse struct {
	Status string `json:"status"`
	Body   struct {
		Homes []Home `json:"homes"`
		User  User   `json:"user"`
	} `json:"body"`
	TimeExec   float64 `json:"time_exec"`
	TimeServer int64   `json:"time_server"`
}

// HomeStatusResponse represents the response from /api/homestatus
type HomeStatusResponse struct {
	Status string `json:"status"`
	Body   struct {
		Home HomeStatus `json:"home"`
	} `json:"body"`
	TimeExec   float64 `json:"time_exec"`
	TimeServer int64   `json:"time_server"`
}

// Home represents a home's topology and configuration
type Home struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Modules []Module `json:"modules"`
	Rooms   []Room   `json:"rooms"`
}

// HomeStatus represents the current status of a home
type HomeStatus struct {
	ID      string       `json:"id"`
	Modules []ModuleData `json:"modules"`
	Rooms   []RoomStatus `json:"rooms"`
}

// Module represents a Netatmo module (thermostat, valve, etc.)
type Module struct {
	ID            string `json:"id"`
	Type          string `json:"type"`
	Name          string `json:"name"`
	SetupDate     int64  `json:"setup_date"`
	RoomID        string `json:"room_id"`
	BridgeID      string `json:"bridge,omitempty"`
	ModuleBridged []string `json:"modules_bridged,omitempty"`
}

// ModuleData represents the current data from a module
type ModuleData struct {
	ID                  string  `json:"id"`
	Type                string  `json:"type"`
	Reachable           bool    `json:"reachable"`
	FirmwareRevision    int     `json:"firmware_revision,omitempty"`
	RFStatus            int     `json:"rf_status,omitempty"`
	BatteryPercent      int     `json:"battery_percent,omitempty"`
	BatteryState        string  `json:"battery_state,omitempty"`
	ThermMeasuredTemperature float64 `json:"therm_measured_temperature,omitempty"`
	ThermSetpointTemperature float64 `json:"therm_setpoint_temperature,omitempty"`
}

// Room represents a room in a home
type Room struct {
	ID                string  `json:"id"`
	Name              string  `json:"name"`
	Type              string  `json:"type"`
	ModuleIDs         []string `json:"module_ids"`
	MeasureOffsetNAPlug float64 `json:"measure_offset_NAPlug,omitempty"`
}

// RoomStatus represents the current status of a room
type RoomStatus struct {
	ID                       string  `json:"id"`
	Reachable                bool    `json:"reachable"`
	ThermMeasuredTemperature float64 `json:"therm_measured_temperature"`
	ThermSetpointTemperature float64 `json:"therm_setpoint_temperature"`
	ThermSetpointMode        string  `json:"therm_setpoint_mode"`
	ThermSetpointStartTime   int64   `json:"therm_setpoint_start_time,omitempty"`
	ThermSetpointEndTime     int64   `json:"therm_setpoint_end_time,omitempty"`
	AnticipatingReachable    bool    `json:"anticipating,omitempty"`
	OpenWindow               bool    `json:"open_window,omitempty"`
	HeatingPowerRequest      int     `json:"heating_power_request,omitempty"`
}

// User represents the Netatmo user
type User struct {
	Email          string `json:"email"`
	Language       string `json:"language"`
	Locale         string `json:"locale"`
	FeelLikeAlgo   int    `json:"feel_like_algo"`
	UnitPressure   int    `json:"unit_pressure"`
	UnitSystem     int    `json:"unit_system"`
	UnitWind       int    `json:"unit_wind"`
	Administrative struct {
		Lang         string `json:"lang"`
		RegLocale    string `json:"reg_locale"`
		Country      string `json:"country"`
		Unit         int    `json:"unit"`
		Windunit     int    `json:"windunit"`
		Pressureunit int    `json:"pressureunit"`
		FeelLikeAlgo int    `json:"feel_like_algo"`
	} `json:"administrative"`
}

// ThermostatReading represents a thermostat reading with measured and setpoint temperatures
type ThermostatReading struct {
	Timestamp            int64   // Unix timestamp
	HomeID               string
	HomeName             string
	RoomID               string
	RoomName             string
	MeasuredTemperature  float64
	SetpointTemperature  float64
	SetpointMode         string
	HeatingPowerRequest  int
	OpenWindow           bool
	Reachable            bool
}
