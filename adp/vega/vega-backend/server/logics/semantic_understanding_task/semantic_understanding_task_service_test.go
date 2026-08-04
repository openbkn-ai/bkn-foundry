// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package semantic_understanding_task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	"github.com/hibiken/asynq"
	"github.com/openbkn-ai/bkn-comm-go/rest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	verrors "github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/interfaces"
	mock_interfaces "github.com/openbkn-ai/bkn-foundry/adp/vega/vega-backend/server/interfaces/mock"
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
				Schema:           "ecommerce",
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
	assert.Equal(t, "ecommerce", resource["schema"])
	assert.NotContains(t, resource, "database")
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

func TestMarshalSemanticUnderstandingInput(t *testing.T) {
	first := make(map[string]any)
	first["resource"] = "orders"
	first["description"] = "orders < archived"

	second := make(map[string]any)
	second["description"] = "orders < archived"
	second["resource"] = "orders"

	firstJSON, firstHash, err := marshalSemanticUnderstandingInput(first)
	require.NoError(t, err)
	secondJSON, secondHash, err := marshalSemanticUnderstandingInput(second)
	require.NoError(t, err)

	assert.Equal(t, firstJSON, secondJSON)
	assert.Equal(t, firstHash, secondHash)
	assert.Contains(t, firstJSON, `\u003c`)
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

func TestSemanticUnderstandingTaskSampleRows(t *testing.T) {
	t.Run("writes queried rows to the task input and honors max rows", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		resourceDataService := mock_interfaces.NewMockResourceDataService(ctrl)
		resource := sampleSemanticResource()
		task, err := normalizeResourceSemanticUnderstandingRequest(resource, &interfaces.CreateSemanticUnderstandingTaskRequest{
			IncludeSampleRows: true,
			SamplePolicy:      &interfaces.SemanticUnderstandingSamplePolicy{Masked: false, MaxRows: 2},
		})
		require.NoError(t, err)
		inputHash := task.InputHash
		resourceDataService.EXPECT().
			QueryWithPaging(gomock.Any(), resource, gomock.Any()).
			DoAndReturn(func(_ context.Context, _ *interfaces.Resource, params *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
				assert.Equal(t, 2, params.Limit)
				assert.Equal(t, []string{"order_id"}, params.OutputFields)
				return &interfaces.ResourceDataQueryResult{Entries: []map[string]any{{"order_id": "o-1"}, {"order_id": "o-2"}}}, nil
			})

		service := &semanticUnderstandingTaskService{rds: resourceDataService}
		require.NoError(t, service.attachUnmaskedSampleRows(context.Background(), resource, task))
		var input interfaces.SemanticUnderstandingResourceAgentInput
		require.NoError(t, sonic.Unmarshal([]byte(task.Input), &input))
		assert.Equal(t, []map[string]any{{"order_id": "o-1"}, {"order_id": "o-2"}}, input.SampleRows)
		assert.Equal(t, inputHash, task.InputHash)
	})

	t.Run("writes an empty sample_rows array when the query has no rows", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		resourceDataService := mock_interfaces.NewMockResourceDataService(ctrl)
		resource := sampleSemanticResource()
		task, err := normalizeResourceSemanticUnderstandingRequest(resource, &interfaces.CreateSemanticUnderstandingTaskRequest{
			IncludeSampleRows: true,
			SamplePolicy:      &interfaces.SemanticUnderstandingSamplePolicy{Masked: false, MaxRows: 2},
		})
		require.NoError(t, err)
		resourceDataService.EXPECT().
			QueryWithPaging(gomock.Any(), resource, gomock.Any()).
			Return(&interfaces.ResourceDataQueryResult{Entries: []map[string]any{}}, nil)

		service := &semanticUnderstandingTaskService{rds: resourceDataService}
		require.NoError(t, service.attachUnmaskedSampleRows(context.Background(), resource, task))
		var payload map[string]sonic.NoCopyRawMessage
		require.NoError(t, sonic.Unmarshal([]byte(task.Input), &payload))
		require.Contains(t, payload, "sample_rows")
		var sampleRows []map[string]any
		require.NoError(t, sonic.Unmarshal(payload["sample_rows"], &sampleRows))
		assert.NotNil(t, sampleRows)
		assert.Empty(t, sampleRows)
	})

	assertTaskCreatedWithoutSamples := func(t *testing.T, service *semanticUnderstandingTaskService, resourceID string) {
		t.Helper()
		got, err := service.CreateResourceTask(context.Background(), resourceID, &interfaces.CreateSemanticUnderstandingTaskRequest{
			IncludeSampleRows: true,
			SamplePolicy:      &interfaces.SemanticUnderstandingSamplePolicy{Masked: false, MaxRows: 2},
		})

		require.NoError(t, err)
		require.NotNil(t, got)
		var input interfaces.SemanticUnderstandingResourceAgentInput
		require.NoError(t, sonic.Unmarshal([]byte(got.Input), &input))
		assert.Empty(t, input.SampleRows)
	}

	t.Run("creates a task when sample query fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		resourceService := mock_interfaces.NewMockResourceService(ctrl)
		resourceDataService := mock_interfaces.NewMockResourceDataService(ctrl)
		taskAccess := mock_interfaces.NewMockSemanticUnderstandingTaskAccess(ctrl)
		resource := sampleSemanticResource()
		resourceService.EXPECT().InternalGetByID(gomock.Any(), resource.ID).Return(resource, nil)
		resourceDataService.EXPECT().QueryWithPaging(gomock.Any(), resource, gomock.Any()).
			Return(nil, errors.New("query unavailable"))
		taskAccess.EXPECT().FindActiveByInputHash(gomock.Any(), interfaces.SemanticUnderstandingTaskScopeResource, gomock.Any()).Return(nil, nil)
		taskAccess.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)

		service := &semanticUnderstandingTaskService{rs: resourceService, rds: resourceDataService, suta: taskAccess, debugTaskQueue: make(chan *asynq.Task, 1)}
		assertTaskCreatedWithoutSamples(t, service, resource.ID)
	})

	t.Run("creates a task when sample query returns an HTTP error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		resourceService := mock_interfaces.NewMockResourceService(ctrl)
		resourceDataService := mock_interfaces.NewMockResourceDataService(ctrl)
		taskAccess := mock_interfaces.NewMockSemanticUnderstandingTaskAccess(ctrl)
		resource := sampleSemanticResource()
		resourceService.EXPECT().InternalGetByID(gomock.Any(), resource.ID).Return(resource, nil)
		resourceDataService.EXPECT().QueryWithPaging(gomock.Any(), resource, gomock.Any()).
			Return(nil, rest.NewHTTPError(context.Background(), http.StatusTooManyRequests, verrors.VegaBackend_Query_ConcurrencyLimitExceeded))
		taskAccess.EXPECT().FindActiveByInputHash(gomock.Any(), interfaces.SemanticUnderstandingTaskScopeResource, gomock.Any()).Return(nil, nil)
		taskAccess.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)

		service := &semanticUnderstandingTaskService{rs: resourceService, rds: resourceDataService, suta: taskAccess, debugTaskQueue: make(chan *asynq.Task, 1)}
		assertTaskCreatedWithoutSamples(t, service, resource.ID)
	})

	t.Run("creates a task when the resource is not queryable", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		resourceService := mock_interfaces.NewMockResourceService(ctrl)
		resourceDataService := mock_interfaces.NewMockResourceDataService(ctrl)
		taskAccess := mock_interfaces.NewMockSemanticUnderstandingTaskAccess(ctrl)
		resource := sampleSemanticResource()
		resource.Status = interfaces.ResourceStatusDisabled
		resourceService.EXPECT().InternalGetByID(gomock.Any(), resource.ID).Return(resource, nil)
		taskAccess.EXPECT().FindActiveByInputHash(gomock.Any(), interfaces.SemanticUnderstandingTaskScopeResource, gomock.Any()).Return(nil, nil)
		taskAccess.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)

		service := &semanticUnderstandingTaskService{rs: resourceService, rds: resourceDataService, suta: taskAccess, debugTaskQueue: make(chan *asynq.Task, 1)}
		assertTaskCreatedWithoutSamples(t, service, resource.ID)
	})

	t.Run("reuses a degraded task for a later identical request", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		resourceService := mock_interfaces.NewMockResourceService(ctrl)
		resourceDataService := mock_interfaces.NewMockResourceDataService(ctrl)
		taskAccess := mock_interfaces.NewMockSemanticUnderstandingTaskAccess(ctrl)
		resource := sampleSemanticResource()
		resourceService.EXPECT().InternalGetByID(gomock.Any(), resource.ID).Return(resource, nil).Times(2)

		queryCount := 0
		resourceDataService.EXPECT().QueryWithPaging(gomock.Any(), resource, gomock.Any()).
			DoAndReturn(func(context.Context, *interfaces.Resource, *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
				queryCount++
				if queryCount == 1 {
					return nil, errors.New("query unavailable")
				}
				return &interfaces.ResourceDataQueryResult{Entries: []map[string]any{{"order_id": "o-1"}}}, nil
			}).
			Times(2)

		var createdTask *interfaces.SemanticUnderstandingTask
		var firstInputHash string
		findCount := 0
		taskAccess.EXPECT().FindActiveByInputHash(gomock.Any(), interfaces.SemanticUnderstandingTaskScopeResource, gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, inputHash string) (*interfaces.SemanticUnderstandingTask, error) {
				findCount++
				if findCount == 1 {
					firstInputHash = inputHash
					return nil, nil
				}
				assert.Equal(t, firstInputHash, inputHash)
				return createdTask, nil
			}).
			Times(2)
		taskAccess.EXPECT().Create(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, task *interfaces.SemanticUnderstandingTask) error {
				createdTask = task
				return nil
			})

		service := &semanticUnderstandingTaskService{rs: resourceService, rds: resourceDataService, suta: taskAccess, debugTaskQueue: make(chan *asynq.Task, 1)}
		req := &interfaces.CreateSemanticUnderstandingTaskRequest{
			IncludeSampleRows: true,
			SamplePolicy:      &interfaces.SemanticUnderstandingSamplePolicy{Masked: false, MaxRows: 2},
		}
		firstTask, err := service.CreateResourceTask(context.Background(), resource.ID, req)
		require.NoError(t, err)
		var firstInput interfaces.SemanticUnderstandingResourceAgentInput
		require.NoError(t, sonic.Unmarshal([]byte(firstTask.Input), &firstInput))
		assert.Empty(t, firstInput.SampleRows)

		secondTask, err := service.CreateResourceTask(context.Background(), resource.ID, req)
		require.NoError(t, err)
		assert.Same(t, firstTask, secondTask)
		var secondInput interfaces.SemanticUnderstandingResourceAgentInput
		require.NoError(t, sonic.Unmarshal([]byte(secondTask.Input), &secondInput))
		assert.Empty(t, secondInput.SampleRows)
	})
}

