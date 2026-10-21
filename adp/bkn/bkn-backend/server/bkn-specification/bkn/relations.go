// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Lifecycle statuses (SPECIFICATION.md §4.3).
const (
	LifecycleActive     = "active"
	LifecycleDeprecated = "deprecated"
)

// Relation importance and verification (SPECIFICATION.md §7.6). An empty Importance is primary.
const (
	ImportancePrimary   = "primary"
	ImportanceSecondary = "secondary"

	VerificationConfirmed   = "confirmed"
	VerificationUnconfirmed = "unconfirmed"
)

// Relation cardinalities, the ends of required and the characteristics (SPECIFICATION.md §7.6).
const (
	CardinalityOneToOne   = "one_to_one"
	CardinalityOneToMany  = "one_to_many"
	CardinalityManyToOne  = "many_to_one"
	CardinalityManyToMany = "many_to_many"

	EndSource = "source"
	EndTarget = "target"

	CharacteristicSymmetric  = "symmetric"
	CharacteristicTransitive = "transitive"
)

var (
	lifecycleKeys     = []string{"status", "reason", "replaced_by"}
	lifecycleStatuses = []string{LifecycleActive, LifecycleDeprecated}
	importances       = []string{ImportancePrimary, ImportanceSecondary}
	verifications     = []string{VerificationConfirmed, VerificationUnconfirmed}
	cardinalities     = []string{CardinalityOneToOne, CardinalityOneToMany, CardinalityManyToOne, CardinalityManyToMany}
	relationEnds      = []string{EndSource, EndTarget}
	characteristics   = []string{CharacteristicSymmetric, CharacteristicTransitive}
	// relationConstraintKeys is the order the keys of ### Relation Constraints are written in.
	relationConstraintKeys = []string{"required", "characteristics"}
	// fallbackCardinalities are the cardinalities a primary relation with a fallback path declares:
	// a Source instance has at most one primary counterpart.
	fallbackCardinalities = []string{CardinalityManyToOne, CardinalityOneToOne}
)

// frontmatterString reads an optional scalar frontmatter field as written. A field that is present
// but not a non-empty string is refused rather than read as absent.
func frontmatterString(fm rawFrontmatter, key string) (string, error) {
	node, ok := fm.values[key]
	if !ok {
		return "", nil
	}
	if node.Kind != yaml.ScalarNode || node.Tag == "!!null" || node.Value == "" {
		return "", specErrorf("frontmatter %s must be a single value", key)
	}
	return node.Value, nil
}

// parseLifecycleNode reads the lifecycle of a frontmatter. status: active, written or not, is held
// as nil.
func parseLifecycleNode(id string, node *yaml.Node) (*Lifecycle, error) {
	if node == nil {
		return nil, nil
	}
	if node.Kind != yaml.MappingNode {
		return nil, specErrorf("frontmatter lifecycle must be a mapping with status, such as {status: deprecated, reason: ...}")
	}
	lc := &Lifecycle{}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key, value := node.Content[i].Value, node.Content[i+1]
		if value.Kind != yaml.ScalarNode || value.Tag == "!!null" || value.Value == "" {
			return nil, specErrorf("lifecycle %s must be a single value", key)
		}
		switch key {
		case "status":
			lc.Status = value.Value
		case "reason":
			lc.Reason = value.Value
		case "replaced_by":
			lc.ReplacedBy = value.Value
		default:
			return nil, specErrorf("lifecycle key %q is unknown; the keys are %s", key, strings.Join(lifecycleKeys, ", "))
		}
	}
	if lc.Status == "" {
		return nil, specErrorf("lifecycle needs status: %s", strings.Join(lifecycleStatuses, " or "))
	}
	if err := validateLifecycle("lifecycle", lc, id); err != nil {
		return nil, err
	}
	if lc.Status == LifecycleActive {
		return nil, nil
	}
	return lc, nil
}

// validateLifecycle checks a lifecycle on its own. A nil lifecycle is active.
func validateLifecycle(where string, lc *Lifecycle, ownID string) error {
	if lc == nil {
		return nil
	}
	switch lc.Status {
	case LifecycleActive:
		if lc.Reason != "" || lc.ReplacedBy != "" {
			return specErrorf("%s: reason and replaced_by go with status deprecated only", where)
		}
	case LifecycleDeprecated:
		if strings.TrimSpace(lc.Reason) == "" {
			return specErrorf("%s: status deprecated needs a reason", where)
		}
		if lc.ReplacedBy != "" && lc.ReplacedBy == ownID {
			return specErrorf("%s: replaced_by %q is the definition itself", where, lc.ReplacedBy)
		}
	default:
		return specErrorf("%s: status must be %s, got %q", where, strings.Join(lifecycleStatuses, " or "), lc.Status)
	}
	return nil
}

