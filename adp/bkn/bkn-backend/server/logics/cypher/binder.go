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
)

// Schema is one knowledge network's modelling metadata, indexed for name
// resolution. It is built per request and thrown away: this service owns the
// data, so reading it is a local query rather than a network round trip, and
// caching it would only add an invalidation problem.
type Schema struct {
	KNID   string
	Branch string

	objectTypesByID   map[string]*interfaces.ObjectType
	objectTypesByName map[string]*interfaces.ObjectType

	relationTypesByID   map[string]*interfaces.RelationType
	relationTypesByName map[string]*interfaces.RelationType
}

// LoadSchema reads every object type and relation type of one knowledge
// network and indexes them by id and by name.
func LoadSchema(ctx context.Context, kn KNSchemaSource, knID, branch string) (*Schema, error) {
	objectTypes, err := kn.AllObjectTypes(ctx, knID, branch)
	if err != nil {
		return nil, err
	}
	relationTypes, err := kn.AllRelationTypes(ctx, knID, branch)
	if err != nil {
		return nil, err
	}

	s := &Schema{
		KNID:                knID,
		Branch:              branch,
		objectTypesByID:     make(map[string]*interfaces.ObjectType, len(objectTypes)),
		objectTypesByName:   make(map[string]*interfaces.ObjectType, len(objectTypes)),
		relationTypesByID:   make(map[string]*interfaces.RelationType, len(relationTypes)),
		relationTypesByName: make(map[string]*interfaces.RelationType, len(relationTypes)),
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

// KNSchemaSource is the slice of the modelling services the compiler needs.
// Narrowing it to two reads keeps the compiler from reaching into write paths.
type KNSchemaSource interface {
	AllObjectTypes(ctx context.Context, knID, branch string) ([]*interfaces.ObjectType, error)
	AllRelationTypes(ctx context.Context, knID, branch string) ([]*interfaces.RelationType, error)
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

// Column maps a property name on an object type to the physical column the
// generated SQL must use.
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
	return "", fmt.Errorf("object type %q has no property %q%s",
		ot.OTID, property, suggest(property, dataPropertyNames(ot)))
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

func dataPropertyNames(ot *interfaces.ObjectType) []string {
	names := make([]string, 0, len(ot.DataProperties))
	for _, dp := range ot.DataProperties {
		names = append(names, dp.Name)
	}
	return names
}

func (s *Schema) suggestObjectTypes(label string) string {
	names := make([]string, 0, len(s.objectTypesByID))
	for id := range s.objectTypesByID {
		names = append(names, id)
	}
	return suggest(label, names)
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
