// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package binding

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	qm "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/querymodel"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/query/compiler"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/query/planning"
)

type resourceResolverFunc func(context.Context, string, qm.ExecutionIdentity) (*interfaces.Resource, error)
type queryAuthorizerFunc func(context.Context, string, qm.ExecutionIdentity, string) (qm.AuthorizationBasis, error)
type bindingSourceResolverFunc func(context.Context, qm.Scan, qm.ExecutionIdentity) (qm.Scan, error)

func (f resourceResolverFunc) ResolveSQLQueryResource(ctx context.Context, id string, identity qm.ExecutionIdentity) (*interfaces.Resource, string, error) {
	resource, err := f(ctx, id, identity)
	return resource, "", err
}

func (f queryAuthorizerFunc) AuthorizeSQLQueryResource(ctx context.Context, id string, identity qm.ExecutionIdentity, operation string) (qm.AuthorizationBasis, error) {
	return f(ctx, id, identity, operation)
}

func bindingResource(id string) *interfaces.Resource {
	return &interfaces.Resource{ID: id, CatalogID: "catalog", Category: interfaces.ResourceCategoryTable, Enabled: true, Status: interfaces.ResourceStatusActive, UpdateTime: 10,
		SchemaDefinition: []*interfaces.Property{
			{Name: "id", Type: interfaces.DataType_Integer, OriginalType: "BIGINT"},
			{Name: "name", Type: interfaces.DataType_String, OriginalType: "VARCHAR(50)"},
			{Name: "amount", Type: interfaces.DataType_Decimal, OriginalType: "DECIMAL(12,2)"},
			{Name: "flag", Type: interfaces.DataType_Boolean, OriginalType: "BOOLEAN"},
		}}
}
func bindingInput(t *testing.T, sql string) *qm.SelectBuilder {
	t.Helper()
	tree, err := compiler.ParseSQL(sql)
	require.NoError(t, err)
	query, err := compiler.ConvertToIR(tree)
	require.NoError(t, err)
	return query
}
func bindSQL(t *testing.T, sql string) (*qm.SelectBuilder, error) {
	t.Helper()
	semantic, err := NewSemanticContext("UTC")
	require.NoError(t, err)
	resolver := resourceResolverFunc(func(_ context.Context, id string, _ qm.ExecutionIdentity) (*interfaces.Resource, error) {
		return bindingResource(id), nil
	})
	authorizer := queryAuthorizerFunc(func(_ context.Context, id string, _ qm.ExecutionIdentity, op string) (qm.AuthorizationBasis, error) {
		return qm.AuthorizationBasis{ResourceID: id, Operation: op, Revision: "auth-v1"}, nil
	})
	return Bind(context.Background(), bindingInput(t, sql), semantic, qm.ExecutionIdentity{SubjectID: "caller"}, resolver, authorizer)
}

