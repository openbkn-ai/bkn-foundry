// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package binding

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	qm "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/querymodel"
)

func derivedResource(id, source string) *interfaces.Resource {
	return &interfaces.Resource{ID: id, CatalogID: "views", Category: interfaces.ResourceCategoryLogicalView, LogicType: interfaces.LogicType_Derived, Enabled: true, Status: interfaces.ResourceStatusActive, UpdateTime: 1,
		LogicDefinition:  &interfaces.DerivedLogicDefinition{SourceResourceID: source},
		SchemaDefinition: []*interfaces.Property{{Name: "public_id", OriginalName: "ID", Type: interfaces.DataType_Integer}, {Name: "label", OriginalName: "NAME", Type: interfaces.DataType_String}}}
}
func sourceForViews() *interfaces.Resource {
	source := bindingResource("base")
	for _, property := range source.SchemaDefinition {
		property.OriginalName = strings.ToUpper(property.Name)
	}
	return source
}
func bindResources(t *testing.T, input *qm.UnboundQuery, resources map[string]*interfaces.Resource) (*qm.BoundQuery, []string, error) {
	t.Helper()
	semantic, err := NewSemanticContext("UTC")
	require.NoError(t, err)
	var calls []string
	resolver := resourceResolverFunc(func(_ context.Context, id string, identity qm.ExecutionIdentity) (*interfaces.Resource, error) {
		assert.Equal(t, "caller", identity.SubjectID)
		calls = append(calls, "resolve:"+id)
		resource := resources[id]
		if resource == nil {
			return nil, errors.New("resource unavailable")
		}
		return resource, nil
	})
	authorizer := queryAuthorizerFunc(func(_ context.Context, id string, identity qm.ExecutionIdentity, op string) (qm.AuthorizationBasis, error) {
		assert.Equal(t, "caller", identity.SubjectID)
		calls = append(calls, "authorize:"+id)
		return qm.AuthorizationBasis{ResourceID: id, Operation: op, Revision: "v1"}, nil
	})
	query, err := Bind(context.Background(), input, semantic, qm.ExecutionIdentity{SubjectID: "caller"}, resolver, authorizer)
	return query, calls, err
}

