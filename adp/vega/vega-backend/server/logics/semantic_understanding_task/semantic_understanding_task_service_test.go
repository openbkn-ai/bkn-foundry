// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package semantic_understanding_task

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/interfaces"
	mock_interfaces "vega-backend/interfaces/mock"
)

func TestBuildCatalogSemanticUnderstandingInput(t *testing.T) {
	threshold := 0.75
	input, _, err := buildCatalogSemanticUnderstandingInput(
		&interfaces.Catalog{ID: "catalog-1", Name: "电商目录", Description: "电商业务资源"},
		[]*interfaces.Resource{
			{
				ID:               "logic-view-1",
				Name:             "订单汇总",
				SourceIdentifier: "order_summary",
				Description:      "订单统计逻辑视图",
				Status:           interfaces.ResourceStatusActive,
				Category:         interfaces.ResourceCategoryLogicView,
				LogicDefinition:  []*interfaces.LogicDefinitionNode{{ID: "hidden"}},
			},
			{
				ID:               "resource-1",
				Name:             "订单",
				SourceIdentifier: "public.orders",
				Description:      "销售订单资源",
				Category:         interfaces.ResourceCategoryTable,
				Database:         "ecommerce",
				SourceMetadata: map[string]any{
					"primary_keys": []any{"order_id"},
					"indices": []any{
						map[string]any{"unique": true, "primary": false, "columns": []any{"order_no"}},
						map[string]any{"unique": true, "primary": true, "columns": []any{"order_id"}},
					},
				},
				SchemaDefinition: []*interfaces.Property{{
					Name:                "order_id",
					DisplayName:         "订单ID",
					Type:                interfaces.DataType_Integer,
					Description:         "销售订单唯一标识",
					OriginalName:        "legacy_order_id",
					OriginalType:        "int8",
					OriginalDescription: "旧字段说明",
				}},
			},
		},
		&interfaces.CreateSemanticUnderstandingTaskRequest{
			ApplyMode:           interfaces.SemanticUnderstandingApplyModeDryRun,
			ConfidenceThreshold: &threshold,
		},
	)

	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(input), &got))
	assert.Equal(t, "电商业务资源", got["catalog"].(map[string]any)["description"])

	resources := got["resources"].([]any)
	require.Len(t, resources, 1)
	resource := resources[0].(map[string]any)
	assert.NotContains(t, resource, "schema_definition")
	fields := resource["fields"].([]any)
	require.Len(t, fields, 1)
	field := fields[0].(map[string]any)
	assert.Equal(t, "订单ID", field["display_name"])
	assert.NotContains(t, field, "original_name")
	assert.NotContains(t, field, "original_type")
	assert.NotContains(t, field, "original_description")

	keys := resource["keys"].(map[string]any)
	assert.Equal(t, []any{"order_id"}, keys["primary"])
	assert.Equal(t, []any{[]any{"order_no"}}, keys["unique"])

	logicViews := got["existing_logic_views"].([]any)
	require.Len(t, logicViews, 1)
	logicView := logicViews[0].(map[string]any)
	assert.Equal(t, "order_summary", logicView["source_identifier"])
	assert.NotContains(t, logicView, "logic_definition")
}

