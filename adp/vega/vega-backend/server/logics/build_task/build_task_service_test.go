// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package build_task

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	verrors "vega-backend/errors"
	"vega-backend/interfaces"
	mock_interfaces "vega-backend/interfaces/mock"
	resourcelogic "vega-backend/logics/resource"
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
		mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		service := &buildTaskService{bta: mockBTA}
		tx := &sql.Tx{}
		mockBTA.EXPECT().MarkRunning(gomock.Any(), tx, "task-1", gomock.Any()).Return(true, nil)

		updated, err := service.InternalMarkRunning(context.Background(), tx, "task-1")

		require.NoError(t, err)
		assert.True(t, updated)
	})
}

func TestBuildTaskServiceInternalSetProgress(t *testing.T) {
	t.Run("delegates progress with caller transaction", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bta := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		service := &buildTaskService{bta: bta}
		tx := &sql.Tx{}
		syncedCount := int64(10)
		syncedMark := `{"id":10}`
		progress := interfaces.BuildTaskProgress{SyncedCount: &syncedCount, SyncedMark: &syncedMark}
		bta.EXPECT().SetProgress(gomock.Any(), tx, "task-1", progress, gomock.Any()).Return(true, nil)

		updated, err := service.InternalSetProgress(context.Background(), tx, "task-1", progress)

		require.NoError(t, err)
		assert.True(t, updated)
	})
}

func TestBuildTaskServiceInternalMarkFailed(t *testing.T) {
	t.Run("delegates with caller transaction", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bta := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		service := &buildTaskService{bta: bta}
		tx := &sql.Tx{}
		bta.EXPECT().MarkFailed(gomock.Any(), tx, "task-1", "execution failed", gomock.Any()).Return(true, nil)

		updated, err := service.InternalMarkFailed(context.Background(), tx, "task-1", "execution failed")

		require.NoError(t, err)
		assert.True(t, updated)
	})
}

func TestBuildTaskServiceInternalMarkCancelled(t *testing.T) {
	t.Run("delegates cancellation with caller transaction", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bta := mock_interfaces.NewMockBuildTaskAccess(ctrl)
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
		bta := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		service := &buildTaskService{bta: bta}
		tx := &sql.Tx{}
		bta.EXPECT().MarkStopped(gomock.Any(), tx, "task-1", gomock.Any()).Return(true, nil)

		updated, err := service.InternalMarkStopped(context.Background(), tx, "task-1")

		require.NoError(t, err)
		assert.True(t, updated)
	})
}

func TestBuildTaskServiceInternalMarkCompleted(t *testing.T) {
	t.Run("delegates completed transition with caller transaction", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bta := mock_interfaces.NewMockBuildTaskAccess(ctrl)
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
	t.Run("rejects missing embedding model", func(t *testing.T) {
		service := &buildTaskService{}
		buildTask := &interfaces.BuildTask{}
		err := service.fillBuildTaskIndexSnapshot(context.Background(), &interfaces.Resource{SchemaDefinition: []*interfaces.Property{{
			Name: "title", Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Vector, RefProperty: "title"}},
		}}}, buildTask)
		requireHTTPError(t, err, verrors.VegaBackend_BuildTask_InvalidParameter_EmbeddingModel)
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
		assert.Contains(t, httpErr.BaseError.ErrorDetails, `property "title" has more than one "fulltext" feature`)
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
		assert.Contains(t, httpErr.BaseError.ErrorDetails, "connection refused")
	})
}

func TestBuildTaskServiceList(t *testing.T) {
	t.Run("list populates only referenced resources and catalogs", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockRS := mock_interfaces.NewMockResourceService(ctrl)
		mockCS.EXPECT().AuthorizedCatalogsForTasks(gomock.Any(), interfaces.OPERATION_TYPE_TASK_MANAGE).
			Return(nil, true, nil, nil)
		mockUMS := mock_interfaces.NewMockUserMgmtService(ctrl)
		service := &buildTaskService{bta: mockBTA, cs: mockCS, rs: mockRS, ums: mockUMS}
		tasks := []*interfaces.BuildTaskSummary{
			{ID: "task-1", ResourceID: "resource-1", CatalogID: "catalog-1"},
			{ID: "task-2", ResourceID: "resource-2", CatalogID: "catalog-1"},
		}

		mockBTA.EXPECT().List(gomock.Any(), gomock.Any()).Return(tasks, int64(2), nil)
		mockRS.EXPECT().InternalGetByIDs(gomock.Any(), []string{"resource-1", "resource-2"}).Return([]*interfaces.Resource{
			{ID: "resource-1", Name: "orders"},
			{ID: "resource-2", Name: "customers"},
		}, nil)
		mockCS.EXPECT().InternalGetByIDs(gomock.Any(), []string{"catalog-1"}).Return([]*interfaces.Catalog{{ID: "catalog-1", Name: "production"}}, nil)
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
		mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockRS := mock_interfaces.NewMockResourceService(ctrl)
		mockCS.EXPECT().AuthorizedCatalogsForTasks(gomock.Any(), interfaces.OPERATION_TYPE_TASK_MANAGE).
			Return(nil, true, nil, nil)
		mockUMS := mock_interfaces.NewMockUserMgmtService(ctrl)
		service := &buildTaskService{bta: mockBTA, cs: mockCS, rs: mockRS, ums: mockUMS}
		tasks := []*interfaces.BuildTaskSummary{{ID: "task-1", ResourceID: "resource-1", CatalogID: "catalog-1"}}

		mockBTA.EXPECT().List(gomock.Any(), gomock.Any()).Return(tasks, int64(1), nil)
		mockRS.EXPECT().InternalGetByIDs(gomock.Any(), []string{"resource-1"}).Return(nil, errors.New("resource service down"))
		mockCS.EXPECT().InternalGetByIDs(gomock.Any(), []string{"catalog-1"}).Return([]*interfaces.Catalog{{ID: "catalog-1", Name: "production"}}, nil)
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
		bta := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		cs := mock_interfaces.NewMockCatalogService(ctrl)
		service := &buildTaskService{bta: bta, cs: cs}
		cs.EXPECT().AuthorizedCatalogsForTasks(gomock.Any(), interfaces.OPERATION_TYPE_TASK_MANAGE).
			Return(nil, true, nil, nil)
		bta.EXPECT().List(gomock.Any(), interfaces.BuildTasksQueryParams{}).
			Return(nil, int64(0), errors.New("list failed"))

		got, total, err := service.List(context.Background(), interfaces.BuildTasksQueryParams{})

		httpErr := requireHTTPError(t, err, verrors.VegaBackend_BuildTask_InternalError_GetFailed)
		assert.Nil(t, got)
		assert.Zero(t, total)
		assert.Contains(t, httpErr.BaseError.ErrorDetails, "list failed")
	})
}

