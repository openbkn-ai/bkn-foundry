// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package build_task

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/kweaver-ai/kweaver-go-lib/rest"
	"go.uber.org/mock/gomock"

	verrors "vega-backend/errors"
	"vega-backend/interfaces"
	mock_interfaces "vega-backend/interfaces/mock"
)

func TestCreateBuildTaskRejectsDisabledCatalog(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockCS := mock_interfaces.NewMockCatalogService(ctrl)
	mockRA := mock_interfaces.NewMockResourceAccess(ctrl)
	service := &buildTaskService{cs: mockCS, ra: mockRA}

	mockRA.EXPECT().GetByID(gomock.Any(), "resource-1").
		Return(&interfaces.Resource{
			ID:        "resource-1",
			CatalogID: "catalog-1",
			Category:  interfaces.ResourceCategoryTable,
		}, nil)
	mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
		Return(&interfaces.Catalog{ID: "catalog-1", Enabled: false}, nil)

	_, err := service.CreateBuildTask(context.Background(), &interfaces.CreateBuildTaskRequest{ResourceID: "resource-1"})
	assertCatalogDisabledError(t, err)
}

func TestStartBuildTaskRejectsDisabledCatalog(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockCS := mock_interfaces.NewMockCatalogService(ctrl)
	mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
	service := &buildTaskService{cs: mockCS, bta: mockBTA}

	mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").
		Return(&interfaces.BuildTask{
			ID:        "task-1",
			CatalogID: "catalog-1",
			Status:    interfaces.BuildTaskStatusInit,
		}, nil)
	mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
		Return(&interfaces.Catalog{ID: "catalog-1", Enabled: false}, nil)

	err := service.StartBuildTask(context.Background(), "task-1", interfaces.BuildTaskExecuteTypeIncremental)
	assertCatalogDisabledError(t, err)
}

func assertCatalogDisabledError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected error")
	}
	httpErr, ok := err.(*rest.HTTPError)
	if !ok {
		t.Fatalf("expected HTTPError, got %T", err)
	}
	if httpErr.HTTPCode != http.StatusConflict {
		t.Fatalf("expected HTTP 409, got %d", httpErr.HTTPCode)
	}
	if httpErr.BaseError.ErrorCode != verrors.VegaBackend_Catalog_IsDisabled {
		t.Fatalf("expected %s, got %s", verrors.VegaBackend_Catalog_IsDisabled, httpErr.BaseError.ErrorCode)
	}
}

// failed 状态必须允许 start（否则失败任务只能删除重建）。
// 借 catalog-disabled 错误证明状态检查已放行：若 failed 被状态机拒绝，
// 错误将是 InvalidStateTransition 而非 Catalog_IsDisabled。
func TestStartBuildTaskAllowsFailedStatus(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockCS := mock_interfaces.NewMockCatalogService(ctrl)
	mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
	service := &buildTaskService{cs: mockCS, bta: mockBTA}

	mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").
		Return(&interfaces.BuildTask{
			ID:        "task-1",
			CatalogID: "catalog-1",
			Status:    interfaces.BuildTaskStatusFailed,
		}, nil)
	mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
		Return(&interfaces.Catalog{ID: "catalog-1", Enabled: false}, nil)

	err := service.StartBuildTask(context.Background(), "task-1", interfaces.BuildTaskExecuteTypeIncremental)
	assertCatalogDisabledError(t, err)
}