// isDeprecated reports whether a lifecycle is deprecated.
func isDeprecated(lc *Lifecycle) bool {
	return lc != nil && lc.Status == LifecycleDeprecated
}

// readRelationFrontmatterV3 reads the 3.0 relation fields of a relation frontmatter. importance:
// primary, written or not, is held as empty.
func readRelationFrontmatterV3(rel *BknRelationType, fm rawFrontmatter) error {
	for _, f := range []struct {
		key string
		dst *string
	}{
		{"reverse_name", &rel.ReverseName},
		{"importance", &rel.Importance},
		{"fallback_for", &rel.FallbackFor},
		{"verification", &rel.Verification},
	} {
		value, err := frontmatterString(fm, f.key)
		if err != nil {
			return err
		}
		*f.dst = value
	}
	if rel.Importance == ImportancePrimary {
		rel.Importance = ""
	}
	return nil
}

// parseRelationConstraintsV3 reads the yaml block of ### Relation Constraints.
func parseRelationConstraintsV3(rel *BknRelationType, sectionText string) error {
	content := scanContent(sectionText)
	if len(content.blocks) == 0 {
		return nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(content.blocks[0].body), &doc); err != nil {
		return specErrorf("section \"### Relation Constraints\" yaml is invalid: %v", err)
	}
	if len(doc.Content) == 0 {
		return nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return specErrorf("section \"### Relation Constraints\" must be a mapping with required, characteristics or both")
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		key, value := root.Content[i].Value, root.Content[i+1]
		var items []string
		if value.Kind != yaml.SequenceNode {
			return specErrorf("relation constraints %s must be a list such as [source]", key)
		}
		for _, item := range value.Content {
			if item.Kind != yaml.ScalarNode || item.Tag == "!!null" {
				return specErrorf("relation constraints %s must list single values", key)
			}
			items = append(items, item.Value)
		}
		switch key {
		case "required":
			rel.RequiredEnds = items
		case "characteristics":
			rel.Characteristics = items
		default:
			return specErrorf("relation constraints key %q is unknown; the keys are %s", key, strings.Join(relationConstraintKeys, ", "))
		}
	}
	return nil
}

// ValidateRelationTypeDeclarations checks the declarations of a relation type on its own: the
// frontmatter fields, Cardinality, ### Relation Constraints and the relation properties. Import runs
// it on a 3.0 definition; a save through the API runs the same check.
func ValidateRelationTypeDeclarations(rt *BknRelationType) error {
	if rt.ReverseName != "" {
		if msg := validateObjectName(rt.ReverseName, ""); msg != "" {
			return specErrorf("relation type reverse_name: %s", msg)
		}
	}
	if err := validateAliases("relation type aliases", rt.Aliases, rt.Name, rt.ReverseName); err != nil {
		return err
	}
	if err := validateLifecycle("relation type lifecycle", rt.Lifecycle, rt.ID); err != nil {
		return err
	}
	if rt.Importance != "" && !containsString(importances, rt.Importance) {
		return specErrorf("relation type importance must be %s, got %q", strings.Join(importances, " or "), rt.Importance)
	}
	if rt.Verification != "" && !containsString(verifications, rt.Verification) {
		return specErrorf("relation type verification must be %s, got %q", strings.Join(verifications, " or "), rt.Verification)
	}
	if err := validateEndpointDeclarations(rt); err != nil {
		return err
	}
	if rt.FallbackFor != "" {
		if rt.Importance != ImportanceSecondary {
			return specErrorf("relation type fallback_for goes with importance: secondary; a fallback path is a secondary relation")
		}
		if rt.FallbackFor == rt.ID {
			return specErrorf("relation type fallback_for %q is the relation itself", rt.FallbackFor)
		}
		if err := checkFallbackShape("relation type fallback path", rt); err != nil {
			return err
		}
	}
	return validateRelationProperties(rt)
}

