// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package filter_condition

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

// 测试用字段映射
func testFieldsMap() map[string]*interfaces.Property {
	return map[string]*interfaces.Property{
		"name":       {Name: "name", Type: interfaces.DataType_String},
		"age":        {Name: "age", Type: interfaces.DataType_Integer},
		"score":      {Name: "score", Type: interfaces.DataType_Float},
		"created_at": {Name: "created_at", Type: interfaces.DataType_Datetime},
		"is_active":  {Name: "is_active", Type: interfaces.DataType_Boolean},
		"tags":       {Name: "tags", Type: interfaces.DataType_Text},
		"other_name": {Name: "other_name", Type: interfaces.DataType_String},
	}
}

func constCfg(name, op string, value any) *interfaces.FilterCondCfg {
	return &interfaces.FilterCondCfg{
		Field:     name,
		Operation: op,
		ValueFrom: interfaces.ValueFrom_Const,
		Value:     value,
	}
}

func TestNormalizeValueFrom(t *testing.T) {
	require.NoError(t, NormalizeValueFrom(nil))
	require.NoError(t, NormalizeValueFrom(&interfaces.FilterCondCfg{}))
	condition := &interfaces.FilterCondCfg{Operation: OperationAnd, SubConds: []*interfaces.FilterCondCfg{
		{Field: "age", Operation: OperationGt, Value: 0},
		{Operation: OperationOr, SubConds: []*interfaces.FilterCondCfg{
			{Field: "age", Operation: OperationEqual, Value: 1},
			{Field: "age", Operation: OperationEqual, Value: 2},
		}},
	}}
	require.NoError(t, NormalizeValueFrom(condition))
	assert.Equal(t, interfaces.ValueFrom_Const, condition.SubConds[0].ValueFrom)
	for _, child := range condition.SubConds[1].SubConds {
		assert.Equal(t, interfaces.ValueFrom_Const, child.ValueFrom)
	}

	field := constCfg("age", OperationGt, "score")
	field.ValueFrom = interfaces.ValueFrom_Field
	require.NoError(t, NormalizeValueFrom(field))
	assert.Equal(t, interfaces.ValueFrom_Field, field.ValueFrom)

	invalid := constCfg("age", OperationEqual, 1)
	invalid.ValueFrom = "unknown"
	require.ErrorContains(t, NormalizeValueFrom(invalid), "value_from")

	constOnly := constCfg("name", OperationLike, "a")
	constOnly.ValueFrom = interfaces.ValueFrom_Field
	require.ErrorContains(t, NormalizeValueFrom(constOnly), "value_from")

	for _, tc := range []struct {
		name      string
		operation string
		want      error
	}{
		{"missing operation", "", ErrMissingOperation},
		{"unsupported operation", "unknown", ErrUnsupportedOperation},
	} {
		t.Run(tc.name, func(t *testing.T) {
			child := constCfg("age", tc.operation, 1)
			require.ErrorIs(t, NormalizeValueFrom(child), tc.want)
			nested := &interfaces.FilterCondCfg{Operation: OperationAnd, SubConds: []*interfaces.FilterCondCfg{child}}
			require.ErrorIs(t, NormalizeValueFrom(nested), tc.want)
		})
	}
}

func TestResolveFulltextWildcardFields(t *testing.T) {
	for _, operation := range []string{OperationMatch, OperationMatchPhrase, OperationMultiMatch} {
		for _, single := range []bool{false, true} {
			if single && operation == OperationMultiMatch {
				continue
			}
			t.Run(fmt.Sprintf("%s/single=%v", operation, single), func(t *testing.T) {
				published := &interfaces.Property{Name: "published", Type: interfaces.DataType_Text, Filterable: new(false)}
				fields := map[string]*interfaces.Property{
					"published": published,
					"hidden":    {Name: "hidden", Type: interfaces.DataType_Text, Filterable: new(false)},
					"allowed":   {Name: "allowed", Type: interfaces.DataType_String, Filterable: new(true)},
					"embedding": {Name: "embedding", Type: interfaces.DataType_Vector, Filterable: new(true)},
				}
				wildcard := constCfg("", operation, "query")
				if single {
					wildcard.Field = interfaces.AllField
				} else {
					wildcard.RemainCfg = map[string]any{"fields": []any{interfaces.AllField}}
				}
				knn := constCfg("embedding", OperationKnnVector, []float32{0.1})
				knn.SubConds = []*interfaces.FilterCondCfg{wildcard}
				cfg := &interfaces.FilterCondCfg{Operation: OperationAnd, SubConds: []*interfaces.FilterCondCfg{
					constCfg("allowed", OperationEqual, "value"), {Operation: OperationOr, SubConds: []*interfaces.FilterCondCfg{knn}},
				}}
				actual, err := NewFilterCondition(t.Context(), cfg, fields)
				require.NoError(t, err)
				require.NoError(t, ResolveFulltextWildcardFields(actual, []string{"published"}))
				leaf := actual.(*AndCond).SubConds[1].(*OrCond).SubConds[0].(*KnnVectorCond).SubConds[0]
				var selected []*interfaces.Property
				switch match := leaf.(type) {
				case *MatchCond:
					selected = match.Fields
				case *MatchPhraseCond:
					selected = match.Fields
				case *MultiMatchCond:
					selected = match.Fields
				}
				require.Len(t, selected, 1)
				assert.Same(t, published, selected[0])
				assert.False(t, *published.Filterable)
				if single {
					assert.Equal(t, interfaces.AllField, wildcard.Field)
				} else {
					assert.Equal(t, []any{interfaces.AllField}, wildcard.RemainCfg["fields"])
				}
				for _, names := range [][]string{nil, {}, {"missing"}} {
					err := ResolveFulltextWildcardFields(actual, names)
					var invalid *interfaces.ConditionBuildError
					require.ErrorAs(t, err, &invalid)
				}
				wildcard.Field = interfaces.AllField
				wildcard.RemainCfg = map[string]any{"fields": []any{"allowed"}}
				explicit, err := NewFilterCondition(t.Context(), wildcard, fields)
				require.NoError(t, err)
				// 显式 fields 优先于 field="*"，不因候选范围改成 published。
				require.NoError(t, ResolveFulltextWildcardFields(explicit, []string{"published"}))
				switch match := explicit.(type) {
				case *MatchCond:
					assert.Equal(t, []*interfaces.Property{fields["allowed"]}, match.Fields)
				case *MatchPhraseCond:
					assert.Equal(t, []*interfaces.Property{fields["allowed"]}, match.Fields)
				case *MultiMatchCond:
					assert.Equal(t, []*interfaces.Property{fields["allowed"]}, match.Fields)
				}
				wildcard.RemainCfg["fields"] = []any{"published"}
				_, err = NewFilterCondition(t.Context(), wildcard, fields)
				require.ErrorContains(t, err, "not filterable")
				wildcard.RemainCfg["fields"] = []any{}
				empty, err := NewFilterCondition(t.Context(), wildcard, fields)
				require.NoError(t, err)
				require.NoError(t, ResolveFulltextWildcardFields(empty, nil))
			})
		}
	}
	require.NoError(t, ResolveFulltextWildcardFields(nil, nil))
}

