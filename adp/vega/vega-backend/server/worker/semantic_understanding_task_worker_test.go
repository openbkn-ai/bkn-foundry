// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package worker

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/openbkn-ai/bkn-comm-go/rest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
)

type accountIDContextMatcher struct {
	accountID string
}

func ctxWithAccountID(t *testing.T, accountID string) gomock.Matcher {
	t.Helper()
	return accountIDContextMatcher{accountID: accountID}
}

func (m accountIDContextMatcher) Matches(x any) bool {
	ctx, ok := x.(context.Context)
	if !ok {
		return false
	}
	accountInfo, ok := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	return ok && accountInfo.ID == m.accountID
}

func (m accountIDContextMatcher) String() string {
	return "context with account id " + m.accountID
}

func TestSemanticUnderstandingTaskWorkerFillQueueRefillsEmptyQueue(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	taskService := vmock.NewMockSemanticUnderstandingTaskService(ctrl)
	worker := &SemanticUnderstandingTaskWorker{
		suts:     taskService,
		queue:    make(chan string, 4),
		inFlight: make(map[string]struct{}),
	}
	taskService.EXPECT().InternalList(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, params interfaces.SemanticUnderstandingTaskQueryParams) ([]*interfaces.SemanticUnderstandingTaskSummary, int64, error) {
			assert.Equal(t, 4, params.Limit)
			assert.Equal(t, []string{interfaces.SemanticUnderstandingTaskStatusPending}, params.Statuses)
			assert.Equal(t, interfaces.SemanticUnderstandingTaskSortCreateTime, params.Sort)
			assert.Equal(t, interfaces.ASC_DIRECTION, params.Direction)
			return []*interfaces.SemanticUnderstandingTaskSummary{{ID: "task-1"}}, 1, nil
		})

	worker.fillQueue(context.Background())

	assert.Len(t, worker.queue, 1)
	assert.False(t, worker.addInFlight("task-1"))
}

func TestSemanticUnderstandingTaskWorkerFillQueueSkipsDatabaseWhenQueueIsNotEmpty(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	taskService := vmock.NewMockSemanticUnderstandingTaskService(ctrl)
	worker := &SemanticUnderstandingTaskWorker{
		suts:     taskService,
		queue:    make(chan string, 4),
		inFlight: make(map[string]struct{}),
	}
	worker.queue <- "already-queued"

	worker.fillQueue(context.Background())
}

