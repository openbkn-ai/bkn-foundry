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
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/connector/local/table"
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
		{name: "discovered negative scale", column: interfaces.TableColumnMeta{NumPrecision: 2, NumScale: -3}, p: 2, s: -3},
		{name: "authoritative discovered scale zero", column: interfaces.TableColumnMeta{NumPrecision: 14}, p: 14},
		{name: "explicit dataset", attributes: map[string]any{"precision": json.Number("10"), "scale": json.Number("4")}, p: 10, s: 4},
		{name: "large declared precision", attributes: map[string]any{"precision": 10000, "scale": 2}, p: 10000, s: 2},
		{name: "fractional precision", attributes: map[string]any{"precision": 10.5, "scale": 2}, failure: true},
		{name: "missing scale", attributes: map[string]any{"precision": 10}, failure: true},
		{name: "scale exceeds precision", attributes: map[string]any{"precision": 3, "scale": 5}, p: 3, s: 5},
		{name: "invalid precision", attributes: map[string]any{"precision": 0, "scale": 11}, failure: true},
		{name: "source scale exceeds precision", column: interfaces.TableColumnMeta{NumPrecision: 3, NumScale: 5}, p: 3, s: 5},
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
			expected := &interfaces.DecimalIndexConfig{Precision: tc.p, Scale: tc.s}
			assert.Equal(t, expected, config)
		})
	}
}

