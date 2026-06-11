// Copyright 2026 openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package worker

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/hibiken/asynq"
	"go.uber.org/mock/gomock"

	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
)

// 复现 bug：worker 异步执行构建任务时，必须把任务创建者(Creator)回填进 ctx，
// 否则下游带权限检查的服务(如 CatalogService.GetByID)会报
// "Access denied: missing account ID or type"。

var testCreator = interfaces.AccountInfo{ID: "u1", Type: "user"}

func accountFromCtx(ctx context.Context) (interfaces.AccountInfo, bool) {
	ai, ok := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	return ai, ok
}

func newBuildTaskPayload(t *testing.T, msg any) []byte {
	t.Helper()
	payload, err := sonic.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal task message failed: %v", err)
	}
	return payload
}

func TestBatchBuildHandlerInjectsCreatorIntoCtx(t *testing.T) {
	ctrl := gomock.NewController(t)
	taskAccess := vmock.NewMockBuildTaskAccess(ctrl)
	resAccess := vmock.NewMockResourceAccess(ctrl)
	cs := vmock.NewMockCatalogService(ctrl)
	ds := vmock.NewMockDatasetService(ctrl)
	// executeBuild 现在无条件调 createLocalIndex（幂等），索引已存在直接跳过
	ds.EXPECT().CheckExist(gomock.Any(), gomock.Any()).Return(true, nil).AnyTimes()
	bh := &batchBuildHandler{taskAccess: taskAccess, resAccess: resAccess, cs: cs, ds: ds}

	taskAccess.EXPECT().GetByID(gomock.Any(), "t1").Return(&interfaces.BuildTask{
		ID: "t1", ResourceID: "r1", Status: interfaces.BuildTaskStatusRunning, Creator: testCreator,
	}, nil)
	resAccess.EXPECT().GetByID(gomock.Any(), "r1").Return(&interfaces.Resource{ID: "r1", CatalogID: "c1"}, nil)
	taskAccess.EXPECT().UpdateStatus(gomock.Any(), "t1", gomock.Any()).Return(nil).AnyTimes()

	var gotAccount interfaces.AccountInfo
	var hasAccount bool
	cs.EXPECT().GetByID(gomock.Any(), "c1", true).DoAndReturn(
		func(ctx context.Context, id string, withSensitiveFields bool) (*interfaces.Catalog, error) {
			gotAccount, hasAccount = accountFromCtx(ctx)
			return nil, errors.New("forbidden")
		})

	task := asynq.NewTask("build:batch", newBuildTaskPayload(t, interfaces.BatchBuildTaskMessage{TaskID: "t1"}))
	if err := bh.HandleTask(context.Background(), task); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasAccount || gotAccount != testCreator {
		t.Fatalf("expected creator %+v injected into ctx when calling catalog service, got %+v (present=%v)",
			testCreator, gotAccount, hasAccount)
	}
}

func TestStreamingBuildHandlerInjectsCreatorIntoCtx(t *testing.T) {
	ctrl := gomock.NewController(t)
	taskAccess := vmock.NewMockBuildTaskAccess(ctrl)
	resAccess := vmock.NewMockResourceAccess(ctrl)
	cs := vmock.NewMockCatalogService(ctrl)
	sh := &streamingBuildHandler{taskAccess: taskAccess, resAccess: resAccess, cs: cs}

	taskAccess.EXPECT().GetByID(gomock.Any(), "t1").Return(&interfaces.BuildTask{
		ID: "t1", ResourceID: "r1", Status: interfaces.BuildTaskStatusRunning, Creator: testCreator,
	}, nil)
	resAccess.EXPECT().GetByID(gomock.Any(), "r1").Return(&interfaces.Resource{ID: "r1", CatalogID: "c1"}, nil)

	var gotAccount interfaces.AccountInfo
	var hasAccount bool
	cs.EXPECT().GetByID(gomock.Any(), "c1", true).DoAndReturn(
		func(ctx context.Context, id string, withSensitiveFields bool) (*interfaces.Catalog, error) {
			gotAccount, hasAccount = accountFromCtx(ctx)
			return nil, errors.New("forbidden")
		})

	task := asynq.NewTask("build:streaming", newBuildTaskPayload(t, interfaces.StreamingBuildTaskMessage{TaskID: "t1"}))
	err := sh.HandleTask(context.Background(), task)
	if err == nil || !strings.Contains(err.Error(), "get catalog failed") {
		t.Fatalf("expected get catalog failed error, got %v", err)
	}
	if !hasAccount || gotAccount != testCreator {
		t.Fatalf("expected creator %+v injected into ctx when calling catalog service, got %+v (present=%v)",
			testCreator, gotAccount, hasAccount)
	}
}

func TestEmbeddingHandlerInjectsCreatorIntoCtx(t *testing.T) {
	ctrl := gomock.NewController(t)
	taskAccess := vmock.NewMockBuildTaskAccess(ctrl)
	resAccess := vmock.NewMockResourceAccess(ctrl)
	eh := &embeddingHandler{taskAccess: taskAccess, resAccess: resAccess}

	taskAccess.EXPECT().GetByID(gomock.Any(), "t1").Return(&interfaces.BuildTask{
		ID: "t1", ResourceID: "r1", Status: interfaces.BuildTaskStatusRunning, Creator: testCreator,
	}, nil)

	var gotAccount interfaces.AccountInfo
	var hasAccount bool
	resAccess.EXPECT().GetByID(gomock.Any(), "r1").DoAndReturn(
		func(ctx context.Context, id string) (*interfaces.Resource, error) {
			gotAccount, hasAccount = accountFromCtx(ctx)
			return nil, nil
		})

	task := asynq.NewTask("build:embedding", newBuildTaskPayload(t, interfaces.EmbeddingBuildTaskMessage{TaskID: "t1"}))
	if err := eh.HandleTask(context.Background(), task); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasAccount || gotAccount != testCreator {
		t.Fatalf("expected creator %+v injected into ctx after loading task, got %+v (present=%v)",
			testCreator, gotAccount, hasAccount)
	}
}