func TestSemanticUnderstandingTaskServiceCreate(t *testing.T) {
	t.Run("creates pending resource task", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		taskAccess := mock_interfaces.NewMockSemanticUnderstandingTaskAccess(ctrl)
		resourceService := mock_interfaces.NewMockResourceService(ctrl)
		service := &semanticUnderstandingTaskService{
			suta:           taskAccess,
			rs:             resourceService,
			debugTaskQueue: make(chan *asynq.Task, 1),
		}
		ctx := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{ID: "u1", Type: interfaces.ACCESSOR_TYPE_USER})
		var createdTask *interfaces.SemanticUnderstandingTask
		var findHash string

		resourceService.EXPECT().InternalGetByID(gomock.Any(), "resource-1").Return(sampleSemanticResource(), nil)
		taskAccess.EXPECT().
			FindActiveByInputHash(gomock.Any(), interfaces.SemanticUnderstandingTaskScopeResource, gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, inputHash string) (*interfaces.SemanticUnderstandingTask, error) {
				findHash = inputHash
				return nil, nil
			})
		taskAccess.EXPECT().
			Create(gomock.Any(), gomock.AssignableToTypeOf(&interfaces.SemanticUnderstandingTask{})).
			DoAndReturn(func(_ context.Context, task *interfaces.SemanticUnderstandingTask) error {
				createdTask = task
				return nil
			})

		got, err := service.CreateResourceTask(ctx, "resource-1", &interfaces.CreateSemanticUnderstandingTaskRequest{
			ApplyMode: interfaces.SemanticUnderstandingApplyModeFillEmpty,
		})

		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Same(t, createdTask, got)
		assert.Equal(t, interfaces.SemanticUnderstandingTaskScopeResource, got.Scope)
		assert.Equal(t, "catalog-1", got.CatalogID)
		assert.Equal(t, "resource-1", got.ResourceID)
		assert.Equal(t, interfaces.SemanticUnderstandingTaskStatusPending, got.Status)
		assert.Equal(t, interfaces.SemanticUnderstandingResourceAgentID, got.AgentID)
		assert.Equal(t, "u1", got.Creator.ID)
		assert.NotEmpty(t, got.Input)
		assert.NotEmpty(t, got.InputHash)
		assert.Equal(t, got.InputHash, findHash)

		select {
		case queuedTask := <-service.DebugTaskQueue():
			assert.Equal(t, interfaces.SemanticUnderstandingTaskType, queuedTask.Type())
		case <-time.After(time.Second):
			t.Fatal("semantic understanding task was not enqueued")
		}
	})

	t.Run("marks task failed when enqueue fails", func(t *testing.T) {
		t.Setenv("DEBUG_MODE", "false")
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		taskAccess := mock_interfaces.NewMockSemanticUnderstandingTaskAccess(ctrl)
		client := asynq.NewClient(asynq.RedisClientOpt{
			Addr:        "127.0.0.1:0",
			DialTimeout: time.Millisecond,
		})
		t.Cleanup(func() { _ = client.Close() })
		service := &semanticUnderstandingTaskService{
			client: client,
			suta:   taskAccess,
		}

		taskAccess.EXPECT().
			FindActiveByInputHash(gomock.Any(), interfaces.SemanticUnderstandingTaskScopeResource, "input-hash").
			Return(nil, nil)
		taskAccess.EXPECT().
			Create(gomock.Any(), gomock.AssignableToTypeOf(&interfaces.SemanticUnderstandingTask{})).
			Return(nil)
		taskAccess.EXPECT().
			MarkFailed(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, failureDetail string) (bool, error) {
				assert.Contains(t, failureDetail, "failed to enqueue task")
				return true, nil
			})

		got, err := service.createTask(context.Background(), &interfaces.SemanticUnderstandingTask{
			Scope:     interfaces.SemanticUnderstandingTaskScopeResource,
			InputHash: "input-hash",
		})

		require.Error(t, err)
		assert.Nil(t, got)
	})

	t.Run("reuses active task with same input hash", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		active := &interfaces.SemanticUnderstandingTask{ID: "semantic-task-1"}
		taskAccess := mock_interfaces.NewMockSemanticUnderstandingTaskAccess(ctrl)
		catalogService := mock_interfaces.NewMockCatalogService(ctrl)
		resourceService := mock_interfaces.NewMockResourceService(ctrl)
		service := &semanticUnderstandingTaskService{suta: taskAccess, cs: catalogService, rs: resourceService}
		var findScope string

		catalogService.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", false).Return(&interfaces.Catalog{ID: "catalog-1", Name: "sales"}, nil)
		resourceService.EXPECT().InternalGetByCatalogID(gomock.Any(), "catalog-1").Return([]*interfaces.Resource{sampleSemanticResource()}, nil)
		taskAccess.EXPECT().
			FindActiveByInputHash(gomock.Any(), interfaces.SemanticUnderstandingTaskScopeCatalog, gomock.Any()).
			DoAndReturn(func(_ context.Context, scope string, _ string) (*interfaces.SemanticUnderstandingTask, error) {
				findScope = scope
				return active, nil
			})

		got, err := service.CreateCatalogTask(context.Background(), "catalog-1", &interfaces.CreateSemanticUnderstandingTaskRequest{
			ApplyMode: interfaces.SemanticUnderstandingApplyModeDryRun,
		})

		require.NoError(t, err)
		assert.Same(t, active, got)
		assert.Equal(t, interfaces.SemanticUnderstandingTaskScopeCatalog, findScope)
	})
}

func TestSemanticUnderstandingTaskServiceStatusUpdates(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	taskAccess := mock_interfaces.NewMockSemanticUnderstandingTaskAccess(ctrl)
	service := &semanticUnderstandingTaskService{suta: taskAccess}

	taskAccess.EXPECT().
		MarkRunning(gomock.Any(), "semantic-task-1", "agent-task-1").
		Return(true, nil)

	running, err := service.MarkRunning(context.Background(), "semantic-task-1", "agent-task-1")
	require.NoError(t, err)
	assert.True(t, running)

	taskAccess.EXPECT().
		MarkSucceeded(gomock.Any(), "semantic-task-1", `{"confidence":0.8}`, 0.8, `{"fields":[]}`).
		Return(true, nil)

	succeeded, err := service.MarkSucceeded(context.Background(), "semantic-task-1", `{"confidence":0.8}`, 0.8, `{"fields":[]}`)
	require.NoError(t, err)
	assert.True(t, succeeded)
}

