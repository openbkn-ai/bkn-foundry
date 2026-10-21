// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package binding

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	qm "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/querymodel"
)

func bindTemporalTest(t *testing.T, property *interfaces.Property, params *interfaces.ResourceDataQueryParams, sql, zone string) (*qm.BoundQuery, error) {
	t.Helper()
	semantics, err := NewSemanticContext(zone)
	require.NoError(t, err)
	var input *qm.UnboundQuery
	if params != nil {
		input, _, err = BuildResourceDataQuery(params, "events")
		require.NoError(t, err)
	} else {
		input = bindingInput(t, sql)
	}
	resource := &interfaces.Resource{ID: "events", CatalogID: "catalog", Category: interfaces.ResourceCategoryTable,
		Enabled: true, Status: interfaces.ResourceStatusActive,
		SchemaDefinition: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_Integer}, property}}
	resolver := resourceResolverFunc(func(context.Context, string, qm.ExecutionIdentity) (*interfaces.Resource, error) {
		return resource, nil
	})
	authorizer := queryAuthorizerFunc(func(_ context.Context, id string, _ qm.ExecutionIdentity, op string) (qm.AuthorizationBasis, error) {
		return qm.AuthorizationBasis{ResourceID: id, Operation: op}, nil
	})
	return Bind(context.Background(), input, semantics, qm.ExecutionIdentity{}, resolver, authorizer)
}

func temporalTestLiteral(t *testing.T, expr *qm.BoundExpr) *qm.Value {
	t.Helper()
	for expr.Kind == qm.ExprCast {
		require.Len(t, expr.Args, 1)
		expr = expr.Args[0]
	}
	require.Equal(t, qm.ExprLiteral, expr.Kind)
	require.NotNil(t, expr.Literal)
	return expr.Literal
}

func temporalTestPredicate(t *testing.T, root qm.BoundRelation) *qm.BoundExpr {
	t.Helper()
	for {
		switch node := root.(type) {
		case *qm.BoundProject:
			root = node.Input
		case *qm.BoundFilter:
			return node.Predicate
		default:
			t.Fatalf("expected a filter, got %T", root)
			return nil
		}
	}
}

