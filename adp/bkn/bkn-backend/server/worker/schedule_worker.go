// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/robfig/cron/v3"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/action_schedule"
)

const (
	DefaultPollInterval     = 10 * time.Second
	DefaultLockTimeout      = 5 * time.Minute
	DefaultExecutionTimeout = 10 * time.Minute
	// eventScheduleSyncPolls is how many polls pass between two syncs of the schedules the
	// platform keeps for record-event action types, in case a sync after a change was missed.
	eventScheduleSyncPolls = 6
)

var (
	swOnce  sync.Once
	sWorker *ScheduleWorker
)

// ScheduleWorker polls for due schedules and executes them
type ScheduleWorker struct {
	appSetting *common.AppSetting
	asa        interfaces.ActionScheduleAccess
	ata        interfaces.ActionTypeAccess
	cronParser cron.Parser
	httpClient *http.Client
	podID      string

	pollInterval     time.Duration
	lockTimeout      time.Duration
	executionTimeout time.Duration

	stopChan chan struct{}
	wg       sync.WaitGroup
}

// NewScheduleWorker creates a singleton instance of ScheduleWorker
func NewScheduleWorker(appSetting *common.AppSetting) *ScheduleWorker {
	swOnce.Do(func() {
		hostname, _ := os.Hostname()
		podID := fmt.Sprintf("%s-%d", hostname, os.Getpid())

		sWorker = &ScheduleWorker{
			appSetting: appSetting,
			asa:        logics.ASA,
			ata:        logics.ATA,
			// The five-field cron of a user's schedule, and the zoned cron or fixed rate of a
			// record-event action type's.
			cronParser: interfaces.ScheduleParser().Parser,
			httpClient: &http.Client{
				Timeout: DefaultExecutionTimeout,
			},
			podID: podID,

			pollInterval:     DefaultPollInterval,
			lockTimeout:      DefaultLockTimeout,
			executionTimeout: DefaultExecutionTimeout,

			stopChan: make(chan struct{}),
		}

		// Override with config if available
		if appSetting.ServerSetting.SchedulePollInterval > 0 {
			sWorker.pollInterval = time.Duration(appSetting.ServerSetting.SchedulePollInterval) * time.Second
		}
		if appSetting.ServerSetting.ScheduleLockTimeout > 0 {
			sWorker.lockTimeout = time.Duration(appSetting.ServerSetting.ScheduleLockTimeout) * time.Second
		}
	})
	return sWorker
}

// Start starts the schedule worker
func (w *ScheduleWorker) Start() {
	logger.Infof("ScheduleWorker starting with podID: %s, pollInterval: %v", w.podID, w.pollInterval)

	w.wg.Add(1)
	go w.pollLoop()
}

// Stop stops the schedule worker
func (w *ScheduleWorker) Stop() {
	logger.Info("ScheduleWorker stopping...")
	close(w.stopChan)
	w.wg.Wait()
	logger.Info("ScheduleWorker stopped")
}

// pollLoop is the main polling loop
func (w *ScheduleWorker) pollLoop() {
	defer w.wg.Done()

	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	// Run immediately on start
	w.syncEventSchedules()
	w.pollAndExecute()

	for polls := 1; ; polls++ {
		select {
		case <-ticker.C:
			if polls%eventScheduleSyncPolls == 0 {
				w.syncEventSchedules()
			}
			w.pollAndExecute()
		case <-w.stopChan:
			return
		}
	}
}

// pollAndExecute polls for due schedules and executes them
func (w *ScheduleWorker) pollAndExecute() {
	ctx := context.Background()
	now := time.Now().UnixMilli()

	// Get due schedules
	schedules, err := w.asa.GetDueSchedules(ctx, now)
	if err != nil {
		logger.Errorf("Failed to get due schedules: %v", err)
		return
	}

	if len(schedules) == 0 {
		return
	}

	logger.Debugf("Found %d due schedules", len(schedules))

	// Try to execute each schedule
	for _, schedule := range schedules {
		w.tryExecuteSchedule(ctx, schedule)
	}
}

// syncEventSchedules keeps the platform's schedules of record-event action types, when the module
// that provides them is assembled.
func (w *ScheduleWorker) syncEventSchedules() {
	if logics.ActionTypeOperationNamed(interfaces.ActionOperationEvent) == nil {
		return
	}
	if err := action_schedule.SyncEventSchedules(context.Background(), w.asa, w.ata, "", time.Now()); err != nil {
		logger.Errorf("Failed to sync the schedules of record-event action types: %v", err)
	}
}

// eventSchedulesRun reports whether the schedules of record-event action types may run now. One
// that may not is passed over without a run and keeps its next run time, so it runs as planned
// once the license allows it again.
func eventSchedulesRun() bool {
	operation := logics.ActionTypeOperationNamed(interfaces.ActionOperationEvent)
	return operation != nil && operation.Available()
}

