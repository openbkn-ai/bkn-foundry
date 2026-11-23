// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package build_task

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	verrors "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	vmock "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/mock"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/connector/factory"
	resourcelogic "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/resource"
)

type analyzerValidatingIndexManager struct {
	interfaces.LocalIndexManager
	available bool
	err       error
	captured  []string
}

func TestBuildTaskServiceInternalMarkRunning(t *testing.T) {
	t.Run("delegates with caller transaction", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		mockBTA := vmock.NewMockBuildTaskAccess(ctrl)
		service := &buildTaskService{bta: mockBTA}
		tx := &sql.Tx{}
		indexName := "published-index"
		mockBTA.EXPECT().MarkRunning(gomock.Any(), tx, "task-1", &indexName, gomock.Any()).Return(true, nil)

		updated, err := service.InternalMarkRunning(context.Background(), tx, "task-1", &indexName)

		require.NoError(t, err)
		assert.True(t, updated)
	})
}

func TestBuildTaskServiceInternalSetProgress(t *testing.T) {
	t.Run("delegates progress with caller transaction", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bta := vmock.NewMockBuildTaskAccess(ctrl)
		service := &buildTaskService{bta: bta}
		tx := &sql.Tx{}
		syncedCount := int64(10)
		syncedMark := `{"id":10}`
		progress := interfaces.BuildTaskProgress{SyncedCount: &syncedCount, SyncedMark: &syncedMark}
		allowedStatuses := []string{interfaces.BuildTaskStatusRunning}
		bta.EXPECT().SetProgress(gomock.Any(), tx, "task-1", progress, allowedStatuses, gomock.Any()).Return(true, nil)

		updated, err := service.InternalSetProgress(context.Background(), tx, "task-1", progress, allowedStatuses)

		require.NoError(t, err)
		assert.True(t, updated)
	})
}

func TestBuildTaskServiceInternalMarkFailed(t *testing.T) {
	t.Run("delegates with caller transaction", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bta := vmock.NewMockBuildTaskAccess(ctrl)
		service := &buildTaskService{bta: bta}
		tx := &sql.Tx{}
		bta.EXPECT().MarkFailed(gomock.Any(), tx, "task-1", "execution failed", activeBuildTaskStatuses, gomock.Any()).Return(true, nil)

		updated, err := service.InternalMarkFailed(context.Background(), tx, "task-1", "execution failed", nil)

		require.NoError(t, err)
		assert.True(t, updated)
	})
}

func TestBuildTaskServiceInternalMarkCancelled(t *testing.T) {
	t.Run("delegates cancellation with caller transaction", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bta := vmock.NewMockBuildTaskAccess(ctrl)
		service := &buildTaskService{bta: bta}
		tx := &sql.Tx{}
		bta.EXPECT().MarkCancelled(gomock.Any(), tx, "task-1", "resource deleted", gomock.Any()).Return(true, nil)

		updated, err := service.InternalMarkCancelled(context.Background(), tx, "task-1", "resource deleted")

		require.NoError(t, err)
		assert.True(t, updated)
	})
}

func TestBuildTaskServiceInternalMarkStopped(t *testing.T) {
	t.Run("delegates stopped transition with caller transaction", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bta := vmock.NewMockBuildTaskAccess(ctrl)
		service := &buildTaskService{bta: bta}
		tx := &sql.Tx{}
		bta.EXPECT().MarkStopped(gomock.Any(), tx, "task-1", []string{interfaces.BuildTaskStatusStopping}, gomock.Any()).Return(true, nil)

		updated, err := service.InternalMarkStopped(context.Background(), tx, "task-1", nil)

		require.NoError(t, err)
		assert.True(t, updated)
	})
}

func TestBuildTaskServiceInternalMarkCompleted(t *testing.T) {
	t.Run("delegates completed transition with caller transaction", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bta := vmock.NewMockBuildTaskAccess(ctrl)
		service := &buildTaskService{bta: bta}
		tx := &sql.Tx{}
		bta.EXPECT().MarkCompleted(gomock.Any(), tx, "task-1", gomock.Any()).Return(true, nil)

		updated, err := service.InternalMarkCompleted(context.Background(), tx, "task-1")

		require.NoError(t, err)
		assert.True(t, updated)
	})
}

func (m *analyzerValidatingIndexManager) ValidateAnalyzer(_ context.Context, analyzer string) (bool, error) {
	m.captured = append(m.captured, analyzer)
	return m.available, m.err
}

func TestBuildTaskServiceFillBuildTaskIndexSnapshot(t *testing.T) {
	t.Run("rejects invalid decimal declarations before creating a task", func(t *testing.T) {
		for _, original := range []string{"numeric", "decimal(0,13)", "decimal(1000,309)"} {
			service := &buildTaskService{}
			task := &interfaces.BuildTask{}
			err := service.fillBuildTaskIndexSnapshot(context.Background(), &interfaces.Resource{SchemaDefinition: []*interfaces.Property{{Name: "amount", Type: interfaces.DataType_Decimal, OriginalType: original}}}, task)
			_ = requireHTTPError(t, err, verrors.VegaBackend_InvalidParameter_RequestBody)
			assert.Nil(t, task.IndexConfig)
		}
	})
	t.Run("snapshots wide decimal declarations without rejecting their unused value range", func(t *testing.T) {
		for _, original := range []string{"decimal(18,2)", "decimal(38,18)", "numeric(3,5)", "decimal(12,13)"} {
			service := &buildTaskService{}
			task := &interfaces.BuildTask{}
			err := service.fillBuildTaskIndexSnapshot(context.Background(), &interfaces.Resource{SchemaDefinition: []*interfaces.Property{{Name: "amount", Type: interfaces.DataType_Decimal, OriginalType: original}}}, task)
			require.NoError(t, err)
			require.Len(t, task.IndexConfig.Fields, 1)
			assert.NotNil(t, task.IndexConfig.Fields[0].Decimal)
		}
	})
	t.Run("rejects missing embedding model", func(t *testing.T) {
		service := &buildTaskService{}
		buildTask := &interfaces.BuildTask{}
		err := service.fillBuildTaskIndexSnapshot(context.Background(), &interfaces.Resource{SchemaDefinition: []*interfaces.Property{{
			Name: "title", Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Vector}},
		}}}, buildTask)
		_ = requireHTTPError(t, err, verrors.VegaBackend_BuildTask_InvalidParameter_EmbeddingModel)
	})
	t.Run("rejects duplicate feature type", func(t *testing.T) {
		service := &buildTaskService{}
		buildTask := &interfaces.BuildTask{}

		err := service.fillBuildTaskIndexSnapshot(context.Background(), &interfaces.Resource{
			SchemaDefinition: []*interfaces.Property{{
				Name: "title",
				Features: []interfaces.PropertyFeature{
					{FeatureType: interfaces.PropertyFeatureType_Fulltext},
					{FeatureType: interfaces.PropertyFeatureType_Fulltext},
				},
			}},
		}, buildTask)

		httpErr := requireHTTPError(t, err, verrors.VegaBackend_InvalidParameter_RequestBody)
		assert.Contains(t, httpErr.ErrorDetails, `property "title" has more than one "fulltext" feature`)
	})
	t.Run("uses only referenced vector field owner model", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mfs := vmock.NewMockModelFactoryService(ctrl)
		service := &buildTaskService{mfs: mfs}
		buildTask := &interfaces.BuildTask{}
		resource := &interfaces.Resource{
			SchemaDefinition: []*interfaces.Property{
				{Name: "content", Type: interfaces.DataType_Text, Features: []interfaces.PropertyFeature{{
					FeatureType: interfaces.PropertyFeatureType_Vector,
					RefProperty: "embedding",
				}}},
				{Name: "embedding", Type: interfaces.DataType_Vector, Features: []interfaces.PropertyFeature{{
					FeatureType: interfaces.PropertyFeatureType_Vector,
					Config: map[string]any{
						"embedding_model": "target-model",
						"dimension":       3,
					},
				}}},
			},
		}
		mfs.EXPECT().GetModelByID(gomock.Any(), "target-model").Return(&interfaces.SmallModel{
			ModelID: "target-model", EmbeddingDim: 3,
		}, nil)

		err := service.fillBuildTaskIndexSnapshot(context.Background(), resource, buildTask)

		require.NoError(t, err)
		require.NotNil(t, buildTask.IndexConfig.Features["embedding"].Vector)
		assert.Equal(t, "target-model", buildTask.IndexConfig.Features["embedding"].Vector.ModelID)
	})
}

func TestValidateBuildTaskAnalyzers(t *testing.T) {
	buildTask := &interfaces.BuildTask{IndexConfig: &interfaces.BuildTaskIndexConfig{
		Features: map[string]interfaces.BuildTaskFieldIndexFeature{
			"status": {Fulltext: &interfaces.BuildTaskFulltextConfig{Analyzer: "hanlp_index"}},
		},
	}}

	t.Run("maps transport failure to build task internal error", func(t *testing.T) {
		validator := &analyzerValidatingIndexManager{err: errors.New("connect OpenSearch: connection refused")}

		err := validateBuildTaskAnalyzers(context.Background(), validator, buildTask)

		httpErr := requireHTTPError(t, err, verrors.VegaBackend_BuildTask_InternalError_ValidateAnalyzerFailed)
		assert.Equal(t, http.StatusInternalServerError, httpErr.HTTPCode)
	})
	t.Run("preserves capability unavailable error", func(t *testing.T) {
		validator := &analyzerValidatingIndexManager{err: &interfaces.IndexCapabilitiesUnavailableError{Cause: errors.New("connection refused")}}

		err := validateBuildTaskAnalyzers(context.Background(), validator, buildTask)

		httpErr := requireHTTPError(t, err, verrors.VegaBackend_IndexCapability_InternalError_Unavailable)
		assert.Equal(t, http.StatusServiceUnavailable, httpErr.HTTPCode)
		assert.Contains(t, httpErr.ErrorDetails, "connection refused")
	})
}

func TestBuildTaskServiceList(t *testing.T) {
	t.Run("list populates only referenced resources and catalogs", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockBTA := vmock.NewMockBuildTaskAccess(ctrl)
		mockCS := vmock.NewMockCatalogService(ctrl)
		mockRS := vmock.NewMockResourceService(ctrl)
		mockCS.EXPECT().ListPermittedCatalogIDs(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return([]string{"catalog-1"}, nil)
		mockUMS := vmock.NewMockUserMgmtService(ctrl)
		service := &buildTaskService{bta: mockBTA, cs: mockCS, rs: mockRS, ums: mockUMS}
		tasks := []*interfaces.BuildTaskSummary{
			{ID: "task-1", ResourceID: "resource-1", CatalogID: "catalog-1"},
			{ID: "task-2", ResourceID: "resource-2", CatalogID: "catalog-1"},
		}

		mockBTA.EXPECT().List(gomock.Any(), gomock.Any()).Return(tasks, int64(2), nil)
		mockRS.EXPECT().InternalGetByIDs(gomock.Any(), []string{"resource-1", "resource-2"}).Return(map[string]*interfaces.Resource{
			"resource-1": {ID: "resource-1", Name: "orders"},
			"resource-2": {ID: "resource-2", Name: "customers"},
		}, nil)
		mockCS.EXPECT().InternalGetByIDs(gomock.Any(), []string{"catalog-1"}).Return(map[string]*interfaces.Catalog{"catalog-1": {ID: "catalog-1", Name: "production"}}, nil)
		mockUMS.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil)

		got, total, err := service.List(context.Background(), interfaces.BuildTasksQueryParams{})

		require.NoError(t, err)
		assert.Equal(t, int64(2), total)
		assert.Equal(t, "orders", got[0].ResourceName)
		assert.Equal(t, "customers", got[1].ResourceName)
		assert.Equal(t, "production", got[0].CatalogName)
	})

	t.Run("list keeps tasks when reference lookup fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockBTA := vmock.NewMockBuildTaskAccess(ctrl)
		mockCS := vmock.NewMockCatalogService(ctrl)
		mockRS := vmock.NewMockResourceService(ctrl)
		mockCS.EXPECT().ListPermittedCatalogIDs(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return([]string{"catalog-1"}, nil)
		mockUMS := vmock.NewMockUserMgmtService(ctrl)
		service := &buildTaskService{bta: mockBTA, cs: mockCS, rs: mockRS, ums: mockUMS}
		tasks := []*interfaces.BuildTaskSummary{{ID: "task-1", ResourceID: "resource-1", CatalogID: "catalog-1"}}

		mockBTA.EXPECT().List(gomock.Any(), gomock.Any()).Return(tasks, int64(1), nil)
		mockRS.EXPECT().InternalGetByIDs(gomock.Any(), []string{"resource-1"}).Return(nil, errors.New("resource service down"))
		mockCS.EXPECT().InternalGetByIDs(gomock.Any(), []string{"catalog-1"}).Return(map[string]*interfaces.Catalog{"catalog-1": {ID: "catalog-1", Name: "production"}}, nil)
		mockUMS.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil)

		got, total, err := service.List(context.Background(), interfaces.BuildTasksQueryParams{})

		require.NoError(t, err)
		assert.Equal(t, int64(1), total)
		assert.Equal(t, "task-1", got[0].ID)
		assert.Empty(t, got[0].ResourceName)
		assert.Equal(t, "production", got[0].CatalogName)
	})

	t.Run("maps access failure to internal error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bta := vmock.NewMockBuildTaskAccess(ctrl)
		cs := vmock.NewMockCatalogService(ctrl)
		service := &buildTaskService{bta: bta, cs: cs}
		cs.EXPECT().ListPermittedCatalogIDs(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return([]string{"catalog-1"}, nil)
		bta.EXPECT().List(gomock.Any(), interfaces.BuildTasksQueryParams{CatalogIDs: []string{"catalog-1"}}).
			Return(nil, int64(0), errors.New("list failed"))

		got, total, err := service.List(context.Background(), interfaces.BuildTasksQueryParams{})

		httpErr := requireHTTPError(t, err, verrors.VegaBackend_BuildTask_InternalError_GetFailed)
		assert.Nil(t, got)
		assert.Zero(t, total)
		assert.Contains(t, httpErr.ErrorDetails, "list failed")
	})
}

