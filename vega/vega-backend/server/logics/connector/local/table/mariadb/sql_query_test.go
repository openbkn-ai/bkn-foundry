// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mariadb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	qm "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/querymodel"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/query/binding"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/query/compiler"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/query/planning"
)

type sqlTestResolver struct{ resource *interfaces.Resource }

func (r sqlTestResolver) ResolveSQLQueryResource(context.Context, string, qm.ExecutionIdentity) (*interfaces.Resource, string, error) {
	return r.resource, interfaces.ConnectorTypeMariaDB, nil
}
func (r sqlTestResolver) AuthorizeSQLQueryResource(_ context.Context, id string, identity qm.ExecutionIdentity, op string) (qm.AuthorizationBasis, error) {
	return qm.AuthorizationBasis{ResourceID: id, Operation: op, Namespace: identity.AuthorizationNamespace}, nil
}
func (r sqlTestResolver) ResolveSQLQueryTargets(_ context.Context, scan qm.BoundScan, identity qm.ExecutionIdentity, _ qm.ExecutionOptions) ([]qm.TargetBinding, error) {
	// 与真实资源解析器相同的非敏感身份引用。
	encoded, _ := json.Marshal(identity)
	sum := sha256.Sum256(encoded)
	target := qm.TargetBinding{Source: qm.QuerySourceSource, Target: qm.ExecutionTarget{CatalogID: "cat", ConnectorType: interfaces.ConnectorTypeMariaDB, PhysicalTargetKey: "catalog:cat", ConnectionProfile: "catalog:cat@7", EffectiveSourceIdentity: "catalog:cat@7:" + hex.EncodeToString(sum[:]), AuthorizationNamespace: identity.AuthorizationNamespace, SchemaRevision: "7", ConfigRevision: "7"}, Relations: []qm.RelationBinding{{Instance: scan.Instance, ResourceID: scan.Resource.ID, Identifier: qm.PhysicalIdentifier{"sales.eu", "orders`all"}}}}
	for _, field := range scan.Fields {
		target.Fields = append(target.Fields, qm.FieldBinding{Address: qm.FieldAddress{Relation: scan.Instance, Field: field.ID}, Identifier: qm.PhysicalIdentifier{field.LogicalName}, NativeType: field.NativeType, MissingValue: qm.MissingAsNull})
	}
	return []qm.TargetBinding{target}, nil
}
func sqlTestPlan(t *testing.T, sql string) (qm.SQLPrepareRequest, qm.SQLQueryExecutionContext) {
	t.Helper()
	tree, err := compiler.ParseSQL(sql)
	require.NoError(t, err)
	input, err := compiler.ConvertToIR(tree)
	require.NoError(t, err)
	semantics, err := binding.NewSemanticContext("UTC")
	require.NoError(t, err)
	identity := qm.ExecutionIdentity{SubjectID: "caller", SubjectType: "user", AuthorizationNamespace: "resource"}
	resolver := sqlTestResolver{resource: &interfaces.Resource{ID: "orders", CatalogID: "cat", Category: interfaces.ResourceCategoryTable, Enabled: true, Status: interfaces.ResourceStatusActive, UpdateTime: 11, SchemaDefinition: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_Integer, OriginalType: "BIGINT"}, {Name: "name", Type: interfaces.DataType_String, OriginalType: "VARCHAR(100)"}}}}
	bound, err := binding.Bind(context.Background(), input, semantics, identity, resolver, resolver)
	require.NoError(t, err)
	plan, err := planning.Plan(context.Background(), bound, identity, qm.ExecutionOptions{QuerySource: qm.QuerySourceSource}, resolver)
	require.NoError(t, err)
	return qm.SQLPrepareRequest{Query: plan.Query, TargetBinding: plan.TargetBinding}, qm.SQLQueryExecutionContext{QueryID: "query-1", Identity: identity}
}
func TestMariaDBConnectorPrepare(t *testing.T) {
	t.Run("opaque template contains no literal SQL input", func(t *testing.T) {
		req, _ := sqlTestPlan(t, "SELECT a.id FROM {{ orders }} a LEFT JOIN {{ orders }} b ON a.id=b.id WHERE a.name='x'' OR 1=1 --' ORDER BY b.id DESC LIMIT 3 OFFSET 2")
		c := &MariaDBConnector{}
		prepared, err := c.Prepare(context.Background(), req)
		require.NoError(t, err)
		p := prepared.(*sqlPrepared)
		assert.Contains(t, p.relation.text, "`sales.eu`.`orders``all`")
		assert.Contains(t, p.relation.text, "LEFT JOIN")
		assert.Contains(t, p.relation.text, "CAST(")
		assert.NotContains(t, p.relation.text, "OR 1=1 --")
		require.Len(t, p.relation.args, 3)
		assert.Equal(t, "x' OR 1=1 --", p.relation.args[0])
		assert.Equal(t, int64(3), p.relation.args[1])
		assert.Equal(t, int64(2), p.relation.args[2])
		assert.False(t, c.connected)
		metadata := p.Metadata()
		metadata.OutputSchema[0].Name = "changed"
		metadata.Dependencies[len(metadata.Dependencies)-1].Authorization[0].ResourceID = "changed"
		assert.NotEqual(t, "changed", p.Metadata().OutputSchema[0].Name)
		assert.NotEqual(t, "changed", p.Metadata().Dependencies[len(metadata.Dependencies)-1].Authorization[0].ResourceID)
		req.Query.OutputSchema[0].Name = "also changed"
		assert.NotEqual(t, "also changed", p.Metadata().OutputSchema[0].Name)
	})
	for _, sql := range []string{"SELECT SUM(id) FROM {{ orders }}", "SELECT id+1 FROM {{ orders }}", "SELECT DISTINCT name FROM {{ orders }}", "SELECT NOW() FROM {{ orders }}"} {
		t.Run(sql, func(t *testing.T) {
			req, _ := sqlTestPlan(t, sql)
			c := &MariaDBConnector{}
			p, err := c.Prepare(context.Background(), req)
			assert.Nil(t, p)
			var unsupported *interfaces.UnsupportedOperationError
			require.ErrorAs(t, err, &unsupported)
			assert.False(t, c.connected)
		})
	}
	t.Run("fingerprint mismatch", func(t *testing.T) {
		req, _ := sqlTestPlan(t, "SELECT id FROM {{ orders }}")
		req.TargetBinding.Fields[0].Identifier[0] = "changed"
		p, err := (&MariaDBConnector{}).Prepare(context.Background(), req)
		assert.Nil(t, p)
		assert.Error(t, err)
	})
}
func TestMariaDBConnectorExecute(t *testing.T) {
	t.Run("window and count retain logical query boundary", func(t *testing.T) {
		req, execution := sqlTestPlan(t, "SELECT id FROM {{ orders }} ORDER BY id DESC LIMIT 3 OFFSET 2")
		db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })
		c := &MariaDBConnector{db: db, connected: true}
		prepared, err := c.Prepare(context.Background(), req)
		require.NoError(t, err)
		p := prepared.(*sqlPrepared)
		read := qm.SQLReadRequest{Kind: qm.SQLReadPage, Offset: 1, Limit: new(int64(1))}
		text, _, _, err := p.readSQL(read)
		require.NoError(t, err)
		// 最外层显式重新排序，逻辑 LIMIT 与 API 读取窗口分别占位。
		assert.Contains(t, text, ") AS q ORDER BY")
		assert.Equal(t, 2, strings.Count(text, " LIMIT ? OFFSET ?"))
		mock.ExpectQuery(text).WithArgs(int64(3), int64(2), int64(2), int64(1)).WillReturnRows(sqlmock.NewRows([]string{"f"}).AddRow(8).AddRow(7)).RowsWillBeClosed()
		stream, err := c.Execute(context.Background(), prepared, execution, read)
		require.NoError(t, err)
		batch, err := stream.Next(context.Background())
		require.NoError(t, err)
		require.Len(t, batch.Rows, 1)
		assert.Equal(t, qm.IntegerValue("8"), batch.Rows[0][0].Value.Data)
		_, err = stream.Next(context.Background())
		assert.ErrorIs(t, err, io.EOF)
		assert.Equal(t, qm.CompletionWindowEnd, stream.Completion().Kind)
		require.NotNil(t, stream.Completion().NextOffset)
		assert.Equal(t, int64(2), *stream.Completion().NextOffset)
		require.NoError(t, stream.Close())
		count := qm.SQLReadRequest{Kind: qm.SQLReadCount}
		text, _, _, err = p.readSQL(count)
		require.NoError(t, err)
		mock.ExpectQuery(text).WithArgs(int64(3), int64(2)).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(3)).RowsWillBeClosed()
		stream, err = c.Execute(context.Background(), prepared, execution, count)
		require.NoError(t, err)
		batch, err = stream.Next(context.Background())
		require.NoError(t, err)
		assert.Equal(t, qm.IntegerValue("3"), batch.Rows[0][0].Value.Data)
		assert.Equal(t, qm.CompletionRelationEnd, stream.Completion().Kind)
		require.NoError(t, stream.Close())
		require.NoError(t, mock.ExpectationsWereMet())
	})
	t.Run("invalid reads and identity perform no source request", func(t *testing.T) {
		req, execution := sqlTestPlan(t, "SELECT id FROM {{ orders }}")
		c := &MariaDBConnector{}
		prepared, err := c.Prepare(context.Background(), req)
		require.NoError(t, err)
		for _, read := range []qm.SQLReadRequest{{Kind: qm.SQLReadPage, Offset: -1}, {Kind: qm.SQLReadCount, Limit: new(int64(1))}, {Kind: "invalid"}} {
			stream, err := c.Execute(context.Background(), prepared, execution, read)
			assert.Nil(t, stream)
			assert.Error(t, err)
		}
		other := execution
		other.Identity.SubjectID = "other"
		stream, err := c.Execute(context.Background(), prepared, other, qm.SQLReadRequest{Kind: qm.SQLReadRelation})
		assert.Nil(t, stream)
		assert.Error(t, err)
		stream, err = c.Execute(context.Background(), prepared, execution, qm.SQLReadRequest{Kind: qm.SQLReadPage, Limit: new(int64(0))})
		require.NoError(t, err)
		_, err = stream.Next(context.Background())
		assert.ErrorIs(t, err, io.EOF)
		assert.Equal(t, qm.CompletionWindowEnd, stream.Completion().Kind)
		require.NoError(t, c.Release(context.Background(), prepared))
		require.NoError(t, c.Release(context.Background(), prepared))
		stream, err = c.Execute(context.Background(), prepared, execution, qm.SQLReadRequest{Kind: qm.SQLReadRelation})
		assert.Nil(t, stream)
		assert.Error(t, err)
		assert.False(t, c.connected)
	})
	t.Run("foreign prepared rejected", func(t *testing.T) {
		req, execution := sqlTestPlan(t, "SELECT id FROM {{ orders }}")
		c := &MariaDBConnector{}
		p, err := c.Prepare(context.Background(), req)
		require.NoError(t, err)
		other := &MariaDBConnector{}
		stream, err := other.Execute(context.Background(), p, execution, qm.SQLReadRequest{Kind: qm.SQLReadRelation})
		assert.Nil(t, stream)
		assert.Error(t, err)
	})
	t.Run("driver failure is not EOF", func(t *testing.T) {
		req, execution := sqlTestPlan(t, "SELECT id FROM {{ orders }}")
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })
		c := &MariaDBConnector{db: db, connected: true}
		p, err := c.Prepare(context.Background(), req)
		require.NoError(t, err)
		failure := errors.New("source failed")
		mock.ExpectQuery("SELECT").WillReturnError(failure)
		stream, err := c.Execute(context.Background(), p, execution, qm.SQLReadRequest{Kind: qm.SQLReadRelation})
		assert.Nil(t, stream)
		assert.ErrorIs(t, err, failure)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}
