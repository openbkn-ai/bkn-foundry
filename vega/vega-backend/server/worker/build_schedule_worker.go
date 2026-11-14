// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package worker

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics"
)

const buildScheduleScanInterval = time.Minute

// BuildScheduleWorker 以数据库为准推进周期计划，任务执行由 BuildTaskWorker 处理。
type BuildScheduleWorker struct {
	sa       interfaces.BuildScheduleAccess
	ss       interfaces.BuildScheduleService
	wg       sync.WaitGroup
	stopCh   chan struct{}
	stopOnce sync.Once
	stopped  atomic.Bool
}

func NewBuildScheduleWorker(_ *common.AppSetting, ss interfaces.BuildScheduleService) *BuildScheduleWorker {
	return &BuildScheduleWorker{sa: logics.BSA, ss: ss, stopCh: make(chan struct{})}
}

func (w *BuildScheduleWorker) Start() {
	w.wg.Add(1)
	go func() { defer w.wg.Done(); w.run(context.Background()) }()
}

func (w *BuildScheduleWorker) Stop() {
	w.stopped.Store(true)
	w.stopOnce.Do(func() { close(w.stopCh) })
	w.wg.Wait()
}

func (w *BuildScheduleWorker) run(ctx context.Context) {
	w.runDue(ctx)
	ticker := time.NewTicker(buildScheduleScanInterval)
	defer ticker.Stop()
	for {
		select {
		case <-w.stopCh:
			return
		case <-ticker.C:
			w.runDue(ctx)
		}
	}
}

func (w *BuildScheduleWorker) runDue(ctx context.Context) {
	defer func() {
		if recover() != nil {
			logger.Error("Scan build schedules panicked")
		}
	}()
	schedules, err := w.sa.ListDue(ctx, time.Now().UnixMilli())
	if err != nil {
		logger.Error("List due build schedules failed")
		return
	}
	for _, schedule := range schedules {
		if w.stopped.Load() {
			return
		}
		w.runSchedule(ctx, schedule)
	}
}

func (w *BuildScheduleWorker) runSchedule(ctx context.Context, s *interfaces.BuildSchedule) {
	w.runScheduleAt(ctx, s, time.Now())
}

func (w *BuildScheduleWorker) runScheduleAt(ctx context.Context, s *interfaces.BuildSchedule, now time.Time) {
	var runID string
	message, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "VegaBackend.Schedule.Result.BuildCallFailed", nil)
	if renderErr != nil {
		logger.Errorf("localized message rendering failed: %v", renderErr)
	}
	result := interfaces.ScheduleRunResult{Status: interfaces.ScheduleStatusFailed, Message: message}
	defer func() {
		if recover() != nil {
			logger.Errorf("Build schedule execution panicked: schedule_id=%s", s.ID)
			return
		}
		if runID != "" {
			if _, err := w.sa.CompleteRun(ctx, s.ID, runID, result); err != nil {
				logger.Errorf("Complete build schedule result failed: schedule_id=%s", s.ID)
			}
		}
	}()
	if !s.Enabled || s.NextRun > now.UnixMilli() {
		return
	}
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, s.Creator)
	if s.EndTime > 0 && now.UnixMilli() > s.EndTime {
		if err := w.ss.InternalUpdateEnabled(ctx, s, false); err != nil {
			logger.Errorf("Disable expired build schedule failed: schedule_id=%s", s.ID)
		}
		return
	}
	cronSchedule, err := common.ParseHourlyCronExpr(s.CronExpr)
	if err != nil {
		message, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "VegaBackend.Schedule.Result.BuildCronInvalid", nil)
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		if _, disableErr := w.sa.DisableInvalid(ctx, s.ID, s.UpdateTime, s.NextRun, message); disableErr != nil {
			logger.Errorf("Disable invalid build schedule failed: schedule_id=%s", s.ID)
		}
		return
	}
	from := now
	if s.StartTime > now.UnixMilli() {
		from = time.UnixMilli(s.StartTime).In(now.Location()).Add(-time.Nanosecond)
	}
	next := cronSchedule.Next(from)
	if next.IsZero() {
		message, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "VegaBackend.Schedule.Result.BuildCronNoFuture", nil)
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		if _, disableErr := w.sa.DisableInvalid(ctx, s.ID, s.UpdateTime, s.NextRun, message); disableErr != nil {
			logger.Errorf("Disable exhausted build schedule failed: schedule_id=%s", s.ID)
		}
		return
	}
	if s.NextRun == 0 || s.StartTime > now.UnixMilli() {
		if _, err = w.sa.InitializeNextRun(ctx, s.ID, s.UpdateTime, s.NextRun, next.UnixMilli()); err != nil {
			logger.Errorf("Initialize build schedule failed: schedule_id=%s", s.ID)
		}
		return
	}
	id, err := uuid.NewV7()
	if err != nil {
		logger.Errorf("Generate build schedule trigger ID failed: schedule_id=%s", s.ID)
		return
	}
	claimed, err := w.sa.ClaimRun(ctx, s.ID, s.UpdateTime, s.NextRun, now.UnixMilli(), next.UnixMilli(), id.String())
	if err != nil {
		logger.Errorf("Claim build schedule failed: schedule_id=%s", s.ID)
		return
	}
	if claimed == 0 {
		return
	}
	runID = id.String()
	result, err = w.ss.ExecuteSchedule(ctx, s)
	if err != nil {
		logger.Errorf("Build schedule call failed: schedule_id=%s", s.ID)
	}
	if result.Status == "" {
		message, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "VegaBackend.Schedule.Result.BuildCreateUnavailable", nil)
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		result = interfaces.ScheduleRunResult{Status: interfaces.ScheduleStatusFailed, Message: message}
	}
}