func TestBuildTaskServicePopulateBuildTaskReferences(t *testing.T) {
	for _, tc := range []struct {
		name        string
		lookupError error
	}{
		{name: "populates the schedule with other current references"},
		{name: "preserves other references when schedule lookup fails", lookupError: errors.New("schedule lookup failed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			bss := vmock.NewMockBuildScheduleService(ctrl)
			cs := vmock.NewMockCatalogService(ctrl)
			rs := vmock.NewMockResourceService(ctrl)
			service := &buildTaskService{bss: bss, cs: cs, rs: rs}
			task := &interfaces.BuildTask{
				ID: "task-1", ResourceID: "resource-1", CatalogID: "catalog-1",
				ScheduleID: "plan-1", TriggerType: interfaces.BuildTaskTriggerScheduled,
			}
			rs.EXPECT().InternalGetByIDs(gomock.Any(), []string{task.ResourceID}).Return(
				map[string]*interfaces.Resource{task.ResourceID: {Name: "orders"}}, nil)
			cs.EXPECT().InternalGetByIDs(gomock.Any(), []string{task.CatalogID}).Return(
				map[string]*interfaces.Catalog{task.CatalogID: {Name: "warehouse"}}, nil)
			plans := map[string]*interfaces.BuildSchedule{
				task.ScheduleID: {ID: task.ScheduleID, Name: "Nightly rebuild"},
			}
			if tc.lookupError != nil {
				plans = nil
			}
			bss.EXPECT().InternalGetByIDs(gomock.Any(), []string{task.ScheduleID, "missing-plan"}).Return(plans, tc.lookupError)

			duplicate := *task
			duplicate.ID = "task-2"
			missing := *task
			missing.ID, missing.ScheduleID = "task-3", "missing-plan"
			manual := *task
			manual.ID, manual.TriggerType = "task-4", interfaces.BuildTaskTriggerManual
			emptyID := *task
			emptyID.ID, emptyID.ScheduleID = "task-5", ""
			err := service.populateBuildTaskReferences(context.Background(), []*interfaces.BuildTask{task, &duplicate, &missing, &manual, &emptyID})

			assert.Equal(t, "orders", task.ResourceName)
			assert.Equal(t, "warehouse", task.CatalogName)
			assert.Equal(t, "plan-1", task.ScheduleID)
			assert.Equal(t, task.ScheduleName, duplicate.ScheduleName)
			assert.Empty(t, missing.ScheduleName)
			assert.Equal(t, "missing-plan", missing.ScheduleID)
			assert.Empty(t, manual.ScheduleName)
			assert.Empty(t, emptyID.ScheduleName)
			if tc.lookupError != nil {
				assert.ErrorIs(t, err, tc.lookupError)
				assert.Empty(t, task.ScheduleName)
			} else {
				require.NoError(t, err)
				assert.Equal(t, "Nightly rebuild", task.ScheduleName)
			}
		})
	}
}

func TestBuildTaskServiceGetByID(t *testing.T) {
	for _, tc := range []struct {
		name         string
		triggerType  string
		scheduleID   string
		schedule     *interfaces.BuildSchedule
		lookupError  error
		expectedName string
	}{
		{
			name:         "populates the current schedule name",
			triggerType:  interfaces.BuildTaskTriggerScheduled,
			scheduleID:   "plan-1",
			schedule:     &interfaces.BuildSchedule{ID: "plan-1", Name: "每日订单全量构建"},
			expectedName: "每日订单全量构建",
		},
		{
			name:        "keeps the schedule ID after the schedule is deleted",
			triggerType: interfaces.BuildTaskTriggerScheduled,
			scheduleID:  "deleted-plan",
		},
		{
			name:        "keeps the task when the schedule lookup fails",
			triggerType: interfaces.BuildTaskTriggerScheduled,
			scheduleID:  "plan-1",
			lookupError: errors.New("schedule lookup failed"),
		},
		{
			name:        "uses no schedule IDs for manual tasks",
			triggerType: interfaces.BuildTaskTriggerManual,
		},
		{
			name:        "does not infer scheduled origin from an ID alone",
			triggerType: interfaces.BuildTaskTriggerManual,
			scheduleID:  "plan-1",
		},
		{
			name:        "uses no schedule IDs when the schedule ID is missing",
			triggerType: interfaces.BuildTaskTriggerScheduled,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			bta := vmock.NewMockBuildTaskAccess(ctrl)
			bss := vmock.NewMockBuildScheduleService(ctrl)
			cs := vmock.NewMockCatalogService(ctrl)
			rs := vmock.NewMockResourceService(ctrl)
			ums := vmock.NewMockUserMgmtService(ctrl)
			service := &buildTaskService{bta: bta, bss: bss, cs: cs, rs: rs, ums: ums}
			task := &interfaces.BuildTask{
				ID: "task-1", ResourceID: "resource-1", CatalogID: "catalog-1",
				ScheduleID: tc.scheduleID, TriggerType: tc.triggerType,
			}
			readTask := bta.EXPECT().GetByID(gomock.Any(), task.ID).Return(task, nil)
			permission := cs.EXPECT().CheckCatalogPermission(gomock.Any(), task.CatalogID,
				[]string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true).Return(true, nil, nil).After(readTask)
			scheduleIDs := []string{}
			if tc.triggerType == interfaces.BuildTaskTriggerScheduled && tc.scheduleID != "" {
				scheduleIDs = append(scheduleIDs, tc.scheduleID)
			}
			plans := map[string]*interfaces.BuildSchedule{}
			if tc.schedule != nil {
				plans[tc.scheduleID] = tc.schedule
			}
			bss.EXPECT().InternalGetByIDs(gomock.Any(), scheduleIDs).Return(plans, tc.lookupError).After(permission)
			rs.EXPECT().InternalGetByIDs(gomock.Any(), []string{task.ResourceID}).Return(map[string]*interfaces.Resource{}, nil)
			cs.EXPECT().InternalGetByIDs(gomock.Any(), []string{task.CatalogID}).Return(map[string]*interfaces.Catalog{}, nil)
			ums.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil)

			got, err := service.GetByID(context.Background(), task.ID)

			require.NoError(t, err)
			require.NotNil(t, got)
			assert.Equal(t, tc.scheduleID, got.ScheduleID)
			assert.Equal(t, tc.expectedName, got.ScheduleName)
			body, err := json.Marshal(got)
			require.NoError(t, err)
			var response map[string]any
			require.NoError(t, json.Unmarshal(body, &response))
			assert.Equal(t, tc.scheduleID, response["schedule_id"])
			if tc.expectedName == "" {
				assert.NotContains(t, response, "schedule_name")
			} else {
				assert.Equal(t, tc.expectedName, response["schedule_name"])
			}
			assert.NotContains(t, string(body), "schedule lookup failed")
		})
	}

	for _, permissionError := range []error{nil, errors.New("permission lookup failed")} {
		t.Run(fmt.Sprintf("does not read schedule before authorization error=%v", permissionError), func(t *testing.T) {
			ctrl := gomock.NewController(t)
			bta := vmock.NewMockBuildTaskAccess(ctrl)
			bss := vmock.NewMockBuildScheduleService(ctrl)
			cs := vmock.NewMockCatalogService(ctrl)
			service := &buildTaskService{bta: bta, bss: bss, cs: cs}
			task := &interfaces.BuildTask{
				ID: "task-1", CatalogID: "catalog-1", ScheduleID: "plan-1",
				TriggerType: interfaces.BuildTaskTriggerScheduled,
			}
			bta.EXPECT().GetByID(gomock.Any(), task.ID).Return(task, nil)
			cs.EXPECT().CheckCatalogPermission(gomock.Any(), task.CatalogID,
				[]string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true).Return(false, nil, permissionError)

			got, err := service.GetByID(context.Background(), task.ID)

			require.Error(t, err)
			assert.Nil(t, got)
			if permissionError != nil {
				assert.ErrorIs(t, err, permissionError)
			} else {
				_ = requireHTTPError(t, err, httperrors.PublicError_Forbidden)
			}
		})
	}

	t.Run("populates referenced resource and catalog", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockBTA := vmock.NewMockBuildTaskAccess(ctrl)
		mockCS := vmock.NewMockCatalogService(ctrl)
		mockRS := vmock.NewMockResourceService(ctrl)
		mockCS.EXPECT().CheckCatalogPermission(gomock.Any(), "catalog-1", []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true).
			Return(true, nil, nil)
		mockUMS := vmock.NewMockUserMgmtService(ctrl)
		mockBSS := vmock.NewMockBuildScheduleService(ctrl)
		service := &buildTaskService{bta: mockBTA, bss: mockBSS, cs: mockCS, rs: mockRS, ums: mockUMS}
		task := &interfaces.BuildTask{ID: "task-1", ResourceID: "resource-1", CatalogID: "catalog-1"}

		mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").Return(task, nil)
		mockRS.EXPECT().InternalGetByIDs(gomock.Any(), []string{"resource-1"}).Return(map[string]*interfaces.Resource{"resource-1": {ID: "resource-1", Name: "orders"}}, nil)
		mockCS.EXPECT().InternalGetByIDs(gomock.Any(), []string{"catalog-1"}).Return(map[string]*interfaces.Catalog{"catalog-1": {ID: "catalog-1", Name: "production"}}, nil)
		mockBSS.EXPECT().InternalGetByIDs(gomock.Any(), []string{}).Return(map[string]*interfaces.BuildSchedule{}, nil)
		mockUMS.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil)

		got, err := service.GetByID(context.Background(), "task-1")

		require.NoError(t, err)
		assert.Equal(t, "orders", got.ResourceName)
		assert.Equal(t, "production", got.CatalogName)
	})
	t.Run("keeps task when account lookup fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockBTA := vmock.NewMockBuildTaskAccess(ctrl)
		mockCS := vmock.NewMockCatalogService(ctrl)
		mockRS := vmock.NewMockResourceService(ctrl)
		mockCS.EXPECT().CheckCatalogPermission(gomock.Any(), "", []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true).
			Return(true, nil, nil)
		mockUMS := vmock.NewMockUserMgmtService(ctrl)
		mockBSS := vmock.NewMockBuildScheduleService(ctrl)
		service := &buildTaskService{bta: mockBTA, bss: mockBSS, cs: mockCS, rs: mockRS, ums: mockUMS}
		task := &interfaces.BuildTask{ID: "task-2"}

		mockBTA.EXPECT().GetByID(gomock.Any(), "task-2").Return(task, nil)
		mockRS.EXPECT().InternalGetByIDs(gomock.Any(), []string{}).Return(map[string]*interfaces.Resource{}, nil)
		mockCS.EXPECT().InternalGetByIDs(gomock.Any(), []string{}).Return(map[string]*interfaces.Catalog{}, nil)
		mockBSS.EXPECT().InternalGetByIDs(gomock.Any(), []string{}).Return(map[string]*interfaces.BuildSchedule{}, nil)
		mockUMS.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(errors.New("user service down"))

		got, err := service.GetByID(context.Background(), "task-2")

		require.NoError(t, err)
		assert.Equal(t, "task-2", got.ID)
	})

	t.Run("maps access failure to internal error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bta := vmock.NewMockBuildTaskAccess(ctrl)
		service := &buildTaskService{bta: bta}
		bta.EXPECT().GetByID(gomock.Any(), "task-1").Return(nil, errors.New("get failed"))

		got, err := service.GetByID(context.Background(), "task-1")

		httpErr := requireHTTPError(t, err, verrors.VegaBackend_BuildTask_InternalError_GetFailed)
		assert.Nil(t, got)
		assert.Contains(t, httpErr.ErrorDetails, "get failed")
	})

	t.Run("returns not found", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bta := vmock.NewMockBuildTaskAccess(ctrl)
		service := &buildTaskService{bta: bta}
		bta.EXPECT().GetByID(gomock.Any(), "missing").Return(nil, nil)

		got, err := service.GetByID(context.Background(), "missing")

		httpErr := requireHTTPError(t, err, verrors.VegaBackend_BuildTask_NotFound)
		assert.Nil(t, got)
		assert.Equal(t, http.StatusNotFound, httpErr.HTTPCode)
	})
}