func TestSemanticUnderstandingTaskWorkerRun(t *testing.T) {
	t.Run("skips cancelled task", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		taskService := vmock.NewMockSemanticUnderstandingTaskService(ctrl)
		worker := &SemanticUnderstandingTaskWorker{suts: taskService}
		taskService.EXPECT().InternalGetByID(gomock.Any(), "semantic-task-1").Return(
			&interfaces.SemanticUnderstandingTask{
				ID: "semantic-task-1", Status: interfaces.SemanticUnderstandingTaskStatusCancelled,
			}, nil,
		)

		require.NoError(t, worker.Run(context.Background(), "semantic-task-1"))
	})

	t.Run("runs agent and marks completed", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		taskService := vmock.NewMockSemanticUnderstandingTaskService(ctrl)
		agentService := vmock.NewMockBknAgentService(ctrl)
		resourceService := vmock.NewMockResourceService(ctrl)
		worker := &SemanticUnderstandingTaskWorker{
			suts: taskService,
			bas:  agentService,
			rs:   resourceService,
		}

		semanticTask := &interfaces.SemanticUnderstandingTask{
			ID:                  "semantic-task-1",
			Scope:               interfaces.SemanticUnderstandingTaskScopeResource,
			ResourceID:          "resource-1",
			Status:              interfaces.SemanticUnderstandingTaskStatusPending,
			AgentID:             interfaces.SemanticUnderstandingResourceAgentID,
			Input:               `{"resource":{"id":"resource-1"}}`,
			ApplyMode:           interfaces.SemanticUnderstandingApplyModeFillEmpty,
			ConfidenceThreshold: 0.75,
			Creator:             interfaces.AccountInfo{ID: "account-1"},
		}
		resourceInfo := &interfaces.Resource{
			ID:          "resource-1",
			Description: "",
			SchemaDefinition: []*interfaces.Property{
				{Name: "id", Type: interfaces.DataType_String},
			},
		}
		resourceService.EXPECT().
			InternalGetByID(gomock.Any(), "resource-1").
			Return(resourceInfo, nil)

		taskService.EXPECT().
			InternalGetByID(gomock.Any(), "semantic-task-1").
			Return(semanticTask, nil)
		agentService.EXPECT().
			Run(ctxWithAccountID(t, "account-1"), semanticTask).
			Return("agent-task-1", nil)
		taskService.EXPECT().
			ClaimRunning(ctxWithAccountID(t, "account-1"), "semantic-task-1").
			Return(true, nil)
		taskService.EXPECT().
			SetAgentTaskID(ctxWithAccountID(t, "account-1"), "semantic-task-1", "agent-task-1").
			Return(true, nil)
		agentService.EXPECT().
			WaitResult(gomock.Any(), "agent-task-1").
			Return(&interfaces.BknAgentTask{
				TaskID: "agent-task-1",
				Status: interfaces.BknAgentTaskStatusSucceeded,
				Result: []byte(`{"confidence":0.82,"resource":{"display_name":"Business Resource","description":"business resource","confidence":0.82},"fields":[{"name":"id","display_name":"标识","description":"identifier","confidence":0.81}],"warnings":[]}`),
			}, nil)
		resourceService.EXPECT().
			GetByID(gomock.Any(), "resource-1").
			Return(resourceInfo, nil)
		resourceService.EXPECT().
			UpdateResource(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, got *interfaces.Resource) error {
				assert.Equal(t, "Business Resource", got.Name)
				assert.Equal(t, "business resource", got.Description)
				require.Len(t, got.SchemaDefinition, 1)
				assert.Equal(t, "标识", got.SchemaDefinition[0].DisplayName)
				assert.Equal(t, "identifier", got.SchemaDefinition[0].Description)
				assert.Equal(t, "account-1", got.Updater.ID)
				assert.NotZero(t, got.UpdateTime)
				return nil
			})
		taskService.EXPECT().
			MarkCompleted(gomock.Any(), "semantic-task-1", `{"confidence":0.82,"resource":{"display_name":"Business Resource","description":"business resource","confidence":0.82},"fields":[{"name":"id","display_name":"标识","description":"identifier","confidence":0.81}],"warnings":[]}`, 0.82, gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, _ string, _ float64, detailJSON string) (bool, error) {
				var detail map[string]sonic.NoCopyRawMessage
				require.NoError(t, sonic.Unmarshal([]byte(detailJSON), &detail))
				assert.Contains(t, detail, "resource")
				assert.Contains(t, detail, "fields")
				assert.Contains(t, detail, "warnings")
				return true, nil
			})
		taskService.EXPECT().
			MarkApplied(gomock.Any(), "semantic-task-1", true, gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, applied bool, detailJSON string) (bool, error) {
				assert.True(t, applied)
				assert.JSONEq(t, `{"resource_updated":true,"updated_resource":["name","description"],"updated_fields":["id"],"field_details":[{"name":"id","status":"updated","updated":["display_name","description"]}]}`, detailJSON)
				return true, nil
			})

		err := worker.Run(context.Background(), "semantic-task-1")

		require.NoError(t, err)
	})

	t.Run("marks failed when agent task failed", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		taskService := vmock.NewMockSemanticUnderstandingTaskService(ctrl)
		agentService := vmock.NewMockBknAgentService(ctrl)
		worker := &SemanticUnderstandingTaskWorker{
			suts: taskService,
			bas:  agentService,
		}

		semanticTask := &interfaces.SemanticUnderstandingTask{
			ID:          "semantic-task-1",
			Status:      interfaces.SemanticUnderstandingTaskStatusRunning,
			AgentTaskID: "agent-task-1",
			Creator:     interfaces.AccountInfo{ID: "account-1"},
		}

		taskService.EXPECT().
			InternalGetByID(gomock.Any(), "semantic-task-1").
			Return(semanticTask, nil)
		agentService.EXPECT().
			WaitResult(gomock.Any(), "agent-task-1").
			Return(&interfaces.BknAgentTask{
				TaskID:        "agent-task-1",
				Status:        interfaces.BknAgentTaskStatusFailed,
				FailureDetail: "agent failed",
			}, nil)
		taskService.EXPECT().
			MarkFailed(gomock.Any(), "semantic-task-1", "agent failed").
			Return(true, nil)

		err := worker.Run(context.Background(), "semantic-task-1")

		require.NoError(t, err)
	})

	t.Run("cancels active task when resource was deleted", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		taskService := vmock.NewMockSemanticUnderstandingTaskService(ctrl)
		resourceService := vmock.NewMockResourceService(ctrl)
		worker := &SemanticUnderstandingTaskWorker{suts: taskService, rs: resourceService}
		taskInfo := &interfaces.SemanticUnderstandingTask{
			ID: "semantic-task-1", Scope: interfaces.SemanticUnderstandingTaskScopeResource,
			ResourceID: "resource-1", Status: interfaces.SemanticUnderstandingTaskStatusPending,
		}
		taskService.EXPECT().InternalGetByID(gomock.Any(), "semantic-task-1").Return(taskInfo, nil)
		resourceService.EXPECT().InternalGetByID(gomock.Any(), "resource-1").Return(nil, nil)
		taskService.EXPECT().MarkCancelled(gomock.Any(), "semantic-task-1", "catalog or resource deleted").
			Return(true, nil)

		require.NoError(t, worker.Run(context.Background(), "semantic-task-1"))
	})

	t.Run("cancels active catalog task when catalog was deleted", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		taskService := vmock.NewMockSemanticUnderstandingTaskService(ctrl)
		catalogService := vmock.NewMockCatalogService(ctrl)
		worker := &SemanticUnderstandingTaskWorker{suts: taskService, cs: catalogService}
		taskInfo := &interfaces.SemanticUnderstandingTask{
			ID: "semantic-task-1", Scope: interfaces.SemanticUnderstandingTaskScopeCatalog,
			CatalogID: "catalog-1", Status: interfaces.SemanticUnderstandingTaskStatusRunning,
		}
		taskService.EXPECT().InternalGetByID(gomock.Any(), "semantic-task-1").Return(taskInfo, nil)
		catalogService.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", false).
			Return(nil, &rest.HTTPError{HTTPCode: http.StatusNotFound})
		taskService.EXPECT().MarkCancelled(gomock.Any(), "semantic-task-1", "catalog or resource deleted").
			Return(true, nil)

		require.NoError(t, worker.Run(context.Background(), "semantic-task-1"))
	})

	t.Run("resumes applying completed task", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		taskService := vmock.NewMockSemanticUnderstandingTaskService(ctrl)
		resourceService := vmock.NewMockResourceService(ctrl)
		worker := &SemanticUnderstandingTaskWorker{suts: taskService, rs: resourceService}
		semanticTask := &interfaces.SemanticUnderstandingTask{
			ID:                  "semantic-task-1",
			Scope:               interfaces.SemanticUnderstandingTaskScopeResource,
			ResourceID:          "resource-1",
			Status:              interfaces.SemanticUnderstandingTaskStatusCompleted,
			ApplyMode:           interfaces.SemanticUnderstandingApplyModeDryRun,
			ConfidenceThreshold: 0.75,
			Confidence:          0.9,
			ResultJSON:          `{"confidence":0.9}`,
			Creator:             interfaces.AccountInfo{ID: "account-1"},
		}
		taskService.EXPECT().
			InternalGetByID(gomock.Any(), "semantic-task-1").
			Return(semanticTask, nil)
		resourceService.EXPECT().InternalGetByID(gomock.Any(), "resource-1").
			Return(&interfaces.Resource{ID: "resource-1"}, nil)
		taskService.EXPECT().
			MarkApplied(ctxWithAccountID(t, "account-1"), "semantic-task-1", false, gomock.Any()).
			Return(true, nil)

		err := worker.Run(context.Background(), "semantic-task-1")

		require.NoError(t, err)
	})

	t.Run("stops retrying completed task when resource was deleted", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		taskService := vmock.NewMockSemanticUnderstandingTaskService(ctrl)
		resourceService := vmock.NewMockResourceService(ctrl)
		worker := &SemanticUnderstandingTaskWorker{suts: taskService, rs: resourceService}
		taskInfo := &interfaces.SemanticUnderstandingTask{
			ID: "semantic-task-1", Scope: interfaces.SemanticUnderstandingTaskScopeResource,
			ResourceID: "resource-1", Status: interfaces.SemanticUnderstandingTaskStatusCompleted,
		}
		taskService.EXPECT().InternalGetByID(gomock.Any(), "semantic-task-1").Return(taskInfo, nil)
		resourceService.EXPECT().InternalGetByID(gomock.Any(), "resource-1").Return(nil, nil)

		require.NoError(t, worker.Run(context.Background(), "semantic-task-1"))
	})

	t.Run("returns run error without marking failed", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		taskService := vmock.NewMockSemanticUnderstandingTaskService(ctrl)
		agentService := vmock.NewMockBknAgentService(ctrl)
		worker := &SemanticUnderstandingTaskWorker{
			suts: taskService,
			bas:  agentService,
		}

		semanticTask := &interfaces.SemanticUnderstandingTask{
			ID:      "semantic-task-1",
			Status:  interfaces.SemanticUnderstandingTaskStatusPending,
			AgentID: interfaces.SemanticUnderstandingResourceAgentID,
			Input:   `{"resource":{"id":"resource-1"}}`,
			Creator: interfaces.AccountInfo{ID: "account-1"},
		}
		taskService.EXPECT().
			InternalGetByID(gomock.Any(), "semantic-task-1").
			Return(semanticTask, nil)
		taskService.EXPECT().
			ClaimRunning(ctxWithAccountID(t, "account-1"), "semantic-task-1").
			Return(true, nil)
		agentService.EXPECT().
			Run(ctxWithAccountID(t, "account-1"), semanticTask).
			Return("", errors.New("temporary agent error"))

		err := worker.Run(context.Background(), "semantic-task-1")

		require.ErrorContains(t, err, "temporary agent error")
	})

	t.Run("returns wait error without marking failed", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		taskService := vmock.NewMockSemanticUnderstandingTaskService(ctrl)
		agentService := vmock.NewMockBknAgentService(ctrl)
		worker := &SemanticUnderstandingTaskWorker{
			suts: taskService,
			bas:  agentService,
		}

		semanticTask := &interfaces.SemanticUnderstandingTask{
			ID:          "semantic-task-1",
			Status:      interfaces.SemanticUnderstandingTaskStatusRunning,
			AgentTaskID: "agent-task-1",
			Creator:     interfaces.AccountInfo{ID: "account-1"},
		}
		taskService.EXPECT().
			InternalGetByID(gomock.Any(), "semantic-task-1").
			Return(semanticTask, nil)
		agentService.EXPECT().
			WaitResult(ctxWithAccountID(t, "account-1"), "agent-task-1").
			Return(nil, errors.New("temporary agent error"))

		err := worker.Run(context.Background(), "semantic-task-1")

		require.ErrorContains(t, err, "temporary agent error")
	})

	t.Run("marks unapplied detail when confidence is below threshold", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		taskService := vmock.NewMockSemanticUnderstandingTaskService(ctrl)
		agentService := vmock.NewMockBknAgentService(ctrl)
		resourceService := vmock.NewMockResourceService(ctrl)
		worker := &SemanticUnderstandingTaskWorker{
			suts: taskService,
			bas:  agentService,
			rs:   resourceService,
		}
		semanticTask := &interfaces.SemanticUnderstandingTask{
			ID:                  "semantic-task-1",
			Scope:               interfaces.SemanticUnderstandingTaskScopeResource,
			ResourceID:          "resource-1",
			Status:              interfaces.SemanticUnderstandingTaskStatusRunning,
			AgentTaskID:         "agent-task-1",
			ApplyMode:           interfaces.SemanticUnderstandingApplyModeForce,
			ConfidenceThreshold: 0.9,
		}

		taskService.EXPECT().
			InternalGetByID(gomock.Any(), "semantic-task-1").
			Return(semanticTask, nil)
		resourceService.EXPECT().InternalGetByID(gomock.Any(), "resource-1").
			Return(&interfaces.Resource{ID: "resource-1"}, nil)
		agentService.EXPECT().
			WaitResult(gomock.Any(), "agent-task-1").
			Return(&interfaces.BknAgentTask{
				TaskID: "agent-task-1",
				Status: interfaces.BknAgentTaskStatusSucceeded,
				Result: []byte(`{"confidence":0.8,"resource":{"description":"business resource"},"fields":[]}`),
			}, nil)
		taskService.EXPECT().
			MarkCompleted(gomock.Any(), "semantic-task-1", `{"confidence":0.8,"resource":{"description":"business resource"},"fields":[]}`, 0.8, gomock.Any()).
			Return(true, nil)
		taskService.EXPECT().
			MarkApplied(gomock.Any(), "semantic-task-1", false, gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, applied bool, detailJSON string) (bool, error) {
				assert.False(t, applied)
				assert.JSONEq(t, `{"reason":"confidence_below_threshold","confidence":0.8,"confidence_threshold":0.9,"scope":"resource"}`, detailJSON)
				return true, nil
			})

		err := worker.Run(context.Background(), "semantic-task-1")

		require.NoError(t, err)
	})
}

