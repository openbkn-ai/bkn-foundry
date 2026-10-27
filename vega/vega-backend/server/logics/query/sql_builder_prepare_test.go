// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package query

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	mock_interfaces "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/mock"
	qm "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/querymodel"
	mysql "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/connector/local/table/mariadb"
)

func builderTestResource() *interfaces.Resource {
	resource := compileTestResource()
	resource.SourceIdentifier = "`sales.eu`.`order.items`"
	resource.SourceMetadata = nil
	return resource
}

func TestPrepareSQLBuilder(t *testing.T) {
	t.Run("authorize before metadata and preserve self join fields", func(t *testing.T) {
		resource := builderTestResource()
		rs, cs := expectCompileMetadata(t, resource, interfaces.ConnectorTypeMySQL)
		q, err := PrepareSQLBuilder(compileTestContext(), "SELECT a.id,b.created FROM {{orders}} a LEFT JOIN {{orders}} b ON a.id=b.id WHERE a.id>2", rs, cs)
		require.NoError(t, err)
		text, args, err := mysql.ToSql(q)
		require.NoError(t, err)
		assert.Equal(t, "SELECT `a`.`id`, `b`.`created` FROM `sales.eu`.`order.items` AS `a` LEFT JOIN `sales.eu`.`order.items` AS `b` ON (`a`.`id` = `b`.`id`) WHERE (`a`.`id` > ?)", text)
		assert.Equal(t, []any{json.Number("2")}, args)
		assert.Equal(t, "id", resource.SchemaDefinition[0].Name)
		assert.Equal(t, "order.id", resource.SchemaDefinition[0].OriginalName)
		assert.Equal(t, "`sales.eu`.`order.items`", resource.SourceIdentifier)
		assert.Nil(t, resource.SourceMetadata)
	})
	t.Run("preserve subquery projection and limit", func(t *testing.T) {
		resource := builderTestResource()
		resource.SchemaDefinition[0].OriginalName = ""
		rs, cs := expectCompileMetadata(t, resource, interfaces.ConnectorTypeMySQL)
		q, err := PrepareSQLBuilder(compileTestContext(), "SELECT q.public_id FROM (SELECT id AS public_id FROM {{orders}} LIMIT 1) q", rs, cs)
		require.NoError(t, err)
		text, _, err := mysql.ToSql(q)
		require.NoError(t, err)
		assert.Equal(t, "SELECT `q`.`public_id` FROM (SELECT `id` AS `public_id` FROM `sales.eu`.`order.items` LIMIT 1) AS `q`", text)
	})
	t.Run("preserve group having and order expressions", func(t *testing.T) {
		rs, cs := expectCompileMetadata(t, builderTestResource(), interfaces.ConnectorTypeMySQL)
		q, err := PrepareSQLBuilder(compileTestContext(), "SELECT a.id AS key_id,COUNT(*) AS n FROM {{orders}} a WHERE a.created IS NOT NULL GROUP BY a.id HAVING COUNT(*)>1 ORDER BY n DESC", rs, cs)
		require.NoError(t, err)
		text, args, err := mysql.ToSql(q)
		require.NoError(t, err)
		assert.Equal(t, "SELECT `a`.`id` AS `key_id`, COUNT(*) AS `n` FROM `sales.eu`.`order.items` AS `a` WHERE (`a`.`created` IS NOT NULL) GROUP BY `a`.`id` HAVING (COUNT(*) > ?) ORDER BY `n` DESC", text)
		assert.Equal(t, []any{json.Number("1")}, args)
	})
	t.Run("source identifier takes precedence over source metadata", func(t *testing.T) {
		resource := builderTestResource()
		resource.SourceIdentifier = "`stored.db`.`table``name`"
		resource.SourceMetadata = map[string]any{"database": "different", "name": "ignored"}
		rs, cs := expectCompileMetadata(t, resource, interfaces.ConnectorTypeMySQL)
		q, err := PrepareSQLBuilder(compileTestContext(), "SELECT a.* FROM {{orders}} a", rs, cs)
		require.NoError(t, err)
		text, _, err := mysql.ToSql(q)
		require.NoError(t, err)
		assert.Equal(t, "SELECT `a`.* FROM `stored.db`.`table``name` AS `a`", text)
	})
	t.Run("denied source never reads metadata", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := mock_interfaces.NewMockResourceService(ctrl)
		cs := mock_interfaces.NewMockCatalogService(ctrl)
		denied := errors.New("denied")
		rs.EXPECT().CheckResourcePermission(gomock.Any(), "orders", interfaces.OPERATION_TYPE_QUERY_DATA).Return(denied)
		q, err := PrepareSQLBuilder(compileTestContext(), "SELECT * FROM {{orders}}", rs, cs)
		assert.Nil(t, q)
		assert.ErrorIs(t, err, denied)
	})
	t.Run("invalid SQL and missing identity read no metadata", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := mock_interfaces.NewMockResourceService(ctrl)
		cs := mock_interfaces.NewMockCatalogService(ctrl)
		q, err := PrepareSQLBuilder(compileTestContext(), "DELETE FROM {{orders}}", rs, cs)
		assert.Nil(t, q)
		assert.Error(t, err)
		q, err = PrepareSQLBuilder(context.Background(), "SELECT * FROM {{orders}}", rs, cs)
		assert.Nil(t, q)
		var he *httperrors.HTTPError
		require.ErrorAs(t, err, &he)
		assert.Equal(t, http.StatusForbidden, he.HTTPCode)
		ctx, cancel := context.WithCancel(compileTestContext())
		cancel()
		q, err = PrepareSQLBuilder(ctx, "SELECT * FROM {{orders}}", rs, cs)
		assert.Nil(t, q)
		assert.ErrorIs(t, err, context.Canceled)
	})
	t.Run("read errors preserved", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := mock_interfaces.NewMockResourceService(ctrl)
		cs := mock_interfaces.NewMockCatalogService(ctrl)
		unavailable := errors.New("unavailable")
		gomock.InOrder(
			rs.EXPECT().CheckResourcePermission(gomock.Any(), "orders", interfaces.OPERATION_TYPE_QUERY_DATA).Return(nil),
			rs.EXPECT().InternalGetByID(gomock.Any(), nil, "orders").Return(nil, unavailable),
		)
		q, err := PrepareSQLBuilder(compileTestContext(), "SELECT * FROM {{orders}}", rs, cs)
		assert.Nil(t, q)
		assert.ErrorIs(t, err, unavailable)
	})
	for _, connector := range []string{interfaces.ConnectorTypeMariaDB, interfaces.ConnectorTypePostgreSQL, interfaces.ConnectorTypeOpenSearch} {
		t.Run("unsupported connector "+connector, func(t *testing.T) {
			rs, cs := expectCompileMetadata(t, builderTestResource(), connector)
			q, err := PrepareSQLBuilder(compileTestContext(), "SELECT * FROM {{orders}}", rs, cs)
			assert.Nil(t, q)
			var be *qm.CompileError
			require.ErrorAs(t, err, &be)
			assert.Equal(t, qm.ErrorPlanningUnsupported, be.Kind)
		})
	}
	t.Run("view never treated as physical table", func(t *testing.T) {
		resource := builderTestResource()
		resource.Category = interfaces.ResourceCategoryLogicalView
		rs, cs := expectCompileMetadata(t, resource, interfaces.ConnectorTypeMySQL)
		q, err := PrepareSQLBuilder(compileTestContext(), "SELECT * FROM {{orders}}", rs, cs)
		assert.Nil(t, q)
		var be *qm.CompileError
		require.ErrorAs(t, err, &be)
		assert.Equal(t, qm.ErrorPlanningUnsupported, be.Kind)
	})
	t.Run("preserve wildcard and physical fields without schema mapping", func(t *testing.T) {
		resource := builderTestResource()
		rs, cs := expectCompileMetadata(t, resource, interfaces.ConnectorTypeMySQL)
		q, err := PrepareSQLBuilder(compileTestContext(), "SELECT a.*,a.`order.id` AS public_id FROM {{orders}} a WHERE a.`order.id`>1 ORDER BY public_id LIMIT 2", rs, cs)
		require.NoError(t, err)
		text, args, err := mysql.ToSql(q)
		require.NoError(t, err)
		assert.Equal(t, "SELECT `a`.*, `a`.`order.id` AS `public_id` FROM `sales.eu`.`order.items` AS `a` WHERE (`a`.`order.id` > ?) ORDER BY `public_id` ASC LIMIT 2", text)
		assert.Equal(t, []any{json.Number("1")}, args)
	})
	t.Run("disabled resource remains unavailable", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := mock_interfaces.NewMockResourceService(ctrl)
		cs := mock_interfaces.NewMockCatalogService(ctrl)
		resource := builderTestResource()
		resource.Enabled = false
		gomock.InOrder(
			rs.EXPECT().CheckResourcePermission(gomock.Any(), "orders", interfaces.OPERATION_TYPE_QUERY_DATA).Return(nil),
			rs.EXPECT().InternalGetByID(gomock.Any(), nil, "orders").Return(resource, nil),
		)
		q, err := PrepareSQLBuilder(compileTestContext(), "SELECT * FROM {{orders}}", rs, cs)
		assert.Nil(t, q)
		var he *httperrors.HTTPError
		require.ErrorAs(t, err, &he)
		assert.Equal(t, http.StatusConflict, he.HTTPCode)
	})
	t.Run("different catalogs rejected after authorization", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := mock_interfaces.NewMockResourceService(ctrl)
		cs := mock_interfaces.NewMockCatalogService(ctrl)
		left := builderTestResource()
		right := builderTestResource()
		right.ID = "other"
		right.CatalogID = "other_catalog"
		gomock.InOrder(
			rs.EXPECT().CheckResourcePermission(gomock.Any(), "orders", interfaces.OPERATION_TYPE_QUERY_DATA).Return(nil),
			rs.EXPECT().InternalGetByID(gomock.Any(), nil, "orders").Return(left, nil),
			cs.EXPECT().InternalGetByID(gomock.Any(), "catalog", false).Return(&interfaces.Catalog{ID: "catalog", ConnectorType: interfaces.ConnectorTypeMySQL, Enabled: true, UpdateTime: 7}, nil),
			rs.EXPECT().CheckResourcePermission(gomock.Any(), "other", interfaces.OPERATION_TYPE_QUERY_DATA).Return(nil),
			rs.EXPECT().InternalGetByID(gomock.Any(), nil, "other").Return(right, nil),
			cs.EXPECT().InternalGetByID(gomock.Any(), "other_catalog", false).Return(&interfaces.Catalog{ID: "other_catalog", ConnectorType: interfaces.ConnectorTypeMySQL, Enabled: true, UpdateTime: 8}, nil),
		)
		q, err := PrepareSQLBuilder(compileTestContext(), "SELECT a.id FROM {{orders}} a JOIN {{other}} b ON a.id=b.id", rs, cs)
		assert.Nil(t, q)
		var be *qm.CompileError
		require.ErrorAs(t, err, &be)
		assert.Equal(t, qm.ErrorPlanningUnsupported, be.Kind)
	})
}
