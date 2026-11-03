// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package local_index

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

func TestDecimalIndexConfig(t *testing.T) {
	property := &interfaces.Property{Name: "amount", Type: interfaces.DataType_Decimal, OriginalType: "decimal(12,2)"}
	for _, tc := range []struct {
		name       string
		column     interfaces.TableColumnMeta
		attributes map[string]any
		p, s       int
		failure    bool
	}{
		{name: "native definition", p: 12, s: 2},
		{name: "authoritative discovered scale zero", column: interfaces.TableColumnMeta{NumPrecision: 14}, p: 14},
		{name: "explicit dataset", attributes: map[string]any{"precision": json.Number("10"), "scale": json.Number("4")}, p: 10, s: 4},
		{name: "large declared precision", attributes: map[string]any{"precision": 10000, "scale": 2}, p: 10000, s: 2},
		{name: "fractional precision", attributes: map[string]any{"precision": 10.5, "scale": 2}, failure: true},
		{name: "missing scale", attributes: map[string]any{"precision": 10}, failure: true},
		{name: "invalid scale", attributes: map[string]any{"precision": 10, "scale": 11}, failure: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copy := *property
			copy.Attributes = tc.attributes
			config, err := DecimalIndexConfig(&copy, tc.column)
			if tc.failure {
				require.Error(t, err)
				assert.Nil(t, config)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, &interfaces.DecimalIndexConfig{Precision: tc.p, Scale: tc.s}, config)
		})
	}
}

func TestDecimalIndexSchema(t *testing.T) {
	property := &interfaces.Property{Name: "amount", OriginalName: "source_amount", OriginalType: "numeric", Type: interfaces.DataType_Decimal, Attributes: map[string]any{"comment": "source", "precision": 18, "scale": 4}}
	input := []*interfaces.Property{property}
	attributes := property.Attributes
	columns := map[string]interfaces.TableColumnMeta{"source_amount": {Name: "source_amount", NumPrecision: 12, NumScale: 2}}
	schema, err := DecimalIndexSchema(input, columns)
	require.NoError(t, err)
	assert.Same(t, property, input[0])
	assert.Same(t, property, schema[0])
	assert.Same(t, &input[0], &schema[0])
	assert.Equal(t, map[string]any{"comment": "source", "precision": 12, "scale": 2}, property.Attributes)
	assert.Equal(t, property.Attributes, attributes)
	assert.Equal(t, 12, schema[0].Attributes["precision"])
	assert.Equal(t, 2, schema[0].Attributes["scale"])
	assert.Equal(t, "source", schema[0].Attributes["comment"])
	assert.Equal(t, interfaces.TableColumnMeta{Name: "source_amount", NumPrecision: 12, NumScale: 2}, columns["source_amount"])
	mapping, err := buildFieldMappings(schema)
	require.NoError(t, err)
	assert.Equal(t, 100.0, mapping["amount"].(map[string]any)["scaling_factor"])
	t.Run("nil source metadata uses field declarations", func(t *testing.T) {
		for _, property := range []*interfaces.Property{
			{Name: "amount", Type: interfaces.DataType_Decimal, Attributes: map[string]any{"precision": 12, "scale": 2}},
			{Name: "amount", Type: interfaces.DataType_Decimal, OriginalType: "decimal(12,2)"},
		} {
			schema, err := DecimalIndexSchema([]*interfaces.Property{nil, property}, nil)
			require.NoError(t, err)
			assert.Nil(t, schema[0])
			assert.Same(t, property, schema[1])
			assert.Equal(t, 12, schema[1].Attributes["precision"])
			assert.Equal(t, 2, schema[1].Attributes["scale"])
		}
	})
	t.Run("missing decimal declaration fails", func(t *testing.T) {
		schema, err := DecimalIndexSchema([]*interfaces.Property{{Name: "amount", Type: interfaces.DataType_Decimal}}, nil)
		var failure *interfaces.DecimalIndexError
		require.ErrorAs(t, err, &failure)
		assert.Equal(t, "amount", failure.Field)
		assert.Equal(t, "missing_precision_scale", failure.Reason)
		assert.Nil(t, schema)
	})
	t.Run("empty schema", func(t *testing.T) {
		schema, err := DecimalIndexSchema(nil, nil)
		require.NoError(t, err)
		assert.Empty(t, schema)
	})
}