func TestSemanticUnderstandingTaskWorkerApplyResourceResult(t *testing.T) {
	t.Run("skips apply when confidence is below threshold", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		resourceService := vmock.NewMockResourceService(ctrl)
		worker := &SemanticUnderstandingTaskWorker{rs: resourceService}
		task := &interfaces.SemanticUnderstandingTask{
			Scope:               interfaces.SemanticUnderstandingTaskScopeResource,
			ResourceID:          "resource-1",
			ApplyMode:           interfaces.SemanticUnderstandingApplyModeForce,
			ConfidenceThreshold: 0.9,
		}

		got, err := worker.applyResult(context.Background(), task, `{"confidence":0.8}`, 0.8, nil)

		require.NoError(t, err)
		assert.False(t, got.Applied)
		assert.JSONEq(t, `{"reason":"confidence_below_threshold","confidence":0.8,"confidence_threshold":0.9,"scope":"resource"}`, got.DetailJSON)
	})

	t.Run("rejects unknown fields", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		resourceService := vmock.NewMockResourceService(ctrl)
		worker := &SemanticUnderstandingTaskWorker{rs: resourceService}
		task := &interfaces.SemanticUnderstandingTask{
			Scope:               interfaces.SemanticUnderstandingTaskScopeResource,
			ResourceID:          "resource-1",
			ApplyMode:           interfaces.SemanticUnderstandingApplyModeForce,
			ConfidenceThreshold: 0.75,
		}
		resourceService.EXPECT().
			GetByID(gomock.Any(), "resource-1").
			Return(&interfaces.Resource{
				ID: "resource-1",
				SchemaDefinition: []*interfaces.Property{
					{Name: "id", Type: interfaces.DataType_String},
				},
			}, nil)

		got, err := worker.applyResult(context.Background(), task, `{"confidence":0.8,"fields":[{"name":"missing","display_name":"Missing"}]}`, 0.8, nil)

		require.NoError(t, err)
		assert.False(t, got.Applied)
		assert.JSONEq(t, `{"resource_updated":false,"skipped_fields":["missing: not found"],"field_details":[{"name":"missing","status":"skipped","reasons":["not found"]}]}`, got.DetailJSON)
	})

	t.Run("fills display names that still equal the technical field name", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		resourceService := vmock.NewMockResourceService(ctrl)
		worker := &SemanticUnderstandingTaskWorker{rs: resourceService}
		task := &interfaces.SemanticUnderstandingTask{
			Scope:               interfaces.SemanticUnderstandingTaskScopeResource,
			ResourceID:          "resource-1",
			ApplyMode:           interfaces.SemanticUnderstandingApplyModeFillEmpty,
			ConfidenceThreshold: 0.75,
		}
		resource := &interfaces.Resource{
			ID: "resource-1",
			SchemaDefinition: []*interfaces.Property{
				{Name: "product_id", DisplayName: "product_id", Type: interfaces.DataType_String},
			},
		}
		resourceService.EXPECT().
			GetByID(gomock.Any(), "resource-1").
			Return(resource, nil)
		resourceService.EXPECT().
			UpdateResource(gomock.Any(), resource).
			DoAndReturn(func(_ context.Context, got *interfaces.Resource) error {
				assert.Equal(t, "商品ID", got.SchemaDefinition[0].DisplayName)
				return nil
			})

		got, err := worker.applyResult(context.Background(), task, `{"confidence":0.9,"fields":[{"name":"product_id","display_name":"商品ID","confidence":0.9}]}`, 0.9, nil)

		require.NoError(t, err)
		assert.True(t, got.Applied)
		assert.JSONEq(t, `{"resource_updated":false,"updated_fields":["product_id"],"field_details":[{"name":"product_id","status":"updated","updated":["display_name"]}]}`, got.DetailJSON)
	})

	t.Run("rejects technical field names in force mode", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		resourceService := vmock.NewMockResourceService(ctrl)
		worker := &SemanticUnderstandingTaskWorker{rs: resourceService}
		task := &interfaces.SemanticUnderstandingTask{
			Scope:               interfaces.SemanticUnderstandingTaskScopeResource,
			ResourceID:          "resource-1",
			ApplyMode:           interfaces.SemanticUnderstandingApplyModeForce,
			ConfidenceThreshold: 0.75,
		}
		resourceService.EXPECT().
			GetByID(gomock.Any(), "resource-1").
			Return(&interfaces.Resource{
				ID: "resource-1",
				SchemaDefinition: []*interfaces.Property{
					{Name: "supplier_id", DisplayName: "供应商ID", Type: interfaces.DataType_String},
				},
			}, nil)

		got, err := worker.applyResult(context.Background(), task, `{"confidence":0.9,"fields":[{"name":"supplier_id","display_name":"Supplier ID","confidence":0.9}]}`, 0.9, nil)

		require.NoError(t, err)
		assert.False(t, got.Applied)
		assert.JSONEq(t, `{"resource_updated":false,"skipped_fields":["supplier_id: display_name equals technical field name"],"field_details":[{"name":"supplier_id","status":"unchanged","reasons":["display_name equals technical field name"]}]}`, got.DetailJSON)
	})

	t.Run("rejects punctuation-only display names in force mode", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		resourceService := vmock.NewMockResourceService(ctrl)
		worker := &SemanticUnderstandingTaskWorker{rs: resourceService}
		task := &interfaces.SemanticUnderstandingTask{
			Scope:               interfaces.SemanticUnderstandingTaskScopeResource,
			ResourceID:          "resource-1",
			ApplyMode:           interfaces.SemanticUnderstandingApplyModeForce,
			ConfidenceThreshold: 0.75,
		}
		resourceService.EXPECT().
			GetByID(gomock.Any(), "resource-1").
			Return(&interfaces.Resource{
				ID: "resource-1",
				SchemaDefinition: []*interfaces.Property{
					{Name: "supplier_id", DisplayName: "供应商ID", Type: interfaces.DataType_String},
				},
			}, nil)

		got, err := worker.applyResult(context.Background(), task, `{"confidence":0.9,"fields":[{"name":"supplier_id","display_name":"---","confidence":0.9}]}`, 0.9, nil)

		require.NoError(t, err)
		assert.False(t, got.Applied)
		assert.JSONEq(t, `{"resource_updated":false,"skipped_fields":["supplier_id: display_name equals technical field name"],"field_details":[{"name":"supplier_id","status":"unchanged","reasons":["display_name equals technical field name"]}]}`, got.DetailJSON)
	})

	t.Run("rejects whitespace-only display names in force mode", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		resourceService := vmock.NewMockResourceService(ctrl)
		worker := &SemanticUnderstandingTaskWorker{rs: resourceService}
		task := &interfaces.SemanticUnderstandingTask{
			Scope:               interfaces.SemanticUnderstandingTaskScopeResource,
			ResourceID:          "resource-1",
			ApplyMode:           interfaces.SemanticUnderstandingApplyModeForce,
			ConfidenceThreshold: 0.75,
		}
		resourceService.EXPECT().
			GetByID(gomock.Any(), "resource-1").
			Return(&interfaces.Resource{
				ID: "resource-1",
				SchemaDefinition: []*interfaces.Property{
					{Name: "supplier_id", DisplayName: "供应商ID", Type: interfaces.DataType_String},
				},
			}, nil)

		got, err := worker.applyResult(context.Background(), task, `{"confidence":0.9,"fields":[{"name":"supplier_id","display_name":"　","confidence":0.9}]}`, 0.9, nil)

		require.NoError(t, err)
		assert.False(t, got.Applied)
		assert.JSONEq(t, `{"resource_updated":false,"skipped_fields":["supplier_id: display_name equals technical field name"],"field_details":[{"name":"supplier_id","status":"unchanged","reasons":["display_name equals technical field name"]}]}`, got.DetailJSON)
	})

	t.Run("fills resource name when it still equals the source identifier", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		resourceService := vmock.NewMockResourceService(ctrl)
		worker := &SemanticUnderstandingTaskWorker{rs: resourceService}
		task := &interfaces.SemanticUnderstandingTask{
			Scope:               interfaces.SemanticUnderstandingTaskScopeResource,
			ResourceID:          "resource-1",
			ApplyMode:           interfaces.SemanticUnderstandingApplyModeFillEmpty,
			ConfidenceThreshold: 0.75,
		}
		resource := &interfaces.Resource{
			ID:               "resource-1",
			Name:             "public.v_product_review_summary",
			SourceIdentifier: "public.v_product_review_summary",
		}
		resourceService.EXPECT().
			GetByID(gomock.Any(), "resource-1").
			Return(resource, nil)
		resourceService.EXPECT().
			UpdateResource(gomock.Any(), resource).
			DoAndReturn(func(_ context.Context, got *interfaces.Resource) error {
				assert.Equal(t, "商品评价汇总视图", got.Name)
				return nil
			})

		got, err := worker.applyResult(context.Background(), task, `{"confidence":0.9,"resource":{"display_name":"商品评价汇总视图","confidence":0.9}}`, 0.9, nil)

		require.NoError(t, err)
		assert.True(t, got.Applied)
		assert.JSONEq(t, `{"resource_updated":true,"updated_resource":["name"]}`, got.DetailJSON)
	})

	t.Run("fills descriptions that still equal their source descriptions", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		resourceService := vmock.NewMockResourceService(ctrl)
		worker := &SemanticUnderstandingTaskWorker{rs: resourceService}
		task := &interfaces.SemanticUnderstandingTask{
			Scope:               interfaces.SemanticUnderstandingTaskScopeResource,
			ResourceID:          "resource-1",
			ApplyMode:           interfaces.SemanticUnderstandingApplyModeFillEmpty,
			ConfidenceThreshold: 0.75,
		}
		resource := &interfaces.Resource{
			ID:          "resource-1",
			Description: "source resource description",
			SourceMetadata: map[string]any{
				"original_description": "source resource description",
			},
			SchemaDefinition: []*interfaces.Property{
				{Name: "product_id", Description: "source field description", OriginalDescription: "source field description", Type: interfaces.DataType_String},
			},
		}
		resourceService.EXPECT().
			GetByID(gomock.Any(), "resource-1").
			Return(resource, nil)
		resourceService.EXPECT().
			UpdateResource(gomock.Any(), resource).
			DoAndReturn(func(_ context.Context, got *interfaces.Resource) error {
				assert.Equal(t, "AI resource description", got.Description)
				assert.Equal(t, "AI field description", got.SchemaDefinition[0].Description)
				return nil
			})

		got, err := worker.applyResult(context.Background(), task, `{"confidence":0.9,"resource":{"description":"AI resource description","confidence":0.9},"fields":[{"name":"product_id","description":"AI field description","confidence":0.9}]}`, 0.9, nil)

		require.NoError(t, err)
		assert.True(t, got.Applied)
		assert.JSONEq(t, `{"resource_updated":true,"updated_resource":["description"],"updated_fields":["product_id"],"field_details":[{"name":"product_id","status":"updated","updated":["description"]}]}`, got.DetailJSON)
	})

	t.Run("skips apply in dry run", func(t *testing.T) {
		worker := &SemanticUnderstandingTaskWorker{}
		task := &interfaces.SemanticUnderstandingTask{
			Scope:               interfaces.SemanticUnderstandingTaskScopeResource,
			ApplyMode:           interfaces.SemanticUnderstandingApplyModeDryRun,
			ConfidenceThreshold: 0.75,
		}

		got, err := worker.applyResult(context.Background(), task, `{"confidence":0.9}`, 0.9, nil)

		require.NoError(t, err)
		assert.False(t, got.Applied)
		assert.JSONEq(t, `{"reason":"dry_run","apply_mode":"dry_run","scope":"resource"}`, got.DetailJSON)
	})
}

