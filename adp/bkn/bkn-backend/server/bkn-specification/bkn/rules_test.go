// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func rprop(name, typ string, mutate ...func(*DataProperty)) *DataProperty {
	p := &DataProperty{Name: name, Type: typ}
	for _, m := range mutate {
		m(p)
	}
	return p
}

func withConstraints(c PropertyConstraints) func(*DataProperty) {
	return func(p *DataProperty) { p.Constraints = &c }
}

func withSemantic(s string) func(*DataProperty) { return func(p *DataProperty) { p.Semantic = s } }

func objectWith(props ...*DataProperty) func() error {
	return func() error {
		return ValidateObjectTypeDeclarations(&BknObjectType{
			BknObjectTypeFrontmatter: BknObjectTypeFrontmatter{ID: "ot", Name: "ot"}, DataProperties: props})
	}
}

func objectAliases(aliases ...string) func() error {
	return func() error {
		return ValidateObjectTypeDeclarations(&BknObjectType{
			BknObjectTypeFrontmatter: BknObjectTypeFrontmatter{ID: "ot", Name: "ot", Aliases: aliases}})
	}
}

func objectLifecycle(lc *Lifecycle) func() error {
	return func() error {
		return ValidateObjectTypeDeclarations(&BknObjectType{
			BknObjectTypeFrontmatter: BknObjectTypeFrontmatter{ID: "ot", Name: "ot", Lifecycle: lc}})
	}
}

func relation(mutate func(*BknRelationType)) func() error {
	return func() error {
		rt := &BknRelationType{
			BknRelationTypeFrontmatter: BknRelationTypeFrontmatter{ID: "rt", Name: "rt"},
			Endpoint:                   Endpoint{Source: "a", Target: "b", Type: "direct"},
		}
		mutate(rt)
		return ValidateRelationTypeDeclarations(rt)
	}
}

func network(objects []*BknObjectType, relations []*BknRelationType, valueTypes []*BknValueType) func() error {
	return func() error {
		return ValidateNetworkDeclarations(&BknNetwork{ObjectTypes: objects, RelationTypes: relations, ValueTypes: valueTypes})
	}
}

func relationDef(id, source, target string, mutate ...func(*BknRelationType)) *BknRelationType {
	rt := &BknRelationType{
		BknRelationTypeFrontmatter: BknRelationTypeFrontmatter{ID: id, Name: id},
		Endpoint:                   Endpoint{Source: source, Target: target, Type: "direct"},
	}
	for _, m := range mutate {
		m(rt)
	}
	return rt
}

func objectDef(id string, lc *Lifecycle, aliases ...string) *BknObjectType {
	return &BknObjectType{BknObjectTypeFrontmatter: BknObjectTypeFrontmatter{ID: id, Name: id, Lifecycle: lc, Aliases: aliases}}
}

func secondaryFallback(primary string) func(*BknRelationType) {
	return func(rt *BknRelationType) { rt.Importance = ImportanceSecondary; rt.FallbackFor = primary }
}

func cardinality(c string) func(*BknRelationType) {
	return func(rt *BknRelationType) { rt.Endpoint.Cardinality = c }
}

var deprecatedLifecycle = &Lifecycle{Status: LifecycleDeprecated, Reason: "retired"}

