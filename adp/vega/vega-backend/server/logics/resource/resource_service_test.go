// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package resource

import (
	"context"
	"database/sql"
	"encoding/json"
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
	mockCS.EXPECT().CheckCatalogPermission(gomock.Any(), gomock.Any(),
		[]string{interfaces.OPERATION_TYPE_RESOURCE_MANAGE}, true).
		DoAndReturn(func(_ context.Context, id string, _ []string, _ bool) (bool, *interfaces.Catalog, error) {
			return true, &interfaces.Catalog{ID: id}, nil
		}).AnyTimes()
	mockDTA.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	mockPS.EXPECT().UpsertResourceParents(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	mockPS.EXPECT().DeleteResourceParents(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	return rs, mockRA, mockPS, mockDS, mockUMS, mockCS, mockBTA
}

type parentAwarePermissionService struct {
	*vmock.MockPermissionService
	upsertParentCalls int
	deleteParentCalls int
	onDeleteParents   func(resourceType string, resourceIDs []string) error
}

func (ps *parentAwarePermissionService) UpsertResourceParents(_ context.Context,
	_, _ string, _ []interfaces.PermissionResourceParent) error {
	ps.upsertParentCalls++
	return nil
}

func (ps *parentAwarePermissionService) DeleteResourceParents(_ context.Context,
	resourceType string, resourceIDs []string) error {
	ps.deleteParentCalls++
	if ps.onDeleteParents != nil {
		return ps.onDeleteParents(resourceType, resourceIDs)
	}
	return nil
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

func TestValidateSchemaDefinitionRejectsDefaultFeatureNameCollision(t *testing.T) {
	schema := []*interfaces.Property{{
		Name: "body",
		Type: interfaces.DataType_Text,
		Features: []interfaces.PropertyFeature{{
			FeatureName: interfaces.LocalIndexKeywordSubfieldName,
			FeatureType: interfaces.PropertyFeatureType_Fulltext,
		}},
	}}
	AddDefaultStringAndTextFeatures(schema, nil)

	err := validateSchemaDefinition(context.Background(), schema)

	httpErr := requireResourceHTTPError(t, err, verrors.VegaBackend_InvalidParameter_RequestBody)
	assert.Contains(t, httpErr.BaseError.ErrorDetails, `property "body" has more than one feature named "keyword"`)
}

func TestValidateSchemaDefinitionRejectsQualifiedFeatureName(t *testing.T) {
	err := validateSchemaDefinition(context.Background(), []*interfaces.Property{{
		Name: "title",
		Type: interfaces.DataType_Text,
		Features: []interfaces.PropertyFeature{{
			FeatureName: "title.keyword",
			FeatureType: interfaces.PropertyFeatureType_Keyword,
		}},
	}})

	httpErr := requireResourceHTTPError(t, err, verrors.VegaBackend_InvalidParameter_RequestBody)
	assert.Contains(t, httpErr.BaseError.ErrorDetails, `feature name "title.keyword" must be relative to property "title"`)
}

func TestValidateLocalIndexVectorOutputs(t *testing.T) {
	err := validateLocalIndexVectorOutputs(context.Background(), []*interfaces.Property{
		{
			Name: "content",
			Type: interfaces.DataType_Text,
			Features: []interfaces.PropertyFeature{{
				FeatureType: interfaces.PropertyFeatureType_Vector,
			}},
		},
		{Name: "content_vector", Type: interfaces.DataType_String},
	})

	httpErr := requireResourceHTTPError(t, err, verrors.VegaBackend_InvalidParameter_RequestBody)
	assert.Contains(t, httpErr.BaseError.ErrorDetails, `generated vector field "content_vector" conflicts with a logical property "content_vector"`)
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
		_ = requireResourceHTTPError(t, err, verrors.VegaBackend_Resource_InvalidParameter_PrimaryKeyFields)

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
	t.Run("trusted proxy cannot bypass internal resource guard", func(t *testing.T) {
		rs, mockRA, _, _ := newS2STestService(t, []string{"cat-int"})
		mockRA.EXPECT().GetByID(gomock.Any(), nil, "r1").Return(&interfaces.Resource{ID: "r1", CatalogID: "cat-int", Internal: true}, nil)

		_, err := rs.GetByID(interfaces.WithTrustedProxyRead(context.Background()), "r1")
		require.Error(t, err)
	})

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
	t.Run("does not query dataset row count for generic service reads", func(t *testing.T) {
		rs, mockRA, mockPS, _, mockUMS, _, _ := newTestService(t)
		mockRA.EXPECT().GetByID(gomock.Any(), nil, "dataset-1").
			Return(&interfaces.Resource{ID: "dataset-1", Category: interfaces.ResourceCategoryDataset}, nil)
		mockPS.EXPECT().FilterResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_RESOURCE,
			[]string{"dataset-1"}, gomock.Any(), true, gomock.Any()).
			Return(map[string]interfaces.PermissionResourceOps{
				"dataset-1": {ResourceID: "dataset-1", Operations: []string{"view_detail"}},
			}, nil)
		mockUMS.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil)

		resource, err := rs.GetByID(context.Background(), "dataset-1")

		require.NoError(t, err)
		assert.Nil(t, resource.ColumnCount)
		assert.Nil(t, resource.RowCount)
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
	t.Run("built-in admin reads internal resource through resource hierarchy", func(t *testing.T) {
		rs, ra, ps, ums := newS2STestService(t, []string{"cat-int"})
		ra.EXPECT().GetByID(gomock.Any(), nil, "r1").
			Return(&interfaces.Resource{ID: "r1", CatalogID: "cat-int", Internal: true}, nil)
		ps.EXPECT().FilterResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_RESOURCE,
			[]string{"r1"}, gomock.Any(), true, gomock.Any()).
			Return(map[string]interfaces.PermissionResourceOps{"r1": {ResourceID: "r1", Operations: interfaces.COMMON_OPERATIONS}}, nil)
		ums.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil)

		ctx := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY,
			interfaces.AccountInfo{ID: interfaces.BuiltinAdminID})
		res, err := rs.GetByID(ctx, "r1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res == nil || len(res.Operations) == 0 {
			t.Fatalf("expected operations to be filled, got %+v", res)
		}
	})
	t.Run("non-admin cannot read internal resource", func(t *testing.T) {
		rs, ra, _, _ := newS2STestService(t, []string{"cat-int"})
		ra.EXPECT().GetByID(gomock.Any(), nil, "r1").
			Return(&interfaces.Resource{ID: "r1", CatalogID: "cat-int", Internal: true}, nil)

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

		_, err := rs.GetByID(interfaces.WithS2SInternalAccess(context.Background()), "r1")
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestResourceServiceInternalGetByIDs(t *testing.T) {
	rs, mockRA, _, _, _, _, _ := newTestService(t)
	empty, err := rs.InternalGetByIDs(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, empty)

	ids := []string{"missing", "r2", "r1"}
	rows := map[string]*interfaces.Resource{
		"r1": {ID: "r1", SchemaDefinition: []*interfaces.Property{{Name: "id"}}},
		"r2": {ID: "r2"},
	}
	mockRA.EXPECT().GetByIDs(gomock.Any(), ids).Return(rows, nil)
	got, err := rs.InternalGetByIDs(context.Background(), ids)
	require.NoError(t, err)
	assert.Equal(t, rows, got)
	assert.NotContains(t, got, "missing")
	require.NotNil(t, got["r1"].ColumnCount)
	assert.Equal(t, 1, *got["r1"].ColumnCount)
}

func TestResourceServiceGetByIDs(t *testing.T) {
	t.Run("public read preserves requested order from keyed access results", func(t *testing.T) {
		rs, mockRA, mockPS, _, mockUMS, _, _ := newTestService(t)
		ids := []string{"r1", "r2"}
		mockRA.EXPECT().GetByIDs(gomock.Any(), ids).
			Return(map[string]*interfaces.Resource{"r2": {ID: "r2"}, "r1": {ID: "r1"}}, nil)
		mockPS.EXPECT().FilterResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_RESOURCE,
			ids, gomock.Any(), true, gomock.Any()).
			Return(map[string]interfaces.PermissionResourceOps{
				"r1": {ResourceID: "r1"}, "r2": {ResourceID: "r2"},
			}, nil)
		mockUMS.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil)

		resources, err := rs.GetByIDs(context.Background(), ids, false)
		require.NoError(t, err)
		require.Len(t, resources, 2)
		assert.Equal(t, ids, []string{resources[0].ID, resources[1].ID})
	})
	t.Run("trusted proxy read still requires resource authorization", func(t *testing.T) {
		rs, mockRA, mockPS, _, mockUMS, _, _ := newTestService(t)
		want := []*interfaces.Resource{{
			ID:               "r1",
			CatalogID:        "cat-user",
			SchemaDefinition: []*interfaces.Property{{Name: "id"}},
		}}
		mockRA.EXPECT().GetByIDs(gomock.Any(), []string{"r1"}).Return(map[string]*interfaces.Resource{"r1": want[0]}, nil)
		mockPS.EXPECT().FilterResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_RESOURCE, []string{"r1"}, gomock.Any(), true, gomock.Any()).Return(map[string]interfaces.PermissionResourceOps{"r1": {ResourceID: "r1"}}, nil)
		mockUMS.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil)

		got, err := rs.GetByIDs(interfaces.WithTrustedProxyRead(context.Background()), []string{"r1"}, false)
		require.NoError(t, err)
		assert.Equal(t, want, got)
		require.NotNil(t, got[0].ColumnCount)
		assert.Equal(t, 1, *got[0].ColumnCount)
	})

	t.Run("trusted proxy read preserves requested order after authorization", func(t *testing.T) {
		rs, mockRA, mockPS, _, mockUMS, _, _ := newTestService(t)
		ids := []string{"r2", "missing", "r1", "r2"}
		mockRA.EXPECT().GetByIDs(gomock.Any(), ids).Return(map[string]*interfaces.Resource{
			"r1": {ID: "r1"}, "r2": {ID: "r2"},
		}, nil)
		mockPS.EXPECT().FilterResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_RESOURCE, ids, gomock.Any(), true, gomock.Any()).Return(map[string]interfaces.PermissionResourceOps{"r1": {ResourceID: "r1"}, "r2": {ResourceID: "r2"}}, nil)
		mockUMS.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil)

		got, err := rs.GetByIDs(interfaces.WithTrustedProxyRead(context.Background()), ids, false)
		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, []string{"r2", "r1"}, []string{got[0].ID, got[1].ID})
	})

	t.Run("includes metadata and dataset row counts when requested", func(t *testing.T) {
		rs, mockRA, mockPS, mockDS, mockUMS, _, _ := newTestService(t)
		table := &interfaces.Resource{
			ID:             "table-1",
			Category:       interfaces.ResourceCategoryTable,
			SourceMetadata: map[string]any{"properties": map[string]any{"row_count": float64(42)}},
		}
		fileset := &interfaces.Resource{
			ID:             "fileset-1",
			Category:       interfaces.ResourceCategoryFileset,
			SourceMetadata: map[string]any{"properties": map[string]any{"row_count": json.Number("9007199254740993")}},
		}
		withoutMetadata := &interfaces.Resource{ID: "api-1", Category: interfaces.ResourceCategoryAPI}
		dataset := &interfaces.Resource{ID: "dataset-1", Category: interfaces.ResourceCategoryDataset}
		mockRA.EXPECT().GetByIDs(gomock.Any(), []string{"table-1", "fileset-1", "api-1", "dataset-1"}).
			Return(map[string]*interfaces.Resource{"table-1": table, "fileset-1": fileset, "api-1": withoutMetadata, "dataset-1": dataset}, nil)
		mockPS.EXPECT().FilterResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_RESOURCE, gomock.Any(), gomock.Any(), true, gomock.Any()).Return(map[string]interfaces.PermissionResourceOps{"table-1": {ResourceID: "table-1"}, "fileset-1": {ResourceID: "fileset-1"}, "api-1": {ResourceID: "api-1"}, "dataset-1": {ResourceID: "dataset-1"}}, nil)
		mockUMS.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil)
		mockDS.EXPECT().CountDocuments(gomock.Any(), dataset).Return(int64(7), nil)

		resources, err := rs.GetByIDs(
			interfaces.WithTrustedProxyRead(context.Background()),
			[]string{"table-1", "fileset-1", "api-1", "dataset-1"},
			true,
		)

		require.NoError(t, err)
		require.NotNil(t, resources[0].RowCount)
		assert.Equal(t, int64(42), *resources[0].RowCount)
		require.NotNil(t, resources[1].RowCount)
		assert.Equal(t, int64(9007199254740993), *resources[1].RowCount)
		assert.Nil(t, resources[2].RowCount)
		require.NotNil(t, resources[3].RowCount)
		assert.Equal(t, int64(7), *resources[3].RowCount)
	})

	t.Run("omits table and dataset row counts when not requested", func(t *testing.T) {
		rs, mockRA, mockPS, _, mockUMS, _, _ := newTestService(t)
		tableRows := int64(42)
		datasetRows := int64(7)
		mockRA.EXPECT().GetByIDs(gomock.Any(), []string{"table-1", "dataset-1"}).
			Return(map[string]*interfaces.Resource{
				"table-1":   {ID: "table-1", Category: interfaces.ResourceCategoryTable, RowCount: &tableRows},
				"dataset-1": {ID: "dataset-1", Category: interfaces.ResourceCategoryDataset, RowCount: &datasetRows},
			}, nil)
		mockPS.EXPECT().FilterResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_RESOURCE, gomock.Any(), gomock.Any(), true, gomock.Any()).Return(map[string]interfaces.PermissionResourceOps{"table-1": {ResourceID: "table-1"}, "dataset-1": {ResourceID: "dataset-1"}}, nil)
		mockUMS.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil)

		resources, err := rs.GetByIDs(
			interfaces.WithTrustedProxyRead(context.Background()),
			[]string{"table-1", "dataset-1"},
			false,
		)

		require.NoError(t, err)
		assert.Nil(t, resources[0].RowCount)
		assert.Nil(t, resources[1].RowCount)
	})

	t.Run("keeps dataset details available when counting rows fails", func(t *testing.T) {
		rs, mockRA, mockPS, mockDS, mockUMS, _, _ := newTestService(t)
		dataset := &interfaces.Resource{ID: "dataset-1", Category: interfaces.ResourceCategoryDataset}
		mockRA.EXPECT().GetByIDs(gomock.Any(), []string{"dataset-1"}).
			Return(map[string]*interfaces.Resource{"dataset-1": dataset}, nil)
		mockPS.EXPECT().FilterResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_RESOURCE, gomock.Any(), gomock.Any(), true, gomock.Any()).Return(map[string]interfaces.PermissionResourceOps{"dataset-1": {ResourceID: "dataset-1"}}, nil)
		mockUMS.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil)
		mockDS.EXPECT().CountDocuments(gomock.Any(), dataset).Return(int64(0), errors.New("count failed"))

		resources, err := rs.GetByIDs(
			interfaces.WithTrustedProxyRead(context.Background()),
			[]string{"dataset-1"},
			true,
		)

		require.NoError(t, err)
		assert.Nil(t, resources[0].RowCount)
	})

	t.Run("get by ids success", func(t *testing.T) {
		rs, mockRA, mockPS, _, mockUMS, _, _ := newTestService(t)
		mockRA.EXPECT().GetByIDs(gomock.Any(), []string{"r1", "r2"}).
			Return(map[string]*interfaces.Resource{"r1": {ID: "r1"}, "r2": {ID: "r2"}}, nil)
		mockPS.EXPECT().FilterResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_RESOURCE,
			[]string{"r1", "r2"}, gomock.Any(), true, gomock.Any()).
			Return(map[string]interfaces.PermissionResourceOps{
				"r1": {ResourceID: "r1", Operations: []string{"view_detail"}},
				"r2": {ResourceID: "r2", Operations: []string{"view_detail"}},
			}, nil)
		mockUMS.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil)

		resources, err := rs.GetByIDs(context.Background(), []string{"r1", "r2"}, false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(resources) != 2 {
			t.Errorf("expected 2 resources, got %d", len(resources))
		}
	})
}