func TestSemanticUnderstandingTaskWorkerApplyCatalogResult(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	resourceService := vmock.NewMockResourceService(ctrl)
	worker := &SemanticUnderstandingTaskWorker{rs: resourceService}
	task := &interfaces.SemanticUnderstandingTask{
		Scope:               interfaces.SemanticUnderstandingTaskScopeCatalog,
		CatalogID:           "catalog-1",
		ApplyMode:           interfaces.SemanticUnderstandingApplyModeForce,
		ConfidenceThreshold: 0.75,
	}
	logicDefinition := []*interfaces.LogicDefinitionNode{
		{ID: "source", Type: interfaces.LogicDefinitionNodeType_Resource},
		{ID: "output", Type: interfaces.LogicDefinitionNodeType_Output, Inputs: []string{"source"}},
	}

	resourceService.EXPECT().
		GetByCatalogID(gomock.Any(), "catalog-1").
		Return([]*interfaces.Resource{
			{ID: "resource-1", CatalogID: "catalog-1", Name: "orders", Category: interfaces.ResourceCategoryTable},
			{ID: "view-2", CatalogID: "catalog-1", Name: "old_view", Category: interfaces.ResourceCategoryLogicView},
		}, nil)
	resourceService.EXPECT().
		Create(gomock.Any(), gomock.AssignableToTypeOf(&interfaces.ResourceRequest{})).
		DoAndReturn(func(_ context.Context, req *interfaces.ResourceRequest) (*interfaces.Resource, error) {
			assert.Equal(t, "catalog-1", req.CatalogID)
			assert.Equal(t, "customer_order_summary", req.Name)
			assert.Equal(t, "customer_order_summary", req.SourceIdentifier)
			assert.Equal(t, "summary view", req.Description)
			assert.Equal(t, interfaces.ResourceCategoryLogicView, req.Category)
			assert.Equal(t, logicDefinition, req.LogicDefinition)
			return &interfaces.Resource{ID: "view-1"}, nil
		})
	resourceService.EXPECT().
		UpdateStatus(gomock.Any(), "view-2", interfaces.ResourceStatusStale, "obsolete").
		Return(nil)

	resultJSON := `{"confidence":0.84,"logic_views":[{"action":"create","name":"customer_order_summary","source_identifier":"customer_order_summary","description":"summary view","source_resources":["resource-1"],"logic_definition":[{"id":"source","type":"resource"},{"id":"output","type":"output","inputs":["source"]}],"confidence":0.82}],"obsolete_logic_views":[{"target_resource_id":"view-2","reason":"obsolete","confidence":0.91}]}`
	got, err := worker.applyResult(context.Background(), task, resultJSON, 0.84, nil)

	require.NoError(t, err)
	require.NotNil(t, got)
	assert.True(t, got.Applied)
	assert.JSONEq(t, `{"created_resource_ids":["view-1"],"staled_resource_ids":["view-2"]}`, got.DetailJSON)
}

