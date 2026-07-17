// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
)

func semanticUnderstandingWorkerTask(t *testing.T, taskID string) *asynq.Task {
	t.Helper()

	payload, err := sonic.Marshal(&interfaces.SemanticUnderstandingTaskMessage{TaskID: taskID})
	require.NoError(t, err)
	return asynq.NewTask(interfaces.SemanticUnderstandingTaskType, payload)
}

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

func TestSemanticUnderstandingTaskWorkerHandleTask(t *testing.T) {
	t.Run("runs agent and marks succeeded", func(t *testing.T) {
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
				Result: []byte(`{"confidence":0.82,"resource":{"display_name":"Business Resource","description":"business resource","confidence":0.82},"fields":[{"name":"id","display_name":"ID","description":"identifier","confidence":0.81}],"warnings":[]}`),
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
				assert.Equal(t, "ID", got.SchemaDefinition[0].DisplayName)
				assert.Equal(t, "identifier", got.SchemaDefinition[0].Description)
				assert.Equal(t, "account-1", got.Updater.ID)
				assert.NotZero(t, got.UpdateTime)
				return nil
			})
		taskService.EXPECT().
			MarkSucceeded(gomock.Any(), "semantic-task-1", `{"confidence":0.82,"resource":{"display_name":"Business Resource","description":"business resource","confidence":0.82},"fields":[{"name":"id","display_name":"ID","description":"identifier","confidence":0.81}],"warnings":[]}`, 0.82, gomock.Any()).
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
				assert.JSONEq(t, `{"resource_updated":true,"updated_resource":["name","description"],"updated_fields":["id"]}`, detailJSON)
				return true, nil
			})

		err := worker.HandleTask(context.Background(), semanticUnderstandingWorkerTask(t, "semantic-task-1"))

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

		err := worker.HandleTask(context.Background(), semanticUnderstandingWorkerTask(t, "semantic-task-1"))

		require.NoError(t, err)
	})

	t.Run("resumes applying succeeded task", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		taskService := vmock.NewMockSemanticUnderstandingTaskService(ctrl)
		worker := &SemanticUnderstandingTaskWorker{suts: taskService}
		semanticTask := &interfaces.SemanticUnderstandingTask{
			ID:                  "semantic-task-1",
			Scope:               interfaces.SemanticUnderstandingTaskScopeResource,
			Status:              interfaces.SemanticUnderstandingTaskStatusSucceeded,
			ApplyMode:           interfaces.SemanticUnderstandingApplyModeDryRun,
			ConfidenceThreshold: 0.75,
			Confidence:          0.9,
			ResultJSON:          `{"confidence":0.9}`,
			Creator:             interfaces.AccountInfo{ID: "account-1"},
		}
		taskService.EXPECT().
			InternalGetByID(gomock.Any(), "semantic-task-1").
			Return(semanticTask, nil)
		taskService.EXPECT().
			MarkApplied(ctxWithAccountID(t, "account-1"), "semantic-task-1", false, gomock.Any()).
			Return(true, nil)

		err := worker.HandleTask(context.Background(), semanticUnderstandingWorkerTask(t, "semantic-task-1"))

		require.NoError(t, err)
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

		err := worker.HandleTask(context.Background(), semanticUnderstandingWorkerTask(t, "semantic-task-1"))

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

		err := worker.HandleTask(context.Background(), semanticUnderstandingWorkerTask(t, "semantic-task-1"))

		require.ErrorContains(t, err, "temporary agent error")
	})

	t.Run("marks unapplied detail when confidence is below threshold", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		taskService := vmock.NewMockSemanticUnderstandingTaskService(ctrl)
		agentService := vmock.NewMockBknAgentService(ctrl)
		worker := &SemanticUnderstandingTaskWorker{
			suts: taskService,
			bas:  agentService,
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
		agentService.EXPECT().
			WaitResult(gomock.Any(), "agent-task-1").
			Return(&interfaces.BknAgentTask{
				TaskID: "agent-task-1",
				Status: interfaces.BknAgentTaskStatusSucceeded,
				Result: []byte(`{"confidence":0.8,"resource":{"description":"business resource"},"fields":[]}`),
			}, nil)
		taskService.EXPECT().
			MarkSucceeded(gomock.Any(), "semantic-task-1", `{"confidence":0.8,"resource":{"description":"business resource"},"fields":[]}`, 0.8, gomock.Any()).
			Return(true, nil)
		taskService.EXPECT().
			MarkApplied(gomock.Any(), "semantic-task-1", false, gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, applied bool, detailJSON string) (bool, error) {
				assert.False(t, applied)
				assert.JSONEq(t, `{"reason":"confidence_below_threshold","confidence":0.8,"confidence_threshold":0.9,"scope":"resource"}`, detailJSON)
				return true, nil
			})

		err := worker.HandleTask(context.Background(), semanticUnderstandingWorkerTask(t, "semantic-task-1"))

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
		assert.JSONEq(t, `{"resource_updated":false,"skipped_fields":["missing: not found"]}`, got.DetailJSON)
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