func TestResourceUpdateUsesFeatureSemanticsForEverySupportedCategory(t *testing.T) {
	current := []interfaces.PropertyFeature{
		{FeatureName: "fulltext", FeatureType: interfaces.PropertyFeatureType_Fulltext},
		{
			FeatureName: "keyword",
			FeatureType: interfaces.PropertyFeatureType_Keyword,
			Config:      map[string]any{"ignore_above": 256},
		},
	}
	requested := []interfaces.PropertyFeature{
		{
			FeatureName: "keyword",
			FeatureType: interfaces.PropertyFeatureType_Keyword,
			Description: "Exact match",
			IsDefault:   true,
			Config:      map[string]any{"ignore_above": 256},
		},
		{
			FeatureName: "fulltext",
			FeatureType: interfaces.PropertyFeatureType_Fulltext,
			Description: "Search",
			IsDefault:   true,
			IsNative:    true,
		},
	}

	for _, category := range []string{
		interfaces.ResourceCategoryTable,
		interfaces.ResourceCategoryDataset,
	} {
		t.Run(category, func(t *testing.T) {
			changed, err := (&resourceService{}).validateResourceUpdateScope(
				context.Background(),
				&interfaces.Resource{
					CatalogID: "catalog-1",
					Category:  category,
					SchemaDefinition: []*interfaces.Property{{
						Name: "content", Type: interfaces.DataType_Text, Features: current,
					}},
				},
				&interfaces.ResourceRequest{
					CatalogID: "catalog-1",
					Category:  category,
					SchemaDefinition: []*interfaces.Property{{
						Name: "content", Type: interfaces.DataType_Text, Features: requested,
					}},
				},
			)

			require.NoError(t, err)
			assert.False(t, changed)
		})
	}
}

