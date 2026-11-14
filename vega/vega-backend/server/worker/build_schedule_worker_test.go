// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	mock "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func buildScheduleWorkerFixture(t *testing.T) (*BuildScheduleWorker, *mock.MockBuildScheduleAccess, *mock.MockBuildScheduleService, *interfaces.BuildSchedule) {
	t.Helper()
	ctrl := gomock.NewController(t)
	access := mock.NewMockBuildScheduleAccess(ctrl)
	service := mock.NewMockBuildScheduleService(ctrl)
	now := time.Date(2026, 11, 14, 12, 15, 0, 0, time.UTC)
	w := &BuildScheduleWorker{sa: access, ss: service, stopCh: make(chan struct{})}
	s := &interfaces.BuildSchedule{ID: "schedule", CatalogID: "catalog", ResourceID: "resource", Enabled: true, CronExpr: "0 * * * *", UpdateTime: 25, NextRun: now.Add(-time.Minute).UnixMilli(), Creator: interfaces.AccountInfo{ID: "creator", Type: "user"}}
	return w, access, service, s
}

func TestBuildScheduleWorkerRunSchedule(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 15, 0, 0, time.UTC)
	t.Run("invalid cron message follows context language", func(t *testing.T) {
		w, access, _, s := buildScheduleWorkerFixture(t)
		s.CronExpr = "invalid"
		access.EXPECT().DisableInvalid(gomock.Any(), s.ID, s.UpdateTime, s.NextRun,
			"Invalid schedule expression; correct it and enable the schedule again.").Return(int64(1), nil)
		ctx := i18n.WithLanguage(context.Background(), i18n.AmericanEnglish)
		w.runScheduleAt(ctx, s, now)
	})
	t.Run("zero next run only initializes time", func(t *testing.T) {
		w, access, _, s := buildScheduleWorkerFixture(t)
		s.NextRun = 0
		s.LastRun = 10
		s.LastStatus = interfaces.ScheduleStatusUnknown
		access.EXPECT().InitializeNextRun(gomock.Any(), s.ID, s.UpdateTime, int64(0), now.Add(45*time.Minute).UnixMilli()).Return(int64(1), nil)
		w.runScheduleAt(context.Background(), s, now)
		assert.Equal(t, int64(10), s.LastRun)
		assert.Equal(t, interfaces.ScheduleStatusUnknown, s.LastStatus)
	})
	t.Run("future start includes exact cron boundary", func(t *testing.T) {
		w, access, _, s := buildScheduleWorkerFixture(t)
		s.StartTime = time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC).UnixMilli()
		access.EXPECT().InitializeNextRun(gomock.Any(), s.ID, s.UpdateTime, s.NextRun, s.StartTime).Return(int64(1), nil)
		w.runScheduleAt(context.Background(), s, now)
	})
	t.Run("invalid cron is disabled with a visible message", func(t *testing.T) {
		w, access, _, s := buildScheduleWorkerFixture(t)
		s.CronExpr = "invalid"
		access.EXPECT().DisableInvalid(gomock.Any(), s.ID, s.UpdateTime, s.NextRun, "调度表达式无效，请修正后重新启用").Return(int64(1), nil)
		w.runScheduleAt(context.Background(), s, now)
	})
	t.Run("expired schedule is disabled without creating task", func(t *testing.T) {
		w, _, service, s := buildScheduleWorkerFixture(t)
		s.EndTime = now.Add(-time.Second).UnixMilli()
		service.EXPECT().InternalUpdateEnabled(gomock.Any(), s, false).Return(nil)
		w.runScheduleAt(context.Background(), s, now)
	})
	for _, name := range []string{"disabled", "not due"} {
		t.Run(name, func(t *testing.T) {
			w, _, _, s := buildScheduleWorkerFixture(t)
			if name == "disabled" {
				s.Enabled = false
			} else {
				s.NextRun = now.Add(time.Hour).UnixMilli()
			}
			w.runScheduleAt(context.Background(), s, now)
		})
	}
	for _, tc := range []struct {
		name string
		rows int64
		err  error
	}{
		{name: "already claimed"}, {name: "database unavailable", err: errors.New("db failed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, access, _, s := buildScheduleWorkerFixture(t)
			access.EXPECT().ClaimRun(gomock.Any(), s.ID, s.UpdateTime, s.NextRun, now.UnixMilli(), now.Add(45*time.Minute).UnixMilli(), gomock.Any()).Return(tc.rows, tc.err)
			w.runScheduleAt(context.Background(), s, now)
		})
	}
	for _, tc := range []struct {
		name   string
		result interfaces.ScheduleRunResult
		err    error
	}{
		{name: "success", result: interfaces.ScheduleRunResult{Status: interfaces.ScheduleStatusSuccess}},
		{name: "conflict skip", result: interfaces.ScheduleRunResult{Status: interfaces.ScheduleStatusSkipped, Message: "busy"}, err: errors.New("active task")},
		{name: "creation failed", result: interfaces.ScheduleRunResult{Status: interfaces.ScheduleStatusFailed, Message: "invalid resource"}, err: errors.New("source failure")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, access, service, s := buildScheduleWorkerFixture(t)
			// 停机错过多次执行，仅产生一次调用，并从恢复时刻计算下次触发。
			s.NextRun = now.Add(-48 * time.Hour).UnixMilli()
			var triggerID string
			gomock.InOrder(
				access.EXPECT().ClaimRun(gomock.Any(), s.ID, s.UpdateTime, s.NextRun, now.UnixMilli(), now.Add(45*time.Minute).UnixMilli(), gomock.Any()).DoAndReturn(func(ctx context.Context, _ string, _, _, _, _ int64, id string) (int64, error) {
					triggerID = id
					parsed, err := uuid.Parse(id)
					require.NoError(t, err)
					assert.Equal(t, uuid.Version(7), parsed.Version())
					assert.Equal(t, s.Creator, ctx.Value(interfaces.ACCOUNT_INFO_KEY))
					return 1, nil
				}),
				service.EXPECT().ExecuteSchedule(gomock.Any(), s).Return(tc.result, tc.err),
				access.EXPECT().CompleteRun(gomock.Any(), s.ID, gomock.Any(), tc.result).DoAndReturn(func(_ context.Context, _, id string, _ interfaces.ScheduleRunResult) (int64, error) {
					assert.Equal(t, triggerID, id)
					return 1, nil
				}),
			)
			w.runScheduleAt(context.Background(), s, now)
		})
	}
	t.Run("panic after claim leaves result unconfirmed and does not retry", func(t *testing.T) {
		w, access, service, s := buildScheduleWorkerFixture(t)
		access.EXPECT().ClaimRun(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(int64(1), nil)
		service.EXPECT().ExecuteSchedule(gomock.Any(), s).DoAndReturn(func(context.Context, *interfaces.BuildSchedule) (interfaces.ScheduleRunResult, error) {
			panic("sensitive panic")
		})
		access.EXPECT().CompleteRun(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
		assert.NotPanics(t, func() { w.runScheduleAt(context.Background(), s, now) })
	})
}

func TestBuildScheduleWorkerRunDue(t *testing.T) {
	t.Run("failure on one schedule does not prevent later execution", func(t *testing.T) {
		w, access, service, first := buildScheduleWorkerFixture(t)
		first.NextRun = 1
		second := *first
		second.ID = "second"
		access.EXPECT().ListDue(gomock.Any(), gomock.Any()).Return([]*interfaces.BuildSchedule{first, &second}, nil)
		access.EXPECT().ClaimRun(gomock.Any(), first.ID, gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(int64(0), errors.New("db failed"))
		access.EXPECT().ClaimRun(gomock.Any(), second.ID, gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(int64(1), nil)
		service.EXPECT().ExecuteSchedule(gomock.Any(), &second).Return(interfaces.ScheduleRunResult{Status: interfaces.ScheduleStatusSuccess}, nil)
		access.EXPECT().CompleteRun(gomock.Any(), second.ID, gomock.Any(), interfaces.ScheduleRunResult{Status: interfaces.ScheduleStatusSuccess}).Return(int64(1), nil)
		w.runDue(context.Background())
	})
}