func TestBindTemporalValues(t *testing.T) {
	local := &interfaces.Property{Name: "created", Type: interfaces.DataType_Datetime, OriginalType: "DATETIME(9)"}
	t.Run("offset becomes service wall time and retains nanoseconds", func(t *testing.T) {
		params := &interfaces.ResourceDataQueryParams{OutputFields: []string{"id"}, FilterCondition: map[string]any{"field": "created", "operation": ">=", "value": "2026-10-21T00:00:00.123456789Z"}}
		before, err := json.Marshal(params)
		require.NoError(t, err)
		query, err := bindTemporalTest(t, local, params, "", "Asia/Shanghai")
		require.NoError(t, err)
		predicate := temporalTestPredicate(t, query.Root)
		value := temporalTestLiteral(t, predicate.Args[1])
		assert.Equal(t, qm.TypeLocalDateTime, value.Type.Kind)
		wall := value.Data.(qm.LocalDateTimeValue)
		assert.Equal(t, 8, wall.Time.Hour)
		assert.Equal(t, 123456789, wall.Time.Nanosecond)
		assert.Equal(t, "filter_condition.value", predicate.Args[1].Origin.Path)
		after, err := json.Marshal(params)
		require.NoError(t, err)
		assert.Equal(t, string(before), string(after))
	})
	t.Run("zoned source keeps the absolute instant", func(t *testing.T) {
		property := &interfaces.Property{Name: "created", Type: interfaces.DataType_Datetime, OriginalType: "timestamp(9) with time zone"}
		params := &interfaces.ResourceDataQueryParams{FilterCondition: map[string]any{"field": "created", "operation": "==", "value": "2026-10-21 08:00:00.123456789"}}
		query, err := bindTemporalTest(t, property, params, "", "Asia/Shanghai")
		require.NoError(t, err)
		value := temporalTestLiteral(t, temporalTestPredicate(t, query.Root).Args[1])
		assert.Equal(t, qm.TypeInstant, value.Type.Kind)
		assert.Equal(t, "2026-10-21T00:00:00.123456789Z", value.Data.(qm.InstantValue).Time.UTC().Format(time.RFC3339Nano))
	})
	t.Run("date and time stay calendar values", func(t *testing.T) {
		for _, tc := range []struct {
			public, native, value string
			kind                  qm.TypeKind
		}{
			{interfaces.DataType_Date, "DATE", "2024-02-29", qm.TypeDate},
			{interfaces.DataType_Time, "TIME(9) WITHOUT TIME ZONE", "23:59:59.000000001", qm.TypeTimeOfDay},
		} {
			query, err := bindTemporalTest(t, &interfaces.Property{Name: "created", Type: tc.public, OriginalType: tc.native}, &interfaces.ResourceDataQueryParams{FilterCondition: map[string]any{"field": "created", "operation": "==", "value": tc.value}}, "", "America/New_York")
			require.NoError(t, err)
			value := temporalTestLiteral(t, temporalTestPredicate(t, query.Root).Args[1])
			assert.Equal(t, tc.kind, value.Type.Kind)
		}
	})
	t.Run("exact milliseconds, including int64 bounds", func(t *testing.T) {
		for _, input := range []any{json.Number("1761004800001"), "1761004800001", json.Number("9223372036854775807"), json.Number("-9223372036854775808")} {
			property := &interfaces.Property{Name: "created", Type: interfaces.DataType_Datetime, OriginalType: "date_nanos"}
			query, err := bindTemporalTest(t, property, &interfaces.ResourceDataQueryParams{FilterCondition: map[string]any{"field": "created", "operation": "==", "value": input}}, "", "UTC")
			require.NoError(t, err)
			value := temporalTestLiteral(t, temporalTestPredicate(t, query.Root).Args[1])
			instant, ok := value.Data.(qm.InstantValue)
			require.True(t, ok)
			text, ok := input.(string)
			if !ok {
				text = input.(json.Number).String()
			}
			want, err := strconv.ParseInt(text, 10, 64)
			require.NoError(t, err)
			assert.Equal(t, want, instant.Time.UnixMilli())
		}
	})
	t.Run("IN range and aggregate HAVING normalize every constant", func(t *testing.T) {
		for _, op := range []string{"in", "range"} {
			params := &interfaces.ResourceDataQueryParams{FilterCondition: map[string]any{"field": "created", "operation": op, "value": []any{"2026-10-21 00:00:00.1", "2026-10-22 00:00:00.123456789"}}}
			query, err := bindTemporalTest(t, local, params, "", "UTC")
			require.NoError(t, err)
			predicate := temporalTestPredicate(t, query.Root)
			for _, arg := range predicate.Args[1:] {
				assert.Equal(t, qm.TypeLocalDateTime, temporalTestLiteral(t, arg).Type.Kind)
			}
		}
		params := &interfaces.ResourceDataQueryParams{Aggregation: &interfaces.Aggregation{Aggr: "min", Property: "created"}, Having: &interfaces.HavingClause{Field: "__value", Operation: ">", Value: "2026-10-21 00:00:00.123456789"}}
		query, err := bindTemporalTest(t, local, params, "", "UTC")
		require.NoError(t, err)
		predicate := temporalTestPredicate(t, query.Root)
		assert.Equal(t, qm.TypeLocalDateTime, temporalTestLiteral(t, predicate.Args[1]).Type.Kind)
	})
	t.Run("unknown source precision is not guessed from the constant", func(t *testing.T) {
		property := &interfaces.Property{Name: "created", Type: interfaces.DataType_Datetime, OriginalType: "DATETIME"}
		query, err := bindTemporalTest(t, property, &interfaces.ResourceDataQueryParams{FilterCondition: map[string]any{"field": "created", "operation": "==", "value": "2026-10-21 00:00:00.123456789"}}, "", "UTC")
		require.NoError(t, err)
		predicate := temporalTestPredicate(t, query.Root)
		assert.Nil(t, predicate.Args[0].Type.TimePrecision)
		assert.Nil(t, predicate.Args[1].Type.TimePrecision)
		assert.Equal(t, 9, *temporalTestLiteral(t, predicate.Args[1]).Type.TimePrecision)
	})
	t.Run("frozen timezone is independent of environment changes", func(t *testing.T) {
		t.Setenv("TZ", "America/New_York")
		query, err := bindTemporalTest(t, local, &interfaces.ResourceDataQueryParams{FilterCondition: map[string]any{"field": "created", "operation": "==", "value": "2026-10-21T00:00:00Z"}}, "", "Asia/Shanghai")
		require.NoError(t, err)
		value := temporalTestLiteral(t, temporalTestPredicate(t, query.Root).Args[1])
		assert.Equal(t, 8, value.Data.(qm.LocalDateTimeValue).Time.Hour)
	})
	t.Run("public timestamp does not inherit datetime input syntax", func(t *testing.T) {
		property := &interfaces.Property{Name: "created", Type: interfaces.DataType_Timestamp, OriginalType: "TIMESTAMPTZ(6)"}
		query, err := bindTemporalTest(t, property, &interfaces.ResourceDataQueryParams{FilterCondition: map[string]any{"field": "created", "operation": "==", "value": "2026-10-21T00:00:00Z"}}, "", "UTC")
		require.Error(t, err)
		assert.Nil(t, query)
	})
	for name, input := range map[string]any{"date only": "2026-10-21", "overprecision": "2026-10-21 00:00:00.1234567890", "fractional millis": json.Number("1.5"), "overflow millis": "9223372036854775808", "nonexistent date": "2026-02-30 00:00:00"} {
		t.Run("reject "+name, func(t *testing.T) {
			query, err := bindTemporalTest(t, local, &interfaces.ResourceDataQueryParams{FilterCondition: map[string]any{"field": "created", "operation": "==", "value": input}}, "", "UTC")
			require.Error(t, err)
			assert.Nil(t, query)
			var compileErr *qm.CompileError
			require.ErrorAs(t, err, &compileErr)
			assert.Equal(t, qm.OriginDTO, compileErr.Origin.Kind)
			assert.Equal(t, "filter_condition.value", compileErr.Origin.Path)
		})
	}

}

