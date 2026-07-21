package logic_view

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-comm-go/rest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
)

func TestLogicViewServiceQueryWithPaging(t *testing.T) {
	t.Run("returns error for unsupported logic type", func(t *testing.T) {
		svc := &logicViewService{}
		result, err := svc.QueryWithPaging(context.Background(), &interfaces.Resource{
			ID:        "logic-1",
			LogicType: "unsupported",
		}, &interfaces.ResourceDataQueryParams{})

		require.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "not supported")
	})
}

func TestLogicViewServiceCursorContinuation(t *testing.T) {
	t.Run("delegates cursor continuation to raw query service", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		queryService := vmock.NewMockRawQueryService(ctrl)
		svc := &logicViewService{qs: queryService}
		nextCursor := "next"
		totalCount := int64(1)
		queryService.EXPECT().Execute(gomock.Any(), gomock.Cond(func(req *interfaces.RawQueryRequest) bool {
			return req.Query == nil && req.QueryFormat == "" && req.Paging.Cursor == "opaque-cursor"
		})).Return(&interfaces.RawQueryResponse{
			Entries:    []map[string]any{{"id": "row-1"}},
			TotalCount: &totalCount,
			Paging:     &interfaces.PagingResponse{NextCursor: &nextCursor},
		}, nil)

		result, err := svc.QueryWithPaging(context.Background(), &interfaces.Resource{}, &interfaces.ResourceDataQueryParams{
			Paging: interfaces.PagingRequest{Cursor: "opaque-cursor"},
		})

		require.NoError(t, err)
		assert.Equal(t, []map[string]any{{"id": "row-1"}}, result.Entries)
		assert.Equal(t, &nextCursor, result.Paging.NextCursor)
		assert.True(t, result.NeedTotal)
	})
}

func TestQueryDerivedLogicViewRejectsUnavailableSource(t *testing.T) {
	view := &interfaces.LogicView{Resource: interfaces.Resource{LogicDefinition: []*interfaces.LogicDefinitionNode{{
		Type:   interfaces.LogicDefinitionNodeType_Resource,
		Config: map[string]any{"resource_id": "source-1"},
	}}}}

	t.Run("disabled source resource", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockRS := vmock.NewMockResourceService(ctrl)
		svc := &logicViewService{rs: mockRS}
		mockRS.EXPECT().GetByID(gomock.Any(), "source-1").Return(&interfaces.Resource{
			ID: "source-1", Status: interfaces.ResourceStatusDisabled,
		}, nil)

		_, _, err := svc.queryDerivedLogicView(context.Background(), view, &interfaces.ResourceDataQueryParams{})
		var httpErr *rest.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
	})

	t.Run("disabled source catalog", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockRS := vmock.NewMockResourceService(ctrl)
		mockCS := vmock.NewMockCatalogService(ctrl)
		svc := &logicViewService{rs: mockRS, cs: mockCS}
		mockRS.EXPECT().GetByID(gomock.Any(), "source-1").Return(&interfaces.Resource{
			ID: "source-1", CatalogID: "catalog-1", Status: interfaces.ResourceStatusActive,
		}, nil)
		mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", true).Return(&interfaces.Catalog{ID: "catalog-1", Enabled: false}, nil)

		_, _, err := svc.queryDerivedLogicView(context.Background(), view, &interfaces.ResourceDataQueryParams{})
		var httpErr *rest.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
	})
}

func TestDerivedIndexCursorRequiresSort(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockRS := vmock.NewMockResourceService(ctrl)
	mockCS := vmock.NewMockCatalogService(ctrl)
	svc := &logicViewService{rs: mockRS, cs: mockCS}
	source := &interfaces.Resource{
		ID:        "source-1",
		CatalogID: "catalog-1",
		Category:  interfaces.ResourceCategoryIndex,
		Status:    interfaces.ResourceStatusActive,
	}
	mockRS.EXPECT().GetByID(gomock.Any(), "source-1").Return(source, nil).AnyTimes()
	mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", true).
		Return(&interfaces.Catalog{ID: "catalog-1", Enabled: false}, nil).AnyTimes()

	result, err := svc.QueryWithPaging(context.Background(), &interfaces.Resource{
		ID:        "logic-1",
		Category:  interfaces.ResourceCategoryLogicView,
		LogicType: interfaces.LogicType_Derived,
		LogicDefinition: []*interfaces.LogicDefinitionNode{{
			Type:   interfaces.LogicDefinitionNodeType_Resource,
			Config: map[string]any{"resource_id": "source-1"},
		}},
	}, &interfaces.ResourceDataQueryParams{
		Paging: interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Limit: 10},
	})

	assert.Nil(t, result)
	var httpErr *rest.HTTPError
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
	assert.Contains(t, err.Error(), "sort is required")
}

