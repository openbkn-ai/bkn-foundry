// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package local_index

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	vmock "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/mock"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/filter_condition"
)

func TestLocalIndexManagerValidateFilterCondition(t *testing.T) {
	condition := &filter_condition.BeforeCond{Value: []any{1, "day"}}
	properties := []*interfaces.Property{{Name: "created", OriginalName: "SOURCE_CREATED", Type: interfaces.DataType_Datetime}}
	want := errors.New("conversion failed")
	for _, failure := range []error{nil, want} {
		connector := vmock.NewMockIndexConnector(gomock.NewController(t))
		connector.EXPECT().ConvertFilterCondition(condition, gomock.Any()).DoAndReturn(
			func(got interfaces.FilterCondition, schema []*interfaces.Property) (map[string]any, error) {
				assert.Same(t, condition, got)
				require.Len(t, schema, 1)
				assert.Equal(t, "created", schema[0].OriginalName)
				assert.NotSame(t, properties[0], schema[0])
				return nil, failure
			})
		err := (&localIndexManager{lic: connector}).ValidateFilterCondition(condition, properties)
		assert.ErrorIs(t, err, failure)
		assert.Equal(t, "SOURCE_CREATED", properties[0].OriginalName)
	}
	require.NoError(t, (&localIndexManager{}).ValidateFilterCondition(nil, nil))
}

func TestLocalIndexManagerGetIndexCapabilities(t *testing.T) {
	for _, tc := range []struct {
		name            string
		standard, index bool
	}{
		{name: "both HanLP analyzers installed", standard: true, index: true},
		{name: "only hanlp_standard installed", standard: true},
		{name: "only hanlp_index installed", index: true},
		{name: "neither HanLP analyzer installed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			connector := vmock.NewMockIndexConnector(gomock.NewController(t))
			manager := &localIndexManager{lic: connector}
			connector.EXPECT().ValidateAnalyzer(gomock.Any(), "standard").Return(true, nil)
			connector.EXPECT().ValidateAnalyzer(gomock.Any(), "english").Return(true, nil)
			connector.EXPECT().ValidateAnalyzer(gomock.Any(), "ik_max_word").Return(false, nil)
			connector.EXPECT().ValidateAnalyzer(gomock.Any(), "hanlp_standard").Return(tc.standard, nil)
			connector.EXPECT().ValidateAnalyzer(gomock.Any(), "hanlp_index").Return(tc.index, nil)
			want := []interfaces.AnalyzerCapability{{ID: "standard"}, {ID: "english"}}
			if tc.standard {
				want = append(want, interfaces.AnalyzerCapability{ID: "hanlp_standard"})
			}
			if tc.index {
				want = append(want, interfaces.AnalyzerCapability{ID: "hanlp_index"})
			}
			capabilities, err := manager.GetIndexCapabilities(context.Background())
			require.NoError(t, err)
			assert.Equal(t, want, capabilities.FulltextAnalyzers)
			for analyzer, expected := range map[string]bool{"hanlp_standard": tc.standard, "hanlp_index": tc.index} {
				available, err := manager.ValidateAnalyzer(context.Background(), analyzer)
				require.NoError(t, err)
				assert.Equal(t, expected, available)
			}
			cached, err := manager.GetIndexCapabilities(context.Background())
			require.NoError(t, err)
			assert.Equal(t, capabilities, cached)
		})
	}
	t.Run("refreshes an expired error after OpenSearch recovers", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		ctx := context.Background()
		connector := vmock.NewMockIndexConnector(ctrl)
		manager := &localIndexManager{lic: connector}

		connector.EXPECT().ValidateAnalyzer(gomock.Any(), "standard").Return(false, errors.New("connection refused"))
		capabilities, err := manager.GetIndexCapabilities(ctx)
		require.Error(t, err)
		assert.Nil(t, capabilities)
		var unavailableErr *interfaces.IndexCapabilitiesUnavailableError
		assert.ErrorAs(t, err, &unavailableErr)

		capabilities, err = manager.GetIndexCapabilities(ctx)
		require.Error(t, err)
		assert.Nil(t, capabilities)

		manager.capabilityMu.Lock()
		manager.capabilityErrorExpires = time.Now().Add(-time.Second)
		manager.capabilityMu.Unlock()

		connector.EXPECT().ValidateAnalyzer(gomock.Any(), "standard").Return(true, nil)
		connector.EXPECT().ValidateAnalyzer(gomock.Any(), "english").Return(true, nil)
		connector.EXPECT().ValidateAnalyzer(gomock.Any(), "ik_max_word").Return(true, nil)
		connector.EXPECT().ValidateAnalyzer(gomock.Any(), "hanlp_standard").Return(false, nil)
		connector.EXPECT().ValidateAnalyzer(gomock.Any(), "hanlp_index").Return(false, nil)

		capabilities, err = manager.GetIndexCapabilities(ctx)
		require.NoError(t, err)
		assert.Equal(t, []interfaces.AnalyzerCapability{
			{ID: "standard"},
			{ID: "english"},
			{ID: "ik_max_word"},
		}, capabilities.FulltextAnalyzers)
		assert.Positive(t, capabilities.CheckedAt)
	})

	t.Run("missing analyzer does not discard available analyzers", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		ctx := context.Background()
		connector := vmock.NewMockIndexConnector(ctrl)
		manager := &localIndexManager{lic: connector}

		connector.EXPECT().ValidateAnalyzer(gomock.Any(), "standard").Return(true, nil)
		connector.EXPECT().ValidateAnalyzer(gomock.Any(), "english").Return(true, nil)
		connector.EXPECT().ValidateAnalyzer(gomock.Any(), "ik_max_word").Return(true, nil)
		connector.EXPECT().ValidateAnalyzer(gomock.Any(), "hanlp_standard").Return(false, nil)
		connector.EXPECT().ValidateAnalyzer(gomock.Any(), "hanlp_index").Return(false, nil)

		capabilities, err := manager.GetIndexCapabilities(ctx)
		require.NoError(t, err)
		assert.Equal(t, []interfaces.AnalyzerCapability{
			{ID: "standard"},
			{ID: "english"},
			{ID: "ik_max_word"},
		}, capabilities.FulltextAnalyzers)
	})

	t.Run("coalesces concurrent refreshes", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		ctx := context.Background()
		connector := vmock.NewMockIndexConnector(ctrl)
		manager := &localIndexManager{lic: connector}

		started := make(chan struct{})
		release := make(chan struct{})
		connector.EXPECT().ValidateAnalyzer(gomock.Any(), "standard").DoAndReturn(func(context.Context, string) (bool, error) {
			close(started)
			<-release
			return true, nil
		})
		connector.EXPECT().ValidateAnalyzer(gomock.Any(), "english").Return(true, nil)
		connector.EXPECT().ValidateAnalyzer(gomock.Any(), "ik_max_word").Return(true, nil)
		connector.EXPECT().ValidateAnalyzer(gomock.Any(), "hanlp_standard").Return(false, nil)
		connector.EXPECT().ValidateAnalyzer(gomock.Any(), "hanlp_index").Return(false, nil)

		const callers = 8
		results := make(chan error, callers)
		var ready sync.WaitGroup
		ready.Add(callers)
		for range callers {
			go func() {
				ready.Done()
				_, err := manager.GetIndexCapabilities(ctx)
				results <- err
			}()
		}
		ready.Wait()
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("capability refresh did not start")
		}
		close(release)
		for range callers {
			require.NoError(t, <-results)
		}
	})
}

