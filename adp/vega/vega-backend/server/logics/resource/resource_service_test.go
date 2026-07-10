// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package resource

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-comm-go/rest"
	"go.uber.org/mock/gomock"

	verrors "vega-backend/errors"
	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
)

// newTestService 使用 mockgen 生成的 mock 构建 resourceService
func newTestService(t *testing.T) (*resourceService,
	*vmock.MockResourceAccess,
	*vmock.MockPermissionService,
	*vmock.MockDatasetService,
	*vmock.MockUserMgmtService,
	*vmock.MockCatalogService,
	*vmock.MockBuildTaskAccess) {

	ctrl := gomock.NewController(t)
	mockRA := vmock.NewMockResourceAccess(ctrl)
	mockPS := vmock.NewMockPermissionService(ctrl)
	mockDS := vmock.NewMockDatasetService(ctrl)
	mockUMS := vmock.NewMockUserMgmtService(ctrl)
	mockCS := vmock.NewMockCatalogService(ctrl)
	mockBTA := vmock.NewMockBuildTaskAccess(ctrl)

	rs := &resourceService{
		ra:  mockRA,
		ps:  mockPS,
		ds:  mockDS,
		ums: mockUMS,
		cs:  mockCS,
		bta: mockBTA,
	}

	// 默认无系统内部目录；覆盖 internal 行为的用例可叠加更具体的 EXPECT
	mockCS.EXPECT().ListInternalIDs(gomock.Any()).Return([]string{}, nil).AnyTimes()

	return rs, mockRA, mockPS, mockDS, mockUMS, mockCS, mockBTA
}

// ===== CheckExistByID =====

func TestCheckExistByID_Found(t *testing.T) {
	rs, mockRA, _, _, _, _, _ := newTestService(t)
	mockRA.EXPECT().GetByID(gomock.Any(), "r1").
		Return(&interfaces.Resource{ID: "r1"}, nil)

	exists, err := rs.CheckExistByID(context.Background(), "r1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !exists {
		t.Error("expected resource to exist")
	}
}

func TestCheckExistByID_NotFound(t *testing.T) {
	rs, mockRA, _, _, _, _, _ := newTestService(t)
	mockRA.EXPECT().GetByID(gomock.Any(), "missing").
		Return(nil, nil)

	exists, err := rs.CheckExistByID(context.Background(), "missing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exists {
		t.Error("expected resource to not exist")
	}
}

func TestCheckExistByID_Error(t *testing.T) {
	rs, mockRA, _, _, _, _, _ := newTestService(t)
	mockRA.EXPECT().GetByID(gomock.Any(), "r1").
		Return(nil, fmt.Errorf("db error"))

	_, err := rs.CheckExistByID(context.Background(), "r1")
	if err == nil {
		t.Fatal("expected error")
	}
}

// ===== CheckExistByName =====

func TestCheckExistByName_Found(t *testing.T) {
	rs, mockRA, _, _, _, _, _ := newTestService(t)
	mockRA.EXPECT().GetByName(gomock.Any(), "cat1", "test").
		Return(&interfaces.Resource{Name: "test"}, nil)

	exists, err := rs.CheckExistByName(context.Background(), "cat1", "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !exists {
		t.Error("expected resource to exist")
	}
}

func TestCheckExistByName_NotFound(t *testing.T) {
	rs, mockRA, _, _, _, _, _ := newTestService(t)
	mockRA.EXPECT().GetByName(gomock.Any(), "cat1", "missing").
		Return(nil, nil)

	exists, err := rs.CheckExistByName(context.Background(), "cat1", "missing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exists {
		t.Error("expected resource to not exist")
	}
}

// ===== GetByID =====

func TestGetByID_Success(t *testing.T) {
	rs, mockRA, mockPS, _, mockUMS, _, _ := newTestService(t)
	mockRA.EXPECT().GetByID(gomock.Any(), "r1").
		Return(&interfaces.Resource{ID: "r1", Name: "test"}, nil)
	mockPS.EXPECT().FilterResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_RESOURCE,
		[]string{"r1"}, gomock.Any(), true, gomock.Any()).
		Return(map[string]interfaces.PermissionResourceOps{
			"r1": {ResourceID: "r1", Operations: []string{"view_detail"}},
		}, nil)
	mockUMS.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil)

	resource, err := rs.GetByID(context.Background(), "r1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resource.ID != "r1" {
		t.Errorf("expected ID 'r1', got '%s'", resource.ID)
	}
}