func TestBuildTaskServiceGetByID(t *testing.T) {
	t.Run("populates referenced resource and catalog", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockRS := mock_interfaces.NewMockResourceService(ctrl)
		mockCS.EXPECT().CheckTaskPermission(gomock.Any(), "catalog-1", interfaces.OPERATION_TYPE_TASK_MANAGE).
			Return(nil)
		mockUMS := mock_interfaces.NewMockUserMgmtService(ctrl)
		service := &buildTaskService{bta: mockBTA, cs: mockCS, rs: mockRS, ums: mockUMS}
		task := &interfaces.BuildTask{ID: "task-1", ResourceID: "resource-1", CatalogID: "catalog-1"}

		mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").Return(task, nil)
		mockRS.EXPECT().InternalGetByIDs(gomock.Any(), []string{"resource-1"}).Return([]*interfaces.Resource{
			{ID: "resource-1", Name: "orders"},
		}, nil)
		mockCS.EXPECT().InternalGetByIDs(gomock.Any(), []string{"catalog-1"}).Return([]*interfaces.Catalog{{ID: "catalog-1", Name: "production"}}, nil)
		mockUMS.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil)

		got, err := service.GetByID(context.Background(), "task-1")

		require.NoError(t, err)
		assert.Equal(t, "orders", got.ResourceName)
		assert.Equal(t, "production", got.CatalogName)
	})
	t.Run("keeps task when account lookup fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockRS := mock_interfaces.NewMockResourceService(ctrl)
		mockCS.EXPECT().CheckTaskPermission(gomock.Any(), "", interfaces.OPERATION_TYPE_TASK_MANAGE).
			Return(nil)
		mockUMS := mock_interfaces.NewMockUserMgmtService(ctrl)
		service := &buildTaskService{bta: mockBTA, cs: mockCS, rs: mockRS, ums: mockUMS}
		task := &interfaces.BuildTask{ID: "task-2"}

		mockBTA.EXPECT().GetByID(gomock.Any(), "task-2").Return(task, nil)
		mockRS.EXPECT().InternalGetByIDs(gomock.Any(), []string{}).Return([]*interfaces.Resource{}, nil)
		mockCS.EXPECT().InternalGetByIDs(gomock.Any(), []string{}).Return([]*interfaces.Catalog{}, nil)
		mockUMS.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(errors.New("user service down"))

		got, err := service.GetByID(context.Background(), "task-2")

		require.NoError(t, err)
		assert.Equal(t, "task-2", got.ID)
	})

	t.Run("maps access failure to internal error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bta := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		service := &buildTaskService{bta: bta}
		bta.EXPECT().GetByID(gomock.Any(), "task-1").Return(nil, errors.New("get failed"))

		got, err := service.GetByID(context.Background(), "task-1")

		httpErr := requireHTTPError(t, err, verrors.VegaBackend_BuildTask_InternalError_GetFailed)
		assert.Nil(t, got)
		assert.Contains(t, httpErr.BaseError.ErrorDetails, "get failed")
	})

	t.Run("returns not found", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bta := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		service := &buildTaskService{bta: bta}
		bta.EXPECT().GetByID(gomock.Any(), "missing").Return(nil, nil)

		got, err := service.GetByID(context.Background(), "missing")

		httpErr := requireHTTPError(t, err, verrors.VegaBackend_BuildTask_NotFound)
		assert.Nil(t, got)
		assert.Equal(t, http.StatusNotFound, httpErr.HTTPCode)
	})
}