func TestBuildTaskServiceCreate(t *testing.T) {
	t.Run("API resources cannot create batch or streaming build tasks", func(t *testing.T) {
		for _, mode := range []string{interfaces.BuildTaskModeBatch, interfaces.BuildTaskModeStreaming} {
			t.Run(mode, func(t *testing.T) {
				ctrl := gomock.NewController(t)
				rs := vmock.NewMockResourceService(ctrl)
				cs := vmock.NewMockCatalogService(ctrl)
				access := vmock.NewMockBuildTaskAccess(ctrl)
				service := &buildTaskService{rs: rs, cs: cs, bta: access}
				rs.EXPECT().GetByID(gomock.Any(), "api-resource").Return(&interfaces.Resource{ID: "api-resource", CatalogID: "api-catalog", Category: interfaces.ResourceCategoryAPI}, nil)
				cs.EXPECT().CheckCatalogPermission(gomock.Any(), "api-catalog", []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true).Return(true, nil, nil)
				access.EXPECT().Create(gomock.Any(), gomock.Any()).Times(0)
				id, err := service.Create(t.Context(), &interfaces.CreateBuildTaskRequest{ResourceID: "api-resource", Mode: mode})
				assert.Empty(t, id)
				failure := requireHTTPError(t, err, verrors.VegaBackend_Resource_InternalError_InvalidCategory)
				assert.Equal(t, http.StatusBadRequest, failure.HTTPCode)
			})
		}
	})
	for _, scheduled := range []bool{false, true} {
		t.Run(fmt.Sprintf("persists server assigned provenance scheduled=%t", scheduled), func(t *testing.T) {
			ctrl := gomock.NewController(t)
			rs := vmock.NewMockResourceService(ctrl)
			cs := vmock.NewMockCatalogService(ctrl)
			bta := vmock.NewMockBuildTaskAccess(ctrl)
			service := &buildTaskService{cf: buildTaskTestConnectorFactory(t), rs: rs, cs: cs, bta: bta}
			resource := buildTaskTestResource()
			resource.IndexConfig.IncrementalFields = nil
			rs.EXPECT().GetByID(gomock.Any(), resource.ID).Return(resource, nil)
			cs.EXPECT().CheckCatalogPermission(gomock.Any(), resource.CatalogID, []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true).Return(true, nil, nil)
			cs.EXPECT().GetByID(gomock.Any(), resource.CatalogID, false).Return(&interfaces.Catalog{ID: resource.CatalogID, Enabled: true}, nil)
			bta.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return(nil, nil)
			req := &interfaces.CreateBuildTaskRequest{ResourceID: resource.ID, Mode: interfaces.BuildTaskModeBatch}
			wantTrigger := interfaces.BuildTaskTriggerManual
			if scheduled {
				req.ScheduleID = "plan-1"
				req.TriggerType = interfaces.BuildTaskTriggerScheduled
				req.ExecuteType = interfaces.BuildTaskExecuteTypeFull
				wantTrigger = interfaces.BuildTaskTriggerScheduled
			}
			bta.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, task *interfaces.BuildTask) error {
				assert.Equal(t, wantTrigger, task.TriggerType)
				assert.Equal(t, req.ScheduleID, task.ScheduleID)
				assert.Equal(t, interfaces.BuildTaskStatusPending, task.Status)
				assert.Equal(t, interfaces.BuildTaskExecuteTypeFull, task.ExecuteType)
				assert.Equal(t, []string{"id"}, task.IndexConfig.PrimaryKeyFields)
				assert.Empty(t, task.IndexConfig.IncrementalFields)
				return nil
			})
			id, err := service.Create(context.Background(), req)
			require.NoError(t, err)
			assert.NotEmpty(t, id)
		})
	}
	for _, tc := range []struct {
		name        string
		executeType string
		fields      []string
	}{
		{name: "incremental without fields cannot use a primary key baseline", executeType: interfaces.BuildTaskExecuteTypeIncremental},
		{name: "full rejects an unknown incremental field", executeType: interfaces.BuildTaskExecuteTypeFull, fields: []string{"missing"}},
		{name: "full rejects a duplicate incremental field", executeType: interfaces.BuildTaskExecuteTypeFull, fields: []string{"id", "id"}},
		{name: "full rejects an unsupported incremental field", executeType: interfaces.BuildTaskExecuteTypeFull, fields: []string{"attachment"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			rs := vmock.NewMockResourceService(ctrl)
			cs := vmock.NewMockCatalogService(ctrl)
			bta := vmock.NewMockBuildTaskAccess(ctrl)
			service := &buildTaskService{rs: rs, cs: cs, bta: bta, dispatchCh: make(chan struct{}, 1)}
			resource := buildTaskTestResource()
			resource.IndexConfig.IncrementalFields = tc.fields
			resource.SchemaDefinition = append(resource.SchemaDefinition, &interfaces.Property{Name: "attachment", Type: interfaces.DataType_Binary})
			resource.LocalIndexStatus = interfaces.ResourceLocalIndexStatusAvailable
			resource.LocalIndexName = "current-index"
			resource.SyncMark = `{"mode":"batch","cursor":[{"key":"id","value":20}]}`
			rs.EXPECT().GetByID(gomock.Any(), resource.ID).Return(resource, nil)
			cs.EXPECT().CheckCatalogPermission(gomock.Any(), resource.CatalogID, []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true).Return(true, nil, nil)

			id, err := service.Create(t.Context(), &interfaces.CreateBuildTaskRequest{
				ResourceID: resource.ID, Mode: interfaces.BuildTaskModeBatch, ExecuteType: tc.executeType,
			})
			assert.Empty(t, id)
			assert.Equal(t, http.StatusBadRequest, requireHTTPError(t, err, verrors.VegaBackend_BuildTask_InvalidParameter_IncrementalFields).HTTPCode)
			assert.Empty(t, service.dispatchCh)
		})
	}
	for _, source := range []string{"resource", "catalog"} {
		for _, status := range []int{http.StatusForbidden, http.StatusNotFound, http.StatusServiceUnavailable, http.StatusInternalServerError} {
			t.Run(fmt.Sprintf("%s lookup error %d", source, status), func(t *testing.T) {
				ctrl := gomock.NewController(t)
				rs := vmock.NewMockResourceService(ctrl)
				bta := vmock.NewMockBuildTaskAccess(ctrl)
				cs := vmock.NewMockCatalogService(ctrl)
				service := &buildTaskService{rs: rs, bta: bta, cs: cs, dispatchCh: make(chan struct{}, 1)}
				code := httperrors.PublicError_Forbidden
				if status == http.StatusNotFound {
					code = verrors.VegaBackend_Resource_NotFound
				}
				if status == http.StatusServiceUnavailable {
					code = verrors.VegaBackend_Resource_InternalError_GetFailed
				}
				cause := httperrors.NewHTTPError(context.Background(), status, code)
				downstream := fmt.Errorf("lookup: %w", cause)
				if status == http.StatusInternalServerError {
					downstream = errors.New("lookup failed")
				}
				resource := buildTaskTestResource()
				if source == "resource" {
					rs.EXPECT().GetByID(gomock.Any(), resource.ID).Return(nil, downstream)
				} else {
					rs.EXPECT().GetByID(gomock.Any(), resource.ID).Return(resource, nil)
					cs.EXPECT().CheckCatalogPermission(gomock.Any(), resource.CatalogID, []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true).Return(true, nil, nil)
					cs.EXPECT().GetByID(gomock.Any(), resource.CatalogID, false).Return(nil, downstream)
				}
				id, err := service.Create(context.Background(), &interfaces.CreateBuildTaskRequest{ResourceID: resource.ID, Mode: interfaces.BuildTaskModeBatch})
				assert.Empty(t, id)
				if status == http.StatusInternalServerError {
					code = verrors.VegaBackend_Resource_InternalError_GetFailed
					if source == "catalog" {
						code = verrors.VegaBackend_Catalog_InternalError_GetFailed
					}
					httpErr := requireHTTPError(t, err, code)
					assert.Equal(t, status, httpErr.HTTPCode)
				} else {
					assert.Same(t, cause, err)
				}
				assert.Empty(t, service.dispatchCh)
			})
		}
	}
	t.Run("connector rejects schema before side effects", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		cs := vmock.NewMockCatalogService(ctrl)
		rs := vmock.NewMockResourceService(ctrl)
		bta := vmock.NewMockBuildTaskAccess(ctrl)
		cf := vmock.NewMockConnectorFactory(ctrl)
		lim := vmock.NewMockLocalIndexManager(ctrl)
		service := &buildTaskService{cs: cs, rs: rs, bta: bta, cf: cf, lim: lim, dispatchCh: make(chan struct{}, 1)}
		resource := buildTaskTestResource()
		catalog := &interfaces.Catalog{ID: resource.CatalogID, Enabled: true, ConnectorType: "custom-table"}
		cs.EXPECT().CheckCatalogPermission(gomock.Any(), resource.CatalogID, []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true).Return(true, catalog, nil)
		rs.EXPECT().GetByID(gomock.Any(), resource.ID).Return(resource, nil)
		cf.EXPECT().ValidateBuildSchema(catalog.ConnectorType, resource).Return(errors.New("field price does not support local index builds"))
		cs.EXPECT().GetByID(gomock.Any(), resource.CatalogID, false).Return(catalog, nil)
		id, err := service.Create(context.Background(), &interfaces.CreateBuildTaskRequest{ResourceID: resource.ID, Mode: interfaces.BuildTaskModeBatch})
		assert.Empty(t, id)
		httpErr := requireHTTPError(t, err, verrors.VegaBackend_BuildTask_InvalidParameter_UnsupportedSchemaFields)
		assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
		assert.Contains(t, httpErr.ErrorDetails, "price")
		assert.Empty(t, service.dispatchCh)
	})

	t.Run("rejects unavailable field analyzer before persistence", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := vmock.NewMockCatalogService(ctrl)
		mockRS := vmock.NewMockResourceService(ctrl)
		mockCS.EXPECT().CheckCatalogPermission(gomock.Any(), "catalog-1", []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true).
			Return(true, nil, nil)
		mockBTA := vmock.NewMockBuildTaskAccess(ctrl)
		validator := &analyzerValidatingIndexManager{}
		service := &buildTaskService{cf: buildTaskTestConnectorFactory(t),
			bta: mockBTA,
			cs:  mockCS,
			lim: validator,
			rs:  mockRS,
		}

		mockRS.EXPECT().GetByID(gomock.Any(), "resource-1").Return(&interfaces.Resource{
			ID:        "resource-1",
			CatalogID: "catalog-1",
			Category:  interfaces.ResourceCategoryTable,
			IndexConfig: &interfaces.ResourceIndexConfig{
				PrimaryKeyFields:        []string{"id"},
				IncrementalFields:       []string{"id"},
				DefaultFulltextAnalyzer: "standard",
			},
			SchemaDefinition: []*interfaces.Property{
				{Name: "id", Type: interfaces.DataType_Integer},
				{Name: "coupon_code", Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Fulltext, Config: map[string]any{"analyzer": "standard"}}}},
				{Name: "status", Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Fulltext, Config: map[string]any{"analyzer": "hanlp_index"}}}},
			},
		}, nil)
		mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", false).Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
		mockBTA.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return(nil, nil)

		_, err := service.Create(context.Background(), &interfaces.CreateBuildTaskRequest{ResourceID: "resource-1", Mode: interfaces.BuildTaskModeBatch})
		httpErr := requireHTTPError(t, err, verrors.VegaBackend_BuildTask_InvalidParameter_Analyzer)
		assert.Contains(t, httpErr.ErrorDetails, "analyzer")
		assert.Len(t, validator.captured, 1)
	})
	t.Run("rejects batch task without primary key fields", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockRS := vmock.NewMockResourceService(ctrl)
		mockCS := vmock.NewMockCatalogService(ctrl)
		mockCS.EXPECT().CheckCatalogPermission(gomock.Any(), "catalog-1", []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true).
			Return(true, nil, nil)
		service := &buildTaskService{cf: buildTaskTestConnectorFactory(t), rs: mockRS, cs: mockCS}

		mockRS.EXPECT().GetByID(gomock.Any(), "resource-1").Return(&interfaces.Resource{
			ID:        "resource-1",
			CatalogID: "catalog-1",
			Category:  interfaces.ResourceCategoryTable,
		}, nil)

		_, err := service.Create(context.Background(), &interfaces.CreateBuildTaskRequest{
			ResourceID: "resource-1",
			Mode:       interfaces.BuildTaskModeBatch,
		})
		httpErr := requireHTTPError(t, err, verrors.VegaBackend_BuildTask_InvalidParameter_PrimaryKeyFields)
		assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
	})
	t.Run("rejects legacy string and text fields until the resource configuration is re-saved", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockRS := vmock.NewMockResourceService(ctrl)
		mockCS := vmock.NewMockCatalogService(ctrl)
		mockCS.EXPECT().CheckCatalogPermission(gomock.Any(), "catalog-1", []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true).Return(true, nil, nil)
		service := &buildTaskService{cf: buildTaskTestConnectorFactory(t), rs: mockRS, cs: mockCS}

		mockRS.EXPECT().GetByID(gomock.Any(), "resource-1").Return(&interfaces.Resource{
			ID:        "resource-1",
			CatalogID: "catalog-1",
			Category:  interfaces.ResourceCategoryTable,
			IndexConfig: &interfaces.ResourceIndexConfig{
				PrimaryKeyFields:  []string{"id"},
				IncrementalFields: []string{"id"},
			},
			SchemaDefinition: []*interfaces.Property{{
				Name: "id",
				Type: interfaces.DataType_String,
			}, {
				Name: "material_number",
				Type: interfaces.DataType_Text,
				Features: []interfaces.PropertyFeature{{
					FeatureName: "fulltext",
					FeatureType: interfaces.PropertyFeatureType_Fulltext,
				}},
			}},
		}, nil)

		_, err := service.Create(context.Background(), &interfaces.CreateBuildTaskRequest{
			ResourceID: "resource-1",
			Mode:       interfaces.BuildTaskModeBatch,
		})
		httpErr := requireHTTPError(t, err, verrors.VegaBackend_BuildTask_InvalidParameter_UnsupportedSchemaFields)
		assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
		assert.Contains(t, httpErr.ErrorDetails, "id")
		assert.Contains(t, httpErr.ErrorDetails, "material_number")
		assert.Contains(t, httpErr.ErrorDetails, "re-save the resource configuration")
	})
	t.Run("rejects streaming task", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockRS := vmock.NewMockResourceService(ctrl)
		mockCS := vmock.NewMockCatalogService(ctrl)
		mockCS.EXPECT().CheckCatalogPermission(gomock.Any(), "catalog-1", []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true).Return(true, nil, nil)
		service := &buildTaskService{cf: buildTaskTestConnectorFactory(t), rs: mockRS, cs: mockCS}

		mockRS.EXPECT().GetByID(gomock.Any(), "resource-1").Return(&interfaces.Resource{
			ID:        "resource-1",
			CatalogID: "catalog-1",
			Category:  interfaces.ResourceCategoryTable,
		}, nil)

		_, err := service.Create(context.Background(), &interfaces.CreateBuildTaskRequest{
			ResourceID: "resource-1",
			Mode:       interfaces.BuildTaskModeStreaming,
		})
		httpErr := requireHTTPError(t, err, verrors.VegaBackend_BuildTask_StreamingUnsupported)
		assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
	})
	t.Run("allows binary and other fields outside build keys", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := vmock.NewMockCatalogService(ctrl)
		mockRS := vmock.NewMockResourceService(ctrl)
		mockBTA := vmock.NewMockBuildTaskAccess(ctrl)
		mockCS.EXPECT().CheckCatalogPermission(gomock.Any(), "catalog-1", []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true).Return(true, nil, nil)
		service := &buildTaskService{cf: buildTaskTestConnectorFactory(t), cs: mockCS, rs: mockRS, bta: mockBTA}
		resource := buildTaskTestResource()
		resource.SchemaDefinition = append(resource.SchemaDefinition,
			&interfaces.Property{Name: "attachment", Type: interfaces.DataType_Binary, OriginalType: "bytea"},
			&interfaces.Property{Name: "interests", Type: interfaces.DataType_Other, OriginalType: "_text"},
		)

		mockRS.EXPECT().GetByID(gomock.Any(), "resource-1").Return(resource, nil)
		mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", false).Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
		mockBTA.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return(nil, nil)
		mockBTA.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)

		_, err := service.Create(context.Background(), &interfaces.CreateBuildTaskRequest{ResourceID: "resource-1", Mode: interfaces.BuildTaskModeBatch})
		require.NoError(t, err)
	})

	t.Run("rejects disabled catalog", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := vmock.NewMockCatalogService(ctrl)
		mockRS := vmock.NewMockResourceService(ctrl)
		mockCS.EXPECT().CheckCatalogPermission(gomock.Any(), "catalog-1", []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true).Return(true, nil, nil)
		service := &buildTaskService{cf: buildTaskTestConnectorFactory(t), cs: mockCS, rs: mockRS}

		mockRS.EXPECT().GetByID(gomock.Any(), "resource-1").
			Return(&interfaces.Resource{
				ID:               "resource-1",
				CatalogID:        "catalog-1",
				Category:         interfaces.ResourceCategoryTable,
				IndexConfig:      &interfaces.ResourceIndexConfig{PrimaryKeyFields: []string{"id"}, IncrementalFields: []string{"id"}},
				SchemaDefinition: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_Integer}},
			}, nil)
		mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
			Return(&interfaces.Catalog{ID: "catalog-1", Enabled: false}, nil)

		_, err := service.Create(context.Background(), &interfaces.CreateBuildTaskRequest{ResourceID: "resource-1"})
		assertCatalogDisabledError(t, err)
	})
	t.Run("rejects active task for resource", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := vmock.NewMockCatalogService(ctrl)
		mockRS := vmock.NewMockResourceService(ctrl)
		mockCS.EXPECT().CheckCatalogPermission(gomock.Any(), "catalog-1", []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true).Return(true, nil, nil)
		mockBTA := vmock.NewMockBuildTaskAccess(ctrl)
		service := &buildTaskService{cf: buildTaskTestConnectorFactory(t), cs: mockCS, rs: mockRS, bta: mockBTA}

		mockRS.EXPECT().GetByID(gomock.Any(), "resource-1").
			Return(&interfaces.Resource{
				ID:               "resource-1",
				CatalogID:        "catalog-1",
				Category:         interfaces.ResourceCategoryTable,
				IndexConfig:      &interfaces.ResourceIndexConfig{PrimaryKeyFields: []string{"id"}, IncrementalFields: []string{"id"}},
				SchemaDefinition: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_Integer}},
			}, nil)
		mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
			Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
		mockBTA.EXPECT().InternalList(gomock.Any(), gomock.Any()).
			Return([]*interfaces.BuildTaskSummary{{ID: "active-task", ResourceID: "resource-1", Status: interfaces.BuildTaskStatusRunning}}, nil)

		_, err := service.Create(context.Background(), &interfaces.CreateBuildTaskRequest{
			ResourceID: "resource-1",
			Mode:       interfaces.BuildTaskModeBatch,
		})

		_ = requireHTTPError(t, err, verrors.VegaBackend_BuildTask_Exist)
	})
	t.Run("creates incremental task from resource baseline", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := vmock.NewMockCatalogService(ctrl)
		mockRS := vmock.NewMockResourceService(ctrl)
		mockBTA := vmock.NewMockBuildTaskAccess(ctrl)
		mockCS.EXPECT().CheckCatalogPermission(gomock.Any(), "catalog-1", []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true).Return(true, nil, nil)
		service := &buildTaskService{cf: buildTaskTestConnectorFactory(t), cs: mockCS, rs: mockRS, bta: mockBTA}
		resource := buildTaskTestResource()
		resource.LocalIndexStatus = interfaces.ResourceLocalIndexStatusAvailable
		resource.LocalIndexName = "resource-1-index"
		resource.SyncMark = `{"mode":"batch","cursor":[]}`

		mockRS.EXPECT().GetByID(gomock.Any(), "resource-1").Return(resource, nil)
		mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
			Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
		mockBTA.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return(nil, nil)
		var captured *interfaces.BuildTask
		mockBTA.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, task *interfaces.BuildTask) error {
				captured = task
				return nil
			})

		_, err := service.Create(context.Background(), &interfaces.CreateBuildTaskRequest{
			ResourceID:  "resource-1",
			Mode:        interfaces.BuildTaskModeBatch,
			ExecuteType: interfaces.BuildTaskExecuteTypeIncremental,
		})

		require.NoError(t, err)
		require.NotNil(t, captured)
		assert.Equal(t, interfaces.BuildTaskExecuteTypeIncremental, captured.ExecuteType)
		assert.Equal(t, mustBuildTaskIndexConfig(t, resource).Fields, captured.IndexConfig.Fields)
	})
	t.Run("rejects streaming task with configured keys", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := vmock.NewMockCatalogService(ctrl)
		mockRS := vmock.NewMockResourceService(ctrl)
		mockCS.EXPECT().CheckCatalogPermission(gomock.Any(), "catalog-1", []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true).Return(true, nil, nil)
		mockBTA := vmock.NewMockBuildTaskAccess(ctrl)
		service := &buildTaskService{cf: buildTaskTestConnectorFactory(t),
			cs:         mockCS,
			rs:         mockRS,
			bta:        mockBTA,
			dispatchCh: make(chan struct{}, buildTaskDispatchBuffer),
		}

		mockRS.EXPECT().GetByID(gomock.Any(), "resource-1").Return(&interfaces.Resource{
			ID:        "resource-1",
			CatalogID: "catalog-1",
			Category:  interfaces.ResourceCategoryTable,
			IndexConfig: &interfaces.ResourceIndexConfig{
				PrimaryKeyFields:  []string{"supplier_id"},
				IncrementalFields: []string{"supplier_id"},
			},
			SchemaDefinition: []*interfaces.Property{{Name: "supplier_id", Type: interfaces.DataType_Integer}},
		}, nil)
		_, err := service.Create(context.Background(), &interfaces.CreateBuildTaskRequest{
			ResourceID: "resource-1",
			Mode:       interfaces.BuildTaskModeStreaming,
		})

		_ = requireHTTPError(t, err, verrors.VegaBackend_BuildTask_StreamingUnsupported)
	})
	t.Run("rejects execute type for streaming", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockRS := vmock.NewMockResourceService(ctrl)
		mockCS := vmock.NewMockCatalogService(ctrl)
		mockCS.EXPECT().CheckCatalogPermission(gomock.Any(), "catalog-1", []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true).Return(true, nil, nil)
		service := &buildTaskService{cf: buildTaskTestConnectorFactory(t), rs: mockRS, cs: mockCS}

		mockRS.EXPECT().GetByID(gomock.Any(), "resource-1").
			Return(&interfaces.Resource{
				ID:        "resource-1",
				CatalogID: "catalog-1",
				Category:  interfaces.ResourceCategoryTable,
			}, nil)

		_, err := service.Create(context.Background(), &interfaces.CreateBuildTaskRequest{
			ResourceID:  "resource-1",
			Mode:        interfaces.BuildTaskModeStreaming,
			ExecuteType: interfaces.BuildTaskExecuteTypeFull,
		})

		_ = requireHTTPError(t, err, verrors.VegaBackend_BuildTask_StreamingUnsupported)
	})
	t.Run("caches default embedding SmallModel by model ID", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := vmock.NewMockCatalogService(ctrl)
		mockRS := vmock.NewMockResourceService(ctrl)
		mockCS.EXPECT().CheckCatalogPermission(gomock.Any(), "catalog-1", []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true).Return(true, nil, nil)
		mockBTA := vmock.NewMockBuildTaskAccess(ctrl)
		mockMFS := vmock.NewMockModelFactoryService(ctrl)
		service := &buildTaskService{cf: buildTaskTestConnectorFactory(t), cs: mockCS, rs: mockRS, bta: mockBTA, mfs: mockMFS}

		mockRS.EXPECT().GetByID(gomock.Any(), "resource-1").
			Return(&interfaces.Resource{
				ID:        "resource-1",
				CatalogID: "catalog-1",
				Category:  interfaces.ResourceCategoryTable,
				IndexConfig: &interfaces.ResourceIndexConfig{
					PrimaryKeyFields:        []string{"id"},
					IncrementalFields:       []string{"id"},
					DefaultEmbeddingModel:   "2064382281006583808",
					DefaultFulltextAnalyzer: "ik_max_word",
				},
				SchemaDefinition: []*interfaces.Property{
					{Name: "id", Type: interfaces.DataType_Integer},
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
		mockBTA.EXPECT().InternalList(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, params interfaces.BuildTasksQueryParams) ([]*interfaces.BuildTaskSummary, error) {
				if params.ResourceID != "resource-1" {
					require.Equal(t, "resource-1", params.ResourceID)
				}
				return nil, nil
			})
		mockMFS.EXPECT().GetModelByID(gomock.Any(), "2064382281006583808").
			Return(&interfaces.SmallModel{ModelID: "2064382281006583808", ModelName: "text-embedding-v4", EmbeddingDim: 1024}, nil)

		var captured *interfaces.BuildTask
		mockBTA.EXPECT().Create(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, bt *interfaces.BuildTask) error {
				captured = bt
				return nil
			})

		_, err := service.Create(context.Background(), &interfaces.CreateBuildTaskRequest{
			ResourceID: "resource-1",
			Mode:       interfaces.BuildTaskModeBatch,
		})
		require.NoError(t, err)
		require.NotNil(t, captured)
		require.NotNil(t, captured.IndexConfig)
		assert.Equal(t, interfaces.BuildTaskExecuteTypeFull, captured.ExecuteType)
		assert.Equal(t, []string{"id"}, captured.IndexConfig.PrimaryKeyFields)
		expectedModel := &interfaces.SmallModel{ModelID: "2064382281006583808", ModelName: "text-embedding-v4", EmbeddingDim: 1024}
		assert.Equal(t, expectedModel, captured.IndexConfig.Features["family_name"].Vector)
		assert.Equal(t, expectedModel, captured.IndexConfig.Features["given_name"].Vector)
		assert.Equal(t, &interfaces.BuildTaskFulltextConfig{Analyzer: "ik_max_word"}, captured.IndexConfig.Features["family_name"].Fulltext)
	})
	t.Run("snapshot unaffected by resource mutation", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := vmock.NewMockCatalogService(ctrl)
		mockRS := vmock.NewMockResourceService(ctrl)
		mockCS.EXPECT().CheckCatalogPermission(gomock.Any(), "catalog-1", []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true).Return(true, nil, nil)
		mockBTA := vmock.NewMockBuildTaskAccess(ctrl)
		mockMFS := vmock.NewMockModelFactoryService(ctrl)
		service := &buildTaskService{cf: buildTaskTestConnectorFactory(t), cs: mockCS, rs: mockRS, bta: mockBTA, mfs: mockMFS}

		resource := &interfaces.Resource{
			ID:        "resource-1",
			CatalogID: "catalog-1",
			Category:  interfaces.ResourceCategoryTable,
			IndexConfig: &interfaces.ResourceIndexConfig{
				PrimaryKeyFields:        []string{"id"},
				IncrementalFields:       []string{"id"},
				DefaultEmbeddingModel:   "2064382281006583808",
				DefaultFulltextAnalyzer: "ik_max_word",
			},
			SchemaDefinition: []*interfaces.Property{
				{Name: "id", Type: interfaces.DataType_Integer},
				{
					Name: "family_name",
					Features: []interfaces.PropertyFeature{
						{FeatureType: interfaces.PropertyFeatureType_Vector, RefProperty: "family_name"},
						{FeatureType: interfaces.PropertyFeatureType_Fulltext, RefProperty: "family_name"},
					},
				},
			},
		}
		expectedFields := mustBuildTaskIndexConfig(t, resource).Fields
		mockRS.EXPECT().GetByID(gomock.Any(), "resource-1").Return(resource, nil)
		mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
			Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
		mockBTA.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return(nil, nil)
		mockMFS.EXPECT().GetModelByID(gomock.Any(), "2064382281006583808").
			Return(&interfaces.SmallModel{ModelID: "2064382281006583808", ModelName: "text-embedding-v4", EmbeddingDim: 1024}, nil)

		var captured *interfaces.BuildTask
		mockBTA.EXPECT().Create(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, bt *interfaces.BuildTask) error {
				captured = bt
				return nil
			})

		_, err := service.Create(context.Background(), &interfaces.CreateBuildTaskRequest{
			ResourceID: "resource-1",
			Mode:       interfaces.BuildTaskModeBatch,
		})
		require.NoError(t, err)
		require.NotNil(t, captured)
		require.NotNil(t, captured.IndexConfig)

		resource.IndexConfig.DefaultEmbeddingModel = "changed-model"
		resource.SchemaDefinition[0].Features = nil

		assert.Equal(t, []string{"id"}, captured.IndexConfig.PrimaryKeyFields)
		assert.Equal(t, expectedFields, captured.IndexConfig.Fields)
		assert.Equal(t, &interfaces.SmallModel{ModelID: "2064382281006583808", ModelName: "text-embedding-v4", EmbeddingDim: 1024}, captured.IndexConfig.Features["family_name"].Vector)
		assert.Equal(t, &interfaces.BuildTaskFulltextConfig{Analyzer: "ik_max_word"}, captured.IndexConfig.Features["family_name"].Fulltext)
	})
	t.Run("uses feature embedding model override", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := vmock.NewMockCatalogService(ctrl)
		mockRS := vmock.NewMockResourceService(ctrl)
		mockCS.EXPECT().CheckCatalogPermission(gomock.Any(), "catalog-1", []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true).Return(true, nil, nil)
		mockBTA := vmock.NewMockBuildTaskAccess(ctrl)
		mockMFS := vmock.NewMockModelFactoryService(ctrl)
		service := &buildTaskService{cf: buildTaskTestConnectorFactory(t), cs: mockCS, rs: mockRS, bta: mockBTA, mfs: mockMFS}

		mockRS.EXPECT().GetByID(gomock.Any(), "resource-1").
			Return(&interfaces.Resource{
				ID:        "resource-1",
				CatalogID: "catalog-1",
				Category:  interfaces.ResourceCategoryTable,
				IndexConfig: &interfaces.ResourceIndexConfig{
					PrimaryKeyFields:      []string{"id"},
					IncrementalFields:     []string{"id"},
					DefaultEmbeddingModel: "default-model-id",
				},
				SchemaDefinition: []*interfaces.Property{
					{Name: "id", Type: interfaces.DataType_Integer},
					{
						Name: "family_name",
						Features: []interfaces.PropertyFeature{
							{
								FeatureType: interfaces.PropertyFeatureType_Vector,
								RefProperty: "family_name",
								Config:      map[string]any{"embedding_model": "2064382281006583808"},
							},
						},
					},
				},
			}, nil)
		mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
			Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
		mockBTA.EXPECT().InternalList(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, params interfaces.BuildTasksQueryParams) ([]*interfaces.BuildTaskSummary, error) {
				if params.ResourceID != "resource-1" {
					require.Equal(t, "resource-1", params.ResourceID)
				}
				return nil, nil
			})
		mockMFS.EXPECT().GetModelByID(gomock.Any(), "2064382281006583808").
			Return(&interfaces.SmallModel{ModelID: "2064382281006583808", ModelName: "text-embedding-v4", EmbeddingDim: 1024}, nil)

		var captured *interfaces.BuildTask
		mockBTA.EXPECT().Create(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, bt *interfaces.BuildTask) error {
				captured = bt
				return nil
			})

		_, err := service.Create(context.Background(), &interfaces.CreateBuildTaskRequest{
			ResourceID: "resource-1",
			Mode:       interfaces.BuildTaskModeBatch,
		})
		require.NoError(t, err)
		require.NotNil(t, captured)
		require.NotNil(t, captured.IndexConfig)
		assert.Equal(t, &interfaces.SmallModel{ModelID: "2064382281006583808", ModelName: "text-embedding-v4", EmbeddingDim: 1024}, captured.IndexConfig.Features["family_name"].Vector)
	})
	t.Run("keeps per field analyzer and embedding model overrides", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := vmock.NewMockCatalogService(ctrl)
		mockRS := vmock.NewMockResourceService(ctrl)
		mockCS.EXPECT().CheckCatalogPermission(gomock.Any(), "catalog-1", []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true).Return(true, nil, nil)
		mockBTA := vmock.NewMockBuildTaskAccess(ctrl)
		mockMFS := vmock.NewMockModelFactoryService(ctrl)
		service := &buildTaskService{cf: buildTaskTestConnectorFactory(t), cs: mockCS, rs: mockRS, bta: mockBTA, mfs: mockMFS}

		mockRS.EXPECT().GetByID(gomock.Any(), "resource-1").
			Return(&interfaces.Resource{
				ID:        "resource-1",
				CatalogID: "catalog-1",
				Category:  interfaces.ResourceCategoryTable,
				IndexConfig: &interfaces.ResourceIndexConfig{
					PrimaryKeyFields:        []string{"id"},
					IncrementalFields:       []string{"id"},
					DefaultEmbeddingModel:   "default-model",
					DefaultFulltextAnalyzer: "default_analyzer",
				},
				SchemaDefinition: []*interfaces.Property{
					{Name: "id", Type: interfaces.DataType_Integer},
					{
						Name: "title",
						Features: []interfaces.PropertyFeature{
							{
								FeatureType: interfaces.PropertyFeatureType_Vector,
								Config:      map[string]any{"embedding_model": "model-a-id"},
							},
							{
								FeatureType: interfaces.PropertyFeatureType_Fulltext,
								Config:      map[string]any{"analyzer": "ik_max_word"},
							},
						},
					},
					{
						Name: "body",
						Features: []interfaces.PropertyFeature{
							{
								FeatureType: interfaces.PropertyFeatureType_Vector,
								Config:      map[string]any{"embedding_model": "model-b-id"},
							},
							{
								FeatureType: interfaces.PropertyFeatureType_Fulltext,
								Config:      map[string]any{"analyzer": "standard"},
							},
						},
					},
				},
			}, nil)
		mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
			Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
		mockBTA.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return(nil, nil)
		mockMFS.EXPECT().GetModelByID(gomock.Any(), "model-a-id").
			Return(&interfaces.SmallModel{ModelID: "model-a-id", ModelName: "model-a", EmbeddingDim: 768}, nil)
		mockMFS.EXPECT().GetModelByID(gomock.Any(), "model-b-id").
			Return(&interfaces.SmallModel{ModelID: "model-b-id", ModelName: "model-b", EmbeddingDim: 1024}, nil)

		var captured *interfaces.BuildTask
		mockBTA.EXPECT().Create(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, bt *interfaces.BuildTask) error {
				captured = bt
				return nil
			})

		_, err := service.Create(context.Background(), &interfaces.CreateBuildTaskRequest{
			ResourceID: "resource-1",
			Mode:       interfaces.BuildTaskModeBatch,
		})

		require.NoError(t, err)
		require.NotNil(t, captured)
		require.NotNil(t, captured.IndexConfig)
		assert.Equal(t, []string{"id"}, captured.IndexConfig.PrimaryKeyFields)
		assert.Equal(t, &interfaces.SmallModel{ModelID: "model-a-id", ModelName: "model-a", EmbeddingDim: 768}, captured.IndexConfig.Features["title"].Vector)
		assert.Equal(t, &interfaces.SmallModel{ModelID: "model-b-id", ModelName: "model-b", EmbeddingDim: 1024}, captured.IndexConfig.Features["body"].Vector)
		assert.Equal(t, &interfaces.BuildTaskFulltextConfig{Analyzer: "ik_max_word"}, captured.IndexConfig.Features["title"].Fulltext)
		assert.Equal(t, &interfaces.BuildTaskFulltextConfig{Analyzer: "standard"}, captured.IndexConfig.Features["body"].Fulltext)
	})
	t.Run("errors when model unresolvable and no dimensions", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := vmock.NewMockCatalogService(ctrl)
		mockRS := vmock.NewMockResourceService(ctrl)
		mockCS.EXPECT().CheckCatalogPermission(gomock.Any(), "catalog-1", []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true).Return(true, nil, nil)
		mockBTA := vmock.NewMockBuildTaskAccess(ctrl)
		mockMFS := vmock.NewMockModelFactoryService(ctrl)
		service := &buildTaskService{cf: buildTaskTestConnectorFactory(t), cs: mockCS, rs: mockRS, bta: mockBTA, mfs: mockMFS}

		mockRS.EXPECT().GetByID(gomock.Any(), "resource-1").
			Return(&interfaces.Resource{
				ID:        "resource-1",
				CatalogID: "catalog-1",
				Category:  interfaces.ResourceCategoryTable,
				IndexConfig: &interfaces.ResourceIndexConfig{
					PrimaryKeyFields:      []string{"id"},
					IncrementalFields:     []string{"id"},
					DefaultEmbeddingModel: "bogus-model-id",
				},
				SchemaDefinition: []*interfaces.Property{
					{Name: "id", Type: interfaces.DataType_Integer},
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
		mockBTA.EXPECT().InternalList(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, params interfaces.BuildTasksQueryParams) ([]*interfaces.BuildTaskSummary, error) {
				if params.ResourceID != "resource-1" {
					require.Equal(t, "resource-1", params.ResourceID)
				}
				return nil, nil
			})
		mockMFS.EXPECT().GetModelByID(gomock.Any(), "bogus-model-id").
			Return(nil, fmt.Errorf("lookup model: %w", interfaces.ErrModelNotFound))

		_, err := service.Create(context.Background(), &interfaces.CreateBuildTaskRequest{
			ResourceID: "resource-1",
			Mode:       interfaces.BuildTaskModeBatch,
		})
		httpErr := requireHTTPError(t, err, verrors.VegaBackend_BuildTask_InvalidParameter_EmbeddingModel)
		assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
	})
}