func TestLocalIndexManagerValidateAnalyzerUsesRefreshedSnapshot(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	ctx := context.Background()
	connector := vmock.NewMockIndexConnector(ctrl)
	manager := &localIndexManager{lic: connector}

	connector.EXPECT().ValidateAnalyzer(gomock.Any(), "standard").Return(true, nil)
	connector.EXPECT().ValidateAnalyzer(gomock.Any(), "english").Return(true, nil)
	connector.EXPECT().ValidateAnalyzer(gomock.Any(), "ik_max_word").Return(true, nil)
	connector.EXPECT().ValidateAnalyzer(gomock.Any(), "hanlp_standard").Return(false, nil)
	connector.EXPECT().ValidateAnalyzer(gomock.Any(), "hanlp_index").Return(false, nil)

	available, err := manager.ValidateAnalyzer(ctx, "ik_max_word")
	require.NoError(t, err)
	assert.True(t, available)
}

func TestLocalIndexManagerListDocuments(t *testing.T) {
	t.Run("grouped decimal metadata reaches the connector before result ordering", func(t *testing.T) {
		for _, countHaving := range []bool{false, true} {
			connector := vmock.NewMockIndexConnector(gomock.NewController(t))
			original := map[string]any{"precision": 20, "scale": 4}
			resource := &interfaces.Resource{Category: interfaces.ResourceCategoryTable, SchemaDefinition: []*interfaces.Property{{Name: "key", Type: interfaces.DataType_Integer}, {Name: "amount", OriginalName: "source_amount", Type: interfaces.DataType_Decimal, Attributes: original}}, SourceMetadata: map[string]any{"columns": []interfaces.TableColumnMeta{{Name: "source_amount", NumPrecision: 12, NumScale: 2}}}}
			params := &interfaces.ResourceDataQueryParams{GroupBy: []*interfaces.GroupByItem{{Property: "key"}}, Aggregation: &interfaces.Aggregation{Property: "amount", Aggr: "sum", Alias: "total"}, Sort: []*interfaces.SortField{{Field: "total", Direction: "desc"}}, Paging: interfaces.PagingRequest{Limit: 1}}
			if countHaving {
				params.Having = &interfaces.HavingClause{Field: "count(*)", Operation: ">", Value: 0}
			}
			connector.EXPECT().ExecuteQuery(gomock.Any(), "index", gomock.Any(), params).DoAndReturn(func(_ context.Context, _ string, queryResource *interfaces.Resource, _ *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
				assert.Equal(t, 12, queryResource.SchemaDefinition[1].Attributes["precision"])
				assert.Equal(t, 2, queryResource.SchemaDefinition[1].Attributes["scale"])
				return &interfaces.ResourceDataQueryResult{Entries: []map[string]any{{"key": 1, "total": json.Number("0.3")}}}, nil
			})
			result, err := (&localIndexManager{lic: connector}).ListDocuments(t.Context(), "index", resource, params)
			require.NoError(t, err)
			assert.Equal(t, []map[string]any{{"key": 1, "total": json.Number("0.3")}}, result.Entries)
			assert.Equal(t, map[string]any{"precision": 20, "scale": 4}, original)
			assert.Equal(t, original, resource.SchemaDefinition[1].Attributes)
		}
	})

	t.Run("decimal queries use the target index without fetching metadata", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			params *interfaces.ResourceDataQueryParams
		}{
			{name: "list", params: &interfaces.ResourceDataQueryParams{}},
			{name: "decimal having", params: &interfaces.ResourceDataQueryParams{Aggregation: &interfaces.Aggregation{Property: "amount", Aggr: "sum", Alias: "total"}, Having: &interfaces.HavingClause{Field: "total", Operation: "==", Value: json.Number("0.30")}}},
			{name: "measure", params: &interfaces.ResourceDataQueryParams{Aggregation: &interfaces.Aggregation{Property: "amount", Aggr: "sum"}}},
			{name: "group", params: &interfaces.ResourceDataQueryParams{Aggregation: &interfaces.Aggregation{Property: "id", Aggr: "count"}, GroupBy: []*interfaces.GroupByItem{{Property: "amount"}}}},
			{name: "sort", params: &interfaces.ResourceDataQueryParams{Aggregation: &interfaces.Aggregation{Property: "id", Aggr: "count"}, Sort: []*interfaces.SortField{{Field: "amount", Direction: "asc"}}}},
			{name: "filter", params: &interfaces.ResourceDataQueryParams{Aggregation: &interfaces.Aggregation{Property: "id", Aggr: "count"}, FilterCondCfg: &interfaces.FilterCondCfg{Name: "id", Operation: "==", Value: 1, ValueFrom: interfaces.ValueFrom_Const}}},
			{name: "unreferenced decimal", params: &interfaces.ResourceDataQueryParams{Aggregation: &interfaces.Aggregation{Property: "id", Aggr: "count"}}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				connector := vmock.NewMockIndexConnector(gomock.NewController(t))
				resource := &interfaces.Resource{
					LocalIndexName: "saved-index",
					SchemaDefinition: []*interfaces.Property{
						{Name: "id", Type: interfaces.DataType_Integer},
						{Name: "amount", OriginalName: "source_amount", Type: interfaces.DataType_Decimal},
					},
					SourceMetadata: map[string]any{"columns": []interfaces.TableColumnMeta{{Name: "source_amount", NumPrecision: 12, NumScale: 2}}},
				}
				schema := append([]*interfaces.Property(nil), resource.SchemaDefinition...)
				expected := &interfaces.ResourceDataQueryResult{}
				connector.EXPECT().ExecuteQuery(gomock.Any(), "target-index", gomock.Any(), tc.params).DoAndReturn(func(_ context.Context, _ string, queryResource *interfaces.Resource, _ *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
					if tc.params.Having != nil {
						assert.Equal(t, 12, queryResource.SchemaDefinition[1].Attributes["precision"])
						assert.Equal(t, 2, queryResource.SchemaDefinition[1].Attributes["scale"])
					}
					return expected, nil
				})
				result, err := (&localIndexManager{lic: connector}).ListDocuments(context.Background(), "target-index", resource, tc.params)
				require.NoError(t, err)
				assert.Same(t, expected, result)
				assert.Equal(t, "saved-index", resource.LocalIndexName)
				assert.Equal(t, schema, resource.SchemaDefinition)
				assert.Equal(t, "source_amount", resource.SchemaDefinition[1].OriginalName)
				assert.Nil(t, resource.SchemaDefinition[1].Attributes)
			})
		}
	})
	t.Run("missing resource fails before invoking the connector", func(t *testing.T) {
		connector := vmock.NewMockIndexConnector(gomock.NewController(t))
		result, err := (&localIndexManager{lic: connector}).ListDocuments(context.Background(), "index", nil, nil)
		var conditionError *interfaces.ConditionBuildError
		require.ErrorAs(t, err, &conditionError)
		assert.Nil(t, result)
	})
	t.Run("exact distinct uses schema without rebuilding mapping", func(t *testing.T) {
		for _, category := range []string{interfaces.ResourceCategoryTable, interfaces.ResourceCategoryDataset} {
			t.Run(category, func(t *testing.T) {
				connector := vmock.NewMockIndexConnector(gomock.NewController(t))
				manager := &localIndexManager{lic: connector}
				sourceAttributes := map[string]any{"source_precision": 20}
				resource := &interfaces.Resource{Category: category, SchemaDefinition: []*interfaces.Property{
					{Name: "id", OriginalName: "source_id", Type: interfaces.DataType_Integer, OriginalType: "BIGINT", Attributes: sourceAttributes},
					{Name: "amount", Type: interfaces.DataType_Decimal, OriginalType: "DECIMAL"},
					{Name: "body", Type: interfaces.DataType_Text, Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Keyword, FeatureName: "raw", Config: map[string]any{"ignore_above": 128}}}},
				}}
				params := &interfaces.ResourceDataQueryParams{Aggregation: &interfaces.Aggregation{Property: "id", Aggr: "count_distinct", AllowApproximate: new(false)}}
				expected := &interfaces.ResourceDataQueryResult{Entries: []map[string]any{{"__value": int64(2)}}}
				connector.EXPECT().ExecuteQuery(gomock.Any(), "index", gomock.Any(), params).DoAndReturn(
					func(_ context.Context, _ string, queryResource *interfaces.Resource, _ *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
						fields := map[string]*interfaces.Property{}
						for _, prop := range queryResource.SchemaDefinition {
							fields[prop.Name] = prop
						}
						assert.Equal(t, "id", fields["id"].OriginalName)
						assert.Equal(t, "BIGINT", fields["id"].OriginalType)
						assert.Equal(t, map[string]any{"source_precision": 20}, fields["id"].Attributes)
						assert.Equal(t, "DECIMAL", fields["amount"].OriginalType)
						assert.Nil(t, fields["amount"].Attributes)
						assert.Empty(t, fields["body"].OriginalType)
						assert.Nil(t, fields["body"].Attributes)
						assert.Equal(t, map[string]any{"ignore_above": 128}, fields["body"].Features[0].Config)
						return expected, nil
					})
				got, err := manager.ListDocuments(context.Background(), "index", resource, params)
				require.NoError(t, err)
				assert.Same(t, expected, got)
				assert.Equal(t, "BIGINT", resource.SchemaDefinition[0].OriginalType)
				assert.Equal(t, "source_id", resource.SchemaDefinition[0].OriginalName)
				assert.Equal(t, map[string]any{"source_precision": 20}, resource.SchemaDefinition[0].Attributes)
				assert.Empty(t, resource.SchemaDefinition[2].OriginalType)
			})
		}
	})
	t.Run("table JSON results are returned as text", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		connector := vmock.NewMockIndexConnector(ctrl)
		manager := &localIndexManager{lic: connector}
		resource := &interfaces.Resource{Category: interfaces.ResourceCategoryTable, SchemaDefinition: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_Integer}, {Name: "payload", Type: interfaces.DataType_Json}}}
		params := &interfaces.ResourceDataQueryParams{}
		stored := []map[string]any{{"payload": map[string]any{"z": "<>&", "a": json.Number("9007199254740993")}}, {"payload": "scalar"}, {"payload": "{incomplete"}, {"payload": nil}, {}}
		queryResource, prepErr := resourceForQuery(resource)
		require.NoError(t, prepErr)
		connector.EXPECT().ExecuteQuery(gomock.Any(), "index", queryResource, params).Return(&interfaces.ResourceDataQueryResult{Entries: stored, TotalCount: 5, SearchAfter: []any{"next"}}, nil)
		gotResult, err := manager.ListDocuments(context.Background(), "index", resource, params)
		var got []map[string]any
		var total int64
		if gotResult != nil {
			got = gotResult.Entries
			total = gotResult.TotalCount
		}
		require.NoError(t, err)
		require.Len(t, got, 5)
		require.IsType(t, "", got[0]["payload"])
		text := got[0]["payload"].(string)
		var decoded any
		require.NoError(t, common.UnmarshalPreciseJSON([]byte(text), &decoded))
		assert.Equal(t, map[string]any{"a": json.Number("9007199254740993"), "z": "<>&"}, decoded)
		assert.Contains(t, text, "<>&")
		assert.Equal(t, []map[string]any{{"payload": "scalar"}, {"payload": "{incomplete"}, {"payload": nil}, {}}, got[1:])
		assert.Equal(t, int64(5), total)
		assert.Equal(t, []any{"next"}, gotResult.SearchAfter)
		assert.Empty(t, params.SearchAfter)
		assert.Len(t, SchemaForQuery(resource.SchemaDefinition), 1)
	})
	t.Run("dataset JSON values retain document types", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		connector := vmock.NewMockIndexConnector(ctrl)
		manager := &localIndexManager{lic: connector}
		resource := &interfaces.Resource{Category: interfaces.ResourceCategoryDataset, SchemaDefinition: []*interfaces.Property{{Name: "payload", Type: interfaces.DataType_Json}}}
		params := &interfaces.ResourceDataQueryParams{}
		values := []any{map[string]any{"id": "creator", "number": json.Number("9007199254740993")}, []any{"value", true}, json.Number("9007199254740993"), true, "{incomplete", nil}
		stored := make([]map[string]any, 0, len(values)+1)
		for _, value := range values {
			stored = append(stored, map[string]any{"payload": value})
		}
		stored = append(stored, map[string]any{})
		queryResource, prepErr := resourceForQuery(resource)
		require.NoError(t, prepErr)
		connector.EXPECT().ExecuteQuery(gomock.Any(), "dataset-index", queryResource, params).
			Return(&interfaces.ResourceDataQueryResult{Entries: stored, TotalCount: int64(len(stored)), SearchAfter: []any{"next"}}, nil)

		gotResult2, err := manager.ListDocuments(context.Background(), "dataset-index", resource, params)
		var got []map[string]any
		var total int64
		if gotResult2 != nil {
			got = gotResult2.Entries
			total = gotResult2.TotalCount
		}

		require.NoError(t, err)
		require.Len(t, got, len(values)+1)
		for i, value := range values {
			assert.Equal(t, value, got[i]["payload"])
		}
		assert.Empty(t, got[len(values)])
		assert.Equal(t, int64(len(values)+1), total)
		assert.Equal(t, []any{"next"}, gotResult2.SearchAfter)
		assert.Empty(t, params.SearchAfter)
	})
	t.Run("aggregate alias matching a JSON field stays numeric", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		connector := vmock.NewMockIndexConnector(ctrl)
		manager := &localIndexManager{lic: connector}
		resource := &interfaces.Resource{SchemaDefinition: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_Integer}, {Name: "payload", Type: interfaces.DataType_Json}}}
		params := &interfaces.ResourceDataQueryParams{Aggregation: &interfaces.Aggregation{Property: "id", Aggr: "count", Alias: "payload"}}
		queryResource, prepErr := resourceForQuery(resource)
		require.NoError(t, prepErr)
		connector.EXPECT().ExecuteQuery(gomock.Any(), "index", queryResource, params).Return(&interfaces.ResourceDataQueryResult{Entries: []map[string]any{{"payload": int64(5)}}, TotalCount: 5}, nil)
		gotResult3, err := manager.ListDocuments(context.Background(), "index", resource, params)
		var got []map[string]any
		if gotResult3 != nil {
			got = gotResult3.Entries
		}
		require.NoError(t, err)
		assert.Equal(t, int64(5), got[0]["payload"])
	})

	ctrl := gomock.NewController(t)
	connector := vmock.NewMockIndexConnector(ctrl)
	resource := &interfaces.Resource{Category: interfaces.ResourceCategoryTable,
		SchemaDefinition: []*interfaces.Property{{Name: "id", OriginalName: "SOURCE_ID", Type: interfaces.DataType_Integer}}}
	params := &interfaces.ResourceDataQueryParams{
		Sort:         []*interfaces.SortField{{Field: "_score", Direction: "desc"}, {Field: "id", Direction: "asc"}},
		OutputFields: []string{"id", "_score"}, SearchAfter: []any{0.875, 1}}
	rows := []map[string]any{{"id": 2, "_score": 0.875}}
	connector.EXPECT().ExecuteQuery(gomock.Any(), "table-index", gomock.Any(), params).DoAndReturn(
		func(_ context.Context, _ string, physical *interfaces.Resource, actual *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
			assert.Equal(t, "id", physical.SchemaDefinition[0].OriginalName)
			assert.Equal(t, "_score", actual.Sort[0].Field)
			assert.Equal(t, []string{"id", "_score"}, actual.OutputFields)
			assert.Equal(t, []any{0.875, 1}, actual.SearchAfter)
			return &interfaces.ResourceDataQueryResult{Entries: rows, TotalCount: 3, SearchAfter: []any{0.875, 2}}, nil
		})
	entriesResult, err := (&localIndexManager{lic: connector}).ListDocuments(context.Background(), "table-index", resource, params)
	var entries []map[string]any
	var total int64
	if entriesResult != nil {
		entries = entriesResult.Entries
		total = entriesResult.TotalCount
	}
	require.NoError(t, err)
	assert.Equal(t, rows, entries)
	assert.Equal(t, int64(3), total)
	assert.Equal(t, []any{0.875, 2}, entriesResult.SearchAfter)
	assert.Equal(t, []any{0.875, 1}, params.SearchAfter)
	assert.Equal(t, "SOURCE_ID", resource.SchemaDefinition[0].OriginalName)
}

