// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package build_task

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/hibiken/asynq"
	"github.com/kweaver-ai/kweaver-go-lib/rest"
	"go.uber.org/mock/gomock"

	"vega-backend/interfaces"
	mock_interfaces "vega-backend/interfaces/mock"
	"vega-backend/logics"
)

// neutralizeEnqueue 让 CreateBuildTask/UpdateBuildTaskConfig 末尾的 enqueueBuildTask
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

// 传入模型名 → 落库归一为模型 ID，并按模型补全维度。
func TestCreateBuildTaskNormalizesModelNameToID(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockCS := mock_interfaces.NewMockCatalogService(ctrl)
	mockRA := mock_interfaces.NewMockResourceAccess(ctrl)
	mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
	mockMFA := mock_interfaces.NewMockModelFactoryAccess(ctrl)
	neutralizeEnqueue(t, ctrl)
	service := &buildTaskService{cs: mockCS, ra: mockRA, bta: mockBTA, mfa: mockMFA}

	mockRA.EXPECT().GetByID(gomock.Any(), "resource-1").
		Return(&interfaces.Resource{ID: "resource-1", CatalogID: "catalog-1", Category: interfaces.ResourceCategoryTable}, nil)
	mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
		Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
	mockBTA.EXPECT().GetByResourceID(gomock.Any(), "resource-1").Return(nil, nil)
	mockMFA.EXPECT().GetModelByName(gomock.Any(), "text-embedding-v4").
		Return(&interfaces.SmallModel{ModelID: "2064382281006583808", ModelName: "text-embedding-v4", EmbeddingDim: 1024}, nil)

	var captured *interfaces.BuildTask
	mockBTA.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, bt *interfaces.BuildTask) error {
			captured = bt
			return nil
		})

	_, err := service.CreateBuildTask(context.Background(), &interfaces.CreateBuildTaskRequest{
		ResourceID:      "resource-1",
		Mode:            interfaces.BuildTaskModeBatch,
		EmbeddingFields: "family_name,given_name",
		EmbeddingModel:  "text-embedding-v4",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if captured == nil {
		t.Fatal("bta.Create was not called")
	}
	if captured.EmbeddingModel != "2064382281006583808" {
		t.Fatalf("embedding_model not normalized to id: got %q", captured.EmbeddingModel)
	}
	if captured.ModelDimensions != 1024 {
		t.Fatalf("model_dimensions not filled from model: got %d", captured.ModelDimensions)
	}
}

// 传入已是模型 ID（按名查不到）且带维度 → 原样保留为 ID，不报错。
func TestCreateBuildTaskKeepsModelIDWhenNameLookupFails(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockCS := mock_interfaces.NewMockCatalogService(ctrl)
	mockRA := mock_interfaces.NewMockResourceAccess(ctrl)
	mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
	mockMFA := mock_interfaces.NewMockModelFactoryAccess(ctrl)
	neutralizeEnqueue(t, ctrl)
	service := &buildTaskService{cs: mockCS, ra: mockRA, bta: mockBTA, mfa: mockMFA}

	mockRA.EXPECT().GetByID(gomock.Any(), "resource-1").
		Return(&interfaces.Resource{ID: "resource-1", CatalogID: "catalog-1", Category: interfaces.ResourceCategoryTable}, nil)
	mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
		Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
	mockBTA.EXPECT().GetByResourceID(gomock.Any(), "resource-1").Return(nil, nil)
	mockMFA.EXPECT().GetModelByName(gomock.Any(), "2064382281006583808").
		Return(nil, fmt.Errorf("model not found"))

	var captured *interfaces.BuildTask
	mockBTA.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, bt *interfaces.BuildTask) error {
			captured = bt
			return nil
		})

	_, err := service.CreateBuildTask(context.Background(), &interfaces.CreateBuildTaskRequest{
		ResourceID:      "resource-1",
		Mode:            interfaces.BuildTaskModeBatch,
		EmbeddingFields: "family_name,given_name",
		EmbeddingModel:  "2064382281006583808",
		ModelDimensions: 1024,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if captured == nil {
		t.Fatal("bta.Create was not called")
	}
	if captured.EmbeddingModel != "2064382281006583808" {
		t.Fatalf("embedding_model id was altered: got %q", captured.EmbeddingModel)
	}
}

// 既解析不到（无效名/未知 id）又没给维度 → 报错，且不落库、不入队。
func TestCreateBuildTaskErrorsWhenModelUnresolvableAndNoDimensions(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockCS := mock_interfaces.NewMockCatalogService(ctrl)
	mockRA := mock_interfaces.NewMockResourceAccess(ctrl)
	mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
	mockMFA := mock_interfaces.NewMockModelFactoryAccess(ctrl)
	service := &buildTaskService{cs: mockCS, ra: mockRA, bta: mockBTA, mfa: mockMFA}

	mockRA.EXPECT().GetByID(gomock.Any(), "resource-1").
		Return(&interfaces.Resource{ID: "resource-1", CatalogID: "catalog-1", Category: interfaces.ResourceCategoryTable}, nil)
	mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
		Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
	mockBTA.EXPECT().GetByResourceID(gomock.Any(), "resource-1").Return(nil, nil)
	mockMFA.EXPECT().GetModelByName(gomock.Any(), "bogus-model").
		Return(nil, fmt.Errorf("model not found"))
	// bta.Create 不应被调用：未设置 EXPECT，gomock 会在被调用时失败。

	_, err := service.CreateBuildTask(context.Background(), &interfaces.CreateBuildTaskRequest{
		ResourceID:      "resource-1",
		Mode:            interfaces.BuildTaskModeBatch,
		EmbeddingFields: "family_name,given_name",
		EmbeddingModel:  "bogus-model",
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

// 编辑配置同样把模型名归一为 ID 写回。
func TestUpdateBuildTaskConfigNormalizesModelNameToID(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockCS := mock_interfaces.NewMockCatalogService(ctrl)
	mockBTA := mock_interfaces.NewMockBuildTaskAccess(ctrl)
	mockMFA := mock_interfaces.NewMockModelFactoryAccess(ctrl)
	neutralizeEnqueue(t, ctrl)
	service := &buildTaskService{cs: mockCS, bta: mockBTA, mfa: mockMFA}

	mockBTA.EXPECT().GetByID(gomock.Any(), "task-1").
		Return(&interfaces.BuildTask{ID: "task-1", CatalogID: "catalog-1", Mode: interfaces.BuildTaskModeBatch, Status: interfaces.BuildTaskStatusInit}, nil)
	mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", false).
		Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
	mockMFA.EXPECT().GetModelByName(gomock.Any(), "text-embedding-v4").
		Return(&interfaces.SmallModel{ModelID: "2064382281006583808", ModelName: "text-embedding-v4", EmbeddingDim: 1024}, nil)

	var captured map[string]any
	mockBTA.EXPECT().UpdateStatus(gomock.Any(), "task-1", gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, updates map[string]any) error {
			captured = updates
			return nil
		})

	err := service.UpdateBuildTaskConfig(context.Background(), "task-1", &interfaces.UpdateBuildTaskConfigRequest{
		EmbeddingFields: "family_name,given_name",
		EmbeddingModel:  "text-embedding-v4",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if captured["embeddingModel"] != "2064382281006583808" {
		t.Fatalf("embedding_model not normalized to id on update: got %v", captured["embeddingModel"])
	}
	if captured["modelDimensions"] != 1024 {
		t.Fatalf("model_dimensions not filled on update: got %v", captured["modelDimensions"])
	}
}