func TestValidateKeys(t *testing.T) {
	resource := &interfaces.Resource{
		IndexConfig: &interfaces.ResourceIndexConfig{PrimaryKeyFields: []string{"tenant", "id"}},
		SchemaDefinition: []*interfaces.Property{
			{Name: "tenant", Type: interfaces.DataType_String}, {Name: "id", Type: interfaces.DataType_UnsignedInteger},
		},
	}
	key := map[string]any{"id": json.Number("18446744073709551615"), "tenant": "acme"}
	t.Run("uses only the configured key fields to validate keys", func(t *testing.T) {
		resource := *resource
		resource.SchemaDefinition = append(resource.SchemaDefinition, nil)
		got, err := ValidateKeys(&resource, []map[string]any{key})
		require.NoError(t, err)
		assert.Equal(t, []map[string]any{{"id": ^uint64(0), "tenant": "acme"}}, got)
	})
	t.Run("rejects overflow without exposing the raw key", func(t *testing.T) {
		const overflow = "18446744073709551616"
		got, err := ValidateKeys(resource, []map[string]any{{"tenant": "acme", "id": json.Number(overflow)}})
		require.ErrorContains(t, err, `primary key field "id"`)
		assert.NotContains(t, err.Error(), overflow)
		assert.Nil(t, got)
	})
	t.Run("deduplicates normalized tuples and preserves distinct tenants", func(t *testing.T) {
		sameKey := map[string]any{"tenant": "acme", "id": ^uint64(0)}
		otherKey := map[string]any{"tenant": "other", "id": ^uint64(0)}
		got, err := ValidateKeys(resource, []map[string]any{key, sameKey, otherKey})
		require.NoError(t, err)
		assert.Equal(t, []map[string]any{sameKey, otherKey}, got)
		assert.Equal(t, json.Number("18446744073709551615"), key["id"])
	})
	for _, test := range []struct {
		name string
		keys []map[string]any
	}{
		{name: "no keys"},
		{name: "too many raw entries", keys: make([]map[string]any, interfaces.MaxBuildTaskKeys+1)},
		{name: "partial key", keys: []map[string]any{{"id": json.Number("1")}}},
		{name: "unexpected field", keys: []map[string]any{{"id": json.Number("1"), "user": "acme"}}},
		{name: "empty string", keys: []map[string]any{{"id": json.Number("1"), "tenant": ""}}},
		{name: "oversized string", keys: []map[string]any{{"id": json.Number("1"), "tenant": strings.Repeat("a", maxBuildTaskKeyValueBytes+1)}}},
		{name: "negative unsigned integer", keys: []map[string]any{{"id": json.Number("-1"), "tenant": "acme"}}},
		{name: "floating key", keys: []map[string]any{{"id": json.Number("1.5"), "tenant": "acme"}}},
		{name: "null key", keys: []map[string]any{{"id": nil, "tenant": "acme"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := ValidateKeys(resource, test.keys)
			require.Error(t, err)
			assert.Nil(t, got)
		})
	}
}

func TestBuildTaskServiceCreateKeys(t *testing.T) {
	for _, test := range []struct {
		name, executeType, localStatus, code string
		keys                                 []map[string]any
		status                               int
	}{
		{name: "persists keys without requiring cursor or reserving an old index", executeType: "keys",
			localStatus: interfaces.ResourceLocalIndexStatusAvailable, keys: []map[string]any{{"id": json.Number("27")}}},
		{name: "keys required", executeType: "keys", localStatus: interfaces.ResourceLocalIndexStatusAvailable,
			status: http.StatusBadRequest, code: verrors.VegaBackend_BuildTask_InvalidKeys},
		{name: "readonly index is rejected", executeType: "keys", localStatus: interfaces.ResourceLocalIndexStatusReadOnly,
			keys: []map[string]any{{"id": json.Number("27")}}, status: http.StatusConflict, code: verrors.VegaBackend_BuildTask_KeysIndexUnavailable},
		{name: "other builds reject keys", executeType: "full",
			keys: []map[string]any{{"id": json.Number("27")}}, status: http.StatusBadRequest, code: verrors.VegaBackend_BuildTask_InvalidKeys},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			access, resources, catalogs := vmock.NewMockBuildTaskAccess(ctrl), vmock.NewMockResourceService(ctrl), vmock.NewMockCatalogService(ctrl)
			service := &buildTaskService{bta: access, rs: resources, cs: catalogs,
				cf: buildTaskTestConnectorFactory(t), lim: vmock.NewMockLocalIndexManager(ctrl), dispatchCh: make(chan struct{}, 1)}
			resource := buildTaskTestResource()
			resource.IndexConfig.IncrementalFields = nil
			resource.LocalIndexStatus = test.localStatus
			resource.LocalIndexName = "vega-build-published"
			resources.EXPECT().GetByID(gomock.Any(), resource.ID).Return(resource, nil)
			catalogs.EXPECT().CheckCatalogPermission(gomock.Any(), resource.CatalogID, []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true).
				Return(true, nil, nil)
			if test.status == 0 {
				catalogs.EXPECT().GetByID(gomock.Any(), resource.CatalogID, false).
					Return(&interfaces.Catalog{ID: resource.CatalogID, Enabled: true}, nil)
				access.EXPECT().Create(gomock.Any(), gomock.Cond(func(task *interfaces.BuildTask) bool {
					return task != nil && task.Status == interfaces.BuildTaskStatusPending &&
						task.IndexName == "" && task.SyncedMark == "" && task.TotalCount == 1 &&
						len(task.Keys) == 1 && task.Keys[0]["id"] == int64(27)
				})).Return(nil)
			}
			id, err := service.Create(t.Context(), &interfaces.CreateBuildTaskRequest{
				ResourceID: resource.ID, Mode: interfaces.BuildTaskModeBatch, ExecuteType: test.executeType, Keys: test.keys,
			})
			if test.status != 0 {
				assert.Equal(t, test.status, requireHTTPError(t, err, test.code).HTTPCode)
				assert.Empty(t, id)
				assert.Empty(t, service.dispatchCh)
				return
			}
			require.NoError(t, err)
			assert.NotEmpty(t, id)
			assert.Len(t, service.dispatchCh, 1)
		})
	}
}

