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

	"vega-backend/interfaces"
	"vega-backend/logics/connectors"
)

func TestLocalIndexManagerDelegatesToIndexConnector(t *testing.T) {
	ctx := context.Background()
	connector := &fakeIndexConnector{
		queryResult: &interfaces.QueryResult{
			Rows:  []map[string]any{{"id": 1}},
			Total: 1,
		},
		document: map[string]any{"id": 1},
		exists:   true,
		docIDs:   []string{"doc-1"},
	}
	manager := &localIndexManager{c: connector}
	schema := []*interfaces.Property{{Name: "id", Type: "integer"}}
	resource := &interfaces.Resource{ID: "resource-1", SchemaDefinition: schema}
	params := &interfaces.ResourceDataQueryParams{}
	docs := []map[string]any{{"id": 1}}

	require.NoError(t, manager.CreateIndex(ctx, "idx", schema))
	assert.Equal(t, "idx", connector.createdName)
	require.NoError(t, manager.UpdateIndex(ctx, "idx", schema))
	assert.Equal(t, "idx", connector.updatedName)
	require.NoError(t, manager.DeleteIndex(ctx, "idx"))
	assert.Equal(t, "idx", connector.deletedName)

	exists, err := manager.CheckExist(ctx, "idx")
	require.NoError(t, err)
	assert.True(t, exists)

	rows, total, err := manager.ListDocuments(ctx, "idx", resource, params)
	require.NoError(t, err)
	assert.Equal(t, []map[string]any{{"id": 1}}, rows)
	assert.Equal(t, int64(1), total)

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
	assert.Equal(t, "doc-1", connector.deletedDocID)
	require.NoError(t, manager.DeleteDocuments(ctx, "idx", "doc-1,doc-2"))
	assert.Equal(t, "doc-1,doc-2", connector.deletedDocIDs)
}

func TestLocalIndexManagerDeleteDocumentsByQueryBuildsActualFilter(t *testing.T) {
	ctx := context.Background()
	connector := &fakeIndexConnector{}
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

	require.NoError(t, manager.DeleteDocumentsByQuery(ctx, "idx", resource, params))
	require.NotNil(t, params.ActualFilterCond)
	assert.Equal(t, "==", params.ActualFilterCond.GetOperation())
	assert.Same(t, params, connector.deleteByQueryParams)
	assert.Equal(t, resource.SchemaDefinition, connector.deleteByQuerySchema)
}

var _ connectors.IndexConnector = (*fakeIndexConnector)(nil)

type fakeIndexConnector struct {
	queryResult         *interfaces.QueryResult
	document            map[string]any
	exists              bool
	docIDs              []string
	createdName         string
	updatedName         string
	deletedName         string
	deletedDocID        string
	deletedDocIDs       string
	deleteByQueryParams *interfaces.ResourceDataQueryParams
	deleteByQuerySchema []*interfaces.Property
}

func (f *fakeIndexConnector) GetType() string { return "fake" }
func (f *fakeIndexConnector) GetName() string { return "fake" }
func (f *fakeIndexConnector) GetMode() string { return interfaces.ConnectorModeLocal }
func (f *fakeIndexConnector) GetCategory() string {
	return interfaces.ConnectorCategoryIndex
}
func (f *fakeIndexConnector) GetEnabled() bool { return true }
func (f *fakeIndexConnector) SetEnabled(bool)  {}
func (f *fakeIndexConnector) GetSensitiveFields() []string {
	return nil
}
func (f *fakeIndexConnector) GetFieldConfig() map[string]interfaces.ConnectorFieldConfig {
	return nil
}
func (f *fakeIndexConnector) New(interfaces.ConnectorConfig) (connectors.Connector, error) {
	return f, nil
}
func (f *fakeIndexConnector) Connect(context.Context) error        { return nil }
func (f *fakeIndexConnector) Ping(context.Context) error           { return nil }
func (f *fakeIndexConnector) Close(context.Context) error          { return nil }
func (f *fakeIndexConnector) TestConnection(context.Context) error { return nil }
func (f *fakeIndexConnector) GetMetadata(context.Context) (map[string]any, error) {
	return nil, nil
}
func (f *fakeIndexConnector) MapType(nativeType string) string { return nativeType }
func (f *fakeIndexConnector) ListIndexes(context.Context) ([]*interfaces.IndexMeta, error) {
	return nil, nil
}
func (f *fakeIndexConnector) GetIndexMeta(context.Context, *interfaces.IndexMeta) error {
	return nil
}
func (f *fakeIndexConnector) ExecuteQuery(context.Context, string, *interfaces.Resource, *interfaces.ResourceDataQueryParams) (*interfaces.QueryResult, error) {
	return f.queryResult, nil
}
func (f *fakeIndexConnector) ExecuteQueryWithDsl(context.Context, string, string) (*interfaces.QueryResult, error) {
	return nil, nil
}
func (f *fakeIndexConnector) ExecuteRawQuery(context.Context, string, map[string]any) (*interfaces.RawQueryResponse, error) {
	return nil, nil
}
func (f *fakeIndexConnector) Create(_ context.Context, name string, _ []*interfaces.Property) error {
	f.createdName = name
	return nil
}
func (f *fakeIndexConnector) Update(_ context.Context, name string, _ []*interfaces.Property) error {
	f.updatedName = name
	return nil
}
func (f *fakeIndexConnector) Delete(_ context.Context, name string) error {
	f.deletedName = name
	return nil
}
func (f *fakeIndexConnector) CheckExist(context.Context, string) (bool, error) {
	return f.exists, nil
}
func (f *fakeIndexConnector) CreateDocuments(context.Context, string, []map[string]any) ([]string, error) {
	return f.docIDs, nil
}
func (f *fakeIndexConnector) GetDocument(context.Context, string, string) (map[string]any, error) {
	return f.document, nil
}
func (f *fakeIndexConnector) DeleteDocument(_ context.Context, _ string, docID string) error {
	f.deletedDocID = docID
	return nil
}
func (f *fakeIndexConnector) UpsertDocuments(context.Context, string, []map[string]any) ([]string, error) {
	return f.docIDs, nil
}
func (f *fakeIndexConnector) DeleteDocuments(_ context.Context, _ string, docIDs string) error {
	f.deletedDocIDs = docIDs
	return nil
}
func (f *fakeIndexConnector) DeleteDocumentsByQuery(_ context.Context, _ string, params *interfaces.ResourceDataQueryParams, schemaDefinition []*interfaces.Property) error {
	f.deleteByQueryParams = params
	f.deleteByQuerySchema = schemaDefinition
	return nil
}