func TestValidateDecimalIndexConfig(t *testing.T) {
	for _, tc := range []struct {
		name             string
		precision, scale int
		reason           string
	}{
		{name: "wide cents", precision: 18, scale: 2},
		{name: "wide fractional declaration", precision: 38, scale: 18},
		{name: "large precision", precision: 10000, scale: 2},
		{name: "largest finite scaling factor", precision: 1000, scale: 308},
		{name: "missing precision", reason: "missing_precision_scale"},
		{name: "invalid scale", precision: 12, scale: 13, reason: "invalid_precision_scale"},
		{name: "negative scale", precision: 38, scale: -1, reason: "invalid_precision_scale"},
		{name: "non finite scaling factor", precision: 1000, scale: 309, reason: "unsupported_scaling_factor"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateDecimalIndexConfig("amount", &interfaces.DecimalIndexConfig{Precision: tc.precision, Scale: tc.scale})
			if tc.reason == "" {
				require.NoError(t, err)
				return
			}
			var failure *interfaces.DecimalIndexError
			require.ErrorAs(t, err, &failure)
			assert.Equal(t, tc.reason, failure.Reason)
		})
	}
}

func TestValidateDecimalDocument(t *testing.T) {
	resource := &interfaces.Resource{SchemaDefinition: []*interfaces.Property{{Name: "amount", Type: interfaces.DataType_Decimal, OriginalType: "decimal(12,2)"}}}
	for _, tc := range []struct {
		name     string
		value    any
		valid    bool
		original string
	}{
		{name: "issue amount", value: "13052.52", valid: true},
		{name: "issue maximum", value: "230134.15", valid: true},
		{name: "zero", value: "0.00", valid: true},
		{name: "null", value: nil, valid: true},
		{name: "nine", value: 9, valid: true},
		{name: "ten", value: 10, valid: true},
		{name: "negative", value: json.Number("-123456.78"), valid: true},
		{name: "trailing zero", value: "1.2300", valid: true},
		{name: "maximum", value: "9999999999.99", valid: true},
		{name: "minimum", value: "-9999999999.99", valid: true},
		{name: "float input", value: 0.1, valid: true},
		{name: "scientific notation", value: json.Number("123e-2"), valid: true},
		{name: "multiple values", value: []any{"1.25", "1.25", nil, "-3.50"}, valid: true},
		{name: "overflow", value: "10000000000.00"},
		{name: "negative overflow", value: "-10000000000.00"},
		{name: "fractional overflow", value: "1.234"},
		{name: "one invalid array item", value: []any{"1.25", "1.234"}},
		{name: "invalid value", value: true},
		{name: "unbounded exponent", value: json.Number("1e1000000000")},
		{name: "non finite", value: "NaN"},
		{name: "wide definition with normal amount", original: "decimal(18,2)", value: "100.00", valid: true},
		{name: "wide definition with representable fraction", original: "decimal(38,18)", value: "1.25", valid: true},
		{name: "actual scaled long overflow", original: "decimal(38,18)", value: "13052.52"},
		{name: "positive long overflow", original: "decimal(38,0)", value: "9223372036854775808"},
		{name: "negative long overflow", original: "decimal(38,0)", value: "-9223372036854775809"},
		{name: "long boundary rounds into saturation", original: "decimal(38,0)", value: "9223372036854775807"},
		{name: "exact large integer", original: "decimal(38,0)", value: "9007199254740992", valid: true},
		{name: "exact integer beyond safe integer interval", original: "decimal(38,0)", value: "9007199254740994", valid: true},
		{name: "inexact neighboring integer", original: "decimal(38,0)", value: "9007199254740993"},
		{name: "negative exact large integer", original: "decimal(38,0)", value: "-9007199254740992", valid: true},
		{name: "fraction loses a significant digit", original: "decimal(38,18)", value: "0.123456789012345678"},
		{name: "fraction rounds onto another value", original: "decimal(38,18)", value: "1.000000000000000001"},
		{name: "small value at high scale", original: "decimal(1000,308)", value: "1e-308", valid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			document := map[string]any{"amount": tc.value}
			current := resource
			if tc.original != "" {
				current = &interfaces.Resource{SchemaDefinition: []*interfaces.Property{{Name: "amount", Type: interfaces.DataType_Decimal, OriginalType: tc.original}}}
			}
			err := ValidateDecimalDocument(current, document)
			if tc.valid {
				require.NoError(t, err)
			} else {
				var failure *interfaces.DecimalIndexError
				require.ErrorAs(t, err, &failure)
				assert.Equal(t, "amount", failure.Field)
			}
			assert.Equal(t, tc.value, document["amount"])
		})
	}
	assert.NoError(t, ValidateDecimalDocument(resource, map[string]any{"other": 1}))
}

