// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"bkn-backend/interfaces"
	"bkn-backend/logics/object_type"
)

// Schema is one knowledge network's modelling metadata, indexed for name
// resolution. It is built per request and thrown away: this service owns the
// data, so reading it is a local query rather than a network round trip, and
// caching it would only add an invalidation problem.
type Schema struct {
	KNID   string
	Branch string

	// hadObjectTypes records whether the network held any object type before
	// the caller's visibility was applied, which is what separates "you may
	// read nothing here" from "there is nothing here".
	hadObjectTypes bool

	objectTypesByID   map[string]*interfaces.ObjectType
	objectTypesByName map[string]*interfaces.ObjectType

	relationTypesByID   map[string]*interfaces.RelationType
	relationTypesByName map[string]*interfaces.RelationType

	// propertyLevels holds, for each object type whose levels were loaded,
	// the caller's effective access level for every data property (#1374).
	// A property at none does not exist for the caller: naming it reads as an
	// unknown property, and it is never offered as a suggestion.
	propertyLevels map[string]map[string]string
}

// Visibility answers which of a knowledge network's object types and relation
// types this caller may read data from.
//
// It is applied while the schema is being built rather than after the query is
// compiled, so a concept the caller cannot query is simply not there. That
// makes "you may not read this" and "no such thing" the same answer, which is
// what keeps the endpoint from being a way to enumerate a model that the
// caller has no access to.
type Visibility interface {
	PermittedObjectTypes(ctx context.Context, knID string, otIDs []string) (map[string]bool, error)
	PermittedRelationTypes(ctx context.Context, knID string, rtIDs []string) (map[string]bool, error)
}

// LoadSchema reads every object type and relation type of one knowledge
// network that the caller may query, and indexes them by id and by name.
func LoadSchema(ctx context.Context, kn KNSchemaSource, visibility Visibility, knID, branch string) (*Schema, error) {
	objectTypes, err := kn.AllObjectTypes(ctx, knID, branch)
	if err != nil {
		return nil, err
	}
	relationTypes, err := kn.AllRelationTypes(ctx, knID, branch)
	if err != nil {
		return nil, err
	}
	hadObjectTypes := len(objectTypes) > 0
	objectTypes, relationTypes, err = applyVisibility(ctx, visibility, knID, objectTypes, relationTypes)
	if err != nil {
		return nil, err
	}

	s := &Schema{
		KNID:                knID,
		Branch:              branch,
		hadObjectTypes:      hadObjectTypes,
		objectTypesByID:     make(map[string]*interfaces.ObjectType, len(objectTypes)),
		objectTypesByName:   make(map[string]*interfaces.ObjectType, len(objectTypes)),
		relationTypesByID:   make(map[string]*interfaces.RelationType, len(relationTypes)),
		relationTypesByName: make(map[string]*interfaces.RelationType, len(relationTypes)),
		propertyLevels:      map[string]map[string]string{},
	}
	for _, ot := range objectTypes {
		s.objectTypesByID[ot.OTID] = ot
		s.objectTypesByName[ot.OTName] = ot
	}
	for _, rt := range relationTypes {
		s.relationTypesByID[rt.RTID] = rt
		s.relationTypesByName[rt.RTName] = rt
	}
	return s, nil
}

func applyVisibility(ctx context.Context, visibility Visibility, knID string,
	objectTypes []*interfaces.ObjectType, relationTypes []*interfaces.RelationType) (
	[]*interfaces.ObjectType, []*interfaces.RelationType, error) {

	if visibility == nil {
		return objectTypes, relationTypes, nil
	}

	otIDs := make([]string, 0, len(objectTypes))
	for _, ot := range objectTypes {
		otIDs = append(otIDs, ot.OTID)
	}
	permittedObjectTypes, err := visibility.PermittedObjectTypes(ctx, knID, otIDs)
	if err != nil {
		return nil, nil, err
	}
	visibleObjectTypes := make([]*interfaces.ObjectType, 0, len(objectTypes))
	for _, ot := range objectTypes {
		if permittedObjectTypes[ot.OTID] {
			visibleObjectTypes = append(visibleObjectTypes, ot)
		}
	}

	rtIDs := make([]string, 0, len(relationTypes))
	for _, rt := range relationTypes {
		rtIDs = append(rtIDs, rt.RTID)
	}
	permittedRelationTypes, err := visibility.PermittedRelationTypes(ctx, knID, rtIDs)
	if err != nil {
		return nil, nil, err
	}
	visibleRelationTypes := make([]*interfaces.RelationType, 0, len(relationTypes))
	for _, rt := range relationTypes {
		// A relation type whose endpoints the caller cannot read is dropped
		// too: a join would otherwise return rows of a hidden object type
		// through the relation's own permission.
		if !permittedRelationTypes[rt.RTID] {
			continue
		}
		if !permittedObjectTypes[rt.SourceObjectTypeID] || !permittedObjectTypes[rt.TargetObjectTypeID] {
			continue
		}
		visibleRelationTypes = append(visibleRelationTypes, rt)
	}
	return visibleObjectTypes, visibleRelationTypes, nil
}