func TestLimitSemanticUnderstandingSampleRows(t *testing.T) {
	t.Run("truncates long text, binary, and nested values", func(t *testing.T) {
		longValue := strings.Repeat("测", interfaces.MaxSemanticUnderstandingSampleValueRunes+1)
		binaryValue := string([]byte{0xff, 0xfe, 0x01})
		rows, truncated, err := limitSemanticUnderstandingSampleRows([]map[string]any{{
			"text":   longValue,
			"binary": binaryValue,
			"nested": map[string]any{"text": longValue, "values": []any{longValue}},
		}})

		require.NoError(t, err)
		assert.False(t, truncated)
		expectedText := strings.Repeat("测", interfaces.MaxSemanticUnderstandingSampleValueRunes-1) + "…"
		assert.Equal(t, expectedText, rows[0]["text"])
		assert.Equal(t, "【二进制内容已省略，原始长度 3 字节】", rows[0]["binary"])
		nested := rows[0]["nested"].(map[string]any)
		assert.Equal(t, expectedText, nested["text"])
		assert.Equal(t, []any{expectedText}, nested["values"])
	})

	t.Run("drops trailing rows when the payload exceeds the limit", func(t *testing.T) {
		rows := make([]map[string]any, interfaces.MaxSemanticUnderstandingSampleRows)
		for rowIndex := range rows {
			rows[rowIndex] = make(map[string]any, 60)
			for fieldIndex := 0; fieldIndex < 60; fieldIndex++ {
				rows[rowIndex][fmt.Sprintf("field_%d", fieldIndex)] = strings.Repeat("a", interfaces.MaxSemanticUnderstandingSampleValueRunes)
			}
		}

		limited, truncated, err := limitSemanticUnderstandingSampleRows(rows)

		require.NoError(t, err)
		assert.True(t, truncated)
		assert.NotEmpty(t, limited)
		assert.Less(t, len(limited), len(rows))
		payload, err := sonic.ConfigStd.Marshal(limited)
		require.NoError(t, err)
		assert.LessOrEqual(t, len(payload), interfaces.MaxSemanticUnderstandingSamplePayloadBytes)
	})

	t.Run("keeps no more than the configured row limit", func(t *testing.T) {
		rows := make([]map[string]any, interfaces.MaxSemanticUnderstandingSampleRows+1)
		for index := range rows {
			rows[index] = map[string]any{"id": index}
		}

		limited, truncated, err := limitSemanticUnderstandingSampleRows(rows)

		require.NoError(t, err)
		assert.False(t, truncated)
		assert.Len(t, limited, interfaces.MaxSemanticUnderstandingSampleRows)
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

	t.Run("keeps task when account lookup fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		taskAccess := mock_interfaces.NewMockSemanticUnderstandingTaskAccess(ctrl)
		userMgmtService := mock_interfaces.NewMockUserMgmtService(ctrl)
		service := &semanticUnderstandingTaskService{suta: taskAccess, ums: userMgmtService}
		task := &interfaces.SemanticUnderstandingTask{ID: "semantic-task-2"}

		taskAccess.EXPECT().GetByID(gomock.Any(), "semantic-task-2").Return(task, nil)
		userMgmtService.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(errors.New("user service down"))

		got, err := service.GetByID(context.Background(), "semantic-task-2")

		require.NoError(t, err)
		assert.Equal(t, "semantic-task-2", got.ID)
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

	t.Run("list keeps tasks when reference lookup fails", func(t *testing.T) {
		tasks := []*interfaces.SemanticUnderstandingTask{{ID: "task-4", CatalogID: "catalog-3", ResourceID: "resource-3"}}
		taskAccess.EXPECT().List(gomock.Any(), gomock.Any()).Return(tasks, int64(1), nil)
		resourceService.EXPECT().InternalGetByIDs(gomock.Any(), []string{"resource-3"}).Return(nil, errors.New("resource service down"))
		catalogService.EXPECT().InternalGetByIDs(gomock.Any(), []string{"catalog-3"}).Return([]*interfaces.Catalog{{ID: "catalog-3", Name: "目录三"}}, nil)

		got, total, err := service.List(context.Background(), interfaces.SemanticUnderstandingTaskQueryParams{})

		require.NoError(t, err)
		assert.Equal(t, int64(1), total)
		assert.Equal(t, "task-4", got[0].ID)
		assert.Empty(t, got[0].ResourceName)
		assert.Equal(t, "目录三", got[0].CatalogName)
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

	t.Run("accepts unmasked sample policy when including samples", func(t *testing.T) {
		got, err := normalizeResourceSemanticUnderstandingRequest(sampleSemanticResource(), &interfaces.CreateSemanticUnderstandingTaskRequest{
			IncludeSampleRows: true,
			SamplePolicy:      &interfaces.SemanticUnderstandingSamplePolicy{Masked: false, MaxRows: 20},
		})

		require.NoError(t, err)
		assert.NotNil(t, got)
	})

	t.Run("rejects sample rows beyond the semantic understanding limit", func(t *testing.T) {
		got, err := normalizeResourceSemanticUnderstandingRequest(sampleSemanticResource(), &interfaces.CreateSemanticUnderstandingTaskRequest{
			IncludeSampleRows: true,
			SamplePolicy: &interfaces.SemanticUnderstandingSamplePolicy{
				Masked:  false,
				MaxRows: interfaces.MaxSemanticUnderstandingSampleRows + 1,
			},
		})

		require.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "sample_policy.max_rows")
	})
}

func sampleSemanticResource() *interfaces.Resource {
	return &interfaces.Resource{
		ID:               "resource-1",
		CatalogID:        "catalog-1",
		Name:             "orders",
		Category:         interfaces.ResourceCategoryTable,
		Schema:           "sales",
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