func TestDecimalIndexSchema(t *testing.T) {
	t.Run("money uses discovered scale for index mapping and exact value checks", func(t *testing.T) {
		for _, tc := range []struct {
			scale   int
			value   any
			want    any
			storage string
		}{
			{scale: 0, value: "9223372036854775807", want: int64(9223372036854775807), storage: "long"},
			{scale: 2, value: "123.45", want: "123.45", storage: "scaled_float"},
			{scale: 3, value: "-0.001", want: "-0.001", storage: "scaled_float"},
		} {
			property := &interfaces.Property{Name: "amount", OriginalName: "source_amount", OriginalType: "money", Type: interfaces.DataType_Decimal}
			columns := []interfaces.TableColumnMeta{{Name: "source_amount", Type: "money", NumPrecision: 19, NumScale: tc.scale}}
			resource := &interfaces.Resource{SchemaDefinition: []*interfaces.Property{property}, SourceMetadata: map[string]any{"columns": columns}}
			metadata, err := table.ColumnMetadata(resource)
			require.NoError(t, err)
			schema, err := DecimalIndexSchema(resource.SchemaDefinition, metadata)
			require.NoError(t, err)
			mapping, err := buildFieldMappings(schema, true)
			require.NoError(t, err)
			assert.Equal(t, tc.storage, mapping["amount"].(map[string]any)["type"])
			doc := map[string]any{"amount": tc.value}
			encoded, err := EncodeDecimalDocument(resource, doc)
			require.NoError(t, err)
			assert.Equal(t, tc.want, encoded["amount"])
			assert.Equal(t, tc.value, doc["amount"])
			if tc.scale == 2 {
				encoded, err = EncodeDecimalDocument(resource, map[string]any{"amount": "92233720368547758.07"})
				var failure *interfaces.DecimalIndexError
				require.ErrorAs(t, err, &failure)
				assert.Nil(t, encoded)
				assert.Equal(t, "amount", failure.Field)
			}
		}
	})

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
	mapping, err := buildFieldMappings(schema, true)
	require.NoError(t, err)
	assert.Equal(t, 100.0, mapping["amount"].(map[string]any)["scaling_factor"])
	t.Run("nil source metadata uses field declarations", func(t *testing.T) {
		for _, property := range []*interfaces.Property{
			{Name: "amount", Type: interfaces.DataType_Decimal, Attributes: map[string]any{"precision": 12, "scale": 2}},
			{Name: "amount", Type: interfaces.DataType_Decimal, OriginalType: "decimal(12,2)"},
		} {
			schema, err := DecimalIndexSchema([]*interfaces.Property{property}, nil)
			require.NoError(t, err)
			assert.Same(t, property, schema[0])
			assert.Equal(t, 12, schema[0].Attributes["precision"])
			assert.Equal(t, 2, schema[0].Attributes["scale"])
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
		{name: "scale exceeds precision", precision: 3, scale: 5},
		{name: "invalid precision", precision: -1, scale: 5, reason: "invalid_precision_scale"},
		{name: "small precision at finite factor limit", precision: 3, scale: 308},
		{name: "small precision with non finite factor", precision: 3, scale: 309, reason: "unsupported_scaling_factor"},
		{name: "negative scale", precision: 38, scale: -1},
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
		{name: "scale exceeds precision", original: "numeric(3,5)", value: "0.00123", valid: true},
		{name: "small positive maximum", original: "numeric(3,5)", value: "0.00999", valid: true},
		{name: "small negative minimum", original: "numeric(3,5)", value: "-0.00999", valid: true},
		{name: "small decimal zero", original: "numeric(3,5)", value: "0", valid: true},
		{name: "small decimal null", original: "numeric(3,5)", value: nil, valid: true},
		{name: "small decimal trailing zeroes", original: "numeric(3,5)", value: "0.0012300", valid: true},
		{name: "small decimal precision overflow", original: "numeric(3,5)", value: "0.01234"},
		{name: "small decimal scale overflow", original: "numeric(3,5)", value: "0.001234"},
		{name: "small decimal unsafe double", original: "numeric(18,20)", value: "0.00123456789012345678"},
		{name: "small decimal at finite factor limit", original: "numeric(3,308)", value: "1e-308", valid: true},
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
		{name: "long maximum", original: "decimal(38,0)", value: "9223372036854775807", valid: true},
		{name: "long minimum", original: "decimal(38,0)", value: "-9223372036854775808", valid: true},
		{name: "negative scale integer", original: "numeric(2,-3)", value: "12000", valid: true},
		{name: "negative scale must retain source rounding", original: "numeric(2,-3)", value: "12345"},
		{name: "negative scale precision overflow", original: "numeric(2,-3)", value: "100000"},
		{name: "exact large integer", original: "decimal(38,0)", value: "9007199254740992", valid: true},
		{name: "exact integer beyond safe integer interval", original: "decimal(38,0)", value: "9007199254740994", valid: true},
		{name: "exact integer without double", original: "decimal(38,0)", value: "9007199254740993", valid: true},
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
	t.Run("only scale-sensitive decimal aggregation parses source columns", func(t *testing.T) {
		resource := &interfaces.Resource{SourceMetadata: map[string]any{"columns": "invalid metadata"}, SchemaDefinition: []*interfaces.Property{{Name: "amount", Type: interfaces.DataType_Decimal, OriginalType: "decimal(12,2)"}}}
		for _, operation := range []string{"sum", "min", "max"} {
			config, err := decimalAggregateConfig(resource, &interfaces.ResourceDataQueryParams{Aggregation: &interfaces.Aggregation{Property: "amount", Aggr: operation}})
			require.Error(t, err)
			assert.Zero(t, config)
			var decimalError *interfaces.DecimalIndexError
			assert.NotErrorAs(t, err, &decimalError)
		}
		for _, operation := range []string{"avg", "count"} {
			config, err := decimalAggregateConfig(resource, &interfaces.ResourceDataQueryParams{Aggregation: &interfaces.Aggregation{Property: "amount", Aggr: operation}})
			require.NoError(t, err)
			assert.Zero(t, config)
		}
	})
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

func TestEncodeDecimalDocument(t *testing.T) {
	t.Run("corrupt source columns cannot be ignored in favor of a native declaration", func(t *testing.T) {
		resource := &interfaces.Resource{SourceMetadata: map[string]any{"columns": "invalid metadata"}, SchemaDefinition: []*interfaces.Property{{Name: "amount", Type: interfaces.DataType_Decimal, OriginalType: "decimal(12,2)"}}}
		document := map[string]any{"amount": "12.34", "label": "unchanged"}
		encoded, err := EncodeDecimalDocument(resource, document)
		require.Error(t, err)
		assert.Nil(t, encoded)
		var decimalError *interfaces.DecimalIndexError
		assert.NotErrorAs(t, err, &decimalError)
		assert.Equal(t, map[string]any{"amount": "12.34", "label": "unchanged"}, document)
	})
	for _, tc := range []struct {
		name, original  string
		value, expected any
		failure         bool
	}{
		{name: "maximum", original: "decimal(38,0)", value: "9223372036854775807", expected: int64(9223372036854775807)},
		{name: "minimum", original: "decimal(38,0)", value: json.Number("-9223372036854775808"), expected: int64(-9223372036854775808)},
		{name: "integer above double precision", original: "decimal(38,0)", value: "9007199254740993", expected: int64(9007199254740993)},
		{name: "negative scale", original: "numeric(2,-3)", value: "12000.00", expected: int64(12000)},
		{name: "array", original: "number(10,0)", value: []any{"12.00", nil, json.Number("-5")}, expected: []any{int64(12), nil, int64(-5)}},
		{name: "fraction", original: "decimal(10,0)", value: "12.5", failure: true},
		{name: "float already lacks exact precision", original: "decimal(38,0)", value: float64(9007199254740994), failure: true},
		{name: "overflow", original: "decimal(38,0)", value: "9223372036854775808", failure: true},
		{name: "positive scale preserves text", original: "decimal(12,2)", value: "19.99", expected: "19.99"},
		{name: "large negative scale zero", original: "numeric(2,-1000)", value: "0", expected: int64(0)},
		{name: "large negative scale nonzero", original: "numeric(2,-1000)", value: "1", failure: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resource := &interfaces.Resource{SchemaDefinition: []*interfaces.Property{{Name: "amount", Type: interfaces.DataType_Decimal, OriginalType: tc.original}}}
			document := map[string]any{"amount": tc.value, "label": "unchanged"}
			encoded, err := EncodeDecimalDocument(resource, document)
			if tc.failure {
				var failure *interfaces.DecimalIndexError
				require.ErrorAs(t, err, &failure)
				assert.Equal(t, "amount", failure.Field)
				assert.Nil(t, encoded)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.expected, encoded["amount"])
				assert.Equal(t, "unchanged", encoded["label"])
			}
			assert.Equal(t, tc.value, document["amount"])
		})
	}
}