// running → stopping：正常停止路径。
func TestStopBuildTaskRunningToStopping(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
	service := &buildTaskService{bta: mockBTA}

	mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").
		Return(&interfaces.BuildTask{ID: "task-1", Status: interfaces.BuildTaskStatusRunning}, nil)
	mockBTA.EXPECT().UpdateStatus(gomock.Any(), "task-1",
		map[string]any{"status": interfaces.BuildTaskStatusStopping}).Return(nil)

	if err := service.StopBuildTask(context.Background(), "task-1"); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

// stopping → stopped：worker 已不在时 stopping 永远不会被推进，
// 二次 stop 必须能强制落停，否则任务卡死无法删除。
func TestStopBuildTaskForceFinalizesStuckStopping(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
	service := &buildTaskService{bta: mockBTA}

	mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").
		Return(&interfaces.BuildTask{ID: "task-1", Status: interfaces.BuildTaskStatusStopping}, nil)
	mockBTA.EXPECT().UpdateStatus(gomock.Any(), "task-1",
		map[string]any{"status": interfaces.BuildTaskStatusStopped}).Return(nil)

	if err := service.StopBuildTask(context.Background(), "task-1"); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

// stopped 任务不可再 stop。
func TestStopBuildTaskRejectsStoppedStatus(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
	service := &buildTaskService{bta: mockBTA}

	mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").
		Return(&interfaces.BuildTask{ID: "task-1", Status: interfaces.BuildTaskStatusStopped}, nil)

	err := service.StopBuildTask(context.Background(), "task-1")
	if err == nil {
		t.Fatal("expected error")
	}
	httpErr, ok := err.(*rest.HTTPError)
	if !ok || httpErr.BaseError.ErrorCode != verrors.VegaBackend_BuildTask_InvalidStateTransition {
		t.Fatalf("expected InvalidStateTransition, got %v", err)
	}
}

// 编辑配置：running 任务不可改(旧 worker 仍在写索引),须先停。
func TestUpdateBuildTaskConfigRejectsRunning(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
	service := &buildTaskService{bta: mockBTA}

	mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").
		Return(&interfaces.BuildTask{ID: "task-1", Mode: interfaces.BuildTaskModeBatch, Status: interfaces.BuildTaskStatusRunning}, nil)

	err := service.UpdateBuildTaskConfig(context.Background(), "task-1", &interfaces.UpdateBuildTaskConfigRequest{FulltextFields: "name"})
	httpErr, ok := err.(*rest.HTTPError)
	if !ok || httpErr.BaseError.ErrorCode != verrors.VegaBackend_BuildTask_InvalidStateTransition {
		t.Fatalf("expected InvalidStateTransition, got %v", err)
	}
}

// 编辑配置：catalog 禁用时拒绝。
func TestUpdateBuildTaskConfigRejectsDisabledCatalog(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
	mockCS := mock_interfaces.NewMockCatalogService(ctrl)
	service := &buildTaskService{bta: mockBTA, cs: mockCS}

	mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").
		Return(&interfaces.BuildTask{ID: "task-1", Mode: interfaces.BuildTaskModeBatch, CatalogID: "catalog-1", Status: interfaces.BuildTaskStatusStopped}, nil)
	mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
		Return(&interfaces.Catalog{ID: "catalog-1", Enabled: false}, nil)

	err := service.UpdateBuildTaskConfig(context.Background(), "task-1", &interfaces.UpdateBuildTaskConfigRequest{FulltextFields: "name"})
	assertCatalogDisabledError(t, err)
}

// 编辑配置：embedding 与 fulltext 都为空 → 400,任务没有要建的索引。
func TestUpdateBuildTaskConfigRejectsNoFields(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
	mockCS := mock_interfaces.NewMockCatalogService(ctrl)
	service := &buildTaskService{bta: mockBTA, cs: mockCS}

	mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").
		Return(&interfaces.BuildTask{ID: "task-1", Mode: interfaces.BuildTaskModeBatch, CatalogID: "catalog-1", Status: interfaces.BuildTaskStatusCompleted}, nil)
	mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
		Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)

	err := service.UpdateBuildTaskConfig(context.Background(), "task-1", &interfaces.UpdateBuildTaskConfigRequest{})
	httpErr, ok := err.(*rest.HTTPError)
	if !ok || httpErr.HTTPCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %v", err)
	}
}

func TestComputeIndexHealth(t *testing.T) {
	cases := []struct {
		name          string
		bt            interfaces.BuildTask
		wantEmbedding string
		wantFulltext  string
		wantUsable    bool
	}{
		{
			name:          "embedding all failed (completed but vectorized=0) -> failed, fulltext ok, unusable",
			bt:            interfaces.BuildTask{Status: "completed", EmbeddingFields: "name", FulltextFields: "name", SyncedCount: 6, VectorizedCount: 0},
			wantEmbedding: "failed", wantFulltext: "ok", wantUsable: false,
		},
		{
			name:          "embedding partial -> partial, unusable",
			bt:            interfaces.BuildTask{Status: "completed", EmbeddingFields: "name", SyncedCount: 6, VectorizedCount: 4},
			wantEmbedding: "partial", wantFulltext: "none", wantUsable: false,
		},
		{
			name:          "embedding full -> ok, usable",
			bt:            interfaces.BuildTask{Status: "completed", EmbeddingFields: "name", SyncedCount: 6, VectorizedCount: 6},
			wantEmbedding: "ok", wantFulltext: "none", wantUsable: true,
		},
		{
			name:          "no embedding requested -> none, usable",
			bt:            interfaces.BuildTask{Status: "completed", FulltextFields: "name", SyncedCount: 6},
			wantEmbedding: "none", wantFulltext: "ok", wantUsable: true,
		},
		{
			name:          "running -> building, not usable yet",
			bt:            interfaces.BuildTask{Status: "running", EmbeddingFields: "name", SyncedCount: 6, VectorizedCount: 2},
			wantEmbedding: "building", wantFulltext: "none", wantUsable: false,
		},
		{
			name:          "empty table -> ok, usable",
			bt:            interfaces.BuildTask{Status: "completed", EmbeddingFields: "name", SyncedCount: 0, VectorizedCount: 0},
			wantEmbedding: "ok", wantFulltext: "none", wantUsable: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := computeIndexHealth(&c.bt)
			if h.Embedding != c.wantEmbedding || h.Fulltext != c.wantFulltext || h.Usable != c.wantUsable {
				t.Fatalf("got embedding=%s fulltext=%s usable=%v, want %s/%s/%v",
					h.Embedding, h.Fulltext, h.Usable, c.wantEmbedding, c.wantFulltext, c.wantUsable)
			}
		})
	}
}

