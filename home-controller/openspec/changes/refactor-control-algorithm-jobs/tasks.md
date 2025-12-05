## 1. Preparation

- [ ] 1.1 Create feature branch `refactor-control-algorithm-jobs`
- [ ] 1.2 Review existing test coverage to understand what needs updating

## 2. Simplify State Structure

- [ ] 2.1 Update `control/types.go`: Remove fields from ThermostatState
  - Remove: `LastSetpoint`, `LastSetpointTime`, `OverrideEndTime`
  - Remove: `SyncedScheduledTemp`, `SyncedScheduledTime`
  - Remove: `LastManualSetpoint`, `LastManualEndTime`
  - Remove: `ConsecutiveIncreases`, `LastCalculatedSetpoint`, `RunawayHaltUntil`
  - Remove: `PendingSetpoint`, `PendingSetpointTime`, `ScheduleJustChanged`
  - Keep: `RoomID`, `RoomName`, `ExternallyModified`, `ExternalModificationTime`, `LastHomeMode`
- [ ] 2.2 Update `Copy()` method to match simplified struct
- [ ] 2.3 Update tests in `control/types_test.go` if exists

## 3. Implement Channel Communication

- [ ] 3.1 Add `homeStatusChannel chan *netatmo.HomeStatusResponse` to Controller struct
- [ ] 3.2 Update Controller constructor to create buffered channel (size 1)
- [ ] 3.3 Create `SendHomeStatus(status *netatmo.HomeStatusResponse)` method for Metric Job
- [ ] 3.4 Create `ReceiveHomeStatus() *netatmo.HomeStatusResponse` method for Control Job

## 4. Refactor Metric Job

- [ ] 4.1 Update `control/home_status_fetcher.go`:
  - Rename to `metric_job.go` for clarity
  - After fetching, send to channel instead of caching
  - Keep metrics buffer push for Prometheus
- [ ] 4.2 Update cron configuration to use `metricJobCron` (default: `"0 * * * * *"`)
- [ ] 4.3 Remove cached status methods (`GetCachedHomeStatus`, `cachedStatusMu`, `cachedStatus`)
- [ ] 4.4 Write tests for channel communication

## 5. Implement Simplified Algorithm

- [ ] 5.1 Update `control/evaluate.go`:
  - Implement three-zone setpoint calculation:
    - diff <= -threshold: `netatmo_measured + 0.5`
    - -threshold < diff < threshold: `netatmo_measured`
    - diff >= threshold: `netatmo_measured - 0.5`
  - Remove offset-based calculation
  - Remove `checkRunawayProtection()` function
  - Remove `checkDelayedExecution()` function
  - Remove `shouldExtendOverride()` function
- [ ] 5.2 Update `applySafetyBounds()` to keep only 7-30°C limits
- [ ] 5.3 Update tests in `control/controller_setpoint_test.go`

## 6. Implement Duration-Based Override Detection

- [ ] 6.1 Update `control/mode_detection.go`:
  - Create `isHumanOverride(roomStatus *netatmo.RoomStatus) bool` function
    - Calculate: `end_time - start_time >= 60 minutes`
  - Remove `detectExternalManualChange()` function
  - Remove `detectExternalModification()` function
  - Simplify `shouldControlRoom()` to use duration-based detection
- [ ] 6.2 Update tests in `control/mode_detection_test.go`

## 7. Implement Boundary-Aligned Override Timing

- [ ] 7.1 Create helper function `calculateOverrideEndTime() time.Time`:
  - Return next :14:59, :29:59, :44:59, or :59:59
- [ ] 7.2 Update `control/execute.go` to use boundary-aligned end times
- [ ] 7.3 Write tests for boundary calculation edge cases

## 8. Implement Non-Blocking Per-Room Processing

- [ ] 8.1 Update `control/controller.go`:
  - Replace `evaluateAndExecuteRooms()` with concurrent version
  - Use `sync.WaitGroup` to coordinate goroutines
  - Each room goroutine handles its own waiting logic
- [ ] 8.2 Create `processRoomAsync()` function:
  - If schedule mode: process immediately with shared data
  - If manual mode < 60 min: calculate wait, sleep, make own API call
  - If manual mode >= 60 min: skip (human override)
  - If override expires outside window: skip