func TestNewFilterCondition(t *testing.T) {
	t.Run("Feature 只使用实际物理目标的过滤开关", func(t *testing.T) {
		for _, operation := range []string{OperationMatch, OperationMatchPhrase, OperationMultiMatch, OperationEqual, OperationEqual2, OperationLike, OperationKnnVector} {
			t.Run(operation, func(t *testing.T) {
				for _, targetEnabled := range []bool{false, true} {
					name, kind, featureType, featureName, physical := "title", interfaces.DataType_String, interfaces.PropertyFeatureType_Fulltext, "search", "title.search"
					switch operation {
					case OperationEqual, OperationEqual2, OperationLike:
						name, kind, featureType, featureName, physical = "body", interfaces.DataType_Text, interfaces.PropertyFeatureType_Keyword, "raw", "body.raw"
					case OperationKnnVector:
						featureType, featureName, physical = interfaces.PropertyFeatureType_Vector, "", "title_vector"
					}
					fields := map[string]*interfaces.Property{
						name:     {Name: name, Type: kind, Filterable: new(!targetEnabled), Features: []interfaces.PropertyFeature{{FeatureType: featureType, FeatureName: featureName}}},
						physical: {Name: physical, Type: interfaces.DataType_String, Filterable: new(targetEnabled)},
					}
					cfg := constCfg(name, operation, "value")
					if operation == OperationMultiMatch {
						cfg.RemainCfg = map[string]any{"fields": []any{name}}
					}
					err := ValidateFilterable(cfg, fields)
					if targetEnabled {
						require.NoError(t, err)
					} else {
						require.ErrorContains(t, err, physical)
					}
					assert.Equal(t, !targetEnabled, *fields[name].Filterable)
				}
			})
		}
	})
	t.Run("物理子字段不扩大全文通配的逻辑字段范围", func(t *testing.T) {
		fields := map[string]*interfaces.Property{
			"body":           {Name: "body", Type: interfaces.DataType_Text, Filterable: new(false), Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Keyword, FeatureName: "raw"}}},
			"body.raw":       {Name: "body.raw", Type: interfaces.DataType_String, Filterable: new(true)},
			"title":          {Name: "title", Type: interfaces.DataType_String, Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Fulltext}}},
			"title.fulltext": {Name: "title.fulltext", Type: interfaces.DataType_Text, Filterable: new(true)},
			"title_vector":   {Name: "title_vector", Type: interfaces.DataType_Vector, Filterable: new(true)},
		}
		for _, operation := range []string{OperationMatch, OperationMatchPhrase, OperationMultiMatch} {
			cfg := constCfg("", operation, "value")
			cfg.RemainCfg = map[string]any{"fields": []any{interfaces.AllField}}
			condition, err := NewFilterCondition(t.Context(), cfg, fields)
			require.NoError(t, err)
			var selected []*interfaces.Property
			switch cond := condition.(type) {
			case *MatchCond:
				selected = cond.Fields
			case *MatchPhraseCond:
				selected = cond.Fields
			case *MultiMatchCond:
				selected = cond.Fields
			}
			names := make([]string, 0, len(selected))
			for _, field := range selected {
				names = append(names, field.Name)
			}
			assert.ElementsMatch(t, []string{"body", "title", "title_vector"}, names)
		}
	})
	t.Run("disabled field references cannot bypass validation", func(t *testing.T) {
		disabled := false
		fields := map[string]*interfaces.Property{
			"allowed": {Name: "allowed", Type: interfaces.DataType_String},
			"blocked": {Name: "blocked", Type: interfaces.DataType_String, Filterable: &disabled},
		}
		cases := []*interfaces.FilterCondCfg{
			{Field: "blocked", Operation: OperationEqual2, Value: "x"},
			{Field: "allowed", Operation: OperationEqual2, ValueFrom: interfaces.ValueFrom_Field, Value: "blocked"},
			{Operation: OperationAnd, SubConds: []*interfaces.FilterCondCfg{{Operation: OperationOr, SubConds: []*interfaces.FilterCondCfg{{Field: "blocked", Operation: OperationNull}}}}},
			{Operation: OperationMultiMatch, Value: "x", RemainCfg: map[string]any{"fields": []any{"allowed", "blocked"}}},
		}
		for _, cfg := range cases {
			result, err := NewFilterCondition(context.Background(), cfg, fields)
			require.ErrorContains(t, err, "not filterable")
			assert.Nil(t, result)
		}
		for _, kind := range []string{interfaces.DataType_Json, interfaces.DataType_Binary, interfaces.DataType_Other, "unknown"} {
			fields["legacy"] = &interfaces.Property{Name: "legacy", Type: kind}
			_, err := NewFilterCondition(context.Background(), constCfg("legacy", OperationNull, nil), fields)
			require.ErrorContains(t, err, "not filterable")
		}
		enabled := true
		fields["legacy"].Filterable = &enabled
		_, err := NewFilterCondition(context.Background(), constCfg("legacy", OperationNull, nil), fields)
		require.NoError(t, err, "explicit true does not get overwritten by type defaults")
	})

	t.Run("全文通配字段不按整个 Schema 的过滤开关拒绝", func(t *testing.T) {
		fields := map[string]*interfaces.Property{
			"allowed": {Name: "allowed", Type: interfaces.DataType_Text},
			"blocked": {Name: "blocked", Type: interfaces.DataType_Text, Filterable: new(false)},
			"payload": {Name: "payload", Type: interfaces.DataType_Json},
		}
		for _, operation := range []string{OperationMatch, OperationMatchPhrase, OperationMultiMatch} {
			t.Run(operation, func(t *testing.T) {
				cfg := constCfg("", operation, "hello")
				cfg.RemainCfg = map[string]any{"fields": []any{"*"}}
				nested := &interfaces.FilterCondCfg{Operation: OperationAnd, SubConds: []*interfaces.FilterCondCfg{cfg}}
				require.NoError(t, ValidateFilterable(nested, fields))
				condition, err := NewFilterCondition(context.Background(), cfg, fields)
				require.NoError(t, err)
				var resolved []*interfaces.Property
				switch actual := condition.(type) {
				case *MatchCond:
					resolved = actual.Fields
				case *MatchPhraseCond:
					resolved = actual.Fields
				case *MultiMatchCond:
					resolved = actual.Fields
				default:
					t.Fatalf("unexpected condition type %T", condition)
				}
				names := make([]string, len(resolved))
				for i, property := range resolved {
					names[i] = property.Name
				}
				assert.ElementsMatch(t, []string{"allowed", "blocked", "payload"}, names)
				_, err = NewFilterCondition(context.Background(), nested, fields)
				require.NoError(t, err)
				if operation != OperationMultiMatch {
					_, err = NewFilterCondition(context.Background(), constCfg("*", operation, "hello"), fields)
					require.NoError(t, err)
					_, err = NewFilterCondition(context.Background(), constCfg("blocked", operation, "hello"), fields)
					require.ErrorContains(t, err, "not filterable")
				}
				cfg.RemainCfg["fields"] = []any{"allowed", "blocked"}
				require.ErrorContains(t, ValidateFilterable(nested, fields), "not filterable")
				_, err = NewFilterCondition(context.Background(), cfg, fields)
				require.ErrorContains(t, err, "not filterable")
			})
		}
	})

	t.Run("large integer ranges preserve exact bounds", func(t *testing.T) {
		fields := map[string]*interfaces.Property{"wide": {Name: "wide", Type: "large_integer"}}
		bounds := []any{"-170141183460469231731687303715884105728", "170141183460469231731687303715884105727"}
		for _, op := range []string{OperationRange, OperationOutRange, OperationBetween} {
			cond, err := NewFilterCondition(context.Background(), constCfg("wide", op, bounds), fields)
			require.NoError(t, err)
			assert.Equal(t, op, cond.GetOperation())
			_, err = NewFilterCondition(context.Background(), constCfg("wide", op, []any{nil, "1"}), fields)
			require.ErrorContains(t, err, "nil")
		}
	})
	t.Run("nanosecond precision", func(t *testing.T) {
		for _, kind := range []string{interfaces.DataType_Datetime, interfaces.DataType_Time} {
			field := &interfaces.Property{Name: "value", Type: kind}
			literal := "14:30:00.123456789"
			if kind == interfaces.DataType_Datetime {
				literal = "2026-10-19T14:30:00.123456789Z"
			}
			cfg := constCfg("value", OperationGte, literal)
			cond, err := NewFilterCondition(context.Background(), cfg, map[string]*interfaces.Property{"value": field})
			require.NoError(t, err)
			if kind == interfaces.DataType_Datetime {
				assert.Equal(t, 123456789, cond.(*GteCond).Value.(time.Time).Nanosecond())
			} else {
				assert.Equal(t, literal, cond.(*GteCond).Value)
			}

		}
	})

	t.Run("temporal input contract", func(t *testing.T) {
		t.Setenv("TZ", "Asia/Shanghai")
		for _, literal := range []any{"2026-10-19T00:00:00+08:00", "2026-10-19 00:00:00", json.Number("1792339200000"), "1792339200000"} {
			cfg := constCfg("created_at", OperationGte, literal)
			cond, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
			require.NoError(t, err)
			parsed, ok := cond.(*GteCond).Value.(time.Time)
			require.True(t, ok, "runtime value: %T", cond.(*GteCond).Value)
			assert.Equal(t, int64(1792339200000), parsed.UnixMilli())
			assert.Equal(t, 0, parsed.Hour())
			assert.Equal(t, literal, cfg.Value)
		}

		for _, nativeType := range []string{"timestamp without time zone", "datetime", "datetime2", "timestamp"} {
			field := &interfaces.Property{Name: "value", Type: interfaces.DataType_Datetime, OriginalType: nativeType}
			native := time.Date(2026, 10, 19, 0, 0, 0, 123456789, time.UTC)
			cfg := constCfg("value", OperationGte, native)
			cond, err := NewFilterCondition(context.Background(), cfg, map[string]*interfaces.Property{"value": field})
			require.NoError(t, err)
			parsed := cond.(*GteCond).Value.(time.Time)
			assert.Equal(t, int64(1792339200123), parsed.UnixMilli())
			assert.Equal(t, 123456789, parsed.Nanosecond())
			assert.Equal(t, native, cfg.Value)
		}
		t.Setenv("TZ", "invalid/zone")
		cond, err := NewFilterCondition(context.Background(), constCfg("created_at", OperationGte, "2026-10-19 00:00:00"), testFieldsMap())
		var zoneErr *interfaces.ServerTimeZoneError
		require.ErrorAs(t, err, &zoneErr)
		assert.Nil(t, cond)
		t.Setenv("TZ", "")
		cond, err = NewFilterCondition(context.Background(), constCfg("created_at", OperationGte, "2026-10-19 00:00:00"), testFieldsMap())
		require.NoError(t, err)
		assert.Equal(t, time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC), cond.(*GteCond).Value)
		t.Setenv("TZ", "Asia/Shanghai")

		for _, tc := range []struct {
			kind    string
			valid   string
			invalid []any
		}{
			{interfaces.DataType_Date, "2026-10-19", []any{"2026-10-19T00:00:00Z", "1792339200000", json.Number("1792339200000"), "2026-02-30"}},
			{interfaces.DataType_Time, "14:30:00.123", []any{"14:30", "24:00:00", "14:30:00Z", "1792339200000", json.Number("1792339200000")}},
		} {
			fields := map[string]*interfaces.Property{"value": {Name: "value", Type: tc.kind}}
			cond, err := NewFilterCondition(context.Background(), constCfg("value", OperationEqual, tc.valid), fields)
			require.NoError(t, err)
			assert.Equal(t, tc.valid, cond.(*EqualCond).Value)
			for _, literal := range tc.invalid {
				cond, err := NewFilterCondition(context.Background(), constCfg("value", OperationEqual, literal), fields)
				require.Error(t, err, "%s: %v", tc.kind, literal)
				assert.Nil(t, cond)
			}
		}
	})
	t.Run("datetime literals", func(t *testing.T) {
		for _, op := range []string{OperationEqual, OperationEqual2, OperationNotEqual, OperationNotEqual2,
			OperationGt, OperationGt2, OperationGte, OperationGte2, OperationLt, OperationLt2,
			OperationLte, OperationLte2, OperationIn, OperationNotIn, OperationRange, OperationOutRange, OperationBetween} {
			t.Run(op, func(t *testing.T) {
				for _, literal := range []any{"2026-10-19", "bad date", "2026-02-30 00:00:00", true, 1.5} {
					value := literal
					if !OperationMap[op].IsSingleValue() {
						value = []any{"2026-10-19 00:00:00", literal}
					}
					cond, err := NewFilterCondition(context.Background(), constCfg("created_at", op, value), testFieldsMap())
					require.Error(t, err, "literal: %v", literal)
					assert.Nil(t, cond)
					assert.Contains(t, err.Error(), "created_at")
				}
				for _, literal := range []any{"2026-10-19 00:00:00", "2026-10-19T00:00:00+08:00", json.Number("1792339200000"), "1792339200000"} {
					value := literal
					if !OperationMap[op].IsSingleValue() {
						value = []any{literal, literal}
					}
					cfg := constCfg("created_at", op, value)
					cond, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
					require.NoError(t, err)
					assert.NotNil(t, cond)
					assert.Equal(t, value, cfg.Value)
				}
			})
		}
		for _, fieldType := range []string{interfaces.DataType_Timestamp} {
			fields := map[string]*interfaces.Property{"value": {Name: "value", Type: fieldType}}
			cond, err := NewFilterCondition(context.Background(), constCfg("value", OperationGte, "connector-specific"), fields)
			require.NoError(t, err)
			assert.NotNil(t, cond)
		}
		cfg := constCfg("created_at", OperationGte, "created_at")
		cfg.ValueFrom = interfaces.ValueFrom_Field
		cond, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
		require.NoError(t, err)
		assert.NotNil(t, cond)
	})
	t.Run("match field arrays", func(t *testing.T) {
		for _, operation := range []string{OperationMatch, OperationMatchPhrase, OperationMultiMatch} {
			t.Run(operation, func(t *testing.T) {
				for _, tc := range []struct {
					name   string
					fields []any
					want   []string
				}{
					{name: "integer", fields: []any{1}},
					{name: "json number", fields: []any{json.Number("1")}},
					{name: "null", fields: []any{nil}},
					{name: "boolean", fields: []any{true}},
					{name: "object", fields: []any{map[string]any{"field": "name"}}},
					{name: "mixed types", fields: []any{"name", 1}},
					{name: "unknown field", fields: []any{"missing"}},
					{name: "single field", fields: []any{"name"}, want: []string{"name"}},
					{name: "multiple fields", fields: []any{"name", "tags"}, want: []string{"name", "tags"}},
					{name: "wildcard", fields: []any{interfaces.AllField},
						want: []string{"name", "age", "score", "created_at", "is_active", "tags", "other_name"}},
				} {
					t.Run(tc.name, func(t *testing.T) {
						cfg := constCfg("", operation, "hello")
						cfg.RemainCfg = map[string]any{"fields": tc.fields}
						cond, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
						if tc.want == nil {
							require.Error(t, err)
							assert.Nil(t, cond)
							assert.Contains(t, err.Error(), "field")
							return
						}
						require.NoError(t, err)
						require.NotNil(t, cond)
						assert.Equal(t, operation, cond.GetOperation())
						var fields []*interfaces.Property
						switch actual := cond.(type) {
						case *MatchCond:
							fields = actual.Fields
						case *MatchPhraseCond:
							fields = actual.Fields
						case *MultiMatchCond:
							fields = actual.Fields
						default:
							t.Fatalf("unexpected condition type %T", cond)
						}
						names := make([]string, len(fields))
						for i, field := range fields {
							names[i] = field.Name
						}
						assert.ElementsMatch(t, tc.want, names)
					})
				}
			})
		}
	})

	t.Run("returns nil for nil config", func(t *testing.T) {
		cond, err := NewFilterCondition(context.Background(), nil, testFieldsMap())

		require.NoError(t, err)
		assert.Nil(t, cond)
	})

	t.Run("returns nil for empty config", func(t *testing.T) {
		cond, err := NewFilterCondition(context.Background(), &interfaces.FilterCondCfg{}, testFieldsMap())

		require.NoError(t, err)
		assert.Nil(t, cond)
	})

	t.Run("returns error for unsupported operation", func(t *testing.T) {
		cond, err := NewFilterCondition(context.Background(), constCfg("name", "unknown_op", "test"), testFieldsMap())

		require.Error(t, err)
		assert.Nil(t, cond)
		assert.Contains(t, err.Error(), "unsupported operation")
	})

	t.Run("creates condition for valid equal operation", func(t *testing.T) {
		cond, err := NewFilterCondition(context.Background(), constCfg("name", "==", "alice"), testFieldsMap())

		require.NoError(t, err)
		require.NotNil(t, cond)
		assert.Equal(t, OperationEqual, cond.GetOperation())
	})
	errorFieldsMap := advancedFieldsMap()
	errorTests := []struct {
		name      string
		cfg       *interfaces.FilterCondCfg
		assertion func(t *testing.T, cond interfaces.FilterCondition)
	}{
		{name: "range accepts numeric field", cfg: constCfg("age", OperationRange, []any{18, 30}), assertion: func(t *testing.T, cond interfaces.FilterCondition) {
			got := cond.(*RangeCond)
			assert.Equal(t, "age", got.Lfield.Name)
			assert.Equal(t, []any{18, 30}, got.Value)
		}},
		{name: "out range accepts datetime field", cfg: constCfg("created_at", OperationOutRange, []any{"2026-01-01 00:00:00", "2026-12-31 00:00:00"}), assertion: func(t *testing.T, cond interfaces.FilterCondition) {
			got := cond.(*OutRangeCond)
			assert.Equal(t, "created_at", got.Lfield.Name)
		}},
		{name: "before accepts interval pair", cfg: constCfg("created_at", OperationBefore, []any{float64(3), CurrentDay}), assertion: func(t *testing.T, cond interfaces.FilterCondition) {
			got := cond.(*BeforeCond)
			assert.Equal(t, []any{float64(3), CurrentDay}, got.Value)
		}},
		{name: "before normalizes json number interval", cfg: constCfg("created_at", OperationBefore, []any{json.Number("3"), CurrentDay}), assertion: func(t *testing.T, cond interfaces.FilterCondition) {
			got := cond.(*BeforeCond)
			assert.Equal(t, []any{int64(3), CurrentDay}, got.Value)
		}},
		{name: "current accepts supported unit", cfg: constCfg("created_at", OperationCurrent, CurrentMonth), assertion: func(t *testing.T, cond interfaces.FilterCondition) {
			got := cond.(*CurrentCond)
			assert.Equal(t, CurrentMonth, got.Value)
		}},
		{name: "between uses declared datetime field", cfg: constCfg("created_at", OperationBetween, []any{"2026-01-01 00:00:00", "2026-01-02 00:00:00"}), assertion: func(t *testing.T, cond interfaces.FilterCondition) {
			got := cond.(*BetweenCond)
			assert.Equal(t, "created_at", got.Lfield.Name)
			assert.Equal(t, interfaces.DataType_Datetime, got.Lfield.Type)
		}},
		{name: "match uses remain fields list", cfg: &interfaces.FilterCondCfg{
			Operation: OperationMatch,
			ValueFrom: interfaces.ValueFrom_Const,
			Value:     "hello",
			RemainCfg: map[string]any{"fields": []any{"name", "tags"}},
		}, assertion: func(t *testing.T, cond interfaces.FilterCondition) {
			got := cond.(*MatchCond)
			require.Len(t, got.Fields, 2)
			assert.Equal(t, "name", got.Fields[0].Name)
			assert.Equal(t, "tags", got.Fields[1].Name)
		}},
		{name: "multi match stores match type", cfg: &interfaces.FilterCondCfg{
			Operation: OperationMultiMatch,
			ValueFrom: interfaces.ValueFrom_Const,
			Value:     "hello",
			RemainCfg: map[string]any{
				"fields":     []any{"name", "tags"},
				"match_type": "best_fields",
			},
		}, assertion: func(t *testing.T, cond interfaces.FilterCondition) {
			got := cond.(*MultiMatchCond)
			require.Len(t, got.Fields, 2)
			assert.Equal(t, "best_fields", got.MatchType)
		}},
		{name: "knn vector accepts vector field and sub conditions", cfg: &interfaces.FilterCondCfg{
			Field:     "embedding",
			Operation: OperationKnnVector,
			ValueFrom: interfaces.ValueFrom_Const,
			Value:     []float32{0.1, 0.2},
			SubConds: []*interfaces.FilterCondCfg{
				constCfg("is_active", OperationTrue, nil),
			},
		}, assertion: func(t *testing.T, cond interfaces.FilterCondition) {
			got := cond.(*KnnVectorCond)
			assert.Equal(t, "embedding", got.FilterFieldName)
			require.Len(t, got.SubConds, 1)
			assert.Equal(t, OperationTrue, got.SubConds[0].GetOperation())
		}},
		{name: "and ignores empty sub condition", cfg: &interfaces.FilterCondCfg{
			Operation: OperationAnd,
			SubConds: []*interfaces.FilterCondCfg{
				{},
				constCfg("name", OperationEqual, "alice"),
			},
		}, assertion: func(t *testing.T, cond interfaces.FilterCondition) {
			got := cond.(*AndCond)
			require.Len(t, got.SubConds, 1)
			assert.Equal(t, OperationEqual, got.SubConds[0].GetOperation())
		}},
	}

	for _, tt := range errorTests {
		t.Run(tt.name, func(t *testing.T) {
			cond, err := NewFilterCondition(context.Background(), tt.cfg, errorFieldsMap)

			require.NoError(t, err)
			require.NotNil(t, cond)
			tt.assertion(t, cond)
		})
	}
	fieldsMap := advancedFieldsMap()
	tests := []struct {
		name       string
		cfg        *interfaces.FilterCondCfg
		errContain string
	}{
		{name: "range rejects string field", cfg: constCfg("name", OperationRange, []any{1, 2}), errContain: "not a date/number field"},
		{name: "before rejects nonnumeric interval", cfg: constCfg("created_at", OperationBefore, []any{"invalid", CurrentDay}), errContain: "positive integer"},
		{name: "current rejects unsupported unit", cfg: constCfg("created_at", OperationCurrent, "quarter"), errContain: "right value should be"},
		{name: "multi match requires fields array", cfg: constCfg("", OperationMultiMatch, "hello"), errContain: "'fields' value should be an array"},
		{name: "multi match rejects unknown match type", cfg: &interfaces.FilterCondCfg{
			Operation: OperationMultiMatch,
			ValueFrom: interfaces.ValueFrom_Const,
			Value:     "hello",
			RemainCfg: map[string]any{
				"fields":     []any{"name"},
				"match_type": "unknown",
			},
		}, errContain: "'match_type' value should be"},
		{name: "knn vector rejects non-vector field", cfg: constCfg("name", OperationKnnVector, []float32{0.1}), errContain: "type must be vector"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cond, err := NewFilterCondition(context.Background(), tt.cfg, fieldsMap)

			require.Error(t, err)
			assert.Nil(t, cond)
			assert.ErrorContains(t, err, tt.errContain)
		})
	}
}