func TestDecimalAggregateConfig(t *testing.T) {
	resource := &interfaces.Resource{SchemaDefinition: []*interfaces.Property{{Name: "amount", OriginalName: "AMOUNT", Type: interfaces.DataType_Decimal, Attributes: map[string]any{"precision": 18, "scale": 4}}}, SourceMetadata: map[string]any{"columns": []interfaces.TableColumnMeta{{Name: "AMOUNT", NumPrecision: 12, NumScale: 2}}}}
	for _, operation := range []string{"sum", "min", "max", "avg", "count", "count_distinct"} {
		t.Run(operation, func(t *testing.T) {
			config, err := decimalAggregateConfig(resource, &interfaces.ResourceDataQueryParams{Aggregation: &interfaces.Aggregation{Property: "amount", Aggr: operation}})
			require.NoError(t, err)
			if operation == "sum" || operation == "min" || operation == "max" {
				assert.Equal(t, interfaces.DecimalIndexConfig{Precision: 12, Scale: 2}, config)
			} else {
				assert.Zero(t, config)
			}
		})
	}
	config, err := decimalAggregateConfig(resource, nil)
	require.NoError(t, err)
	assert.Zero(t, config)
	_, err = decimalAggregateConfig(&interfaces.Resource{SchemaDefinition: []*interfaces.Property{{Name: "amount", Type: interfaces.DataType_Decimal}}}, &interfaces.ResourceDataQueryParams{Aggregation: &interfaces.Aggregation{Property: "amount", Aggr: "sum"}})
	var failure *interfaces.DecimalIndexError
	require.ErrorAs(t, err, &failure)
	assert.Equal(t, "missing_precision_scale", failure.Reason)
}

func TestNormalizeDecimalAggregate(t *testing.T) {
	resource := &interfaces.Resource{SchemaDefinition: []*interfaces.Property{{Name: "amount", Type: interfaces.DataType_Decimal, OriginalType: "decimal(12,2)"}}}
	for _, operation := range []string{"sum", "min", "max", "avg"} {
		t.Run(operation, func(t *testing.T) {
			params := &interfaces.ResourceDataQueryParams{Aggregation: &interfaces.Aggregation{Property: "amount", Aggr: operation, Alias: "value"}}
			value := json.Number("119751.35999999999")
			result := &interfaces.ResourceDataQueryResult{Entries: []map[string]any{{"value": value}, {"value": nil}}, IsApproximate: false}
			config, err := decimalAggregateConfig(resource, params)
			require.NoError(t, err)
			require.NoError(t, normalizeDecimalAggregate(config, params, result))
			want := json.Number("119751.36")
			if operation == "avg" {
				want = value
			}
			assert.Equal(t, want, result.Entries[0]["value"])
			assert.Nil(t, result.Entries[1]["value"])
			assert.False(t, result.IsApproximate)
		})
	}
}