func TestSourceMetadataRowCountHandlesMissingMetadata(t *testing.T) {
	for _, metadata := range []map[string]any{
		nil,
		{},
		{"properties": map[string]any{}},
	} {
		count, ok := sourceMetadataRowCount(metadata)
		assert.False(t, ok)
		assert.Zero(t, count)
	}
}

func TestResourceServiceInternalGetByCatalogID(t *testing.T) {
	rs, mockRA, _, _, _, _, _ := newTestService(t)
	mockRA.EXPECT().GetByCatalogID(gomock.Any(), "cat1").
		Return([]*interfaces.Resource{{ID: "r1", CatalogID: "cat1"}}, nil)

	resources, err := rs.InternalGetByCatalogID(context.Background(), "cat1")

	require.NoError(t, err)
	require.Len(t, resources, 1)
	assert.Equal(t, "r1", resources[0].ID)
}

func TestResourceServiceList(t *testing.T) {
	t.Run("includes internal candidates for built-in admin", func(t *testing.T) {
		rs, mockRA, _, _, _, _, _ := newTestService(t)
		mockRA.EXPECT().ListPermissionRefs(gomock.Any(), interfaces.ResourcesQueryParams{IncludeInternal: true}).
			Return(nil, nil)
		ctx := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY,
			interfaces.AccountInfo{ID: interfaces.BuiltinAdminID})

		resources, total, err := rs.List(ctx, interfaces.ResourcesQueryParams{})

		require.NoError(t, err)
		assert.Empty(t, resources)
		assert.Zero(t, total)
	})
	t.Run("restores descending order from keyed summary lookup", func(t *testing.T) {
		rs, mockRA, mockPS, _, mockUMS, _, _ := newTestService(t)
		params := interfaces.ResourcesQueryParams{
			PaginationQueryParams: interfaces.PaginationQueryParams{Sort: interfaces.ResourceSortName, Direction: interfaces.DESC_DIRECTION, Limit: 2},
		}
		refs := []interfaces.ResourcePermissionRef{{ResourceID: "r3"}, {ResourceID: "r2"}, {ResourceID: "r1"}}
		mockRA.EXPECT().ListPermissionRefs(gomock.Any(), params).Return(refs, nil)
		mockPS.EXPECT().FilterResources(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), true, gomock.Any()).
			Return(map[string]interfaces.PermissionResourceOps{
				"r3": {ResourceID: "r3"}, "r2": {ResourceID: "r2"}, "r1": {ResourceID: "r1"},
			}, nil)
		mockRA.EXPECT().GetSummariesByIDs(gomock.Any(), []string{"r3", "r2"}).
			Return(map[string]*interfaces.ResourceSummary{"r2": {ID: "r2"}, "r3": {ID: "r3"}}, nil)
		mockUMS.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil)

		result, total, err := rs.List(context.Background(), params)
		require.NoError(t, err)
		assert.Equal(t, int64(3), total)
		require.Len(t, result, 2)
		assert.Equal(t, []string{"r3", "r2"}, []string{result[0].ID, result[1].ID})
	})
	t.Run("restores name order from keyed summary lookup", func(t *testing.T) {
		rs, mockRA, mockPS, _, mockUMS, _, _ := newTestService(t)
		params := interfaces.ResourcesQueryParams{
			PaginationQueryParams: interfaces.PaginationQueryParams{Sort: interfaces.ResourceSortName, Direction: interfaces.ASC_DIRECTION, Offset: 1, Limit: 2},
			CatalogID:             "catalog-1",
		}
		refs := []interfaces.ResourcePermissionRef{
			{ResourceID: "r1", CatalogID: "catalog-1"},
			{ResourceID: "r2", CatalogID: "catalog-1"},
			{ResourceID: "r3", CatalogID: "catalog-1"},
			{ResourceID: "r4", CatalogID: "catalog-1"},
		}
		mockRA.EXPECT().ListPermissionRefs(gomock.Any(), params).Return(refs, nil)
		mockPS.EXPECT().FilterResources(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), true, gomock.Any()).
			Return(map[string]interfaces.PermissionResourceOps{
				"r1": {ResourceID: "r1"}, "r2": {ResourceID: "r2"}, "r3": {ResourceID: "r3"}, "r4": {ResourceID: "r4"},
			}, nil)
		mockRA.EXPECT().GetSummariesByIDs(gomock.Any(), []string{"r2", "r3"}).
			Return(map[string]*interfaces.ResourceSummary{"r3": {ID: "r3"}, "r2": {ID: "r2"}}, nil)
		mockUMS.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil)

		result, total, err := rs.List(context.Background(), params)
		require.NoError(t, err)
		assert.Equal(t, int64(4), total)
		require.Len(t, result, 2)
		assert.Equal(t, []string{"r2", "r3"}, []string{result[0].ID, result[1].ID})
	})
	t.Run("list pagination", func(t *testing.T) {
		rs, mockRA, mockPS, _, mockUMS, _, _ := newTestService(t)
		refs := []interfaces.ResourcePermissionRef{{ResourceID: "r1"}, {ResourceID: "r2"}, {ResourceID: "r3"}, {ResourceID: "r4"}}
		mockRA.EXPECT().ListPermissionRefs(gomock.Any(), gomock.Any()).Return(refs, nil)
		mockPS.EXPECT().FilterResources(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), true, gomock.Any()).
			Return(map[string]interfaces.PermissionResourceOps{
				"r1": {ResourceID: "r1"}, "r2": {ResourceID: "r2"}, "r3": {ResourceID: "r3"}, "r4": {ResourceID: "r4"},
			}, nil)
		summaries := []*interfaces.ResourceSummary{{ID: "r2"}, {ID: "r3"}}
		mockRA.EXPECT().GetSummariesByIDs(gomock.Any(), gomock.Any()).Return(map[string]*interfaces.ResourceSummary{"r2": summaries[0], "r3": summaries[1]}, nil)
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
		refs := []interfaces.ResourcePermissionRef{{ResourceID: "r1"}, {ResourceID: "r2"}}
		summaries := []*interfaces.ResourceSummary{
			{ID: "r1", Category: interfaces.ResourceCategoryDataset, LocalIndexName: "index-1"},
			{ID: "r2"},
		}
		mockRA.EXPECT().ListPermissionRefs(gomock.Any(), gomock.Any()).Return(refs, nil)
		mockPS.EXPECT().FilterResources(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), true, gomock.Any()).
			Return(map[string]interfaces.PermissionResourceOps{
				"r1": {ResourceID: "r1"}, "r2": {ResourceID: "r2"},
			}, nil)
		mockRA.EXPECT().GetSummariesByIDs(gomock.Any(), gomock.Any()).Return(map[string]*interfaces.ResourceSummary{"r1": summaries[0], "r2": summaries[1]}, nil)
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
	t.Run("list excludes internal resources for non-admin", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockRA := vmock.NewMockResourceAccess(ctrl)
		mockPS := vmock.NewMockPermissionService(ctrl)
		mockCS := vmock.NewMockCatalogService(ctrl)
		mockUMS := vmock.NewMockUserMgmtService(ctrl)
		rs := &resourceService{ra: mockRA, ps: mockPS, cs: mockCS, ums: mockUMS}

		mockRA.EXPECT().ListPermissionRefs(gomock.Any(), interfaces.ResourcesQueryParams{
			PaginationQueryParams: interfaces.PaginationQueryParams{Limit: -1},
		}).Return([]interfaces.ResourcePermissionRef{{ResourceID: "r1"}}, nil)
		// Access 层已排除 internal Resource，仅普通资源送入 bkn-safe。
		mockPS.EXPECT().FilterResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_RESOURCE,
			[]string{"r1"}, gomock.Any(), true, gomock.Any()).
			Return(map[string]interfaces.PermissionResourceOps{"r1": {ResourceID: "r1"}}, nil)
		mockRA.EXPECT().GetSummariesByIDs(gomock.Any(), []string{"r1"}).
			Return(map[string]*interfaces.ResourceSummary{"r1": {ID: "r1"}}, nil)
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
	var httpErr *rest.HTTPError
	ok := errors.As(err, &httpErr)
	require.Truef(t, ok, "expected HTTPError, got %T", err)
	assert.Equal(t, wantCode, httpErr.BaseError.ErrorCode)
	return httpErr
}