// 删任务应连带 drop 其 OpenSearch 索引（与删资源/删 catalog 级联语义一致）。
func TestDeleteBuildTasks_DropsIndexAndRow(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
	mockRA := mock_interfaces.NewMockResourceAccess(ctrl)
	mockDS := mock_interfaces.NewMockDatasetService(ctrl)
	service := &buildTaskService{bta: mockBTA, ra: mockRA, ds: mockDS}

	mockBTA.EXPECT().GetByID(gomock.Any(), "t1").
		Return(&interfaces.BuildTask{ID: "t1", ResourceID: "r1", Status: "completed"}, nil)
	mockRA.EXPECT().GetByID(gomock.Any(), "r1").
		Return(&interfaces.Resource{ID: "r1", LocalIndexName: interfaces.BuildIndexName("r1", "old-task")}, nil)
	mockDS.EXPECT().Delete(gomock.Any(), interfaces.BuildIndexName("r1", "t1")).Return(nil)
	mockBTA.EXPECT().Delete(gomock.Any(), "t1").Return(nil)

	if err := service.DeleteBuildTasks(context.Background(), []string{"t1"}, false, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDeleteBuildTasks_RefusesActiveLocalIndex(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
	mockRA := mock_interfaces.NewMockResourceAccess(ctrl)
	mockDS := mock_interfaces.NewMockDatasetService(ctrl)
	service := &buildTaskService{bta: mockBTA, ra: mockRA, ds: mockDS}

	idx := interfaces.BuildIndexName("r1", "t1")
	mockBTA.EXPECT().GetByID(gomock.Any(), "t1").
		Return(&interfaces.BuildTask{ID: "t1", ResourceID: "r1", Status: interfaces.BuildTaskStatusCompleted}, nil)
	mockRA.EXPECT().GetByID(gomock.Any(), "r1").
		Return(&interfaces.Resource{ID: "r1", LocalIndexName: idx}, nil)
	// Active index conflicts must not delete either the index or the task row.

	err := service.DeleteBuildTasks(context.Background(), []string{"t1"}, false, false)
	httpErr, ok := err.(*rest.HTTPError)
	if !ok {
		t.Fatalf("expected HTTPError, got %T: %v", err, err)
	}
	if httpErr.HTTPCode != http.StatusConflict {
		t.Fatalf("expected 409, got %d", httpErr.HTTPCode)
	}
	if httpErr.BaseError.ErrorCode != verrors.VegaBackend_BuildTask_ActiveIndexInUse {
		t.Fatalf("expected %s, got %s", verrors.VegaBackend_BuildTask_ActiveIndexInUse, httpErr.BaseError.ErrorCode)
	}
}

func TestDeleteBuildTasks_DeleteActiveLocalIndexWhenExplicitlyAllowed(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
	mockRA := mock_interfaces.NewMockResourceAccess(ctrl)
	mockDS := mock_interfaces.NewMockDatasetService(ctrl)
	service := &buildTaskService{bta: mockBTA, ra: mockRA, ds: mockDS}

	idx := interfaces.BuildIndexName("r1", "t1")
	resource := &interfaces.Resource{ID: "r1", LocalIndexName: idx}
	mockBTA.EXPECT().GetByID(gomock.Any(), "t1").
		Return(&interfaces.BuildTask{ID: "t1", ResourceID: "r1", Status: interfaces.BuildTaskStatusCompleted}, nil)
	mockRA.EXPECT().GetByID(gomock.Any(), "r1").Return(resource, nil)
	mockRA.EXPECT().Update(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, got *interfaces.Resource) error {
			if got.ID != "r1" {
				t.Fatalf("expected resource r1, got %s", got.ID)
			}
			if got.LocalIndexName != "" {
				t.Fatalf("expected LocalIndexName to be cleared, got %q", got.LocalIndexName)
			}
			return nil
		})
	mockDS.EXPECT().Delete(gomock.Any(), idx).Return(nil)
	mockBTA.EXPECT().Delete(gomock.Any(), "t1").Return(nil)

	if err := service.DeleteBuildTasks(context.Background(), []string{"t1"}, false, true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDeleteBuildTasks_ClearActiveLocalIndexFailureBlocksDeletion(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
	mockRA := mock_interfaces.NewMockResourceAccess(ctrl)
	mockDS := mock_interfaces.NewMockDatasetService(ctrl)
	service := &buildTaskService{bta: mockBTA, ra: mockRA, ds: mockDS}

	idx := interfaces.BuildIndexName("r1", "t1")
	mockBTA.EXPECT().GetByID(gomock.Any(), "t1").
		Return(&interfaces.BuildTask{ID: "t1", ResourceID: "r1", Status: interfaces.BuildTaskStatusCompleted}, nil)
	mockRA.EXPECT().GetByID(gomock.Any(), "r1").
		Return(&interfaces.Resource{ID: "r1", LocalIndexName: idx}, nil)
	mockRA.EXPECT().Update(gomock.Any(), gomock.Any()).Return(errors.New("update failed"))
	// Clearing LocalIndexName failed, so the index and task row must remain untouched.

	err := service.DeleteBuildTasks(context.Background(), []string{"t1"}, false, true)
	httpErr, ok := err.(*rest.HTTPError)
	if !ok {
		t.Fatalf("expected HTTPError, got %T: %v", err, err)
	}
	if httpErr.HTTPCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", httpErr.HTTPCode)
	}
}

func TestDeleteBuildTasks_AllowsOrphanTaskWhenResourceMissing(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
	mockRA := mock_interfaces.NewMockResourceAccess(ctrl)
	mockDS := mock_interfaces.NewMockDatasetService(ctrl)
	service := &buildTaskService{bta: mockBTA, ra: mockRA, ds: mockDS}

	mockBTA.EXPECT().GetByID(gomock.Any(), "t1").
		Return(&interfaces.BuildTask{ID: "t1", ResourceID: "missing-resource", Status: interfaces.BuildTaskStatusFailed}, nil)
	mockRA.EXPECT().GetByID(gomock.Any(), "missing-resource").Return(nil, nil)
	mockDS.EXPECT().Delete(gomock.Any(), interfaces.BuildIndexName("missing-resource", "t1")).Return(nil)
	mockBTA.EXPECT().Delete(gomock.Any(), "t1").Return(nil)

	if err := service.DeleteBuildTasks(context.Background(), []string{"t1"}, false, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDeleteBuildTasks_ResourceLookupFailureBlocksDeletion(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
	mockRA := mock_interfaces.NewMockResourceAccess(ctrl)
	mockDS := mock_interfaces.NewMockDatasetService(ctrl)
	service := &buildTaskService{bta: mockBTA, ra: mockRA, ds: mockDS}

	mockBTA.EXPECT().GetByID(gomock.Any(), "t1").
		Return(&interfaces.BuildTask{ID: "t1", ResourceID: "r1", Status: interfaces.BuildTaskStatusStopped}, nil)
	mockRA.EXPECT().GetByID(gomock.Any(), "r1").Return(nil, errors.New("db unavailable"))
	// If the guard cannot prove the index is safe to delete, deletion must not proceed.

	err := service.DeleteBuildTasks(context.Background(), []string{"t1"}, false, false)
	httpErr, ok := err.(*rest.HTTPError)
	if !ok {
		t.Fatalf("expected HTTPError, got %T: %v", err, err)
	}
	if httpErr.HTTPCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", httpErr.HTTPCode)
	}
}

// 任一任务运行中 → 整批 409，索引/行都不删。
func TestDeleteBuildTasks_RefusesRunning(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
	mockDS := mock_interfaces.NewMockDatasetService(ctrl)
	service := &buildTaskService{bta: mockBTA, ds: mockDS}

	mockBTA.EXPECT().GetByID(gomock.Any(), "t1").
		Return(&interfaces.BuildTask{ID: "t1", ResourceID: "r1", Status: "running"}, nil)
	// 不应调用 ds.Delete / bta.Delete

	if err := service.DeleteBuildTasks(context.Background(), []string{"t1"}, false, true); err == nil {
		t.Fatalf("expected 409 when a task is running")
	}
}
