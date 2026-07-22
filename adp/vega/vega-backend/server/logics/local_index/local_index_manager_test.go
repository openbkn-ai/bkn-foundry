// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package local_index

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
)

func TestLocalIndexManagerDelegatesToIndexConnector(t *testing.T) {
	t.Run("local index manager delegates to index connector", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		ctx := context.Background()
		connector := vmock.NewMockIndexConnector(ctrl)
		manager := &localIndexManager{c: connector}
		schema := []*interfaces.Property{{Name: "id", Type: "integer"}}
		resource := &interfaces.Resource{ID: "resource-1", SchemaDefinition: schema}
		params := &interfaces.ResourceDataQueryParams{}
		docs := []map[string]any{{"id": 1}}
		queryResult := &interfaces.QueryResult{
			Rows:        []map[string]any{{"id": 1}},
			Total:       1,
			SearchAfter: []any{"sort-1"},
		}
		document := map[string]any{"id": 1}
		docIDs := []string{"doc-1"}

		connector.EXPECT().Create(ctx, "idx", schema).Return(nil)
		connector.EXPECT().Update(ctx, "idx", schema).Return(nil)
		connector.EXPECT().Delete(ctx, "idx").Return(nil)
		connector.EXPECT().CheckExist(ctx, "idx").Return(true, nil)
		connector.EXPECT().ExecuteQuery(ctx, "idx", resource, params).Return(queryResult, nil)
		connector.EXPECT().GetDocument(ctx, "idx", "doc-1").Return(document, nil)
		connector.EXPECT().CreateDocuments(ctx, "idx", docs).Return(docIDs, nil)
		connector.EXPECT().UpsertDocuments(ctx, "idx", docs).Return(docIDs, nil)
		connector.EXPECT().DeleteDocument(ctx, "idx", "doc-1").Return(nil)
		connector.EXPECT().DeleteDocuments(ctx, "idx", "doc-1,doc-2").Return(nil)

		require.NoError(t, manager.CreateIndex(ctx, "idx", schema))
		require.NoError(t, manager.UpdateIndex(ctx, "idx", schema))
		require.NoError(t, manager.DeleteIndex(ctx, "idx"))

		exists, err := manager.CheckExist(ctx, "idx")
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

		created, err := manager.CreateDocuments(ctx, "idx", docs)
		require.NoError(t, err)
		assert.Equal(t, []string{"doc-1"}, created)

		upserted, err := manager.UpsertDocuments(ctx, "idx", docs)
		require.NoError(t, err)
		assert.Equal(t, []string{"doc-1"}, upserted)

		require.NoError(t, manager.DeleteDocument(ctx, "idx", "doc-1"))
		require.NoError(t, manager.DeleteDocuments(ctx, "idx", "doc-1,doc-2"))
	})
}

func TestLocalIndexManagerDeleteDocumentsByQueryBuildsActualFilter(t *testing.T) {
	t.Run("local index manager delete documents by query builds actual filter", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		ctx := context.Background()
		connector := vmock.NewMockIndexConnector(ctrl)
		manager := &localIndexManager{c: connector}
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
			DeleteDocumentsByQuery(ctx, "idx", params, resource.SchemaDefinition).
			DoAndReturn(func(_ context.Context, _ string, p *interfaces.ResourceDataQueryParams, schema []*interfaces.Property) error {
				gotParams = p
				gotSchema = schema
				return nil
			})

		require.NoError(t, manager.DeleteDocumentsByQuery(ctx, "idx", resource, params))
		require.NotNil(t, params.ActualFilterCond)
		assert.Equal(t, "==", params.ActualFilterCond.GetOperation())
		assert.Same(t, params, gotParams)
		assert.Equal(t, resource.SchemaDefinition, gotSchema)
	})
}
