// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package resource

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	mockDTA := vmock.NewMockDiscoverTaskAccess(ctrl)

	rs := &resourceService{
		ra:  mockRA,
		ps:  mockPS,
		ds:  mockDS,
		ums: mockUMS,
		cs:  mockCS,
		bta: mockBTA,
		dta: mockDTA,
	}

	// 默认无系统内部目录；覆盖 internal 行为的用例可叠加更具体的 EXPECT
	mockCS.EXPECT().InternalCatalogIDSet(gomock.Any()).Return(map[string]struct{}{}, nil).AnyTimes()
	mockDTA.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()

	return rs, mockRA, mockPS, mockDS, mockUMS, mockCS, mockBTA
}

func TestResourceServiceInternalLocalIndexTransaction(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockRA := vmock.NewMockResourceAccess(ctrl)
	service := &resourceService{ra: mockRA}
	tx := &sql.Tx{}
	resource := &interfaces.Resource{ID: "resource-1"}
	mockRA.EXPECT().GetByID(gomock.Any(), tx, "resource-1").Return(resource, nil)
	mockRA.EXPECT().UpdateLocalIndexState(
		gomock.Any(), tx, "resource-1",
		interfaces.ResourceLocalIndexStatusAvailable,
		"index-v1",
		`{"mode":"batch","cursor":[10]}`,
	).Return(true, nil)

	got, err := service.InternalGetByID(context.Background(), tx, "resource-1")
	require.NoError(t, err)
	assert.Same(t, resource, got)
	updated, err := service.InternalUpdateLocalIndexState(
		context.Background(), tx, "resource-1",
		interfaces.ResourceLocalIndexStatusAvailable,
		"index-v1",
		`{"mode":"batch","cursor":[10]}`,
	)
	require.NoError(t, err)
	assert.True(t, updated)
}