func TestSemanticUnderstandingTaskServiceGetByID(t *testing.T) {
	t.Run("enriches creator name", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		taskAccess := mock_interfaces.NewMockSemanticUnderstandingTaskAccess(ctrl)
		userMgmtService := mock_interfaces.NewMockUserMgmtService(ctrl)
		service := &semanticUnderstandingTaskService{suta: taskAccess, ums: userMgmtService}
		task := &interfaces.SemanticUnderstandingTask{
			ID:      "semantic-task-1",
			Creator: interfaces.AccountInfo{ID: "u1", Type: interfaces.ACCESSOR_TYPE_USER},
		}

		taskAccess.EXPECT().GetByID(gomock.Any(), "semantic-task-1").Return(task, nil)
		userMgmtService.EXPECT().
			GetAccountNames(gomock.Any(), []*interfaces.AccountInfo{&task.Creator}).
			DoAndReturn(func(_ context.Context, accountInfos []*interfaces.AccountInfo) error {
				accountInfos[0].Name = "Alice"
				return nil
			})

		got, err := service.GetByID(context.Background(), "semantic-task-1")

		require.NoError(t, err)
		require.Same(t, task, got)
		assert.Equal(t, "Alice", got.Creator.Name)
	})

	t.Run("returns not found when task is missing", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		taskAccess := mock_interfaces.NewMockSemanticUnderstandingTaskAccess(ctrl)
		service := &semanticUnderstandingTaskService{suta: taskAccess}

		taskAccess.EXPECT().GetByID(gomock.Any(), "missing").Return(nil, nil)

		got, err := service.GetByID(context.Background(), "missing")

		assert.Nil(t, got)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "NotFound")
	})
}

func TestSemanticUnderstandingTaskServicePopulatesReferenceNames(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	taskAccess := mock_interfaces.NewMockSemanticUnderstandingTaskAccess(ctrl)
	catalogService := mock_interfaces.NewMockCatalogService(ctrl)
	resourceService := mock_interfaces.NewMockResourceService(ctrl)
	userMgmtService := mock_interfaces.NewMockUserMgmtService(ctrl)
	service := &semanticUnderstandingTaskService{
		suta: taskAccess,
		cs:   catalogService,
		rs:   resourceService,
		ums:  userMgmtService,
	}

	t.Run("list batches current page reference ids", func(t *testing.T) {
		tasks := []*interfaces.SemanticUnderstandingTask{
			{ID: "task-1", CatalogID: "catalog-1", ResourceID: "resource-1"},
			{ID: "task-2", CatalogID: "catalog-1", ResourceID: "resource-1"},
		}
		taskAccess.EXPECT().List(gomock.Any(), gomock.Any()).Return(tasks, int64(2), nil)
		resourceService.EXPECT().InternalGetByIDs(gomock.Any(), []string{"resource-1"}).Return([]*interfaces.Resource{{ID: "resource-1", Name: "资源一"}}, nil)
		catalogService.EXPECT().InternalGetByIDs(gomock.Any(), []string{"catalog-1"}).Return([]*interfaces.Catalog{{ID: "catalog-1", Name: "目录一"}}, nil)

		got, _, err := service.List(context.Background(), interfaces.SemanticUnderstandingTaskQueryParams{})

		require.NoError(t, err)
		assert.Equal(t, "资源一", got[0].ResourceName)
		assert.Equal(t, "目录一", got[1].CatalogName)
	})

	t.Run("get populates reference names", func(t *testing.T) {
		task := &interfaces.SemanticUnderstandingTask{ID: "task-3", CatalogID: "catalog-2", ResourceID: "resource-2"}
		taskAccess.EXPECT().GetByID(gomock.Any(), "task-3").Return(task, nil)
		resourceService.EXPECT().InternalGetByIDs(gomock.Any(), []string{"resource-2"}).Return([]*interfaces.Resource{{ID: "resource-2", Name: "资源二"}}, nil)
		catalogService.EXPECT().InternalGetByIDs(gomock.Any(), []string{"catalog-2"}).Return([]*interfaces.Catalog{{ID: "catalog-2", Name: "目录二"}}, nil)
		userMgmtService.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil)

		got, err := service.GetByID(context.Background(), "task-3")

		require.NoError(t, err)
		assert.Equal(t, "资源二", got.ResourceName)
		assert.Equal(t, "目录二", got.CatalogName)
	})
}