func TestResourceServiceValidateIndexConfigModelsRejectsReferencedVectorConfig(t *testing.T) {
	rs := &resourceService{}
	schema := []*interfaces.Property{
		{
			Name: "content",
			Type: interfaces.DataType_Text,
			Features: []interfaces.PropertyFeature{{
				FeatureType: interfaces.PropertyFeatureType_Vector,
				RefProperty: "embedding",
				Config:      map[string]any{"embedding_model": "content-model"},
			}},
		},
		{
			Name: "embedding",
			Type: interfaces.DataType_Vector,
			Features: []interfaces.PropertyFeature{{
				FeatureType: interfaces.PropertyFeatureType_Vector,
				Config:      map[string]any{"embedding_model": "embedding-model"},
			}},
		},
	}

	err := rs.validateIndexConfigModels(context.Background(), schema, nil)

	httpErr := requireResourceHTTPError(t, err, verrors.VegaBackend_InvalidParameter_RequestBody)
	assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
	assert.Contains(t, httpErr.BaseError.ErrorDetails, `vector feature on field "content" that references "embedding" must not define config`)
}

func TestValidateKeywordConfig(t *testing.T) {
	t.Run("accepts boundary values", func(t *testing.T) {
		minimum, maximum := 1, interfaces.MaxKeywordIgnoreAbove
		for _, value := range []*int{&minimum, &maximum} {
			err := validateKeywordConfig(context.Background(), []*interfaces.Property{{
				Name: "title", Type: interfaces.DataType_Text,
				Features: []interfaces.PropertyFeature{{
					FeatureType: interfaces.PropertyFeatureType_Keyword,
					Config:      map[string]any{"ignore_above": *value},
				}},
			}}, &interfaces.ResourceIndexConfig{DefaultKeywordIgnoreAbove: value})
			require.NoError(t, err)
		}
	})

	t.Run("rejects invalid resource and field values", func(t *testing.T) {
		zero := 0
		err := validateKeywordConfig(context.Background(), nil,
			&interfaces.ResourceIndexConfig{DefaultKeywordIgnoreAbove: &zero})
		assert.Error(t, err)

		for _, value := range []any{0, 1.5, 8192} {
			err = validateKeywordConfig(context.Background(), []*interfaces.Property{{
				Name: "title", Features: []interfaces.PropertyFeature{{
					FeatureType: interfaces.PropertyFeatureType_Keyword,
					Config:      map[string]any{"ignore_above": value},
				}},
			}}, nil)
			assert.Error(t, err)
		}
	})
}

func TestValidateSchemaDefinitionRejectsNullField(t *testing.T) {
	err := validateSchemaDefinition(context.Background(), []*interfaces.Property{{Name: "id"}, nil})

	httpErr := requireResourceHTTPError(t, err, verrors.VegaBackend_InvalidParameter_RequestBody)
	assert.Contains(t, httpErr.BaseError.ErrorDetails, "cannot contain null fields")
}