func expectResourceServiceTransaction(t *testing.T, rs *resourceService, commit bool) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	rs.db = db
	mock.ExpectBegin()
	if commit {
		mock.ExpectCommit()
	} else {
		mock.ExpectRollback()
	}
	mock.ExpectClose()
	t.Cleanup(func() {
		require.NoError(t, db.Close())
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestValidateSchemaDefinitionRejectsDuplicateFeatureTypes(t *testing.T) {
	err := validateSchemaDefinition(context.Background(), []*interfaces.Property{{
		Name: "code",
		Features: []interfaces.PropertyFeature{
			{FeatureType: interfaces.PropertyFeatureType_Keyword},
			{FeatureType: interfaces.PropertyFeatureType_Keyword},
		},
	}})

	httpErr := requireResourceHTTPError(t, err, verrors.VegaBackend_InvalidParameter_RequestBody)
	assert.Contains(t, httpErr.BaseError.ErrorDetails, `property "code" has more than one "keyword" feature`)
}

func TestResourceServiceInternalMetadataUpdateConflict(t *testing.T) {
	t.Run("semantic metadata", func(t *testing.T) {
		rs, mockRA, _, _, _, _, _ := newTestService(t)
		expectedUpdateTime := int64(42)
		resource := &interfaces.Resource{ID: "r1", UpdateTime: 43}
		mockRA.EXPECT().UpdateSemanticMetadata(gomock.Any(), nil, resource, expectedUpdateTime).
			Return(int64(0), nil)

		err := rs.InternalUpdateSemanticMetadata(context.Background(), nil, resource, expectedUpdateTime)
		httpErr := requireResourceHTTPError(t, err, verrors.VegaBackend_Resource_UpdateConflict)
		assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
	})

	t.Run("discovery metadata", func(t *testing.T) {
		rs, mockRA, _, _, _, _, _ := newTestService(t)
		expectedUpdateTime := int64(42)
		resource := &interfaces.Resource{ID: "r1", UpdateTime: 43, SchemaDefinition: []*interfaces.Property{{Name: "id"}}}
		current := &interfaces.Resource{ID: "r1", UpdateTime: expectedUpdateTime, SchemaDefinition: []*interfaces.Property{{Name: "id"}}}
		tx := &sql.Tx{}
		mockRA.EXPECT().GetByID(gomock.Any(), tx, "r1").Return(current, nil)
		mockRA.EXPECT().UpdateDiscoveryMetadata(gomock.Any(), tx, resource, expectedUpdateTime).
			Return(int64(0), nil)

		err := rs.InternalUpdateDiscoveryMetadata(context.Background(), tx, resource, expectedUpdateTime)
		httpErr := requireResourceHTTPError(t, err, verrors.VegaBackend_Resource_UpdateConflict)
		assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
	})
}

func TestValidateIndexConfigKeyFields(t *testing.T) {
	schema := []*interfaces.Property{
		{Name: "id", Type: interfaces.DataType_Integer},
		{Name: "updated_at", Type: interfaces.DataType_Timestamp},
		{Name: "body", Type: interfaces.DataType_Text},
	}

	t.Run("allows an empty key configuration", func(t *testing.T) {
		require.NoError(t, validateIndexConfigKeyFields(context.Background(), schema, nil))
	})

	t.Run("allows configured primary and incremental fields", func(t *testing.T) {
		err := validateIndexConfigKeyFields(context.Background(), schema, &interfaces.ResourceIndexConfig{
			PrimaryKeyFields:  []string{"id"},
			IncrementalFields: []string{"updated_at", "id"},
		})
		require.NoError(t, err)
	})

	t.Run("rejects primary keys absent from schema", func(t *testing.T) {
		err := validateIndexConfigKeyFields(context.Background(), schema, &interfaces.ResourceIndexConfig{
			PrimaryKeyFields: []string{"missing_id"},
		})
		httpErr := requireResourceHTTPError(t, err, verrors.VegaBackend_Resource_InvalidParameter_PrimaryKeyFields)
		require.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
		require.Contains(t, httpErr.BaseError.ErrorDetails, `primary_key_fields field "missing_id"`)
	})

	t.Run("rejects duplicate primary keys and unsupported incremental types", func(t *testing.T) {
		err := validateIndexConfigKeyFields(context.Background(), schema, &interfaces.ResourceIndexConfig{
			PrimaryKeyFields: []string{"id", "id"},
		})
		requireResourceHTTPError(t, err, verrors.VegaBackend_Resource_InvalidParameter_PrimaryKeyFields)

		err = validateIndexConfigKeyFields(context.Background(), schema, &interfaces.ResourceIndexConfig{
			IncrementalFields: []string{"body"},
		})
		httpErr := requireResourceHTTPError(t, err, verrors.VegaBackend_Resource_InvalidParameter_IncrementalFields)
		assert.Contains(t, httpErr.BaseError.ErrorDetails, `unsupported type "text"`)
	})
}

func TestResourceServiceCheckExistByID(t *testing.T) {
	t.Run("check exist by idfound", func(t *testing.T) {
		rs, mockRA, _, _, _, _, _ := newTestService(t)
		mockRA.EXPECT().GetByID(gomock.Any(), nil, "r1").
			Return(&interfaces.Resource{ID: "r1"}, nil)

		exists, err := rs.CheckExistByID(context.Background(), "r1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !exists {
			t.Error("expected resource to exist")
		}
	})
	t.Run("check exist by idnot found", func(t *testing.T) {
		rs, mockRA, _, _, _, _, _ := newTestService(t)
		mockRA.EXPECT().GetByID(gomock.Any(), nil, "missing").
			Return(nil, nil)

		exists, err := rs.CheckExistByID(context.Background(), "missing")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if exists {
			t.Error("expected resource to not exist")
		}
	})
	t.Run("check exist by iderror", func(t *testing.T) {
		rs, mockRA, _, _, _, _, _ := newTestService(t)
		mockRA.EXPECT().GetByID(gomock.Any(), nil, "r1").
			Return(nil, fmt.Errorf("db error"))

		_, err := rs.CheckExistByID(context.Background(), "r1")
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestResourceServiceCheckExistByName(t *testing.T) {
	t.Run("check exist by name found", func(t *testing.T) {
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
	})
	t.Run("check exist by name not found", func(t *testing.T) {
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
	})
}

func TestResourceServiceGetByID(t *testing.T) {
	t.Run("keeps resource when account name lookup fails", func(t *testing.T) {
		rs, mockRA, mockPS, _, mockUMS, _, _ := newTestService(t)
		mockRA.EXPECT().GetByID(gomock.Any(), nil, "r1").
			Return(&interfaces.Resource{ID: "r1", Name: "test"}, nil)
		mockPS.EXPECT().FilterResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_RESOURCE,
			[]string{"r1"}, gomock.Any(), true, gomock.Any()).
			Return(map[string]interfaces.PermissionResourceOps{
				"r1": {ResourceID: "r1", Operations: []string{"view_detail"}},
			}, nil)
		mockUMS.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(errors.New("user management unavailable"))

		resource, err := rs.GetByID(context.Background(), "r1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resource.ID != "r1" {
			t.Errorf("expected ID 'r1', got '%s'", resource.ID)
		}
	})
	t.Run("get by idnot found", func(t *testing.T) {
		rs, mockRA, _, _, _, _, _ := newTestService(t)
		mockRA.EXPECT().GetByID(gomock.Any(), nil, "missing").
			Return(nil, nil)

		_, err := rs.GetByID(context.Background(), "missing")
		if err == nil {
			t.Fatal("expected error for not found resource")
		}
	})
	t.Run("get by iddberror", func(t *testing.T) {
		rs, mockRA, _, _, _, _, _ := newTestService(t)
		mockRA.EXPECT().GetByID(gomock.Any(), nil, "r1").
			Return(nil, fmt.Errorf("db error"))

		_, err := rs.GetByID(context.Background(), "r1")
		if err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("bypasses view detail permission for internal resource with S2S marker", func(t *testing.T) {
		rs, ra, _, ums := newS2STestService(t, []string{"cat-int"})
		ra.EXPECT().GetByID(gomock.Any(), nil, "r1").
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
	t.Run("rejects internal resource without S2S marker when permission filter returns empty", func(t *testing.T) {
		rs, ra, ps, _ := newS2STestService(t, []string{"cat-int"})
		ra.EXPECT().GetByID(gomock.Any(), nil, "r1").
			Return(&interfaces.Resource{ID: "r1", CatalogID: "cat-int"}, nil)
		ps.EXPECT().FilterResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_INTERNAL_RESOURCE,
			gomock.Any(), gomock.Any(), true, gomock.Any()).
			Return(map[string]interfaces.PermissionResourceOps{}, nil)
		// 资源侧拒了会再问所属目录（#817）；目录也没批，结论不变。
		ra.EXPECT().GetPermissionRefsByIDs(gomock.Any(), []string{"r1"}).
			Return([]interfaces.ResourcePermissionRef{{ResourceID: "r1", CatalogID: "cat-int"}}, nil)
		ps.EXPECT().FilterResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_INTERNAL_CATALOG,
			[]string{"cat-int"}, gomock.Any(), true, gomock.Any()).
			Return(map[string]interfaces.PermissionResourceOps{}, nil)

		_, err := rs.GetByID(context.Background(), "r1")
		if err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("keeps per-account auth for non-internal resource with S2S marker", func(t *testing.T) {
		rs, ra, ps, _ := newS2STestService(t, []string{})
		ra.EXPECT().GetByID(gomock.Any(), nil, "r1").
			Return(&interfaces.Resource{ID: "r1", CatalogID: "cat-user"}, nil)
		ps.EXPECT().FilterResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_RESOURCE,
			gomock.Any(), gomock.Any(), true, gomock.Any()).
			Return(map[string]interfaces.PermissionResourceOps{}, nil)
		// 同上：回落到目录，目录也没批。
		ra.EXPECT().GetPermissionRefsByIDs(gomock.Any(), []string{"r1"}).
			Return([]interfaces.ResourcePermissionRef{{ResourceID: "r1", CatalogID: "cat-user"}}, nil)
		ps.EXPECT().FilterResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_CATALOG,
			[]string{"cat-user"}, gomock.Any(), true, gomock.Any()).
			Return(map[string]interfaces.PermissionResourceOps{}, nil)

		_, err := rs.GetByID(interfaces.WithS2SInternalAccess(context.Background()), "r1")
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestResourceServiceGetByIDs(t *testing.T) {
	t.Run("get by ids success", func(t *testing.T) {
		rs, mockRA, mockPS, _, mockUMS, _, _ := newTestService(t)
		mockRA.EXPECT().GetByIDs(gomock.Any(), []string{"r1", "r2"}).
			Return([]*interfaces.Resource{{ID: "r1"}, {ID: "r2"}}, nil)
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
	})
}

func TestResourceServiceGetByCatalogID(t *testing.T) {
	t.Run("get by catalog idsuccess", func(t *testing.T) {
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
	})
}

func TestResourceServiceList(t *testing.T) {
	t.Run("list pagination", func(t *testing.T) {
		rs, mockRA, mockPS, _, mockUMS, _, _ := newTestService(t)
		refs := []interfaces.ResourcePermissionRef{{ResourceID: "c1"}, {ResourceID: "c2"}, {ResourceID: "c3"}, {ResourceID: "c4"}}
		mockRA.EXPECT().ListPermissionRefs(gomock.Any(), gomock.Any()).Return(refs, nil)
		mockPS.EXPECT().FilterResources(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), true, gomock.Any()).
			Return(map[string]interfaces.PermissionResourceOps{
				"c1": {ResourceID: "c1"}, "c2": {ResourceID: "c2"}, "c3": {ResourceID: "c3"}, "c4": {ResourceID: "c4"},
			}, nil)
		summaries := []*interfaces.ResourceSummary{{ID: "r2"}, {ID: "r3"}}
		mockRA.EXPECT().GetSummariesByIDs(gomock.Any(), gomock.Any()).Return(summaries, nil)
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
	})
	t.Run("list return all", func(t *testing.T) {
		rs, mockRA, mockPS, _, mockUMS, _, _ := newTestService(t)
		refs := []interfaces.ResourcePermissionRef{{ResourceID: "c1"}, {ResourceID: "c2"}}
		summaries := []*interfaces.ResourceSummary{{ID: "r1"}, {ID: "r2"}}
		mockRA.EXPECT().ListPermissionRefs(gomock.Any(), gomock.Any()).Return(refs, nil)
		mockPS.EXPECT().FilterResources(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), true, gomock.Any()).
			Return(map[string]interfaces.PermissionResourceOps{
				"c1": {ResourceID: "c1"}, "c2": {ResourceID: "c2"},
			}, nil)
		mockRA.EXPECT().GetSummariesByIDs(gomock.Any(), gomock.Any()).Return(summaries, nil)
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
	})
	t.Run("list offset beyond total", func(t *testing.T) {
		rs, mockRA, mockPS, _, _, _, _ := newTestService(t)

		refs := []interfaces.ResourcePermissionRef{{ResourceID: "c1"}}
		mockRA.EXPECT().ListPermissionRefs(gomock.Any(), gomock.Any()).Return(refs, nil)
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
	})
	t.Run("list internal resource checked separately", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockRA := vmock.NewMockResourceAccess(ctrl)
		mockPS := vmock.NewMockPermissionService(ctrl)
		mockCS := vmock.NewMockCatalogService(ctrl)
		mockUMS := vmock.NewMockUserMgmtService(ctrl)
		rs := &resourceService{ra: mockRA, ps: mockPS, cs: mockCS, ums: mockUMS}

		mockRA.EXPECT().ListPermissionRefs(gomock.Any(), interfaces.ResourcesQueryParams{
			PaginationQueryParams: interfaces.PaginationQueryParams{Limit: -1},
		}).Return([]interfaces.ResourcePermissionRef{{ResourceID: "r1"}, {ResourceID: "r2", CatalogID: "cat-internal"}}, nil)
		mockCS.EXPECT().InternalCatalogIDSet(gomock.Any()).Return(map[string]struct{}{"cat-internal": {}}, nil)
		// 普通资源按 resource 类型校验
		mockPS.EXPECT().FilterResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_RESOURCE,
			[]string{"r1"}, gomock.Any(), true, gomock.Any()).
			Return(map[string]interfaces.PermissionResourceOps{"r1": {ResourceID: "r1"}}, nil)
		// 内部目录下的资源按 internal_resource 类型校验；业务角色无授权 → 被过滤
		mockPS.EXPECT().FilterResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_INTERNAL_RESOURCE,
			[]string{"r2"}, gomock.Any(), true, gomock.Any()).
			Return(map[string]interfaces.PermissionResourceOps{}, nil)
		// r2 资源侧被拒，回落问它所属的内部目录（#817）；目录也没批，仍然被过滤掉。
		mockCS.EXPECT().InternalCatalogIDSet(gomock.Any()).Return(map[string]struct{}{"cat-internal": {}}, nil)
		mockRA.EXPECT().GetPermissionRefsByIDs(gomock.Any(), []string{"r2"}).
			Return([]interfaces.ResourcePermissionRef{{ResourceID: "r2", CatalogID: "cat-internal"}}, nil)
		mockPS.EXPECT().FilterResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_INTERNAL_CATALOG,
			[]string{"cat-internal"}, gomock.Any(), true, gomock.Any()).
			Return(map[string]interfaces.PermissionResourceOps{}, nil)
		mockRA.EXPECT().GetSummariesByIDs(gomock.Any(), []string{"r1"}).
			Return([]*interfaces.ResourceSummary{{ID: "r1"}}, nil)
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
	})
}

func TestValidateIndexConfigAnalyzers(t *testing.T) {
	schema := []*interfaces.Property{
		{Name: "title", Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Fulltext}}},
		{Name: "summary", Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Fulltext, Config: map[string]any{"analyzer": "english"}}}},
	}

	t.Run("accepts defaults and field overrides in the capability snapshot", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		lim := vmock.NewMockLocalIndexManager(ctrl)
		lim.EXPECT().ValidateAnalyzer(gomock.Any(), "standard").Return(true, nil)
		lim.EXPECT().ValidateAnalyzer(gomock.Any(), "english").Return(true, nil)
		rs := &resourceService{lim: lim}

		err := rs.validateIndexConfigAnalyzers(context.Background(), schema, &interfaces.ResourceIndexConfig{DefaultFulltextAnalyzer: "standard"})
		require.NoError(t, err)
	})

	t.Run("rejects an unavailable analyzer with affected fields", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		lim := vmock.NewMockLocalIndexManager(ctrl)
		lim.EXPECT().ValidateAnalyzer(gomock.Any(), "standard").Return(true, nil)
		lim.EXPECT().ValidateAnalyzer(gomock.Any(), "english").Return(false, nil)
		rs := &resourceService{lim: lim}

		err := rs.validateIndexConfigAnalyzers(context.Background(), schema, &interfaces.ResourceIndexConfig{DefaultFulltextAnalyzer: "standard"})
		httpErr := requireResourceHTTPError(t, err, verrors.VegaBackend_Resource_InvalidParameter_Analyzer)
		assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
		assert.Contains(t, httpErr.BaseError.ErrorDetails, "english")
		assert.Contains(t, httpErr.BaseError.ErrorDetails, "summary")
	})

	t.Run("returns capability unavailable when the startup probe failed", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		lim := vmock.NewMockLocalIndexManager(ctrl)
		lim.EXPECT().ValidateAnalyzer(gomock.Any(), "standard").Return(false, &interfaces.IndexCapabilitiesUnavailableError{Cause: errors.New("connection refused")})
		rs := &resourceService{lim: lim}

		err := rs.validateIndexConfigAnalyzers(context.Background(), schema, &interfaces.ResourceIndexConfig{DefaultFulltextAnalyzer: "standard"})
		httpErr := requireResourceHTTPError(t, err, verrors.VegaBackend_IndexCapability_InternalError_Unavailable)
		assert.Equal(t, http.StatusServiceUnavailable, httpErr.HTTPCode)
		assert.Contains(t, httpErr.BaseError.ErrorDetails, "connection refused")
	})

	t.Run("returns an internal error for other analyzer validation failures", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		lim := vmock.NewMockLocalIndexManager(ctrl)
		lim.EXPECT().ValidateAnalyzer(gomock.Any(), "standard").Return(false, errors.New("unexpected validation failure"))
		rs := &resourceService{lim: lim}

		err := rs.validateIndexConfigAnalyzers(context.Background(), schema, &interfaces.ResourceIndexConfig{DefaultFulltextAnalyzer: "standard"})
		httpErr := requireResourceHTTPError(t, err, verrors.VegaBackend_Resource_InternalError)
		assert.Equal(t, http.StatusInternalServerError, httpErr.HTTPCode)
		assert.Contains(t, httpErr.BaseError.ErrorDetails, "unexpected validation failure")
	})

	t.Run("validates each configured fulltext feature without collection", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		lim := vmock.NewMockLocalIndexManager(ctrl)
		lim.EXPECT().ValidateAnalyzer(gomock.Any(), "standard").Return(true, nil)
		lim.EXPECT().ValidateAnalyzer(gomock.Any(), "english").Return(true, nil)
		rs := &resourceService{lim: lim}
		multiFeatureSchema := []*interfaces.Property{{
			Name: "title",
			Features: []interfaces.PropertyFeature{
				{FeatureType: interfaces.PropertyFeatureType_Fulltext, Config: map[string]any{"analyzer": "standard"}},
				{FeatureType: interfaces.PropertyFeatureType_Fulltext, Config: map[string]any{"analyzer": "english"}},
			},
		}}

		require.NoError(t, rs.validateIndexConfigAnalyzers(context.Background(), multiFeatureSchema, nil))
	})
}

