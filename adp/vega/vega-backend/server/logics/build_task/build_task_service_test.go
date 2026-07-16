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

func TestBuildTaskServiceCreateBuildTask(t *testing.T) {
	t.Run("rejects disabled catalog", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockRS := mock_interfaces.NewMockResourceService(ctrl)
		service := &buildTaskService{debugTaskQueue: make(chan *asynq.Task, 10), cs: mockCS, rs: mockRS}

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
	})
	t.Run("rejects active task for resource", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockRS := mock_interfaces.NewMockResourceService(ctrl)
		mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		service := &buildTaskService{debugTaskQueue: make(chan *asynq.Task, 10), cs: mockCS, rs: mockRS, bta: mockBTA}

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

		requireHTTPError(t, err, verrors.VegaBackend_BuildTask_Exist)
	})
	t.Run("rejects execute type for streaming", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockRS := mock_interfaces.NewMockResourceService(ctrl)
		service := &buildTaskService{debugTaskQueue: make(chan *asynq.Task, 10), rs: mockRS}

		mockRS.EXPECT().GetByID(gomock.Any(), "resource-1").
			Return(&interfaces.Resource{
				ID:        "resource-1",
				CatalogID: "catalog-1",
				Category:  interfaces.ResourceCategoryTable,
			}, nil)

		_, err := service.CreateBuildTask(context.Background(), &interfaces.CreateBuildTaskRequest{
			ResourceID:  "resource-1",
			Mode:        interfaces.BuildTaskModeStreaming,
			ExecuteType: interfaces.BuildTaskExecuteTypeFull,
		})

		requireHTTPError(t, err, verrors.VegaBackend_BuildTask_InvalidExecuteType)
	})
	t.Run("normalizes model name to id", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockRS := mock_interfaces.NewMockResourceService(ctrl)
		mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		mockMFS := mock_interfaces.NewMockModelFactoryService(ctrl)
		neutralizeEnqueue(t, ctrl)
		service := &buildTaskService{debugTaskQueue: make(chan *asynq.Task, 10), cs: mockCS, rs: mockRS, bta: mockBTA, mfs: mockMFS}

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
					require.Equal(t, "resource-1", params.ResourceID)
				}
				return nil, 0, nil
			})
		mockMFS.EXPECT().GetModelByName(gomock.Any(), "text-embedding-v4").
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
		require.NoError(t, err)
		require.NotNil(t, captured)
		require.NotNil(t, captured.IndexConfig)
		assert.Equal(t, []string{"id"}, captured.IndexConfig.BuildKeyFields)
		assert.Equal(t, &interfaces.BuildTaskEmbeddingConfig{ModelID: "2064382281006583808", Dimensions: 1024}, captured.IndexConfig.Features["family_name"].Vector)
		assert.Equal(t, &interfaces.BuildTaskEmbeddingConfig{ModelID: "2064382281006583808", Dimensions: 1024}, captured.IndexConfig.Features["given_name"].Vector)
		assert.Equal(t, &interfaces.BuildTaskFulltextConfig{Analyzer: "ik_max_word"}, captured.IndexConfig.Features["family_name"].Fulltext)
	})
	t.Run("snapshot unaffected by resource mutation", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockRS := mock_interfaces.NewMockResourceService(ctrl)
		mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		mockMFS := mock_interfaces.NewMockModelFactoryService(ctrl)
		neutralizeEnqueue(t, ctrl)
		service := &buildTaskService{debugTaskQueue: make(chan *asynq.Task, 10), cs: mockCS, rs: mockRS, bta: mockBTA, mfs: mockMFS}

		resource := &interfaces.Resource{
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
			},
		}
		mockRS.EXPECT().GetByID(gomock.Any(), "resource-1").Return(resource, nil)
		mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
			Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
		mockBTA.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, int64(0), nil)
		mockMFS.EXPECT().GetModelByName(gomock.Any(), "text-embedding-v4").
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
		require.NoError(t, err)
		require.NotNil(t, captured)
		require.NotNil(t, captured.IndexConfig)

		resource.IndexConfig.BuildKeyFields[0] = "changed"
		resource.IndexConfig.DefaultEmbeddingModel = "changed-model"
		resource.SchemaDefinition[0].Features = nil

		assert.Equal(t, []string{"id"}, captured.IndexConfig.BuildKeyFields)
		assert.Equal(t, &interfaces.BuildTaskEmbeddingConfig{ModelID: "2064382281006583808", Dimensions: 1024}, captured.IndexConfig.Features["family_name"].Vector)
		assert.Equal(t, &interfaces.BuildTaskFulltextConfig{Analyzer: "ik_max_word"}, captured.IndexConfig.Features["family_name"].Fulltext)
	})
	t.Run("uses feature embedding model override", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockRS := mock_interfaces.NewMockResourceService(ctrl)
		mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		mockMFS := mock_interfaces.NewMockModelFactoryService(ctrl)
		neutralizeEnqueue(t, ctrl)
		service := &buildTaskService{debugTaskQueue: make(chan *asynq.Task, 10), cs: mockCS, rs: mockRS, bta: mockBTA, mfs: mockMFS}

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
					require.Equal(t, "resource-1", params.ResourceID)
				}
				return nil, 0, nil
			})
		mockMFS.EXPECT().GetModelByName(gomock.Any(), "text-embedding-v4").
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
		require.NoError(t, err)
		require.NotNil(t, captured)
		require.NotNil(t, captured.IndexConfig)
		assert.Equal(t, &interfaces.BuildTaskEmbeddingConfig{ModelID: "2064382281006583808", Dimensions: 1024}, captured.IndexConfig.Features["family_name"].Vector)
	})
	t.Run("keeps per field analyzer and embedding model overrides", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockRS := mock_interfaces.NewMockResourceService(ctrl)
		mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		mockMFS := mock_interfaces.NewMockModelFactoryService(ctrl)
		neutralizeEnqueue(t, ctrl)
		service := &buildTaskService{debugTaskQueue: make(chan *asynq.Task, 10), cs: mockCS, rs: mockRS, bta: mockBTA, mfs: mockMFS}

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
					{
						Name: "title",
						Features: []interfaces.PropertyFeature{
							{
								FeatureType: interfaces.PropertyFeatureType_Vector,
								Config:      map[string]any{"embedding_model": "model-a"},
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
								Config:      map[string]any{"embedding_model": "model-b"},
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
		mockBTA.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, int64(0), nil)
		mockMFS.EXPECT().GetModelByName(gomock.Any(), "model-a").
			Return(&interfaces.SmallModel{ModelID: "model-a-id", ModelName: "model-a", EmbeddingDim: 768}, nil)
		mockMFS.EXPECT().GetModelByName(gomock.Any(), "model-b").
			Return(&interfaces.SmallModel{ModelID: "model-b-id", ModelName: "model-b", EmbeddingDim: 1024}, nil)

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

		require.NoError(t, err)
		require.NotNil(t, captured)
		require.NotNil(t, captured.IndexConfig)
		assert.Equal(t, []string{"id"}, captured.IndexConfig.BuildKeyFields)
		assert.Equal(t, &interfaces.BuildTaskEmbeddingConfig{ModelID: "model-a-id", Dimensions: 768}, captured.IndexConfig.Features["title"].Vector)
		assert.Equal(t, &interfaces.BuildTaskEmbeddingConfig{ModelID: "model-b-id", Dimensions: 1024}, captured.IndexConfig.Features["body"].Vector)
		assert.Equal(t, &interfaces.BuildTaskFulltextConfig{Analyzer: "ik_max_word"}, captured.IndexConfig.Features["title"].Fulltext)
		assert.Equal(t, &interfaces.BuildTaskFulltextConfig{Analyzer: "standard"}, captured.IndexConfig.Features["body"].Fulltext)
	})
	t.Run("errors when model unresolvable and no dimensions", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockRS := mock_interfaces.NewMockResourceService(ctrl)
		mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		mockMFS := mock_interfaces.NewMockModelFactoryService(ctrl)
		service := &buildTaskService{debugTaskQueue: make(chan *asynq.Task, 10), cs: mockCS, rs: mockRS, bta: mockBTA, mfs: mockMFS}

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
					require.Equal(t, "resource-1", params.ResourceID)
				}
				return nil, 0, nil
			})
		mockMFS.EXPECT().GetModelByName(gomock.Any(), "bogus-model").
			Return(nil, fmt.Errorf("model not found"))

		_, err := service.CreateBuildTask(context.Background(), &interfaces.CreateBuildTaskRequest{
			ResourceID: "resource-1",
			Mode:       interfaces.BuildTaskModeBatch,
		})
		httpErr := requireHTTPError(t, err, verrors.VegaBackend_BuildTask_InternalError_CreateFailed)
		assert.Equal(t, http.StatusInternalServerError, httpErr.HTTPCode)
	})
}