func TestCreateIncrementalBuildWithPublishedIndex(t *testing.T) {
	for _, tc := range []struct {
		name        string
		mark        string
		indexStatus string
		scheduled   bool
		wantHTTP    int
	}{
		{name: "rejects a missing checkpoint before scheduling", indexStatus: interfaces.ResourceLocalIndexStatusAvailable, wantHTTP: http.StatusConflict},
		{name: "rejects a missing checkpoint for a scheduled build", indexStatus: interfaces.ResourceLocalIndexStatusAvailable, scheduled: true, wantHTTP: http.StatusConflict},
		{name: "uses a committed baseline", indexStatus: interfaces.ResourceLocalIndexStatusAvailable, mark: `{"mode":"batch","cursor":[]}`},
		{name: "rejects an invalid committed baseline", indexStatus: interfaces.ResourceLocalIndexStatusAvailable, mark: "invalid", wantHTTP: http.StatusConflict},
		{name: "rejects an unavailable index", wantHTTP: http.StatusConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			access := vmock.NewMockBuildTaskAccess(ctrl)
			resources := vmock.NewMockResourceService(ctrl)
			catalogs := vmock.NewMockCatalogService(ctrl)
			service := &buildTaskService{
				bta: access, rs: resources, cs: catalogs, lim: vmock.NewMockLocalIndexManager(ctrl),
				cf: buildTaskTestConnectorFactory(t), dispatchCh: make(chan struct{}, 1),
			}
			resource := buildTaskTestResource()
			resource.LocalIndexName = "vega-build-published"
			resource.LocalIndexStatus = tc.indexStatus
			resource.SyncMark = tc.mark
			resources.EXPECT().GetByID(gomock.Any(), resource.ID).Return(resource, nil)
			catalogs.EXPECT().CheckCatalogPermission(gomock.Any(), resource.CatalogID, []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true).
				Return(true, nil, nil)

			var queued *interfaces.BuildTask
			if tc.wantHTTP == 0 {
				catalogs.EXPECT().GetByID(gomock.Any(), resource.CatalogID, false).
					Return(&interfaces.Catalog{ID: resource.CatalogID, Enabled: true}, nil)
				access.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return(nil, nil)
				access.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ context.Context, task *interfaces.BuildTask) error {
						queued = task
						return nil
					})
			}

			req := &interfaces.CreateBuildTaskRequest{
				ResourceID: resource.ID, Mode: interfaces.BuildTaskModeBatch, ExecuteType: interfaces.BuildTaskExecuteTypeIncremental,
			}
			if tc.scheduled {
				req.ScheduleID = "schedule-1"
				req.TriggerType = interfaces.BuildTaskTriggerScheduled
			}
			id, err := service.Create(t.Context(), req)
			if tc.wantHTTP != 0 {
				failure := requireHTTPError(t, err, verrors.VegaBackend_BuildTask_IncrementalBaselineUnavailable)
				assert.Equal(t, tc.wantHTTP, failure.HTTPCode)
				assert.Empty(t, service.dispatchCh)
				assert.Empty(t, id)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, queued)
			assert.NotEmpty(t, id)
			assert.Equal(t, id, queued.ID)
			assert.Equal(t, interfaces.BuildTaskStatusPending, queued.Status)
			assert.Equal(t, interfaces.BuildTaskExecuteTypeIncremental, queued.ExecuteType)
			assert.Equal(t, resource.LocalIndexName, queued.IndexName)
			assert.Empty(t, queued.SyncedMark)
			if tc.scheduled {
				assert.Equal(t, req.ScheduleID, queued.ScheduleID)
				assert.Equal(t, req.TriggerType, queued.TriggerType)
			}
			assert.Len(t, service.dispatchCh, 1)
		})
	}
}

func TestNormalizeCreateBuildTaskExecuteType(t *testing.T) {
	t.Run("defaults to full", func(t *testing.T) {
		executeType, err := normalizeCreateBuildTaskExecuteType(context.Background(), &interfaces.CreateBuildTaskRequest{
			Mode: interfaces.BuildTaskModeBatch,
		})

		require.NoError(t, err)
		require.Equal(t, interfaces.BuildTaskExecuteTypeFull, executeType)
	})
	t.Run("returns empty for streaming", func(t *testing.T) {
		executeType, err := normalizeCreateBuildTaskExecuteType(context.Background(), &interfaces.CreateBuildTaskRequest{
			Mode: interfaces.BuildTaskModeStreaming,
		})

		require.NoError(t, err)
		require.Empty(t, executeType)
	})
}

func TestValidateIncrementalAdmission(t *testing.T) {
	validMark := `{"mode":"batch","cursor":[]}`
	tests := []struct {
		name     string
		resource *interfaces.Resource
		wantErr  bool
	}{
		{name: "accepts established empty cursor", resource: func() *interfaces.Resource {
			resource := buildTaskTestResource()
			resource.LocalIndexStatus = interfaces.ResourceLocalIndexStatusAvailable
			resource.LocalIndexName = "resource-1-index"
			resource.SyncMark = validMark
			return resource
		}()},
		{name: "accepts checkpoint with appended primary key", resource: func() *interfaces.Resource {
			resource := buildTaskTestResource()
			resource.IndexConfig.IncrementalFields = []string{"updated_at"}
			resource.SchemaDefinition = append(resource.SchemaDefinition, &interfaces.Property{Name: "updated_at", Type: interfaces.DataType_Timestamp})
			resource.LocalIndexStatus = interfaces.ResourceLocalIndexStatusAvailable
			resource.LocalIndexName = "resource-1-index"
			resource.SyncMark = `{"mode":"batch","cursor":[{"key":"updated_at","value":"2026-09-08T10:00:00Z"},{"key":"id","value":1}]}`
			return resource
		}()},
		{name: "rejects unavailable index", resource: func() *interfaces.Resource {
			resource := buildTaskTestResource()
			resource.SyncMark = validMark
			return resource
		}(), wantErr: true},
		{name: "rejects a published index with no checkpoint", resource: func() *interfaces.Resource {
			resource := buildTaskTestResource()
			resource.LocalIndexStatus = interfaces.ResourceLocalIndexStatusAvailable
			resource.LocalIndexName = "resource-1-index"
			return resource
		}(), wantErr: true},
		{name: "rejects recovery on a read-only published index", resource: func() *interfaces.Resource {
			resource := buildTaskTestResource()
			resource.LocalIndexStatus = interfaces.ResourceLocalIndexStatusReadOnly
			resource.LocalIndexName = "resource-1-index"
			return resource
		}(), wantErr: true},
		{name: "rejects cursor incompatible with build keys", resource: func() *interfaces.Resource {
			resource := buildTaskTestResource()
			resource.LocalIndexStatus = interfaces.ResourceLocalIndexStatusAvailable
			resource.LocalIndexName = "resource-1-index"
			resource.SyncMark = `{"mode":"batch","cursor":[{"key":"other_id","value":1}]}`
			return resource
		}(), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateIncrementalAdmission(context.Background(), tt.resource)
			if !tt.wantErr {
				require.NoError(t, err)
				return
			}
			httpErr := requireHTTPError(t, err, verrors.VegaBackend_BuildTask_IncrementalBaselineUnavailable)
			assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
			if tt.resource.LocalIndexStatus == interfaces.ResourceLocalIndexStatusReadOnly {
				assert.Contains(t, httpErr.ErrorDetails, "run a full build")
			}
		})
	}
}

