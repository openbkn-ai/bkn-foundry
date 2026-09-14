// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package local_index

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
)

func TestLocalIndexManagerGetIndexCapabilities(t *testing.T) {
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
	connector.EXPECT().ValidateAnalyzer(gomock.Any(), "hanlp_index").Return(false, nil)

	available, err := manager.ValidateAnalyzer(ctx, "ik_max_word")
	require.NoError(t, err)
	assert.True(t, available)
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
		queryResult := &interfaces.QueryResult{
			Entries:     []map[string]any{{"id": 1}},
			Total:       1,
			SearchAfter: []any{"sort-1"},
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
		connector.EXPECT().ExecuteQuery(ctx, "idx", resourceForQuery(resource), params).Return(queryResult, nil)
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

		rows, total, err := manager.ListDocuments(ctx, "idx", resource, params)
		require.NoError(t, err)
		assert.Equal(t, []map[string]any{{"id": 1}}, rows)
		assert.Equal(t, int64(1), total)
		assert.Equal(t, []any{"sort-1"}, params.SearchAfter)

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
			SchemaDefinition: []*interfaces.Property{{Name: "id", Type: "integer"}},
		}
		params := &interfaces.ResourceDataQueryParams{
			FilterCondCfg: &interfaces.FilterCondCfg{
				Name:      "id",
				Operation: "==",
				ValueOptCfg: interfaces.ValueOptCfg{
					ValueFrom: interfaces.ValueFrom_Const,
					Value:     1,
				},
			},
		}
		var gotParams *interfaces.ResourceDataQueryParams
		var gotSchema []*interfaces.Property
		connector.EXPECT().
			DeleteDocumentsByQuery(ctx, "idx", params, SchemaForQuery(resource.SchemaDefinition)).
			DoAndReturn(func(_ context.Context, _ string, p *interfaces.ResourceDataQueryParams, schema []*interfaces.Property) error {
				gotParams = p
				gotSchema = schema
				return nil
			})

		require.NoError(t, manager.DeleteDocumentsByQuery(ctx, "idx", resource, params))
		assert.Nil(t, params.ActualFilterCond)
		assert.Same(t, params, gotParams)
		assert.Equal(t, SchemaForQuery(resource.SchemaDefinition), gotSchema)
	})
}

func TestSchemaForQueryUsesManagedFieldNames(t *testing.T) {
	schema := []*interfaces.Property{{
		Name:         "body",
		OriginalName: "source_body",
		Type:         interfaces.DataType_Text,
	}}

	got := SchemaForQuery(schema)

	require.Len(t, got, 1)
	assert.Equal(t, "body", got[0].OriginalName)
	assert.Equal(t, "source_body", schema[0].OriginalName)
}