func TestSemanticUnderstandingTaskWorkerApplyCatalogResultRejectsInvalidSourceIdentifier(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	resourceService := vmock.NewMockResourceService(ctrl)
	worker := &SemanticUnderstandingTaskWorker{rs: resourceService}
	task := &interfaces.SemanticUnderstandingTask{
		Scope:     interfaces.SemanticUnderstandingTaskScopeCatalog,
		CatalogID: "catalog-1",
		ApplyMode: interfaces.SemanticUnderstandingApplyModeForce,
	}
	resourceService.EXPECT().
		GetByCatalogID(gomock.Any(), "catalog-1").
		Return([]*interfaces.Resource{{ID: "resource-1", CatalogID: "catalog-1", Category: interfaces.ResourceCategoryTable}}, nil)

	resultJSON := `{"logic_views":[{"action":"create","name":"订单汇总","source_identifier":"order-summary","source_resources":["resource-1"],"logic_definition":[{"id":"source","type":"resource"}]}]}`
	_, err := worker.applyCatalogResult(context.Background(), task, resultJSON, nil)

	require.ErrorContains(t, err, "source_identifier must be lower snake_case")
}

func TestParseBknAgentResult(t *testing.T) {
	t.Run("parses pure json", func(t *testing.T) {
		gotResult, gotConfidence, gotDetail, err := parseBknAgentResult(&interfaces.BknAgentTask{
			Result: []byte(`{"confidence":0.9,"fields":[{"name":"name"}],"ignored":true}`),
		})

		require.NoError(t, err)
		assert.JSONEq(t, `{"confidence":0.9,"fields":[{"name":"name"}],"ignored":true}`, gotResult)
		assert.Equal(t, 0.9, gotConfidence)
		assert.JSONEq(t, `{"fields":[{"name":"name"}]}`, gotDetail)
	})

	t.Run("extracts json object from agent text", func(t *testing.T) {
		gotResult, gotConfidence, gotDetail, err := parseBknAgentResult(&interfaces.BknAgentTask{
			Result: []byte(`No knowledge networks exist. {"confidence":0.8,"logic_views":[],"warnings":["keep {braces} in string"],"obsolete_logic_views":[]} extra text`),
		})

		require.NoError(t, err)
		assert.JSONEq(t, `{"confidence":0.8,"logic_views":[],"warnings":["keep {braces} in string"],"obsolete_logic_views":[]}`, gotResult)
		assert.Equal(t, 0.8, gotConfidence)
		assert.JSONEq(t, `{"logic_views":[],"warnings":["keep {braces} in string"],"obsolete_logic_views":[]}`, gotDetail)
	})
}