// tryExecuteSchedule attempts to acquire lock and execute a schedule
func (w *ScheduleWorker) tryExecuteSchedule(ctx context.Context, schedule *interfaces.ActionSchedule) {
	if schedule.PlatformKept() && !eventSchedulesRun() {
		logger.Debugf("Schedule %s of a record-event action type waits for the license", schedule.ID)
		return
	}
	now := time.Now().UnixMilli()
	lockTimeoutMs := w.lockTimeout.Milliseconds()

	// Try to acquire lock
	rowsAffected, err := w.asa.TryAcquireLock(ctx, schedule.ID, w.podID, now, lockTimeoutMs)
	if err != nil {
		logger.Errorf("Failed to acquire lock for schedule %s: %v", schedule.ID, err)
		return
	}

	if rowsAffected == 0 {
		// Another pod is handling this schedule
		logger.Debugf("Schedule %s is being handled by another pod", schedule.ID)
		return
	}

	logger.Infof("Acquired lock for schedule %s, executing...", schedule.ID)

	// Execute the schedule
	executionID, err := w.executeSchedule(ctx, schedule)
	if err != nil {
		logger.Errorf("Failed to execute schedule %s: %v", schedule.ID, err)
	} else {
		logger.Infof("Schedule %s executed successfully, execution_id: %s", schedule.ID, executionID)
	}

	// Calculate next run time
	nextRunTime, err := w.calculateNextRunTime(schedule.CronExpression, now)
	if err != nil {
		logger.Errorf("Failed to calculate next run time for schedule %s: %v", schedule.ID, err)
		// Use fallback: retry after 1 hour to prevent schedule from being permanently stuck
		// This should rarely happen since cron expressions are validated at creation time
		nextRunTime = now + int64(time.Hour.Milliseconds())
		logger.Warnf("Using fallback next run time for schedule %s: %d", schedule.ID, nextRunTime)
	}

	// Release lock and update times
	lastRunTime := time.Now().UnixMilli()
	if err := w.asa.ReleaseLock(ctx, schedule.ID, w.podID, lastRunTime, nextRunTime); err != nil {
		logger.Errorf("Failed to release lock for schedule %s: %v", schedule.ID, err)
	}
}

// ExecuteSchedule runs the schedule once now, as its execution subject, without changing when it
// runs next.
func (w *ScheduleWorker) ExecuteSchedule(ctx context.Context, schedule *interfaces.ActionSchedule) (string, error) {
	return w.executeSchedule(ctx, schedule)
}

var _ interfaces.ScheduleExecutor = (*ScheduleWorker)(nil)

// executeSchedule calls ontology-query to execute the action
func (w *ScheduleWorker) executeSchedule(ctx context.Context, schedule *interfaces.ActionSchedule) (string, error) {
	executionSubject := schedule.ExecutionSubject
	if executionSubject.ID == "" || executionSubject.Type == "" {
		return "", fmt.Errorf("schedule execution subject is missing")
	}
	if w.ata == nil {
		return "", fmt.Errorf("schedule action type access is unavailable")
	}
	actionTypes, err := w.ata.GetActionTypesByIDs(ctx, schedule.KNID, schedule.Branch, []string{schedule.ActionTypeID})
	if err != nil {
		return "", fmt.Errorf("load scheduled action type: %w", err)
	}
	if len(actionTypes) == 0 {
		return "", fmt.Errorf("scheduled action type %q no longer exists", schedule.ActionTypeID)
	}
	identities := schedule.InstanceIdentities
	if schedule.PlatformKept() {
		// A record-event action type selects its objects when it runs.
		if !interfaces.IsEventAction(actionTypes[0]) {
			return "", fmt.Errorf("schedule %q follows action type %q, which no longer records events", schedule.ID, schedule.ActionTypeID)
		}
		identities = []map[string]any{}
	} else if err := interfaces.ValidateIdentitySchedule(actionTypes[0], schedule.InstanceIdentities); err != nil {
		return "", fmt.Errorf("schedule %q cannot execute: %w", schedule.ID, err)
	}

	// Build the request to ontology-query
	executeURL := fmt.Sprintf("%s/api/ontology-query/in/v1/knowledge-networks/%s/action-types/%s/execute",
		w.appSetting.OntologyQueryUrl, schedule.KNID, schedule.ActionTypeID)

	requestBody := map[string]any{
		"trigger_type":         "scheduled",
		"_instance_identities": identities,
		"dynamic_params":       schedule.DynamicParams,
	}

	bodyBytes, err := json.Marshal(requestBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request body: %v", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, executeURL, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	// Set internal account info headers
	req.Header.Set(interfaces.HTTP_HEADER_ACCOUNT_ID, executionSubject.ID)
	req.Header.Set(interfaces.HTTP_HEADER_ACCOUNT_TYPE, executionSubject.Type)

	resp, err := w.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to execute request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	// Parse response to get execution_id
	var response struct {
		ExecutionID string `json:"execution_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", fmt.Errorf("failed to decode response: %v", err)
	}

	return response.ExecutionID, nil
}

// calculateNextRunTime calculates the next run time based on cron expression
func (w *ScheduleWorker) calculateNextRunTime(cronExpr string, from int64) (int64, error) {
	schedule, err := w.cronParser.Parse(cronExpr)
	if err != nil {
		return 0, fmt.Errorf("invalid cron expression: %v", err)
	}

	fromTime := time.UnixMilli(from)
	nextTime := schedule.Next(fromTime)
	return nextTime.UnixMilli(), nil
}