func TestBuildTaskServiceCreate(t *testing.T) {
	t.Run("rejects unavailable field analyzer before persistence", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockRS := mock_interfaces.NewMockResourceService(ctrl)
		mockCS.EXPECT().CheckTaskPermission(gomock.Any(), "catalog-1", interfaces.OPERATION_TYPE_TASK_MANAGE).
			Return(nil)
		mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		validator := &analyzerValidatingIndexManager{}
		service := &buildTaskService{
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
				BuildKeyFields:          []string{"id"},
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
		assert.Contains(t, httpErr.BaseError.ErrorDetails, "analyzer")
		assert.Len(t, validator.captured, 1)
	})
	t.Run("rejects batch task without build key fields", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockRS := mock_interfaces.NewMockResourceService(ctrl)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockCS.EXPECT().CheckTaskPermission(gomock.Any(), "catalog-1", interfaces.OPERATION_TYPE_TASK_MANAGE).Return(nil)
		service := &buildTaskService{rs: mockRS, cs: mockCS}

		mockRS.EXPECT().GetByID(gomock.Any(), "resource-1").Return(&interfaces.Resource{
			ID:        "resource-1",
			CatalogID: "catalog-1",
			Category:  interfaces.ResourceCategoryTable,
		}, nil)

		_, err := service.Create(context.Background(), &interfaces.CreateBuildTaskRequest{
			ResourceID: "resource-1",
			Mode:       interfaces.BuildTaskModeBatch,
		})
		httpErr := requireHTTPError(t, err, verrors.VegaBackend_BuildTask_InvalidParameter_BuildKeyFields)
		assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
	})
	t.Run("rejects streaming task without build key fields", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockRS := mock_interfaces.NewMockResourceService(ctrl)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockCS.EXPECT().CheckTaskPermission(gomock.Any(), "catalog-1", interfaces.OPERATION_TYPE_TASK_MANAGE).Return(nil)
		service := &buildTaskService{rs: mockRS, cs: mockCS}

		mockRS.EXPECT().GetByID(gomock.Any(), "resource-1").Return(&interfaces.Resource{
			ID:        "resource-1",
			CatalogID: "catalog-1",
			Category:  interfaces.ResourceCategoryTable,
		}, nil)

		_, err := service.Create(context.Background(), &interfaces.CreateBuildTaskRequest{
			ResourceID: "resource-1",
			Mode:       interfaces.BuildTaskModeStreaming,
		})
		httpErr := requireHTTPError(t, err, verrors.VegaBackend_BuildTask_InvalidParameter_BuildKeyFields)
		assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
	})
	t.Run("rejects resources containing unsupported fields before creating a task", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockRS := mock_interfaces.NewMockResourceService(ctrl)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockCS.EXPECT().CheckTaskPermission(gomock.Any(), "catalog-1", interfaces.OPERATION_TYPE_TASK_MANAGE).Return(nil)
		service := &buildTaskService{rs: mockRS, cs: mockCS}

		mockRS.EXPECT().GetByID(gomock.Any(), "resource-1").Return(&interfaces.Resource{
			ID:          "resource-1",
			CatalogID:   "catalog-1",
			Category:    interfaces.ResourceCategoryTable,
			IndexConfig: &interfaces.ResourceIndexConfig{BuildKeyFields: []string{"id"}},
			SchemaDefinition: []*interfaces.Property{
				{Name: "id", Type: interfaces.DataType_Integer},
				{Name: "interests", Type: interfaces.DataType_Other, OriginalType: "_text"},
			},
		}, nil)

		_, err := service.Create(context.Background(), &interfaces.CreateBuildTaskRequest{ResourceID: "resource-1", Mode: interfaces.BuildTaskModeBatch})
		httpErr := requireHTTPError(t, err, verrors.VegaBackend_BuildTask_InvalidParameter_UnsupportedSchemaFields)
		assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
		assert.Contains(t, httpErr.BaseError.ErrorDetails, "interests (original_type: _text)")
	})

	t.Run("rejects an unsupported build key type before creating a task", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockRS := mock_interfaces.NewMockResourceService(ctrl)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockCS.EXPECT().CheckTaskPermission(gomock.Any(), "catalog-1", interfaces.OPERATION_TYPE_TASK_MANAGE).Return(nil)
		service := &buildTaskService{rs: mockRS, cs: mockCS}

		mockRS.EXPECT().GetByID(gomock.Any(), "resource-1").Return(&interfaces.Resource{
			ID:               "resource-1",
			CatalogID:        "catalog-1",
			Category:         interfaces.ResourceCategoryTable,
			IndexConfig:      &interfaces.ResourceIndexConfig{BuildKeyFields: []string{"body"}},
			SchemaDefinition: []*interfaces.Property{{Name: "body", Type: interfaces.DataType_Text}},
		}, nil)

		_, err := service.Create(context.Background(), &interfaces.CreateBuildTaskRequest{ResourceID: "resource-1", Mode: interfaces.BuildTaskModeBatch})
		httpErr := requireHTTPError(t, err, verrors.VegaBackend_BuildTask_InvalidParameter_BuildKeyFields)
		assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
		assert.Contains(t, httpErr.BaseError.ErrorDetails, `unsupported type "text"`)
	})

	t.Run("rejects disabled catalog", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockRS := mock_interfaces.NewMockResourceService(ctrl)
		mockCS.EXPECT().CheckTaskPermission(gomock.Any(), "catalog-1", interfaces.OPERATION_TYPE_TASK_MANAGE).Return(nil)
		service := &buildTaskService{cs: mockCS, rs: mockRS}

		mockRS.EXPECT().GetByID(gomock.Any(), "resource-1").
			Return(&interfaces.Resource{
				ID:               "resource-1",
				CatalogID:        "catalog-1",
				Category:         interfaces.ResourceCategoryTable,
				IndexConfig:      &interfaces.ResourceIndexConfig{BuildKeyFields: []string{"id"}},
				SchemaDefinition: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_Integer}},
			}, nil)
		mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
			Return(&interfaces.Catalog{ID: "catalog-1", Enabled: false}, nil)

		_, err := service.Create(context.Background(), &interfaces.CreateBuildTaskRequest{ResourceID: "resource-1"})
		assertCatalogDisabledError(t, err)
	})
	t.Run("rejects active task for resource", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockRS := mock_interfaces.NewMockResourceService(ctrl)
		mockCS.EXPECT().CheckTaskPermission(gomock.Any(), "catalog-1", interfaces.OPERATION_TYPE_TASK_MANAGE).Return(nil)
		mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		service := &buildTaskService{cs: mockCS, rs: mockRS, bta: mockBTA}

		mockRS.EXPECT().GetByID(gomock.Any(), "resource-1").
			Return(&interfaces.Resource{
				ID:               "resource-1",
				CatalogID:        "catalog-1",
				Category:         interfaces.ResourceCategoryTable,
				IndexConfig:      &interfaces.ResourceIndexConfig{BuildKeyFields: []string{"id"}},
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

		requireHTTPError(t, err, verrors.VegaBackend_BuildTask_Exist)
	})
	t.Run("creates incremental task from resource baseline", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockRS := mock_interfaces.NewMockResourceService(ctrl)
		mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		mockCS.EXPECT().CheckTaskPermission(gomock.Any(), "catalog-1", interfaces.OPERATION_TYPE_TASK_MANAGE).Return(nil)
		service := &buildTaskService{cs: mockCS, rs: mockRS, bta: mockBTA}
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
	t.Run("allows streaming task with a build key and no physical primary key", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockRS := mock_interfaces.NewMockResourceService(ctrl)
		mockCS.EXPECT().CheckTaskPermission(gomock.Any(), "catalog-1", interfaces.OPERATION_TYPE_TASK_MANAGE).Return(nil)
		mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		service := &buildTaskService{
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
				BuildKeyFields: []string{"supplier_id"},
			},
			SchemaDefinition: []*interfaces.Property{{Name: "supplier_id", Type: interfaces.DataType_Integer}},
		}, nil)
		mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
			Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
		mockBTA.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return(nil, nil)
		mockBTA.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)

		_, err := service.Create(context.Background(), &interfaces.CreateBuildTaskRequest{
			ResourceID: "resource-1",
			Mode:       interfaces.BuildTaskModeStreaming,
		})

		require.NoError(t, err)
		select {
		case <-service.DispatchSignal():
		default:
			t.Fatal("expected a dispatch signal after the task was persisted")
		}
	})
	t.Run("rejects execute type for streaming", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockRS := mock_interfaces.NewMockResourceService(ctrl)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockCS.EXPECT().CheckTaskPermission(gomock.Any(), "catalog-1", interfaces.OPERATION_TYPE_TASK_MANAGE).Return(nil)
		service := &buildTaskService{rs: mockRS, cs: mockCS}

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

		requireHTTPError(t, err, verrors.VegaBackend_BuildTask_InvalidExecuteType)
	})
	t.Run("caches default embedding SmallModel by model ID", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockRS := mock_interfaces.NewMockResourceService(ctrl)
		mockCS.EXPECT().CheckTaskPermission(gomock.Any(), "catalog-1", interfaces.OPERATION_TYPE_TASK_MANAGE).Return(nil)
		mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		mockMFS := mock_interfaces.NewMockModelFactoryService(ctrl)
		service := &buildTaskService{cs: mockCS, rs: mockRS, bta: mockBTA, mfs: mockMFS}

		mockRS.EXPECT().GetByID(gomock.Any(), "resource-1").
			Return(&interfaces.Resource{
				ID:        "resource-1",
				CatalogID: "catalog-1",
				Category:  interfaces.ResourceCategoryTable,
				IndexConfig: &interfaces.ResourceIndexConfig{
					BuildKeyFields:          []string{"id"},
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
		assert.Equal(t, []string{"id"}, captured.IndexConfig.BuildKeyFields)
		expectedModel := &interfaces.SmallModel{ModelID: "2064382281006583808", ModelName: "text-embedding-v4", EmbeddingDim: 1024}
		assert.Equal(t, expectedModel, captured.IndexConfig.Features["family_name"].Vector)
		assert.Equal(t, expectedModel, captured.IndexConfig.Features["given_name"].Vector)
		assert.Equal(t, &interfaces.BuildTaskFulltextConfig{Analyzer: "ik_max_word"}, captured.IndexConfig.Features["family_name"].Fulltext)
	})
	t.Run("snapshot unaffected by resource mutation", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockRS := mock_interfaces.NewMockResourceService(ctrl)
		mockCS.EXPECT().CheckTaskPermission(gomock.Any(), "catalog-1", interfaces.OPERATION_TYPE_TASK_MANAGE).Return(nil)
		mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		mockMFS := mock_interfaces.NewMockModelFactoryService(ctrl)
		service := &buildTaskService{cs: mockCS, rs: mockRS, bta: mockBTA, mfs: mockMFS}

		resource := &interfaces.Resource{
			ID:        "resource-1",
			CatalogID: "catalog-1",
			Category:  interfaces.ResourceCategoryTable,
			IndexConfig: &interfaces.ResourceIndexConfig{
				BuildKeyFields:          []string{"id"},
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

		resource.IndexConfig.BuildKeyFields[0] = "changed"
		resource.IndexConfig.DefaultEmbeddingModel = "changed-model"
		resource.SchemaDefinition[0].Features = nil

		assert.Equal(t, []string{"id"}, captured.IndexConfig.BuildKeyFields)
		assert.Equal(t, expectedFields, captured.IndexConfig.Fields)
		assert.Equal(t, &interfaces.SmallModel{ModelID: "2064382281006583808", ModelName: "text-embedding-v4", EmbeddingDim: 1024}, captured.IndexConfig.Features["family_name"].Vector)
		assert.Equal(t, &interfaces.BuildTaskFulltextConfig{Analyzer: "ik_max_word"}, captured.IndexConfig.Features["family_name"].Fulltext)
	})
	t.Run("uses feature embedding model override", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockRS := mock_interfaces.NewMockResourceService(ctrl)
		mockCS.EXPECT().CheckTaskPermission(gomock.Any(), "catalog-1", interfaces.OPERATION_TYPE_TASK_MANAGE).Return(nil)
		mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		mockMFS := mock_interfaces.NewMockModelFactoryService(ctrl)
		service := &buildTaskService{cs: mockCS, rs: mockRS, bta: mockBTA, mfs: mockMFS}

		mockRS.EXPECT().GetByID(gomock.Any(), "resource-1").
			Return(&interfaces.Resource{
				ID:        "resource-1",
				CatalogID: "catalog-1",
				Category:  interfaces.ResourceCategoryTable,
				IndexConfig: &interfaces.ResourceIndexConfig{
					BuildKeyFields:        []string{"id"},
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
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockRS := mock_interfaces.NewMockResourceService(ctrl)
		mockCS.EXPECT().CheckTaskPermission(gomock.Any(), "catalog-1", interfaces.OPERATION_TYPE_TASK_MANAGE).Return(nil)
		mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		mockMFS := mock_interfaces.NewMockModelFactoryService(ctrl)
		service := &buildTaskService{cs: mockCS, rs: mockRS, bta: mockBTA, mfs: mockMFS}

		mockRS.EXPECT().GetByID(gomock.Any(), "resource-1").
			Return(&interfaces.Resource{
				ID:        "resource-1",
				CatalogID: "catalog-1",
				Category:  interfaces.ResourceCategoryTable,
				IndexConfig: &interfaces.ResourceIndexConfig{
					BuildKeyFields:          []string{"id"},
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
		assert.Equal(t, []string{"id"}, captured.IndexConfig.BuildKeyFields)
		assert.Equal(t, &interfaces.SmallModel{ModelID: "model-a-id", ModelName: "model-a", EmbeddingDim: 768}, captured.IndexConfig.Features["title"].Vector)
		assert.Equal(t, &interfaces.SmallModel{ModelID: "model-b-id", ModelName: "model-b", EmbeddingDim: 1024}, captured.IndexConfig.Features["body"].Vector)
		assert.Equal(t, &interfaces.BuildTaskFulltextConfig{Analyzer: "ik_max_word"}, captured.IndexConfig.Features["title"].Fulltext)
		assert.Equal(t, &interfaces.BuildTaskFulltextConfig{Analyzer: "standard"}, captured.IndexConfig.Features["body"].Fulltext)
	})
	t.Run("errors when model unresolvable and no dimensions", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockRS := mock_interfaces.NewMockResourceService(ctrl)
		mockCS.EXPECT().CheckTaskPermission(gomock.Any(), "catalog-1", interfaces.OPERATION_TYPE_TASK_MANAGE).Return(nil)
		mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		mockMFS := mock_interfaces.NewMockModelFactoryService(ctrl)
		service := &buildTaskService{cs: mockCS, rs: mockRS, bta: mockBTA, mfs: mockMFS}

		mockRS.EXPECT().GetByID(gomock.Any(), "resource-1").
			Return(&interfaces.Resource{
				ID:        "resource-1",
				CatalogID: "catalog-1",
				Category:  interfaces.ResourceCategoryTable,
				IndexConfig: &interfaces.ResourceIndexConfig{
					BuildKeyFields:        []string{"id"},
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

func TestValidateIncrementalBaseline(t *testing.T) {
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
		{name: "rejects unavailable index", resource: func() *interfaces.Resource {
			resource := buildTaskTestResource()
			resource.SyncMark = validMark
			return resource
		}(), wantErr: true},
		{name: "rejects absent checkpoint", resource: func() *interfaces.Resource {
			resource := buildTaskTestResource()
			resource.LocalIndexStatus = interfaces.ResourceLocalIndexStatusAvailable
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
			err := validateIncrementalBaseline(context.Background(), tt.resource)
			if !tt.wantErr {
				require.NoError(t, err)
				return
			}
			httpErr := requireHTTPError(t, err, verrors.VegaBackend_BuildTask_IncrementalBaselineUnavailable)
			assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
		})
	}
}

func TestBuildTaskServiceStart(t *testing.T) {
	t.Run("persists full reset before requesting dispatch", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockRS := mock_interfaces.NewMockResourceService(ctrl)
		mockCS.EXPECT().CheckTaskPermission(gomock.Any(), "catalog-1", interfaces.OPERATION_TYPE_TASK_MANAGE).Return(nil)
		mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		service := &buildTaskService{
			cs:         mockCS,
			rs:         mockRS,
			bta:        mockBTA,
			dispatchCh: make(chan struct{}, buildTaskDispatchBuffer),
		}
		resource := buildTaskTestResource()
		task := &interfaces.BuildTask{
			ID: "task-1", ResourceID: "resource-1", CatalogID: "catalog-1",
			Status: interfaces.BuildTaskStatusFailed, ExecuteType: interfaces.BuildTaskExecuteTypeFull,
			IndexConfig: mustBuildTaskIndexConfig(t, resource),
		}
		mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").Return(task, nil)
		mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
			Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
		mockBTA.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return(nil, nil)
		mockRS.EXPECT().GetByID(gomock.Any(), "resource-1").Return(resource, nil)
		mockBTA.EXPECT().MarkPending(gomock.Any(), nil, "task-1", true).Return(true, nil)

		require.NoError(t, service.Start(context.Background(), "task-1", true))
		select {
		case <-service.DispatchSignal():
		default:
			t.Fatal("expected a dispatch signal after the reset was persisted")
		}
	})
	t.Run("rejects reset for incremental task", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		mockCS.EXPECT().CheckTaskPermission(gomock.Any(), "catalog-1", interfaces.OPERATION_TYPE_TASK_MANAGE).Return(nil)
		service := &buildTaskService{cs: mockCS, bta: mockBTA}

		mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").Return(&interfaces.BuildTask{
			ID:          "task-1",
			CatalogID:   "catalog-1",
			Status:      interfaces.BuildTaskStatusStopped,
			ExecuteType: interfaces.BuildTaskExecuteTypeIncremental,
		}, nil)

		err := service.Start(context.Background(), "task-1", true)
		httpErr := requireHTTPError(t, err, verrors.VegaBackend_BuildTask_IncrementalResetUnsupported)
		assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
	})
	t.Run("rejects incremental restart without resource baseline", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockRS := mock_interfaces.NewMockResourceService(ctrl)
		mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		mockCS.EXPECT().CheckTaskPermission(gomock.Any(), "catalog-1", interfaces.OPERATION_TYPE_TASK_MANAGE).Return(nil)
		service := &buildTaskService{cs: mockCS, rs: mockRS, bta: mockBTA}
		resource := buildTaskTestResource()

		mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").Return(&interfaces.BuildTask{
			ID:          "task-1",
			ResourceID:  "resource-1",
			CatalogID:   "catalog-1",
			Status:      interfaces.BuildTaskStatusStopped,
			ExecuteType: interfaces.BuildTaskExecuteTypeIncremental,
			IndexConfig: mustBuildTaskIndexConfig(t, resource),
		}, nil)
		mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
			Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
		mockBTA.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return(nil, nil)
		mockRS.EXPECT().GetByID(gomock.Any(), "resource-1").Return(resource, nil)

		err := service.Start(context.Background(), "task-1", false)
		requireHTTPError(t, err, verrors.VegaBackend_BuildTask_IncrementalBaselineUnavailable)
	})

	t.Run("returns conflict when status changes before start update", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockRS := mock_interfaces.NewMockResourceService(ctrl)
		mockCS.EXPECT().CheckTaskPermission(gomock.Any(), "catalog-1", interfaces.OPERATION_TYPE_TASK_MANAGE).Return(nil)
		mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		service := &buildTaskService{cs: mockCS, rs: mockRS, bta: mockBTA}

		resource := buildTaskTestResource()
		task := &interfaces.BuildTask{
			ID: "task-1", ResourceID: "resource-1", CatalogID: "catalog-1",
			Status:      interfaces.BuildTaskStatusStopped,
			IndexConfig: mustBuildTaskIndexConfig(t, resource),
		}
		mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").Return(task, nil)
		mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
			Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
		mockBTA.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return(nil, nil)
		mockRS.EXPECT().GetByID(gomock.Any(), "resource-1").Return(resource, nil)
		mockBTA.EXPECT().MarkPending(gomock.Any(), nil, "task-1", false).Return(false, nil)

		err := service.Start(context.Background(), "task-1", false)
		httpErr := requireHTTPError(t, err, verrors.VegaBackend_BuildTask_InvalidStateTransition)
		assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
	})

	t.Run("rejects disabled catalog", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		mockRS := mock_interfaces.NewMockResourceService(ctrl)
		mockCS.EXPECT().CheckTaskPermission(gomock.Any(), "catalog-1", interfaces.OPERATION_TYPE_TASK_MANAGE).Return(nil)
		service := &buildTaskService{cs: mockCS, bta: mockBTA, rs: mockRS}

		mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").
			Return(&interfaces.BuildTask{
				ID:        "task-1",
				CatalogID: "catalog-1",
				Status:    interfaces.BuildTaskStatusStopped,
			}, nil)
		mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
			Return(&interfaces.Catalog{ID: "catalog-1", Enabled: false}, nil)

		err := service.Start(context.Background(), "task-1", false)
		assertCatalogDisabledError(t, err)
	})
	t.Run("allows failed status", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		mockRS := mock_interfaces.NewMockResourceService(ctrl)
		mockCS.EXPECT().CheckTaskPermission(gomock.Any(), "catalog-1", interfaces.OPERATION_TYPE_TASK_MANAGE).Return(nil)
		service := &buildTaskService{cs: mockCS, bta: mockBTA, rs: mockRS}

		mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").
			Return(&interfaces.BuildTask{
				ID:        "task-1",
				CatalogID: "catalog-1",
				Status:    interfaces.BuildTaskStatusFailed,
			}, nil)
		mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
			Return(&interfaces.Catalog{ID: "catalog-1", Enabled: false}, nil)

		err := service.Start(context.Background(), "task-1", false)
		assertCatalogDisabledError(t, err)
	})
	for _, status := range []string{interfaces.BuildTaskStatusPending, interfaces.BuildTaskStatusCompleted} {
		t.Run("rejects "+status+" status", func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
			mockRSAuth := mock_interfaces.NewMockResourceService(ctrl)
			mockCSAuth := mock_interfaces.NewMockCatalogService(ctrl)
			mockCSAuth.EXPECT().CheckTaskPermission(gomock.Any(), "catalog-1", interfaces.OPERATION_TYPE_TASK_MANAGE).Return(nil)
			service := &buildTaskService{bta: mockBTA, rs: mockRSAuth, cs: mockCSAuth}

			mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").
				Return(&interfaces.BuildTask{ID: "task-1", CatalogID: "catalog-1", Status: status}, nil)

			err := service.Start(context.Background(), "task-1", false)
			requireHTTPError(t, err, verrors.VegaBackend_BuildTask_InvalidStateTransition)
		})
	}
	t.Run("rejects another active task for resource", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		mockRS := mock_interfaces.NewMockResourceService(ctrl)
		mockCS.EXPECT().CheckTaskPermission(gomock.Any(), "catalog-1", interfaces.OPERATION_TYPE_TASK_MANAGE).Return(nil)
		service := &buildTaskService{cs: mockCS, bta: mockBTA, rs: mockRS}

		mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").
			Return(&interfaces.BuildTask{
				ID:         "task-1",
				ResourceID: "resource-1",
				CatalogID:  "catalog-1",
				Status:     interfaces.BuildTaskStatusStopped,
			}, nil)
		mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
			Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
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
		requireHTTPError(t, err, verrors.VegaBackend_BuildTask_Exist)
	})
	t.Run("rejects changed index config", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockRS := mock_interfaces.NewMockResourceService(ctrl)
		mockCS.EXPECT().CheckTaskPermission(gomock.Any(), "catalog-1", interfaces.OPERATION_TYPE_TASK_MANAGE).Return(nil)
		mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		service := &buildTaskService{cs: mockCS, rs: mockRS, bta: mockBTA}
		originalResource := buildTaskTestResource()
		currentResource := &interfaces.Resource{
			ID:          "resource-1",
			CatalogID:   "catalog-1",
			IndexConfig: &interfaces.ResourceIndexConfig{BuildKeyFields: []string{"updated_at"}},
			SchemaDefinition: []*interfaces.Property{
				{Name: "updated_at", Type: interfaces.DataType_Timestamp},
			},
		}

		mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").Return(&interfaces.BuildTask{
			ID:          "task-1",
			ResourceID:  "resource-1",
			CatalogID:   "catalog-1",
			Status:      interfaces.BuildTaskStatusStopped,
			IndexConfig: mustBuildTaskIndexConfig(t, originalResource),
		}, nil)
		mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
			Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
		mockBTA.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return(nil, nil)
		mockRS.EXPECT().GetByID(gomock.Any(), "resource-1").Return(currentResource, nil)

		err := service.Start(context.Background(), "task-1", false)
		requireHTTPError(t, err, verrors.VegaBackend_BuildTask_IndexConfigChanged)
	})
	t.Run("restart does not depend on newer completed tasks", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockRS := mock_interfaces.NewMockResourceService(ctrl)
		mockCS.EXPECT().CheckTaskPermission(gomock.Any(), "catalog-1", interfaces.OPERATION_TYPE_TASK_MANAGE).Return(nil)
		mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		service := &buildTaskService{cs: mockCS, rs: mockRS, bta: mockBTA}
		resource := buildTaskTestResource()

		task := &interfaces.BuildTask{
			ID:          "task-1",
			ResourceID:  "resource-1",
			CatalogID:   "catalog-1",
			Status:      interfaces.BuildTaskStatusStopped,
			CreateTime:  100,
			IndexConfig: mustBuildTaskIndexConfig(t, resource),
		}
		mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").Return(task, nil)
		mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
			Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
		mockBTA.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return(nil, nil)
		mockRS.EXPECT().GetByID(gomock.Any(), "resource-1").Return(resource, nil)
		mockBTA.EXPECT().MarkPending(gomock.Any(), nil, "task-1", false).Return(true, nil)

		require.NoError(t, service.Start(context.Background(), "task-1", false))
	})
	t.Run("rejects restart when the resource contains an unsupported field", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockRS := mock_interfaces.NewMockResourceService(ctrl)
		mockCS.EXPECT().CheckTaskPermission(gomock.Any(), "catalog-1", interfaces.OPERATION_TYPE_TASK_MANAGE).Return(nil)
		mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		service := &buildTaskService{cs: mockCS, rs: mockRS, bta: mockBTA}

		mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").Return(&interfaces.BuildTask{
			ID:         "task-1",
			ResourceID: "resource-1",
			CatalogID:  "catalog-1",
			Status:     interfaces.BuildTaskStatusStopped,
			IndexConfig: &interfaces.BuildTaskIndexConfig{
				IndexConfigContract: interfaces.IndexConfigContract{BuildKeyFields: []string{"id"}},
				Features:            map[string]interfaces.BuildTaskFieldIndexFeature{},
			},
		}, nil)
		mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
			Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
		mockBTA.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return(nil, nil)
		mockRS.EXPECT().GetByID(gomock.Any(), "resource-1").Return(&interfaces.Resource{
			ID:        "resource-1",
			CatalogID: "catalog-1",
			IndexConfig: &interfaces.ResourceIndexConfig{
				BuildKeyFields: []string{"id"},
			},
			SchemaDefinition: []*interfaces.Property{
				{Name: "id", Type: interfaces.DataType_Integer},
				{Name: "interests", Type: interfaces.DataType_Other, OriginalType: "_text"},
			},
		}, nil)

		err := service.Start(context.Background(), "task-1", false)
		httpErr := requireHTTPError(t, err, verrors.VegaBackend_BuildTask_InvalidParameter_UnsupportedSchemaFields)
		assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
		assert.Contains(t, httpErr.BaseError.ErrorDetails, "interests (original_type: _text)")
	})
	t.Run("rejects unavailable analyzer before updating status or dispatching", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockRS := mock_interfaces.NewMockResourceService(ctrl)
		mockCS.EXPECT().CheckTaskPermission(gomock.Any(), "catalog-1", interfaces.OPERATION_TYPE_TASK_MANAGE).Return(nil)
		mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		validator := &analyzerValidatingIndexManager{}
		service := &buildTaskService{
			bta: mockBTA,
			cs:  mockCS,
			lim: validator,
			rs:  mockRS,
		}
		resource := &interfaces.Resource{
			ID:          "resource-1",
			CatalogID:   "catalog-1",
			IndexConfig: &interfaces.ResourceIndexConfig{BuildKeyFields: []string{"id"}},
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
			IndexConfig: func() *interfaces.BuildTaskIndexConfig {
				config := mustBuildTaskIndexConfig(t, resource)
				config.Features = map[string]interfaces.BuildTaskFieldIndexFeature{
					"status": {Fulltext: &interfaces.BuildTaskFulltextConfig{Analyzer: "hanlp_index"}},
				}
				return config
			}(),
		}
		mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").Return(task, nil)
		mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
			Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
		mockBTA.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return(nil, nil)
		mockRS.EXPECT().GetByID(gomock.Any(), "resource-1").Return(resource, nil)

		err := service.Start(context.Background(), "task-1", false)
		httpErr := requireHTTPError(t, err, verrors.VegaBackend_BuildTask_InvalidParameter_Analyzer)
		assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
		assert.Contains(t, httpErr.BaseError.ErrorDetails, "unavailable")
		assert.Equal(t, []string{"hanlp_index"}, validator.captured)
	})
}

