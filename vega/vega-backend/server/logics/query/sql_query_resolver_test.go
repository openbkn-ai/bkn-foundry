// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package query

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	mock_interfaces "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/mock"
	qm "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/querymodel"
)

func TestSQLQueryResolverResolveSQLQueryResource(t *testing.T) {
	t.Run("identity and authorization gate metadata", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		r, err := newSQLQueryResolver(compileTestContext(), mock_interfaces.NewMockResourceService(ctrl), mock_interfaces.NewMockCatalogService(ctrl))
		require.NoError(t, err)
		_, _, err = r.ResolveSQLQueryResource(compileTestContext(), "orders", r.identity)
		var he *rest.HTTPError
		require.ErrorAs(t, err, &he)
		assert.Equal(t, http.StatusForbidden, he.HTTPCode)
		forged := r.identity
		forged.SubjectID = "other"
		_, err = r.AuthorizeSQLQueryResource(compileTestContext(), "orders", forged, interfaces.OPERATION_TYPE_QUERY_DATA)
		require.ErrorAs(t, err, &he)
		assert.Equal(t, http.StatusForbidden, he.HTTPCode)
		ctx, cancel := context.WithCancel(compileTestContext())
		cancel()
		_, _, err = r.ResolveSQLQueryResource(ctx, "orders", r.identity)
		assert.ErrorIs(t, err, context.Canceled)
	})
	for _, tc := range []struct {
		name   string
		mutate func(*interfaces.Resource)
		code   int
	}{
		{"disabled", func(r *interfaces.Resource) { r.Enabled = false }, http.StatusConflict},
		{"stale", func(r *interfaces.Resource) { r.Status = interfaces.ResourceStatusStale }, http.StatusConflict},
		{"missing", func(r *interfaces.Resource) { r.LastDiscoverStatus = interfaces.DiscoverStatusMissing }, http.StatusConflict},
		{"empty schema", func(r *interfaces.Resource) { r.SchemaDefinition = nil }, http.StatusConflict},
		{"builtin", func(r *interfaces.Resource) { r.Builtin = true }, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			rs := mock_interfaces.NewMockResourceService(ctrl)
			cs := mock_interfaces.NewMockCatalogService(ctrl)
			resource := compileTestResource()
			tc.mutate(resource)
			rs.EXPECT().CheckResourcePermission(gomock.Any(), "orders", interfaces.OPERATION_TYPE_QUERY_DATA).Return(nil)
			rs.EXPECT().InternalGetByID(gomock.Any(), nil, "orders").Return(resource, nil)
			r, err := newSQLQueryResolver(compileTestContext(), rs, cs)
			require.NoError(t, err)
			_, err = r.AuthorizeSQLQueryResource(compileTestContext(), "orders", r.identity, interfaces.OPERATION_TYPE_QUERY_DATA)
			require.NoError(t, err)
			_, _, err = r.ResolveSQLQueryResource(compileTestContext(), "orders", r.identity)
			var he *rest.HTTPError
			require.ErrorAs(t, err, &he)
			assert.Equal(t, tc.code, he.HTTPCode)
		})
	}
	t.Run("resource error preserved and catalog not read", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := mock_interfaces.NewMockResourceService(ctrl)
		cs := mock_interfaces.NewMockCatalogService(ctrl)
		failure := errors.New("metadata unavailable")
		rs.EXPECT().CheckResourcePermission(gomock.Any(), "orders", interfaces.OPERATION_TYPE_QUERY_DATA).Return(nil)
		rs.EXPECT().InternalGetByID(gomock.Any(), nil, "orders").Return(nil, failure)
		r, err := newSQLQueryResolver(compileTestContext(), rs, cs)
		require.NoError(t, err)
		_, err = r.AuthorizeSQLQueryResource(compileTestContext(), "orders", r.identity, interfaces.OPERATION_TYPE_QUERY_DATA)
		require.NoError(t, err)
		_, _, err = r.ResolveSQLQueryResource(compileTestContext(), "orders", r.identity)
		assert.ErrorIs(t, err, failure)
	})
}

func TestSourceRelationIdentifier(t *testing.T) {
	for _, tc := range []struct {
		name      string
		connector string
		meta      map[string]any
		want      qm.PhysicalIdentifier
		invalid   bool
	}{
		{name: "mysql literal dots", connector: interfaces.ConnectorTypeMySQL, meta: map[string]any{"database": "a.b", "name": "x`y"}, want: qm.PhysicalIdentifier{"a.b", "x`y"}},
		{name: "postgres schema", connector: interfaces.ConnectorTypePostgreSQL, meta: map[string]any{"database": "db", "schema": "a.b", "name": "t"}, want: qm.PhysicalIdentifier{"a.b", "t"}},
		{name: "sqlserver database", connector: interfaces.ConnectorTypeSQLServer, meta: map[string]any{"database": "db", "schema": "s", "name": "t"}, want: qm.PhysicalIdentifier{"db", "s", "t"}},
		{name: "nul rejected", connector: interfaces.ConnectorTypeMySQL, meta: map[string]any{"database": "db", "name": "t\x00x"}, invalid: true},
		{name: "missing raw metadata", connector: interfaces.ConnectorTypeMySQL, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resource := compileTestResource()
			resource.SourceMetadata = tc.meta
			got, err := sourceRelationIdentifier(resource, tc.connector, qm.Origin{})
			if tc.invalid {
				assert.Nil(t, got)
				var ce *qm.CompileError
				require.ErrorAs(t, err, &ce)
				assert.Equal(t, qm.ErrorPlanningUnsupported, ce.Kind)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestSQLQueryResolverResolveSQLQueryTargets(t *testing.T) {
	resource := compileTestResource()
	rs, cs := expectCompileMetadata(t, resource, interfaces.ConnectorTypeMySQL)
	r, err := newSQLQueryResolver(compileTestContext(), rs, cs)
	require.NoError(t, err)
	_, err = r.AuthorizeSQLQueryResource(compileTestContext(), "orders", r.identity, interfaces.OPERATION_TYPE_QUERY_DATA)
	require.NoError(t, err)
	_, _, err = r.ResolveSQLQueryResource(compileTestContext(), "orders", r.identity)
	require.NoError(t, err)
	scan := qm.BoundScan{Resource: qm.ResourceReference{ID: "orders", CatalogID: "catalog", Version: "11"}, Instance: 1,
		Fields: []qm.BoundSourceField{{ID: 1, LogicalName: "id", NativeType: qm.NativeType{ConnectorType: interfaces.ConnectorTypeMySQL, Name: "BIGINT"}}, {ID: 2, LogicalName: "created", NativeType: qm.NativeType{ConnectorType: interfaces.ConnectorTypeMySQL, Name: "TIMESTAMP(6)"}}}}
	candidates, err := r.ResolveSQLQueryTargets(compileTestContext(), scan, r.identity, qm.ExecutionOptions{QuerySource: qm.QuerySourceSource})
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	scan.Fields[1] = scan.Fields[0]
	got, err := r.ResolveSQLQueryTargets(compileTestContext(), scan, r.identity, qm.ExecutionOptions{QuerySource: qm.QuerySourceSource})
	assert.Nil(t, got)
	var ce *qm.CompileError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, qm.ErrorInvalidTree, ce.Kind)
}