func TestBindTemporalSQLCasts(t *testing.T) {
	property := &interfaces.Property{Name: "created", Type: interfaces.DataType_Datetime, OriginalType: "DATETIME(9)"}
	query, err := bindTemporalTest(t, property, nil, "SELECT id FROM {{events}} WHERE created = '2026-10-21 00:00:00'", "UTC")
	require.Error(t, err)
	assert.Nil(t, query)
	query, err = bindTemporalTest(t, property, nil, "SELECT id FROM {{events}} WHERE created = CAST('2026-10-21 00:00:00.123456' AS DATETIME(6))", "UTC")
	require.NoError(t, err)
	predicate := temporalTestPredicate(t, query.Root)
	assert.Equal(t, qm.TypeLocalDateTime, temporalTestLiteral(t, predicate.Args[1]).Type.Kind)
	for _, sql := range []string{
		"SELECT CAST('2024-02-29' AS DATE) FROM {{events}}",
		"SELECT CAST('23:59:59.123456' AS TIME(6)) FROM {{events}}",
		"SELECT CAST('2026-10-21 00:00:00.123456000' AS DATETIME(6)) FROM {{events}}",
		"SELECT id FROM {{events}} WHERE created < CAST(NOW() AS DATETIME(6))",
	} {
		_, err := bindTemporalTest(t, property, nil, sql, "Asia/Shanghai")
		require.NoError(t, err, sql)
	}
	for _, sql := range []string{
		"SELECT CAST('2026-10-21 00:00:00.123456789' AS DATETIME(6)) FROM {{events}}",
		"SELECT CAST('2026-10-21 00:00:00' AS DATETIME(7)) FROM {{events}}",
		"SELECT CAST('24:00:00' AS TIME) FROM {{events}}",
		"SELECT CAST('-01:00:00' AS TIME) FROM {{events}}",
	} {
		query, err := bindTemporalTest(t, property, nil, sql, "UTC")
		require.Error(t, err, sql)
		assert.Nil(t, query)
	}

	publicTimestamp := &interfaces.Property{Name: "created", Type: interfaces.DataType_Timestamp, OriginalType: "TIMESTAMP(6) WITHOUT TIME ZONE"}
	query, err = bindTemporalTest(t, publicTimestamp, nil, "SELECT CAST(created AS DATETIME(6)) AS changed FROM {{events}}", "UTC")
	require.NoError(t, err)
	assert.Equal(t, interfaces.DataType_Datetime, query.OutputSchema[0].Encoding.PublicType)

}

