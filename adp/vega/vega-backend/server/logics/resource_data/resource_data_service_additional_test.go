package resource_data

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/interfaces"
	mock_interfaces "vega-backend/interfaces/mock"
)

func TestPrepareSortParams(t *testing.T) {
	rds := &resourceDataService{}
	resource := &interfaces.Resource{
		SchemaDefinition: []*interfaces.Property{
			{Name: "name"},
			{Name: "age"},
		},
	}
	params := &interfaces.ResourceDataQueryParams{
		Sort: []*interfaces.SortField{
			{Field: "name", Direction: "asc"},
			{Field: "missing", Direction: "desc"},
			{Field: "__value", Direction: "desc"},
			{Field: "group_name", Direction: "asc"},
			{Field: "total", Direction: "desc"},
		},
		Aggregation: &interfaces.Aggregation{
			Alias: "total",
		},
		GroupBy: []*interfaces.GroupByItem{
			{Property: "group_name"},
		},
	}

	got := rds.prepareSortParams(resource, params)

	require.Same(t, params, got)
	assert.Equal(t, []*interfaces.SortField{
		{Field: "name", Direction: "asc"},
		{Field: "__value", Direction: "desc"},
		{Field: "group_name", Direction: "asc"},
		{Field: "total", Direction: "desc"},
	}, got.Sort)
}

func TestPrepareSortParamsNilInputs(t *testing.T) {
	rds := &resourceDataService{}
	params := &interfaces.ResourceDataQueryParams{}

	assert.Nil(t, rds.prepareSortParams(nil, nil))
	assert.Same(t, params, rds.prepareSortParams(nil, params))
	assert.Nil(t, rds.prepareSortParams(&interfaces.Resource{}, nil))
}

func TestQueryDatasetDelegatesToDatasetService(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockCS := mock_interfaces.NewMockCatalogService(ctrl)
	mockDS := mock_interfaces.NewMockDatasetService(ctrl)
	rds := &resourceDataService{cs: mockCS, ds: mockDS}
	resource := &interfaces.Resource{
		ID:        "dataset-1",
		CatalogID: "catalog-1",
		Category:  interfaces.ResourceCategoryDataset,
		SchemaDefinition: []*interfaces.Property{
			{Name: "name", Type: interfaces.DataType_String},
		},
	}
	params := &interfaces.ResourceDataQueryParams{
		FilterCondCfg: &interfaces.FilterCondCfg{
			Name:      "name",
			Operation: "==",
			ValueOptCfg: interfaces.ValueOptCfg{
				ValueFrom: interfaces.ValueFrom_Const,
				Value:     "alice",
			},
		},
	}
	wantRows := []map[string]any{{"name": "alice"}}

	mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", true).
		Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
	mockDS.EXPECT().ListDocuments(gomock.Any(), "dataset-1", resource, params).
		DoAndReturn(func(ctx context.Context, resourceID string, gotResource *interfaces.Resource,
			gotParams *interfaces.ResourceDataQueryParams) ([]map[string]any, int64, error) {
			require.NotNil(t, gotParams.ActualFilterCond)
			assert.Equal(t, "==", gotParams.ActualFilterCond.GetOperation())
			return wantRows, int64(1), nil
		})

	rows, total, err := rds.Query(context.Background(), resource, params)

	require.NoError(t, err)
	assert.Equal(t, wantRows, rows)
	assert.Equal(t, int64(1), total)
}

func TestQueryLogicViewPreparesParamsAndDelegates(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockCS := mock_interfaces.NewMockCatalogService(ctrl)
	mockLVS := mock_interfaces.NewMockLogicViewService(ctrl)
	rds := &resourceDataService{cs: mockCS, lvs: mockLVS}
	resource := &interfaces.Resource{
		ID:        "logic-view-1",
		CatalogID: "catalog-1",
		Category:  interfaces.ResourceCategoryLogicView,
		SchemaDefinition: []*interfaces.Property{
			{Name: "name"},
		},
	}
	params := &interfaces.ResourceDataQueryParams{
		Sort: []*interfaces.SortField{
			{Field: "name", Direction: "asc"},
			{Field: "missing", Direction: "desc"},
		},
		OutputFields: []string{"name", "missing"},
	}
	wantRows := []map[string]any{{"name": "alice"}}

	mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", true).
		Return(&interfaces.Catalog{ID: "catalog-1", Enabled: true}, nil)
	mockLVS.EXPECT().Query(gomock.Any(), resource, params).
		DoAndReturn(func(ctx context.Context, gotResource *interfaces.Resource,
			gotParams *interfaces.ResourceDataQueryParams) ([]map[string]any, int64, error) {
			assert.Equal(t, []*interfaces.SortField{{Field: "name", Direction: "asc"}}, gotParams.Sort)
			assert.Equal(t, []string{"name"}, gotParams.OutputFields)
			return wantRows, int64(1), nil
		})

	rows, total, err := rds.Query(context.Background(), resource, params)

	require.NoError(t, err)
	assert.Equal(t, wantRows, rows)
	assert.Equal(t, int64(1), total)
}