func TestSemanticUnderstandingTaskServiceDelete(t *testing.T) {
	t.Run("deletes completed tasks and ignores missing ids", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		taskAccess := mock_interfaces.NewMockSemanticUnderstandingTaskAccess(ctrl)
		service := &semanticUnderstandingTaskService{suta: taskAccess}

		taskAccess.EXPECT().
			GetByIDs(gomock.Any(), []string{"task-1", "missing", "task-2"}).
			Return([]*interfaces.SemanticUnderstandingTask{
				{ID: "task-1", Status: interfaces.SemanticUnderstandingTaskStatusSucceeded},
				{ID: "task-2", Status: interfaces.SemanticUnderstandingTaskStatusFailed},
			}, nil)
		taskAccess.EXPECT().
			DeleteByIDs(gomock.Any(), []string{"task-1", "task-2"}).
			Return(int64(2), nil)

		err := service.Delete(context.Background(), []string{"task-1", "task-1", "missing", "task-2"}, true)

		require.NoError(t, err)
	})

	t.Run("rejects pending or running tasks", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		taskAccess := mock_interfaces.NewMockSemanticUnderstandingTaskAccess(ctrl)
		service := &semanticUnderstandingTaskService{suta: taskAccess}

		taskAccess.EXPECT().
			GetByIDs(gomock.Any(), []string{"task-1", "task-2"}).
			Return([]*interfaces.SemanticUnderstandingTask{
				{ID: "task-1", Status: interfaces.SemanticUnderstandingTaskStatusPending},
				{ID: "task-2", Status: interfaces.SemanticUnderstandingTaskStatusSucceeded},
			}, nil)

		err := service.Delete(context.Background(), []string{"task-1", "task-2"}, false)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "HasRunningExecution")
	})

	t.Run("rejects missing tasks when ignore missing is false", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		taskAccess := mock_interfaces.NewMockSemanticUnderstandingTaskAccess(ctrl)
		service := &semanticUnderstandingTaskService{suta: taskAccess}

		taskAccess.EXPECT().
			GetByIDs(gomock.Any(), []string{"task-1", "missing"}).
			Return([]*interfaces.SemanticUnderstandingTask{
				{ID: "task-1", Status: interfaces.SemanticUnderstandingTaskStatusSucceeded},
			}, nil)

		err := service.Delete(context.Background(), []string{"task-1", "missing"}, false)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "NotFound")
		assert.Contains(t, err.Error(), "missing")
	})
}

func TestNormalizeResourceSemanticUnderstandingRequest(t *testing.T) {
	t.Run("defaults optional values", func(t *testing.T) {
		got, err := normalizeResourceSemanticUnderstandingRequest(sampleSemanticResource(), nil)

		require.NoError(t, err)
		assert.Equal(t, interfaces.SemanticUnderstandingTaskScopeResource, got.Scope)
		assert.Equal(t, "catalog-1", got.CatalogID)
		assert.Equal(t, "resource-1", got.ResourceID)
		assert.Equal(t, interfaces.SemanticUnderstandingApplyModeFillEmpty, got.ApplyMode)
		assert.Equal(t, interfaces.DefaultSemanticUnderstandingConfidenceThreshold, got.ConfidenceThreshold)
		assert.NotEmpty(t, got.Input)
		assert.NotEmpty(t, got.InputHash)
	})

	t.Run("requires masked sample policy when including samples", func(t *testing.T) {
		_, err := normalizeResourceSemanticUnderstandingRequest(sampleSemanticResource(), &interfaces.CreateSemanticUnderstandingTaskRequest{
			IncludeSampleRows: true,
			SamplePolicy:      &interfaces.SemanticUnderstandingSamplePolicy{Masked: false, MaxRows: 20},
		})

		require.Error(t, err)
		assert.ErrorContains(t, err, "masked")
	})
}

func sampleSemanticResource() *interfaces.Resource {
	return &interfaces.Resource{
		ID:               "resource-1",
		CatalogID:        "catalog-1",
		Name:             "orders",
		Category:         interfaces.ResourceCategoryTable,
		Database:         "sales",
		SourceIdentifier: "orders",
		Description:      "order table",
		SchemaDefinition: []*interfaces.Property{
			{
				Name:                "order_id",
				Type:                interfaces.DataType_String,
				OriginalName:        "order_id",
				OriginalType:        "varchar",
				OriginalDescription: "order id",
			},
		},
	}
}
