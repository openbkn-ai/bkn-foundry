// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	vmock "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/mock"
)

func TestDiscoverScheduleWorkerRunDue(t *testing.T) {
	t.Run("continues after a schedule panics", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		access := vmock.NewMockDiscoverScheduleAccess(ctrl)
		service := vmock.NewMockDiscoverScheduleService(ctrl)
		first, second := dueDiscoverSchedule("first"), dueDiscoverSchedule("second")
		access.EXPECT().ListDue(gomock.Any(), gomock.Any()).Return([]*interfaces.DiscoverSchedule{first, second}, nil)
		gomock.InOrder(
			access.EXPECT().ClaimRun(gomock.Any(), first.ID, first.UpdateTime, first.NextRun, gomock.Any(), gomock.Any(), gomock.Any()).Return(int64(1), nil),
			service.EXPECT().ExecuteSchedule(gomock.Any(), first).DoAndReturn(func(context.Context, *interfaces.DiscoverSchedule) (interfaces.ScheduleRunResult, error) {
				panic("interrupted")
			}),
			access.EXPECT().ClaimRun(gomock.Any(), second.ID, second.UpdateTime, second.NextRun, gomock.Any(), gomock.Any(), gomock.Any()).Return(int64(1), nil),
			service.EXPECT().ExecuteSchedule(gomock.Any(), second).Return(interfaces.ScheduleRunResult{Status: interfaces.ScheduleStatusSuccess}, nil),
			access.EXPECT().CompleteRun(gomock.Any(), second.ID, gomock.Any(), interfaces.ScheduleRunResult{Status: interfaces.ScheduleStatusSuccess}).Return(int64(1), nil),
		)
		w := &DiscoverScheduleWorker{dsa: access, dss: service}
		require.NotPanics(t, func() { w.runDue(context.Background()) })
	})
	t.Run("does not execute when listing fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		access := vmock.NewMockDiscoverScheduleAccess(ctrl)
		access.EXPECT().ListDue(gomock.Any(), gomock.Any()).Return(nil, errors.New("unavailable"))
		(&DiscoverScheduleWorker{dsa: access}).runDue(context.Background())
	})
	t.Run("recovers an access panic", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		access := vmock.NewMockDiscoverScheduleAccess(ctrl)
		access.EXPECT().ListDue(gomock.Any(), gomock.Any()).Do(func(context.Context, int64) { panic("interrupted") })
		require.NotPanics(t, func() { (&DiscoverScheduleWorker{dsa: access}).runDue(context.Background()) })
	})
}