func TestQueryBinderDerivedView(t *testing.T) {
	t.Run("fixed condition stays inside right side of outer join", func(t *testing.T) {
		view := derivedResource("view", "base")
		view.LogicDefinition = &interfaces.DerivedLogicDefinition{SourceResourceID: "base", FilterCondition: map[string]any{"field": "flag", "operation": "true"}}
		input := bindingInput(t, "SELECT a.id AS aid, v.public_id AS vid FROM {{base}} a LEFT JOIN {{view}} v ON a.id=v.public_id")
		query, calls, err := bindResources(t, input, map[string]*interfaces.Resource{"base": sourceForViews(), "view": view})
		require.NoError(t, err)
		assert.Equal(t, []string{"authorize:base", "resolve:base", "authorize:view", "resolve:view"}, calls)
		project := query.Root.(*qm.BoundProject)
		join := project.Input.(*qm.BoundJoin)
		boundary := join.Right.(*qm.BoundViewBoundary)
		fixed := boundary.Input.(*qm.BoundFilter)
		source := fixed.Input.(*qm.BoundScan)
		assert.Equal(t, qm.JoinLeft, join.Kind)
		assert.Equal(t, qm.ViewAuthorizationInvoker, boundary.Authorization.Mode)
		assert.Equal(t, qm.OriginSavedView, fixed.Predicate.Origin.Kind)
		assert.Equal(t, "view", fixed.Predicate.Origin.ResourceID)
		assert.Len(t, boundary.OutputSchema, 2)
		assert.Equal(t, "public_id", boundary.OutputSchema[0].Name)
		assert.NotEqual(t, source.Instance, boundary.Instance)
		assert.NotEqual(t, join.Left.(*qm.BoundScan).Instance, source.Instance)
		assert.Equal(t, boundary.Columns[0].Output, project.Columns[1].Expr.Field)
		assert.Equal(t, source.Fields[0].ID, boundary.Columns[0].Input)
		require.Len(t, query.Dependencies, 3)
		assert.True(t, query.OutputSchema[1].Nullable)
		for _, dep := range query.Dependencies {
			if dep.ID == "view" {
				assert.Equal(t, qm.DependencyView, dep.Kind)
			}
		}
		assert.Equal(t, qm.RelationInstanceID(2), input.Root.(*qm.Project).Input.(*qm.Join).Right.(*qm.Scan).Instance)
	})
	t.Run("nested view exports stay scoped and hide physical source fields", func(t *testing.T) {
		resources := map[string]*interfaces.Resource{"base": sourceForViews(), "inner": derivedResource("inner", "base"), "outer": derivedResource("outer", "inner")}
		query, calls, err := bindResources(t, bindingInput(t, "SELECT v.public_id FROM {{outer}} v"), resources)
		require.NoError(t, err)
		assert.Equal(t, []string{"authorize:outer", "resolve:outer", "authorize:inner", "resolve:inner", "authorize:base", "resolve:base"}, calls)
		outer := query.Root.(*qm.BoundProject).Input.(*qm.BoundViewBoundary)
		inner := outer.Input.(*qm.BoundViewBoundary)
		assert.NotEqual(t, outer.Scope, inner.Scope)
		assert.NotEqual(t, outer.Columns[0].Output, inner.Columns[0].Output)
		assert.Equal(t, inner.Columns[0].Output, outer.Columns[0].Input)
		for _, sql := range []string{"SELECT v.id FROM {{outer}} v", "SELECT v.flag FROM {{outer}} v", "SELECT base.id FROM {{outer}} v"} {
			result, _, err := bindResources(t, bindingInput(t, sql), resources)
			require.Error(t, err)
			assert.Nil(t, result)
		}
	})
	t.Run("underlying authorization denial never reads underlying metadata", func(t *testing.T) {
		semantic, err := NewSemanticContext("UTC")
		require.NoError(t, err)
		denied := errors.New("denied")
		var reads []string
		resolver := resourceResolverFunc(func(_ context.Context, id string, _ qm.ExecutionIdentity) (*interfaces.Resource, error) {
			reads = append(reads, id)
			return derivedResource("view", "base"), nil
		})
		authorizer := queryAuthorizerFunc(func(_ context.Context, id string, _ qm.ExecutionIdentity, op string) (qm.AuthorizationBasis, error) {
			if id == "base" {
				return qm.AuthorizationBasis{}, denied
			}
			return qm.AuthorizationBasis{ResourceID: id, Operation: op}, nil
		})
		result, err := Bind(context.Background(), bindingInput(t, "SELECT public_id FROM {{view}}"), semantic, qm.ExecutionIdentity{}, resolver, authorizer)
		require.ErrorIs(t, err, denied)
		assert.Nil(t, result)
		assert.Equal(t, []string{"view"}, reads)
		var stored *interfaces.StoredConditionBuildError
		assert.False(t, errors.As(err, &stored))
	})
	t.Run("saved condition errors retain definition provenance", func(t *testing.T) {
		for _, raw := range []any{map[string]any{"field": "flag", "operation": "=="}, map[string]any{"field": "missing", "operation": "null"}, map[string]any{"field": "id", "operation": "==", "value": "wrong type"}} {
			view := derivedResource("view", "base")
			view.LogicDefinition = &interfaces.DerivedLogicDefinition{SourceResourceID: "base", FilterCondition: raw}
			query, _, err := bindResources(t, bindingInput(t, "SELECT public_id FROM {{view}}"), map[string]*interfaces.Resource{"view": view, "base": sourceForViews()})
			require.Error(t, err)
			assert.Nil(t, query)
			var stored *interfaces.StoredConditionBuildError
			require.ErrorAs(t, err, &stored)
			var compile *qm.CompileError
			require.ErrorAs(t, err, &compile)
			assert.Equal(t, qm.OriginSavedView, compile.Origin.Kind)
			assert.Equal(t, "view", compile.Origin.ResourceID)
		}
	})
	t.Run("cycles and depth are bounded", func(t *testing.T) {
		resources := map[string]*interfaces.Resource{"a": derivedResource("a", "b"), "b": derivedResource("b", "a")}
		query, _, err := bindResources(t, bindingInput(t, "SELECT public_id FROM {{a}}"), resources)
		require.Error(t, err)
		assert.Nil(t, query)
		resources = map[string]*interfaces.Resource{"base": sourceForViews()}
		for i := 0; i <= interfaces.MaxRecursionDepth; i++ {
			source := "base"
			if i < interfaces.MaxRecursionDepth {
				source = fmt.Sprintf("v-%d", i+1)
			}
			id := fmt.Sprintf("v-%d", i)
			resources[id] = derivedResource(id, source)
		}
		query, _, err = bindResources(t, bindingInput(t, "SELECT public_id FROM {{v-0}}"), resources)
		require.Error(t, err)
		assert.Nil(t, query)
		var compile *qm.CompileError
		require.ErrorAs(t, err, &compile)
		assert.Equal(t, qm.ErrorLimit, compile.Kind)
	})
	for name, change := range map[string]func(*interfaces.Resource, *interfaces.Resource){
		"removed field":           func(view, source *interfaces.Resource) { view.SchemaDefinition[0].OriginalName = "MISSING" },
		"changed type":            func(view, source *interfaces.Resource) { view.SchemaDefinition[0].Type = interfaces.DataType_String },
		"ambiguous original name": func(view, source *interfaces.Resource) { source.SchemaDefinition[1].OriginalName = "ID" },
		"missing feature": func(view, source *interfaces.Resource) {
			view.SchemaDefinition[1].Features = []interfaces.PropertyFeature{{FeatureType: "keyword"}}
		},
		"duplicate output": func(view, source *interfaces.Resource) { view.SchemaDefinition[1].Name = "public_id" },
	} {
		t.Run(name, func(t *testing.T) {
			view, source := derivedResource("view", "base"), sourceForViews()
			change(view, source)
			query, _, err := bindResources(t, bindingInput(t, "SELECT public_id FROM {{view}}"), map[string]*interfaces.Resource{"view": view, "base": source})
			require.Error(t, err)
			assert.Nil(t, query)
		})
	}
}

