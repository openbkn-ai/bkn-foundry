// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package build_task

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/hibiken/asynq"
	"github.com/openbkn-ai/bkn-comm-go/rest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	verrors "vega-backend/errors"
	"vega-backend/interfaces"
	mock_interfaces "vega-backend/interfaces/mock"
	"vega-backend/logics"
)

func TestCreateBuildTaskRejectsDisabledCatalog(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockCS := mock_interfaces.NewMockCatalogService(ctrl)
	mockRS := mock_interfaces.NewMockResourceService(ctrl)
	service := &buildTaskService{cs: mockCS, rs: mockRS}

	mockRS.EXPECT().GetByID(gomock.Any(), "resource-1").
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

func TestCreateBuildTaskRejectsActiveTaskForResource(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockCS := mock_interfaces.NewMockCatalogService(ctrl)
	mockRS := mock_interfaces.NewMockResourceService(ctrl)
	mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
	service := &buildTaskService{cs: mockCS, rs: mockRS, bta: mockBTA}

	mockRS.EXPECT().GetByID(gomock.Any(), "resource-1").
		Return(&interfaces.Resource{
			ID:        "resource-1",
			CatalogID: "catalog-1",
			Category:  interfaces.ResourceCategoryTable,
		}, nil)
	mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
		Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
	mockBTA.EXPECT().List(gomock.Any(), gomock.Any()).
		Return([]*interfaces.BuildTask{{ID: "active-task", ResourceID: "resource-1", Status: interfaces.BuildTaskStatusRunning}}, int64(1), nil)

	_, err := service.CreateBuildTask(context.Background(), &interfaces.CreateBuildTaskRequest{
		ResourceID: "resource-1",
		Mode:       interfaces.BuildTaskModeBatch,
	})

	httpErr, ok := err.(*rest.HTTPError)
	if !ok {
		t.Fatalf("expected HTTPError, got %T", err)
	}
	if httpErr.BaseError.ErrorCode != verrors.VegaBackend_BuildTask_Exist {
		t.Fatalf("expected %s, got %s", verrors.VegaBackend_BuildTask_Exist, httpErr.BaseError.ErrorCode)
	}
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

func TestStartBuildTaskRejectsFullRebuildForCompletedTask(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
	service := &buildTaskService{bta: mockBTA}

	mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").
		Return(&interfaces.BuildTask{
			ID:     "task-1",
			Status: interfaces.BuildTaskStatusCompleted,
		}, nil)

	err := service.StartBuildTask(context.Background(), "task-1", interfaces.BuildTaskExecuteTypeFull)
	httpErr, ok := err.(*rest.HTTPError)
	if !ok {
		t.Fatalf("expected HTTPError, got %T", err)
	}
	if httpErr.BaseError.ErrorCode != verrors.VegaBackend_BuildTask_InvalidStateTransition {
		t.Fatalf("expected %s, got %s", verrors.VegaBackend_BuildTask_InvalidStateTransition, httpErr.BaseError.ErrorCode)
	}
}

func TestStartBuildTaskRejectsAnotherActiveTaskForResource(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockCS := mock_interfaces.NewMockCatalogService(ctrl)
	mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
	service := &buildTaskService{cs: mockCS, bta: mockBTA}

	mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").
		Return(&interfaces.BuildTask{
			ID:         "task-1",
			ResourceID: "resource-1",
			CatalogID:  "catalog-1",
			Status:     interfaces.BuildTaskStatusStopped,
		}, nil)
	mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
		Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
	mockBTA.EXPECT().List(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, params interfaces.BuildTasksQueryParams) ([]*interfaces.BuildTask, int64, error) {
			if params.ResourceID != "resource-1" {
				t.Fatalf("expected resource-1 active task lookup, got %q", params.ResourceID)
			}
			return []*interfaces.BuildTask{{
				ID:         "task-2",
				ResourceID: "resource-1",
				Status:     interfaces.BuildTaskStatusRunning,
			}}, 1, nil
		})

	err := service.StartBuildTask(context.Background(), "task-1", interfaces.BuildTaskExecuteTypeIncremental)
	httpErr, ok := err.(*rest.HTTPError)
	if !ok {
		t.Fatalf("expected HTTPError, got %T", err)
	}
	if httpErr.BaseError.ErrorCode != verrors.VegaBackend_BuildTask_Exist {
		t.Fatalf("expected %s, got %s", verrors.VegaBackend_BuildTask_Exist, httpErr.BaseError.ErrorCode)
	}
}

func TestStartBuildTaskAllowsInitTaskItselfAsActive(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockCS := mock_interfaces.NewMockCatalogService(ctrl)
	mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
	neutralizeEnqueue(t, ctrl)
	service := &buildTaskService{cs: mockCS, bta: mockBTA}

	task := &interfaces.BuildTask{
		ID:         "task-1",
		ResourceID: "resource-1",
		CatalogID:  "catalog-1",
		Mode:       interfaces.BuildTaskModeBatch,
		Status:     interfaces.BuildTaskStatusInit,
	}
	mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").Return(task, nil)
	mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
		Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
	mockBTA.EXPECT().List(gomock.Any(), gomock.Any()).
		Return([]*interfaces.BuildTask{task}, int64(1), nil)

	if err := service.StartBuildTask(context.Background(), "task-1", interfaces.BuildTaskExecuteTypeIncremental); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// running → stopping：正常停止路径。
func TestStopBuildTaskRunningToStopping(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
	service := &buildTaskService{bta: mockBTA}

	mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").
		Return(&interfaces.BuildTask{ID: "task-1", Status: interfaces.BuildTaskStatusRunning}, nil)
	mockBTA.EXPECT().UpdateStatus(gomock.Any(), "task-1",
		interfaces.NewBuildTaskUpdate().WithStatus(interfaces.BuildTaskStatusStopping)).Return(true, nil)

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
		interfaces.NewBuildTaskUpdate().WithStatus(interfaces.BuildTaskStatusStopped)).Return(true, nil)

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
			bt:            interfaces.BuildTask{Status: "completed", IndexConfig: testBuildTaskIndexConfig(true, true), SyncedCount: 6, VectorizedCount: 0},
			wantEmbedding: "failed", wantFulltext: "ok", wantUsable: false,
		},
		{
			name:          "embedding partial -> partial, unusable",
			bt:            interfaces.BuildTask{Status: "completed", IndexConfig: testBuildTaskIndexConfig(true, false), SyncedCount: 6, VectorizedCount: 4},
			wantEmbedding: "partial", wantFulltext: "none", wantUsable: false,
		},
		{
			name:          "embedding full -> ok, usable",
			bt:            interfaces.BuildTask{Status: "completed", IndexConfig: testBuildTaskIndexConfig(true, false), SyncedCount: 6, VectorizedCount: 6},
			wantEmbedding: "ok", wantFulltext: "none", wantUsable: true,
		},
		{
			name:          "no embedding requested -> none, usable",
			bt:            interfaces.BuildTask{Status: "completed", IndexConfig: testBuildTaskIndexConfig(false, true), SyncedCount: 6},
			wantEmbedding: "none", wantFulltext: "ok", wantUsable: true,
		},
		{
			name:          "running -> building, not usable yet",
			bt:            interfaces.BuildTask{Status: "running", IndexConfig: testBuildTaskIndexConfig(true, false), SyncedCount: 6, VectorizedCount: 2},
			wantEmbedding: "building", wantFulltext: "none", wantUsable: false,
		},
		{
			name:          "empty table -> ok, usable",
			bt:            interfaces.BuildTask{Status: "completed", IndexConfig: testBuildTaskIndexConfig(true, false), SyncedCount: 0, VectorizedCount: 0},
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

func testBuildTaskIndexConfig(vector bool, fulltext bool) *interfaces.BuildTaskIndexConfig {
	feature := interfaces.BuildTaskFieldIndexFeature{}
	if vector {
		feature.Vector = &interfaces.BuildTaskEmbeddingConfig{ModelID: "m1", Dimensions: 1024}
	}
	if fulltext {
		feature.Fulltext = &interfaces.BuildTaskFulltextConfig{Analyzer: "ik_max_word"}
	}
	return &interfaces.BuildTaskIndexConfig{
		Features: map[string]interfaces.BuildTaskFieldIndexFeature{"name": feature},
	}
}

// 删任务应连带 drop 其 OpenSearch 索引（与删资源/删 catalog 级联语义一致）。
func TestDeleteBuildTasks_DropsIndexAndRow(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
	mockRS := mock_interfaces.NewMockResourceService(ctrl)
	mockLIM := mock_interfaces.NewMockLocalIndexManager(ctrl)
	service := &buildTaskService{bta: mockBTA, rs: mockRS, lim: mockLIM}

	mockBTA.EXPECT().GetByID(gomock.Any(), "t1").
		Return(&interfaces.BuildTask{ID: "t1", ResourceID: "r1", Status: "completed"}, nil)
	mockRS.EXPECT().GetByID(gomock.Any(), "r1").
		Return(&interfaces.Resource{ID: "r1", LocalIndexName: interfaces.BuildIndexName("r1", "old-task")}, nil)
	mockLIM.EXPECT().DeleteIndex(gomock.Any(), interfaces.BuildIndexName("r1", "t1")).Return(nil)
	mockBTA.EXPECT().Delete(gomock.Any(), "t1").Return(nil)

	if err := service.DeleteBuildTasks(context.Background(), []string{"t1"}, false, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDeleteBuildTasks_RefusesActiveLocalIndex(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
	mockRS := mock_interfaces.NewMockResourceService(ctrl)
	mockLIM := mock_interfaces.NewMockLocalIndexManager(ctrl)
	service := &buildTaskService{bta: mockBTA, rs: mockRS, lim: mockLIM}

	idx := interfaces.BuildIndexName("r1", "t1")
	mockBTA.EXPECT().GetByID(gomock.Any(), "t1").
		Return(&interfaces.BuildTask{ID: "t1", ResourceID: "r1", Status: interfaces.BuildTaskStatusCompleted}, nil)
	mockRS.EXPECT().GetByID(gomock.Any(), "r1").
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
	mockRS := mock_interfaces.NewMockResourceService(ctrl)
	mockLIM := mock_interfaces.NewMockLocalIndexManager(ctrl)
	service := &buildTaskService{bta: mockBTA, rs: mockRS, lim: mockLIM}

	idx := interfaces.BuildIndexName("r1", "t1")
	resource := &interfaces.Resource{ID: "r1", LocalIndexName: idx}
	mockBTA.EXPECT().GetByID(gomock.Any(), "t1").
		Return(&interfaces.BuildTask{ID: "t1", ResourceID: "r1", Status: interfaces.BuildTaskStatusCompleted}, nil)
	mockRS.EXPECT().GetByID(gomock.Any(), "r1").Return(resource, nil)
	mockRS.EXPECT().UpdateResource(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, got *interfaces.Resource) error {
			if got.ID != "r1" {
				t.Fatalf("expected resource r1, got %s", got.ID)
			}
			if got.LocalIndexName != "" {
				t.Fatalf("expected LocalIndexName to be cleared, got %q", got.LocalIndexName)
			}
			return nil
		})
	mockLIM.EXPECT().DeleteIndex(gomock.Any(), idx).Return(nil)
	mockBTA.EXPECT().Delete(gomock.Any(), "t1").Return(nil)

	if err := service.DeleteBuildTasks(context.Background(), []string{"t1"}, false, true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDeleteBuildTasks_ClearActiveLocalIndexFailureBlocksDeletion(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
	mockRS := mock_interfaces.NewMockResourceService(ctrl)
	mockLIM := mock_interfaces.NewMockLocalIndexManager(ctrl)
	service := &buildTaskService{bta: mockBTA, rs: mockRS, lim: mockLIM}

	idx := interfaces.BuildIndexName("r1", "t1")
	mockBTA.EXPECT().GetByID(gomock.Any(), "t1").
		Return(&interfaces.BuildTask{ID: "t1", ResourceID: "r1", Status: interfaces.BuildTaskStatusCompleted}, nil)
	mockRS.EXPECT().GetByID(gomock.Any(), "r1").
		Return(&interfaces.Resource{ID: "r1", LocalIndexName: idx}, nil)
	mockRS.EXPECT().UpdateResource(gomock.Any(), gomock.Any()).Return(errors.New("update failed"))
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
	mockRS := mock_interfaces.NewMockResourceService(ctrl)
	mockLIM := mock_interfaces.NewMockLocalIndexManager(ctrl)
	service := &buildTaskService{bta: mockBTA, rs: mockRS, lim: mockLIM}

	mockBTA.EXPECT().GetByID(gomock.Any(), "t1").
		Return(&interfaces.BuildTask{ID: "t1", ResourceID: "missing-resource", Status: interfaces.BuildTaskStatusFailed}, nil)
	mockRS.EXPECT().GetByID(gomock.Any(), "missing-resource").Return(nil, nil)
	mockLIM.EXPECT().DeleteIndex(gomock.Any(), interfaces.BuildIndexName("missing-resource", "t1")).Return(nil)
	mockBTA.EXPECT().Delete(gomock.Any(), "t1").Return(nil)

	if err := service.DeleteBuildTasks(context.Background(), []string{"t1"}, false, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDeleteBuildTasks_ResourceLookupFailureBlocksDeletion(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
	mockRS := mock_interfaces.NewMockResourceService(ctrl)
	mockLIM := mock_interfaces.NewMockLocalIndexManager(ctrl)
	service := &buildTaskService{bta: mockBTA, rs: mockRS, lim: mockLIM}

	mockBTA.EXPECT().GetByID(gomock.Any(), "t1").
		Return(&interfaces.BuildTask{ID: "t1", ResourceID: "r1", Status: interfaces.BuildTaskStatusStopped}, nil)
	mockRS.EXPECT().GetByID(gomock.Any(), "r1").Return(nil, errors.New("db unavailable"))
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
	mockLIM := mock_interfaces.NewMockLocalIndexManager(ctrl)
	service := &buildTaskService{bta: mockBTA, lim: mockLIM}

	mockBTA.EXPECT().GetByID(gomock.Any(), "t1").
		Return(&interfaces.BuildTask{ID: "t1", ResourceID: "r1", Status: "running"}, nil)
	// 不应调用 local index delete / bta.Delete

	if err := service.DeleteBuildTasks(context.Background(), []string{"t1"}, false, true); err == nil {
		t.Fatalf("expected 409 when a task is running")
	}
}

// neutralizeEnqueue 让 CreateBuildTask/StartBuildTask 末尾的 enqueueBuildTask
// 不 panic：CreateClient 返回真实但指向不可达 redis 的 client，Enqueue 会失败，
// 而 enqueueBuildTask 对入队失败仅记日志、不影响返回值。
func neutralizeEnqueue(t *testing.T, ctrl *gomock.Controller) {
	t.Helper()
	mockAQA := mock_interfaces.NewMockAsynqAccess(ctrl)
	client := asynq.NewClient(asynq.RedisClientOpt{Addr: "127.0.0.1:0"})
	mockAQA.EXPECT().CreateClient().Return(client).AnyTimes()
	prev := logics.AQA
	logics.AQA = mockAQA
	t.Cleanup(func() {
		logics.AQA = prev
		_ = client.Close()
	})
}

func TestCreateBuildTaskNormalizesModelNameToID(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockCS := mock_interfaces.NewMockCatalogService(ctrl)
	mockRS := mock_interfaces.NewMockResourceService(ctrl)
	mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
	mockMFA := mock_interfaces.NewMockModelFactoryAccess(ctrl)
	neutralizeEnqueue(t, ctrl)
	service := &buildTaskService{cs: mockCS, rs: mockRS, bta: mockBTA, mfa: mockMFA}

	mockRS.EXPECT().GetByID(gomock.Any(), "resource-1").
		Return(&interfaces.Resource{
			ID:        "resource-1",
			CatalogID: "catalog-1",
			Category:  interfaces.ResourceCategoryTable,
			IndexConfig: &interfaces.ResourceIndexConfig{
				BuildKeyFields:          []string{"id"},
				DefaultEmbeddingModel:   "text-embedding-v4",
				DefaultFulltextAnalyzer: "ik_max_word",
			},
			SchemaDefinition: []*interfaces.Property{
				{
					Name: "family_name",
					Features: []interfaces.PropertyFeature{
						{FeatureType: interfaces.PropertyFeatureType_Vector, RefProperty: "family_name"},
						{FeatureType: interfaces.PropertyFeatureType_Fulltext, RefProperty: "family_name"},
					},
				},
				{
					Name: "given_name",
					Features: []interfaces.PropertyFeature{
						{FeatureType: interfaces.PropertyFeatureType_Vector, RefProperty: "given_name"},
					},
				},
			},
		}, nil)
	mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
		Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
	mockBTA.EXPECT().List(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, params interfaces.BuildTasksQueryParams) ([]*interfaces.BuildTask, int64, error) {
			if params.ResourceID != "resource-1" {
				t.Fatalf("expected resource-1 active task lookup, got %q", params.ResourceID)
			}
			return nil, 0, nil
		})
	mockMFA.EXPECT().GetModelByName(gomock.Any(), "text-embedding-v4").
		Return(&interfaces.SmallModel{ModelID: "2064382281006583808", ModelName: "text-embedding-v4", EmbeddingDim: 1024}, nil)

	var captured *interfaces.BuildTask
	mockBTA.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, bt *interfaces.BuildTask) error {
			captured = bt
			return nil
		})

	_, err := service.CreateBuildTask(context.Background(), &interfaces.CreateBuildTaskRequest{
		ResourceID: "resource-1",
		Mode:       interfaces.BuildTaskModeBatch,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if captured == nil {
		t.Fatal("bta.Create was not called")
	}
	require.NotNil(t, captured.IndexConfig)
	assert.Equal(t, []string{"id"}, captured.IndexConfig.BuildKeyFields)
	assert.Equal(t, &interfaces.BuildTaskEmbeddingConfig{ModelID: "2064382281006583808", Dimensions: 1024}, captured.IndexConfig.Features["family_name"].Vector)
	assert.Equal(t, &interfaces.BuildTaskEmbeddingConfig{ModelID: "2064382281006583808", Dimensions: 1024}, captured.IndexConfig.Features["given_name"].Vector)
	assert.Equal(t, &interfaces.BuildTaskFulltextConfig{Analyzer: "ik_max_word"}, captured.IndexConfig.Features["family_name"].Fulltext)
}

func TestCreateBuildTaskUsesFeatureEmbeddingModelOverride(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockCS := mock_interfaces.NewMockCatalogService(ctrl)
	mockRS := mock_interfaces.NewMockResourceService(ctrl)
	mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
	mockMFA := mock_interfaces.NewMockModelFactoryAccess(ctrl)
	neutralizeEnqueue(t, ctrl)
	service := &buildTaskService{cs: mockCS, rs: mockRS, bta: mockBTA, mfa: mockMFA}

	mockRS.EXPECT().GetByID(gomock.Any(), "resource-1").
		Return(&interfaces.Resource{
			ID:        "resource-1",
			CatalogID: "catalog-1",
			Category:  interfaces.ResourceCategoryTable,
			IndexConfig: &interfaces.ResourceIndexConfig{
				DefaultEmbeddingModel: "default-model",
			},
			SchemaDefinition: []*interfaces.Property{
				{
					Name: "family_name",
					Features: []interfaces.PropertyFeature{
						{
							FeatureType: interfaces.PropertyFeatureType_Vector,
							RefProperty: "family_name",
							Config:      map[string]any{"embedding_model": "text-embedding-v4"},
						},
					},
				},
			},
		}, nil)
	mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
		Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
	mockBTA.EXPECT().List(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, params interfaces.BuildTasksQueryParams) ([]*interfaces.BuildTask, int64, error) {
			if params.ResourceID != "resource-1" {
				t.Fatalf("expected resource-1 active task lookup, got %q", params.ResourceID)
			}
			return nil, 0, nil
		})
	mockMFA.EXPECT().GetModelByName(gomock.Any(), "text-embedding-v4").
		Return(&interfaces.SmallModel{ModelID: "2064382281006583808", ModelName: "text-embedding-v4", EmbeddingDim: 1024}, nil)

	var captured *interfaces.BuildTask
	mockBTA.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, bt *interfaces.BuildTask) error {
			captured = bt
			return nil
		})

	_, err := service.CreateBuildTask(context.Background(), &interfaces.CreateBuildTaskRequest{
		ResourceID: "resource-1",
		Mode:       interfaces.BuildTaskModeBatch,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if captured == nil {
		t.Fatal("bta.Create was not called")
	}
	require.NotNil(t, captured.IndexConfig)
	assert.Equal(t, &interfaces.BuildTaskEmbeddingConfig{ModelID: "2064382281006583808", Dimensions: 1024}, captured.IndexConfig.Features["family_name"].Vector)
}

func TestCreateBuildTaskErrorsWhenModelUnresolvableAndNoDimensions(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockCS := mock_interfaces.NewMockCatalogService(ctrl)
	mockRS := mock_interfaces.NewMockResourceService(ctrl)
	mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
	mockMFA := mock_interfaces.NewMockModelFactoryAccess(ctrl)
	service := &buildTaskService{cs: mockCS, rs: mockRS, bta: mockBTA, mfa: mockMFA}

	mockRS.EXPECT().GetByID(gomock.Any(), "resource-1").
		Return(&interfaces.Resource{
			ID:        "resource-1",
			CatalogID: "catalog-1",
			Category:  interfaces.ResourceCategoryTable,
			IndexConfig: &interfaces.ResourceIndexConfig{
				DefaultEmbeddingModel: "bogus-model",
			},
			SchemaDefinition: []*interfaces.Property{
				{
					Name: "family_name",
					Features: []interfaces.PropertyFeature{
						{FeatureType: interfaces.PropertyFeatureType_Vector, RefProperty: "family_name"},
					},
				},
			},
		}, nil)
	mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
		Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
	mockBTA.EXPECT().List(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, params interfaces.BuildTasksQueryParams) ([]*interfaces.BuildTask, int64, error) {
			if params.ResourceID != "resource-1" {
				t.Fatalf("expected resource-1 active task lookup, got %q", params.ResourceID)
			}
			return nil, 0, nil
		})
	mockMFA.EXPECT().GetModelByName(gomock.Any(), "bogus-model").
		Return(nil, fmt.Errorf("model not found"))

	_, err := service.CreateBuildTask(context.Background(), &interfaces.CreateBuildTaskRequest{
		ResourceID: "resource-1",
		Mode:       interfaces.BuildTaskModeBatch,
	})
	if err == nil {
		t.Fatal("expected error for unresolvable model without dimensions")
	}
	httpErr, ok := err.(*rest.HTTPError)
	if !ok {
		t.Fatalf("expected HTTPError, got %T", err)
	}
	if httpErr.HTTPCode != http.StatusInternalServerError {
		t.Fatalf("expected HTTP 500, got %d", httpErr.HTTPCode)
	}
}