func TestAssessResourceSemanticResultQuality(t *testing.T) {
	input := `{
        "resource": {
            "name": "supply_chain.supplier_entity",
            "description": "供应商主数据",
            "schema_definition": [{
                "name": "supplier_id",
                "display_name": "supplier_id",
                "description": "供应商ID"
            }]
        }
    }`

	t.Run("marks no-op field output as low quality", func(t *testing.T) {
		result, confidence, detail, err := assessResourceSemanticResultQuality(
			`{"confidence":1,"resource":{"display_name":"supply_chain.supplier_entity","description":"供应商主数据"},"fields":[{"name":"supplier_id","display_name":"Supplier ID","description":"供应商ID"}],"warnings":[]}`,
			input,
			`{"resource":{"display_name":"supply_chain.supplier_entity","description":"供应商主数据"},"fields":[{"name":"supplier_id","display_name":"Supplier ID","description":"供应商ID"}],"warnings":[]}`,
			1,
		)

		require.NoError(t, err)
		assert.Zero(t, confidence)
		assert.JSONEq(t, `{
            "confidence": 1,
            "resource": {"display_name": "supply_chain.supplier_entity", "description": "供应商主数据"},
            "fields": [{"name": "supplier_id", "display_name": "Supplier ID", "description": "供应商ID"}],
			"warnings": []
        }`, result)
		assert.JSONEq(t, `{
            "resource": {"display_name": "supply_chain.supplier_entity", "description": "供应商主数据"},
            "fields": [{"name": "supplier_id", "display_name": "Supplier ID", "description": "供应商ID"}],
            "warnings": ["no effective field semantic enhancements: all field display names/descriptions are unchanged or invalid"],
            "quality": {"resource_effective": false, "field_total": 1, "field_effective": 0}
        }`, detail)
	})

	t.Run("preserves completed task semantics when the input snapshot is missing or invalid", func(t *testing.T) {
		resultJSON := `{"confidence":0.9,"resource":{},"fields":[]}`
		for _, inputJSON := range []string{"", "not-json"} {
			result, confidence, detail, err := assessResourceSemanticResultQuality(resultJSON, inputJSON, `{"fields":[]}`, 0.9)

			require.NoError(t, err)
			assert.Equal(t, resultJSON, result)
			assert.Equal(t, 0.9, confidence)
			assert.Equal(t, `{"fields":[]}`, detail)
		}
	})

	t.Run("keeps agent confidence for valid resource-only update", func(t *testing.T) {
		_, confidence, detail, err := assessResourceSemanticResultQuality(
			`{"confidence":1,"resource":{"display_name":"供应商主数据","description":"供应商业务主数据"},"fields":[{"name":"supplier_id","display_name":"supplier_id","description":"供应商ID"}],"warnings":[]}`,
			input,
			`{"resource":{"display_name":"供应商主数据","description":"供应商业务主数据"},"fields":[{"name":"supplier_id","display_name":"supplier_id","description":"供应商ID"}],"warnings":[]}`,
			1,
		)

		require.NoError(t, err)
		assert.Equal(t, 1.0, confidence)
		assert.JSONEq(t, `{
            "resource": {"display_name": "供应商主数据", "description": "供应商业务主数据"},
            "fields": [{"name": "supplier_id", "display_name": "supplier_id", "description": "供应商ID"}],
            "warnings": ["no effective field semantic enhancements: all field display names/descriptions are unchanged or invalid"],
            "quality": {"resource_effective": true, "field_total": 1, "field_effective": 0}
        }`, detail)
	})
}