func TestGetByID_NotFound(t *testing.T) {
	rs, mockRA, _, _, _, _, _ := newTestService(t)
	mockRA.EXPECT().GetByID(gomock.Any(), "missing").
		Return(nil, nil)

	_, err := rs.GetByID(context.Background(), "missing")
	if err == nil {
		t.Fatal("expected error for not found resource")
	}
}

func TestGetByID_DBError(t *testing.T) {
	rs, mockRA, _, _, _, _, _ := newTestService(t)
	mockRA.EXPECT().GetByID(gomock.Any(), "r1").
		Return(nil, fmt.Errorf("db error"))

	_, err := rs.GetByID(context.Background(), "r1")
	if err == nil {
		t.Fatal("expected error")
	}
}

// ===== GetByIDs =====

func TestGetByIDs_Success(t *testing.T) {
	rs, mockRA, mockPS, _, mockUMS, _, _ := newTestService(t)
	mockRA.EXPECT().GetByIDs(gomock.Any(), []string{"r1", "r2"}).
		Return([]*interfaces.Resource{{ID: "r1"}, {ID: "r2"}}, nil)
	mockRA.EXPECT().AttachListExtensions(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	mockPS.EXPECT().FilterResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_RESOURCE,
		[]string{"r1", "r2"}, gomock.Any(), true, gomock.Any()).
		Return(map[string]interfaces.PermissionResourceOps{
			"r1": {ResourceID: "r1", Operations: []string{"view_detail"}},
			"r2": {ResourceID: "r2", Operations: []string{"view_detail"}},
		}, nil)
	mockUMS.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil)

	resources, err := rs.GetByIDs(context.Background(), []string{"r1", "r2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resources) != 2 {
		t.Errorf("expected 2 resources, got %d", len(resources))
	}
}

// ===== GetByCatalogID =====

func TestGetByCatalogID_Success(t *testing.T) {
	rs, mockRA, _, _, _, _, _ := newTestService(t)
	mockRA.EXPECT().GetByCatalogID(gomock.Any(), "cat1").
		Return([]*interfaces.Resource{{ID: "r1", CatalogID: "cat1"}}, nil)

	resources, err := rs.GetByCatalogID(context.Background(), "cat1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resources) != 1 {
		t.Errorf("expected 1 resource, got %d", len(resources))
	}
}

// ===== List 分页逻辑 =====