func TestBuildTaskServiceStart(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusNotFound, http.StatusInternalServerError} {
		t.Run(fmt.Sprintf("resource lookup error %d before status update", status), func(t *testing.T) {
			ctrl := gomock.NewController(t)
			cs := vmock.NewMockCatalogService(ctrl)
			rs := vmock.NewMockResourceService(ctrl)
			bta := vmock.NewMockBuildTaskAccess(ctrl)
			service := &buildTaskService{cs: cs, rs: rs, bta: bta, dispatchCh: make(chan struct{}, 1)}
			task := &interfaces.BuildTask{ID: "task-1", ResourceID: "resource-1", CatalogID: "catalog-1", Status: interfaces.BuildTaskStatusStopped, Mode: interfaces.BuildTaskModeBatch, IndexName: "test-index"}
			bta.EXPECT().GetByID(gomock.Any(), task.ID).Return(task, nil)
			cs.EXPECT().CheckCatalogPermission(gomock.Any(), task.CatalogID, []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true).Return(true, &interfaces.Catalog{ID: task.CatalogID, Enabled: true}, nil)
			bta.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return(nil, nil)
			code := httperrors.PublicError_Forbidden
			if status == http.StatusNotFound {
				code = verrors.VegaBackend_Resource_NotFound
			}
			cause := httperrors.NewHTTPError(context.Background(), status, code)
			downstream := fmt.Errorf("lookup: %w", cause)
			if status == http.StatusInternalServerError {
				downstream = errors.New("lookup failed")
			}
			rs.EXPECT().GetByID(gomock.Any(), task.ResourceID).Return(nil, downstream)
			err := service.Start(context.Background(), task.ID, false)
			if status == http.StatusInternalServerError {
				httpErr := requireHTTPError(t, err, verrors.VegaBackend_Resource_InternalError_GetFailed)
				assert.Equal(t, status, httpErr.HTTPCode)
			} else {
				assert.Same(t, cause, err)
			}
			assert.Empty(t, service.dispatchCh)
		})
	}

	for _, changed := range []bool{false, true} {
		t.Run(fmt.Sprintf("start uses fingerprint changed=%v", changed), func(t *testing.T) {
			ctrl := gomock.NewController(t)
			cs := vmock.NewMockCatalogService(ctrl)
			rs := vmock.NewMockResourceService(ctrl)
			bta := vmock.NewMockBuildTaskAccess(ctrl)
			service := &buildTaskService{cs: cs, rs: rs, bta: bta, cf: vmock.NewMockConnectorFactory(ctrl), dispatchCh: make(chan struct{}, 1)}
			resource := buildTaskTestResource()
			resource.SchemaDefinition[0].Type = interfaces.DataType_String
			resource.SchemaDefinition[0].OriginalType = "VARCHAR"
			resourcelogic.AddDefaultStringAndTextFeatures(resource.SchemaDefinition, resource.IndexConfig)
			task := &interfaces.BuildTask{ID: "task-1", ResourceID: resource.ID, CatalogID: resource.CatalogID, Status: interfaces.BuildTaskStatusStopped, Mode: interfaces.BuildTaskModeBatch, ExecuteType: interfaces.BuildTaskExecuteTypeFull, IndexName: "test-index", IndexConfig: mustBuildTaskIndexConfig(t, resource)}
			if changed {
				resource.SchemaDefinition[0].OriginalType = "LARGEINT"
			}
			catalog := &interfaces.Catalog{ID: resource.CatalogID, Enabled: true, ConnectorType: "doris"}
			cs.EXPECT().CheckCatalogPermission(gomock.Any(), resource.CatalogID, []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true).Return(true, catalog, nil)
			rs.EXPECT().GetByID(gomock.Any(), resource.ID).Return(resource, nil)
			bta.EXPECT().GetByID(gomock.Any(), task.ID).Return(task, nil)
			bta.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return(nil, nil)
			if !changed {
				bta.EXPECT().MarkPending(gomock.Any(), nil, task.ID, false).Return(true, nil)
			}
			err := service.Start(context.Background(), task.ID, false)
			if changed {
				httpErr := requireHTTPError(t, err, verrors.VegaBackend_BuildTask_IndexConfigChanged)
				assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
				assert.Empty(t, service.dispatchCh)
			} else {
				require.NoError(t, err)
				assert.Len(t, service.dispatchCh, 1)
			}
		})
	}

	t.Run("rejects a historical task without an index name", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := vmock.NewMockCatalogService(ctrl)
		mockBTA := vmock.NewMockBuildTaskAccess(ctrl)
		service := &buildTaskService{cs: mockCS, bta: mockBTA}
		mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").Return(&interfaces.BuildTask{
			ID: "task-1", CatalogID: "catalog-1", Status: interfaces.BuildTaskStatusFailed,
			ExecuteType: interfaces.BuildTaskExecuteTypeFull,
		}, nil)
		mockCS.EXPECT().CheckCatalogPermission(gomock.Any(), "catalog-1", []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true).Return(true, nil, nil)

		err := service.Start(context.Background(), "task-1", false)

		httpErr := requireHTTPError(t, err, verrors.VegaBackend_BuildTask_IndexConfigChanged)
		assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
		assert.Contains(t, httpErr.ErrorDetails, "create a new build task")
	})

	t.Run("rejects a legacy string or text schema until the resource is re-saved", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := vmock.NewMockCatalogService(ctrl)
		mockRS := vmock.NewMockResourceService(ctrl)
		mockBTA := vmock.NewMockBuildTaskAccess(ctrl)
		service := &buildTaskService{cs: mockCS, rs: mockRS, bta: mockBTA}
		resource := buildTaskTestResource()
		resource.SchemaDefinition = append(resource.SchemaDefinition, &interfaces.Property{
			Name: "description", Type: interfaces.DataType_Text,
		})
		task := &interfaces.BuildTask{
			ID: "task-1", ResourceID: resource.ID, CatalogID: resource.CatalogID,
			Status: interfaces.BuildTaskStatusStopped, ExecuteType: interfaces.BuildTaskExecuteTypeFull,
			IndexName: "vega-build-test-index", IndexConfig: mustBuildTaskIndexConfig(t, resource),
		}

		mockBTA.EXPECT().GetByID(gomock.Any(), task.ID).Return(task, nil)
		mockCS.EXPECT().CheckCatalogPermission(gomock.Any(), task.CatalogID, []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true).
			Return(true, &interfaces.Catalog{ID: task.CatalogID, Enabled: true}, nil)
		mockBTA.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return(nil, nil)
		mockRS.EXPECT().GetByID(gomock.Any(), resource.ID).Return(resource, nil)

		err := service.Start(context.Background(), task.ID, false)

		httpErr := requireHTTPError(t, err, verrors.VegaBackend_BuildTask_InvalidParameter_UnsupportedSchemaFields)
		assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
		assert.Contains(t, httpErr.ErrorDetails, "re-save the resource configuration")
	})

	for _, reset := range []bool{false, true} {
		t.Run(fmt.Sprintf("primary key only full restart reset=%t", reset), func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockCS := vmock.NewMockCatalogService(ctrl)
			mockRS := vmock.NewMockResourceService(ctrl)
			mockCS.EXPECT().CheckCatalogPermission(gomock.Any(), "catalog-1", []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true).
				Return(true, &interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
			mockBTA := vmock.NewMockBuildTaskAccess(ctrl)
			service := &buildTaskService{cf: buildTaskTestConnectorFactory(t),
				cs:         mockCS,
				rs:         mockRS,
				bta:        mockBTA,
				dispatchCh: make(chan struct{}, buildTaskDispatchBuffer),
			}
			resource := buildTaskTestResource()
			resource.IndexConfig.IncrementalFields = nil
			task := &interfaces.BuildTask{
				ID: "task-1", ResourceID: "resource-1", CatalogID: "catalog-1",
				Status: interfaces.BuildTaskStatusFailed, ExecuteType: interfaces.BuildTaskExecuteTypeFull,
				IndexName:   "vega-build-test-index",
				IndexConfig: mustBuildTaskIndexConfig(t, resource),
			}
			mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").Return(task, nil)
			mockBTA.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return(nil, nil)
			mockRS.EXPECT().GetByID(gomock.Any(), "resource-1").Return(resource, nil)
			mockBTA.EXPECT().MarkPending(gomock.Any(), nil, "task-1", reset).Return(true, nil)

			require.NoError(t, service.Start(context.Background(), "task-1", reset))
			select {
			case <-service.DispatchSignal():
			default:
				t.Fatal("expected a dispatch signal after the restart request was persisted")
			}
		})
	}
	t.Run("restarting incremental after a primary key change requires a full build", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		cs := vmock.NewMockCatalogService(ctrl)
		rs := vmock.NewMockResourceService(ctrl)
		bta := vmock.NewMockBuildTaskAccess(ctrl)
		service := &buildTaskService{cs: cs, rs: rs, bta: bta}
		resource := buildTaskTestResource()
		resource.LocalIndexName = "published-index"
		task := &interfaces.BuildTask{
			ID: "task-1", ResourceID: resource.ID, CatalogID: resource.CatalogID, Status: interfaces.BuildTaskStatusStopped,
			ExecuteType: interfaces.BuildTaskExecuteTypeIncremental, IndexName: resource.LocalIndexName,
			IndexConfig: mustBuildTaskIndexConfig(t, resource),
		}
		resource.SchemaDefinition = append(resource.SchemaDefinition, &interfaces.Property{
			Name: "new_id", OriginalName: "new_id", OriginalType: "int", Type: interfaces.DataType_Integer,
		})
		resource.IndexConfig.PrimaryKeyFields = []string{"new_id"}
		resource.LocalIndexStatus = interfaces.ResourceLocalIndexStatusReadOnly

		bta.EXPECT().GetByID(gomock.Any(), task.ID).Return(task, nil)
		cs.EXPECT().CheckCatalogPermission(gomock.Any(), task.CatalogID, []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true).
			Return(true, &interfaces.Catalog{ID: task.CatalogID, Enabled: true}, nil)
		bta.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return(nil, nil)
		rs.EXPECT().GetByID(gomock.Any(), resource.ID).Return(resource, nil)

		err := service.Start(t.Context(), task.ID, false)
		httpErr := requireHTTPError(t, err, verrors.VegaBackend_BuildTask_IncrementalBaselineUnavailable)
		assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
		assert.Contains(t, httpErr.ErrorDetails, "run a full build")
	})
	t.Run("rejects reset for incremental task", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := vmock.NewMockCatalogService(ctrl)
		mockBTA := vmock.NewMockBuildTaskAccess(ctrl)
		mockCS.EXPECT().CheckCatalogPermission(gomock.Any(), "catalog-1", []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true).
			Return(true, &interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
		service := &buildTaskService{cs: mockCS, bta: mockBTA}

		mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").Return(&interfaces.BuildTask{
			ID:          "task-1",
			CatalogID:   "catalog-1",
			Status:      interfaces.BuildTaskStatusStopped,
			ExecuteType: interfaces.BuildTaskExecuteTypeIncremental,
			IndexName:   "vega-build-test-index",
		}, nil)

		err := service.Start(context.Background(), "task-1", true)
		httpErr := requireHTTPError(t, err, verrors.VegaBackend_BuildTask_IncrementalResetUnsupported)
		assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
	})
	for _, tc := range []struct {
		name         string
		fields       []string
		mark         string
		indexChanged bool
		code         string
		status       int
	}{
		{
			name:   "rejects incremental restart when the baseline is missing",
			fields: []string{"id"},
			code:   verrors.VegaBackend_BuildTask_IncrementalBaselineUnavailable,
			status: http.StatusConflict,
		},
		{
			name:   "rejects incremental restart with an invalid committed baseline",
			fields: []string{"id"},
			mark:   "invalid",
			code:   verrors.VegaBackend_BuildTask_IncrementalBaselineUnavailable,
			status: http.StatusConflict,
		},
		{
			name:         "rejects incremental restart against a superseded index",
			fields:       []string{"id"},
			indexChanged: true,
			mark:         `{"mode":"batch","cursor":[]}`,
			code:         verrors.VegaBackend_BuildTask_IndexConfigChanged,
			status:       http.StatusConflict,
		},
		{
			name:   "incremental restart with baseline but no incremental fields",
			mark:   `{"mode":"batch","cursor":[{"key":"id","value":20}]}`,
			code:   verrors.VegaBackend_BuildTask_InvalidParameter_IncrementalFields,
			status: http.StatusBadRequest,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockCS := vmock.NewMockCatalogService(ctrl)
			mockRS := vmock.NewMockResourceService(ctrl)
			mockBTA := vmock.NewMockBuildTaskAccess(ctrl)
			mockCS.EXPECT().CheckCatalogPermission(gomock.Any(), "catalog-1", []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true).
				Return(true, &interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
			service := &buildTaskService{cs: mockCS, rs: mockRS, bta: mockBTA, lim: vmock.NewMockLocalIndexManager(ctrl), dispatchCh: make(chan struct{}, 1)}
			resource := buildTaskTestResource()
			resource.IndexConfig.IncrementalFields = tc.fields
			resource.LocalIndexStatus = interfaces.ResourceLocalIndexStatusAvailable
			resource.LocalIndexName = "current-index"
			resource.SyncMark = tc.mark
			taskIndexName := resource.LocalIndexName
			if tc.indexChanged {
				taskIndexName = "previous-index"
			}

			mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").Return(&interfaces.BuildTask{
				ID:          "task-1",
				ResourceID:  "resource-1",
				CatalogID:   "catalog-1",
				Status:      interfaces.BuildTaskStatusStopped,
				ExecuteType: interfaces.BuildTaskExecuteTypeIncremental,
				IndexName:   taskIndexName,
				IndexConfig: mustBuildTaskIndexConfig(t, resource),
			}, nil)
			mockBTA.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return(nil, nil)
			mockRS.EXPECT().GetByID(gomock.Any(), "resource-1").Return(resource, nil)

			if tc.status == 0 {
				mockBTA.EXPECT().MarkPending(gomock.Any(), nil, "task-1", false).Return(true, nil)
			}
			err := service.Start(context.Background(), "task-1", false)
			if tc.status == 0 {
				require.NoError(t, err)
				assert.Len(t, service.dispatchCh, 1)
				return
			}
			assert.Equal(t, tc.status, requireHTTPError(t, err, tc.code).HTTPCode)
			assert.Empty(t, service.dispatchCh)
		})
	}

	t.Run("returns conflict when status changes before start update", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := vmock.NewMockCatalogService(ctrl)
		mockRS := vmock.NewMockResourceService(ctrl)
		mockCS.EXPECT().CheckCatalogPermission(gomock.Any(), "catalog-1", []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true).
			Return(true, &interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
		mockBTA := vmock.NewMockBuildTaskAccess(ctrl)
		service := &buildTaskService{cs: mockCS, rs: mockRS, bta: mockBTA}

		resource := buildTaskTestResource()
		task := &interfaces.BuildTask{
			ID: "task-1", ResourceID: "resource-1", CatalogID: "catalog-1",
			Status:      interfaces.BuildTaskStatusStopped,
			IndexName:   "vega-build-test-index",
			IndexConfig: mustBuildTaskIndexConfig(t, resource),
		}
		mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").Return(task, nil)
		mockBTA.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return(nil, nil)
		mockRS.EXPECT().GetByID(gomock.Any(), "resource-1").Return(resource, nil)
		mockBTA.EXPECT().MarkPending(gomock.Any(), nil, "task-1", false).Return(false, nil)

		err := service.Start(context.Background(), "task-1", false)
		httpErr := requireHTTPError(t, err, verrors.VegaBackend_BuildTask_InvalidStateTransition)
		assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
	})

	t.Run("rejects disabled catalog", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := vmock.NewMockCatalogService(ctrl)
		mockBTA := vmock.NewMockBuildTaskAccess(ctrl)
		mockRS := vmock.NewMockResourceService(ctrl)
		mockCS.EXPECT().CheckCatalogPermission(gomock.Any(), "catalog-1", []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true).
			Return(true, &interfaces.Catalog{ID: "catalog-1", Enabled: false}, nil)
		service := &buildTaskService{cs: mockCS, bta: mockBTA, rs: mockRS}

		mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").
			Return(&interfaces.BuildTask{
				ID:        "task-1",
				CatalogID: "catalog-1",
				Status:    interfaces.BuildTaskStatusStopped,
				IndexName: "vega-build-test-index",
			}, nil)

		err := service.Start(context.Background(), "task-1", false)
		assertCatalogDisabledError(t, err)
	})
	t.Run("allows failed status", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := vmock.NewMockCatalogService(ctrl)
		mockBTA := vmock.NewMockBuildTaskAccess(ctrl)
		mockRS := vmock.NewMockResourceService(ctrl)
		mockCS.EXPECT().CheckCatalogPermission(gomock.Any(), "catalog-1", []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true).
			Return(true, &interfaces.Catalog{ID: "catalog-1", Enabled: false}, nil)
		service := &buildTaskService{cs: mockCS, bta: mockBTA, rs: mockRS}

		mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").
			Return(&interfaces.BuildTask{
				ID:        "task-1",
				CatalogID: "catalog-1",
				Status:    interfaces.BuildTaskStatusFailed,
				IndexName: "vega-build-test-index",
			}, nil)

		err := service.Start(context.Background(), "task-1", false)
		assertCatalogDisabledError(t, err)
	})
	for _, status := range []string{interfaces.BuildTaskStatusPending, interfaces.BuildTaskStatusCompleted} {
		t.Run("rejects "+status+" status", func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockBTA := vmock.NewMockBuildTaskAccess(ctrl)
			mockRSAuth := vmock.NewMockResourceService(ctrl)
			mockCSAuth := vmock.NewMockCatalogService(ctrl)
			mockCSAuth.EXPECT().CheckCatalogPermission(gomock.Any(), "catalog-1", []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true).Return(true, nil, nil)
			service := &buildTaskService{bta: mockBTA, rs: mockRSAuth, cs: mockCSAuth}

			mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").
				Return(&interfaces.BuildTask{ID: "task-1", CatalogID: "catalog-1", Status: status}, nil)

			err := service.Start(context.Background(), "task-1", false)
			_ = requireHTTPError(t, err, verrors.VegaBackend_BuildTask_InvalidStateTransition)
		})
	}
	t.Run("rejects another active task for resource", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := vmock.NewMockCatalogService(ctrl)
		mockBTA := vmock.NewMockBuildTaskAccess(ctrl)
		mockRS := vmock.NewMockResourceService(ctrl)
		mockCS.EXPECT().CheckCatalogPermission(gomock.Any(), "catalog-1", []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true).
			Return(true, &interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
		service := &buildTaskService{cs: mockCS, bta: mockBTA, rs: mockRS}

		mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").
			Return(&interfaces.BuildTask{
				ID:         "task-1",
				ResourceID: "resource-1",
				CatalogID:  "catalog-1",
				Status:     interfaces.BuildTaskStatusStopped,
				IndexName:  "vega-build-test-index",
			}, nil)
		mockBTA.EXPECT().InternalList(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, params interfaces.BuildTasksQueryParams) ([]*interfaces.BuildTaskSummary, error) {
				if params.ResourceID != "resource-1" {
					require.Equal(t, "resource-1", params.ResourceID)
				}
				return []*interfaces.BuildTaskSummary{{
					ID:         "task-2",
					ResourceID: "resource-1",
					Status:     interfaces.BuildTaskStatusRunning,
				}}, nil
			})

		err := service.Start(context.Background(), "task-1", false)
		_ = requireHTTPError(t, err, verrors.VegaBackend_BuildTask_Exist)
	})
	t.Run("rejects changed index config", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := vmock.NewMockCatalogService(ctrl)
		mockRS := vmock.NewMockResourceService(ctrl)
		mockCS.EXPECT().CheckCatalogPermission(gomock.Any(), "catalog-1", []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true).
			Return(true, &interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
		mockBTA := vmock.NewMockBuildTaskAccess(ctrl)
		service := &buildTaskService{cs: mockCS, rs: mockRS, bta: mockBTA}
		originalResource := buildTaskTestResource()
		currentResource := &interfaces.Resource{
			ID:          "resource-1",
			CatalogID:   "catalog-1",
			IndexConfig: &interfaces.ResourceIndexConfig{PrimaryKeyFields: []string{"id"}, IncrementalFields: []string{"updated_at", "id"}},
			SchemaDefinition: []*interfaces.Property{
				{Name: "id", Type: interfaces.DataType_Integer},
				{Name: "updated_at", Type: interfaces.DataType_Timestamp},
			},
		}

		mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").Return(&interfaces.BuildTask{
			ID:          "task-1",
			ResourceID:  "resource-1",
			CatalogID:   "catalog-1",
			Status:      interfaces.BuildTaskStatusStopped,
			IndexName:   "vega-build-test-index",
			IndexConfig: mustBuildTaskIndexConfig(t, originalResource),
		}, nil)
		mockBTA.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return(nil, nil)
		mockRS.EXPECT().GetByID(gomock.Any(), "resource-1").Return(currentResource, nil)

		err := service.Start(context.Background(), "task-1", false)
		_ = requireHTTPError(t, err, verrors.VegaBackend_BuildTask_IndexConfigChanged)
	})
	t.Run("restart does not depend on newer completed tasks", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := vmock.NewMockCatalogService(ctrl)
		mockRS := vmock.NewMockResourceService(ctrl)
		mockCS.EXPECT().CheckCatalogPermission(gomock.Any(), "catalog-1", []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true).
			Return(true, &interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
		mockBTA := vmock.NewMockBuildTaskAccess(ctrl)
		service := &buildTaskService{cs: mockCS, rs: mockRS, bta: mockBTA}
		resource := buildTaskTestResource()

		task := &interfaces.BuildTask{
			ID:          "task-1",
			ResourceID:  "resource-1",
			CatalogID:   "catalog-1",
			Status:      interfaces.BuildTaskStatusStopped,
			CreateTime:  100,
			IndexName:   "vega-build-test-index",
			IndexConfig: mustBuildTaskIndexConfig(t, resource),
		}
		mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").Return(task, nil)
		mockBTA.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return(nil, nil)
		mockRS.EXPECT().GetByID(gomock.Any(), "resource-1").Return(resource, nil)
		mockBTA.EXPECT().MarkPending(gomock.Any(), nil, "task-1", false).Return(true, nil)

		require.NoError(t, service.Start(context.Background(), "task-1", false))
	})
	t.Run("allows excluded fields outside build keys", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := vmock.NewMockCatalogService(ctrl)
		mockRS := vmock.NewMockResourceService(ctrl)
		mockBTA := vmock.NewMockBuildTaskAccess(ctrl)
		service := &buildTaskService{cs: mockCS, rs: mockRS, bta: mockBTA}
		resource := buildTaskTestResource()
		resource.SchemaDefinition = append(resource.SchemaDefinition,
			&interfaces.Property{Name: "attachment", Type: interfaces.DataType_Binary, OriginalType: "bytea"},
			&interfaces.Property{Name: "interests", Type: interfaces.DataType_Other, OriginalType: "_text"},
		)
		task := &interfaces.BuildTask{
			ID:          "task-1",
			ResourceID:  "resource-1",
			CatalogID:   "catalog-1",
			Status:      interfaces.BuildTaskStatusStopped,
			IndexName:   "vega-build-test-index",
			IndexConfig: mustBuildTaskIndexConfig(t, resource),
		}

		mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").Return(task, nil)
		mockCS.EXPECT().CheckCatalogPermission(gomock.Any(), "catalog-1", []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true).
			Return(true, &interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
		mockBTA.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return(nil, nil)
		mockRS.EXPECT().GetByID(gomock.Any(), "resource-1").Return(resource, nil)
		mockBTA.EXPECT().MarkPending(gomock.Any(), nil, "task-1", false).Return(true, nil)

		require.NoError(t, service.Start(context.Background(), "task-1", false))
	})
	t.Run("rejects unavailable analyzer before updating status or dispatching", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := vmock.NewMockCatalogService(ctrl)
		mockRS := vmock.NewMockResourceService(ctrl)
		mockCS.EXPECT().CheckCatalogPermission(gomock.Any(), "catalog-1", []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true).
			Return(true, &interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
		mockBTA := vmock.NewMockBuildTaskAccess(ctrl)
		validator := &analyzerValidatingIndexManager{}
		service := &buildTaskService{cf: buildTaskTestConnectorFactory(t),
			bta: mockBTA,
			cs:  mockCS,
			lim: validator,
			rs:  mockRS,
		}
		resource := &interfaces.Resource{
			ID:          "resource-1",
			CatalogID:   "catalog-1",
			IndexConfig: &interfaces.ResourceIndexConfig{PrimaryKeyFields: []string{"id"}, IncrementalFields: []string{"id"}},
			SchemaDefinition: []*interfaces.Property{
				{Name: "id", Type: interfaces.DataType_Integer},
				{
					Name: "status",
					Features: []interfaces.PropertyFeature{{
						FeatureType: interfaces.PropertyFeatureType_Fulltext,
						Config:      map[string]any{"analyzer": "hanlp_index"},
					}},
				},
			},
		}
		task := &interfaces.BuildTask{
			ID:         "task-1",
			ResourceID: "resource-1",
			CatalogID:  "catalog-1",
			Status:     interfaces.BuildTaskStatusStopped,
			IndexName:  "vega-build-test-index",
			IndexConfig: func() *interfaces.BuildTaskIndexConfig {
				config := mustBuildTaskIndexConfig(t, resource)
				config.Features = map[string]interfaces.BuildTaskFieldIndexFeature{
					"status": {Fulltext: &interfaces.BuildTaskFulltextConfig{Analyzer: "hanlp_index"}},
				}
				return config
			}(),
		}
		mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").Return(task, nil)
		mockBTA.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return(nil, nil)
		mockRS.EXPECT().GetByID(gomock.Any(), "resource-1").Return(resource, nil)

		err := service.Start(context.Background(), "task-1", false)
		httpErr := requireHTTPError(t, err, verrors.VegaBackend_BuildTask_InvalidParameter_Analyzer)
		assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
		assert.Contains(t, httpErr.ErrorDetails, "unavailable")
		assert.Equal(t, []string{"hanlp_index"}, validator.captured)
	})
}