func TestBindProjectionOnlySourceTypes(t *testing.T) {
	for _, tc := range []struct{ public, native string }{
		{interfaces.DataType_Time, "TIME"},
		{interfaces.DataType_Timestamp, "TIMESTAMP"},
		{interfaces.DataType_Datetime, "DATE"},
		{interfaces.DataType_Decimal, "DECIMAL"},
		{interfaces.DataType_Decimal, "DECIMAL(65,30)"},
		{interfaces.DataType_Vector, "dense_vector"},
		{interfaces.DataType_Other, "geography"},
		{interfaces.DataType_Ip, "ip"},
		{interfaces.DataType_Point, "geo_point"},
		{interfaces.DataType_Shape, "geo_shape"},
	} {
		t.Run(tc.public+" "+tc.native, func(t *testing.T) {
			property := &interfaces.Property{Name: "created", Type: tc.public, OriginalType: tc.native}
			for _, sql := range []string{"SELECT id FROM {{events}}", "SELECT * FROM {{events}}", "SELECT created AS renamed FROM {{events}}"} {
				query, err := bindTemporalTest(t, property, nil, sql, "UTC")
				require.NoError(t, err)
				assert.Equal(t, tc.native, query.Root.(*qm.BoundProject).Input.(*qm.BoundScan).Fields[1].NativeType.Name)
				if sql != "SELECT id FROM {{events}}" {
					assert.Equal(t, tc.public, query.OutputSchema[len(query.OutputSchema)-1].Encoding.PublicType)
				}
			}
			query, err := bindTemporalTest(t, property, nil, "SELECT id FROM {{events}} WHERE created = 'value'", "UTC")
			require.Error(t, err)
			assert.Nil(t, query)
			query, err = bindTemporalTest(t, property, nil, "SELECT COALESCE(created, created) FROM {{events}}", "UTC")
			require.Error(t, err)
			assert.Nil(t, query)
		})
	}
}

func TestBindTemporalSavedView(t *testing.T) {
	semantic, err := NewSemanticContext("Asia/Shanghai")
	require.NoError(t, err)
	source := &interfaces.Resource{ID: "events", CatalogID: "catalog", Category: interfaces.ResourceCategoryTable,
		Enabled: true, Status: interfaces.ResourceStatusActive,
		SchemaDefinition: []*interfaces.Property{{Name: "created", Type: interfaces.DataType_Datetime, OriginalType: "DATETIME(9)"}}}
	view := derivedResource("view", "events")
	view.SchemaDefinition = []*interfaces.Property{{Name: "created", OriginalName: "created", Type: interfaces.DataType_Datetime}}
	definition := view.LogicDefinition.(*interfaces.DerivedLogicDefinition)
	definition.FilterCondition = map[string]any{"field": "created", "operation": ">", "value": "bad date"}
	resolver := resourceResolverFunc(func(_ context.Context, id string, _ qm.ExecutionIdentity) (*interfaces.Resource, error) {
		if id == "view" {
			return view, nil
		}
		return source, nil
	})
	authorizer := queryAuthorizerFunc(func(_ context.Context, id string, _ qm.ExecutionIdentity, op string) (qm.AuthorizationBasis, error) {
		return qm.AuthorizationBasis{ResourceID: id, Operation: op}, nil
	})
	query, err := Bind(context.Background(), bindingInput(t, "SELECT * FROM {{view}}"), semantic, qm.ExecutionIdentity{}, resolver, authorizer)
	require.Error(t, err)
	assert.Nil(t, query)
	var stored *interfaces.StoredConditionBuildError
	require.ErrorAs(t, err, &stored)
	var compiled *qm.CompileError
	require.ErrorAs(t, err, &compiled)
	assert.Equal(t, qm.OriginSavedView, compiled.Origin.Kind)
	assert.Equal(t, "view", compiled.Origin.ResourceID)
	assert.Contains(t, compiled.Origin.Path, ".value")
	definition.FilterCondition = map[string]any{"field": "created", "operation": ">", "value": "2026-10-21T00:00:00.123456789Z"}
	query, err = Bind(context.Background(), bindingInput(t, "SELECT * FROM {{view}}"), semantic, qm.ExecutionIdentity{}, resolver, authorizer)
	require.NoError(t, err)
	boundary := query.Root.(*qm.BoundProject).Input.(*qm.BoundViewBoundary)
	value := temporalTestLiteral(t, boundary.Input.(*qm.BoundFilter).Predicate.Args[1])
	assert.Equal(t, 8, value.Data.(qm.LocalDateTimeValue).Time.Hour)
}
