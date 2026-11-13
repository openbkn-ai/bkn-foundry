// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// A requirement concept never becomes an item of its own: it is matched to the data that holds it,
// a table of the project's catalogs or an object type of the draft (one a knowledge-base
// extraction drafted, too), or to nothing in the data yet. The concepts pass proposes the match
// with its basis; a person confirms it, chooses another, or leaves the concept out.

// maxMatchBasisRunes bounds the basis the Agent gives for a match.
const maxMatchBasisRunes = 200

// agentMatch is the match the Agent proposes for a concept in pass concepts: a table by its name
// as the message lists it, or an object type by its id; neither when nothing in the data is it.
type agentMatch struct {
	Table        string `json:"table"`
	ObjectTypeID string `json:"object_type_id"`
	Basis        string `json:"basis"`
}

// matchSchema is the JSON Schema of the match a concept of pass concepts takes.
func matchSchema() map[string]any {
	return schemaObject(map[string]any{
		"table": schemaString(), "object_type_id": schemaString(), "basis": schemaString(),
	}, "basis")
}

// matchedTarget is what the Agent's match names among the tables and the adopted object types the
// message listed, nil when it names nothing listed.
func matchedTarget(m *agentMatch, tables []*interfaces.VegaTable,
	adopted map[interfaces.RequirementItemRef]bool) *interfaces.RequirementMatchTarget {
	if m == nil {
		return nil
	}
	if id := strings.TrimSpace(m.ObjectTypeID); id != "" &&
		adopted[interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: id}] {
		return &interfaces.RequirementMatchTarget{Kind: interfaces.REQUIREMENT_MATCH_OBJECT_TYPE, ID: id}
	}
	if name := strings.TrimSpace(m.Table); name != "" {
		listed := tables[:min(len(tables), maxMessageTables)]
		if i := slices.IndexFunc(listed, func(t *interfaces.VegaTable) bool { return t != nil && t.Name == name }); i >= 0 {
			return &interfaces.RequirementMatchTarget{Kind: interfaces.REQUIREMENT_MATCH_TABLE, ID: listed[i].ID}
		}
	}
	return nil
}

// proposedMatch is the match the Agent proposed, as stored: its basis shortened, with no target
// when it named nothing listed.
func proposedMatch(m *agentMatch, tables []*interfaces.VegaTable,
	adopted map[interfaces.RequirementItemRef]bool) *interfaces.RequirementMatch {
	if m == nil {
		return nil
	}
	basis := strings.TrimSpace(m.Basis)
	if utf8.RuneCountInString(basis) > maxMatchBasisRunes {
		basis = string([]rune(basis)[:maxMatchBasisRunes])
	}
	return &interfaces.RequirementMatch{Target: matchedTarget(m, tables, adopted), Basis: basis,
		By: interfaces.REQUIREMENT_MATCH_BY_AI}
}

// mappedObjectType is the drafted object type a target names: the object type itself, or the one
// drafted from the table, which no document drafted.
func mappedObjectType(target *interfaces.RequirementMatchTarget, drafted *draftedItems) *interfaces.RequirementItemRef {
	if target == nil {
		return nil
	}
	switch target.Kind {
	case interfaces.REQUIREMENT_MATCH_OBJECT_TYPE:
		ref := interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: target.ID}
		if drafted.has(ref) {
			return &ref
		}
	case interfaces.REQUIREMENT_MATCH_TABLE:
		if drafted.draft == nil {
			return nil
		}
		for _, ot := range drafted.draft.Items.ObjectTypes {
			if ot.Document == nil && ot.Table.ResourceID == target.ID {
				return &interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: ot.ID}
			}
		}
	}
	return nil
}

// mappedMatch is the person's match a mapping to a drafted item makes: the object type it names,
// confirmed; nothing in the data when it is cleared or names a relation type.
func mappedMatch(ref *interfaces.RequirementItemRef) *interfaces.RequirementMatch {
	match := &interfaces.RequirementMatch{By: interfaces.REQUIREMENT_MATCH_BY_PERSON}
	if ref != nil && ref.Kind == interfaces.REQUIREMENT_ITEM_OBJECT_TYPE {
		match.Target = &interfaces.RequirementMatchTarget{Kind: interfaces.REQUIREMENT_MATCH_OBJECT_TYPE, ID: ref.ID}
		match.Confirmed = true
	}
	return match
}