// validateEndpointDeclarations checks Cardinality, required and characteristics.
func validateEndpointDeclarations(rt *BknRelationType) error {
	card := rt.Endpoint.Cardinality
	if card != "" && !containsString(cardinalities, card) {
		return specErrorf("Endpoint Cardinality must be one of %s, got %q", strings.Join(cardinalities, ", "), card)
	}
	if rt.Endpoint.Type == RELATION_MAPPING_TYPE_FILTERED_CROSS_JOIN && card != "" && card != CardinalityManyToMany {
		return specErrorf("Endpoint Cardinality of a filtered_cross_join relation can only be many_to_many, which links every pair, got %q", card)
	}
	if err := checkSubset("relation constraints required", rt.RequiredEnds, relationEnds); err != nil {
		return err
	}
	if err := checkSubset("relation constraints characteristics", rt.Characteristics, characteristics); err != nil {
		return err
	}
	if len(rt.Characteristics) > 0 && rt.Endpoint.Source != rt.Endpoint.Target {
		return specErrorf("relation constraints characteristics apply to a relation whose Source and Target are the same object type, got %q and %q",
			rt.Endpoint.Source, rt.Endpoint.Target)
	}
	if containsString(rt.Characteristics, CharacteristicSymmetric) {
		if card == CardinalityOneToMany || card == CardinalityManyToOne {
			return specErrorf("a symmetric relation has the same count both ways, so its Cardinality is one_to_one, many_to_many or empty, got %s", card)
		}
		if rt.ReverseName != "" {
			return specErrorf("a symmetric relation reads the same both ways, so it has no reverse_name")
		}
		if len(rt.RequiredEnds) == 1 {
			return specErrorf("a symmetric relation has no direction, so required is [source, target] or not written")
		}
	}
	return nil
}

// checkSubset checks that a list holds allowed values, each once.
func checkSubset(where string, items, allowed []string) error {
	seen := map[string]bool{}
	for _, item := range items {
		if !containsString(allowed, item) {
			return specErrorf("%s: %q is not one of %s", where, item, strings.Join(allowed, ", "))
		}
		if seen[item] {
			return specErrorf("%s: %q appears twice", where, item)
		}
		seen[item] = true
	}
	return nil
}

// checkFallbackShape checks what both a primary relation and its fallback path must be: direct or
// indirect, without characteristics.
func checkFallbackShape(where string, rt *BknRelationType) error {
	if rt.Endpoint.Type == RELATION_MAPPING_TYPE_FILTERED_CROSS_JOIN {
		return specErrorf("%s %q is a filtered_cross_join relation; fallback paths are direct or indirect", where, rt.ID)
	}
	if len(rt.Characteristics) > 0 {
		return specErrorf("%s %q declares characteristics; fallback paths do not", where, rt.ID)
	}
	return nil
}

// validateRelationProperties checks the relation properties: only an indirect relation has them,
// with the rules of object type properties, no unique, and no Mapped Field that joins the ends.
func validateRelationProperties(rt *BknRelationType) error {
	if len(rt.DataProperties) == 0 {
		return nil
	}
	indirect, ok := rt.MappingRules.(*InDirectMappingRule)
	if rt.Endpoint.Type != RELATION_MAPPING_TYPE_INDIRECT || !ok {
		return specErrorf("only indirect relations have relation properties, but Endpoint Type is %s", rt.Endpoint.Type)
	}
	joins := map[string]bool{}
	for _, r := range indirect.SourceMappingRules {
		joins[r.TargetProperty] = true
	}
	for _, r := range indirect.TargetMappingRules {
		joins[r.SourceProperty] = true
	}
	for _, p := range rt.DataProperties {
		if p == nil {
			continue
		}
		if joins[p.MappedField] {
			return specErrorf("relation property %q: Mapped Field %q joins the ends in Source Mapping or Target Mapping; a relation property maps another column",
				p.Name, p.MappedField)
		}
		if p.Constraints != nil && p.Constraints.Unique {
			return specErrorf("relation property %q: unique does not apply, an edge has no primary key", p.Name)
		}
	}
	return validatePropertyDeclarations(rt.DataProperties)
}