// Every rule a declaration refusal reports carries its stable code, the property it concerns and the
// params its message is built from, so that a caller words it without reading the English text.
func TestDeclarationRulesCarryCodeAndParams(t *testing.T) {
	cases := []struct {
		rule     string
		check    func() error
		property string
		params   map[string]any
	}{
		{RuleSemanticUnknown, objectWith(rprop("a", "string", withSemantic("mood"))), "a",
			map[string]any{"semantic": "mood", "allowed": semanticOrder}},
		{RuleSemanticTypeMismatch, objectWith(rprop("a", "string", withSemantic(SemanticEventTime))), "a",
			map[string]any{"semantic": SemanticEventTime, "allowed": dateTypes, "type": "string"}},
		{RuleSemanticDuplicated, objectWith(rprop("a", "string", withSemantic(SemanticStatus)), rprop("b", "string", withSemantic(SemanticStatus))), "b",
			map[string]any{"semantic": SemanticStatus, "other_property": "a"}},
		{RuleEndTimeWithoutEventTime, objectWith(rprop("a", "datetime", withSemantic(SemanticEndTime))), "", nil},
		{RuleLatitudeLongitudeUnpaired, objectWith(rprop("a", "float", withSemantic(SemanticLatitude))), "", nil},
		{RuleLocationWithLatitudeLongitude, objectWith(rprop("p", "point", withSemantic(SemanticLocation)),
			rprop("a", "float", withSemantic(SemanticLatitude)), rprop("b", "float", withSemantic(SemanticLongitude))), "", nil},
		{RuleCRSMismatch, objectWith(
			rprop("a", "float", withSemantic(SemanticLatitude), withConstraints(PropertyConstraints{CRS: "wgs84"})),
			rprop("b", "float", withSemantic(SemanticLongitude), withConstraints(PropertyConstraints{CRS: "gcj02"}))), "",
			map[string]any{"latitude_crs": "wgs84", "longitude_crs": "gcj02"}},
		{RuleValueTypeWithValueRules, objectWith(rprop("a", "string", withConstraints(PropertyConstraints{ValueType: "vt", Unit: "kg"}))), "a",
			map[string]any{"value_type": "vt"}},
		{RuleUniqueTypeUnsupported, objectWith(rprop("a", "json", withConstraints(PropertyConstraints{Unique: true}))), "a",
			map[string]any{"type": "json"}},
		{RuleCRSWithoutGeoSemantic, objectWith(rprop("a", "string", withConstraints(PropertyConstraints{CRS: "wgs84"}))), "a", nil},
		{RuleCRSInvalid, objectWith(rprop("a", "float", withSemantic(SemanticLatitude), withConstraints(PropertyConstraints{CRS: "mars"})),
			rprop("b", "float", withSemantic(SemanticLongitude))), "a", map[string]any{"crs": "mars", "allowed": validCRS}},
		{RuleTimezoneTypeMismatch, objectWith(rprop("a", "string", withConstraints(PropertyConstraints{Timezone: "Asia/Shanghai"}))), "a",
			map[string]any{"type": "string"}},
		{RuleTimezoneInvalid, objectWith(rprop("a", "datetime", withConstraints(PropertyConstraints{Timezone: "Mars/Base"}))), "a",
			map[string]any{"timezone": "Mars/Base"}},
		{RuleRangeBoundsMissing, objectWith(rprop("a", "integer", withConstraints(PropertyConstraints{Range: &ValueRange{}}))), "a", nil},
		{RuleRangeNotNumeric, objectWith(rprop("a", "integer", withConstraints(PropertyConstraints{Range: &ValueRange{Min: "low"}}))), "a", nil},
		{RuleRangeMinGreaterThanMax, objectWith(rprop("a", "integer", withConstraints(PropertyConstraints{Range: &ValueRange{Min: int64(5), Max: int64(1)}}))), "a",
			map[string]any{"min": int64(5), "max": int64(1)}},
		{RuleRangeNotDate, objectWith(rprop("a", "date", withConstraints(PropertyConstraints{Range: &ValueRange{Min: "soon"}}))), "a",
			map[string]any{"type": "date"}},
		{RuleRangeNotTime, objectWith(rprop("a", "datetime", withConstraints(PropertyConstraints{Range: &ValueRange{Min: "soon"}}))), "a",
			map[string]any{"type": "datetime"}},
		{RuleRangeMinLaterThanMax, objectWith(rprop("a", "date", withConstraints(PropertyConstraints{Range: &ValueRange{Min: "2026-02-01", Max: "2026-01-01"}}))), "a",
			map[string]any{"min": "2026-02-01", "max": "2026-01-01"}},
		{RuleRangeTypeUnsupported, objectWith(rprop("a", "string", withConstraints(PropertyConstraints{Range: &ValueRange{Min: int64(1)}}))), "a",
			map[string]any{"type": "string"}},
		{RuleLengthBoundsMissing, objectWith(rprop("a", "string", withConstraints(PropertyConstraints{Length: &LengthRange{}}))), "a", nil},
		{RuleLengthBoundNegative, objectWith(rprop("a", "string", withConstraints(PropertyConstraints{Length: &LengthRange{Min: intPtr(-1)}}))), "a",
			map[string]any{"bound": "min"}},
		{RuleLengthTypeMismatch, objectWith(rprop("a", "integer", withConstraints(PropertyConstraints{Length: &LengthRange{Min: intPtr(1)}}))), "a",
			map[string]any{"type": "integer", "allowed": stringTypes}},
		{RuleLengthMinGreaterThanMax, objectWith(rprop("a", "string", withConstraints(PropertyConstraints{Length: &LengthRange{Min: intPtr(5), Max: intPtr(2)}}))), "a",
			map[string]any{"min": 5, "max": 2}},
		{RulePatternTypeMismatch, objectWith(rprop("a", "integer", withConstraints(PropertyConstraints{Pattern: "^a$"}))), "a",
			map[string]any{"type": "integer", "allowed": stringTypes}},
		{RulePatternInvalid, objectWith(rprop("a", "string", withConstraints(PropertyConstraints{Pattern: "("}))), "a",
			map[string]any{"pattern": "(", "error": "error parsing regexp: missing closing ): `(`"}},
		{RuleUnitTypeMismatch, objectWith(rprop("a", "string", withConstraints(PropertyConstraints{Unit: "kg"}))), "a",
			map[string]any{"type": "string"}},

		{RuleAliasesTooMany, objectAliases("a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k"), "",
			map[string]any{"count": 11, "max": maxAliases}},
		{RuleAliasEmpty, objectAliases(""), "", nil},
		{RuleAliasFullWidthComma, objectAliases("a，b"), "", map[string]any{"alias": "a，b"}},
		{RuleAliasContainsComma, objectAliases("a,b"), "", map[string]any{"alias": "a,b"}},
		{RuleAliasTooLong, objectAliases(strings.Repeat("a", maxAliasRunes+1)), "",
			map[string]any{"alias": strings.Repeat("a", maxAliasRunes+1), "max": maxAliasRunes}},
		{RuleAliasDuplicated, objectAliases("x", "x"), "", map[string]any{"alias": "x"}},
		{RuleAliasEqualsOwnName, objectAliases("ot"), "", map[string]any{"alias": "ot"}},
		{RuleAliasDuplicated, objectWith(rprop("a", "string", func(p *DataProperty) { p.Aliases = []string{"x", "x"} })), "a",
			map[string]any{"alias": "x"}},
		{RuleAliasConflict, objectWith(rprop("a", "string"), rprop("b", "string", func(p *DataProperty) { p.Aliases = []string{"a"} })), "b",
			map[string]any{"alias": "a", "conflicts_with_property": "a"}},

		{RuleLifecycleFieldsWithoutDeprecation, objectLifecycle(&Lifecycle{Status: LifecycleActive, Reason: "why"}), "", nil},
		{RuleLifecycleReasonMissing, objectLifecycle(&Lifecycle{Status: LifecycleDeprecated}), "", nil},
		{RuleLifecycleReplacedByInvalid, objectLifecycle(&Lifecycle{Status: LifecycleDeprecated, Reason: "r", ReplacedBy: "Not An Id"}), "",
			map[string]any{"replaced_by": "Not An Id"}},
		{RuleLifecycleReplacedBySelf, objectLifecycle(&Lifecycle{Status: LifecycleDeprecated, Reason: "r", ReplacedBy: "ot"}), "",
			map[string]any{"replaced_by": "ot"}},
		{RuleLifecycleStatusInvalid, objectLifecycle(&Lifecycle{Status: "gone"}), "",
			map[string]any{"status": "gone", "allowed": lifecycleStatuses}},

		{RuleReverseNameInvalid, relation(func(rt *BknRelationType) { rt.ReverseName = strings.Repeat("n", fieldNameMaxLength+1) }), "",
			map[string]any{"reverse_name": strings.Repeat("n", fieldNameMaxLength+1), "reason": "name length exceeds 40 characters"}},
		{RuleImportanceInvalid, relation(func(rt *BknRelationType) { rt.Importance = "tertiary" }), "",
			map[string]any{"importance": "tertiary", "allowed": importances}},
		{RuleVerificationInvalid, relation(func(rt *BknRelationType) { rt.Verification = "maybe" }), "",
			map[string]any{"verification": "maybe", "allowed": verifications}},
		{RuleCardinalityInvalid, relation(cardinality("some_to_some")), "",
			map[string]any{"cardinality": "some_to_some", "allowed": cardinalities}},
		{RuleCardinalityCrossJoin, relation(func(rt *BknRelationType) {
			rt.Endpoint.Type = RELATION_MAPPING_TYPE_FILTERED_CROSS_JOIN
			rt.Endpoint.Cardinality = CardinalityOneToOne
		}), "", map[string]any{"cardinality": CardinalityOneToOne}},
		{RuleListValueInvalid, relation(func(rt *BknRelationType) { rt.RequiredEnds = []string{"middle"} }), "",
			map[string]any{"field": "required", "value": "middle", "allowed": relationEnds}},
		{RuleListValueDuplicated, relation(func(rt *BknRelationType) { rt.RequiredEnds = []string{EndSource, EndSource} }), "",
			map[string]any{"field": "required", "value": EndSource}},
		{RuleCharacteristicsEndsDiffer, relation(func(rt *BknRelationType) { rt.Characteristics = []string{CharacteristicSymmetric} }), "",
			map[string]any{"source": "a", "target": "b"}},
		{RuleSymmetricCardinality, relation(func(rt *BknRelationType) {
			rt.Endpoint.Target = "a"
			rt.Characteristics = []string{CharacteristicSymmetric}
			rt.Endpoint.Cardinality = CardinalityOneToMany
		}), "", map[string]any{"cardinality": CardinalityOneToMany}},
		{RuleSymmetricReverseName, relation(func(rt *BknRelationType) {
			rt.Endpoint.Target = "a"
			rt.Characteristics = []string{CharacteristicSymmetric}
			rt.ReverseName = "back"
		}), "", nil},
		{RuleSymmetricRequired, relation(func(rt *BknRelationType) {
			rt.Endpoint.Target = "a"
			rt.Characteristics = []string{CharacteristicSymmetric}
			rt.RequiredEnds = []string{EndSource}
		}), "", nil},
		{RuleFallbackForInvalid, relation(secondaryFallback("Not An Id")), "", map[string]any{"fallback_for": "Not An Id"}},
		{RuleFallbackForNotSecondary, relation(func(rt *BknRelationType) { rt.FallbackFor = "main" }), "", map[string]any{"fallback_for": "main"}},
		{RuleFallbackForSelf, relation(secondaryFallback("rt")), "", map[string]any{"fallback_for": "rt"}},
		{RuleFallbackCrossJoin, relation(func(rt *BknRelationType) {
			secondaryFallback("main")(rt)
			rt.Endpoint.Type = RELATION_MAPPING_TYPE_FILTERED_CROSS_JOIN
		}), "", map[string]any{"relation_type": "rt"}},
		{RuleFallbackCharacteristics, relation(func(rt *BknRelationType) {
			secondaryFallback("main")(rt)
			rt.Endpoint.Target = "a"
			rt.Characteristics = []string{CharacteristicTransitive}
		}), "", map[string]any{"relation_type": "rt"}},
		{RuleRelationPropertiesNotIndirect, relation(func(rt *BknRelationType) { rt.DataProperties = []*DataProperty{rprop("p", "string")} }), "",
			map[string]any{"endpoint_type": "direct"}},

		{RuleRelationPropertiesTooMany, indirectRelation(func() []*DataProperty {
			props := make([]*DataProperty, maxPropertyNum+1)
			for i := range props {
				props[i] = rprop("p"+strconv.Itoa(i), "string")
			}
			return props
		}()...), "", map[string]any{"count": maxPropertyNum + 1, "max": maxPropertyNum}},
		{RuleRelationPropertyInvalid, indirectRelation(rprop("bad name", "string")), "",
			map[string]any{"reason": `property name "bad name" must match ^[a-zA-Z0-9][a-zA-Z0-9_-]{0,39}$`}},
		{RuleRelationPropertyJoinsEnds, indirectRelation(&DataProperty{Name: "p", DisplayName: "P", Type: "string", MappedField: "emp_id"}), "p",
			map[string]any{"mapped_field": "emp_id"}},
		{RuleRelationPropertyUnique, indirectRelation(&DataProperty{Name: "p", DisplayName: "P", Type: "string", Constraints: &PropertyConstraints{Unique: true}}), "p", nil},

		{RuleReplacedByMissing, network([]*BknObjectType{objectDef("old", &Lifecycle{Status: LifecycleDeprecated, Reason: "r", ReplacedBy: "new"})}, nil, nil), "",
			map[string]any{"replaced_by": "new"}},
		{RuleReplacedByDeprecated, network([]*BknObjectType{
			objectDef("old", &Lifecycle{Status: LifecycleDeprecated, Reason: "r", ReplacedBy: "new"}), objectDef("new", deprecatedLifecycle)}, nil, nil), "",
			map[string]any{"replaced_by": "new"}},
		{RuleEndpointDeprecated, network([]*BknObjectType{objectDef("a", nil), objectDef("b", deprecatedLifecycle)},
			[]*BknRelationType{relationDef("rt", "a", "b")}, nil), "", map[string]any{"object_type": "b"}},
		{RuleFallbackTargetMissing, network([]*BknObjectType{objectDef("a", nil), objectDef("b", nil)},
			[]*BknRelationType{relationDef("rt", "a", "b", secondaryFallback("main"))}, nil), "", map[string]any{"fallback_for": "main"}},
		{RuleFallbackTargetNotPrimary, network([]*BknObjectType{objectDef("a", nil), objectDef("b", nil)},
			[]*BknRelationType{relationDef("main", "a", "b", func(rt *BknRelationType) { rt.Importance = ImportanceSecondary }),
				relationDef("rt", "a", "b", secondaryFallback("main"))}, nil), "", map[string]any{"fallback_for": "main"}},
		{RuleFallbackTargetDeprecated, network([]*BknObjectType{objectDef("a", nil), objectDef("b", nil)},
			[]*BknRelationType{relationDef("main", "a", "b", func(rt *BknRelationType) { rt.Lifecycle = deprecatedLifecycle }),
				relationDef("rt", "a", "b", secondaryFallback("main"))}, nil), "", map[string]any{"fallback_for": "main"}},
		{RuleFallbackEndsDiffer, network([]*BknObjectType{objectDef("a", nil), objectDef("b", nil), objectDef("c", nil)},
			[]*BknRelationType{relationDef("main", "a", "c", cardinality(CardinalityManyToOne)),
				relationDef("rt", "a", "b", secondaryFallback("main"))}, nil), "",
			map[string]any{"fallback_for": "main", "source": "a", "target": "b", "primary_source": "a", "primary_target": "c"}},
		{RuleFallbackPrimaryCardinality, network([]*BknObjectType{objectDef("a", nil), objectDef("b", nil)},
			[]*BknRelationType{relationDef("main", "a", "b"), relationDef("rt", "a", "b", secondaryFallback("main"))}, nil), "",
			map[string]any{"fallback_for": "main", "cardinality": ""}},
		{RuleFallbackAlreadyTaken, network([]*BknObjectType{objectDef("a", nil), objectDef("b", nil)},
			[]*BknRelationType{relationDef("main", "a", "b", cardinality(CardinalityManyToOne)),
				relationDef("one", "a", "b", secondaryFallback("main")), relationDef("two", "a", "b", secondaryFallback("main"))}, nil), "",
			map[string]any{"fallback_for": "main", "other_fallback": "one"}},

		{RuleValueTypeDuplicated, network(nil, nil, []*BknValueType{rvalueType("vt", "one"), rvalueType("vt", "two")}), "",
			map[string]any{"value_type": "vt"}},
		{RuleValueTypeNameDuplicated, network(nil, nil, []*BknValueType{rvalueType("vt1", "same"), rvalueType("vt2", "same")}), "",
			map[string]any{"value_type": "vt2", "other_value_type": "vt1", "name": "same"}},
		{RuleValueTypeUndefined, network([]*BknObjectType{objectWithProps("ot", rprop("a", "string", withConstraints(PropertyConstraints{ValueType: "vt"})))}, nil, nil), "a",
			map[string]any{"value_type": "vt"}},
		{RuleValueTypeBaseMismatch, network([]*BknObjectType{objectWithProps("ot", rprop("a", "integer", withConstraints(PropertyConstraints{ValueType: "vt"})))}, nil,
			[]*BknValueType{rvalueType("vt", "vt")}), "a", map[string]any{"value_type": "vt", "base_type": "string", "type": "integer"}},
		{RuleAliasConflict, network([]*BknObjectType{objectDef("a", nil, "b"), objectDef("b", nil)}, nil, nil), "",
			map[string]any{"alias": "b", "conflicts_with": "b"}},
	}

	covered := map[string]bool{}
	for _, tc := range cases {
		t.Run(tc.rule, func(t *testing.T) {
			err := tc.check()
			require.Error(t, err)
			specErr, ok := err.(*SpecError)
			require.True(t, ok, "%v", err)
			assert.Equal(t, tc.rule, specErr.Rule(), specErr.Error())
			assert.Equal(t, tc.property, specErr.Property())
			assert.Equal(t, tc.params, specErr.Params())
			assert.NotEmpty(t, specErr.Error(), "the English message stays")
		})
		covered[tc.rule] = true
	}

	// Every rule constant has a case: a new rule cannot be added without its code and params tested.
	parsed, err := parser.ParseFile(token.NewFileSet(), "rules.go", nil, 0)
	require.NoError(t, err)
	ast.Inspect(parsed, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for i, name := range spec.Names {
			if !strings.HasPrefix(name.Name, "Rule") {
				continue
			}
			value := strings.Trim(spec.Values[i].(*ast.BasicLit).Value, `"`)
			assert.True(t, covered[value], "rule %s (%s) has no case", name.Name, value)
		}
		return true
	})
}

// indirectRelation validates an indirect relation that joins the ends on emp_id and carries props.
func indirectRelation(props ...*DataProperty) func() error {
	return relation(func(rt *BknRelationType) {
		rt.Endpoint.Type = RELATION_MAPPING_TYPE_INDIRECT
		rt.MappingRules = &InDirectMappingRule{
			SourceMappingRules: []MappingRule{{SourceProperty: "id", TargetProperty: "emp_id"}},
			TargetMappingRules: []MappingRule{{SourceProperty: "dept_id", TargetProperty: "id"}},
		}
		rt.DataProperties = props
	})
}

func rvalueType(id, name string) *BknValueType {
	return &BknValueType{BknValueTypeFrontmatter: BknValueTypeFrontmatter{ID: id, Name: name}, BaseType: "string"}
}

func objectWithProps(id string, props ...*DataProperty) *BknObjectType {
	ot := objectDef(id, nil)
	ot.DataProperties = props
	return ot
}