func TestList_Pagination(t *testing.T) {
	rs, mockRA, mockPS, _, mockUMS, _, _ := newTestService(t)
	ids := []string{"c1", "c2", "c3", "c4"}
	mockRA.EXPECT().ListIDs(gomock.Any(), gomock.Any()).Return(ids, nil)
	mockPS.EXPECT().FilterResources(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), true, gomock.Any()).
		Return(map[string]interfaces.PermissionResourceOps{
			"c1": {ResourceID: "c1"}, "c2": {ResourceID: "c2"}, "c3": {ResourceID: "c3"}, "c4": {ResourceID: "c4"},
		}, nil)
	resources := []*interfaces.Resource{{ID: "r2"}, {ID: "r3"}}
	mockRA.EXPECT().GetByIDsBasic(gomock.Any(), gomock.Any()).Return(resources, nil)
	mockRA.EXPECT().AttachListExtensions(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	mockUMS.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil)

	result, total, err := rs.List(context.Background(), interfaces.ResourcesQueryParams{
		PaginationQueryParams: interfaces.PaginationQueryParams{Offset: 1, Limit: 2},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 4 {
		t.Errorf("expected total 4, got %d", total)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 results, got %d", len(result))
	}
	if result[0].ID != "r2" {
		t.Errorf("expected first item 'r2', got '%s'", result[0].ID)
	}
}

func TestList_ReturnAll(t *testing.T) {
	rs, mockRA, mockPS, _, mockUMS, _, _ := newTestService(t)
	ids := []string{"c1", "c2"}
	resources := []*interfaces.Resource{{ID: "r1"}, {ID: "r2"}}
	mockRA.EXPECT().ListIDs(gomock.Any(), gomock.Any()).Return(ids, nil)
	mockPS.EXPECT().FilterResources(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), true, gomock.Any()).
		Return(map[string]interfaces.PermissionResourceOps{
			"c1": {ResourceID: "c1"}, "c2": {ResourceID: "c2"},
		}, nil)
	mockRA.EXPECT().GetByIDsBasic(gomock.Any(), gomock.Any()).Return(resources, nil)
	mockRA.EXPECT().AttachListExtensions(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	mockUMS.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil)

	result, total, err := rs.List(context.Background(), interfaces.ResourcesQueryParams{
		PaginationQueryParams: interfaces.PaginationQueryParams{Limit: -1},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 2 {
		t.Errorf("expected total 2, got %d", total)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 results, got %d", len(result))
	}
}

func TestList_OffsetBeyondTotal(t *testing.T) {
	rs, mockRA, mockPS, _, _, _, _ := newTestService(t)

	ids := []string{"c1"}
	mockRA.EXPECT().ListIDs(gomock.Any(), gomock.Any()).Return(ids, nil)
	mockPS.EXPECT().FilterResources(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), true, gomock.Any()).
		Return(map[string]interfaces.PermissionResourceOps{"c1": {ResourceID: "c1"}}, nil)

	result, total, err := rs.List(context.Background(), interfaces.ResourcesQueryParams{
		PaginationQueryParams: interfaces.PaginationQueryParams{Offset: 10, Limit: 5},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 1 {
		t.Errorf("expected total 1, got %d", total)
	}
	if len(result) != 0 {
		t.Errorf("expected 0 results, got %d", len(result))
	}
}

func TestCreate_DatasetCategory(t *testing.T) {
	rs, mockRA, mockPS, mockDS, _, mockCS, _ := newTestService(t)
	mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	mockCS.EXPECT().CheckExistByID(gomock.Any(), gomock.Any()).Return(true, nil)
	mockRA.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
	mockDS.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
	mockPS.EXPECT().CreateResources(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

	resource, err := rs.Create(context.Background(), &interfaces.ResourceRequest{
		Name:     "test-dataset",
		Category: interfaces.ResourceCategoryDataset,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resource == nil {
		t.Error("expected non-empty ID")
	}
}

// ===== DeleteByIDs =====

func TestDeleteByIDs_Empty(t *testing.T) {
	rs, _, _, _, _, _, _ := newTestService(t)
	err := rs.DeleteByIDs(context.Background(), []string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDeleteByIDs_Success(t *testing.T) {
	rs, mockRA, mockPS, _, _, _, mockBTA := newTestService(t)
	mockPS.EXPECT().FilterResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_RESOURCE,
		[]string{"r1"}, gomock.Any(), true, gomock.Any()).
		Return(map[string]interfaces.PermissionResourceOps{
			"r1": {ResourceID: "r1"},
		}, nil)
	mockRA.EXPECT().GetByIDs(gomock.Any(), []string{"r1"}).
		Return([]*interfaces.Resource{{ID: "r1", Category: "table"}}, nil)
	mockRA.EXPECT().DeleteByIDs(gomock.Any(), []string{"r1"}).Return(nil)
	mockPS.EXPECT().DeleteResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_RESOURCE, []string{"r1"}).Return(nil)
	// 级联：无构建任务时 List 返回空，不再走 GetByResourceID 拦截
	mockBTA.EXPECT().List(gomock.Any(), gomock.Any()).Return([]*interfaces.BuildTask{}, int64(0), nil)
	err := rs.DeleteByIDs(context.Background(), []string{"r1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// 删资源时有构建任务：级联 drop 索引 + 删任务行，再删资源。
func TestDeleteByIDs_CascadesBuildTaskAndIndex(t *testing.T) {
	rs, mockRA, mockPS, _, _, _, mockBTA := newTestService(t)
	ctrl := gomock.NewController(t)
	mockLIM := vmock.NewMockLocalIndexManager(ctrl)
	rs.lim = mockLIM
	mockPS.EXPECT().FilterResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_RESOURCE,
		[]string{"r1"}, gomock.Any(), true, gomock.Any()).
		Return(map[string]interfaces.PermissionResourceOps{"r1": {ResourceID: "r1"}}, nil)
	mockRA.EXPECT().GetByIDs(gomock.Any(), []string{"r1"}).
		Return([]*interfaces.Resource{{ID: "r1", Category: "table"}}, nil)
	// 一个已完成任务 t1 → 期望 drop 其索引并删任务行
	mockBTA.EXPECT().List(gomock.Any(), gomock.Any()).
		Return([]*interfaces.BuildTask{{ID: "t1", ResourceID: "r1", Status: "completed"}}, int64(1), nil)
	mockLIM.EXPECT().DeleteIndex(gomock.Any(), interfaces.BuildIndexName("r1", "t1")).Return(nil)
	mockBTA.EXPECT().Delete(gomock.Any(), "t1").Return(nil)
	mockRA.EXPECT().DeleteByIDs(gomock.Any(), []string{"r1"}).Return(nil)
	mockPS.EXPECT().DeleteResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_RESOURCE, []string{"r1"}).Return(nil)
	if err := rs.DeleteByIDs(context.Background(), []string{"r1"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// 删资源时任务在运行中：级联拒绝，资源不删。
func TestDeleteByIDs_RefusesWhenTaskRunning(t *testing.T) {
	rs, mockRA, mockPS, _, _, _, mockBTA := newTestService(t)
	mockPS.EXPECT().FilterResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_RESOURCE,
		[]string{"r1"}, gomock.Any(), true, gomock.Any()).
		Return(map[string]interfaces.PermissionResourceOps{"r1": {ResourceID: "r1"}}, nil)
	mockRA.EXPECT().GetByIDs(gomock.Any(), []string{"r1"}).
		Return([]*interfaces.Resource{{ID: "r1", Category: "table"}}, nil)
	mockBTA.EXPECT().List(gomock.Any(), gomock.Any()).
		Return([]*interfaces.BuildTask{{ID: "t1", ResourceID: "r1", Status: "running"}}, int64(1), nil)
	// 不应调用 DeleteByIDs / bta.Delete / ds.Delete
	err := rs.DeleteByIDs(context.Background(), []string{"r1"})
	if err == nil {
		t.Fatalf("expected error when a build task is running")
	}
}

// ===== Create =====

func TestCreate_Success(t *testing.T) {
	rs, mockRA, mockPS, _, _, mockCS, _ := newTestService(t)
	mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	mockCS.EXPECT().CheckExistByID(gomock.Any(), gomock.Any()).Return(true, nil)
	mockRA.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
	mockPS.EXPECT().CreateResources(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

	resource, err := rs.Create(context.Background(), &interfaces.ResourceRequest{
		Name:     "test-resource",
		Category: "table",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resource == nil {
		t.Error("expected non-empty ID")
	}
}

func TestCreate_WithExplicitID(t *testing.T) {
	rs, mockRA, mockPS, _, _, mockCS, _ := newTestService(t)
	mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	mockCS.EXPECT().CheckExistByID(gomock.Any(), gomock.Any()).Return(true, nil)
	mockRA.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
	mockPS.EXPECT().CreateResources(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

	resource, err := rs.Create(context.Background(), &interfaces.ResourceRequest{
		ID:       "custom-id",
		Name:     "test-resource",
		Category: "table",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resource == nil || resource.ID != "custom-id" {
		t.Errorf("expected 'custom-id', got '%s'", resource.ID)
	}
}

func TestCreate_DBError(t *testing.T) {
	rs, mockRA, mockPS, _, _, mockCS, _ := newTestService(t)
	mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	mockCS.EXPECT().CheckExistByID(gomock.Any(), gomock.Any()).Return(true, nil)
	mockRA.EXPECT().Create(gomock.Any(), gomock.Any()).Return(fmt.Errorf("db error"))

	_, err := rs.Create(context.Background(), &interfaces.ResourceRequest{
		Name: "test-resource",
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

// ===== UpdateStatus =====

func TestUpdateStatus_Success(t *testing.T) {
	rs, mockRA, _, _, _, _, _ := newTestService(t)
	mockRA.EXPECT().UpdateStatus(gomock.Any(), "r1", "active", "").Return(nil)

	err := rs.UpdateStatus(context.Background(), "r1", "active", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUpdateStatus_Error(t *testing.T) {
	rs, mockRA, _, _, _, _, _ := newTestService(t)
	mockRA.EXPECT().UpdateStatus(gomock.Any(), "r1", "active", "").
		Return(fmt.Errorf("db error"))

	err := rs.UpdateStatus(context.Background(), "r1", "active", "")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestUpdateResource_PreservesDiscoverStatus(t *testing.T) {
	rs, mockRA, _, _, _, _, _ := newTestService(t)
	resource := &interfaces.Resource{ID: "r1", LastDiscoverStatus: interfaces.DiscoverStatusUpdated}
	mockRA.EXPECT().Update(gomock.Any(), resource).Return(nil)

	err := rs.UpdateResource(context.Background(), resource)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resource.LastDiscoverStatus != interfaces.DiscoverStatusUpdated {
		t.Fatalf("expected resource discover status to be set, got %q", resource.LastDiscoverStatus)
	}
}

func TestUpdateDiscoverStatus_Success(t *testing.T) {
	rs, mockRA, _, _, _, _, _ := newTestService(t)
	mockRA.EXPECT().UpdateDiscoverStatus(gomock.Any(), "r1", interfaces.DiscoverStatusUpdated).Return(nil)

	err := rs.UpdateDiscoverStatus(context.Background(), "r1", interfaces.DiscoverStatusUpdated)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUpdateDiscoverStatus_Error(t *testing.T) {
	rs, mockRA, _, _, _, _, _ := newTestService(t)
	mockRA.EXPECT().UpdateDiscoverStatus(gomock.Any(), "r1", interfaces.DiscoverStatusUpdated).
		Return(fmt.Errorf("db error"))

	err := rs.UpdateDiscoverStatus(context.Background(), "r1", interfaces.DiscoverStatusUpdated)
	if err == nil {
		t.Fatal("expected error")
	}
}

// ===== Update =====

func TestUpdate_NilResource(t *testing.T) {
	rs, _, _, _, _, _, _ := newTestService(t)
	err := rs.Update(context.Background(), nil, &interfaces.ResourceRequest{})
	if err == nil {
		t.Fatal("expected error for nil resource")
	}
}

func TestUpdate_Success(t *testing.T) {
	rs, mockRA, mockPS, _, _, mockCS, _ := newTestService(t)
	mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	mockCS.EXPECT().CheckExistByID(gomock.Any(), gomock.Any()).Return(true, nil)
	mockRA.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil)

	err := rs.Update(context.Background(), &interfaces.Resource{ID: "r1", Name: "updated"}, &interfaces.ResourceRequest{
		Name: "updated",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUpdate_RejectsBuildRelevantChangeWhenActiveBuildTaskExists(t *testing.T) {
	rs, _, mockPS, _, _, _, mockBTA := newTestService(t)
	mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	mockBTA.EXPECT().List(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, params interfaces.BuildTasksQueryParams) ([]*interfaces.BuildTask, int64, error) {
			if params.ResourceID != "r1" {
				t.Fatalf("expected resource r1, got %q", params.ResourceID)
			}
			return []*interfaces.BuildTask{{
				ID:         "task-1",
				ResourceID: "r1",
				Status:     interfaces.BuildTaskStatusRunning,
			}}, 1, nil
		})

	err := rs.Update(context.Background(), &interfaces.Resource{
		ID:               "r1",
		CatalogID:        "cat1",
		Category:         interfaces.ResourceCategoryTable,
		Name:             "table",
		SourceIdentifier: "public.orders",
		SchemaDefinition: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_String}},
	}, &interfaces.ResourceRequest{
		CatalogID:        "cat1",
		Name:             "table",
		SourceIdentifier: "public.orders",
		SchemaDefinition: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_String}, {Name: "title", Type: interfaces.DataType_String}},
	})

	httpErr, ok := err.(*rest.HTTPError)
	if !ok {
		t.Fatalf("expected HTTPError, got %T", err)
	}
	if httpErr.HTTPCode != http.StatusConflict {
		t.Fatalf("expected 409, got %d", httpErr.HTTPCode)
	}
	if httpErr.BaseError.ErrorCode != verrors.VegaBackend_BuildTask_Exist {
		t.Fatalf("expected %s, got %s", verrors.VegaBackend_BuildTask_Exist, httpErr.BaseError.ErrorCode)
	}
}

func TestUpdate_AllowsNonBuildRelevantChangeWhenActiveBuildTaskExists(t *testing.T) {
	rs, mockRA, mockPS, _, _, mockCS, _ := newTestService(t)
	mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	mockCS.EXPECT().CheckExistByID(gomock.Any(), "cat1").Return(true, nil)
	mockRA.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil)

	err := rs.Update(context.Background(), &interfaces.Resource{
		ID:               "r1",
		CatalogID:        "cat1",
		Category:         interfaces.ResourceCategoryTable,
		Name:             "table",
		Description:      "old",
		SourceIdentifier: "public.orders",
		SchemaDefinition: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_String}},
	}, &interfaces.ResourceRequest{
		CatalogID:        "cat1",
		Name:             "table",
		Description:      "new",
		SourceIdentifier: "public.orders",
		SchemaDefinition: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_String}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ===== Internal catalog 下的资源（internal_resource 类型） =====

func TestCreate_InternalCatalogResourceUsesInternalAuthType(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockRA := vmock.NewMockResourceAccess(ctrl)
	mockPS := vmock.NewMockPermissionService(ctrl)
	mockCS := vmock.NewMockCatalogService(ctrl)
	rs := &resourceService{ra: mockRA, ps: mockPS, cs: mockCS}

	mockCS.EXPECT().ListInternalIDs(gomock.Any()).Return([]string{"cat-internal"}, nil)
	mockPS.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
		Type: interfaces.AUTH_RESOURCE_TYPE_INTERNAL_RESOURCE,
		ID:   interfaces.RESOURCE_ID_ALL,
	}, []string{interfaces.OPERATION_TYPE_CREATE}).Return(nil)
	mockCS.EXPECT().CheckExistByID(gomock.Any(), "cat-internal").Return(true, nil)
	mockRA.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
	mockPS.EXPECT().CreateResources(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, resources []interfaces.PermissionResource, _ []string) error {
			if resources[0].Type != interfaces.AUTH_RESOURCE_TYPE_INTERNAL_RESOURCE {
				t.Fatalf("expected internal_resource auth type, got %s", resources[0].Type)
			}
			return nil
		},
	)

	_, err := rs.Create(context.Background(), &interfaces.ResourceRequest{
		CatalogID: "cat-internal",
		Name:      "internal-res",
		Category:  "table",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestList_InternalResourceCheckedSeparately(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockRA := vmock.NewMockResourceAccess(ctrl)
	mockPS := vmock.NewMockPermissionService(ctrl)
	mockCS := vmock.NewMockCatalogService(ctrl)
	mockUMS := vmock.NewMockUserMgmtService(ctrl)
	rs := &resourceService{ra: mockRA, ps: mockPS, cs: mockCS, ums: mockUMS}

	mockRA.EXPECT().ListIDs(gomock.Any(), interfaces.ResourcesQueryParams{
		PaginationQueryParams: interfaces.PaginationQueryParams{Limit: -1},
	}).Return([]string{"r1", "r2"}, nil)
	mockCS.EXPECT().ListInternalIDs(gomock.Any()).Return([]string{"cat-internal"}, nil)
	mockRA.EXPECT().ListIDs(gomock.Any(), interfaces.ResourcesQueryParams{CatalogID: "cat-internal"}).
		Return([]string{"r2"}, nil)
	// 普通资源按 resource 类型校验
	mockPS.EXPECT().FilterResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_RESOURCE,
		[]string{"r1"}, gomock.Any(), true, gomock.Any()).
		Return(map[string]interfaces.PermissionResourceOps{"r1": {ResourceID: "r1"}}, nil)
	// 内部目录下的资源按 internal_resource 类型校验；业务角色无授权 → 被过滤
	mockPS.EXPECT().FilterResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_INTERNAL_RESOURCE,
		[]string{"r2"}, gomock.Any(), true, gomock.Any()).
		Return(map[string]interfaces.PermissionResourceOps{}, nil)
	mockRA.EXPECT().GetByIDsBasic(gomock.Any(), []string{"r1"}).
		Return([]*interfaces.Resource{{ID: "r1"}}, nil)
	mockRA.EXPECT().AttachListExtensions(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	mockUMS.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil)

	result, total, err := rs.List(context.Background(), interfaces.ResourcesQueryParams{
		PaginationQueryParams: interfaces.PaginationQueryParams{Limit: -1},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 1 {
		t.Errorf("expected total 1, got %d", total)
	}
	if len(result) != 1 || result[0].ID != "r1" {
		t.Errorf("expected only 'r1' visible, got %v", result)
	}
}

func newS2STestService(t *testing.T, internalCatalogIDs []string) (
	*resourceService, *vmock.MockResourceAccess, *vmock.MockPermissionService, *vmock.MockUserMgmtService) {
	ctrl := gomock.NewController(t)
	ra := vmock.NewMockResourceAccess(ctrl)
	ps := vmock.NewMockPermissionService(ctrl)
	ums := vmock.NewMockUserMgmtService(ctrl)
	cs := vmock.NewMockCatalogService(ctrl)
	rs := &resourceService{ra: ra, ps: ps, ums: ums, cs: cs}
	cs.EXPECT().ListInternalIDs(gomock.Any()).Return(internalCatalogIDs, nil).AnyTimes()
	return rs, ra, ps, ums
}

func TestGetByID_S2SInternal_Bypass(t *testing.T) {
	t.Run("bypasses view detail permission for internal resource with S2S marker", func(t *testing.T) {
		rs, ra, _, ums := newS2STestService(t, []string{"cat-int"})
		ra.EXPECT().GetByID(gomock.Any(), "r1").
			Return(&interfaces.Resource{ID: "r1", CatalogID: "cat-int"}, nil)
		ums.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil)

		res, err := rs.GetByID(interfaces.WithS2SInternalAccess(context.Background()), "r1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res == nil || len(res.Operations) == 0 {
			t.Fatalf("expected operations to be filled, got %+v", res)
		}
	})
}

func TestGetByID_Internal_NoMarker_Forbidden(t *testing.T) {
	t.Run("rejects internal resource without S2S marker when permission filter returns empty", func(t *testing.T) {
		rs, ra, ps, _ := newS2STestService(t, []string{"cat-int"})
		ra.EXPECT().GetByID(gomock.Any(), "r1").
			Return(&interfaces.Resource{ID: "r1", CatalogID: "cat-int"}, nil)
		ps.EXPECT().FilterResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_INTERNAL_RESOURCE,
			gomock.Any(), gomock.Any(), true, gomock.Any()).
			Return(map[string]interfaces.PermissionResourceOps{}, nil)

		_, err := rs.GetByID(context.Background(), "r1")
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestGetByID_NonInternal_WithMarker_StillAuthz(t *testing.T) {
	t.Run("keeps per-account auth for non-internal resource with S2S marker", func(t *testing.T) {
		rs, ra, ps, _ := newS2STestService(t, []string{})
		ra.EXPECT().GetByID(gomock.Any(), "r1").
			Return(&interfaces.Resource{ID: "r1", CatalogID: "cat-user"}, nil)
		ps.EXPECT().FilterResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_RESOURCE,
			gomock.Any(), gomock.Any(), true, gomock.Any()).
			Return(map[string]interfaces.PermissionResourceOps{}, nil)

		_, err := rs.GetByID(interfaces.WithS2SInternalAccess(context.Background()), "r1")
		if err == nil {
			t.Fatal("expected error")
		}
	})
}