func TestLocalIndexManagerDelegatesToIndexConnector(t *testing.T) {
	t.Run("local index manager delegates to index connector", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		ctx := context.Background()
		connector := vmock.NewMockIndexConnector(ctrl)
		manager := &localIndexManager{lic: connector}
		schema := []*interfaces.Property{{Name: "id", Type: "integer"}}
		resource := &interfaces.Resource{ID: "resource-1", SchemaDefinition: schema}
		params := &interfaces.ResourceDataQueryParams{}
		docs := []map[string]any{{"id": 1}}
		queryResult := &interfaces.ResourceDataQueryResult{
			IsApproximate: true,
			Entries:       []map[string]any{{"id": 1}},
			TotalCount:    1,
			SearchAfter:   []any{"sort-1"},
		}
		document := map[string]any{"id": 1}
		docIDs := []string{"doc-1"}
		properties := map[string]any{
			"id": map[string]any{"type": "long"},
		}

		connector.EXPECT().CreateIndex(ctx, "idx", properties, nil).Return(nil)
		connector.EXPECT().UpdateIndex(ctx, "idx", properties).Return(nil)
		connector.EXPECT().DeleteIndex(ctx, "idx").Return(nil)
		connector.EXPECT().CheckIndexExist(ctx, "idx").Return(true, nil)
		queryResource, prepErr := resourceForQuery(resource)
		require.NoError(t, prepErr)
		connector.EXPECT().ExecuteQuery(ctx, "idx", queryResource, params).Return(queryResult, nil)
		connector.EXPECT().GetDocument(ctx, "idx", "doc-1").Return(document, nil)
		connector.EXPECT().GetDocuments(ctx, "idx", []string{"doc-1", "missing"}).Return([]map[string]any{document, nil}, nil)
		connector.EXPECT().CreateDocuments(ctx, "idx", docs).Return(docIDs, nil)
		connector.EXPECT().UpsertDocuments(ctx, "idx", docs).Return(docIDs, nil)
		connector.EXPECT().DeleteDocument(ctx, "idx", "doc-1").Return(nil)
		connector.EXPECT().DeleteDocuments(ctx, "idx", []string{"doc-1", "doc-2"}).Return(nil)

		require.NoError(t, manager.CreateIndex(ctx, "idx", schema, nil))
		require.NoError(t, manager.UpdateIndex(ctx, "idx", schema))
		require.NoError(t, manager.DeleteIndex(ctx, "idx"))

		exists, err := manager.CheckIndexExist(ctx, "idx")
		require.NoError(t, err)
		assert.True(t, exists)

		rowsResult, err := manager.ListDocuments(ctx, "idx", resource, params)
		var rows []map[string]any
		var total int64
		if rowsResult != nil {
			rows = rowsResult.Entries
			total = rowsResult.TotalCount
		}
		require.NoError(t, err)
		assert.Equal(t, []map[string]any{{"id": 1}}, rows)
		assert.Equal(t, int64(1), total)
		assert.Same(t, queryResult, rowsResult)
		assert.True(t, rowsResult.IsApproximate)
		assert.Equal(t, []any{"sort-1"}, rowsResult.SearchAfter)
		assert.Empty(t, params.SearchAfter)

		doc, err := manager.GetDocument(ctx, "idx", "doc-1")
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"id": 1}, doc)

		loaded, err := manager.GetDocuments(ctx, "idx", []string{"doc-1", "missing"})
		require.NoError(t, err)
		assert.Equal(t, []map[string]any{document, nil}, loaded)

		created, err := manager.CreateDocuments(ctx, "idx", docs)
		require.NoError(t, err)
		assert.Equal(t, []string{"doc-1"}, created)

		upserted, err := manager.UpsertDocuments(ctx, "idx", docs)
		require.NoError(t, err)
		assert.Equal(t, []string{"doc-1"}, upserted)

		require.NoError(t, manager.DeleteDocument(ctx, "idx", "doc-1"))
		require.NoError(t, manager.DeleteDocuments(ctx, "idx", []string{"doc-1", "doc-2"}))
	})
}