func TestBuildTaskServiceEnqueueBuildTaskDebugMode(t *testing.T) {
	t.Setenv("DEBUG_MODE", "true")

	service := &buildTaskService{debugTaskQueue: make(chan *asynq.Task, 1)}
	drainDebugBuildTaskQueue(service.DebugTaskQueue())
	t.Cleanup(func() { drainDebugBuildTaskQueue(service.DebugTaskQueue()) })

	require.NoError(t, service.enqueueTask(context.Background(), &interfaces.BuildTask{
		ID:   "build-task-1",
		Mode: interfaces.BuildTaskModeBatch,
	}, interfaces.BuildTaskExecuteTypeIncremental))

	select {
	case task := <-service.DebugTaskQueue():
		assert.Equal(t, interfaces.BuildTaskTypeBatch, task.Type())
	default:
		t.Fatal("expected debug build task to be enqueued")
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
}

func TestBuildTaskServiceStartBuildTask(t *testing.T) {
	t.Run("rejects disabled catalog", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		service := &buildTaskService{debugTaskQueue: make(chan *asynq.Task, 10), cs: mockCS, bta: mockBTA}

		mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").
			Return(&interfaces.BuildTask{
				ID:        "task-1",
				CatalogID: "catalog-1",
				Status:    interfaces.BuildTaskStatusInit,
			}, nil)
		mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
			Return(&interfaces.Catalog{ID: "catalog-1", Enabled: false}, nil)

		err := service.StartBuildTask(context.Background(), "task-1", false)
		assertCatalogDisabledError(t, err)
	})
	t.Run("allows failed status", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		service := &buildTaskService{debugTaskQueue: make(chan *asynq.Task, 10), cs: mockCS, bta: mockBTA}

		mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").
			Return(&interfaces.BuildTask{
				ID:        "task-1",
				CatalogID: "catalog-1",
				Status:    interfaces.BuildTaskStatusFailed,
			}, nil)
		mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
			Return(&interfaces.Catalog{ID: "catalog-1", Enabled: false}, nil)

		err := service.StartBuildTask(context.Background(), "task-1", false)
		assertCatalogDisabledError(t, err)
	})
	t.Run("allows reset for completed task", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockRS := mock_interfaces.NewMockResourceService(ctrl)
		mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		neutralizeEnqueue(t, ctrl)
		service := &buildTaskService{debugTaskQueue: make(chan *asynq.Task, 10), cs: mockCS, rs: mockRS, bta: mockBTA}

		task := &interfaces.BuildTask{
			ID:         "task-1",
			ResourceID: "resource-1",
			CatalogID:  "catalog-1",
			Mode:       interfaces.BuildTaskModeBatch,
			Status:     interfaces.BuildTaskStatusCompleted,
			CreateTime: 100,
		}
		mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").Return(task, nil)
		mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
			Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
		mockBTA.EXPECT().List(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, params interfaces.BuildTasksQueryParams) ([]*interfaces.BuildTask, int64, error) {
				if len(params.Statuses) == 1 && params.Statuses[0] == interfaces.BuildTaskStatusCompleted {
					return []*interfaces.BuildTask{task}, int64(1), nil
				}
				return nil, int64(0), nil
			}).Times(2)
		mockRS.EXPECT().GetByID(gomock.Any(), "resource-1").Return(&interfaces.Resource{
			ID:        "resource-1",
			CatalogID: "catalog-1",
		}, nil)
		mockBTA.EXPECT().UpdateStatus(gomock.Any(), nil, "task-1",
			interfaces.NewBuildTaskUpdate().WithStatus(interfaces.BuildTaskStatusInit)).Return(true, nil)

		require.NoError(t, service.StartBuildTask(context.Background(), "task-1", true))
	})
	t.Run("rejects another active task for resource", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		service := &buildTaskService{debugTaskQueue: make(chan *asynq.Task, 10), cs: mockCS, bta: mockBTA}

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
					require.Equal(t, "resource-1", params.ResourceID)
				}
				return []*interfaces.BuildTask{{
					ID:         "task-2",
					ResourceID: "resource-1",
					Status:     interfaces.BuildTaskStatusRunning,
				}}, 1, nil
			})

		err := service.StartBuildTask(context.Background(), "task-1", false)
		requireHTTPError(t, err, verrors.VegaBackend_BuildTask_Exist)
	})
	t.Run("rejects changed index config", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockRS := mock_interfaces.NewMockResourceService(ctrl)
		mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		service := &buildTaskService{debugTaskQueue: make(chan *asynq.Task, 10), cs: mockCS, rs: mockRS, bta: mockBTA}

		mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").Return(&interfaces.BuildTask{
			ID:         "task-1",
			ResourceID: "resource-1",
			CatalogID:  "catalog-1",
			Status:     interfaces.BuildTaskStatusStopped,
			IndexConfig: &interfaces.BuildTaskIndexConfig{
				BuildKeyFields: []string{"id"},
				Features:       map[string]interfaces.BuildTaskFieldIndexFeature{},
			},
		}, nil)
		mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
			Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
		mockBTA.EXPECT().List(gomock.Any(), gomock.Any()).Return(nil, int64(0), nil)
		mockRS.EXPECT().GetByID(gomock.Any(), "resource-1").Return(&interfaces.Resource{
			ID:        "resource-1",
			CatalogID: "catalog-1",
			IndexConfig: &interfaces.ResourceIndexConfig{
				BuildKeyFields: []string{"updated_at"},
			},
		}, nil)

		err := service.StartBuildTask(context.Background(), "task-1", false)
		requireHTTPError(t, err, verrors.VegaBackend_BuildTask_InvalidStateTransition)
	})
	t.Run("rejects newer completed task", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockRS := mock_interfaces.NewMockResourceService(ctrl)
		mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		service := &buildTaskService{debugTaskQueue: make(chan *asynq.Task, 10), cs: mockCS, rs: mockRS, bta: mockBTA}

		task := &interfaces.BuildTask{
			ID:         "task-1",
			ResourceID: "resource-1",
			CatalogID:  "catalog-1",
			Status:     interfaces.BuildTaskStatusStopped,
			CreateTime: 100,
			IndexConfig: &interfaces.BuildTaskIndexConfig{
				BuildKeyFields: []string{"id"},
				Features:       map[string]interfaces.BuildTaskFieldIndexFeature{},
			},
		}
		mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").Return(task, nil)
		mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
			Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
		mockBTA.EXPECT().List(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, params interfaces.BuildTasksQueryParams) ([]*interfaces.BuildTask, int64, error) {
				if len(params.Statuses) == 1 && params.Statuses[0] == interfaces.BuildTaskStatusCompleted {
					if params.OrderBy != interfaces.BuildTaskOrderByCreatedAt || params.Limit != 1 {
						require.Equal(t, interfaces.BuildTaskOrderByCreatedAt, params.OrderBy)
						require.Equal(t, 1, params.Limit)
					}
					return []*interfaces.BuildTask{{
						ID:         "task-2",
						ResourceID: "resource-1",
						Status:     interfaces.BuildTaskStatusCompleted,
						CreateTime: 200,
					}}, int64(1), nil
				}
				return nil, int64(0), nil
			}).Times(2)
		mockRS.EXPECT().GetByID(gomock.Any(), "resource-1").Return(&interfaces.Resource{
			ID:        "resource-1",
			CatalogID: "catalog-1",
			IndexConfig: &interfaces.ResourceIndexConfig{
				BuildKeyFields: []string{"id"},
			},
		}, nil)

		err := service.StartBuildTask(context.Background(), "task-1", false)
		requireHTTPError(t, err, verrors.VegaBackend_BuildTask_InvalidStateTransition)
	})
	t.Run("allows init task itself as active", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockRS := mock_interfaces.NewMockResourceService(ctrl)
		mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		neutralizeEnqueue(t, ctrl)
		service := &buildTaskService{debugTaskQueue: make(chan *asynq.Task, 10), cs: mockCS, rs: mockRS, bta: mockBTA}

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
			DoAndReturn(func(_ context.Context, params interfaces.BuildTasksQueryParams) ([]*interfaces.BuildTask, int64, error) {
				if len(params.Statuses) == 1 && params.Statuses[0] == interfaces.BuildTaskStatusCompleted {
					return nil, int64(0), nil
				}
				return []*interfaces.BuildTask{task}, int64(1), nil
			}).Times(2)
		mockRS.EXPECT().GetByID(gomock.Any(), "resource-1").Return(&interfaces.Resource{
			ID:        "resource-1",
			CatalogID: "catalog-1",
		}, nil)

		require.NoError(t, service.StartBuildTask(context.Background(), "task-1", false))
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

// failed 状态必须允许 start（否则失败任务只能删除重建）。
// 借 catalog-disabled 错误证明状态检查已放行：若 failed 被状态机拒绝，
// 错误将是 InvalidStateTransition 而非 Catalog_IsDisabled。
func TestBuildTaskServiceStopBuildTask(t *testing.T) {
	t.Run("running to stopping", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		service := &buildTaskService{debugTaskQueue: make(chan *asynq.Task, 10), bta: mockBTA}

		mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").
			Return(&interfaces.BuildTask{ID: "task-1", Status: interfaces.BuildTaskStatusRunning}, nil)
		mockBTA.EXPECT().UpdateStatus(gomock.Any(), nil, "task-1",
			interfaces.NewBuildTaskUpdate().WithStatus(interfaces.BuildTaskStatusStopping)).Return(true, nil)

		require.NoError(t, service.StopBuildTask(context.Background(), "task-1"))
	})
	t.Run("force finalizes stuck stopping", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		service := &buildTaskService{debugTaskQueue: make(chan *asynq.Task, 10), bta: mockBTA}

		mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").
			Return(&interfaces.BuildTask{ID: "task-1", Status: interfaces.BuildTaskStatusStopping}, nil)
		mockBTA.EXPECT().UpdateStatus(gomock.Any(), nil, "task-1",
			interfaces.NewBuildTaskUpdate().WithStatus(interfaces.BuildTaskStatusStopped)).Return(true, nil)

		require.NoError(t, service.StopBuildTask(context.Background(), "task-1"))
	})
	t.Run("rejects stopped status", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		service := &buildTaskService{debugTaskQueue: make(chan *asynq.Task, 10), bta: mockBTA}

		mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").
			Return(&interfaces.BuildTask{ID: "task-1", Status: interfaces.BuildTaskStatusStopped}, nil)

		err := service.StopBuildTask(context.Background(), "task-1")
		requireHTTPError(t, err, verrors.VegaBackend_BuildTask_InvalidStateTransition)
	})
}