- [ ] 8.3 Write tests for concurrent processing scenarios

## 9. Refactor Control Job

- [ ] 9.1 Update `control/controller.go` `Run()` method:
  - Wait for data on homeStatusChannel
  - Verify data is from current minute
  - Spawn per-room goroutines
  - Wait for all goroutines to complete
- [ ] 9.2 Remove `runSyncMode()` and `runNormalMode()` - replace with single flow
- [ ] 9.3 Update cron configuration to use `controlJobCron` (default: `"0 0,15,30,45 * * * *"`)

## 10. Implement Hard Override Job

- [ ] 10.1 Create `control/hard_override_job.go`:
  - Create `HardOverrideJob` struct with controller reference
  - Implement `Run(ctx context.Context)` method
  - Check for active hard override windows
  - Use same three-zone algorithm with override target temp
  - Skip rooms with human overrides (>= 60 min)
- [ ] 10.2 Add `hardOverrideJobCron` configuration (default: `"0 * * * * *"`)
- [ ] 10.3 Write tests for Hard Override Job

## 11. Remove Deprecated Code

- [ ] 11.1 Delete `control/sync.go` entirely (schedule sync no longer needed)
- [ ] 11.2 Delete related test files:
  - `control/sync_timing_test.go`
  - `control/sync_hybrid_test.go`
- [ ] 11.3 Remove from `control/evaluate.go`:
  - `clearPendingSetpoint()`
  - `setPendingSetpoint()`
- [ ] 11.4 Remove schedule sync configuration fields from `config/config.go`:
  - `ScheduleSyncIntervalMinutes`
  - `ScheduleSyncPollIntervalSeconds`
  - `ScheduleSyncPollTimeoutSeconds`

## 12. Update Configuration

- [ ] 12.1 Update `config/config.go`:
  - Change `TemperatureThreshold` default from 0.5 to 0.2
  - Rename `HomeStatusFetchCron` to `MetricJobCron`
  - Rename `ControlLoopCron` to `ControlJobCron`
  - Add `HardOverrideJobCron` field
  - Update `ControlJobCron` default to `"0 0,15,30,45 * * * *"`
  - Remove deprecated schedule sync fields
- [ ] 12.2 Update config validation
- [ ] 12.3 Update `config.yaml` example file
- [ ] 12.4 Update config tests

## 13. Update Main Orchestration

- [ ] 13.1 Update `main.go` to register three jobs:
  - Metric Job with `MetricJobCron`
  - Control Job with `ControlJobCron`
  - Hard Override Job with `HardOverrideJobCron`
- [ ] 13.2 Ensure proper job dependency (Control Job starts after Metric Job scheduled)

## 14. Update Documentation

- [ ] 14.1 Update `CONTROL_ALGORITHM.md`:
  - Document three-job architecture
  - Document simplified algorithm
  - Document duration-based override detection
  - Update timing diagrams
  - Remove references to removed features
- [ ] 14.2 Update `home-controller/CLAUDE.md` with new architecture

## 15. Testing

- [ ] 15.1 Run all existing tests, fix failures
- [ ] 15.2 Add integration tests for job coordination
- [ ] 15.3 Run tests with race detector: `go test -race ./...`
- [ ] 15.4 Manual testing in dry-run mode

## 16. Cleanup

- [ ] 16.1 Run `go fmt ./...`
- [ ] 16.2 Run `go vet ./...`
- [ ] 16.3 Remove any unused imports/variables
- [ ] 16.4 Final code review

## Dependencies

- Tasks 2-4 can be done in parallel
- Task 5 depends on Task 2 (simplified state)
- Task 6 depends on Task 2 (simplified state)
- Tasks 7-8 depend on Task 5 (simplified algorithm)
- Task 9 depends on Tasks 3, 4, 8 (channel, metric job, concurrent processing)
- Task 10 depends on Task 5 (simplified algorithm)
- Task 11 depends on Tasks 9, 10 (new jobs in place)
- Tasks 12-14 can start after Task 11
- Task 15 depends on all implementation tasks
