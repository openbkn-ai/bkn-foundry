//go:build integration

// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package query

import (
	"context"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	mock_interfaces "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/mock"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/query/querypolicy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"os/exec"
	"testing"
)

func TestRawQueryServicePrepareSQLQueryIntegration(t *testing.T) {
	require.NoError(t, exec.Command("python3", "-c", "import sqlglot").Run(), "integration tests require the production SQLGlot runtime")
	for _, tc := range []struct{ name, input, connectorType, reference string }{
		{"mysql input to postgres dotted table", "mysql", interfaces.ConnectorTypePostgreSQL, `"Sales.eu"."Orders.part"`},
		{"postgres input to mysql quoted name", "postgres", interfaces.ConnectorTypeMySQL, "`Sales`.`ord``ers`"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			rs := mock_interfaces.NewMockResourceService(ctrl)
			cs := mock_interfaces.NewMockCatalogService(ctrl)
			resource := &interfaces.Resource{ID: "r1", CatalogID: "c1", Enabled: true, Status: interfaces.ResourceStatusActive, SourceIdentifier: tc.reference, SchemaDefinition: []*interfaces.Property{{Name: "id"}}}
			expectRawQueryResources(rs, []string{"r1"}, resource)
			rs.EXPECT().InternalGetByID(gomock.Any(), nil, "r1").Return(resource, nil).Times(1)
			cs.EXPECT().InternalGetByID(gomock.Any(), "c1", true).Return(&interfaces.Catalog{ID: "c1", Enabled: true, ConnectorType: tc.connectorType}, nil)
			previous := rawQueryPolicy
			rawQueryPolicy = querypolicy.NewSQLGlotAdapter()
			t.Cleanup(func() { rawQueryPolicy = previous })
			got, err := (&rawQueryService{cs: cs, rs: rs}).prepareSQLQuery(context.Background(), &interfaces.RawQueryRequest{
				Query: "SELECT o.id FROM {{r1}} AS o WHERE o.id = '{{r1}}' /* {{r1}} */", QueryFormat: interfaces.QueryFormatSQL, InputDialect: tc.input,
			})
			require.NoError(t, err)
			assert.Contains(t, got.sql, tc.reference+" AS o")
			assert.Contains(t, got.sql, "'{{r1}}'")
			assert.Contains(t, got.sql, "/* {{r1}} */")
			assert.NotContains(t, got.sql, "__vega_resource_")
		})
	}

}