func requireResourceHTTPError(t *testing.T, err error, wantCode string) *rest.HTTPError {
	t.Helper()
	require.Error(t, err)
	httpErr, ok := err.(*rest.HTTPError)
	require.Truef(t, ok, "expected HTTPError, got %T", err)
	assert.Equal(t, wantCode, httpErr.BaseError.ErrorCode)
	return httpErr
}

func TestValidateSchemaDefinitionRejectsNullField(t *testing.T) {
	err := validateSchemaDefinition(context.Background(), []*interfaces.Property{{Name: "id"}, nil})

	httpErr := requireResourceHTTPError(t, err, verrors.VegaBackend_InvalidParameter_RequestBody)
	assert.Contains(t, httpErr.BaseError.ErrorDetails, "cannot contain null fields")
}

func TestResourceServiceCreate(t *testing.T) {
	t.Run("create dataset category", func(t *testing.T) {
		rs, mockRA, mockPS, mockDS, _, mockCS, _ := newTestService(t)
		expectResourceServiceTransaction(t, rs, true)
		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockCS.EXPECT().CheckExistByID(gomock.Any(), gomock.Any()).Return(true, nil)
		mockRA.EXPECT().Create(gomock.Any(), gomock.Not(nil), gomock.Any()).Return(nil)
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
	})
	t.Run("create success", func(t *testing.T) {
		rs, mockRA, mockPS, _, _, mockCS, _ := newTestService(t)
		expectResourceServiceTransaction(t, rs, true)
		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockCS.EXPECT().CheckExistByID(gomock.Any(), gomock.Any()).Return(true, nil)
		mockRA.EXPECT().Create(gomock.Any(), gomock.Not(nil), gomock.Any()).Return(nil)
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
	})
	t.Run("create with explicit id", func(t *testing.T) {
		rs, mockRA, mockPS, _, _, mockCS, _ := newTestService(t)
		expectResourceServiceTransaction(t, rs, true)
		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockCS.EXPECT().CheckExistByID(gomock.Any(), gomock.Any()).Return(true, nil)
		mockRA.EXPECT().Create(gomock.Any(), gomock.Not(nil), gomock.Any()).Return(nil)
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
	})
	t.Run("create dberror", func(t *testing.T) {
		rs, mockRA, mockPS, _, _, mockCS, _ := newTestService(t)
		expectResourceServiceTransaction(t, rs, false)
		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockCS.EXPECT().CheckExistByID(gomock.Any(), gomock.Any()).Return(true, nil)
		mockRA.EXPECT().Create(gomock.Any(), gomock.Not(nil), gomock.Any()).Return(fmt.Errorf("db error"))

		_, err := rs.Create(context.Background(), &interfaces.ResourceRequest{
			Name: "test-resource",
		})
		if err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("create rejects missing feature embedding model ID", func(t *testing.T) {
		rs, _, mockPS, _, _, mockCS, _ := newTestService(t)
		ctrl := gomock.NewController(t)
		mockMFS := vmock.NewMockModelFactoryService(ctrl)
		rs.mfs = mockMFS
		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockCS.EXPECT().CheckExistByID(gomock.Any(), "cat1").Return(true, nil)
		mockMFS.EXPECT().GetModelByID(gomock.Any(), "missing-model-id").Return(nil, fmt.Errorf("model not found"))

		_, err := rs.Create(context.Background(), &interfaces.ResourceRequest{
			CatalogID:        "cat1",
			Name:             "table",
			Category:         interfaces.ResourceCategoryTable,
			SourceIdentifier: "public.orders",
			SchemaDefinition: []*interfaces.Property{
				{
					Name: "title",
					Features: []interfaces.PropertyFeature{
						{
							FeatureType: interfaces.PropertyFeatureType_Vector,
							RefProperty: "title",
							Config:      map[string]any{"embedding_model": "missing-model-id"},
						},
					},
				},
			},
		})

		httpErr, ok := err.(*rest.HTTPError)
		if !ok {
			t.Fatalf("expected HTTPError, got %T", err)
		}
		if httpErr.HTTPCode != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", httpErr.HTTPCode)
		}
		if httpErr.BaseError.ErrorCode != verrors.VegaBackend_InvalidParameter_RequestBody {
			t.Fatalf("expected %s, got %s", verrors.VegaBackend_InvalidParameter_RequestBody, httpErr.BaseError.ErrorCode)
		}
	})
	t.Run("create rejects vector feature without an embedding model", func(t *testing.T) {
		rs, _, mockPS, _, _, mockCS, _ := newTestService(t)
		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockCS.EXPECT().CheckExistByID(gomock.Any(), "cat1").Return(true, nil)

		_, err := rs.Create(context.Background(), &interfaces.ResourceRequest{
			CatalogID:        "cat1",
			Name:             "table",
			Category:         interfaces.ResourceCategoryTable,
			SourceIdentifier: "public.orders",
			SchemaDefinition: []*interfaces.Property{{
				Name: "title",
				Features: []interfaces.PropertyFeature{{
					FeatureType: interfaces.PropertyFeatureType_Vector,
					RefProperty: "title",
				}},
			}},
		})

		httpErr, ok := err.(*rest.HTTPError)
		if !ok {
			t.Fatalf("expected HTTPError, got %T", err)
		}
		if httpErr.HTTPCode != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", httpErr.HTTPCode)
		}
		if httpErr.BaseError.ErrorCode != verrors.VegaBackend_InvalidParameter_RequestBody {
			t.Fatalf("expected %s, got %s", verrors.VegaBackend_InvalidParameter_RequestBody, httpErr.BaseError.ErrorCode)
		}
		if !strings.Contains(httpErr.Error(), "embedding model is required") {
			t.Fatalf("expected actionable missing-model error, got %v", httpErr)
		}
	})
	t.Run("create internal catalog resource uses internal auth type", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockRA := vmock.NewMockResourceAccess(ctrl)
		mockPS := vmock.NewMockPermissionService(ctrl)
		mockCS := vmock.NewMockCatalogService(ctrl)
		rs := &resourceService{ra: mockRA, ps: mockPS, cs: mockCS}
		expectResourceServiceTransaction(t, rs, true)

		mockCS.EXPECT().InternalCatalogIDSet(gomock.Any()).Return(map[string]struct{}{"cat-internal": {}}, nil)
		// 建表只判目标目录的 resource_manage（#801）：内部目录下的资源判
		// internal_catalog，与 resourceAuthResourceType 的分型对称。
		mockPS.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
			Type: interfaces.AUTH_RESOURCE_TYPE_INTERNAL_CATALOG,
			ID:   "cat-internal",
		}, []string{interfaces.OPERATION_TYPE_RESOURCE_MANAGE}).Return(nil)
		mockCS.EXPECT().CheckExistByID(gomock.Any(), "cat-internal").Return(true, nil)
		mockRA.EXPECT().Create(gomock.Any(), gomock.Not(nil), gomock.Any()).Return(nil)
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
	})
}