func assertCatalogDisabledError(t *testing.T, err error) {
	t.Helper()
	httpErr := requireHTTPError(t, err, verrors.VegaBackend_Catalog_IsDisabled)
	assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
}

func requireHTTPError(t *testing.T, err error, wantErrorCode string) *httperrors.HTTPError {
	t.Helper()
	require.Error(t, err)
	var httpErr *httperrors.HTTPError
	ok := errors.As(err, &httpErr)
	require.Truef(t, ok, "expected HTTPError, got %T", err)
	assert.Equal(t, wantErrorCode, httpErr.ErrorCode)
	return httpErr
}

func buildTaskTestResource() *interfaces.Resource {
	return &interfaces.Resource{
		ID:          "resource-1",
		CatalogID:   "catalog-1",
		Category:    interfaces.ResourceCategoryTable,
		IndexConfig: &interfaces.ResourceIndexConfig{PrimaryKeyFields: []string{"id"}, IncrementalFields: []string{"id"}},
		SchemaDefinition: []*interfaces.Property{
			{Name: "id", Type: interfaces.DataType_Integer},
		},
	}
}

func mustBuildTaskIndexConfig(t *testing.T, resource *interfaces.Resource) *interfaces.BuildTaskIndexConfig {
	t.Helper()
	fields, err := resourcelogic.SnapshotBuildTaskIndexConfigFields(resource)
	require.NoError(t, err)
	config := &interfaces.BuildTaskIndexConfig{
		IndexConfigContract: interfaces.IndexConfigContract{Fields: fields},
		Features:            map[string]interfaces.BuildTaskFieldIndexFeature{},
	}
	if resource.IndexConfig != nil {
		config.PrimaryKeyFields = append([]string(nil), resource.IndexConfig.PrimaryKeyFields...)
		config.IncrementalFields = append([]string(nil), resource.IndexConfig.IncrementalFields...)
	}
	return config
}

