// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"encoding/json"
	"strings"
)

// The declaration fields of a relation type (BKN spec 3.0, SPECIFICATION.md §7.6), besides
// DECLARATION_ALIASES, DECLARATION_CONSTRAINTS and DECLARATION_LIFECYCLE. DECLARATION_PROPERTIES is
// the relation properties, mapping_rules.properties of an indirect relation.
const (
	DECLARATION_REVERSE_NAME = "reverse_name"
	DECLARATION_CARDINALITY  = "cardinality"
	DECLARATION_IMPORTANCE   = "importance"
	DECLARATION_FALLBACK_FOR = "fallback_for"
	DECLARATION_VERIFICATION = "verification"
	DECLARATION_PROPERTIES   = "properties"
)

// IMPORTANCE_PRIMARY is the default importance; it is stored as empty.
const IMPORTANCE_PRIMARY = "primary"

// RelationDeclarationFields lists the declaration fields of a relation type in the order an overwrite
// refusal names them.
var RelationDeclarationFields = []string{
	DECLARATION_REVERSE_NAME, DECLARATION_ALIASES, DECLARATION_CARDINALITY, DECLARATION_CONSTRAINTS,
	DECLARATION_LIFECYCLE, DECLARATION_IMPORTANCE, DECLARATION_FALLBACK_FOR, DECLARATION_VERIFICATION,
	DECLARATION_PROPERTIES,
}

// RelationConstraints are ### Relation Constraints: the ends whose every instance has a counterpart,
// and the characteristics of a relation from an object type to itself.
type RelationConstraints struct {
	Required        []string `json:"required,omitempty" mapstructure:"required"`
	Characteristics []string `json:"characteristics,omitempty" mapstructure:"characteristics"`
}

// IsEmpty reports whether the constraints declare nothing.
func (c *RelationConstraints) IsEmpty() bool {
	return c == nil || (len(c.Required) == 0 && len(c.Characteristics) == 0)
}

// RelationDeclarations are the 3.0 declarations of a relation type. Each is one field: an update that
// carries it replaces it whole, and none of them is merged with the stored value.
type RelationDeclarations struct {
	ReverseName  string               `json:"reverse_name,omitempty" mapstructure:"reverse_name"`
	Aliases      []string             `json:"aliases,omitempty" mapstructure:"aliases"`
	Cardinality  string               `json:"cardinality,omitempty" mapstructure:"cardinality"`
	Constraints  *RelationConstraints `json:"constraints,omitempty" mapstructure:"constraints"`
	Lifecycle    *Lifecycle           `json:"lifecycle,omitempty" mapstructure:"lifecycle"`
	Importance   string               `json:"importance,omitempty" mapstructure:"importance"`
	FallbackFor  string               `json:"fallback_for,omitempty" mapstructure:"fallback_for"`
	Verification string               `json:"verification,omitempty" mapstructure:"verification"`
}

// UnmarshalJSON records which declaration fields the JSON carried, mapping_rules.properties among
// them, so an interactive update can keep the stored ones it left out.
func (rt *RelationType) UnmarshalJSON(data []byte) error {
	type relationType RelationType
	var value relationType
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	// properties is a key of mapping_rules, not of the relation type.
	topLevel := RelationDeclarationFields[:len(RelationDeclarationFields)-1]
	declared, err := carriedKeys(data, topLevel...)
	if err != nil {
		return err
	}
	if mappingCarriesProperties(data) {
		if declared == nil {
			declared = map[string]bool{}
		}
		declared[DECLARATION_PROPERTIES] = true
	}
	*rt = RelationType(value)
	rt.declared = declared
	return nil
}

// mappingCarriesProperties reports whether the mapping_rules object of a relation type JSON has a
// properties key.
func mappingCarriesProperties(data []byte) bool {
	var raw struct {
		MappingRules json.RawMessage `json:"mapping_rules"`
	}
	if err := json.Unmarshal(data, &raw); err != nil || len(raw.MappingRules) == 0 || raw.MappingRules[0] != '{' {
		return false
	}
	inner, err := carriedKeys(raw.MappingRules, DECLARATION_PROPERTIES)
	return err == nil && inner[DECLARATION_PROPERTIES]
}

// Declares reports whether the JSON this relation type was read from carried a declaration field,
// null included.
func (rt *RelationType) Declares(field string) bool {
	return rt != nil && rt.declared[field]
}