// validateNetworkRelations checks the relation and lifecycle declarations that span definitions:
// replaced_by references, endpoints that are deprecated, and fallback paths. The message names the
// definition that refers, so a change to the one referred to says what it breaks.
func validateNetworkRelations(doc *BknNetwork) error {
	objects := map[string]*BknObjectType{}
	for _, ot := range doc.ObjectTypes {
		objects[ot.ID] = ot
	}
	relations := map[string]*BknRelationType{}
	for _, rt := range doc.RelationTypes {
		relations[rt.ID] = rt
	}
	actions := map[string]*BknActionType{}
	for _, at := range doc.ActionTypes {
		actions[at.ID] = at
	}

	for _, ot := range doc.ObjectTypes {
		if err := checkReplacedBy(kindObjectType, ot.ID, ot.Lifecycle, func(id string) (*Lifecycle, bool) {
			o, ok := objects[id]
			if !ok {
				return nil, false
			}
			return o.Lifecycle, true
		}); err != nil {
			return err
		}
	}
	for _, at := range doc.ActionTypes {
		if err := checkReplacedBy(kindActionType, at.ID, at.Lifecycle, func(id string) (*Lifecycle, bool) {
			a, ok := actions[id]
			if !ok {
				return nil, false
			}
			return a.Lifecycle, true
		}); err != nil {
			return err
		}
	}
	fallbackOf := map[string]string{}
	for _, rt := range doc.RelationTypes {
		if err := checkReplacedBy(kindRelationType, rt.ID, rt.Lifecycle, func(id string) (*Lifecycle, bool) {
			r, ok := relations[id]
			if !ok {
				return nil, false
			}
			return r.Lifecycle, true
		}); err != nil {
			return err
		}
		if !isDeprecated(rt.Lifecycle) {
			for _, end := range []string{rt.Endpoint.Source, rt.Endpoint.Target} {
				if o, ok := objects[end]; ok && isDeprecated(o.Lifecycle) {
					return specErrorf("relation type %q: endpoint object type %q is deprecated, so the relation is deprecated too", rt.ID, end)
				}
			}
		}
		if rt.FallbackFor == "" {
			continue
		}
		where := fmt.Sprintf("relation type %q fallback_for %q", rt.ID, rt.FallbackFor)
		primary, ok := relations[rt.FallbackFor]
		if !ok {
			return specErrorf("%s: no relation type has this id", where)
		}
		switch {
		case primary.Importance != "" && primary.Importance != ImportancePrimary:
			return specErrorf("%s: the relation it stands in for is secondary; a fallback path stands in for a primary relation", where)
		case isDeprecated(primary.Lifecycle):
			return specErrorf("%s: the relation it stands in for is deprecated; a fallback path stands in for an active relation", where)
		case primary.Endpoint.Source != rt.Endpoint.Source || primary.Endpoint.Target != rt.Endpoint.Target:
			return specErrorf("%s: the ends differ (%s -> %s and %s -> %s); a fallback path has the Source and Target of its primary relation",
				where, rt.Endpoint.Source, rt.Endpoint.Target, primary.Endpoint.Source, primary.Endpoint.Target)
		case !containsString(fallbackCardinalities, primary.Endpoint.Cardinality):
			return specErrorf("%s: the primary relation declares Cardinality %q; it must be many_to_one or one_to_one, so a Source instance has at most one primary counterpart",
				where, primary.Endpoint.Cardinality)
		}
		if err := checkFallbackShape(where+": primary relation", primary); err != nil {
			return err
		}
		if other, taken := fallbackOf[primary.ID]; taken {
			return specErrorf("%s: relation type %q is already its fallback path; a primary relation has one", where, other)
		}
		fallbackOf[primary.ID] = rt.ID
	}
	return nil
}

// checkReplacedBy checks a deprecated definition's replaced_by: a definition of the same kind that is
// not deprecated itself.
func checkReplacedBy(kind, id string, lc *Lifecycle, lookup func(string) (*Lifecycle, bool)) error {
	if lc == nil || lc.ReplacedBy == "" {
		return nil
	}
	label := strings.ReplaceAll(kind, "_", " ")
	target, ok := lookup(lc.ReplacedBy)
	if !ok {
		return specErrorf("%s %q lifecycle replaced_by %q: no %s has this id", label, id, lc.ReplacedBy, label)
	}
	if isDeprecated(target) {
		return specErrorf("%s %q lifecycle replaced_by %q: that %s is deprecated too; replace with one in use", label, id, lc.ReplacedBy, label)
	}
	return nil
}