func assertCatalogDisabledError(t *testing.T, err error) {
	t.Helper()
	httpErr := requireHTTPError(t, err, verrors.VegaBackend_Catalog_IsDisabled)
	assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
}

func requireHTTPError(t *testing.T, err error, wantErrorCode string) *rest.HTTPError {
	t.Helper()
	require.Error(t, err)
	httpErr, ok := err.(*rest.HTTPError)
	require.Truef(t, ok, "expected HTTPError, got %T", err)
	assert.Equal(t, wantErrorCode, httpErr.BaseError.ErrorCode)
	return httpErr
}

func buildTaskTestResource() *interfaces.Resource {
	return &interfaces.Resource{
		ID:          "resource-1",
		CatalogID:   "catalog-1",
		Category:    interfaces.ResourceCategoryTable,
		IndexConfig: &interfaces.ResourceIndexConfig{BuildKeyFields: []string{"id"}},
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
		config.IndexConfigContract.BuildKeyFields = append([]string(nil), resource.IndexConfig.BuildKeyFields...)
	}
	return config
}

func TestBuildTaskServiceStop(t *testing.T) {
	t.Run("running to stopping", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		mockRSAuth := mock_interfaces.NewMockResourceService(ctrl)
		mockCSAuth := mock_interfaces.NewMockCatalogService(ctrl)
		mockCSAuth.EXPECT().CheckTaskPermission(gomock.Any(), "catalog-1", interfaces.OPERATION_TYPE_TASK_MANAGE).Return(nil)
		service := &buildTaskService{bta: mockBTA, rs: mockRSAuth, cs: mockCSAuth}

		mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").
			Return(&interfaces.BuildTask{ID: "task-1", CatalogID: "catalog-1", Status: interfaces.BuildTaskStatusRunning}, nil)
		mockBTA.EXPECT().MarkStopping(gomock.Any(), nil, "task-1").Return(true, nil)

		require.NoError(t, service.Stop(context.Background(), "task-1"))
	})
	t.Run("pending to stopped", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		mockRSAuth := mock_interfaces.NewMockResourceService(ctrl)
		mockCSAuth := mock_interfaces.NewMockCatalogService(ctrl)
		mockCSAuth.EXPECT().CheckTaskPermission(gomock.Any(), "catalog-1", interfaces.OPERATION_TYPE_TASK_MANAGE).Return(nil)
		service := &buildTaskService{bta: mockBTA, rs: mockRSAuth, cs: mockCSAuth}

		mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").
			Return(&interfaces.BuildTask{ID: "task-1", CatalogID: "catalog-1", Status: interfaces.BuildTaskStatusPending}, nil)
		mockBTA.EXPECT().MarkStopped(gomock.Any(), nil, "task-1", gomock.Any()).Return(true, nil)

		require.NoError(t, service.Stop(context.Background(), "task-1"))
	})
	t.Run("returns conflict when status changes before stop update", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		mockRSAuth := mock_interfaces.NewMockResourceService(ctrl)
		mockCSAuth := mock_interfaces.NewMockCatalogService(ctrl)
		mockCSAuth.EXPECT().CheckTaskPermission(gomock.Any(), "catalog-1", interfaces.OPERATION_TYPE_TASK_MANAGE).Return(nil)
		service := &buildTaskService{bta: mockBTA, rs: mockRSAuth, cs: mockCSAuth}

		mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").
			Return(&interfaces.BuildTask{ID: "task-1", CatalogID: "catalog-1", Status: interfaces.BuildTaskStatusPending}, nil)
		mockBTA.EXPECT().MarkStopped(gomock.Any(), nil, "task-1", gomock.Any()).Return(false, nil)

		err := service.Stop(context.Background(), "task-1")
		httpErr := requireHTTPError(t, err, verrors.VegaBackend_BuildTask_InvalidStateTransition)
		assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
	})
	for _, status := range []string{interfaces.BuildTaskStatusStopping, interfaces.BuildTaskStatusStopped} {
		t.Run("rejects "+status+" status", func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
			mockRSAuth := mock_interfaces.NewMockResourceService(ctrl)
			mockCSAuth := mock_interfaces.NewMockCatalogService(ctrl)
			mockCSAuth.EXPECT().CheckTaskPermission(gomock.Any(), "catalog-1", interfaces.OPERATION_TYPE_TASK_MANAGE).Return(nil)
			service := &buildTaskService{bta: mockBTA, rs: mockRSAuth, cs: mockCSAuth}

			mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").
				Return(&interfaces.BuildTask{ID: "task-1", CatalogID: "catalog-1", Status: status}, nil)

			err := service.Stop(context.Background(), "task-1")
			requireHTTPError(t, err, verrors.VegaBackend_BuildTask_InvalidStateTransition)
		})
	}
}