// sameTarget tells whether two targets name the same data.
func sameTarget(a, b *interfaces.RequirementMatchTarget) bool {
	return a != nil && b != nil && *a == *b
}

func (s *requirementService) MatchConcept(ctx context.Context, token string, projectID string, conceptID string,
	edit interfaces.RequirementMatchEdit, caller interfaces.AccountInfo) (*interfaces.Requirement, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "MatchRequirementConcept")
	defer span.End()

	target := edit.Target
	if target != nil {
		target = &interfaces.RequirementMatchTarget{Kind: target.Kind, ID: strings.TrimSpace(target.ID)}
		if target.ID == "" || (target.Kind != interfaces.REQUIREMENT_MATCH_TABLE &&
			target.Kind != interfaces.REQUIREMENT_MATCH_OBJECT_TYPE) {
			span.SetStatus(codes.Error, "Invalid target")
			return nil, invalidItem(ctx, "target is not a table or an object type")
		}
	}
	req, err := s.update(ctx, token, projectID, edit.UpdateTime, caller,
		func(spec *interfaces.RequirementSpec, drafted *draftedItems) error {
			i, ok := find(spec.Concepts, conceptID, func(c *interfaces.RequirementConcept) string { return c.ID })
			if !ok {
				return notFound(ctx)
			}
			concept := spec.Concepts[i]
			current := concept.Match
			if current == nil {
				current = &interfaces.RequirementMatch{}
			}
			switch {
			case edit.Excluded:
				// Left out, it keeps what it was matched to, so taking it back in restores that.
				next := *current
				next.Excluded = true
				concept.Match = &next
			case target != nil:
				if err := s.checkTarget(ctx, drafted, target, caller); err != nil {
					return err
				}
				next := &interfaces.RequirementMatch{Target: target, By: interfaces.REQUIREMENT_MATCH_BY_PERSON, Confirmed: true}
				if sameTarget(current.Target, target) {
					// Confirming what was proposed keeps who proposed it and why.
					next.By, next.Basis = current.By, current.Basis
				}
				if next.By == "" {
					next.By = interfaces.REQUIREMENT_MATCH_BY_PERSON
				}
				concept.Match = next
			default:
				concept.Match = &interfaces.RequirementMatch{By: interfaces.REQUIREMENT_MATCH_BY_PERSON}
			}
			if !concept.Match.Excluded {
				concept.MapsTo = mappedObjectType(concept.Match.Target, drafted)
			}
			concept.Edited = true
			followConcepts(spec)
			return nil
		})
	if err != nil {
		span.SetStatus(codes.Error, "Match concept failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return req, nil
}

// checkTarget checks that the target is data of the project: an object type of its draft, or a
// table of its catalogs as the caller lists them.
func (s *requirementService) checkTarget(ctx context.Context, drafted *draftedItems,
	target *interfaces.RequirementMatchTarget, caller interfaces.AccountInfo) error {
	if target.Kind == interfaces.REQUIREMENT_MATCH_OBJECT_TYPE {
		if !drafted.has(interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: target.ID}) {
			return invalidItem(ctx, "object type "+target.ID+" is not in the draft")
		}
		return nil
	}
	if mappedObjectType(target, drafted) != nil {
		return nil
	}
	tables, unlisted := s.listTables(ctx, s.catalogsOf(ctx, drafted.token, drafted.entry, caller), caller)
	if slices.ContainsFunc(tables, func(t *interfaces.VegaTable) bool { return t != nil && t.ID == target.ID }) {
		return nil
	}
	// A catalog that could not be listed may hold it: that is no wrong choice of the person's.
	if len(unlisted) > 0 {
		return internalError(ctx, "List the tables of the project's catalogs failed",
			fmt.Errorf("catalogs not listed: %s", strings.Join(unlisted, ", ")))
	}
	return invalidItem(ctx, "table "+target.ID+" is not in the project's catalogs")
}