func TestEqualCond(t *testing.T) {
	t.Run("equal cond valid", func(t *testing.T) {
		cfg := constCfg("name", "==", "alice")
		cond, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		eq := cond.(*EqualCond)
		if eq.Lfield.Name != "name" {
			t.Errorf("expected left field 'name', got '%s'", eq.Lfield.Name)
		}
		if eq.Value != "alice" {
			t.Errorf("expected value 'alice', got '%v'", eq.Value)
		}
	})
	t.Run("equal cond empty field name", func(t *testing.T) {
		cfg := constCfg("", "==", "alice")
		_, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err == nil {
			t.Fatal("expected error for empty field name")
		}
		if !strings.Contains(err.Error(), "left field is empty") {
			t.Errorf("unexpected error: %v", err)
		}
	})
	t.Run("equal cond field not found", func(t *testing.T) {
		cfg := constCfg("nonexistent", "==", "alice")
		_, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err == nil {
			t.Fatal("expected error for unknown field")
		}
		if !strings.Contains(err.Error(), "not found") {
			t.Errorf("unexpected error: %v", err)
		}
	})
	t.Run("equal cond rejects array value", func(t *testing.T) {
		cfg := constCfg("name", "==", []any{"a", "b"})
		_, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err == nil {
			t.Fatal("expected error for array value in equal condition")
		}
		if !strings.Contains(err.Error(), "single value") {
			t.Errorf("unexpected error: %v", err)
		}
	})
	t.Run("equal cond field to field", func(t *testing.T) {
		cfg := &interfaces.FilterCondCfg{
			Field:     "name",
			Operation: "==",
			ValueFrom: interfaces.ValueFrom_Field,
			Value:     "other_name",
		}
		cond, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		eq := cond.(*EqualCond)
		if eq.Rfield == nil {
			t.Fatal("expected right field to be set")
		}
		if eq.Rfield.Name != "other_name" {
			t.Errorf("expected right field 'other_name', got '%s'", eq.Rfield.Name)
		}
	})
	t.Run("equal cond field to field right not found", func(t *testing.T) {
		cfg := &interfaces.FilterCondCfg{
			Field:     "name",
			Operation: "==",
			ValueFrom: interfaces.ValueFrom_Field,
			Value:     "nonexistent",
		}
		_, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err == nil {
			t.Fatal("expected error for unknown right field")
		}
		if !strings.Contains(err.Error(), "right field") {
			t.Errorf("unexpected error: %v", err)
		}
	})
	t.Run("equal cond alias operations", func(t *testing.T) {
		// "eq" 是 "==" 的别名
		cfg := constCfg("name", "eq", "alice")
		cond, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cond.GetOperation() != OperationEqual {
			t.Errorf("expected operation '==', got '%s'", cond.GetOperation())
		}
	})
}