// expectDeleteGrantedByCatalog 把「有权删这批表」这件事装配成它现在真实的样子:
// delete 已经从资源类型的词表里撤掉,资源侧一次都不问,权限来自表所在的目录。
// newTestService 已经默认 stub 了 InternalCatalogIDSet(无内部目录),这里不再重复声明。
func expectDeleteGrantedByCatalog(mockRA *vmock.MockResourceAccess,
	mockPS *vmock.MockPermissionService, ids []string, catalogID string) {

	refs := make([]interfaces.ResourcePermissionRef, 0, len(ids))
	granted := make(map[string]interfaces.PermissionResourceOps, 1)
	for _, id := range ids {
		refs = append(refs, interfaces.ResourcePermissionRef{ResourceID: id, CatalogID: catalogID})
	}
	granted[catalogID] = interfaces.PermissionResourceOps{
		ResourceID: catalogID,
		Operations: []string{interfaces.OPERATION_TYPE_RESOURCE_MANAGE},
	}
	mockRA.EXPECT().GetPermissionRefsByIDs(gomock.Any(), ids).Return(refs, nil)
	mockPS.EXPECT().FilterResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_CATALOG,
		[]string{catalogID}, []string{interfaces.OPERATION_TYPE_RESOURCE_MANAGE}, true, gomock.Any()).
		Return(granted, nil)
}