func TestBuildTaskServiceDeleteByIDs(t *testing.T) {
	for _, status := range []string{
		interfaces.BuildTaskStatusCompleted,
		interfaces.BuildTaskStatusFailed,
		interfaces.BuildTaskStatusStopped,
	} {
		t.Run("deletes "+status+" task without resource or index access", func(t *testing.T) {
			ctrl := gomock.NewController(t)
			bta := mock_interfaces.NewMockBuildTaskAccess(ctrl)
			cs := mock_interfaces.NewMockCatalogService(ctrl)
			rs := mock_interfaces.NewMockResourceService(ctrl)
			lim := mock_interfaces.NewMockLocalIndexManager(ctrl)
			service := &buildTaskService{bta: bta, cs: cs, rs: rs, lim: lim}

			bta.EXPECT().GetByIDs(gomock.Any(), []string{"t1"}).Return(map[string]*interfaces.BuildTask{
				"t1": {ID: "t1", CatalogID: "catalog-1", ResourceID: "r1", Status: status},
			}, nil)
			cs.EXPECT().CheckTaskPermission(gomock.Any(), "catalog-1", interfaces.OPERATION_TYPE_TASK_MANAGE).Return(nil)
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
			mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
			mockCS := mock_interfaces.NewMockCatalogService(ctrl)
			mockCS.EXPECT().CheckTaskPermission(gomock.Any(), "catalog-1", interfaces.OPERATION_TYPE_TASK_MANAGE).Return(nil)
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
		bta := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		svc := &buildTaskService{bta: bta}
		bta.EXPECT().GetByIDs(gomock.Any(), []string{"t1"}).Return(nil, errors.New("database unavailable"))

		requireHTTPError(t, svc.DeleteByIDs(context.Background(), []string{"t1"}, false), verrors.VegaBackend_BuildTask_InternalError_GetFailed)
	})

	t.Run("reports missing ids unless ignored", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bta := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		svc := &buildTaskService{bta: bta}
		bta.EXPECT().GetByIDs(gomock.Any(), []string{"missing"}).Return(map[string]*interfaces.BuildTask{}, nil)

		httpErr := requireHTTPError(t, svc.DeleteByIDs(context.Background(), []string{"missing"}, false), verrors.VegaBackend_BuildTask_NotFound)
		assert.Equal(t, map[string]any{"missing_ids": []string{"missing"}}, httpErr.BaseError.ErrorDetails)
	})

	t.Run("ignores missing ids and deletes returned terminal tasks", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bta := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		cs := mock_interfaces.NewMockCatalogService(ctrl)
		svc := &buildTaskService{bta: bta, cs: cs}
		bta.EXPECT().GetByIDs(gomock.Any(), []string{"t1", "missing"}).Return(map[string]*interfaces.BuildTask{
			"t1": {ID: "t1", CatalogID: "c1", Status: interfaces.BuildTaskStatusCompleted},
		}, nil)
		cs.EXPECT().CheckTaskPermission(gomock.Any(), "c1", interfaces.OPERATION_TYPE_TASK_MANAGE).Return(nil)
		bta.EXPECT().DeleteByIDs(gomock.Any(), []string{"t1"}).Return(int64(1), nil)

		require.NoError(t, svc.DeleteByIDs(context.Background(), []string{"t1", "missing"}, true))
	})

	t.Run("reports active ids before missing ids", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bta := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		cs := mock_interfaces.NewMockCatalogService(ctrl)
		svc := &buildTaskService{bta: bta, cs: cs}
		bta.EXPECT().GetByIDs(gomock.Any(), []string{"active", "missing"}).Return(map[string]*interfaces.BuildTask{
			"active": {ID: "active", CatalogID: "c1", Status: interfaces.BuildTaskStatusRunning},
		}, nil)
		cs.EXPECT().CheckTaskPermission(gomock.Any(), "c1", interfaces.OPERATION_TYPE_TASK_MANAGE).Return(nil)

		httpErr := requireHTTPError(t, svc.DeleteByIDs(context.Background(), []string{"active", "missing"}, false), verrors.VegaBackend_BuildTask_HasRunningExecution)
		assert.Equal(t, map[string]any{"active_ids": []string{"active"}}, httpErr.BaseError.ErrorDetails)
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
			bta := mock_interfaces.NewMockBuildTaskAccess(ctrl)
			cs := mock_interfaces.NewMockCatalogService(ctrl)
			svc := &buildTaskService{bta: bta, cs: cs}
			bta.EXPECT().GetByIDs(gomock.Any(), []string{"t1"}).Return(map[string]*interfaces.BuildTask{
				"t1": {ID: "t1", CatalogID: "c1", Status: interfaces.BuildTaskStatusCompleted},
			}, nil)
			cs.EXPECT().CheckTaskPermission(gomock.Any(), "c1", interfaces.OPERATION_TYPE_TASK_MANAGE).Return(nil)
			bta.EXPECT().DeleteByIDs(gomock.Any(), []string{"t1"}).Return(tt.result, tt.err)

			requireHTTPError(t, svc.DeleteByIDs(context.Background(), []string{"t1"}, false), verrors.VegaBackend_BuildTask_InternalError_DeleteFailed)
		})
	}
}

func TestBuildTaskServiceInternalGetByID(t *testing.T) {
	t.Run("delegates lookup", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bta := mock_interfaces.NewMockBuildTaskAccess(ctrl)
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
		bta := mock_interfaces.NewMockBuildTaskAccess(ctrl)
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
		bta := mock_interfaces.NewMockBuildTaskAccess(ctrl)
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
		bta := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		svc := &buildTaskService{bta: bta}
		bta.EXPECT().GetStatusByID(gomock.Any(), "t1").Return(interfaces.BuildTaskStatusRunning, nil)

		status, err := svc.InternalGetStatusByID(context.Background(), "t1")
		require.NoError(t, err)
		assert.Equal(t, interfaces.BuildTaskStatusRunning, status)
	})
}
