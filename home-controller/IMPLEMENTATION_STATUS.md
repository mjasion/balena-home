# Refactor Control Algorithm Implementation Status

## Completed (5/10 Tasks)

✅ **Task 1: Simplified State Structure** (types.go)
- Removed: LastSetpoint, PendingSetpoint, SyncedScheduledTemp, ConsecutiveIncreases, RunawayHaltUntil, ScheduleJustChanged
- Kept minimal: RoomID, RoomName, LastHomeMode, ExternallyModified, ExternalModificationTime
- Updated Copy() method

✅ **Task 2: Channel Communication** (controller.go)
- Added `homeStatusChan <-chan *netatmo.HomeStatusResponse` field
- Updated Constructor to accept channel
- Added ReceiveHomeStatus() method to block-read from channel

✅ **Task 3: Metric Job** (home_status_fetcher.go)
- Renamed HomeStatusFetcher to MetricJob
- Now sends HomeStatusResponse to channel instead of caching
- Removed GetCachedHomeStatus() and related caching code
- Kept metrics buffer push for Prometheus
- Added backwards compatibility with type alias

✅ **Task 4: Simplified Three-Zone Algorithm** (evaluate.go - COMPLETE REWRITE)
- Implemented three-zone algorithm:
  - Too cold (diff <= -threshold): setpoint = measured + 0.5°C
  - Within range (-threshold < diff < threshold): setpoint = measured
  - Too warm (diff >= threshold): setpoint = measured - 0.5°C
- Removed: offset-based calculation, checkRunawayProtection(), checkDelayedExecution(), shouldExtendOverride()
- Added: calculateBoundaryAlignedEndTime() returning :14:59, :29:59, :44:59, :59:59
- Simplified determineScheduledTemp() to just return current setpoint (or hard override if active)

✅ **Task 5: Duration-Based Override Detection** (mode_detection.go - COMPLETE REWRITE)
- Implemented isHumanOverride(): returns (end_time - start_time >= 60 minutes)
- Simplified shouldControlRoom() with duration-based check
- Updated detectHomeModeChange() with simplified logic
- Removed complex external modification detection
- Kept mode change tracking for away/hg transitions

## In Progress / Next Steps (5/10 Tasks)

❌ **Task 6: Refactor Control Job for Concurrent Processing** (controller.go - runControlLoop)

Current state: Calls GetCachedHomeStatus() which no longer exists

Changes needed:
1. Replace GetCachedHomeStatus() with ReceiveHomeStatus(ctx)
2. Remove needsSync check and runSyncMode() call (schedule sync removed)
3. Only use runNormalMode() flow
4. Implement concurrent per-room processing:
   ```go
   // Pseudocode
   homeStatus := c.ReceiveHomeStatus(ctx)
   var wg sync.WaitGroup
   for _, mapping := range c.config.Mappings {
     wg.Add(1)
     go func(m config.ThermostatMapping) {
       defer wg.Done()
       roomStatus := homeStatus.Body.Home.Rooms[...]

       // Check if waiting for manual mode expiration
       if roomStatus.ThermSetpointMode == "manual" && overrideExpiresWithinWindow(roomStatus) {
         waitUntilExpiration(roomStatus.ThermSetpointEndTime)
         // Make own API call to get fresh data
         freshStatus, _ := c.netatmoClient.GetHomeStatus(ctx, c.homeID)
         roomStatus = freshStatus.Body.Home.Rooms[...]
       }

       decision := c.evaluateRoom(ctx, m, map[string]*RoomStatus{m.RoomID: roomStatus})
       c.executeDecision(ctx, decision)
     }(mapping)
   }
   wg.Wait()
   ```

❌ **Task 7: Create Hard Override Job** (control/hard_override_job.go - NEW FILE)

File structure:
```go
type HardOverrideJob struct {
  controller *Controller
  logger *zap.Logger
  tracer trace.Tracer
}

func NewHardOverrideJob(...) *HardOverrideJob
func (h *HardOverrideJob) Run(ctx context.Context)
```

Logic:
- Check if hard override active for current time
- Fetch home status
- For each room with active hard override:
  - Skip if human override active (duration >= 60 min)
  - Use same three-zone algorithm as Control Job but with override target temp
  - Set override with boundary-aligned end time
  - Skip if already correct setpoint

❌ **Task 8: Remove Deprecated Code**

Files to delete:
- control/sync.go (entire schedule sync logic)

Files to clean:
- control/sync_timing_test.go
- control/sync_hybrid_test.go
- Any test files referencing removed functions

❌ **Task 9: Update Configuration** (config/config.go)

Changes needed:
1. Change TemperatureThreshold default from 0.5 to 0.2
2. Update cron field names:
   - HomeStatusFetchCron → MetricJobCron (default: "0 * * * * *")
   - ControlLoopCron → ControlJobCron (default: "0 0,15,30,45 * * * *")
3. Add: HardOverrideJobCron (default: "0 * * * * *")
4. Remove schedule sync fields:
   - ScheduleSyncIntervalMinutes
   - ScheduleSyncPollIntervalSeconds
   - ScheduleSyncPollTimeoutSeconds
5. Update Validate() method to remove schedule sync validation

❌ **Task 10: Run Tests and Fix**

Commands:
```bash
go test ./... -v
go test ./... -race  # Race detector
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

Expected failures:
- Tests referencing removed sync functions
- Tests using old caching mechanism
- Tests expecting old state fields
- Tests with wrong cron defaults

## Key Implementation Notes

### Controller Constructor Changes
The Controller.New() now requires a buffered channel:
```go
// In main.go or initialization code:
homeStatusChan := make(chan *netatmo.HomeStatusResponse, 1)
controller := control.New(cfg, client, controlBuf, metricsBuf, logger, homeStatusChan)
metricJob := control.NewMetricJob(controller, logger, tracer, homeStatusChan)
```

### Job Scheduling
Three jobs need to be scheduled:
1. **Metric Job** - Every minute: `metricJob.Run(ctx)`
2. **Control Job** - Every 15 minutes at :00/:15/:30/:45: `controller.Run(ctx)`
3. **Hard Override Job** - Every minute: `hardOverrideJob.Run(ctx)`

### Per-Room Waiting Logic
When Control Job encounters a room in manual mode with algorithm-set override (< 15 min):
1. Calculate wait time: `override_end_time - now`
2. If expires within window and > 0 time: wait and make fresh API call
3. Process with fresh data
4. If expires outside window (> 15 min): skip this room

### Removed Features
These safety mechanisms are gone - the 15-minute timing provides natural rate limiting:
- Delayed execution (pending setpoint confirmation)
- Schedule sync (brief switch to schedule mode to read scheduled temp)
- Runaway protection (3+ consecutive increases detection)
- Override extension logic
- Complex state tracking

## Testing Checklist

- [ ] Three-zone algorithm produces correct setpoints
- [ ] Boundary-aligned times: 12:00 → 12:14:59, 12:15 → 12:29:59, etc.
- [ ] Human override detection: duration >= 3600 seconds (60 min)
- [ ] Per-room concurrent processing works without race conditions
- [ ] Hard override job respects time windows
- [ ] Control job waits for Metric Job data without deadlock
- [ ] Configuration validation passes with new schema
- [ ] All tests pass with `-race` flag
- [ ] Manual testing in dry-run mode