func TestLocalIndexManagerDeleteDocumentsByQueryDelegatesToConnector(t *testing.T) {
	t.Run("local index manager delegates a prepared delete query", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		ctx := context.Background()
		connector := vmock.NewMockIndexConnector(ctrl)
		manager := &localIndexManager{lic: connector}
		resource := &interfaces.Resource{
			LocalIndexName: "saved-index",
			SchemaDefinition: []*interfaces.Property{
				{Name: "id", Type: "integer"},
				{Name: "amount", Type: interfaces.DataType_Decimal, OriginalType: "decimal(12,2)"},
			},
		}
		params := &interfaces.ResourceDataQueryParams{
			FilterCondCfg: &interfaces.FilterCondCfg{
				Name:      "id",
				Operation: "==",
				ValueFrom: interfaces.ValueFrom_Const,
				Value:     1,
			},
		}
		var gotParams *interfaces.ResourceDataQueryParams
		var gotSchema []*interfaces.Property
		connector.EXPECT().
			DeleteDocumentsByQuery(ctx, "idx", params, SchemaForQuery(resource.SchemaDefinition)).
			DoAndReturn(func(_ context.Context, _ string, p *interfaces.ResourceDataQueryParams, schema []*interfaces.Property) (*interfaces.DeleteByQueryResult, error) {
				gotParams = p
				gotSchema = schema
				return &interfaces.DeleteByQueryResult{}, nil
			})

		_, err := manager.DeleteDocumentsByQuery(ctx, "idx", resource, params)
		require.NoError(t, err)
		assert.Nil(t, params.ActualFilterCond)
		assert.Same(t, params, gotParams)
		assert.Equal(t, SchemaForQuery(resource.SchemaDefinition), gotSchema)
		assert.Equal(t, "saved-index", resource.LocalIndexName)
	})
}