func TestInCond(t *testing.T) {
	t.Run("in cond valid", func(t *testing.T) {
		cfg := constCfg("name", "in", []any{"alice", "bob"})
		cond, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		in := cond.(*InCond)
		if len(in.Value) != 2 {
			t.Errorf("expected 2 values, got %d", len(in.Value))
		}
	})
	t.Run("in cond empty array", func(t *testing.T) {
		cfg := constCfg("name", "in", []any{})
		_, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err == nil {
			t.Fatal("expected error for empty array")
		}
		if !strings.Contains(err.Error(), "length >= 1") {
			t.Errorf("unexpected error: %v", err)
		}
	})
	t.Run("in cond non array value", func(t *testing.T) {
		cfg := constCfg("name", "in", "single_value")
		_, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err == nil {
			t.Fatal("expected error for non-array value")
		}
		if !strings.Contains(err.Error(), "should be an array") {
			t.Errorf("unexpected error: %v", err)
		}
	})
	t.Run("in cond rejects field value from", func(t *testing.T) {
		cfg := &interfaces.FilterCondCfg{
			Field:     "name",
			Operation: "in",
			ValueFrom: interfaces.ValueFrom_Field,
			Value:     []any{"alice"},
		}
		_, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err == nil {
			t.Fatal("expected error for non-const value_from")
		}
		if !strings.Contains(err.Error(), "does not support value_from") {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestLikeCond(t *testing.T) {
	t.Run("SQL LIKE 模式保持原值并拒绝孤立转义", func(t *testing.T) {
		for _, operation := range []string{OperationLike, OperationNotLike} {
			for _, pattern := range []string{"", "%", "_", "%ali%", "ali%", "%ali", "object_type", "中文_😀%", ".*", `a*b?c`, `50\%`, `a\_b`, `a\\b`, `a\nb`} {
				t.Run(operation+"/"+pattern, func(t *testing.T) {
					cfg := constCfg("name", operation, pattern)
					condition, err := NewFilterCondition(t.Context(), cfg, testFieldsMap())
					require.NoError(t, err)
					switch value := condition.(type) {
					case *LikeCond:
						assert.Equal(t, pattern, value.Value)
					case *NotLikeCond:
						assert.Equal(t, pattern, value.Value)
					}
					assert.Equal(t, pattern, cfg.Value)
				})
			}
			condition, err := NewFilterCondition(t.Context(), constCfg("name", operation, `trailing\`), testFieldsMap())
			require.ErrorContains(t, err, "escape")
			assert.Nil(t, condition)
		}
	})
	t.Run("like cond valid", func(t *testing.T) {
		cfg := constCfg("name", "like", "ali")
		cond, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		like := cond.(*LikeCond)
		if like.Value != "ali" {
			t.Errorf("expected value 'ali', got '%s'", like.Value)
		}
	})
	t.Run("like cond non string field", func(t *testing.T) {
		cfg := constCfg("age", "like", "test")
		_, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err == nil {
			t.Fatal("expected error for non-string field")
		}
		if !strings.Contains(err.Error(), "not a string field") {
			t.Errorf("unexpected error: %v", err)
		}
	})
	t.Run("like cond non string value", func(t *testing.T) {
		cfg := constCfg("name", "like", 123)
		_, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err == nil {
			t.Fatal("expected error for non-string value")
		}
		if !strings.Contains(err.Error(), "not a string value") {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestValidateLikePattern(t *testing.T) {
	for _, operation := range []string{OperationLike, OperationNotLike} {
		for _, pattern := range []string{"", "%", "_", "a%_", `\%`, `\_`, `\\`, `\q`, "中文_😀%"} {
			require.NoError(t, ValidateLikePattern(operation, pattern))
		}
		for _, pattern := range []string{`\`, `abc\`, `a\\\`} {
			err := ValidateLikePattern(operation, pattern)
			var invalid *interfaces.ConditionBuildError
			require.ErrorAs(t, err, &invalid)
			assert.Contains(t, err.Error(), operation)
		}
	}
}

func TestNormalizeSQLLikePattern(t *testing.T) {
	for _, operation := range []string{OperationLike, OperationNotLike} {
		for _, tc := range []struct{ pattern, want string }{
			{"", ""}, {"%_", "%_"}, {"%ali%", "%ali%"}, {`50\%`, `50\%`}, {`a\_b`, `a\_b`}, {`a\\b`, `a\\b`},
			{`a\qb`, "aqb"}, {`\*\?\[`, "*?["}, {`\中\😀_`, "中😀_"}, {`\q\%_\\\?`, `q\%_\\?`},
		} {
			t.Run(operation+"/"+tc.pattern, func(t *testing.T) {
				original := tc.pattern
				got, err := NormalizeSQLLikePattern(operation, tc.pattern)
				require.NoError(t, err)
				assert.Equal(t, tc.want, got)
				assert.Equal(t, original, tc.pattern)
			})
		}
		got, err := NormalizeSQLLikePattern(operation, `a\`)
		var invalid *interfaces.ConditionBuildError
		require.ErrorAs(t, err, &invalid)
		assert.Empty(t, got)
	}
}

func TestSQLLikeToWildcard(t *testing.T) {
	for _, tc := range []struct{ pattern, want string }{
		{"", ""}, {"%", "*"}, {"_", "?"}, {"ali", "ali"}, {"%ali%", "*ali*"},
		{"ali%", "ali*"}, {"%ali", "*ali"}, {"a_b", "a?b"}, {"中文_😀%", "中文?😀*"},
		{`50\%`, "50%"}, {`a\_b`, "a_b"}, {`a\\b`, `a\\b`}, {`a\nb`, "anb"},
		{`a*b?c`, `a\*b\?c`}, {`.*`, `.\*`}, {`\*\?`, `\*\?`}, {`'[%]`, "'[*]"},
	} {
		t.Run(tc.pattern, func(t *testing.T) {
			got, err := SQLLikeToWildcard(OperationLike, tc.pattern)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
	got, err := SQLLikeToWildcard(OperationNotLike, `abc\`)
	require.Error(t, err)
	assert.Empty(t, got)
}

func TestRangeCond(t *testing.T) {
	t.Run("range cond valid", func(t *testing.T) {
		cfg := constCfg("age", "range", []any{18, 65})
		cond, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		r := cond.(*RangeCond)
		if len(r.Value) != 2 {
			t.Errorf("expected 2 values, got %d", len(r.Value))
		}
	})
	t.Run("range cond wrong array length", func(t *testing.T) {
		cfg := constCfg("age", "range", []any{18})
		_, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err == nil {
			t.Fatal("expected error for wrong array length")
		}
		if !strings.Contains(err.Error(), "length 2") {
			t.Errorf("unexpected error: %v", err)
		}
	})
	t.Run("range cond non numeric field", func(t *testing.T) {
		cfg := constCfg("name", "range", []any{1, 2})
		_, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err == nil {
			t.Fatal("expected error for non-numeric field")
		}
		if !strings.Contains(err.Error(), "not a date/number field") {
			t.Errorf("unexpected error: %v", err)
		}
	})
	t.Run("range cond date field", func(t *testing.T) {
		cfg := constCfg("created_at", "range", []any{"2024-01-01 00:00:00", "2024-12-31 00:00:00"})
		_, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestOrCondNew(t *testing.T) {
	for _, tc := range []struct {
		name     string
		children []*interfaces.FilterCondCfg
	}{
		{"empty object", []*interfaces.FilterCondCfg{{}}},
		{"nil children", []*interfaces.FilterCondCfg{nil}},
		{"mixed empty children", []*interfaces.FilterCondCfg{nil, {}}},
		{"nested empty group", []*interfaces.FilterCondCfg{{Operation: OperationOr, SubConds: []*interfaces.FilterCondCfg{{}}}}},
	} {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			cfg := &interfaces.FilterCondCfg{Operation: OperationOr, SubConds: tc.children}
			cond, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
			require.ErrorContains(t, err, "no effective child conditions")
			assert.Nil(t, cond)
		})
	}
	t.Run("ignores empty children beside valid conditions", func(t *testing.T) {
		cfg := &interfaces.FilterCondCfg{Operation: OperationOr, SubConds: []*interfaces.FilterCondCfg{nil, {}, constCfg("name", OperationEqual, "alice"), constCfg("age", OperationGt, 18)}}
		cond, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
		require.NoError(t, err)
		children := cond.(*OrCond).SubConds
		require.Len(t, children, 2)
		assert.Equal(t, OperationEqual, children[0].GetOperation())
		assert.Equal(t, OperationGt, children[1].GetOperation())
	})
}

func TestAndCond(t *testing.T) {
	for _, tc := range []struct {
		name     string
		children []*interfaces.FilterCondCfg
	}{
		{"empty object", []*interfaces.FilterCondCfg{{}}},
		{"nil children", []*interfaces.FilterCondCfg{nil}},
		{"mixed empty children", []*interfaces.FilterCondCfg{nil, {}}},
		{"nested empty group", []*interfaces.FilterCondCfg{{Operation: OperationAnd, SubConds: []*interfaces.FilterCondCfg{{}}}}},
	} {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			cfg := &interfaces.FilterCondCfg{Operation: OperationAnd, SubConds: tc.children}
			cond, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
			require.ErrorContains(t, err, "no effective child conditions")
			assert.Nil(t, cond)
		})
	}
	t.Run("ignores empty children beside a valid condition", func(t *testing.T) {
		cfg := &interfaces.FilterCondCfg{Operation: OperationAnd, SubConds: []*interfaces.FilterCondCfg{nil, {}, constCfg("name", OperationEqual, "alice")}}
		cond, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
		require.NoError(t, err)
		require.Len(t, cond.(*AndCond).SubConds, 1)
		assert.Equal(t, OperationEqual, cond.(*AndCond).SubConds[0].GetOperation())
	})
	t.Run("and cond valid", func(t *testing.T) {
		cfg := &interfaces.FilterCondCfg{
			Operation: "and",
			SubConds: []*interfaces.FilterCondCfg{
				constCfg("name", "==", "alice"),
				constCfg("age", ">", 18),
			},
		}
		cond, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		and := cond.(*AndCond)
		if len(and.SubConds) != 2 {
			t.Errorf("expected 2 sub-conditions, got %d", len(and.SubConds))
		}
	})
	t.Run("and cond empty sub conds", func(t *testing.T) {
		cfg := &interfaces.FilterCondCfg{
			Operation: "and",
			SubConds:  []*interfaces.FilterCondCfg{},
		}
		_, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err == nil {
			t.Fatal("expected error for empty sub-conditions")
		}
		if !strings.Contains(err.Error(), "size is 0") {
			t.Errorf("unexpected error: %v", err)
		}
	})
	t.Run("and cond invalid sub cond", func(t *testing.T) {
		cfg := &interfaces.FilterCondCfg{
			Operation: "and",
			SubConds: []*interfaces.FilterCondCfg{
				constCfg("nonexistent", "==", "test"),
			},
		}
		_, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err == nil {
			t.Fatal("expected error for invalid sub-condition")
		}
	})
	t.Run("and cond nested and or", func(t *testing.T) {
		cfg := &interfaces.FilterCondCfg{
			Operation: "and",
			SubConds: []*interfaces.FilterCondCfg{
				constCfg("name", "==", "alice"),
				{
					Operation: "or",
					SubConds: []*interfaces.FilterCondCfg{
						constCfg("age", ">", 18),
						constCfg("age", "<", 65),
					},
				},
			},
		}
		cond, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		and := cond.(*AndCond)
		if len(and.SubConds) != 2 {
			t.Errorf("expected 2 sub-conditions, got %d", len(and.SubConds))
		}
		if and.SubConds[1].GetOperation() != OperationOr {
			t.Errorf("expected nested 'or', got '%s'", and.SubConds[1].GetOperation())
		}
	})
}

func TestComparisonOps(t *testing.T) {
	t.Run("comparison ops empty field", func(t *testing.T) {
		ops := []string{"==", "!=", ">", ">=", "<", "<="}
		for _, op := range ops {
			cfg := constCfg("", op, "test")
			_, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
			if err == nil {
				t.Errorf("operation '%s': expected error for empty field name", op)
			}
		}
	})
	t.Run("comparison ops field not found", func(t *testing.T) {
		ops := []string{"==", "!=", ">", ">=", "<", "<=", "in", "not_in", "between", "out_range"}
		for _, op := range ops {
			t.Run(op, func(t *testing.T) {
				var value any = 1
				switch op {
				case "in", "not_in", "between", "out_range":
					value = []any{1, 2}
				}
				cfg := constCfg("nonexistent", op, value)
				condition, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
				require.ErrorContains(t, err, "not found in schema")
				assert.Nil(t, condition)
			})
		}
	})
}

func TestIsSlice(t *testing.T) {
	t.Run("returns true for slices and arrays", func(t *testing.T) {
		assert.True(t, IsSlice([]int{1, 2, 3}))
		assert.True(t, IsSlice([]string{"a"}))
		assert.True(t, IsSlice([2]int{1, 2}))
	})

	t.Run("returns false for scalar values", func(t *testing.T) {
		assert.False(t, IsSlice("not a slice"))
		assert.False(t, IsSlice(42))
	})
}

func TestIsSameType(t *testing.T) {
	t.Run("returns true for empty and same-type values", func(t *testing.T) {
		assert.True(t, IsSameType([]any{}))
		assert.True(t, IsSameType([]any{1, 2, 3}))
		assert.True(t, IsSameType([]any{"a", "b"}))
	})

	t.Run("returns false for mixed types", func(t *testing.T) {
		assert.False(t, IsSameType([]any{1, "two", 3}))
	})
}

func TestAllOperationsRegistered(t *testing.T) {
	t.Run("all operations registered", func(t *testing.T) {
		expectedOps := []string{
			"and", "or",
			"==", "eq", "!=", "not_eq",
			">", "gt", ">=", "gte", "<", "lt", "<=", "lte",
			"in", "not_in",
			"like", "not_like",
			"contain", "not_contain",
			"range", "out_range",
			"exist", "not_exist",
			"empty", "not_empty",
			"regex", "match", "match_phrase",
			"prefix", "not_prefix",
			"null", "not_null",
			"true", "false",
			"before", "current", "between",
			"knn_vector", "multi_match",
		}

		for _, op := range expectedOps {
			if _, exists := OperationMap[op]; !exists {
				t.Errorf("operation '%s' not registered in OperationMap", op)
			}
		}
	})
}

func TestNullCondValid(t *testing.T) {
	t.Run("null cond valid", func(t *testing.T) {
		cfg := &interfaces.FilterCondCfg{
			Field:     "name",
			Operation: "null",
		}
		cond, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cond.GetOperation() != OperationNull {
			t.Errorf("expected operation 'null', got '%s'", cond.GetOperation())
		}
		if cond.NeedValue() {
			t.Error("null condition should not need value")
		}
	})
}

func TestBooleanPredicateFieldTypes(t *testing.T) {
	fields := map[string]*interfaces.Property{
		"native_boolean":     {Name: "native_boolean", Type: interfaces.DataType_Boolean},
		"oracle_flag":        {Name: "oracle_flag", Type: interfaces.DataType_Decimal, OriginalType: "number"},
		"mariadb_flag":       {Name: "mariadb_flag", Type: interfaces.DataType_Integer, OriginalType: "tinyint(1)"},
		"number_two":         {Name: "number_two", Type: interfaces.DataType_Decimal, OriginalType: "number"},
		"fractional_number":  {Name: "fractional_number", Type: interfaces.DataType_Decimal, OriginalType: "number"},
		"unqualified_number": {Name: "unqualified_number", Type: interfaces.DataType_Decimal, OriginalType: "number"},
		"integer":            {Name: "integer", Type: interfaces.DataType_Integer, OriginalType: "int"},
		"tinyint_two":        {Name: "tinyint_two", Type: interfaces.DataType_Integer, OriginalType: "tinyint(2)"},
		"unsigned_integer":   {Name: "unsigned_integer", Type: interfaces.DataType_UnsignedInteger},
		"float":              {Name: "float", Type: interfaces.DataType_Float},
		"name":               {Name: "name", Type: interfaces.DataType_String},
	}

	for _, operation := range []string{OperationTrue, OperationFalse} {
		t.Run(operation, func(t *testing.T) {
			for _, field := range []string{"native_boolean", "oracle_flag", "mariadb_flag", "number_two", "fractional_number", "unqualified_number", "integer", "tinyint_two", "unsigned_integer"} {
				condition, err := NewFilterCondition(context.Background(), &interfaces.FilterCondCfg{Field: field, Operation: operation}, fields)
				require.NoError(t, err)
				require.NotNil(t, condition)
			}
			for _, field := range []string{"float", "name"} {
				condition, err := NewFilterCondition(context.Background(), &interfaces.FilterCondCfg{Field: field, Operation: operation}, fields)
				require.ErrorContains(t, err, "not a boolean or supported numeric field")
				assert.Nil(t, condition)
			}
		})
	}
}

func TestConditionMetadata(t *testing.T) {
	tests := []struct {
		name             string
		condition        interfaces.FilterCondition
		operation        string
		supportSubCond   bool
		needName         bool
		needValue        bool
		needConstValue   bool
		singleValue      bool
		fixedArrayValue  bool
		requiredValueLen int
	}{
		{name: "and", condition: &AndCond{}, operation: OperationAnd, supportSubCond: true, requiredValueLen: -1},
		{name: "or", condition: &OrCond{}, operation: OperationOr, supportSubCond: true, requiredValueLen: -1},
		{name: "equal", condition: &EqualCond{}, operation: OperationEqual, needName: true, needValue: true, singleValue: true, requiredValueLen: -1},
		{name: "not equal", condition: &NotEqualCond{}, operation: OperationNotEqual, needName: true, needValue: true, singleValue: true, requiredValueLen: -1},
		{name: "gt", condition: &GtCond{}, operation: OperationGt, needName: true, needValue: true, singleValue: true, requiredValueLen: -1},
		{name: "gte", condition: &GteCond{}, operation: OperationGte, needName: true, needValue: true, singleValue: true, requiredValueLen: -1},
		{name: "lt", condition: &LtCond{}, operation: OperationLt, needName: true, needValue: true, singleValue: true, requiredValueLen: -1},
		{name: "lte", condition: &LteCond{}, operation: OperationLte, needName: true, needValue: true, singleValue: true, requiredValueLen: -1},
		{name: "in", condition: &InCond{}, operation: OperationIn, needName: true, needValue: true, needConstValue: true, requiredValueLen: -1},
		{name: "not in", condition: &NotInCond{}, operation: OperationNotIn, needName: true, needValue: true, needConstValue: true, requiredValueLen: -1},
		{name: "like", condition: &LikeCond{}, operation: OperationLike, needName: true, needValue: true, needConstValue: true, singleValue: true, requiredValueLen: -1},
		{name: "not like", condition: &NotLikeCond{}, operation: OperationNotLike, needName: true, needValue: true, needConstValue: true, singleValue: true, requiredValueLen: -1},
		{name: "contain", condition: &ContainCond{}, operation: OperationContain, needName: true, needValue: true, needConstValue: true, requiredValueLen: -1},
		{name: "not contain", condition: &NotContainCond{}, operation: OperationNotContain, needName: true, needValue: true, needConstValue: true, requiredValueLen: -1},
		{name: "range", condition: &RangeCond{}, operation: OperationRange, needName: true, needValue: true, needConstValue: true, fixedArrayValue: true, requiredValueLen: 2},
		{name: "out range", condition: &OutRangeCond{}, operation: OperationOutRange, needName: true, needValue: true, needConstValue: true, fixedArrayValue: true, requiredValueLen: 2},
		{name: "exist", condition: &ExistCond{}, operation: OperationExist, needName: true, requiredValueLen: -1},
		{name: "not exist", condition: &NotExistCond{}, operation: OperationNotExist, needName: true, requiredValueLen: -1},
		{name: "empty", condition: &EmptyCond{}, operation: OperationEmpty, needName: true, requiredValueLen: -1},
		{name: "not empty", condition: &NotEmptyCond{}, operation: OperationNotEmpty, needName: true, requiredValueLen: -1},
		{name: "regex", condition: &RegexCond{}, operation: OperationRegex, needName: true, needValue: true, needConstValue: true, singleValue: true, requiredValueLen: -1},
		{name: "match", condition: &MatchCond{}, operation: OperationMatch, needValue: true, needConstValue: true, singleValue: true, requiredValueLen: -1},
		{name: "match phrase", condition: &MatchPhraseCond{}, operation: OperationMatchPhrase, needValue: true, needConstValue: true, singleValue: true, requiredValueLen: -1},
		{name: "prefix", condition: &PrefixCond{}, operation: OperationPrefix, needName: true, needValue: true, needConstValue: true, singleValue: true, requiredValueLen: -1},
		{name: "not prefix", condition: &NotPrefixCond{}, operation: OperationNotPrefix, needName: true, needValue: true, needConstValue: true, singleValue: true, requiredValueLen: -1},
		{name: "null", condition: &NullCond{}, operation: OperationNull, needName: true, requiredValueLen: -1},
		{name: "not null", condition: &NotNullCond{}, operation: OperationNotNull, needName: true, requiredValueLen: -1},
		{name: "true", condition: &TrueCond{}, operation: OperationTrue, needName: true, requiredValueLen: -1},
		{name: "false", condition: &FalseCond{}, operation: OperationFalse, needName: true, requiredValueLen: -1},
		{name: "before", condition: &BeforeCond{}, operation: OperationBefore, needName: true, needValue: true, needConstValue: true, fixedArrayValue: true, requiredValueLen: 2},
		{name: "current", condition: &CurrentCond{}, operation: OperationCurrent, needName: true, needValue: true, needConstValue: true, singleValue: true, requiredValueLen: 1},
		{name: "between", condition: &BetweenCond{}, operation: OperationBetween, needName: true, needValue: true, needConstValue: true, fixedArrayValue: true, requiredValueLen: 2},
		{name: "knn vector", condition: &KnnVectorCond{}, operation: OperationKnnVector, supportSubCond: true, needName: true, needValue: true, needConstValue: true, requiredValueLen: -1},
		{name: "multi match", condition: &MultiMatchCond{}, operation: OperationMultiMatch, needValue: true, needConstValue: true, singleValue: true, requiredValueLen: -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.operation, tt.condition.GetOperation())
			assert.Equal(t, tt.supportSubCond, tt.condition.SupportSubCond())
			assert.Equal(t, tt.needName, tt.condition.NeedName())
			assert.Equal(t, tt.needValue, tt.condition.NeedValue())
			assert.Equal(t, tt.needConstValue, tt.condition.NeedConstValue())
			assert.Equal(t, tt.singleValue, tt.condition.IsSingleValue())
			assert.Equal(t, tt.fixedArrayValue, tt.condition.IsFixedLenArrayValue())
			assert.Equal(t, tt.requiredValueLen, tt.condition.RequiredValueLen())
		})
	}
}

func advancedFieldsMap() map[string]*interfaces.Property {
	fields := testFieldsMap()
	fields["embedding"] = &interfaces.Property{Name: "embedding", Type: interfaces.DataType_Vector}
	return fields
}

func TestConditionConstructorsRejectNilValues(t *testing.T) {
	fields := testFieldsMap()
	for _, operation := range []string{OperationEqual, OperationNotEqual, OperationGt, OperationGte, OperationLt, OperationLte} {
		t.Run(operation, func(t *testing.T) {
			cfg := &interfaces.FilterCondCfg{Field: "age", Operation: operation, ValueFrom: interfaces.ValueFrom_Const}
			condition, err := OperationMap[operation].New(context.Background(), cfg, fields)
			require.ErrorContains(t, err, "must not be nil")
			assert.Nil(t, condition)
		})
	}
	for _, operation := range []string{OperationRange, OperationOutRange, OperationBetween} {
		for _, values := range [][]any{{nil, 2}, {1, nil}} {
			t.Run(operation, func(t *testing.T) {
				cfg := &interfaces.FilterCondCfg{Field: "age", Operation: operation, ValueFrom: interfaces.ValueFrom_Const, Value: values}
				condition, err := OperationMap[operation].New(context.Background(), cfg, fields)
				require.ErrorContains(t, err, "must not be nil")
				assert.Nil(t, condition)
			})
		}
	}
}

func TestConstantOnlyConditionConstructorsRejectFieldSources(t *testing.T) {
	for operation, prototype := range OperationMap {
		if !prototype.NeedConstValue() {
			continue
		}
		t.Run(operation, func(t *testing.T) {
			cfg := constCfg("name", operation, "other_name")
			cfg.ValueFrom = interfaces.ValueFrom_Field
			cfg.RemainCfg = map[string]any{"fields": []any{"name"}}
			fields := testFieldsMap()
			switch operation {
			case OperationRange, OperationOutRange, OperationBetween, OperationBefore, OperationCurrent:
				cfg.Field = "created_at"
			case OperationKnnVector:
				fields["name"].Type = interfaces.DataType_Vector
			}
			condition, err := prototype.New(context.Background(), cfg, fields)
			require.ErrorContains(t, err, "value_from")
			assert.Nil(t, condition)
		})
	}
}

func TestNewFilterConditionValidatesValueFrom(t *testing.T) {
	for _, operation := range []string{OperationEqual, OperationNull, OperationAnd} {
		t.Run(operation+" rejects unknown source", func(t *testing.T) {
			cfg := constCfg("name", operation, "value")
			cfg.ValueFrom = "unknown"
			condition, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
			require.ErrorContains(t, err, "value_from")
			assert.Nil(t, condition)
		})
	}
	t.Run("rejects nested unknown source", func(t *testing.T) {
		child := constCfg("name", OperationEqual, "value")
		child.ValueFrom = "unknown"
		cfg := &interfaces.FilterCondCfg{Operation: OperationAnd, SubConds: []*interfaces.FilterCondCfg{child}}
		condition, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
		require.ErrorContains(t, err, "value_from")
		assert.Nil(t, condition)
	})
	t.Run("defaults to constant", func(t *testing.T) {
		cfg := constCfg("name", OperationEqual, "value")
		cfg.ValueFrom = ""
		condition, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
		require.NoError(t, err)
		assert.Equal(t, interfaces.ValueFrom_Const, cfg.ValueFrom)
		assert.IsType(t, &EqualCond{}, condition)
	})
	t.Run("supports field comparison", func(t *testing.T) {
		cfg := constCfg("name", OperationEqual, "other_name")
		cfg.ValueFrom = interfaces.ValueFrom_Field
		condition, err := NewFilterCondition(context.Background(), cfg, testFieldsMap())
		require.NoError(t, err)
		assert.Equal(t, testFieldsMap()["other_name"], condition.(*EqualCond).Rfield)
	})
}