// KNSchemaSource is the slice of the modelling services the compiler needs.
// Narrowing it to two reads keeps the compiler from reaching into write paths.
type KNSchemaSource interface {
	AllObjectTypes(ctx context.Context, knID, branch string) ([]*interfaces.ObjectType, error)
	AllRelationTypes(ctx context.Context, knID, branch string) ([]*interfaces.RelationType, error)
}

// NothingReadable reports a network that holds object types of which the
// caller may read none. An empty network is not that case: there every label
// really is unknown, and saying so is both true and more useful than a
// refusal.
func (s *Schema) NothingReadable() bool {
	return s.hadObjectTypes && len(s.objectTypesByID) == 0
}

// ResolveLabel maps a Cypher label to an object type.
//
// Both the object type id and its name are accepted. A name is unambiguous
// because modelling rejects a duplicate name inside a knowledge network, and
// accepting it lets a query read as the model does. The id wins when a token
// happens to match one object type's id and another's name, and that collision
// is reported rather than resolved silently, because either answer would be a
// guess about what the author meant.
func (s *Schema) ResolveLabel(label string) (*interfaces.ObjectType, error) {
	byID, hasID := s.objectTypesByID[label]
	byName, hasName := s.objectTypesByName[label]

	switch {
	case hasID && hasName && byID.OTID != byName.OTID:
		return nil, fmt.Errorf(
			"label %q is ambiguous: it is the id of object type %q and the name of object type %q; use the id to disambiguate",
			label, byID.OTName, byName.OTID)
	case hasID:
		return byID, nil
	case hasName:
		return byName, nil
	default:
		return nil, fmt.Errorf("unknown label %q in knowledge network %q%s",
			label, s.KNID, s.suggestObjectTypes(label))
	}
}

// ResolveRelationType maps a Cypher relationship type to a relation type,
// following the same id-first rule as ResolveLabel.
func (s *Schema) ResolveRelationType(name string) (*interfaces.RelationType, error) {
	byID, hasID := s.relationTypesByID[name]
	byName, hasName := s.relationTypesByName[name]

	switch {
	case hasID && hasName && byID.RTID != byName.RTID:
		return nil, fmt.Errorf(
			"relationship type %q is ambiguous: it is the id of %q and the name of %q; use the id to disambiguate",
			name, byID.RTName, byName.RTID)
	case hasID:
		return byID, nil
	case hasName:
		return byName, nil
	default:
		return nil, fmt.Errorf("unknown relationship type %q in knowledge network %q", name, s.KNID)
	}
}

// SetPropertyLevels records the caller's effective access level for each data
// property of one object type.
func (s *Schema) SetPropertyLevels(otID string, levels map[string]string) {
	s.propertyLevels[otID] = levels
}

// PropertyLevels returns the levels recorded for one object type, and whether
// any were.
func (s *Schema) PropertyLevels(otID string) (map[string]string, bool) {
	levels, ok := s.propertyLevels[otID]
	return levels, ok
}

// hidden reports a property the caller may not know exists.
func (s *Schema) hidden(ot *interfaces.ObjectType, property string) bool {
	return s.propertyLevels[ot.OTID][property] == interfaces.PROPERTY_ACCESS_NONE
}

// PropertyColumn is Column for a property the query names.
//
// A property whose level for the caller is none does not exist for them, so
// naming it gets exactly the answer a property that is not in the model gets,
// with suggestions drawn from the same visible set. Anything that told the two
// apart would tell the caller the property is there.
//
// A logic property that public reads hide, because it is computed from a
// property at none, gets the same answer, so naming it here does not confirm
// what the object type read would not show.
func (s *Schema) PropertyColumn(ot *interfaces.ObjectType, property string) (string, error) {
	if s.hidden(ot, property) || s.hiddenLogicProperty(ot, property) {
		return "", s.unknownProperty(ot, property)
	}
	return s.Column(ot, property)
}