// running → stopping：正常停止路径。
// stopping → stopped：worker 已不在时 stopping 永远不会被推进，
// 二次 stop 必须能强制落停，否则任务卡死无法删除。
// stopped 任务不可再 stop。
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
			bt:            interfaces.BuildTask{Status: "completed", IndexConfig: buildTaskIndexConfig(true, true), SyncedCount: 6, VectorizedCount: 0},
			wantEmbedding: "failed", wantFulltext: "ok", wantUsable: false,
		},
		{
			name:          "embedding partial -> partial, unusable",
			bt:            interfaces.BuildTask{Status: "completed", IndexConfig: buildTaskIndexConfig(true, false), SyncedCount: 6, VectorizedCount: 4},
			wantEmbedding: "partial", wantFulltext: "none", wantUsable: false,
		},
		{
			name:          "embedding full -> ok, usable",
			bt:            interfaces.BuildTask{Status: "completed", IndexConfig: buildTaskIndexConfig(true, false), SyncedCount: 6, VectorizedCount: 6},
			wantEmbedding: "ok", wantFulltext: "none", wantUsable: true,
		},
		{
			name:          "no embedding requested -> none, usable",
			bt:            interfaces.BuildTask{Status: "completed", IndexConfig: buildTaskIndexConfig(false, true), SyncedCount: 6},
			wantEmbedding: "none", wantFulltext: "ok", wantUsable: true,
		},
		{
			name:          "running -> building, not usable yet",
			bt:            interfaces.BuildTask{Status: "running", IndexConfig: buildTaskIndexConfig(true, false), SyncedCount: 6, VectorizedCount: 2},
			wantEmbedding: "building", wantFulltext: "none", wantUsable: false,
		},
		{
			name:          "empty table -> ok, usable",
			bt:            interfaces.BuildTask{Status: "completed", IndexConfig: buildTaskIndexConfig(true, false), SyncedCount: 0, VectorizedCount: 0},
			wantEmbedding: "ok", wantFulltext: "none", wantUsable: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := computeIndexHealth(&c.bt)
			assert.Equal(t, c.wantEmbedding, h.Embedding)
			assert.Equal(t, c.wantFulltext, h.Fulltext)
			assert.Equal(t, c.wantUsable, h.Usable)
		})
	}
}