func TestBuildTaskServiceStop(t *testing.T) {
	t.Run("running to stopping", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockBTA := vmock.NewMockBuildTaskAccess(ctrl)
		mockRSAuth := vmock.NewMockResourceService(ctrl)
		mockCSAuth := vmock.NewMockCatalogService(ctrl)
		mockCSAuth.EXPECT().CheckCatalogPermission(gomock.Any(), "catalog-1", []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true).Return(true, nil, nil)
		service := &buildTaskService{bta: mockBTA, rs: mockRSAuth, cs: mockCSAuth}

		mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").
			Return(&interfaces.BuildTask{ID: "task-1", CatalogID: "catalog-1", Status: interfaces.BuildTaskStatusRunning}, nil)
		mockBTA.EXPECT().MarkStopping(gomock.Any(), nil, "task-1").Return(true, nil)

		require.NoError(t, service.Stop(context.Background(), "task-1"))
	})
	t.Run("pending to stopped", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockBTA := vmock.NewMockBuildTaskAccess(ctrl)
		mockRSAuth := vmock.NewMockResourceService(ctrl)
		mockCSAuth := vmock.NewMockCatalogService(ctrl)
		mockCSAuth.EXPECT().CheckCatalogPermission(gomock.Any(), "catalog-1", []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true).Return(true, nil, nil)
		service := &buildTaskService{bta: mockBTA, rs: mockRSAuth, cs: mockCSAuth}

		mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").
			Return(&interfaces.BuildTask{ID: "task-1", CatalogID: "catalog-1", Status: interfaces.BuildTaskStatusPending}, nil)
		mockBTA.EXPECT().MarkStopped(gomock.Any(), nil, "task-1", []string{interfaces.BuildTaskStatusPending}, gomock.Any()).Return(true, nil)

		require.NoError(t, service.Stop(context.Background(), "task-1"))
	})
	t.Run("returns conflict when status changes before stop update", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockBTA := vmock.NewMockBuildTaskAccess(ctrl)
		mockRSAuth := vmock.NewMockResourceService(ctrl)
		mockCSAuth := vmock.NewMockCatalogService(ctrl)
		mockCSAuth.EXPECT().CheckCatalogPermission(gomock.Any(), "catalog-1", []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true).Return(true, nil, nil)
		service := &buildTaskService{bta: mockBTA, rs: mockRSAuth, cs: mockCSAuth}

		mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").
			Return(&interfaces.BuildTask{ID: "task-1", CatalogID: "catalog-1", Status: interfaces.BuildTaskStatusPending}, nil)
		mockBTA.EXPECT().MarkStopped(gomock.Any(), nil, "task-1", []string{interfaces.BuildTaskStatusPending}, gomock.Any()).Return(false, nil)

		err := service.Stop(context.Background(), "task-1")
		httpErr := requireHTTPError(t, err, verrors.VegaBackend_BuildTask_InvalidStateTransition)
		assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
	})
	t.Run("a stale pending stop cannot finish an accepted running stop", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bta := vmock.NewMockBuildTaskAccess(ctrl)
		cs := vmock.NewMockCatalogService(ctrl)
		service := &buildTaskService{bta: bta, cs: cs}
		task := interfaces.BuildTask{ID: "task-1", CatalogID: "catalog-1", Status: interfaces.BuildTaskStatusPending}
		pending := task
		operations := []string{interfaces.OPERATION_TYPE_TASK_MANAGE}

		gomock.InOrder(
			bta.EXPECT().GetByID(gomock.Any(), task.ID).Return(&pending, nil),
			cs.EXPECT().CheckCatalogPermission(gomock.Any(), task.CatalogID, operations, true).
				Do(func(context.Context, string, []string, bool) {
					// The worker claims the task and another Stop commits while the first is checking permissions.
					task.Status = interfaces.BuildTaskStatusRunning
					require.NoError(t, service.Stop(t.Context(), task.ID))
				}).Return(true, nil, nil),
			bta.EXPECT().GetByID(gomock.Any(), task.ID).Return(&task, nil),
			cs.EXPECT().CheckCatalogPermission(gomock.Any(), task.CatalogID, operations, true).Return(true, nil, nil),
			bta.EXPECT().MarkStopping(gomock.Any(), nil, task.ID).
				DoAndReturn(func(context.Context, *sql.Tx, string) (bool, error) {
					assert.Equal(t, interfaces.BuildTaskStatusRunning, task.Status)
					task.Status = interfaces.BuildTaskStatusStopping
					return true, nil
				}),
			bta.EXPECT().MarkStopped(gomock.Any(), nil, task.ID, gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, _ *sql.Tx, _ string, allowed []string, _ int64) (bool, error) {
					if !slices.Contains(allowed, task.Status) {
						return false, nil
					}
					task.Status = interfaces.BuildTaskStatusStopped
					return true, nil
				}),
		)

		err := service.Stop(t.Context(), task.ID)

		assert.Equal(t, interfaces.BuildTaskStatusStopping, task.Status)
		httpErr := requireHTTPError(t, err, verrors.VegaBackend_BuildTask_InvalidStateTransition)
		assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
	})
	for _, status := range []string{interfaces.BuildTaskStatusStopping, interfaces.BuildTaskStatusStopped} {
		t.Run("rejects "+status+" status", func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockBTA := vmock.NewMockBuildTaskAccess(ctrl)
			mockRSAuth := vmock.NewMockResourceService(ctrl)
			mockCSAuth := vmock.NewMockCatalogService(ctrl)
			mockCSAuth.EXPECT().CheckCatalogPermission(gomock.Any(), "catalog-1", []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true).Return(true, nil, nil)
			service := &buildTaskService{bta: mockBTA, rs: mockRSAuth, cs: mockCSAuth}

			mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").
				Return(&interfaces.BuildTask{ID: "task-1", CatalogID: "catalog-1", Status: status}, nil)

			err := service.Stop(context.Background(), "task-1")
			_ = requireHTTPError(t, err, verrors.VegaBackend_BuildTask_InvalidStateTransition)
		})
	}
}

func TestBuildTaskServiceDeleteByIDs(t *testing.T) {
	t.Run("checks a shared catalog only once", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bta := vmock.NewMockBuildTaskAccess(ctrl)
		cs := vmock.NewMockCatalogService(ctrl)
		service := &buildTaskService{bta: bta, cs: cs}

		ids := []string{"t1", "t2"}
		bta.EXPECT().GetByIDs(gomock.Any(), ids).Return(map[string]*interfaces.BuildTask{
			"t1": {ID: "t1", CatalogID: "catalog-1", Status: interfaces.BuildTaskStatusCompleted},
			"t2": {ID: "t2", CatalogID: "catalog-1", Status: interfaces.BuildTaskStatusStopped},
		}, nil)
		cs.EXPECT().CheckCatalogPermission(gomock.Any(), "catalog-1",
			[]string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true).Return(true, nil, nil).Times(1)
		bta.EXPECT().DeleteByIDs(gomock.Any(), ids).Return(int64(2), nil)

		require.NoError(t, service.DeleteByIDs(context.Background(), ids, false))
	})

	for _, status := range []string{
		interfaces.BuildTaskStatusCompleted,
		interfaces.BuildTaskStatusFailed,
		interfaces.BuildTaskStatusStopped,
	} {
		t.Run("deletes "+status+" task without resource or index access", func(t *testing.T) {
			ctrl := gomock.NewController(t)
			bta := vmock.NewMockBuildTaskAccess(ctrl)
			cs := vmock.NewMockCatalogService(ctrl)
			rs := vmock.NewMockResourceService(ctrl)
			lim := vmock.NewMockLocalIndexManager(ctrl)
			service := &buildTaskService{bta: bta, cs: cs, rs: rs, lim: lim}

			bta.EXPECT().GetByIDs(gomock.Any(), []string{"t1"}).Return(map[string]*interfaces.BuildTask{
				"t1": {ID: "t1", CatalogID: "catalog-1", ResourceID: "r1", Status: status},
			}, nil)
			cs.EXPECT().CheckCatalogPermission(gomock.Any(), "catalog-1", []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true).Return(true, nil, nil)
			bta.EXPECT().DeleteByIDs(gomock.Any(), []string{"t1"}).Return(int64(1), nil)

			require.NoError(t, service.DeleteByIDs(context.Background(), []string{"t1"}, false))
		})
	}
	for _, status := range []string{
		interfaces.BuildTaskStatusPending,
		interfaces.BuildTaskStatusRunning,
		interfaces.BuildTaskStatusStopping,
	} {
		t.Run("refuses "+status, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockBTA := vmock.NewMockBuildTaskAccess(ctrl)
			mockCS := vmock.NewMockCatalogService(ctrl)
			mockCS.EXPECT().CheckCatalogPermission(gomock.Any(), "catalog-1", []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true).Return(true, nil, nil)
			service := &buildTaskService{bta: mockBTA, cs: mockCS}

			mockBTA.EXPECT().GetByIDs(gomock.Any(), []string{"t1"}).Return(map[string]*interfaces.BuildTask{
				"t1": {ID: "t1", CatalogID: "catalog-1", ResourceID: "r1", Status: status},
			}, nil)

			err := service.DeleteByIDs(context.Background(), []string{"t1"}, false)

			httpErr := requireHTTPError(t, err, verrors.VegaBackend_BuildTask_HasRunningExecution)
			assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
		})
	}

	t.Run("maps bulk read failure to internal error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bta := vmock.NewMockBuildTaskAccess(ctrl)
		svc := &buildTaskService{bta: bta}
		bta.EXPECT().GetByIDs(gomock.Any(), []string{"t1"}).Return(nil, errors.New("database unavailable"))

		_ = requireHTTPError(t, svc.DeleteByIDs(context.Background(), []string{"t1"}, false), verrors.VegaBackend_BuildTask_InternalError_GetFailed)
	})

	t.Run("reports missing ids unless ignored", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bta := vmock.NewMockBuildTaskAccess(ctrl)
		svc := &buildTaskService{bta: bta}
		bta.EXPECT().GetByIDs(gomock.Any(), []string{"missing"}).Return(map[string]*interfaces.BuildTask{}, nil)

		httpErr := requireHTTPError(t, svc.DeleteByIDs(context.Background(), []string{"missing"}, false), verrors.VegaBackend_BuildTask_NotFound)
		assert.Equal(t, map[string]any{"missing_ids": []string{"missing"}}, httpErr.ErrorDetails)
	})

	t.Run("ignores missing ids and deletes returned terminal tasks", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bta := vmock.NewMockBuildTaskAccess(ctrl)
		cs := vmock.NewMockCatalogService(ctrl)
		svc := &buildTaskService{bta: bta, cs: cs}
		bta.EXPECT().GetByIDs(gomock.Any(), []string{"t1", "missing"}).Return(map[string]*interfaces.BuildTask{
			"t1": {ID: "t1", CatalogID: "c1", Status: interfaces.BuildTaskStatusCompleted},
		}, nil)
		cs.EXPECT().CheckCatalogPermission(gomock.Any(), "c1", []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true).Return(true, nil, nil)
		bta.EXPECT().DeleteByIDs(gomock.Any(), []string{"t1"}).Return(int64(1), nil)

		require.NoError(t, svc.DeleteByIDs(context.Background(), []string{"t1", "missing"}, true))
	})

	t.Run("reports active ids before missing ids", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bta := vmock.NewMockBuildTaskAccess(ctrl)
		cs := vmock.NewMockCatalogService(ctrl)
		svc := &buildTaskService{bta: bta, cs: cs}
		bta.EXPECT().GetByIDs(gomock.Any(), []string{"active", "missing"}).Return(map[string]*interfaces.BuildTask{
			"active": {ID: "active", CatalogID: "c1", Status: interfaces.BuildTaskStatusRunning},
		}, nil)
		cs.EXPECT().CheckCatalogPermission(gomock.Any(), "c1", []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true).Return(true, nil, nil)

		httpErr := requireHTTPError(t, svc.DeleteByIDs(context.Background(), []string{"active", "missing"}, false), verrors.VegaBackend_BuildTask_HasRunningExecution)
		assert.Equal(t, map[string]any{"active_ids": []string{"active"}}, httpErr.ErrorDetails)
	})

	for _, tt := range []struct {
		name   string
		result int64
		err    error
	}{
		{"maps delete failure to internal error", 0, errors.New("delete failed")},
		{"rejects incomplete delete", 0, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			bta := vmock.NewMockBuildTaskAccess(ctrl)
			cs := vmock.NewMockCatalogService(ctrl)
			svc := &buildTaskService{bta: bta, cs: cs}
			bta.EXPECT().GetByIDs(gomock.Any(), []string{"t1"}).Return(map[string]*interfaces.BuildTask{
				"t1": {ID: "t1", CatalogID: "c1", Status: interfaces.BuildTaskStatusCompleted},
			}, nil)
			cs.EXPECT().CheckCatalogPermission(gomock.Any(), "c1", []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true).Return(true, nil, nil)
			bta.EXPECT().DeleteByIDs(gomock.Any(), []string{"t1"}).Return(tt.result, tt.err)

			_ = requireHTTPError(t, svc.DeleteByIDs(context.Background(), []string{"t1"}, false), verrors.VegaBackend_BuildTask_InternalError_DeleteFailed)
		})
	}
}

func TestBuildTaskServiceInternalGetByID(t *testing.T) {
	t.Run("delegates lookup", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bta := vmock.NewMockBuildTaskAccess(ctrl)
		svc := &buildTaskService{bta: bta}

		bta.EXPECT().GetByID(gomock.Any(), "t1").Return(&interfaces.BuildTask{ID: "t1"}, nil)

		task, err := svc.InternalGetByID(context.Background(), "t1")
		require.NoError(t, err)
		assert.Equal(t, "t1", task.ID)
	})
}

func TestBuildTaskServiceInternalGetByCatalogID(t *testing.T) {
	t.Run("delegates catalog lookup", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bta := vmock.NewMockBuildTaskAccess(ctrl)
		svc := &buildTaskService{bta: bta}
		bta.EXPECT().GetByCatalogID(gomock.Any(), "c1").Return([]*interfaces.BuildTask{{ID: "t1"}}, nil)

		tasks, err := svc.InternalGetByCatalogID(context.Background(), "c1")
		require.NoError(t, err)
		assert.Len(t, tasks, 1)
	})
}

func TestBuildTaskServiceInternalList(t *testing.T) {
	t.Run("delegates query parameters", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bta := vmock.NewMockBuildTaskAccess(ctrl)
		svc := &buildTaskService{bta: bta}
		params := interfaces.BuildTasksQueryParams{ResourceID: "r1"}
		bta.EXPECT().InternalList(gomock.Any(), params).Return([]*interfaces.BuildTaskSummary{{ID: "t1"}}, nil)

		tasks, err := svc.InternalList(context.Background(), params)

		require.NoError(t, err)
		require.Len(t, tasks, 1)
		assert.Equal(t, "t1", tasks[0].ID)
	})
}

func TestBuildTaskServiceInternalGetStatusByID(t *testing.T) {
	t.Run("delegates status lookup", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bta := vmock.NewMockBuildTaskAccess(ctrl)
		svc := &buildTaskService{bta: bta}
		bta.EXPECT().GetStatusByID(gomock.Any(), "t1").Return(interfaces.BuildTaskStatusRunning, nil)

		status, err := svc.InternalGetStatusByID(context.Background(), "t1")
		require.NoError(t, err)
		assert.Equal(t, interfaces.BuildTaskStatusRunning, status)
	})
}

func buildTaskTestConnectorFactory(t *testing.T) interfaces.ConnectorFactory {
	t.Helper()
	cf := vmock.NewMockConnectorFactory(gomock.NewController(t))
	cf.EXPECT().ValidateBuildSchema(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	return cf
}

func TestBuildTaskServiceValidateConnectorBuildSchema(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
	}{
		{"accepted", nil, 0}, {"schema rejected", errors.New("field price is unsupported"), http.StatusBadRequest},
		{"edition denied", factory.ErrConnectorEntitlementDenied, http.StatusForbidden}, {"connector disabled", factory.ErrConnectorDisabled, http.StatusConflict}, {"connector unavailable", factory.ErrConnectorUnavailable, http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cf := vmock.NewMockConnectorFactory(gomock.NewController(t))
			resource := buildTaskTestResource()
			cf.EXPECT().ValidateBuildSchema("custom-table", resource).Return(tc.err)
			err := (&buildTaskService{cf: cf}).validateConnectorBuildSchema(context.Background(), resource, "custom-table")
			if tc.status == 0 {
				require.NoError(t, err)
				return
			}
			var httpErr *httperrors.HTTPError
			require.ErrorAs(t, err, &httpErr)
			assert.Equal(t, tc.status, httpErr.HTTPCode)
		})
	}
}