func TestResourceServiceDeleteByIDs(t *testing.T) {
	t.Run("delete by ids empty", func(t *testing.T) {
		rs, _, _, _, _, _, _ := newTestService(t)
		err := rs.DeleteByIDs(context.Background(), []string{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("delete by ids success", func(t *testing.T) {
		rs, mockRA, mockPS, _, _, _, mockBTA := newTestService(t)
		ctrl := gomock.NewController(t)
		mockLIM := vmock.NewMockLocalIndexManager(ctrl)
		rs.lim = mockLIM
		expectDeleteGrantedByCatalog(mockRA, mockPS, []string{"r1"}, "cat1")
		mockRA.EXPECT().GetByIDs(gomock.Any(), []string{"r1"}).
			Return([]*interfaces.Resource{{ID: "r1", Category: "table", LocalIndexName: "vega-build-r1-t1"}}, nil)
		expectResourceBuildTasksForDelete(t, mockBTA, "r1", nil)
		mockRA.EXPECT().DeleteByIDs(gomock.Any(), []string{"r1"}).Return(nil)
		mockPS.EXPECT().DeleteResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_RESOURCE, []string{"r1"}).Return(nil)
		err := rs.DeleteByIDs(context.Background(), []string{"r1"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("rejects deletion while resource refresh is pending or running", func(t *testing.T) {
		rs, mockRA, mockPS, _, _, _, _ := newTestService(t)
		ctrl := gomock.NewController(t)
		mockDTA := vmock.NewMockDiscoverTaskAccess(ctrl)
		rs.dta = mockDTA
		expectDeleteGrantedByCatalog(mockRA, mockPS, []string{"r1"}, "cat1")
		mockRA.EXPECT().GetByIDs(gomock.Any(), []string{"r1"}).
			Return([]*interfaces.Resource{{ID: "r1"}}, nil)
		mockDTA.EXPECT().InternalList(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, params interfaces.DiscoverTaskQueryParams) ([]*interfaces.DiscoverTaskSummary, error) {
				assert.Equal(t, "r1", params.ResourceID)
				assert.Equal(t, []string{interfaces.DiscoverTaskStatusPending, interfaces.DiscoverTaskStatusRunning}, params.Statuses)
				assert.Equal(t, 1, params.Limit)
				return []*interfaces.DiscoverTaskSummary{{ID: "discover-1", ResourceID: "r1"}}, nil
			})

		httpErr := requireResourceHTTPError(t, rs.DeleteByIDs(context.Background(), []string{"r1"}),
			verrors.VegaBackend_DiscoverTask_ResourceRefreshInProgress)
		assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
	})
	t.Run("deletes resource before dataset", func(t *testing.T) {
		rs, mockRA, mockPS, mockDS, _, _, mockBTA := newTestService(t)
		expectDeleteGrantedByCatalog(mockRA, mockPS, []string{"r1"}, "cat1")
		gomock.InOrder(
			mockRA.EXPECT().GetByIDs(gomock.Any(), []string{"r1"}).
				Return([]*interfaces.Resource{{ID: "r1", Category: interfaces.ResourceCategoryDataset}}, nil),
			mockBTA.EXPECT().InternalList(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, params interfaces.BuildTasksQueryParams) ([]*interfaces.BuildTaskSummary, error) {
					assert.Equal(t, "r1", params.ResourceID)
					assert.Equal(t, []string{interfaces.BuildTaskStatusRunning, interfaces.BuildTaskStatusStopping}, params.Statuses)
					assert.Equal(t, 1, params.Limit)
					return nil, nil
				}),
			mockRA.EXPECT().DeleteByIDs(gomock.Any(), []string{"r1"}).Return(nil),
			mockDS.EXPECT().Delete(gomock.Any(), "r1").Return(nil),
			mockPS.EXPECT().DeleteResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_RESOURCE, []string{"r1"}).Return(nil),
		)

		require.NoError(t, rs.DeleteByIDs(context.Background(), []string{"r1"}))
	})
	t.Run("does not delete dataset when resource deletion fails", func(t *testing.T) {
		rs, mockRA, mockPS, _, _, _, mockBTA := newTestService(t)
		expectDeleteGrantedByCatalog(mockRA, mockPS, []string{"r1"}, "cat1")
		mockRA.EXPECT().GetByIDs(gomock.Any(), []string{"r1"}).
			Return([]*interfaces.Resource{{ID: "r1", Category: interfaces.ResourceCategoryDataset}}, nil)
		expectResourceBuildTasksForDelete(t, mockBTA, "r1", nil)
		mockRA.EXPECT().DeleteByIDs(gomock.Any(), []string{"r1"}).Return(errors.New("delete resource failed"))

		err := rs.DeleteByIDs(context.Background(), []string{"r1"})
		require.Error(t, err)
	})
	t.Run("rejects deletion while build task is active", func(t *testing.T) {
		rs, mockRA, mockPS, _, _, _, mockBTA := newTestService(t)
		expectDeleteGrantedByCatalog(mockRA, mockPS, []string{"r1"}, "cat1")
		mockRA.EXPECT().GetByIDs(gomock.Any(), []string{"r1"}).
			Return([]*interfaces.Resource{{ID: "r1"}}, nil)
		expectResourceBuildTasksForDelete(t, mockBTA, "r1", []*interfaces.BuildTaskSummary{{
			ID: "task-1", ResourceID: "r1", Status: interfaces.BuildTaskStatusRunning,
		}})

		httpErr := requireResourceHTTPError(t, rs.DeleteByIDs(context.Background(), []string{"r1"}), verrors.VegaBackend_BuildTask_HasRunningExecution)
		assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
	})
	t.Run("allows deletion while build task is pending", func(t *testing.T) {
		rs, mockRA, mockPS, _, _, _, mockBTA := newTestService(t)
		expectDeleteGrantedByCatalog(mockRA, mockPS, []string{"r1"}, "cat1")
		mockRA.EXPECT().GetByIDs(gomock.Any(), []string{"r1"}).
			Return([]*interfaces.Resource{{ID: "r1"}}, nil)
		// The access query excludes pending tasks when deleting a resource.
		expectResourceBuildTasksForDelete(t, mockBTA, "r1", nil)
		mockRA.EXPECT().DeleteByIDs(gomock.Any(), []string{"r1"}).Return(nil)
		mockPS.EXPECT().DeleteResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_RESOURCE, []string{"r1"}).Return(nil)

		require.NoError(t, rs.DeleteByIDs(context.Background(), []string{"r1"}))
	})
}

func expectResourceBuildTasksForDelete(t *testing.T, mockBTA *vmock.MockBuildTaskAccess,
	resourceID string, tasks []*interfaces.BuildTaskSummary) {
	t.Helper()
	mockBTA.EXPECT().InternalList(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, params interfaces.BuildTasksQueryParams) ([]*interfaces.BuildTaskSummary, error) {
			assert.Equal(t, resourceID, params.ResourceID)
			assert.Equal(t, []string{interfaces.BuildTaskStatusRunning, interfaces.BuildTaskStatusStopping}, params.Statuses)
			assert.Equal(t, 1, params.Limit)
			return tasks, nil
		})
}

func TestResourceServiceRejectBuildRelevantUpdateWhenActiveBuildTask(t *testing.T) {
	t.Run("rejects pending task when requested", func(t *testing.T) {
		rs, _, _, _, _, _, mockBTA := newTestService(t)
		mockBTA.EXPECT().InternalList(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, params interfaces.BuildTasksQueryParams) ([]*interfaces.BuildTaskSummary, error) {
				assert.Equal(t, "r1", params.ResourceID)
				assert.Equal(t, []string{
					interfaces.BuildTaskStatusPending,
					interfaces.BuildTaskStatusRunning,
					interfaces.BuildTaskStatusStopping,
				}, params.Statuses)
				return []*interfaces.BuildTaskSummary{{Status: interfaces.BuildTaskStatusPending}}, nil
			})

		httpErr := requireResourceHTTPError(t,
			rs.rejectResourceOperationWhenActiveBuildTask(context.Background(), "r1", true),
			verrors.VegaBackend_BuildTask_Exist)
		assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
	})

	t.Run("excludes pending task when not requested", func(t *testing.T) {
		rs, _, _, _, _, _, mockBTA := newTestService(t)
		mockBTA.EXPECT().InternalList(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, params interfaces.BuildTasksQueryParams) ([]*interfaces.BuildTaskSummary, error) {
				assert.Equal(t, []string{
					interfaces.BuildTaskStatusRunning,
					interfaces.BuildTaskStatusStopping,
				}, params.Statuses)
				return nil, nil
			})

		require.NoError(t, rs.rejectResourceOperationWhenActiveBuildTask(context.Background(), "r1", false))
	})
}