func TestBind(t *testing.T) {
	t.Run("authorization precedes metadata and self join keeps independent fields", func(t *testing.T) {
		semantic, err := NewSemanticContext("UTC")
		require.NoError(t, err)
		resource := bindingResource("orders")
		var events []string
		identity := qm.ExecutionIdentity{SubjectID: "caller"}
		resolver := resourceResolverFunc(func(_ context.Context, id string, got qm.ExecutionIdentity) (*interfaces.Resource, error) {
			assert.Equal(t, identity, got)
			events = append(events, "resolve:"+id)
			return resource, nil
		})
		authorizer := queryAuthorizerFunc(func(_ context.Context, id string, got qm.ExecutionIdentity, op string) (qm.AuthorizationBasis, error) {
			assert.Equal(t, identity, got)
			assert.Equal(t, interfaces.OPERATION_TYPE_QUERY_DATA, op)
			events = append(events, "authorize:"+id)
			return qm.AuthorizationBasis{ResourceID: id, Operation: op, Revision: "v1"}, nil
		})
		input := bindingInput(t, "SELECT a.id AS left_id, b.id AS right_id FROM {{orders}} a LEFT JOIN {{orders}} b ON a.id=b.id")
		rawScan := input.FromPart.(*qm.Join).Left.(*qm.Scan)
		rawScan.Instance = 999
		rawScan.Identifier = qm.PhysicalIdentifier{"untrusted"}
		rawScan.Fields = []qm.SourceField{{ID: 999, Identifier: qm.PhysicalIdentifier{"untrusted"}}}
		query, err := Bind(context.Background(), input, semantic, identity, resolver, authorizer)
		require.NoError(t, err)
		assert.Equal(t, []string{"authorize:orders", "resolve:orders"}, events)
		assert.Equal(t, qm.PhysicalIdentifier{"untrusted"}, rawScan.Identifier)
		assert.Equal(t, qm.RelationInstanceID(999), rawScan.Instance)
		project := query
		join := project.FromPart.(*qm.Join)
		left, right := join.Left.(*qm.Scan), join.Right.(*qm.Scan)
		assert.NotEqual(t, left.Instance, right.Instance)
		assert.Empty(t, left.Identifier)
		assert.Empty(t, left.Fields[0].Identifier)
		assert.NotEqual(t, qm.RelationInstanceID(999), left.Instance)
		assert.NotEqual(t, left.Fields[0].ID, right.Fields[0].ID)
		assert.Equal(t, left.Fields[0].ID, project.ColumnsPart[0].Expr.Field)
		assert.Equal(t, right.Fields[0].ID, project.ColumnsPart[1].Expr.Field)
		assert.True(t, query.Schema()[1].Nullable)
		assert.Equal(t, []string{"left_id", "right_id"}, []string{query.Schema()[0].Name, query.Schema()[1].Name})

		resource.SchemaDefinition[0].Name = "changed"
		assert.Equal(t, "id", left.Fields[0].LogicalName)
		parts := input.Clone()
		parts.ColumnsPart[0].Alias = "changed"
		assert.Equal(t, "left_id", query.Schema()[0].Name)
		assert.Equal(t, qm.OriginSQL, query.Schema()[0].Origin.Kind)
	})
	t.Run("denial never reads metadata", func(t *testing.T) {
		semantic, err := NewSemanticContext("UTC")
		require.NoError(t, err)
		denied := errors.New("permission refused")
		calls := 0
		resolver := resourceResolverFunc(func(context.Context, string, qm.ExecutionIdentity) (*interfaces.Resource, error) {
			calls++
			return nil, errors.New("must not read")
		})
		authorizer := queryAuthorizerFunc(func(context.Context, string, qm.ExecutionIdentity, string) (qm.AuthorizationBasis, error) {
			return qm.AuthorizationBasis{}, denied
		})
		query, err := Bind(context.Background(), bindingInput(t, "SELECT id FROM {{orders}}"), semantic, qm.ExecutionIdentity{}, resolver, authorizer)
		require.ErrorIs(t, err, denied)
		assert.Nil(t, query)
		assert.Zero(t, calls)
	})
	t.Run("wildcards preserve schema order and projections have unique slots", func(t *testing.T) {
		query, err := bindSQL(t, "SELECT a.*, a.id AS again FROM {{orders}} a")
		require.NoError(t, err)
		require.Len(t, query.Schema(), 5)
		assert.Equal(t, "id", query.Schema()[0].Name)
		assert.Equal(t, "flag", query.Schema()[3].Name)
		project := query
		assert.Equal(t, project.ColumnsPart[0].Expr.Field, project.ColumnsPart[4].Expr.Field)
		assert.NotEqual(t, project.ColumnsPart[0].Output, project.ColumnsPart[4].Output)
	})
	t.Run("grouping having and ordering share aggregate slots", func(t *testing.T) {
		query, err := bindSQL(t, "SELECT id, MIN(amount) AS m, COUNT(*) AS n FROM {{orders}} GROUP BY id HAVING MIN(amount)>1 ORDER BY m DESC")
		require.NoError(t, err)
		parts := query
		require.Len(t, parts.ColumnsPart, 3)
		assert.Equal(t, qm.FunctionID("COUNT_STAR"), parts.ColumnsPart[2].Expr.Function)
		assert.False(t, parts.ColumnsPart[2].Expr.Nullable)
		assert.Equal(t, qm.TypeInteger, parts.ColumnsPart[2].Expr.Type.Kind)
		predicate := parts.HavingPart[0].Args[0]
		if predicate.Kind == qm.ExprCast {
			predicate = predicate.Args[0]
		}
		assert.True(t, sameBoundExpr(parts.ColumnsPart[1].Expr, predicate))
		assert.True(t, sameBoundExpr(parts.ColumnsPart[1].Expr, parts.OrderByPart[0].Expr))
		assert.Equal(t, qm.NullsLast, parts.OrderByPart[0].Nulls)
	})
	t.Run("group expressions and derived scopes", func(t *testing.T) {
		query, err := bindSQL(t, "SELECT q.n FROM (SELECT id+1 AS n FROM {{orders}} GROUP BY id+1) q ORDER BY q.n")
		require.NoError(t, err)
		parts := query
		subquery := parts.FromPart.(*qm.Subquery)
		inner := subquery.Query
		assert.Equal(t, subquery.Columns[0].Output, parts.ColumnsPart[0].Expr.Field)
		assert.True(t, sameBoundExpr(inner.GroupByPart[0], inner.ColumnsPart[0].Expr))
		assert.NotEqual(t, inner.ColumnsPart[0].Output, subquery.Columns[0].Output)
	})
	t.Run("grouping resolves field identity and rejects original ambiguity", func(t *testing.T) {
		_, err := bindSQL(t, "SELECT id+1 AS n FROM {{orders}} a GROUP BY a.id+1 ORDER BY id+1")
		require.NoError(t, err)
		_, err = bindSQL(t, "SELECT id FROM {{orders}} GROUP BY id,id")
		require.NoError(t, err)
		query, err := bindSQL(t, "SELECT id FROM {{orders}} a JOIN {{customers}} b ON a.id=b.id GROUP BY a.id")
		require.Error(t, err)
		assert.Nil(t, query)
	})
	t.Run("hidden ordering stays inside logical limit", func(t *testing.T) {
		query, err := bindSQL(t, "SELECT name FROM {{orders}} ORDER BY id LIMIT 10")
		require.NoError(t, err)
		parts := query
		require.Len(t, parts.OrderByPart, 1)
		require.Len(t, query.Schema(), 1)
		assert.NotEqual(t, parts.ColumnsPart[0].Expr.Field, parts.OrderByPart[0].Expr.Field)
		assert.Equal(t, int64(10), parts.LimitPart.Count)
		assert.Equal(t, "name", query.Schema()[0].Name)
	})
	t.Run("sort output alias shadows input and allows positions", func(t *testing.T) {
		query, err := bindSQL(t, "SELECT name AS id, amount AS a FROM {{orders}} ORDER BY id, 2 DESC")
		require.NoError(t, err)
		sort := query
		assert.Equal(t, query.ColumnsPart[0].Expr.Field, sort.OrderByPart[0].Expr.Field)
		assert.Equal(t, qm.TypeString, sort.OrderByPart[0].Expr.Type.Kind)
		assert.Equal(t, query.ColumnsPart[1].Expr.Field, sort.OrderByPart[1].Expr.Field)
		query, err = bindSQL(t, "SELECT MIN(amount) AS id FROM {{orders}} GROUP BY id ORDER BY id")
		require.NoError(t, err)
		assert.True(t, sameBoundExpr(query.ColumnsPart[0].Expr, query.OrderByPart[0].Expr))
	})
	t.Run("distinct sort can use projected expressions", func(t *testing.T) {
		_, err := bindSQL(t, "SELECT DISTINCT id AS n FROM {{orders}} ORDER BY id")
		require.NoError(t, err)
		_, err = bindSQL(t, "SELECT DISTINCT id FROM {{orders}} ORDER BY id+1")
		require.NoError(t, err)
	})
	t.Run("stable functions remain typed calls", func(t *testing.T) {
		query, err := bindSQL(t, "SELECT NOW() AS a, CURRENT_TIMESTAMP AS b FROM {{orders}}")
		require.NoError(t, err)
		columns := query.ColumnsPart
		require.Len(t, columns, 2)
		for _, column := range columns {
			assert.Equal(t, qm.ExprCall, column.Expr.Kind)
			assert.Equal(t, qm.FunctionID("CURRENT_TIMESTAMP"), column.Expr.Function)
			assert.Equal(t, qm.TypeInstant, column.Expr.Type.Kind)
		}
	})
	t.Run("strict expressions preserve exact types and NULL behavior", func(t *testing.T) {
		query, err := bindSQL(t, "SELECT -9223372036854775808 AS lo, 18446744073709551615 AS hi, 1.2300 AS d, NULL IS NULL AS z, COALESCE(NULL,1) AS c, CASE WHEN flag THEN id ELSE 0 END AS k FROM {{orders}}")
		require.NoError(t, err)
		columns := query.ColumnsPart
		assert.Equal(t, qm.IntegerValue("-9223372036854775808"), columns[0].Expr.Value.Data)
		assert.True(t, columns[1].Expr.Type.Unsigned)
		assert.Equal(t, qm.DecimalValue("1.2300"), columns[2].Expr.Value.Data)
		assert.Equal(t, 4, columns[2].Expr.Type.Scale)
		assert.False(t, columns[3].Expr.Nullable)
		assert.False(t, columns[4].Expr.Nullable)
		assert.Equal(t, qm.ExprCase, columns[5].Expr.Kind)
		assert.False(t, columns[5].Expr.CaseBase)
		assert.True(t, columns[5].Expr.CaseElse)
	})
	for _, sql := range []string{
		"SELECT id FROM {{orders}} a JOIN {{customers}} b ON a.id=b.id",
		"SELECT a.id FROM {{orders}} a JOIN {{customers}} a ON a.id=a.id",
		"SELECT *, id FROM {{orders}}", "SELECT id AS expr_2, 1 FROM {{orders}}",
		"SELECT name FROM {{orders}} GROUP BY id", "SELECT id, COUNT(*) AS n FROM {{orders}}",
		"SELECT id AS x FROM {{orders}} WHERE x>0", "SELECT id FROM {{orders}} GROUP BY id HAVING name='x'",
		"SELECT id AS x FROM {{orders}} GROUP BY x", "SELECT q.id FROM (SELECT name FROM {{orders}}) q",
		"SELECT id FROM (SELECT id FROM {{orders}} WHERE flag) q WHERE orders.flag",
		"SELECT DISTINCT name FROM {{orders}} ORDER BY id", "SELECT id FROM {{orders}} ORDER BY 0",
		"SELECT id FROM {{orders}} ORDER BY 3", "SELECT id FROM {{orders}} WHERE id",
		"SELECT name+1 AS n FROM {{orders}}", "SELECT id='1' AS bad FROM {{orders}}",
		"SELECT CASE WHEN id THEN 1 END AS bad FROM {{orders}}", "SELECT COALESCE(id,name) AS bad FROM {{orders}}",
		"SELECT CAST(id AS DOUBLE)+id AS bad FROM {{orders}}",
	} {
		t.Run("reject "+sql, func(t *testing.T) { query, err := bindSQL(t, sql); require.Error(t, err); assert.Nil(t, query) })
	}
	t.Run("malformed IR is bounded and rejected", func(t *testing.T) {
		semantic, err := NewSemanticContext("UTC")
		require.NoError(t, err)
		resolver := resourceResolverFunc(func(_ context.Context, id string, _ qm.ExecutionIdentity) (*interfaces.Resource, error) {
			return bindingResource(id), nil
		})
		authorizer := queryAuthorizerFunc(func(_ context.Context, id string, _ qm.ExecutionIdentity, op string) (qm.AuthorizationBasis, error) {
			return qm.AuthorizationBasis{ResourceID: id, Operation: op}, nil
		})
		source := &qm.Subquery{Alias: "cycle"}
		cycle := qm.Select().From(source)
		source.Query = cycle
		input := bindingInput(t, "SELECT id FROM {{orders}} WHERE flag")
		expression := input.WherePart[0]
		expression.Kind = qm.ExprUnary
		expression.Op = "NOT"
		expression.Args = []*qm.Sqlizer{expression}
		for _, q := range []*qm.SelectBuilder{nil, {}, cycle, input} {
			result, err := Bind(context.Background(), q, semantic, qm.ExecutionIdentity{}, resolver, authorizer)
			require.Error(t, err)
			assert.Nil(t, result)
		}
		for _, kind := range []qm.ExprKind{qm.ExprField} {
			query := bindingInput(t, "SELECT id FROM {{orders}}")
			query.ColumnsPart[0].Expr.Kind = kind
			result, err := Bind(context.Background(), query, semantic, qm.ExecutionIdentity{}, resolver, authorizer)
			require.Error(t, err)
			assert.Nil(t, result)
			var compileErr *qm.CompileError
			require.ErrorAs(t, err, &compileErr)
			assert.Equal(t, qm.ErrorBinding, compileErr.Kind)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err = Bind(ctx, bindingInput(t, "SELECT id FROM {{orders}}"), semantic, qm.ExecutionIdentity{}, resolver, authorizer)
		require.ErrorIs(t, err, context.Canceled)
	})
	t.Run("port failures and metadata validation preserve boundaries", func(t *testing.T) {
		semantic, err := NewSemanticContext("UTC")
		require.NoError(t, err)
		failure := errors.New("metadata unavailable")
		authorizer := queryAuthorizerFunc(func(_ context.Context, id string, _ qm.ExecutionIdentity, op string) (qm.AuthorizationBasis, error) {
			return qm.AuthorizationBasis{ResourceID: id, Operation: op}, nil
		})
		resolver := resourceResolverFunc(func(context.Context, string, qm.ExecutionIdentity) (*interfaces.Resource, error) { return nil, failure })
		query, err := Bind(context.Background(), bindingInput(t, "SELECT id FROM {{orders}}"), semantic, qm.ExecutionIdentity{}, resolver, authorizer)
		require.ErrorIs(t, err, failure)
		assert.Nil(t, query)
		for _, change := range []func(*interfaces.Resource){
			func(r *interfaces.Resource) { r.Enabled = false },
			func(r *interfaces.Resource) { r.Category = interfaces.ResourceCategoryLogicalView },
			func(r *interfaces.Resource) { r.SchemaDefinition[2].OriginalType = "DECIMAL(10,11)" },
			func(r *interfaces.Resource) { r.SchemaDefinition[1].Name = "id" },
			func(r *interfaces.Resource) { r.ID = "other" },
		} {
			resource := bindingResource("orders")
			change(resource)
			resolver = resourceResolverFunc(func(context.Context, string, qm.ExecutionIdentity) (*interfaces.Resource, error) {
				return resource, nil
			})
			query, err = Bind(context.Background(), bindingInput(t, "SELECT id FROM {{orders}}"), semantic, qm.ExecutionIdentity{}, resolver, authorizer)
			require.Error(t, err)
			assert.Nil(t, query)
		}
		calls := 0
		resolver = resourceResolverFunc(func(context.Context, string, qm.ExecutionIdentity) (*interfaces.Resource, error) {
			calls++
			return nil, failure
		})
		wrongBasis := queryAuthorizerFunc(func(context.Context, string, qm.ExecutionIdentity, string) (qm.AuthorizationBasis, error) {
			return qm.AuthorizationBasis{ResourceID: "other", Operation: interfaces.OPERATION_TYPE_QUERY_DATA}, nil
		})
		_, err = Bind(context.Background(), bindingInput(t, "SELECT id FROM {{orders}}"), semantic, qm.ExecutionIdentity{}, resolver, wrongBasis)
		require.Error(t, err)
		assert.Zero(t, calls)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		canceling := queryAuthorizerFunc(func(_ context.Context, id string, _ qm.ExecutionIdentity, op string) (qm.AuthorizationBasis, error) {
			cancel()
			return qm.AuthorizationBasis{ResourceID: id, Operation: op}, nil
		})
		_, err = Bind(ctx, bindingInput(t, "SELECT id FROM {{orders}}"), semantic, qm.ExecutionIdentity{}, resolver, canceling)
		require.ErrorIs(t, err, context.Canceled)
		assert.Zero(t, calls)
		semantic.Version = "unknown"
		_, err = Bind(context.Background(), bindingInput(t, "SELECT id FROM {{orders}}"), semantic, qm.ExecutionIdentity{}, resolver, authorizer)
		require.Error(t, err)
		var configuration *qm.CompileError
		require.ErrorAs(t, err, &configuration)
		assert.Equal(t, qm.ErrorConfiguration, configuration.Kind)
		assert.Zero(t, calls)
	})
	t.Run("hidden projection cannot cross a prior limit", func(t *testing.T) {
		semantic, err := NewSemanticContext("UTC")
		require.NoError(t, err)
		resolver := resourceResolverFunc(func(_ context.Context, id string, _ qm.ExecutionIdentity) (*interfaces.Resource, error) {
			return bindingResource(id), nil
		})
		authorizer := queryAuthorizerFunc(func(_ context.Context, id string, _ qm.ExecutionIdentity, op string) (qm.AuthorizationBasis, error) {
			return qm.AuthorizationBasis{ResourceID: id, Operation: op}, nil
		})
		input := bindingInput(t, "SELECT name FROM {{orders}} LIMIT 1")
		input = qm.Select(qm.As(qm.Field("name"), "name")).FromSelect(input, "limited").OrderBy(qm.SortKey{Expr: qm.Field("id")})
		query, err := Bind(context.Background(), input, semantic, qm.ExecutionIdentity{}, resolver, authorizer)
		require.Error(t, err)
		assert.Nil(t, query)
	})
	t.Run("integer literals do not narrow ordinary arithmetic", func(t *testing.T) {
		query, err := bindSQL(t, "SELECT 127+1 AS n, 1/2 AS half FROM {{orders}}")
		require.NoError(t, err)
		expressions := query.ColumnsPart
		assert.Equal(t, 64, expressions[0].Expr.Type.BitWidth)
		assert.Equal(t, qm.TypeDecimal, expressions[1].Expr.Type.Kind)
		assert.Equal(t, 6, expressions[1].Expr.Type.Scale)
		assert.Equal(t, 1, expressions[1].Expr.Args[0].Type.Precision)
	})
	t.Run("decimal arithmetic preserves operand precision", func(t *testing.T) {
		query, err := bindSQL(t, "SELECT 1.23*1.2 AS n FROM {{orders}}")
		require.NoError(t, err)
		expr := query.ColumnsPart[0].Expr
		assert.Equal(t, qm.LogicalType{Kind: qm.TypeDecimal, Precision: 5, Scale: 3}, expr.Type)
		assert.Equal(t, qm.LogicalType{Kind: qm.TypeDecimal, Precision: 3, Scale: 2}, expr.Args[0].Type)
		assert.Equal(t, qm.LogicalType{Kind: qm.TypeDecimal, Precision: 2, Scale: 1}, expr.Args[1].Type)
	})
	t.Run("bindings can feed the single source planner", func(t *testing.T) {
		query, err := bindSQL(t, "SELECT a.id AS aid,b.id AS bid FROM {{orders}} a JOIN {{customers}} b ON a.id=b.id")
		require.NoError(t, err)
		resolver := bindingSourceResolverFunc(func(_ context.Context, scan qm.Scan, _ qm.ExecutionIdentity) (qm.Scan, error) {
			candidate := scan
			candidate.Identifier = qm.PhysicalIdentifier{"db", scan.Resource.ID}
			candidate.Fields = append([]qm.SourceField(nil), scan.Fields...)
			for i := range candidate.Fields {
				candidate.Fields[i].Identifier = qm.PhysicalIdentifier{candidate.Fields[i].LogicalName}
			}
			return candidate, nil
		})
		plan, err := planning.Plan(context.Background(), query, qm.ExecutionIdentity{SubjectID: "caller"}, resolver)
		require.NoError(t, err)
		assert.NotSame(t, query, plan)
		join := plan.FromPart.(*qm.Join)
		assert.Equal(t, qm.PhysicalIdentifier{"db", "orders"}, join.Left.(*qm.Scan).Identifier)
		assert.Equal(t, qm.PhysicalIdentifier{"db", "customers"}, join.Right.(*qm.Scan).Identifier)
	})
}

func (f bindingSourceResolverFunc) ResolveSQLQuerySource(ctx context.Context, scan qm.Scan, identity qm.ExecutionIdentity) (qm.Scan, error) {
	return f(ctx, scan, identity)
}