func TestSchemaForQueryUsesManagedFieldNames(t *testing.T) {
	t.Run("large integer uses the physical string query contract", func(t *testing.T) {
		source := &interfaces.Property{Name: "wide", OriginalName: "source_wide", Type: interfaces.DataType_LargeInteger, OriginalType: "LARGEINT"}
		got := SchemaForQuery([]*interfaces.Property{source})
		require.Len(t, got, 1)
		assert.Equal(t, interfaces.DataType_String, got[0].Type)
		assert.Equal(t, "wide", got[0].OriginalName)
		assert.Equal(t, interfaces.DataType_LargeInteger, source.Type)
		assert.Equal(t, "source_wide", source.OriginalName)
		fields := map[string]*interfaces.Property{"wide": got[0]}
		for _, op := range []string{"==", "like", "prefix"} {
			actual, err := filter_condition.NewFilterCondition(context.Background(), &interfaces.FilterCondCfg{Name: "wide", Operation: op, Value: "0100"}, fields)
			require.NoError(t, err)
			assert.Equal(t, op, actual.GetOperation())
		}
		for _, op := range []string{"range", "out_range", "between"} {
			actual, err := filter_condition.NewFilterCondition(context.Background(), &interfaces.FilterCondCfg{Name: "wide", Operation: op, Value: []any{"-09", "010"}}, fields)
			require.Error(t, err)
			assert.Nil(t, actual)
		}
	})
	schema := []*interfaces.Property{{
		Name:         "body",
		OriginalName: "source_body",
		Type:         interfaces.DataType_Text,
	}, {Name: "payload", Type: interfaces.DataType_Json}, {Name: "blob", Type: interfaces.DataType_Binary}, {Name: "unknown", Type: interfaces.DataType_Other}, nil}

	got := SchemaForQuery(schema)

	require.Len(t, got, 1)
	assert.Equal(t, "body", got[0].OriginalName)
	assert.Equal(t, "source_body", schema[0].OriginalName)
}

