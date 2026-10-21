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
type bindingTargetResolverFunc func(context.Context, qm.BoundScan, qm.ExecutionIdentity, qm.ExecutionOptions) ([]qm.TargetBinding, error)

func (f resourceResolverFunc) ResolveSQLQueryResource(ctx context.Context, id string, identity qm.ExecutionIdentity) (*interfaces.Resource, error) {
	return f(ctx, id, identity)
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
func bindingInput(t *testing.T, sql string) *qm.UnboundQuery {
	t.Helper()
	tree, err := compiler.ParseSQL(sql)
	require.NoError(t, err)
	query, err := compiler.ConvertToIR(tree)
	require.NoError(t, err)
	return query
}
func bindSQL(t *testing.T, sql string) (*qm.BoundQuery, error) {
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
		query, err := Bind(context.Background(), input, semantic, identity, resolver, authorizer)
		require.NoError(t, err)
		assert.Equal(t, []string{"authorize:orders", "resolve:orders"}, events)
		project := query.Root.(*qm.BoundProject)
		join := project.Input.(*qm.BoundJoin)
		left, right := join.Left.(*qm.BoundScan), join.Right.(*qm.BoundScan)
		assert.NotEqual(t, left.Instance, right.Instance)
		assert.NotEqual(t, left.Fields[0].ID, right.Fields[0].ID)
		assert.Equal(t, left.Fields[0].ID, project.Columns[0].Expr.Field)
		assert.Equal(t, right.Fields[0].ID, project.Columns[1].Expr.Field)
		assert.True(t, query.OutputSchema[1].Nullable)
		assert.Equal(t, []string{"left_id", "right_id"}, []string{query.OutputSchema[0].Name, query.OutputSchema[1].Name})
		require.Len(t, query.Dependencies, 2)
		assert.Equal(t, "10", query.Dependencies[1].Version)
		assert.NotEmpty(t, query.Dependencies[1].SchemaFingerprint)
		resource.SchemaDefinition[0].Name = "changed"
		assert.Equal(t, "id", left.Fields[0].LogicalName)
		input.Root.(*qm.Project).Columns[0].Alias = "changed"
		assert.Equal(t, "left_id", query.OutputSchema[0].Name)
		assert.Equal(t, qm.OriginSQL, query.OutputSchema[0].Origin.Kind)
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
		require.Len(t, query.OutputSchema, 5)
		assert.Equal(t, "id", query.OutputSchema[0].Name)
		assert.Equal(t, "flag", query.OutputSchema[3].Name)
		project := query.Root.(*qm.BoundProject)
		assert.Equal(t, project.Columns[0].Expr.Field, project.Columns[4].Expr.Field)
		assert.NotEqual(t, project.Columns[0].Output, project.Columns[4].Output)
	})
	t.Run("grouping having and ordering share aggregate slots", func(t *testing.T) {
		query, err := bindSQL(t, "SELECT id, MIN(amount) AS m, COUNT(*) AS n FROM {{orders}} GROUP BY id HAVING MIN(amount)>1 ORDER BY m DESC")
		require.NoError(t, err)
		sort := query.Root.(*qm.BoundSort)
		project := sort.Input.(*qm.BoundProject)
		having := project.Input.(*qm.BoundFilter)
		aggregate := having.Input.(*qm.BoundAggregate)
		require.Len(t, aggregate.Calls, 2)
		assert.Equal(t, qm.FunctionID("COUNT_STAR"), aggregate.Calls[1].Expr.Function)
		assert.False(t, aggregate.Calls[1].Expr.Nullable)
		assert.Equal(t, qm.TypeInteger, aggregate.Calls[1].Expr.Type.Kind)
		assert.Equal(t, aggregate.Calls[0].Output, project.Columns[1].Expr.Field)
		predicate := having.Predicate.Args[0]
		if predicate.Kind == qm.ExprCast {
			predicate = predicate.Args[0]
		}
		assert.Equal(t, aggregate.Calls[0].Output, predicate.Field)
		assert.Equal(t, project.Columns[1].Output, sort.Keys[0].Expr.Field)
		assert.Equal(t, qm.NullsLast, sort.Keys[0].Nulls)
	})
	t.Run("group expressions and derived scopes", func(t *testing.T) {
		query, err := bindSQL(t, "SELECT q.n FROM (SELECT id+1 AS n FROM {{orders}} GROUP BY id+1) q ORDER BY q.n")
		require.NoError(t, err)
		sort := query.Root.(*qm.BoundSort)
		project := sort.Input.(*qm.BoundProject)
		subquery := project.Input.(*qm.BoundSubquery)
		assert.Equal(t, subquery.Columns[0].Output, project.Columns[0].Expr.Field)
		inner := subquery.Input.(*qm.BoundProject)
		aggregate := inner.Input.(*qm.BoundAggregate)
		assert.Equal(t, aggregate.GroupKeys[0].Output, inner.Columns[0].Expr.Field)
		assert.NotEqual(t, inner.Columns[0].Output, subquery.Columns[0].Output)
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
		cleanup := query.Root.(*qm.BoundProject)
		limit := cleanup.Input.(*qm.BoundLimit)
		sort := limit.Input.(*qm.BoundSort)
		project := sort.Input.(*qm.BoundProject)
		require.Len(t, project.OutputSchema, 2)
		assert.True(t, project.OutputSchema[1].Hidden)
		assert.Equal(t, project.Columns[1].Output, sort.Keys[0].Expr.Field)
		assert.Equal(t, int64(10), limit.Count)
		require.Len(t, query.OutputSchema, 1)
		assert.Equal(t, "name", query.OutputSchema[0].Name)
		assert.False(t, query.OutputSchema[0].Hidden)
	})
	t.Run("sort output alias shadows input and allows positions", func(t *testing.T) {
		query, err := bindSQL(t, "SELECT name AS id, amount AS a FROM {{orders}} ORDER BY id, 2 DESC")
		require.NoError(t, err)
		sort := query.Root.(*qm.BoundSort)
		assert.Equal(t, query.OutputSchema[0].ID, sort.Keys[0].Expr.Field)
		assert.Equal(t, qm.TypeString, sort.Keys[0].Expr.Type.Kind)
		assert.Equal(t, query.OutputSchema[1].ID, sort.Keys[1].Expr.Field)
		query, err = bindSQL(t, "SELECT MIN(amount) AS id FROM {{orders}} GROUP BY id ORDER BY id")
		require.NoError(t, err)
		assert.Equal(t, query.OutputSchema[0].ID, query.Root.(*qm.BoundSort).Keys[0].Expr.Field)
	})
	t.Run("distinct sort can use projected expressions", func(t *testing.T) {
		_, err := bindSQL(t, "SELECT DISTINCT id AS n FROM {{orders}} ORDER BY id")
		require.NoError(t, err)
		_, err = bindSQL(t, "SELECT DISTINCT id FROM {{orders}} ORDER BY id+1")
		require.NoError(t, err)
	})
	t.Run("stable function only declares one runtime value", func(t *testing.T) {
		query, err := bindSQL(t, "SELECT NOW() AS a, CURRENT_TIMESTAMP AS b FROM {{orders}}")
		require.NoError(t, err)
		require.Len(t, query.RuntimeSlots, 1)
		slot := query.RuntimeSlots[0]
		assert.Equal(t, qm.RuntimeQueryStart, slot.Source)
		require.NotNil(t, slot.Type.TimePrecision)
		assert.Equal(t, 6, *slot.Type.TimePrecision)
		columns := query.Root.(*qm.BoundProject).Columns
		assert.Equal(t, columns[0].Expr.RuntimeSlot, columns[1].Expr.RuntimeSlot)
		assert.False(t, columns[0].Expr.Nullable)
	})
	t.Run("strict expressions preserve exact types and NULL behavior", func(t *testing.T) {
		query, err := bindSQL(t, "SELECT -9223372036854775808 AS lo, 18446744073709551615 AS hi, 1.2300 AS d, NULL IS NULL AS z, COALESCE(NULL,1) AS c, CASE WHEN flag THEN id ELSE 0 END AS k FROM {{orders}}")
		require.NoError(t, err)
		columns := query.Root.(*qm.BoundProject).Columns
		assert.Equal(t, qm.IntegerValue("-9223372036854775808"), columns[0].Expr.Literal.Data)
		assert.True(t, columns[1].Expr.Type.Unsigned)
		assert.Equal(t, qm.DecimalValue("1.2300"), columns[2].Expr.Literal.Data)
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
		cycle := &qm.Filter{}
		cycle.Input = cycle
		input := bindingInput(t, "SELECT id FROM {{orders}} WHERE flag")
		expression := input.Root.(*qm.Project).Input.(*qm.Filter).Predicate
		expression.Kind = qm.ExprUnary
		expression.Op = "NOT"
		expression.Args = []*qm.Expr{expression}
		for _, q := range []*qm.UnboundQuery{nil, {}, {Scope: 1, Root: cycle}, input} {
			result, err := Bind(context.Background(), q, semantic, qm.ExecutionIdentity{}, resolver, authorizer)
			require.Error(t, err)
			assert.Nil(t, result)
		}
		for _, kind := range []qm.ExprKind{qm.ExprField, qm.ExprRuntimeSlot} {
			query := bindingInput(t, "SELECT id FROM {{orders}}")
			query.Root.(*qm.Project).Columns[0].Expr.Kind = kind
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
			func(r *interfaces.Resource) { r.SchemaDefinition[2].OriginalType = "DECIMAL" },
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
		input.Root = &qm.Sort{Input: input.Root, Keys: []qm.SortKey{{Expr: &qm.Expr{Kind: qm.ExprName, Name: []string{"id"}}}}}
		query, err := Bind(context.Background(), input, semantic, qm.ExecutionIdentity{}, resolver, authorizer)
		require.Error(t, err)
		assert.Nil(t, query)
	})
	t.Run("integer literals do not narrow ordinary arithmetic", func(t *testing.T) {
		query, err := bindSQL(t, "SELECT 127+1 AS n, 1/2 AS half FROM {{orders}}")
		require.NoError(t, err)
		expressions := query.Root.(*qm.BoundProject).Columns
		assert.Equal(t, 64, expressions[0].Expr.Type.BitWidth)
		assert.Equal(t, qm.TypeDecimal, expressions[1].Expr.Type.Kind)
		assert.Equal(t, 6, expressions[1].Expr.Type.Scale)
		assert.Equal(t, 1, expressions[1].Expr.Args[0].Type.Precision)
	})
	t.Run("decimal arithmetic preserves operand precision", func(t *testing.T) {
		query, err := bindSQL(t, "SELECT 1.23*1.2 AS n FROM {{orders}}")
		require.NoError(t, err)
		expr := query.Root.(*qm.BoundProject).Columns[0].Expr
		assert.Equal(t, qm.LogicalType{Kind: qm.TypeDecimal, Precision: 5, Scale: 3}, expr.Type)
		assert.Equal(t, qm.LogicalType{Kind: qm.TypeDecimal, Precision: 3, Scale: 2}, expr.Args[0].Type)
		assert.Equal(t, qm.LogicalType{Kind: qm.TypeDecimal, Precision: 2, Scale: 1}, expr.Args[1].Type)
	})
	t.Run("bindings can feed the single source planner", func(t *testing.T) {
		query, err := bindSQL(t, "SELECT a.id AS aid,b.id AS bid FROM {{orders}} a JOIN {{customers}} b ON a.id=b.id")
		require.NoError(t, err)
		resolver := bindingTargetResolverFunc(func(_ context.Context, scan qm.BoundScan, _ qm.ExecutionIdentity, _ qm.ExecutionOptions) ([]qm.TargetBinding, error) {
			candidate := qm.TargetBinding{
				Target:    qm.ExecutionTarget{CatalogID: scan.Resource.CatalogID, ConnectorType: "mysql", PhysicalTargetKey: "db", ConnectionProfile: "profile", EffectiveSourceIdentity: "principal", AuthorizationNamespace: "namespace", SchemaRevision: "schema-v1", ConfigRevision: "config-v1"},
				Source:    qm.QuerySourceSource,
				Relations: []qm.RelationBinding{{Instance: scan.Instance, ResourceID: scan.Resource.ID, Identifier: qm.PhysicalIdentifier{"db", scan.Resource.ID}}},
			}
			for _, f := range scan.Fields {
				candidate.Fields = append(candidate.Fields, qm.FieldBinding{Address: qm.FieldAddress{Relation: scan.Instance, Field: f.ID}, Identifier: qm.PhysicalIdentifier{f.LogicalName}, NativeType: f.NativeType})
			}
			return []qm.TargetBinding{candidate}, nil
		})
		plan, err := planning.Plan(context.Background(), query, qm.ExecutionIdentity{SubjectID: "caller"}, qm.ExecutionOptions{}, resolver)
		require.NoError(t, err)
		assert.Same(t, query, plan.Query)
		assert.Len(t, plan.TargetBinding.Relations, 2)
	})
}

func (f bindingTargetResolverFunc) ResolveSQLQueryTargets(ctx context.Context, scan qm.BoundScan, identity qm.ExecutionIdentity, options qm.ExecutionOptions) ([]qm.TargetBinding, error) {
	return f(ctx, scan, identity, options)
}