func TestStructuredBinding(t *testing.T) {
	t.Run("aggregate alias is not rebound to same named source string", func(t *testing.T) {
		params := &interfaces.ResourceDataQueryParams{Aggregation: &interfaces.Aggregation{Property: "amount", Aggr: "min", Alias: "name"}, GroupBy: []*interfaces.GroupByItem{{Property: "id"}}, Having: &interfaces.HavingClause{Field: "__value", Operation: ">", Value: 1}, OutputFields: []string{"name"}, Sort: []*interfaces.SortField{{Field: "id"}}}
		input, options, err := BuildResourceDataQuery(params, "base")
		require.NoError(t, err)
		query, _, err := bindResources(t, input, map[string]*interfaces.Resource{"base": sourceForViews()})
		require.NoError(t, err)
		require.Len(t, query.OutputSchema, 1)
		assert.Equal(t, qm.TypeDecimal, query.OutputSchema[0].Type.Kind)
		crop := query.Root.(*qm.BoundProject)
		sort := crop.Input.(*qm.BoundSort)
		project := sort.Input.(*qm.BoundProject)
		having := project.Input.(*qm.BoundFilter)
		aggregate := having.Input.(*qm.BoundAggregate)
		require.Len(t, aggregate.GroupKeys, 1)
		assert.Equal(t, qm.ExprField, sort.Keys[0].Expr.Kind)
		assert.Equal(t, qm.QuerySourceAuto, options.QuerySource)
	})
	t.Run("literal contains and SQL LIKE remain distinct", func(t *testing.T) {
		params := &interfaces.ResourceDataQueryParams{FilterCondition: map[string]any{"field": "name", "operation": "like", "value": "%_"}}
		input, _, err := BuildResourceDataQuery(params, "base")
		require.NoError(t, err)
		query, _, err := bindResources(t, input, map[string]*interfaces.Resource{"base": sourceForViews()})
		require.NoError(t, err)
		predicate := query.Root.(*qm.BoundProject).Input.(*qm.BoundFilter).Predicate
		assert.Equal(t, qm.ExprLiteralContains, predicate.Kind)
		assert.Equal(t, qm.StringValue("%_"), predicate.Args[1].Literal.Data)
		sql, _, err := bindResources(t, bindingInput(t, "SELECT id FROM {{base}} WHERE name LIKE '%_'"), map[string]*interfaces.Resource{"base": sourceForViews()})
		require.NoError(t, err)
		assert.Equal(t, qm.ExprLike, sql.Root.(*qm.BoundProject).Input.(*qm.BoundFilter).Predicate.Kind)
	})
	t.Run("literal contains cannot be injected as SQL origin", func(t *testing.T) {
		params := &interfaces.ResourceDataQueryParams{FilterCondition: map[string]any{"field": "name", "operation": "like", "value": "x"}}
		input, _, err := BuildResourceDataQuery(params, "base")
		require.NoError(t, err)
		input.Root.(*qm.Project).Input.(*qm.Filter).Predicate.Origin.Kind = qm.OriginSQL
		query, _, err := bindResources(t, input, map[string]*interfaces.Resource{"base": sourceForViews()})
		require.Error(t, err)
		assert.Nil(t, query)
	})
	t.Run("explicit unknown output uses existing public error", func(t *testing.T) {
		input, _, err := BuildResourceDataQuery(&interfaces.ResourceDataQueryParams{OutputFields: []string{"missing"}}, "base")
		require.NoError(t, err)
		query, _, err := bindResources(t, input, map[string]*interfaces.Resource{"base": sourceForViews()})
		require.Error(t, err)
		assert.Nil(t, query)
		var output *interfaces.OutputFieldError
		require.ErrorAs(t, err, &output)
		assert.Equal(t, interfaces.OutputFieldUnknown, output.Reason)
	})
	t.Run("default output without fields uses no queryable fields error", func(t *testing.T) {
		source := sourceForViews()
		source.SchemaDefinition = nil
		input, _, err := BuildResourceDataQuery(&interfaces.ResourceDataQueryParams{}, "base")
		require.NoError(t, err)
		_, _, err = bindResources(t, input, map[string]*interfaces.Resource{"base": source})
		require.Error(t, err)
		var output *interfaces.OutputFieldError
		require.ErrorAs(t, err, &output)
		assert.Equal(t, interfaces.OutputFieldNoneQueryable, output.Reason)
	})
}