func TestLocalIndexManagerRejectsUnavailableQueryFields(t *testing.T) {
	for _, fieldType := range []string{interfaces.DataType_Json, interfaces.DataType_Binary, interfaces.DataType_Other} {
		t.Run(fieldType, func(t *testing.T) {
			for _, params := range []*interfaces.ResourceDataQueryParams{
				{Sort: []*interfaces.SortField{{Field: "payload"}}},
				{Aggregation: &interfaces.Aggregation{Property: "payload", Aggr: "count"}},
				{GroupBy: []*interfaces.GroupByItem{{Property: "payload"}}},
			} {
				connector := vmock.NewMockIndexConnector(gomock.NewController(t))
				manager := &localIndexManager{lic: connector}
				resource := &interfaces.Resource{SchemaDefinition: []*interfaces.Property{{Name: "payload", Type: fieldType}}}
				rowsResult, err := manager.ListDocuments(context.Background(), "idx", resource, params)
				var rows []map[string]any
				var total int64
				if rowsResult != nil {
					rows = rowsResult.Entries
					total = rowsResult.TotalCount
				}
				assert.Nil(t, rows)
				assert.Zero(t, total)
				var conditionError *interfaces.ConditionBuildError
				require.ErrorAs(t, err, &conditionError)
				assert.ErrorContains(t, err, `field "payload" not found in schema`)
			}
		})
	}
}

