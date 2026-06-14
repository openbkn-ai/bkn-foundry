// Copyright 2026 openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package build_task

import (
	"context"
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