func TestDiscoverScheduleWorkerRunScheduleAt(t *testing.T) {
	now := time.Date(2026, 11, 14, 10, 15, 0, 0, time.UTC)
	next := time.Date(2026, 11, 14, 11, 0, 0, 0, time.UTC).UnixMilli()
	newSchedule := func() *interfaces.DiscoverSchedule {
		return &interfaces.DiscoverSchedule{ID: "schedule", CatalogID: "catalog", Enabled: true, CronExpr: "0 * * * *", NextRun: now.Add(-3 * time.Hour).UnixMilli(), UpdateTime: 25, LastRun: 20, LastStatus: interfaces.ScheduleStatusFailed, LastMessage: "previous failure", Creator: interfaces.AccountInfo{ID: "creator", Type: "user"}}
	}
	t.Run("initializes zero next run without creating a task or overwriting result", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		access := vmock.NewMockDiscoverScheduleAccess(ctrl)
		service := vmock.NewMockDiscoverScheduleService(ctrl)
		schedule := newSchedule()
		schedule.NextRun = 0
		access.EXPECT().InitializeNextRun(gomock.Any(), schedule.ID, schedule.UpdateTime, int64(0), next).Return(int64(1), nil)
		(&DiscoverScheduleWorker{dsa: access, dss: service}).runScheduleAt(context.Background(), schedule, now)
		assert.Equal(t, int64(20), schedule.LastRun)
		assert.Equal(t, interfaces.ScheduleStatusFailed, schedule.LastStatus)
		assert.Equal(t, "previous failure", schedule.LastMessage)
	})
	t.Run("honors a future start boundary", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		access := vmock.NewMockDiscoverScheduleAccess(ctrl)
		schedule := newSchedule()
		schedule.StartTime = next
		access.EXPECT().InitializeNextRun(gomock.Any(), schedule.ID, schedule.UpdateTime, schedule.NextRun, next).Return(int64(1), nil)
		(&DiscoverScheduleWorker{dsa: access}).runScheduleAt(context.Background(), schedule, now)
	})
	for _, status := range []string{interfaces.ScheduleStatusSuccess, interfaces.ScheduleStatusSkipped, interfaces.ScheduleStatusFailed} {
		t.Run("persists "+status+" after claim and coalesces missed occurrences", func(t *testing.T) {
			ctrl := gomock.NewController(t)
			access := vmock.NewMockDiscoverScheduleAccess(ctrl)
			service := vmock.NewMockDiscoverScheduleService(ctrl)
			schedule := newSchedule()
			result := interfaces.ScheduleRunResult{Status: status}
			var runErr error
			if status != interfaces.ScheduleStatusSuccess {
				result.Message = "public explanation"
			}
			if status == interfaces.ScheduleStatusFailed {
				runErr = errors.New("private error")
			}
			var runID string
			gomock.InOrder(
				access.EXPECT().ClaimRun(gomock.Any(), schedule.ID, schedule.UpdateTime, schedule.NextRun, now.UnixMilli(), next, gomock.Any()).DoAndReturn(func(ctx context.Context, _ string, _, _, _, _ int64, token string) (int64, error) {
					_, err := uuid.Parse(token)
					require.NoError(t, err)
					runID = token
					assert.Equal(t, schedule.Creator, ctx.Value(interfaces.ACCOUNT_INFO_KEY))
					return 1, nil
				}),
				service.EXPECT().ExecuteSchedule(gomock.Any(), schedule).DoAndReturn(func(ctx context.Context, _ *interfaces.DiscoverSchedule) (interfaces.ScheduleRunResult, error) {
					assert.NotEmpty(t, runID)
					assert.Equal(t, schedule.Creator, ctx.Value(interfaces.ACCOUNT_INFO_KEY))
					return result, runErr
				}),
				access.EXPECT().CompleteRun(gomock.Any(), schedule.ID, gomock.Any(), result).DoAndReturn(func(_ context.Context, _, token string, _ interfaces.ScheduleRunResult) (int64, error) {
					assert.Equal(t, runID, token)
					return 1, nil
				}),
			)
			(&DiscoverScheduleWorker{dsa: access, dss: service}).runScheduleAt(context.Background(), schedule, now)
		})
	}
	for _, tc := range []struct {
		name string
		err  error
	}{{"already claimed", nil}, {"claim failed", errors.New("db unavailable")}} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			access := vmock.NewMockDiscoverScheduleAccess(ctrl)
			service := vmock.NewMockDiscoverScheduleService(ctrl)
			schedule := newSchedule()
			access.EXPECT().ClaimRun(gomock.Any(), schedule.ID, schedule.UpdateTime, schedule.NextRun, now.UnixMilli(), next, gomock.Any()).Return(int64(0), tc.err)
			(&DiscoverScheduleWorker{dsa: access, dss: service}).runScheduleAt(context.Background(), schedule, now)
		})
	}
	for _, tc := range []struct {
		expression string
		message    string
	}{
		{"invalid", "The schedule expression is invalid or runs more than once per hour; correct it before enabling."},
		{"*/30 * * * *", "The schedule expression is invalid or runs more than once per hour; correct it before enabling."},
		{"0 0 31 2 *", "The schedule expression has no valid future trigger time; correct it before enabling."},
	} {
		t.Run("disables invalid persisted expression "+tc.expression, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			access := vmock.NewMockDiscoverScheduleAccess(ctrl)
			schedule := newSchedule()
			schedule.CronExpr = tc.expression
			access.EXPECT().DisableInvalid(gomock.Any(), schedule.ID, schedule.UpdateTime, schedule.NextRun, tc.message).Return(int64(1), nil)
			ctx := i18n.WithLanguage(context.Background(), i18n.AmericanEnglish)
			(&DiscoverScheduleWorker{dsa: access}).runScheduleAt(ctx, schedule, now)
		})
	}
	for _, tc := range []struct {
		language i18n.Language
		message  string
	}{
		{i18n.SimplifiedChinese, "无法创建探查任务，请检查目录配置与权限"},
		{i18n.AmericanEnglish, "Could not create a discovery task; check the catalog configuration and permissions."},
	} {
		t.Run("localizes task creation fallback/"+string(tc.language), func(t *testing.T) {
			ctrl := gomock.NewController(t)
			access := vmock.NewMockDiscoverScheduleAccess(ctrl)
			service := vmock.NewMockDiscoverScheduleService(ctrl)
			schedule := newSchedule()
			gomock.InOrder(
				access.EXPECT().ClaimRun(gomock.Any(), schedule.ID, schedule.UpdateTime, schedule.NextRun, now.UnixMilli(), next, gomock.Any()).Return(int64(1), nil),
				service.EXPECT().ExecuteSchedule(gomock.Any(), schedule).Return(interfaces.ScheduleRunResult{}, errors.New("private task error")),
				access.EXPECT().CompleteRun(gomock.Any(), schedule.ID, gomock.Any(), interfaces.ScheduleRunResult{Status: interfaces.ScheduleStatusFailed, Message: tc.message}).Return(int64(1), nil),
			)
			ctx := i18n.WithLanguage(context.Background(), tc.language)
			(&DiscoverScheduleWorker{dsa: access, dss: service}).runScheduleAt(ctx, schedule, now)
		})
	}

	t.Run("retains triggering when task call panics", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		access := vmock.NewMockDiscoverScheduleAccess(ctrl)
		service := vmock.NewMockDiscoverScheduleService(ctrl)
		schedule := newSchedule()
		access.EXPECT().ClaimRun(gomock.Any(), schedule.ID, schedule.UpdateTime, schedule.NextRun, now.UnixMilli(), next, gomock.Any()).Return(int64(1), nil)
		service.EXPECT().ExecuteSchedule(gomock.Any(), schedule).DoAndReturn(func(context.Context, *interfaces.DiscoverSchedule) (interfaces.ScheduleRunResult, error) {
			panic("interrupted")
		})
		require.NotPanics(t, func() {
			(&DiscoverScheduleWorker{dsa: access, dss: service}).runScheduleAt(context.Background(), schedule, now)
		})
	})
	t.Run("does not retry when completion persistence fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		access := vmock.NewMockDiscoverScheduleAccess(ctrl)
		service := vmock.NewMockDiscoverScheduleService(ctrl)
		schedule := newSchedule()
		access.EXPECT().ClaimRun(gomock.Any(), schedule.ID, schedule.UpdateTime, schedule.NextRun, now.UnixMilli(), next, gomock.Any()).Return(int64(1), nil)
		service.EXPECT().ExecuteSchedule(gomock.Any(), schedule).Return(interfaces.ScheduleRunResult{Status: interfaces.ScheduleStatusSuccess}, nil)
		access.EXPECT().CompleteRun(gomock.Any(), schedule.ID, gomock.Any(), interfaces.ScheduleRunResult{Status: interfaces.ScheduleStatusSuccess}).Return(int64(0), errors.New("unavailable"))
		(&DiscoverScheduleWorker{dsa: access, dss: service}).runScheduleAt(context.Background(), schedule, now)
	})
	t.Run("disables expired schedule without executing", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		service := vmock.NewMockDiscoverScheduleService(ctrl)
		schedule := newSchedule()
		schedule.EndTime = now.Add(-time.Millisecond).UnixMilli()
		service.EXPECT().InternalUpdateEnabled(gomock.Any(), schedule, false).Return(nil)
		(&DiscoverScheduleWorker{dss: service}).runScheduleAt(context.Background(), schedule, now)
	})
	t.Run("ignores disabled or future schedules", func(t *testing.T) {
		disabled := newSchedule()
		disabled.Enabled = false
		future := newSchedule()
		future.NextRun = next
		w := &DiscoverScheduleWorker{}
		w.runScheduleAt(context.Background(), disabled, now)
		w.runScheduleAt(context.Background(), future, now)
	})
}

func dueDiscoverSchedule(id string) *interfaces.DiscoverSchedule {
	return &interfaces.DiscoverSchedule{ID: id, Enabled: true, CronExpr: "0 * * * *", NextRun: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()}
}