func TestValidateQueryFieldsAggregationAliases(t *testing.T) {
	t.Run("implicit count value is an output rather than a source field", func(t *testing.T) {
		resource := &interfaces.Resource{SchemaDefinition: []*interfaces.Property{{Name: "key", Type: interfaces.DataType_Integer}}}
		params := &interfaces.ResourceDataQueryParams{GroupBy: []*interfaces.GroupByItem{{Property: "key"}}, Having: &interfaces.HavingClause{Field: "count(*)", Operation: ">", Value: 0}, Sort: []*interfaces.SortField{{Field: "__value", Direction: interfaces.DESC_DIRECTION}}}
		require.NoError(t, validateQueryFields(resource, params))
		params.Having = nil
		require.ErrorContains(t, validateQueryFields(resource, params), "not found")
	})

	for _, alias := range []string{"payload", "__value"} {
		resource := &interfaces.Resource{SchemaDefinition: []*interfaces.Property{{Name: alias, Type: interfaces.DataType_Json}, {Name: "amount", Type: interfaces.DataType_Integer}}}
		for _, declared := range []string{alias, ""} {
			if declared == "" && alias != "__value" {
				continue
			}
			params := &interfaces.ResourceDataQueryParams{Aggregation: &interfaces.Aggregation{Property: "amount", Aggr: "sum", Alias: declared}, Sort: []*interfaces.SortField{{Field: alias}}}
			require.NoError(t, validateQueryFields(resource, params))
			params.Aggregation.Property = alias
			require.ErrorContains(t, validateQueryFields(resource, params), "not found")
		}
	}
}