func (s *Schema) hiddenLogicProperty(ot *interfaces.ObjectType, property string) bool {
	if _, loaded := s.propertyLevels[ot.OTID]; !loaded {
		return false
	}
	visible := make(map[string]struct{}, len(ot.DataProperties))
	for _, name := range s.visiblePropertyNames(ot) {
		visible[name] = struct{}{}
	}
	for _, lp := range ot.LogicProperties {
		if lp != nil && lp.Name == property {
			return !object_type.LogicPropertyVisible(lp, visible)
		}
	}
	return false
}

// Column maps a property name on an object type to the physical column the
// generated SQL must use. It is also how the planner resolves the keys a
// relation type maps and the primary keys it compares, which come from the
// model rather than from the query, so it resolves a property the caller may
// not see; only its error text keeps to what they may see.
//
// Only the property name is accepted, never the display name: display names
// carry no uniqueness guarantee, so accepting them would need a collision rule
// first. A logic property is rejected outright -- it is computed by the
// modelling layer and has no column behind it to select.
func (s *Schema) Column(ot *interfaces.ObjectType, property string) (string, error) {
	for _, dp := range ot.DataProperties {
		if dp.Name != property {
			continue
		}
		if dp.MappedField == nil || dp.MappedField.Name == "" {
			if s.hidden(ot, property) {
				return "", fmt.Errorf("object type %q has a key property with no mapped column", ot.OTID)
			}
			return "", fmt.Errorf("property %q of object type %q has no mapped column", property, ot.OTID)
		}
		return dp.MappedField.Name, nil
	}
	for _, lp := range ot.LogicProperties {
		if lp.Name == property {
			return "", fmt.Errorf(
				"property %q of object type %q is a logic property: it is computed by the modelling layer and has no column to query",
				property, ot.OTID)
		}
	}
	return "", s.unknownProperty(ot, property)
}

func (s *Schema) unknownProperty(ot *interfaces.ObjectType, property string) error {
	return fmt.Errorf("object type %q has no property %q%s",
		ot.OTID, property, suggest(property, s.visiblePropertyNames(ot)))
}

// ResourceID is the vega resource backing an object type, for the
// {{.resource_id}} placeholder.
//
// Two broken bindings occur in real networks and mean different things: a
// missing data source is an object type that was never bound, while an empty
// id with a name still present is a binding whose resource no longer resolves,
// typically after a catalog was rebuilt. The messages stay distinct so the
// modeller knows which one to fix.
func (s *Schema) ResourceID(ot *interfaces.ObjectType) (string, error) {
	if ot.DataSource == nil {
		return "", fmt.Errorf("object type %q has no data source bound", ot.OTID)
	}
	if ot.DataSource.ID == "" {
		return "", fmt.Errorf("object type %q has a stale data source binding (name %q, no resource id)",
			ot.OTID, ot.DataSource.Name)
	}
	return ot.DataSource.ID, nil
}

// visiblePropertyNames is what a suggestion may be drawn from: every data
// property the caller may know exists.
func (s *Schema) visiblePropertyNames(ot *interfaces.ObjectType) []string {
	names := make([]string, 0, len(ot.DataProperties))
	for _, dp := range ot.DataProperties {
		if !s.hidden(ot, dp.Name) {
			names = append(names, dp.Name)
		}
	}
	return names
}

// suggestObjectTypes offers both ids and names, because a label may be written
// either way and the near miss is as likely to be on one as on the other.
func (s *Schema) suggestObjectTypes(label string) string {
	candidates := make([]string, 0, 2*len(s.objectTypesByID))
	for id, ot := range s.objectTypesByID {
		candidates = append(candidates, id)
		if ot.OTName != "" && ot.OTName != id {
			candidates = append(candidates, ot.OTName)
		}
	}
	return suggest(label, candidates)
}

// suggest offers near matches so a typo does not read as a modelling gap. It
// is a hint only, and stays empty when nothing is close enough to help.
func suggest(want string, candidates []string) string {
	lower := strings.ToLower(want)
	var near []string
	for _, c := range candidates {
		lc := strings.ToLower(c)
		if lc == lower || strings.Contains(lc, lower) || strings.Contains(lower, lc) {
			near = append(near, c)
		}
	}
	if len(near) == 0 {
		return ""
	}
	sort.Strings(near)
	if len(near) > 5 {
		near = near[:5]
	}
	return fmt.Sprintf(" (did you mean %s?)", strings.Join(near, ", "))
}