func buildTaskIndexConfig(vector bool, fulltext bool) *interfaces.BuildTaskIndexConfig {
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

func TestBuildTaskServiceDeleteBuildTasks(t *testing.T) {
	t.Run("drops index and row", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		mockRS := mock_interfaces.NewMockResourceService(ctrl)
		mockLIM := mock_interfaces.NewMockLocalIndexManager(ctrl)
		service := &buildTaskService{debugTaskQueue: make(chan *asynq.Task, 10), bta: mockBTA, rs: mockRS, lim: mockLIM}

		mockBTA.EXPECT().GetByID(gomock.Any(), "t1").
			Return(&interfaces.BuildTask{ID: "t1", ResourceID: "r1", Status: "completed"}, nil)
		mockRS.EXPECT().GetByID(gomock.Any(), "r1").
			Return(&interfaces.Resource{ID: "r1", LocalIndexName: interfaces.BuildIndexName("r1", "old-task")}, nil)
		mockLIM.EXPECT().DeleteIndex(gomock.Any(), interfaces.BuildIndexName("r1", "t1")).Return(nil)
		mockBTA.EXPECT().Delete(gomock.Any(), "t1").Return(nil)

		require.NoError(t, service.DeleteBuildTasks(context.Background(), []string{"t1"}, false, false))
	})
	t.Run("refuses active local index", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		mockRS := mock_interfaces.NewMockResourceService(ctrl)
		mockLIM := mock_interfaces.NewMockLocalIndexManager(ctrl)
		service := &buildTaskService{debugTaskQueue: make(chan *asynq.Task, 10), bta: mockBTA, rs: mockRS, lim: mockLIM}

		idx := interfaces.BuildIndexName("r1", "t1")
		mockBTA.EXPECT().GetByID(gomock.Any(), "t1").
			Return(&interfaces.BuildTask{ID: "t1", ResourceID: "r1", Status: interfaces.BuildTaskStatusCompleted}, nil)
		mockRS.EXPECT().GetByID(gomock.Any(), "r1").
			Return(&interfaces.Resource{ID: "r1", LocalIndexName: idx}, nil)
		// Active index conflicts must not delete either the index or the task row.

		err := service.DeleteBuildTasks(context.Background(), []string{"t1"}, false, false)
		httpErr := requireHTTPError(t, err, verrors.VegaBackend_BuildTask_ActiveIndexInUse)
		assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
	})
	t.Run("deletes active local index when explicitly allowed", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		mockRS := mock_interfaces.NewMockResourceService(ctrl)
		mockLIM := mock_interfaces.NewMockLocalIndexManager(ctrl)
		service := &buildTaskService{debugTaskQueue: make(chan *asynq.Task, 10), bta: mockBTA, rs: mockRS, lim: mockLIM}

		idx := interfaces.BuildIndexName("r1", "t1")
		resource := &interfaces.Resource{ID: "r1", LocalIndexName: idx}
		mockBTA.EXPECT().GetByID(gomock.Any(), "t1").
			Return(&interfaces.BuildTask{ID: "t1", ResourceID: "r1", Status: interfaces.BuildTaskStatusCompleted}, nil)
		mockRS.EXPECT().GetByID(gomock.Any(), "r1").Return(resource, nil)
		mockRS.EXPECT().UpdateResource(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, got *interfaces.Resource) error {
				if got.ID != "r1" {
					require.Equal(t, "r1", got.ID)
				}
				if got.LocalIndexName != "" {
					require.Empty(t, got.LocalIndexName)
				}
				return nil
			})
		mockLIM.EXPECT().DeleteIndex(gomock.Any(), idx).Return(nil)
		mockBTA.EXPECT().Delete(gomock.Any(), "t1").Return(nil)

		require.NoError(t, service.DeleteBuildTasks(context.Background(), []string{"t1"}, false, true))
	})
	t.Run("clear active local index failure blocks deletion", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		mockRS := mock_interfaces.NewMockResourceService(ctrl)
		mockLIM := mock_interfaces.NewMockLocalIndexManager(ctrl)
		service := &buildTaskService{debugTaskQueue: make(chan *asynq.Task, 10), bta: mockBTA, rs: mockRS, lim: mockLIM}

		idx := interfaces.BuildIndexName("r1", "t1")
		mockBTA.EXPECT().GetByID(gomock.Any(), "t1").
			Return(&interfaces.BuildTask{ID: "t1", ResourceID: "r1", Status: interfaces.BuildTaskStatusCompleted}, nil)
		mockRS.EXPECT().GetByID(gomock.Any(), "r1").
			Return(&interfaces.Resource{ID: "r1", LocalIndexName: idx}, nil)
		mockRS.EXPECT().UpdateResource(gomock.Any(), gomock.Any()).Return(errors.New("update failed"))
		// Clearing LocalIndexName failed, so the index and task row must remain untouched.

		err := service.DeleteBuildTasks(context.Background(), []string{"t1"}, false, true)
		httpErr := requireHTTPError(t, err, verrors.VegaBackend_Resource_InternalError_UpdateFailed)
		assert.Equal(t, http.StatusInternalServerError, httpErr.HTTPCode)
	})
	t.Run("allows orphan task when resource missing", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		mockRS := mock_interfaces.NewMockResourceService(ctrl)
		mockLIM := mock_interfaces.NewMockLocalIndexManager(ctrl)
		service := &buildTaskService{debugTaskQueue: make(chan *asynq.Task, 10), bta: mockBTA, rs: mockRS, lim: mockLIM}

		mockBTA.EXPECT().GetByID(gomock.Any(), "t1").
			Return(&interfaces.BuildTask{ID: "t1", ResourceID: "missing-resource", Status: interfaces.BuildTaskStatusFailed}, nil)
		mockRS.EXPECT().GetByID(gomock.Any(), "missing-resource").Return(nil, nil)
		mockLIM.EXPECT().DeleteIndex(gomock.Any(), interfaces.BuildIndexName("missing-resource", "t1")).Return(nil)
		mockBTA.EXPECT().Delete(gomock.Any(), "t1").Return(nil)

		require.NoError(t, service.DeleteBuildTasks(context.Background(), []string{"t1"}, false, false))
	})
	t.Run("resource lookup failure blocks deletion", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		mockRS := mock_interfaces.NewMockResourceService(ctrl)
		mockLIM := mock_interfaces.NewMockLocalIndexManager(ctrl)
		service := &buildTaskService{debugTaskQueue: make(chan *asynq.Task, 10), bta: mockBTA, rs: mockRS, lim: mockLIM}

		mockBTA.EXPECT().GetByID(gomock.Any(), "t1").
			Return(&interfaces.BuildTask{ID: "t1", ResourceID: "r1", Status: interfaces.BuildTaskStatusStopped}, nil)
		mockRS.EXPECT().GetByID(gomock.Any(), "r1").Return(nil, errors.New("db unavailable"))
		// If the guard cannot prove the index is safe to delete, deletion must not proceed.

		err := service.DeleteBuildTasks(context.Background(), []string{"t1"}, false, false)
		httpErr := requireHTTPError(t, err, verrors.VegaBackend_BuildTask_InternalError_GetFailed)
		assert.Equal(t, http.StatusInternalServerError, httpErr.HTTPCode)
	})
	t.Run("refuses running", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		mockLIM := mock_interfaces.NewMockLocalIndexManager(ctrl)
		service := &buildTaskService{debugTaskQueue: make(chan *asynq.Task, 10), bta: mockBTA, lim: mockLIM}

		mockBTA.EXPECT().GetByID(gomock.Any(), "t1").
			Return(&interfaces.BuildTask{ID: "t1", ResourceID: "r1", Status: "running"}, nil)
		// 不应调用 local index delete / bta.Delete

		require.Error(t, service.DeleteBuildTasks(context.Background(), []string{"t1"}, false, true))
	})
}

// 删任务应连带 drop 其 OpenSearch 索引（与删资源/删 catalog 级联语义一致）。
// 任一任务运行中 → 整批 409，索引/行都不删。
// neutralizeEnqueue 让 CreateBuildTask/StartBuildTask 末尾的 enqueueTask
// 不 panic：CreateClient 返回真实但指向不可达 redis 的 client，Enqueue 会失败，
// 而 enqueueTask 对入队失败仅记日志、不影响返回值。
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

func drainDebugBuildTaskQueue(queue <-chan *asynq.Task) {
	for {
		select {
		case <-queue:
		default:
			return
		}
	}
}