func TestValidateQueryFieldsKnownSchema(t *testing.T) {
	resource := &interfaces.Resource{SchemaDefinition: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_Integer}, {Name: "payload", Type: interfaces.DataType_Json}, {Name: "text", Type: interfaces.DataType_String, Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Vector}}}}}
	for _, name := range []string{"missing", "payload", "payload.id"} {
		for _, params := range []*interfaces.ResourceDataQueryParams{{Sort: []*interfaces.SortField{{Field: name}}}, {GroupBy: []*interfaces.GroupByItem{{Property: name}}}, {Aggregation: &interfaces.Aggregation{Property: name, Aggr: "count"}}} {
			err := validateQueryFields(resource, params)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "not found")
		}
	}
	for _, name := range []string{"id", "text_vector", "_score"} {
		require.NoError(t, validateQueryFields(resource, &interfaces.ResourceDataQueryParams{Sort: []*interfaces.SortField{{Field: name}}}))
	}
}

func TestResourceForQueryIncludesGeneratedFields(t *testing.T) {
	resource := &interfaces.Resource{SchemaDefinition: []*interfaces.Property{{Name: "title", OriginalName: "source_title", Type: interfaces.DataType_String, Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Vector}}}, {Name: "payload", Type: interfaces.DataType_Json}}}
	got, err := resourceForQuery(resource)
	require.NoError(t, err)
	fields := map[string]*interfaces.Property{}
	for _, prop := range got.SchemaDefinition {
		fields[prop.Name] = prop
	}
	require.Contains(t, fields, "title_vector")
	assert.Equal(t, interfaces.DataType_Vector, fields["title_vector"].Type)
	assert.Equal(t, "title", fields["title"].OriginalName)
	assert.NotContains(t, fields, "payload")
	assert.Equal(t, "source_title", resource.SchemaDefinition[0].OriginalName)
	assert.Len(t, resource.SchemaDefinition, 2)
}