func TestExecutePhysicalQuery(t *testing.T) {
	t.Run("returns error for unsupported category", func(t *testing.T) {
		rows, total, err := executePhysicalQuery(context.Background(), &interfaces.Catalog{}, &interfaces.Resource{
			ID:       "resource-1",
			Category: "unsupported",
		}, &interfaces.ResourceDataQueryParams{})

		require.Error(t, err)
		assert.Nil(t, rows)
		assert.Zero(t, total)
		assert.Contains(t, err.Error(), "unsupported resource category")
	})
}

func TestLogicViewServiceGetIndicesByView(t *testing.T) {
	t.Run("returns indices grouped by view resource", func(t *testing.T) {
		svc := &logicViewService{}
		catalog, indices, viewIndicesMap, err := svc.getIndicesByView(&interfaces.LogicView{
			RefResources: map[string]*interfaces.Resource{
				"resource-1": {
					ID:               "resource-1",
					CatalogID:        "catalog-1",
					SourceIdentifier: "db.public.orders",
				},
				"resource-2": {
					ID:               "resource-2",
					CatalogID:        "catalog-1",
					SourceIdentifier: "customers",
				},
			},
		})

		require.NoError(t, err)
		assert.Equal(t, "catalog-1", catalog)
		assert.ElementsMatch(t, []string{"orders", "customers"}, indices)
		assert.ElementsMatch(t, []string{"orders"}, viewIndicesMap["resource-1"])
		assert.ElementsMatch(t, []string{"customers"}, viewIndicesMap["resource-2"])
	})

	t.Run("returns error when catalogs differ", func(t *testing.T) {
		svc := &logicViewService{}
		catalog, indices, viewIndicesMap, err := svc.getIndicesByView(&interfaces.LogicView{
			Resource: interfaces.Resource{Name: "view-1"},
			RefResources: map[string]*interfaces.Resource{
				"resource-1": {
					ID:               "resource-1",
					CatalogID:        "catalog-1",
					SourceIdentifier: "orders",
				},
				"resource-2": {
					ID:               "resource-2",
					CatalogID:        "catalog-2",
					SourceIdentifier: "customers",
				},
			},
		})

		require.Error(t, err)
		assert.Empty(t, catalog)
		assert.Nil(t, indices)
		assert.Nil(t, viewIndicesMap)
		assert.Contains(t, err.Error(), "different catalog")
	})
}

func TestQueryAnalysisString(t *testing.T) {
	t.Run("returns error text when analysis has error", func(t *testing.T) {
		got := (&QueryAnalysis{Error: errors.New("parse failed")}).String()

		assert.Contains(t, got, "parse failed")
	})

	t.Run("formats fields and feature flags", func(t *testing.T) {
		got := (&QueryAnalysis{
			Fields: []FieldInfo{
				{Name: "id"},
				{Name: "count(*)", Alias: "total"},
				{Name: "*", IsStar: true},
			},
			HasUnion:     true,
			HasJoin:      true,
			HasAggregate: true,
			HasSubquery:  true,
			HasCase:      true,
		}).String()

		assert.Contains(t, got, "查询字段 (3 个)")
		assert.Contains(t, got, "count(*) AS total")
		assert.Contains(t, got, "包含UNION: true")
		assert.Contains(t, got, "包含CASE表达式: true")
	})
}

func TestQueryAnalysisGetFieldNames(t *testing.T) {
	t.Run("prefers aliases and preserves star", func(t *testing.T) {
		got := (&QueryAnalysis{Fields: []FieldInfo{
			{Name: "id"},
			{Name: "count(*)", Alias: "total"},
			{IsStar: true},
		}}).GetFieldNames()

		assert.Equal(t, []string{"id", "total", "*"}, got)
	})
}

func TestQueryAnalysisHasComplexFields(t *testing.T) {
	t.Run("returns true when any field is complex", func(t *testing.T) {
		assert.True(t, (&QueryAnalysis{Fields: []FieldInfo{{Name: "id"}, {Name: "count(*)", IsComplex: true}}}).HasComplexFields())
	})

	t.Run("returns false when all fields are simple", func(t *testing.T) {
		assert.False(t, (&QueryAnalysis{Fields: []FieldInfo{{Name: "id"}}}).HasComplexFields())
	})
}

func TestQueryAnalysisGetSimpleFieldNames(t *testing.T) {
	t.Run("excludes star and complex fields", func(t *testing.T) {
		got := (&QueryAnalysis{Fields: []FieldInfo{
			{Name: "id"},
			{Name: "name", Alias: "display_name"},
			{Name: "count(*)", IsComplex: true},
			{IsStar: true},
		}}).GetSimpleFieldNames()

		assert.Equal(t, []string{"id", "display_name"}, got)
	})
}

func TestQueryAnalysisFormatAsJSON(t *testing.T) {
	t.Run("formats analysis as json", func(t *testing.T) {
		got := (&QueryAnalysis{Fields: []FieldInfo{{Name: "id"}}}).FormatAsJSON()

		assert.Contains(t, got, `"fields":`)
		assert.Contains(t, got, `"name": "id"`)
	})
}