func TestResourceServiceCreate(t *testing.T) {
	t.Run("create dataset category", func(t *testing.T) {
		rs, mockRA, _, mockDS, _, _, _ := newTestService(t)
		expectResourceServiceTransaction(t, rs, true)
		mockRA.EXPECT().Create(gomock.Any(), gomock.Not(nil), gomock.Any()).Return(nil)
		mockDS.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, got *interfaces.Resource) error {
				require.Len(t, got.SchemaDefinition, 2)
				require.Len(t, got.SchemaDefinition[0].Features, 1)
				assert.Equal(t, interfaces.PropertyFeatureType_Keyword, got.SchemaDefinition[0].Features[0].FeatureType)
				require.Len(t, got.SchemaDefinition[1].Features, 2)
				return nil
			})

		resource, err := rs.Create(context.Background(), &interfaces.ResourceRequest{
			Name:     "test-dataset",
			Category: interfaces.ResourceCategoryDataset,
			SchemaDefinition: []*interfaces.Property{
				{Name: "code", Type: interfaces.DataType_String},
				{Name: "body", Type: interfaces.DataType_Text},
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		require.NotNil(t, resource)
	})
	t.Run("removes parent when the resource transaction cannot commit", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockRA := vmock.NewMockResourceAccess(ctrl)
		mockPS := vmock.NewMockPermissionService(ctrl)
		mockCS := vmock.NewMockCatalogService(ctrl)
		db, sqlMock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() {
			require.NoError(t, db.Close())
			require.NoError(t, sqlMock.ExpectationsWereMet())
		})
		rs := &resourceService{db: db, ra: mockRA, ps: mockPS, cs: mockCS}

		sqlMock.ExpectBegin()
		sqlMock.ExpectCommit().WillReturnError(errors.New("commit failed"))
		sqlMock.ExpectClose()
		mockCS.EXPECT().CheckCatalogPermission(gomock.Any(), "cat1",
			[]string{interfaces.OPERATION_TYPE_RESOURCE_MANAGE}, true).
			Return(true, &interfaces.Catalog{ID: "cat1"}, nil)
		mockRA.EXPECT().Create(gomock.Any(), gomock.Not(nil), gomock.Any()).Return(nil)
		mockPS.EXPECT().UpsertResourceParents(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_RESOURCE,
			interfaces.AUTH_RESOURCE_TYPE_CATALOG, gomock.Any()).Return(nil)
		mockPS.EXPECT().DeleteResourceParents(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_RESOURCE,
			gomock.Any()).DoAndReturn(func(_ context.Context, _ string, ids []string) error {
			require.Len(t, ids, 1)
			require.NotEmpty(t, ids[0])
			return nil
		})

		_, err = rs.Create(context.Background(), &interfaces.ResourceRequest{
			CatalogID: "cat1",
			Name:      "resource",
			Category:  interfaces.ResourceCategoryTable,
		})

		require.Error(t, err)
	})
	t.Run("create success", func(t *testing.T) {
		rs, mockRA, _, _, _, _, _ := newTestService(t)
		expectResourceServiceTransaction(t, rs, true)
		mockRA.EXPECT().Create(gomock.Any(), gomock.Not(nil), gomock.Any()).
			DoAndReturn(func(_ context.Context, _ *sql.Tx, resource *interfaces.Resource) error {
				assert.False(t, resource.Internal)
				return nil
			})

		resource, err := rs.Create(context.Background(), &interfaces.ResourceRequest{
			Name:     "test-resource",
			Category: "table",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		require.NotNil(t, resource)
		assert.False(t, resource.Internal)
	})
	t.Run("rejects internal resource in a normal catalog", func(t *testing.T) {
		rs, _, _, _, _, _, _ := newTestService(t)
		internal := true

		_, err := rs.Create(context.Background(), &interfaces.ResourceRequest{
			CatalogID: "cat-normal",
			Name:      "invalid-internal-resource",
			Category:  interfaces.ResourceCategoryTable,
			Internal:  &internal,
		})

		httpErr := requireResourceHTTPError(t, err, verrors.VegaBackend_InvalidParameter_RequestBody)
		assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
		assert.Contains(t, httpErr.BaseError.ErrorDetails, "must match its catalog")
	})
	t.Run("rejects normal resource in an internal catalog", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := vmock.NewMockCatalogService(ctrl)
		rs := &resourceService{cs: mockCS}
		mockCS.EXPECT().CheckCatalogPermission(gomock.Any(), "cat-internal",
			[]string{interfaces.OPERATION_TYPE_RESOURCE_MANAGE}, true).
			Return(true, &interfaces.Catalog{ID: "cat-internal", Internal: true}, nil)
		ctx := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY,
			interfaces.AccountInfo{ID: interfaces.BuiltinAdminID})

		_, err := rs.Create(ctx, &interfaces.ResourceRequest{
			CatalogID: "cat-internal",
			Name:      "invalid-normal-resource",
			Category:  interfaces.ResourceCategoryTable,
		})

		httpErr := requireResourceHTTPError(t, err, verrors.VegaBackend_InvalidParameter_RequestBody)
		assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
		assert.Contains(t, httpErr.BaseError.ErrorDetails, "must match its catalog")
	})
	t.Run("returns internal catalog visibility error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := vmock.NewMockCatalogService(ctrl)
		rs := &resourceService{cs: mockCS}
		visibilityErr := rest.NewHTTPError(context.Background(), http.StatusForbidden,
			rest.PublicError_Forbidden).WithErrorDetails("internal catalogs are restricted to the built-in administrator")
		mockCS.EXPECT().CheckCatalogPermission(gomock.Any(), "cat-internal",
			[]string{interfaces.OPERATION_TYPE_RESOURCE_MANAGE}, true).
			Return(false, nil, visibilityErr)
		internal := true

		_, err := rs.Create(context.Background(), &interfaces.ResourceRequest{
			CatalogID: "cat-internal",
			Name:      "internal-resource",
			Category:  interfaces.ResourceCategoryTable,
			Internal:  &internal,
		})

		httpErr := requireResourceHTTPError(t, err, rest.PublicError_Forbidden)
		assert.Equal(t, http.StatusForbidden, httpErr.HTTPCode)
	})
	t.Run("rejects missing catalog resource manage permission", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := vmock.NewMockCatalogService(ctrl)
		rs := &resourceService{cs: mockCS}
		mockCS.EXPECT().CheckCatalogPermission(gomock.Any(), "cat1",
			[]string{interfaces.OPERATION_TYPE_RESOURCE_MANAGE}, true).
			Return(false, nil, nil)

		_, err := rs.Create(context.Background(), &interfaces.ResourceRequest{
			CatalogID: "cat1",
			Name:      "resource",
			Category:  interfaces.ResourceCategoryTable,
		})

		httpErr := requireResourceHTTPError(t, err, rest.PublicError_Forbidden)
		assert.Equal(t, http.StatusForbidden, httpErr.HTTPCode)
	})
	t.Run("registers parent as part of normal resource creation", func(t *testing.T) {
		rs, mockRA, _, _, _, _, _ := newTestService(t)
		expectResourceServiceTransaction(t, rs, true)
		mockRA.EXPECT().Create(gomock.Any(), gomock.Not(nil), gomock.Any()).
			DoAndReturn(func(_ context.Context, _ *sql.Tx, resource *interfaces.Resource) error {
				assert.False(t, resource.Internal)
				return nil
			})

		resource, err := rs.Create(context.Background(), &interfaces.ResourceRequest{
			CatalogID: "cat-1", Name: "test-resource", Category: interfaces.ResourceCategoryTable,
		})
		require.NoError(t, err)
		assert.NotNil(t, resource)
	})
	t.Run("create table adds required default features to text fields", func(t *testing.T) {
		rs, mockRA, _, _, _, _, _ := newTestService(t)
		expectResourceServiceTransaction(t, rs, true)
		mockRA.EXPECT().Create(gomock.Any(), gomock.Not(nil), gomock.Any()).Return(nil)

		resource, err := rs.Create(context.Background(), &interfaces.ResourceRequest{
			Name:     "test-resource",
			Category: interfaces.ResourceCategoryTable,
			SchemaDefinition: []*interfaces.Property{{
				Name: "body",
				Type: interfaces.DataType_Text,
			}},
		})

		require.NoError(t, err)
		require.Len(t, resource.SchemaDefinition[0].Features, 2)
		keyword := resource.SchemaDefinition[0].Features[0]
		assert.Equal(t, interfaces.PropertyFeatureType_Keyword, keyword.FeatureType)
		assert.Equal(t, interfaces.LocalIndexKeywordSubfieldName, keyword.FeatureName)
		assert.Equal(t, interfaces.DefaultTextKeywordIgnoreAbove, keyword.Config["ignore_above"])
		assert.Equal(t, interfaces.PropertyFeatureType_Fulltext, resource.SchemaDefinition[0].Features[1].FeatureType)
	})
	t.Run("create with explicit id", func(t *testing.T) {
		rs, mockRA, _, _, _, _, _ := newTestService(t)
		expectResourceServiceTransaction(t, rs, true)
		mockRA.EXPECT().Create(gomock.Any(), gomock.Not(nil), gomock.Any()).Return(nil)

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
		rs, mockRA, _, _, _, _, _ := newTestService(t)
		expectResourceServiceTransaction(t, rs, false)
		mockRA.EXPECT().Create(gomock.Any(), gomock.Not(nil), gomock.Any()).Return(fmt.Errorf("db error"))

		_, err := rs.Create(context.Background(), &interfaces.ResourceRequest{
			Name: "test-resource",
		})
		if err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("deletes dataset index when resource persistence fails", func(t *testing.T) {
		rs, mockRA, _, mockDS, _, _, _ := newTestService(t)
		expectResourceServiceTransaction(t, rs, false)
		mockDS.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
		mockRA.EXPECT().Create(gomock.Any(), gomock.Not(nil), gomock.Any()).Return(errors.New("insert failed"))
		mockDS.EXPECT().Delete(gomock.Any(), gomock.Any()).Return(nil)

		_, err := rs.Create(context.Background(), &interfaces.ResourceRequest{
			Name:     "test-dataset",
			Category: interfaces.ResourceCategoryDataset,
		})

		require.Error(t, err)
	})
	t.Run("create rejects missing feature embedding model ID", func(t *testing.T) {
		rs, _, _, _, _, _, _ := newTestService(t)
		ctrl := gomock.NewController(t)
		mockMFS := vmock.NewMockModelFactoryService(ctrl)
		rs.mfs = mockMFS
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

		var httpErr *rest.HTTPError
		ok := errors.As(err, &httpErr)
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
		rs, _, _, _, _, _, _ := newTestService(t)

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

		var httpErr *rest.HTTPError
		ok := errors.As(err, &httpErr)
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
	t.Run("create internal catalog resource requires admin and records parent", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockRA := vmock.NewMockResourceAccess(ctrl)
		mockPS := vmock.NewMockPermissionService(ctrl)
		mockCS := vmock.NewMockCatalogService(ctrl)
		rs := &resourceService{ra: mockRA, ps: mockPS, cs: mockCS}
		expectResourceServiceTransaction(t, rs, true)

		mockCS.EXPECT().CheckCatalogPermission(gomock.Any(), "cat-internal",
			[]string{interfaces.OPERATION_TYPE_RESOURCE_MANAGE}, true).
			Return(true, &interfaces.Catalog{ID: "cat-internal", Internal: true}, nil)
		mockRA.EXPECT().Create(gomock.Any(), gomock.Not(nil), gomock.Any()).
			DoAndReturn(func(_ context.Context, _ *sql.Tx, resource *interfaces.Resource) error {
				assert.True(t, resource.Internal)
				return nil
			})
		mockPS.EXPECT().UpsertResourceParents(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_RESOURCE,
			interfaces.AUTH_RESOURCE_TYPE_CATALOG, gomock.Any()).Return(nil)

		ctx := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY,
			interfaces.AccountInfo{ID: interfaces.BuiltinAdminID})
		internal := true
		_, err := rs.Create(ctx, &interfaces.ResourceRequest{
			CatalogID: "cat-internal",
			Name:      "internal-res",
			Category:  "table",
			Internal:  &internal,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestResourceServiceInternalCreateTracksParentForTransactionCompensation(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockRA := vmock.NewMockResourceAccess(ctrl)
	mockPS := vmock.NewMockPermissionService(ctrl)
	rs := &resourceService{ra: mockRA, ps: mockPS}
	tx := &sql.Tx{}
	ctx, tracker, owner := WithResourceParentTracker(context.Background())
	require.True(t, owner)
	internal := true

	mockRA.EXPECT().Create(gomock.Any(), tx, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *sql.Tx, resource *interfaces.Resource) error {
			assert.True(t, resource.Internal)
			return nil
		})
	mockPS.EXPECT().UpsertResourceParents(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_RESOURCE,
		interfaces.AUTH_RESOURCE_TYPE_CATALOG, []interfaces.PermissionResourceParent{{
			ResourceID: "resource-1", ParentID: "cat-internal",
		}}).Return(nil)
	mockPS.EXPECT().DeleteResourceParents(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_RESOURCE,
		[]string{"resource-1"}).Return(nil)

	resource, err := rs.InternalCreate(ctx, tx, &interfaces.ResourceRequest{
		ID:        "resource-1",
		CatalogID: "cat-internal",
		Name:      "internal-resource",
		Category:  interfaces.ResourceCategoryTable,
		Internal:  &internal,
	})

	require.NoError(t, err)
	require.NotNil(t, resource)
	require.NoError(t, tracker.Cleanup(ctx))
}

func TestResourceServiceInternalCreateRequiresParentTracker(t *testing.T) {
	rs := &resourceService{}

	_, err := rs.InternalCreate(context.Background(), &sql.Tx{}, &interfaces.ResourceRequest{})

	require.EqualError(t, err, "resource parent tracker is required")
}

// expectDeleteGrantedByCatalog 通过 bkn-safe 的 resource 判定装配删除授权。
func expectDeleteGrantedByCatalog(mockRA *vmock.MockResourceAccess,
	mockPS *vmock.MockPermissionService, ids []string, catalogID string) {

	granted := make(map[string]interfaces.PermissionResourceOps, len(ids))
	for _, id := range ids {
		granted[id] = interfaces.PermissionResourceOps{
			ResourceID: id,
			Operations: []string{interfaces.OPERATION_TYPE_DELETE},
		}
	}
	mockPS.EXPECT().FilterResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_RESOURCE,
		ids, []string{interfaces.OPERATION_TYPE_DELETE}, true, gomock.Any()).
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
			Return(map[string]*interfaces.Resource{"r1": {ID: "r1", Category: "table", LocalIndexName: "vega-build-r1-t1"}}, nil)
		expectResourceBuildTasksForDelete(t, mockBTA, "r1", nil)
		mockRA.EXPECT().DeleteByIDs(gomock.Any(), []string{"r1"}).Return(nil)
		mockPS.EXPECT().DeleteResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_RESOURCE, []string{"r1"}).Return(nil)
		err := rs.DeleteByIDs(context.Background(), []string{"r1"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("does not touch parent edge when local deletion fails", func(t *testing.T) {
		rs, mockRA, mockPS, _, _, _, mockBTA := newTestService(t)
		parentPS := &parentAwarePermissionService{
			MockPermissionService: mockPS,
		}
		rs.ps = parentPS

		expectDeleteGrantedByCatalog(mockRA, mockPS, []string{"r1"}, "cat1")
		mockRA.EXPECT().GetByIDs(gomock.Any(), []string{"r1"}).Return(map[string]*interfaces.Resource{
			"r1": {ID: "r1", CatalogID: "cat1"},
		}, nil)
		expectResourceBuildTasksForDelete(t, mockBTA, "r1", nil)
		mockRA.EXPECT().DeleteByIDs(gomock.Any(), []string{"r1"}).Return(errors.New("delete resource failed"))

		err := rs.DeleteByIDs(context.Background(), []string{"r1"})
		require.Error(t, err)
		assert.Zero(t, parentPS.deleteParentCalls)
		assert.Zero(t, parentPS.upsertParentCalls)
	})
	t.Run("deletes parent edge after local resource deletion", func(t *testing.T) {
		rs, mockRA, mockPS, _, _, _, mockBTA := newTestService(t)
		localDeleted := false
		parentPS := &parentAwarePermissionService{
			MockPermissionService: mockPS,
			onDeleteParents: func(resourceType string, resourceIDs []string) error {
				assert.True(t, localDeleted)
				assert.Equal(t, interfaces.AUTH_RESOURCE_TYPE_RESOURCE, resourceType)
				assert.Equal(t, []string{"r1"}, resourceIDs)
				return nil
			},
		}
		rs.ps = parentPS

		expectDeleteGrantedByCatalog(mockRA, mockPS, []string{"r1"}, "cat1")
		mockRA.EXPECT().GetByIDs(gomock.Any(), []string{"r1"}).Return(map[string]*interfaces.Resource{
			"r1": {ID: "r1", CatalogID: "cat1"},
		}, nil)
		expectResourceBuildTasksForDelete(t, mockBTA, "r1", nil)
		mockRA.EXPECT().DeleteByIDs(gomock.Any(), []string{"r1"}).DoAndReturn(
			func(_ context.Context, _ []string) error {
				localDeleted = true
				return nil
			})
		mockPS.EXPECT().DeleteResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_RESOURCE,
			[]string{"r1"}).Return(nil)

		require.NoError(t, rs.DeleteByIDs(context.Background(), []string{"r1"}))
		assert.Equal(t, 1, parentPS.deleteParentCalls)
	})
	t.Run("rejects deletion while resource refresh is pending or running", func(t *testing.T) {
		rs, mockRA, mockPS, _, _, _, _ := newTestService(t)
		ctrl := gomock.NewController(t)
		mockDTA := vmock.NewMockDiscoverTaskAccess(ctrl)
		rs.dta = mockDTA
		expectDeleteGrantedByCatalog(mockRA, mockPS, []string{"r1"}, "cat1")
		mockRA.EXPECT().GetByIDs(gomock.Any(), []string{"r1"}).
			Return(map[string]*interfaces.Resource{"r1": {ID: "r1"}}, nil)
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
				Return(map[string]*interfaces.Resource{"r1": {ID: "r1", Category: interfaces.ResourceCategoryDataset, LocalIndexName: "vega-dataset-index-1"}}, nil),
			mockBTA.EXPECT().InternalList(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, params interfaces.BuildTasksQueryParams) ([]*interfaces.BuildTaskSummary, error) {
					assert.Equal(t, "r1", params.ResourceID)
					assert.Equal(t, []string{interfaces.BuildTaskStatusRunning, interfaces.BuildTaskStatusStopping}, params.Statuses)
					assert.Equal(t, 1, params.Limit)
					return nil, nil
				}),
			mockRA.EXPECT().DeleteByIDs(gomock.Any(), []string{"r1"}).Return(nil),
			mockDS.EXPECT().Delete(gomock.Any(), gomock.Any()).Return(nil),
			mockPS.EXPECT().DeleteResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_RESOURCE, []string{"r1"}).Return(nil),
		)

		require.NoError(t, rs.DeleteByIDs(context.Background(), []string{"r1"}))
	})
	t.Run("does not delete dataset when resource deletion fails", func(t *testing.T) {
		rs, mockRA, mockPS, _, _, _, mockBTA := newTestService(t)
		expectDeleteGrantedByCatalog(mockRA, mockPS, []string{"r1"}, "cat1")
		mockRA.EXPECT().GetByIDs(gomock.Any(), []string{"r1"}).
			Return(map[string]*interfaces.Resource{"r1": {ID: "r1", Category: interfaces.ResourceCategoryDataset}}, nil)
		expectResourceBuildTasksForDelete(t, mockBTA, "r1", nil)
		mockRA.EXPECT().DeleteByIDs(gomock.Any(), []string{"r1"}).Return(errors.New("delete resource failed"))

		err := rs.DeleteByIDs(context.Background(), []string{"r1"})
		require.Error(t, err)
	})
	t.Run("rejects deletion while build task is active", func(t *testing.T) {
		rs, mockRA, mockPS, _, _, _, mockBTA := newTestService(t)
		expectDeleteGrantedByCatalog(mockRA, mockPS, []string{"r1"}, "cat1")
		mockRA.EXPECT().GetByIDs(gomock.Any(), []string{"r1"}).
			Return(map[string]*interfaces.Resource{"r1": {ID: "r1"}}, nil)
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
			Return(map[string]*interfaces.Resource{"r1": {ID: "r1"}}, nil)
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
	t.Run("rejects changing internal after creation", func(t *testing.T) {
		rs, _, _, _, _, _, _ := newTestService(t)
		internal := true

		err := rs.Update(context.Background(), &interfaces.Resource{
			ID: "r1", CatalogID: "cat1", Internal: false,
		}, &interfaces.ResourceRequest{Internal: &internal})

		httpErr := requireResourceHTTPError(t, err, verrors.VegaBackend_InvalidParameter_RequestBody)
		assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
		assert.Contains(t, httpErr.BaseError.ErrorDetails, "immutable")
	})
	t.Run("allows dataset feature metadata and order changes without rebuilding the index", func(t *testing.T) {
		rs, mockRA, mockPS, _, _, mockCS, _ := newTestService(t)
		expectResourceServiceTransaction(t, rs, true)
		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockCS.EXPECT().CheckExistByID(gomock.Any(), "cat1").Return(true, nil)
		resource := &interfaces.Resource{
			ID:             "r1",
			CatalogID:      "cat1",
			Category:       interfaces.ResourceCategoryDataset,
			Name:           "dataset",
			LocalIndexName: "vega-dataset-index-1",
			SchemaDefinition: []*interfaces.Property{{
				Name: "content",
				Type: interfaces.DataType_Text,
				Features: []interfaces.PropertyFeature{
					{FeatureName: "fulltext", FeatureType: interfaces.PropertyFeatureType_Fulltext},
					{FeatureName: "keyword", FeatureType: interfaces.PropertyFeatureType_Keyword, Config: map[string]any{"ignore_above": 256}},
				},
			}},
		}
		mockRA.EXPECT().Update(gomock.Any(), gomock.Not(nil), resource, int64(1)).Return(int64(1), nil)

		err := rs.Update(context.Background(), resource, &interfaces.ResourceRequest{
			CatalogID:          "cat1",
			Category:           interfaces.ResourceCategoryDataset,
			Name:               "dataset",
			Enabled:            resource.Enabled,
			ExpectedUpdateTime: 1,
			SchemaDefinition: []*interfaces.Property{{
				Name: "content",
				Type: interfaces.DataType_Text,
				Features: []interfaces.PropertyFeature{
					{FeatureName: "keyword", FeatureType: interfaces.PropertyFeatureType_Keyword, Description: "Exact match", IsDefault: true, Config: map[string]any{"ignore_above": 256}},
					{FeatureName: "fulltext", FeatureType: interfaces.PropertyFeatureType_Fulltext, Description: "Search", IsDefault: true, IsNative: true},
				},
			}},
		})

		require.NoError(t, err)
		assert.Equal(t, "Exact match", resource.SchemaDefinition[0].Features[0].Description)
	})

	t.Run("update nil resource", func(t *testing.T) {
		rs, _, _, _, _, _, _ := newTestService(t)
		err := rs.Update(context.Background(), nil, &interfaces.ResourceRequest{})
		if err == nil {
			t.Fatal("expected error for nil resource")
		}
	})
	t.Run("update success", func(t *testing.T) {
		rs, mockRA, mockPS, _, _, mockCS, _ := newTestService(t)
		parentPS := &parentAwarePermissionService{MockPermissionService: mockPS}
		rs.ps = parentPS
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
		assert.Zero(t, parentPS.upsertParentCalls)
	})
	t.Run("re-saving a table persists the default keyword feature for text fields", func(t *testing.T) {
		rs, mockRA, mockPS, _, _, mockCS, mockBTA := newTestService(t)
		expectResourceServiceTransaction(t, rs, true)
		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockBTA.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return(nil, nil)
		mockCS.EXPECT().CheckExistByID(gomock.Any(), "cat1").Return(true, nil)
		mockRA.EXPECT().Update(gomock.Any(), gomock.Not(nil), gomock.Any(), int64(0)).
			DoAndReturn(func(_ context.Context, _ *sql.Tx, got *interfaces.Resource, _ int64) (int64, error) {
				require.Len(t, got.SchemaDefinition, 1)
				require.Len(t, got.SchemaDefinition[0].Features, 2)
				keyword := got.SchemaDefinition[0].Features[1]
				assert.Equal(t, interfaces.PropertyFeatureType_Keyword, keyword.FeatureType)
				assert.Equal(t, interfaces.LocalIndexKeywordSubfieldName, keyword.FeatureName)
				assert.Equal(t, interfaces.DefaultTextKeywordIgnoreAbove, keyword.Config["ignore_above"])
				return 1, nil
			})

		resource := &interfaces.Resource{
			ID:               "r1",
			CatalogID:        "cat1",
			Category:         interfaces.ResourceCategoryTable,
			Name:             "table",
			SourceIdentifier: "public.materials",
			SchemaDefinition: []*interfaces.Property{{
				Name: "material_number",
				Type: interfaces.DataType_Text,
				Features: []interfaces.PropertyFeature{{
					FeatureName: "fulltext",
					FeatureType: interfaces.PropertyFeatureType_Fulltext,
				}},
			}},
		}
		err := rs.Update(context.Background(), resource, &interfaces.ResourceRequest{
			CatalogID:        "cat1",
			Category:         interfaces.ResourceCategoryTable,
			Name:             "table",
			SourceIdentifier: "public.materials",
			SchemaDefinition: []*interfaces.Property{{
				Name: "material_number",
				Type: interfaces.DataType_Text,
				Features: []interfaces.PropertyFeature{{
					FeatureName: "fulltext",
					FeatureType: interfaces.PropertyFeatureType_Fulltext,
				}},
			}},
		})

		require.NoError(t, err)
	})
	t.Run("updates dataset index mapping before persisting a schema change", func(t *testing.T) {
		rs, mockRA, mockPS, mockDS, _, mockCS, mockBTA := newTestService(t)
		expectResourceServiceTransaction(t, rs, true)
		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockBTA.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return(nil, nil)
		mockCS.EXPECT().CheckExistByID(gomock.Any(), "cat1").Return(true, nil)
		resource := &interfaces.Resource{
			ID:             "r1",
			CatalogID:      "cat1",
			Category:       interfaces.ResourceCategoryDataset,
			Name:           "dataset",
			LocalIndexName: "vega-dataset-index-1",
			SchemaDefinition: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_String, Features: []interfaces.PropertyFeature{{
				FeatureName: "keyword", FeatureType: interfaces.PropertyFeatureType_Keyword,
				Config: map[string]any{"ignore_above": interfaces.DefaultTextKeywordIgnoreAbove},
			}}}},
		}
		mockDS.EXPECT().ListDocuments(gomock.Any(), resource, gomock.Any()).
			DoAndReturn(func(_ context.Context, _ *interfaces.Resource, params *interfaces.ResourceDataQueryParams) ([]map[string]any, int64, error) {
				assert.Equal(t, 1, params.Paging.Limit)
				return nil, 0, nil
			})
		mockDS.EXPECT().Update(gomock.Any(), resource).Return(nil)
		mockRA.EXPECT().Update(gomock.Any(), gomock.Not(nil), resource, int64(0)).Return(int64(1), nil)

		err := rs.Update(context.Background(), resource, &interfaces.ResourceRequest{
			CatalogID: "cat1",
			Category:  interfaces.ResourceCategoryDataset,
			Name:      "dataset",
			SchemaDefinition: []*interfaces.Property{
				{Name: "id", Type: interfaces.DataType_String},
				{Name: "content", Type: interfaces.DataType_Text},
			},
		})

		require.NoError(t, err)
	})
	t.Run("updates the dataset mapping when completing a legacy vector dimension", func(t *testing.T) {
		rs, mockRA, mockPS, mockDS, _, mockCS, _ := newTestService(t)
		expectResourceServiceTransaction(t, rs, true)
		ctrl := gomock.NewController(t)
		mockMFS := vmock.NewMockModelFactoryService(ctrl)
		rs.mfs = mockMFS
		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockCS.EXPECT().CheckExistByID(gomock.Any(), "cat1").Return(true, nil)
		mockMFS.EXPECT().GetModelByID(gomock.Any(), "embedding-1").
			Return(&interfaces.SmallModel{ModelID: "embedding-1", EmbeddingDim: 3}, nil)
		resource := &interfaces.Resource{
			ID:             "r1",
			CatalogID:      "cat1",
			Category:       interfaces.ResourceCategoryDataset,
			Name:           "dataset",
			LocalIndexName: "vega-dataset-index-1",
			SchemaDefinition: []*interfaces.Property{{
				Name: "content", Type: interfaces.DataType_Text,
				Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Vector}},
			}},
			IndexConfig: &interfaces.ResourceIndexConfig{DefaultEmbeddingModel: "embedding-1"},
		}
		mockRA.EXPECT().Update(gomock.Any(), gomock.Not(nil), resource, int64(0)).Return(int64(1), nil)
		mockDS.EXPECT().Update(gomock.Any(), resource).Return(nil)

		err := rs.Update(context.Background(), resource, &interfaces.ResourceRequest{
			CatalogID:        "cat1",
			Category:         interfaces.ResourceCategoryDataset,
			Name:             "dataset",
			SchemaDefinition: resource.SchemaDefinition,
			IndexConfig:      resource.IndexConfig,
		})

		require.NoError(t, err)
		assert.Equal(t, 3, resource.SchemaDefinition[0].Features[0].Config["dimension"])
	})
	t.Run("does not update dataset mapping when the resource version is stale", func(t *testing.T) {
		rs, mockRA, mockPS, mockDS, _, mockCS, mockBTA := newTestService(t)
		expectResourceServiceTransaction(t, rs, false)
		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockBTA.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return(nil, nil)
		mockCS.EXPECT().CheckExistByID(gomock.Any(), "cat1").Return(true, nil)
		resource := &interfaces.Resource{
			ID:             "r1",
			CatalogID:      "cat1",
			Category:       interfaces.ResourceCategoryDataset,
			Name:           "dataset",
			LocalIndexName: "vega-dataset-index-1",
			SchemaDefinition: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_String, Features: []interfaces.PropertyFeature{{
				FeatureName: "keyword", FeatureType: interfaces.PropertyFeatureType_Keyword,
				Config: map[string]any{"ignore_above": interfaces.DefaultTextKeywordIgnoreAbove},
			}}}},
		}
		mockDS.EXPECT().ListDocuments(gomock.Any(), resource, gomock.Any()).Return(nil, int64(0), nil)
		mockRA.EXPECT().Update(gomock.Any(), gomock.Not(nil), resource, int64(42)).Return(int64(0), nil)

		err := rs.Update(context.Background(), resource, &interfaces.ResourceRequest{
			CatalogID:          "cat1",
			Category:           interfaces.ResourceCategoryDataset,
			Name:               "dataset",
			ExpectedUpdateTime: 42,
			SchemaDefinition: []*interfaces.Property{
				{Name: "id", Type: interfaces.DataType_String},
				{Name: "content", Type: interfaces.DataType_Text},
			},
		})

		httpErr := requireResourceHTTPError(t, err, verrors.VegaBackend_Resource_UpdateConflict)
		assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
	})
	t.Run("rejects dataset index structure changes when documents exist", func(t *testing.T) {
		rs, _, mockPS, mockDS, _, _, mockBTA := newTestService(t)
		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockBTA.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return(nil, nil)
		resource := &interfaces.Resource{
			ID:               "r1",
			CatalogID:        "cat1",
			Category:         interfaces.ResourceCategoryDataset,
			Name:             "dataset",
			LocalIndexName:   "vega-dataset-index-1",
			SchemaDefinition: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_String}},
		}
		mockDS.EXPECT().ListDocuments(gomock.Any(), resource, gomock.Any()).
			Return([]map[string]any{{"id": "doc-1"}}, int64(1), nil)

		err := rs.Update(context.Background(), resource, &interfaces.ResourceRequest{
			CatalogID: "cat1",
			Category:  interfaces.ResourceCategoryDataset,
			Name:      "dataset",
			SchemaDefinition: []*interfaces.Property{
				{Name: "id", Type: interfaces.DataType_String},
				{Name: "content", Type: interfaces.DataType_Text},
			},
		})

		httpErr := requireResourceHTTPError(t, err, verrors.VegaBackend_InvalidParameter_RequestBody)
		assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
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

		var httpErr *rest.HTTPError
		ok := errors.As(err, &httpErr)
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
			SchemaDefinition: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_String, Features: []interfaces.PropertyFeature{{
				FeatureName: "keyword", FeatureType: interfaces.PropertyFeatureType_Keyword,
				Config: map[string]any{"ignore_above": interfaces.DefaultTextKeywordIgnoreAbove},
			}}}},
		}, &interfaces.ResourceRequest{
			CatalogID:        "cat1",
			Category:         interfaces.ResourceCategoryTable,
			Name:             "table",
			Description:      "new",
			SourceIdentifier: "public.orders",
			SchemaDefinition: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_String, Features: []interfaces.PropertyFeature{{
				FeatureName: "keyword", FeatureType: interfaces.PropertyFeatureType_Keyword,
				Config: map[string]any{"ignore_above": interfaces.DefaultTextKeywordIgnoreAbove},
			}}}},
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
				if len(got.SchemaDefinition) != 1 || len(got.SchemaDefinition[0].Features) != 2 {
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
			SchemaDefinition: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_String, Features: []interfaces.PropertyFeature{{
				FeatureName: "keyword", FeatureType: interfaces.PropertyFeatureType_Keyword,
				Config: map[string]any{"ignore_above": interfaces.DefaultTextKeywordIgnoreAbove},
			}}}},
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
	t.Run("update marks local index stale when key fields and schema features both change", func(t *testing.T) {
		rs, mockRA, mockPS, _, _, mockCS, mockBTA := newTestService(t)
		expectResourceServiceTransaction(t, rs, true)
		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockBTA.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return(nil, nil)
		mockCS.EXPECT().CheckExistByID(gomock.Any(), "cat1").Return(true, nil)
		mockRA.EXPECT().Update(gomock.Any(), gomock.Not(nil), gomock.Any(), int64(0)).Return(int64(1), nil)
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
			SchemaDefinition: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_String, Features: []interfaces.PropertyFeature{{
				FeatureName: "keyword", FeatureType: interfaces.PropertyFeatureType_Keyword,
				Config: map[string]any{"ignore_above": interfaces.DefaultTextKeywordIgnoreAbove},
			}}}},
			IndexConfig: &interfaces.ResourceIndexConfig{
				PrimaryKeyFields: []string{"id"}, IncrementalFields: []string{"id"},
			},
		}, &interfaces.ResourceRequest{
			CatalogID:        "cat1",
			Category:         interfaces.ResourceCategoryTable,
			Name:             "table",
			SourceIdentifier: "public.orders",
			SchemaDefinition: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_String, Features: []interfaces.PropertyFeature{{
				FeatureName: "fulltext", FeatureType: interfaces.PropertyFeatureType_Fulltext,
			}}}},
			IndexConfig: &interfaces.ResourceIndexConfig{
				PrimaryKeyFields: []string{"id"}, IncrementalFields: nil,
			},
		})
		require.NoError(t, err)
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

		var httpErr *rest.HTTPError
		ok := errors.As(err, &httpErr)
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
	t.Run("update key fields without a checkpoint does not update local index state", func(t *testing.T) {
		rs, mockRA, mockPS, _, _, mockCS, mockBTA := newTestService(t)
		expectResourceServiceTransaction(t, rs, true)
		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockBTA.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return(nil, nil)
		mockCS.EXPECT().CheckExistByID(gomock.Any(), "cat1").Return(true, nil)
		mockRA.EXPECT().Update(gomock.Any(), gomock.Not(nil), gomock.Any(), int64(0)).Return(int64(1), nil)

		err := rs.Update(context.Background(), &interfaces.Resource{
			ID:               "r1",
			CatalogID:        "cat1",
			Category:         interfaces.ResourceCategoryTable,
			Name:             "table",
			LocalIndexName:   "vega-build-r1-task-1",
			LocalIndexStatus: interfaces.ResourceLocalIndexStatusAvailable,
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
				PrimaryKeyFields:  []string{"id"},
				IncrementalFields: []string{"updated_at", "id"},
			},
		})

		require.NoError(t, err)
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

		var httpErr *rest.HTTPError
		ok := errors.As(err, &httpErr)
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
			SchemaDefinition: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_String, Features: []interfaces.PropertyFeature{{
				FeatureName: "keyword", FeatureType: interfaces.PropertyFeatureType_Keyword,
				Config: map[string]any{"ignore_above": interfaces.DefaultTextKeywordIgnoreAbove},
			}}}},
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
				Features: []interfaces.PropertyFeature{{
					FeatureName: "keyword", FeatureType: interfaces.PropertyFeatureType_Keyword,
					Config: map[string]any{"ignore_above": interfaces.DefaultTextKeywordIgnoreAbove},
				}},
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

		var httpErr *rest.HTTPError

		ok := errors.As(err, &httpErr)
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

		var httpErr *rest.HTTPError
		ok := errors.As(err, &httpErr)
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

		var httpErr *rest.HTTPError
		ok := errors.As(err, &httpErr)
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

		var httpErr *rest.HTTPError
		ok := errors.As(err, &httpErr)
		if !ok {
			t.Fatalf("expected HTTPError, got %T", err)
		}
		if httpErr.HTTPCode != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", httpErr.HTTPCode)
		}
	})
	t.Run("dataset update allows adding properties", func(t *testing.T) {
		rs, mockRA, mockPS, mockDS, _, mockCS, mockBTA := newTestService(t)
		expectResourceServiceTransaction(t, rs, true)
		mockPS.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockBTA.EXPECT().InternalList(gomock.Any(), gomock.Any()).Return(nil, nil)
		mockDS.EXPECT().ListDocuments(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, int64(0), nil)
		mockCS.EXPECT().CheckExistByID(gomock.Any(), "cat1").Return(true, nil)
		mockDS.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil)
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

func TestResourceServiceListAuthResourcesDoesNotFilterByPermission(t *testing.T) {
	ctrl := gomock.NewController(t)
	ra := vmock.NewMockResourceAccess(ctrl)
	rs := &resourceService{ra: ra}
	params := interfaces.AuthResourceQueryParams{
		PaginationQueryParams: interfaces.PaginationQueryParams{Offset: 2, Limit: 1},
	}
	want := []*interfaces.AuthResourceEntry{{
		ID: "resource-1", Name: "Resource One", Type: interfaces.AUTH_RESOURCE_TYPE_RESOURCE,
	}}
	ra.EXPECT().ListAuthResources(gomock.Any(), params).Return(want, int64(3), nil)

	got, total, err := rs.ListAuthResources(context.Background(), params)

	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	assert.Equal(t, want, got)
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