func TestResourceServiceUpdateStatus(t *testing.T) {
	t.Run("update status success", func(t *testing.T) {
		rs, mockRA, _, _, _, _, _ := newTestService(t)
		mockRA.EXPECT().UpdateStatus(gomock.Any(), nil, "r1", "active", "").Return(nil)

		err := rs.UpdateStatus(context.Background(), "r1", "active", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("update status error", func(t *testing.T) {
		rs, mockRA, _, _, _, _, _ := newTestService(t)
		mockRA.EXPECT().UpdateStatus(gomock.Any(), nil, "r1", "active", "").
			Return(fmt.Errorf("db error"))

		err := rs.UpdateStatus(context.Background(), "r1", "active", "")
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestResourceServiceUpdateDiscoverStatus(t *testing.T) {
	t.Run("update discover status success", func(t *testing.T) {
		rs, mockRA, _, _, _, _, _ := newTestService(t)
		mockRA.EXPECT().UpdateDiscoverStatus(gomock.Any(), "r1", interfaces.DiscoverStatusUpdated).Return(nil)

		err := rs.UpdateDiscoverStatus(context.Background(), "r1", interfaces.DiscoverStatusUpdated)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("update discover status error", func(t *testing.T) {
		rs, mockRA, _, _, _, _, _ := newTestService(t)
		mockRA.EXPECT().UpdateDiscoverStatus(gomock.Any(), "r1", interfaces.DiscoverStatusUpdated).
			Return(fmt.Errorf("db error"))

		err := rs.UpdateDiscoverStatus(context.Background(), "r1", interfaces.DiscoverStatusUpdated)
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestResourceServiceUpdate(t *testing.T) {
	t.Run("update nil resource", func(t *testing.T) {
		rs, _, _, _, _, _, _ := newTestService(t)
		err := rs.Update(context.Background(), nil, &interfaces.ResourceRequest{})
		if err == nil {
			t.Fatal("expected error for nil resource")
		}
	})
	t.Run("update success", func(t *testing.T) {
		rs, mockRA, mockPS, _, _, mockCS, _ := newTestService(t)
		expectResourceServiceTransaction(t, rs, true)
		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockCS.EXPECT().CheckExistByID(gomock.Any(), gomock.Any()).Return(true, nil)
		mockRA.EXPECT().Update(gomock.Any(), gomock.Not(nil), gomock.Any(), int64(0)).Return(int64(1), nil)

		err := rs.Update(context.Background(), &interfaces.Resource{ID: "r1", CatalogID: "cat1", Name: "updated", Category: interfaces.ResourceCategoryTable}, &interfaces.ResourceRequest{
			CatalogID: "cat1",
			Name:      "updated",
			Category:  interfaces.ResourceCategoryTable,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("returns conflict for stale resource", func(t *testing.T) {
		rs, mockRA, mockPS, _, _, mockCS, _ := newTestService(t)
		expectResourceServiceTransaction(t, rs, false)
		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockCS.EXPECT().CheckExistByID(gomock.Any(), gomock.Any()).Return(true, nil)
		expectedUpdateTime := int64(42)
		mockRA.EXPECT().Update(gomock.Any(), gomock.Not(nil), gomock.Any(), expectedUpdateTime).
			DoAndReturn(func(_ context.Context, _ *sql.Tx, resource *interfaces.Resource, expected int64) (int64, error) {
				assert.Equal(t, expectedUpdateTime, expected)
				assert.Greater(t, resource.UpdateTime, expectedUpdateTime)
				return 0, nil
			})

		err := rs.Update(context.Background(), &interfaces.Resource{ID: "r1", CatalogID: "cat1", Name: "updated", Category: interfaces.ResourceCategoryTable}, &interfaces.ResourceRequest{
			CatalogID:          "cat1",
			Name:               "updated",
			Category:           interfaces.ResourceCategoryTable,
			ExpectedUpdateTime: expectedUpdateTime,
		})

		var httpErr *rest.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Resource_UpdateConflict, httpErr.BaseError.ErrorCode)
	})
	t.Run("returns conflict when no resource is updated", func(t *testing.T) {
		rs, mockRA, mockPS, _, _, mockCS, _ := newTestService(t)
		expectResourceServiceTransaction(t, rs, false)
		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockCS.EXPECT().CheckExistByID(gomock.Any(), gomock.Any()).Return(true, nil)
		mockRA.EXPECT().Update(gomock.Any(), gomock.Not(nil), gomock.Any(), int64(0)).Return(int64(0), nil)

		err := rs.Update(context.Background(), &interfaces.Resource{ID: "r1", CatalogID: "cat1", Name: "updated", Category: interfaces.ResourceCategoryTable}, &interfaces.ResourceRequest{
			CatalogID: "cat1",
			Name:      "updated",
			Category:  interfaces.ResourceCategoryTable,
		})

		var httpErr *rest.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Resource_UpdateConflict, httpErr.BaseError.ErrorCode)
	})
	t.Run("update rejects build relevant change when active build task exists", func(t *testing.T) {
		rs, _, mockPS, _, _, _, mockBTA := newTestService(t)
		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockBTA.EXPECT().InternalList(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, params interfaces.BuildTasksQueryParams) ([]*interfaces.BuildTaskSummary, error) {
				if params.ResourceID != "r1" {
					t.Fatalf("expected resource r1, got %q", params.ResourceID)
				}
				return []*interfaces.BuildTaskSummary{{
					ID:         "task-1",
					ResourceID: "r1",
					Status:     interfaces.BuildTaskStatusRunning,
				}}, nil
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
			Category:         interfaces.ResourceCategoryTable,
			Name:             "table",
			SourceIdentifier: "public.orders",
			SchemaDefinition: []*interfaces.Property{{
				Name: "id",
				Type: interfaces.DataType_String,
				Features: []interfaces.PropertyFeature{{
					FeatureName: "fulltext",
					FeatureType: interfaces.PropertyFeatureType_Fulltext,
				}},
			}},
		})

		httpErr, ok := err.(*rest.HTTPError)
		if !ok {
			t.Fatalf("expected HTTPError, got %T", err)
		}
		if httpErr.HTTPCode != http.StatusConflict {
			t.Fatalf("expected 409, got %d", httpErr.HTTPCode)
		}
		if httpErr.BaseError.ErrorCode != verrors.VegaBackend_BuildTask_HasRunningExecution {
			t.Fatalf("expected %s, got %s", verrors.VegaBackend_BuildTask_HasRunningExecution, httpErr.BaseError.ErrorCode)
		}
	})
	t.Run("update allows non build relevant change when active build task exists", func(t *testing.T) {
		rs, mockRA, mockPS, _, _, mockCS, _ := newTestService(t)
		expectResourceServiceTransaction(t, rs, true)
		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockCS.EXPECT().CheckExistByID(gomock.Any(), "cat1").Return(true, nil)
		mockRA.EXPECT().Update(gomock.Any(), gomock.Not(nil), gomock.Any(), int64(0)).
			DoAndReturn(func(_ context.Context, _ *sql.Tx, got *interfaces.Resource, _ int64) (int64, error) {
				if got.LocalIndexName != "vega-build-r1-task-1" {
					t.Fatalf("expected LocalIndexName to be preserved, got %q", got.LocalIndexName)
				}
				return 1, nil
			})
		err := rs.Update(context.Background(), &interfaces.Resource{
			ID:               "r1",
			CatalogID:        "cat1",
			Category:         interfaces.ResourceCategoryTable,
			Name:             "table",
			Description:      "old",
			LocalIndexName:   "vega-build-r1-task-1",
			LocalIndexStatus: interfaces.ResourceLocalIndexStatusAvailable,
			SyncMark:         `{"mode":"batch","cursor":[]}`,
			SourceIdentifier: "public.orders",
			SchemaDefinition: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_String}},
		}, &interfaces.ResourceRequest{
			CatalogID:        "cat1",
			Category:         interfaces.ResourceCategoryTable,
			Name:             "table",
			Description:      "new",
			SourceIdentifier: "public.orders",
			SchemaDefinition: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_String}},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("update marks available local index stale when build relevant fields change", func(t *testing.T) {
		rs, mockRA, mockPS, _, _, mockCS, mockBTA := newTestService(t)
		expectResourceServiceTransaction(t, rs, true)
		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockBTA.EXPECT().InternalList(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, params interfaces.BuildTasksQueryParams) ([]*interfaces.BuildTaskSummary, error) {
				if params.ResourceID != "r1" {
					t.Fatalf("expected resource r1, got %q", params.ResourceID)
				}
				return nil, nil
			})
		mockCS.EXPECT().CheckExistByID(gomock.Any(), "cat1").Return(true, nil)
		mockRA.EXPECT().Update(gomock.Any(), gomock.Not(nil), gomock.Any(), int64(0)).
			DoAndReturn(func(_ context.Context, _ *sql.Tx, got *interfaces.Resource, _ int64) (int64, error) {
				if got.LocalIndexName != "vega-build-r1-task-1" {
					t.Fatalf("expected LocalIndexName to be preserved, got %q", got.LocalIndexName)
				}
				if len(got.SchemaDefinition) != 1 || len(got.SchemaDefinition[0].Features) != 1 {
					t.Fatalf("expected updated schema features, got %#v", got.SchemaDefinition)
				}
				return 1, nil
			})
		mockRA.EXPECT().UpdateLocalIndexState(
			gomock.Any(), gomock.Not(nil), "r1",
			interfaces.ResourceLocalIndexStatusStale,
			"vega-build-r1-task-1", "",
		).Return(true, nil)
		err := rs.Update(context.Background(), &interfaces.Resource{
			ID:               "r1",
			CatalogID:        "cat1",
			Category:         interfaces.ResourceCategoryTable,
			Name:             "table",
			LocalIndexStatus: interfaces.ResourceLocalIndexStatusAvailable,
			LocalIndexName:   "vega-build-r1-task-1",
			SyncMark:         `{"mode":"batch","cursor":[1]}`,
			SourceIdentifier: "public.orders",
			SchemaDefinition: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_String}},
		}, &interfaces.ResourceRequest{
			CatalogID:        "cat1",
			Category:         interfaces.ResourceCategoryTable,
			Name:             "table",
			SourceIdentifier: "public.orders",
			SchemaDefinition: []*interfaces.Property{{
				Name: "id",
				Type: interfaces.DataType_String,
				Features: []interfaces.PropertyFeature{{
					FeatureName: "fulltext",
					FeatureType: interfaces.PropertyFeatureType_Fulltext,
				}},
			}},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("update rejects index config change when active build task exists", func(t *testing.T) {
		rs, _, mockPS, _, _, _, mockBTA := newTestService(t)
		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockBTA.EXPECT().InternalList(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, params interfaces.BuildTasksQueryParams) ([]*interfaces.BuildTaskSummary, error) {
				if params.ResourceID != "r1" {
					t.Fatalf("expected resource r1, got %q", params.ResourceID)
				}
				return []*interfaces.BuildTaskSummary{{
					ID:         "task-1",
					ResourceID: "r1",
					Status:     interfaces.BuildTaskStatusRunning,
				}}, nil
			})

		err := rs.Update(context.Background(), &interfaces.Resource{
			ID:               "r1",
			CatalogID:        "cat1",
			Category:         interfaces.ResourceCategoryTable,
			Name:             "table",
			SourceIdentifier: "public.orders",
			IndexConfig: &interfaces.ResourceIndexConfig{
				PrimaryKeyFields:  []string{"id"},
				IncrementalFields: []string{"id"},
			},
		}, &interfaces.ResourceRequest{
			CatalogID:        "cat1",
			Category:         interfaces.ResourceCategoryTable,
			Name:             "table",
			SourceIdentifier: "public.orders",
			IndexConfig: &interfaces.ResourceIndexConfig{
				PrimaryKeyFields:  []string{"id"},
				IncrementalFields: []string{"updated_at", "id"},
			},
		})

		httpErr, ok := err.(*rest.HTTPError)
		if !ok {
			t.Fatalf("expected HTTPError, got %T", err)
		}
		if httpErr.HTTPCode != http.StatusConflict {
			t.Fatalf("expected 409, got %d", httpErr.HTTPCode)
		}
		if httpErr.BaseError.ErrorCode != verrors.VegaBackend_BuildTask_HasRunningExecution {
			t.Fatalf("expected %s, got %s", verrors.VegaBackend_BuildTask_HasRunningExecution, httpErr.BaseError.ErrorCode)
		}
	})
	t.Run("update clears local index name when index config changes", func(t *testing.T) {
		rs, mockRA, mockPS, _, _, mockCS, mockBTA := newTestService(t)
		expectResourceServiceTransaction(t, rs, true)
		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockBTA.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return(nil, nil)
		mockCS.EXPECT().CheckExistByID(gomock.Any(), "cat1").Return(true, nil)
		mockRA.EXPECT().Update(gomock.Any(), gomock.Not(nil), gomock.Any(), int64(0)).
			DoAndReturn(func(_ context.Context, _ *sql.Tx, got *interfaces.Resource, _ int64) (int64, error) {
				if got.LocalIndexName != "vega-build-r1-task-1" {
					t.Fatalf("expected LocalIndexName to be preserved, got %q", got.LocalIndexName)
				}
				if got.IndexConfig == nil || len(got.IndexConfig.IncrementalFields) != 2 {
					t.Fatalf("expected updated index config, got %#v", got.IndexConfig)
				}
				return 1, nil
			})
		mockRA.EXPECT().UpdateLocalIndexState(gomock.Any(), gomock.Not(nil), "r1",
			interfaces.ResourceLocalIndexStatusAvailable, "vega-build-r1-task-1", "").Return(true, nil)
		err := rs.Update(context.Background(), &interfaces.Resource{
			ID:               "r1",
			CatalogID:        "cat1",
			Category:         interfaces.ResourceCategoryTable,
			Name:             "table",
			LocalIndexName:   "vega-build-r1-task-1",
			LocalIndexStatus: interfaces.ResourceLocalIndexStatusAvailable,
			SyncMark:         `{"mode":"batch","cursor":[]}`,
			SourceIdentifier: "public.orders",
			SchemaDefinition: []*interfaces.Property{
				{Name: "id", Type: interfaces.DataType_Integer},
				{Name: "updated_at", Type: interfaces.DataType_Timestamp},
			},
			IndexConfig: &interfaces.ResourceIndexConfig{
				PrimaryKeyFields:  []string{"id"},
				IncrementalFields: []string{"id"},
			},
		}, &interfaces.ResourceRequest{
			CatalogID:        "cat1",
			Category:         interfaces.ResourceCategoryTable,
			Name:             "table",
			SourceIdentifier: "public.orders",
			IndexConfig: &interfaces.ResourceIndexConfig{
				PrimaryKeyFields:        []string{"id"},
				IncrementalFields:       []string{"updated_at", "id"},
				DefaultFulltextAnalyzer: "ik_max_word",
				DefaultEmbeddingModel:   "embedding",
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("update rejects missing default embedding model ID", func(t *testing.T) {
		rs, _, mockPS, _, _, _, mockBTA := newTestService(t)
		ctrl := gomock.NewController(t)
		mockMFS := vmock.NewMockModelFactoryService(ctrl)
		rs.mfs = mockMFS
		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockBTA.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return(nil, nil)
		mockMFS.EXPECT().GetModelByID(gomock.Any(), "missing-model-id").Return(nil, fmt.Errorf("model not found"))

		err := rs.Update(context.Background(), &interfaces.Resource{
			ID:               "r1",
			CatalogID:        "cat1",
			Category:         interfaces.ResourceCategoryTable,
			Name:             "table",
			SourceIdentifier: "public.orders",
			SchemaDefinition: []*interfaces.Property{
				{
					Name: "title",
					Features: []interfaces.PropertyFeature{
						{FeatureType: interfaces.PropertyFeatureType_Vector, RefProperty: "title"},
					},
				},
			},
		}, &interfaces.ResourceRequest{
			CatalogID:        "cat1",
			Category:         interfaces.ResourceCategoryTable,
			Name:             "table",
			SourceIdentifier: "public.orders",
			IndexConfig: &interfaces.ResourceIndexConfig{
				DefaultEmbeddingModel: "missing-model-id",
			},
		})

		httpErr, ok := err.(*rest.HTTPError)
		if !ok {
			t.Fatalf("expected HTTPError, got %T", err)
		}
		if httpErr.HTTPCode != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", httpErr.HTTPCode)
		}
		if httpErr.BaseError.ErrorCode != verrors.VegaBackend_InvalidParameter_RequestBody {
			t.Fatalf("expected %s, got %s", verrors.VegaBackend_InvalidParameter_RequestBody, httpErr.BaseError.ErrorCode)
		}
	})
	t.Run("update allows unused default embedding model", func(t *testing.T) {
		rs, mockRA, mockPS, _, _, mockCS, mockBTA := newTestService(t)
		expectResourceServiceTransaction(t, rs, true)
		ctrl := gomock.NewController(t)
		mockMFS := vmock.NewMockModelFactoryService(ctrl)
		rs.mfs = mockMFS
		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockBTA.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return(nil, nil)
		mockCS.EXPECT().CheckExistByID(gomock.Any(), "cat1").Return(true, nil)
		mockRA.EXPECT().Update(gomock.Any(), gomock.Not(nil), gomock.Any(), int64(0)).Return(int64(1), nil)
		err := rs.Update(context.Background(), &interfaces.Resource{
			ID:               "r1",
			CatalogID:        "cat1",
			Category:         interfaces.ResourceCategoryTable,
			Name:             "table",
			SourceIdentifier: "public.orders",
			SchemaDefinition: []*interfaces.Property{
				{
					Name: "title",
					Features: []interfaces.PropertyFeature{
						{FeatureType: interfaces.PropertyFeatureType_Fulltext, RefProperty: "title"},
					},
				},
			},
		}, &interfaces.ResourceRequest{
			CatalogID:        "cat1",
			Category:         interfaces.ResourceCategoryTable,
			Name:             "table",
			SourceIdentifier: "public.orders",
			IndexConfig: &interfaces.ResourceIndexConfig{
				DefaultEmbeddingModel: "missing-model",
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("update allows schema display fields without clearing local index", func(t *testing.T) {
		rs, mockRA, mockPS, _, _, mockCS, _ := newTestService(t)
		expectResourceServiceTransaction(t, rs, true)
		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockCS.EXPECT().CheckExistByID(gomock.Any(), "cat1").Return(true, nil)
		mockRA.EXPECT().Update(gomock.Any(), gomock.Not(nil), gomock.Any(), int64(0)).
			DoAndReturn(func(_ context.Context, _ *sql.Tx, got *interfaces.Resource, _ int64) (int64, error) {
				if got.LocalIndexName != "vega-build-r1-task-1" {
					t.Fatalf("expected LocalIndexName to be preserved, got %q", got.LocalIndexName)
				}
				if got.SchemaDefinition[0].DisplayName != "Order ID" || got.SchemaDefinition[0].Description != "business id" {
					t.Fatalf("schema display fields were not updated: %#v", got.SchemaDefinition[0])
				}
				return 1, nil
			})
		err := rs.Update(context.Background(), &interfaces.Resource{
			ID:               "r1",
			CatalogID:        "cat1",
			Category:         interfaces.ResourceCategoryTable,
			Name:             "table",
			LocalIndexName:   "vega-build-r1-task-1",
			SourceIdentifier: "public.orders",
			SchemaDefinition: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_String}},
		}, &interfaces.ResourceRequest{
			CatalogID:        "cat1",
			Category:         interfaces.ResourceCategoryTable,
			Name:             "table",
			SourceIdentifier: "public.orders",
			SchemaDefinition: []*interfaces.Property{{
				Name:        "id",
				DisplayName: "Order ID",
				Type:        interfaces.DataType_String,
				Description: "business id",
			}},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("update ignores source managed field changes", func(t *testing.T) {
		rs, mockRA, mockPS, _, _, mockCS, _ := newTestService(t)
		expectResourceServiceTransaction(t, rs, true)
		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockCS.EXPECT().CheckExistByID(gomock.Any(), "cat1").Return(true, nil)
		mockRA.EXPECT().Update(gomock.Any(), gomock.Not(nil), gomock.Any(), int64(0)).
			DoAndReturn(func(_ context.Context, _ *sql.Tx, resource *interfaces.Resource, _ int64) (int64, error) {
				assert.Equal(t, "public.orders", resource.SourceIdentifier)
				assert.Equal(t, map[string]any{"owner": "discovery"}, resource.SourceMetadata)
				assert.Equal(t, "public", resource.Schema)
				assert.Equal(t, interfaces.ResourceStatusActive, resource.Status)
				return 1, nil
			})

		err := rs.Update(context.Background(), &interfaces.Resource{
			ID:               "r1",
			CatalogID:        "cat1",
			Category:         interfaces.ResourceCategoryTable,
			Name:             "table",
			Status:           interfaces.ResourceStatusActive,
			Schema:           "public",
			SourceIdentifier: "public.orders",
			SourceMetadata:   map[string]any{"owner": "discovery"},
			SchemaDefinition: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_String}},
		}, &interfaces.ResourceRequest{
			CatalogID:        "cat1",
			Category:         interfaces.ResourceCategoryTable,
			Name:             "table",
			Status:           interfaces.ResourceStatusStale,
			Schema:           "archive",
			SourceIdentifier: "public.customers",
			SourceMetadata:   map[string]any{"owner": "request"},
		})

		require.NoError(t, err)
	})
	t.Run("update rejects catalog change", func(t *testing.T) {
		rs, _, mockPS, _, _, _, _ := newTestService(t)
		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

		err := rs.Update(context.Background(), &interfaces.Resource{
			ID:        "r1",
			CatalogID: "cat1",
			Category:  interfaces.ResourceCategoryTable,
		}, &interfaces.ResourceRequest{
			CatalogID: "cat2",
			Category:  interfaces.ResourceCategoryTable,
		})

		httpErr, ok := err.(*rest.HTTPError)
		require.True(t, ok)
		assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
	})
	t.Run("update requires category", func(t *testing.T) {
		rs, _, mockPS, _, _, _, _ := newTestService(t)
		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

		err := rs.Update(context.Background(), &interfaces.Resource{
			ID:        "r1",
			CatalogID: "cat1",
			Category:  interfaces.ResourceCategoryDataset,
		}, &interfaces.ResourceRequest{})

		httpErr, ok := err.(*rest.HTTPError)
		if !ok {
			t.Fatalf("expected HTTPError, got %T", err)
		}
		if httpErr.HTTPCode != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", httpErr.HTTPCode)
		}
	})
	t.Run("update rejects category change", func(t *testing.T) {
		rs, _, mockPS, _, _, _, _ := newTestService(t)
		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

		err := rs.Update(context.Background(), &interfaces.Resource{
			ID:        "r1",
			CatalogID: "cat1",
			Category:  interfaces.ResourceCategoryDataset,
		}, &interfaces.ResourceRequest{
			Category: interfaces.ResourceCategoryTable,
		})

		httpErr, ok := err.(*rest.HTTPError)
		if !ok {
			t.Fatalf("expected HTTPError, got %T", err)
		}
		if httpErr.HTTPCode != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", httpErr.HTTPCode)
		}
	})
	t.Run("update rejects schema structure changes", func(t *testing.T) {
		rs, _, mockPS, _, _, _, _ := newTestService(t)
		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

		err := rs.Update(context.Background(), &interfaces.Resource{
			ID:               "r1",
			CatalogID:        "cat1",
			Category:         interfaces.ResourceCategoryTable,
			Name:             "table",
			SourceIdentifier: "public.orders",
			SchemaDefinition: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_String}},
		}, &interfaces.ResourceRequest{
			CatalogID:        "cat1",
			Category:         interfaces.ResourceCategoryTable,
			Name:             "table",
			SourceIdentifier: "public.orders",
			SchemaDefinition: []*interfaces.Property{
				{Name: "id", Type: interfaces.DataType_String},
				{Name: "title", Type: interfaces.DataType_String},
			},
		})

		httpErr, ok := err.(*rest.HTTPError)
		if !ok {
			t.Fatalf("expected HTTPError, got %T", err)
		}
		if httpErr.HTTPCode != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", httpErr.HTTPCode)
		}
	})
	t.Run("dataset update allows adding properties", func(t *testing.T) {
		rs, mockRA, mockPS, _, _, mockCS, mockBTA := newTestService(t)
		expectResourceServiceTransaction(t, rs, true)
		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockBTA.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return(nil, nil)
		mockCS.EXPECT().CheckExistByID(gomock.Any(), "cat1").Return(true, nil)
		mockRA.EXPECT().Update(gomock.Any(), gomock.Not(nil), gomock.Any(), int64(0)).
			DoAndReturn(func(_ context.Context, _ *sql.Tx, got *interfaces.Resource, _ int64) (int64, error) {
				if got.LocalIndexName != "vega-build-r1-task-1" {
					t.Fatalf("expected LocalIndexName to be preserved, got %q", got.LocalIndexName)
				}
				if len(got.SchemaDefinition) != 2 || got.SchemaDefinition[1].Name != "title" {
					t.Fatalf("expected added dataset property, got %#v", got.SchemaDefinition)
				}
				return 1, nil
			})
		err := rs.Update(context.Background(), &interfaces.Resource{
			ID:               "r1",
			CatalogID:        "cat1",
			Category:         interfaces.ResourceCategoryDataset,
			Name:             "dataset",
			LocalIndexName:   "vega-build-r1-task-1",
			SourceIdentifier: "dataset-r1",
			SchemaDefinition: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_String}},
		}, &interfaces.ResourceRequest{
			CatalogID:        "cat1",
			Category:         interfaces.ResourceCategoryDataset,
			Name:             "dataset",
			SourceIdentifier: "dataset-r1",
			SchemaDefinition: []*interfaces.Property{
				{Name: "id", Type: interfaces.DataType_String},
				{Name: "title", Type: interfaces.DataType_Text},
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

// 删资源时任务在运行中：级联拒绝，资源不删。
func newS2STestService(t *testing.T, internalCatalogIDs []string) (
	*resourceService, *vmock.MockResourceAccess, *vmock.MockPermissionService, *vmock.MockUserMgmtService) {
	ctrl := gomock.NewController(t)
	ra := vmock.NewMockResourceAccess(ctrl)
	ps := vmock.NewMockPermissionService(ctrl)
	ums := vmock.NewMockUserMgmtService(ctrl)
	cs := vmock.NewMockCatalogService(ctrl)
	rs := &resourceService{ra: ra, ps: ps, ums: ums, cs: cs}
	internalCatalogs := make(map[string]struct{}, len(internalCatalogIDs))
	for _, id := range internalCatalogIDs {
		internalCatalogs[id] = struct{}{}
	}
	cs.EXPECT().InternalCatalogIDSet(gomock.Any()).Return(internalCatalogs, nil).AnyTimes()
	return rs, ra, ps, ums
}