// RelationProperties returns the relation properties of an indirect relation type, nil for others.
func (rt *RelationType) RelationProperties() []*DataProperty {
	if rt == nil {
		return nil
	}
	if indirect, ok := rt.MappingRules.(*InDirectMapping); ok && indirect != nil {
		return indirect.Properties
	}
	return nil
}

// NormalizeDeclarations stores the defaults as empty: no aliases, an active lifecycle, importance
// primary, empty constraints and no relation properties are written as NULL, the way rows saved
// before the columns read. reverse_name is trimmed, as a name is.
func (rt *RelationType) NormalizeDeclarations() {
	rt.ReverseName = strings.TrimSpace(rt.ReverseName)
	rt.Aliases = NormalizeAliases(rt.Aliases)
	rt.Lifecycle = NormalizeLifecycle(rt.Lifecycle)
	if rt.Importance == IMPORTANCE_PRIMARY {
		rt.Importance = ""
	}
	if rt.Constraints.IsEmpty() {
		rt.Constraints = nil
	}
	if indirect, ok := rt.MappingRules.(*InDirectMapping); ok && indirect != nil {
		if len(indirect.Properties) == 0 {
			indirect.Properties = nil
		}
		for _, p := range indirect.Properties {
			if p == nil {
				continue
			}
			if p.Constraints.IsEmpty() {
				p.Constraints = nil
			}
			p.Aliases = NormalizeAliases(p.Aliases)
		}
	}
}

// DeclaredFields lists the declaration fields the relation type has a value for, in
// RelationDeclarationFields order.
func (rt *RelationType) DeclaredFields() []string {
	var fields []string
	for _, field := range RelationDeclarationFields {
		if rt.hasDeclaration(field) {
			fields = append(fields, field)
		}
	}
	return fields
}

func (rt *RelationType) hasDeclaration(field string) bool {
	switch field {
	case DECLARATION_REVERSE_NAME:
		return rt.ReverseName != ""
	case DECLARATION_ALIASES:
		return len(rt.Aliases) > 0
	case DECLARATION_CARDINALITY:
		return rt.Cardinality != ""
	case DECLARATION_CONSTRAINTS:
		return !rt.Constraints.IsEmpty()
	case DECLARATION_LIFECYCLE:
		return NormalizeLifecycle(rt.Lifecycle) != nil
	case DECLARATION_IMPORTANCE:
		return rt.Importance != "" && rt.Importance != IMPORTANCE_PRIMARY
	case DECLARATION_FALLBACK_FOR:
		return rt.FallbackFor != ""
	case DECLARATION_VERIFICATION:
		return rt.Verification != ""
	case DECLARATION_PROPERTIES:
		return len(rt.RelationProperties()) > 0
	}
	return false
}

// KeepUndeclared copies from stored the declaration fields this relation type's JSON left out. The
// relation properties are kept only while both are indirect: a relation of another type has none.
func (rt *RelationType) KeepUndeclared(stored *RelationType) {
	if stored == nil {
		return
	}
	if !rt.Declares(DECLARATION_REVERSE_NAME) {
		rt.ReverseName = stored.ReverseName
	}
	if !rt.Declares(DECLARATION_ALIASES) {
		rt.Aliases = stored.Aliases
	}
	if !rt.Declares(DECLARATION_CARDINALITY) {
		rt.Cardinality = stored.Cardinality
	}
	if !rt.Declares(DECLARATION_CONSTRAINTS) {
		rt.Constraints = stored.Constraints
	}
	if !rt.Declares(DECLARATION_LIFECYCLE) {
		rt.Lifecycle = stored.Lifecycle
	}
	if !rt.Declares(DECLARATION_IMPORTANCE) {
		rt.Importance = stored.Importance
	}
	if !rt.Declares(DECLARATION_FALLBACK_FOR) {
		rt.FallbackFor = stored.FallbackFor
	}
	if !rt.Declares(DECLARATION_VERIFICATION) {
		rt.Verification = stored.Verification
	}
	if !rt.Declares(DECLARATION_PROPERTIES) {
		if indirect, ok := rt.MappingRules.(*InDirectMapping); ok && indirect != nil {
			indirect.Properties = stored.RelationProperties()
		}
	}
}
