// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// A person adds a relation when object types are left unconnected. An isolated object type is an
// adopted one no adopted relation type has as an end. For one of them, the candidate relations
// are drafting's own, by the same rules, without the gates that left them out: every field's
// every match, a stem in another catalog, an inference the counts refute, each with its counts.
// Nothing is adopted or written; a person makes the relation they choose.

const (
	// suggestionsMaxCounts bounds the candidates counted for one object type, the likelier first.
	suggestionsMaxCounts = 10
	// suggestionsCountTimeout bounds all the counts of one object type's candidates.
	suggestionsCountTimeout = 30 * time.Second
	// reasonNotCounted is a candidate past the counts one object type takes, or their time.
	reasonNotCounted = "not_counted"
	// notAdopted is why an object type no one adopted has no suggestions.
	notAdopted = "not_adopted"
)

// isolatedObjectTypes are the adopted object types no adopted relation type has as an end.
func isolatedObjectTypes(items *interfaces.DraftItems) []interfaces.ReleaseItem {
	connected := map[string]bool{}
	for _, rt := range items.RelationTypes {
		if rt.Adopted {
			connected[rt.Source], connected[rt.Target] = true, true
		}
	}
	isolated := []interfaces.ReleaseItem{}
	for _, ot := range items.ObjectTypes {
		if ot.Adopted && !connected[ot.ID] {
			isolated = append(isolated, interfaces.ReleaseItem{Kind: interfaces.RELEASE_ITEM_OBJECT_TYPE, ID: ot.ID, Name: ot.Name})
		}
	}
	return isolated
}

func (s *draftService) readyDraftToRead(ctx context.Context, token string, projectID string) (*interfaces.Draft, error) {
	draft, err := s.GetDraft(ctx, token, projectID)
	if err != nil {
		return nil, err
	}
	if draft.Status != interfaces.DRAFT_STATUS_READY {
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Draft_NotReady)
	}
	return draft, nil
}

func (s *draftService) IsolatedObjectTypes(ctx context.Context, token string, projectID string) (*interfaces.DraftIsolated, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "IsolatedObjectTypes")
	defer span.End()

	draft, err := s.readyDraftToRead(ctx, token, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Draft not readable")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return &interfaces.DraftIsolated{Isolated: isolatedObjectTypes(&draft.Items)}, nil
}

func (s *draftService) RelationSuggestions(ctx context.Context, token string, projectID string, objectTypeID string,
	caller interfaces.AccountInfo) (*interfaces.DraftRelationSuggestions, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "RelationSuggestions")
	defer span.End()

	draft, ot, err := s.suggestingFor(ctx, token, projectID, objectTypeID)
	if err != nil {
		span.SetStatus(codes.Error, "No object type to suggest for")
		return nil, err
	}
	suggestions, err := s.suggestionsOf(ctx, draft, ot, caller)
	if err != nil {
		span.SetStatus(codes.Error, "Read tables failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return suggestions, nil
}

// suggestingFor reads the ready draft and the adopted object type suggestions are for.
func (s *draftService) suggestingFor(ctx context.Context, token string, projectID string,
	objectTypeID string) (*interfaces.Draft, *interfaces.DraftObjectType, error) {
	draft, err := s.readyDraftToRead(ctx, token, projectID)
	if err != nil {
		return nil, nil, err
	}
	ot := findObjectType(&draft.Items, objectTypeID)
	switch {
	case ot == nil:
		return nil, nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Draft_ItemNotFound)
	case !ot.Adopted:
		return nil, nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Draft_InvalidItem).
			WithErrorDetails(map[string]any{"reason": notAdopted})
	}
	return draft, ot, nil
}

func (s *draftService) suggestionsOf(ctx context.Context, draft *interfaces.Draft, ot *interfaces.DraftObjectType,
	caller interfaces.AccountInfo) (*interfaces.DraftRelationSuggestions, error) {
	candidates, err := s.relationCandidates(ctx, draft, ot, caller)
	if err != nil {
		return nil, err
	}
	return &interfaces.DraftRelationSuggestions{
		ObjectType: interfaces.ReleaseItem{Kind: interfaces.RELEASE_ITEM_OBJECT_TYPE, ID: ot.ID, Name: ot.Name},
		Candidates: candidates,
	}, nil
}

// pairCandidate is one field of a source referring to a target's single-field identity.
type pairCandidate struct {
	source, target           *interfaces.DraftObjectType
	sourceTable, targetTable *interfaces.VegaTable
	field, key               string
	origin                   string
	rank                     int
}

// relationCandidates are the relations an object type could take: those drafted and not
// adopted, then drafting's candidates in both directions with their counts.
func (s *draftService) relationCandidates(ctx context.Context, draft *interfaces.Draft, ot *interfaces.DraftObjectType,
	caller interfaces.AccountInfo) ([]interfaces.DraftRelationCandidate, error) {
	items := &draft.Items
	candidates := []interfaces.DraftRelationCandidate{}
	adopted := map[string]bool{}
	for _, o := range items.ObjectTypes {
		adopted[o.ID] = o.Adopted
	}
	for _, rt := range items.RelationTypes {
		if rt.Adopted || (rt.Source != ot.ID && rt.Target != ot.ID) || !adopted[rt.Source] || !adopted[rt.Target] ||
			rt.LinkTable != nil {
			continue
		}
		candidates = append(candidates, relationCandidateOf(rt, interfaces.DRAFT_RELATION_CANDIDATE_DRAFTED, rt.ID, rt.Name, rt.Evidence))
	}

	ids := []string{}
	for _, o := range items.ObjectTypes {
		if o.Adopted {
			ids = append(ids, o.Table.ResourceID)
		}
	}
	tables, err := s.va.GetTables(ctx, caller, ids)
	if err != nil {
		return nil, internalError(ctx, "Read tables failed", err)
	}
	countCtx, cancel := context.WithTimeout(ctx, suggestionsCountTimeout)
	defer cancel()
	timeout := min(s.setting.QueryTimeout, int(suggestionsCountTimeout.Seconds()))
	d := newDrafter(s.setting, &vegaCounter{va: s.va, account: caller, timeoutSeconds: timeout})
	d.namingLanguage = draftLanguage(draft)
	byTable := map[string]*interfaces.VegaTable{}
	for _, table := range tables {
		byTable[table.ID] = table
	}
	for _, o := range items.ObjectTypes {
		table := byTable[o.Table.ResourceID]
		if table == nil || !o.Adopted {
			continue
		}
		d.kinds[table.ID] = tableKindEntity
		d.objectTypes[table.ID] = o
		d.fields[table.ID] = map[string]string{}
		for _, p := range o.Properties {
			d.fields[table.ID][p.MappedField] = p.Name
		}
		if !o.NeedsIdentity {
			for _, key := range o.PrimaryKeys {
				d.keys[table.ID] = append(d.keys[table.ID], mappedField(o, key))
			}
		}
		d.bySourceName[sourceName(table.CatalogID, table.SourceSchema, table.SourceName)] = table
	}
	self := byTable[ot.Table.ResourceID]
	if self == nil {
		return candidates, nil
	}

	pairs := []pairCandidate{}
	for _, table := range tables {
		other := d.objectTypes[table.ID]
		if other == nil || other.ID == ot.ID {
			continue
		}
		pairs = append(pairs, d.pairCandidates(self, table)...)
		pairs = append(pairs, d.pairCandidates(table, self)...)
	}
	// The likelier first: declared keys, then names, then stems, each by how surely it matches.
	originRank := map[string]int{interfaces.DRAFT_ORIGIN_FOREIGN_KEY: 0, interfaces.DRAFT_ORIGIN_NAME_MATCH: 1,
		interfaces.DRAFT_ORIGIN_STEM_MATCH: 2}
	slices.SortStableFunc(pairs, func(a, b pairCandidate) int {
		return cmp.Or(cmp.Compare(originRank[a.origin], originRank[b.origin]), cmp.Compare(b.rank, a.rank))
	})

	relationIDs := map[string]bool{}
	for _, r := range items.RelationTypes {
		relationIDs[r.ID] = true
	}
	counted := 0
	found := []interfaces.DraftRelationCandidate{}
	for _, p := range pairs {
		property, targetKey := d.fields[p.sourceTable.ID][p.field], d.fields[p.targetTable.ID][p.key]
		relation := &interfaces.DraftRelationType{Source: p.source.ID, Target: p.target.ID,
			Mapping: []interfaces.DraftFieldMapping{{SourceProperty: property, TargetProperty: targetKey}}}
		// A relation the draft has, adopted or listed above, is not suggested again.
		if hasRelation(items, relation) {
			continue
		}
		evidence := interfaces.DraftJoinEvidence{}
		switch {
		case p.sourceTable.CatalogID != p.targetTable.CatalogID:
			evidence.Reason = reasonCrossCatalog
		case counted == suggestionsMaxCounts || countCtx.Err() != nil:
			evidence.Reason = reasonNotCounted
		default:
			counted++
			rows, nulls, orphans, distinct, err := d.counter.joinStats(countCtx, p.sourceTable, p.field, p.targetTable, p.key)
			if err != nil {
				evidence.Reason = fmt.Sprintf("%s: %v", reasonQueryFailed, err)
				if countCtx.Err() != nil {
					evidence.Reason = reasonNotCounted
				}
			} else {
				evidence = joinEvidence(rows, nulls, orphans)
				evidence.SourceDistinct = distinct
			}
		}
		id := uniqueName(idFrom(p.source.ID+"_to_"+p.target.ID, idPrefixRelationType), relationIDs)
		relationIDs[id] = true
		relation.ID = id
		found = append(found, relationCandidateOf(relation, p.origin, "", d.relationName(p.source, property, id), evidence))
	}
	// Counted and found first, by their hit rate; what could not be counted after.
	slices.SortStableFunc(found, func(a, b interfaces.DraftRelationCandidate) int {
		switch {
		case a.Origin == interfaces.DRAFT_ORIGIN_FOREIGN_KEY && b.Origin != interfaces.DRAFT_ORIGIN_FOREIGN_KEY:
			return -1
		case b.Origin == interfaces.DRAFT_ORIGIN_FOREIGN_KEY && a.Origin != interfaces.DRAFT_ORIGIN_FOREIGN_KEY:
			return 1
		case a.Evidence.Computed != b.Evidence.Computed:
			if a.Evidence.Computed {
				return -1
			}
			return 1
		}
		return cmp.Compare(b.Evidence.HitRate, a.Evidence.HitRate)
	})
	return append(candidates, found...), nil
}

// pairCandidates are the fields of source pointing at target's single-field identity, as drafting
// finds them: a declared key, a name, or a stem, in any catalog, each field with each match.
func (d *drafter) pairCandidates(source *interfaces.VegaTable, target *interfaces.VegaTable) []pairCandidate {
	sourceType, targetType := d.objectTypes[source.ID], d.objectTypes[target.ID]
	targetKeys := d.keys[target.ID]
	if sourceType == nil || targetType == nil || len(targetKeys) != 1 {
		return nil
	}
	key := targetKeys[0]
	keyColumn, ok := column(target, key)
	if !ok {
		return nil
	}
	inKey := map[string]bool{}
	for _, field := range d.keys[source.ID] {
		inKey[field] = true
	}
	declared := map[string]bool{}
	for _, fk := range source.ForeignKeys {
		if len(fk.Columns) == 1 && len(fk.RefColumns) == 1 && d.target(source, fk) == target && fk.RefColumns[0] == key {
			declared[fk.Columns[0]] = true
		}
	}
	pairs := []pairCandidate{}
	for _, c := range source.Columns {
		if _, mapped := d.fields[source.ID][c.Name]; !mapped || inKey[c.Name] || c.Type != keyColumn.Type {
			continue
		}
		p := pairCandidate{source: sourceType, target: targetType, sourceTable: source, targetTable: target, field: c.Name, key: key}
		switch {
		case declared[c.Name]:
			p.origin, p.rank = interfaces.DRAFT_ORIGIN_FOREIGN_KEY, 0
		case nameMatch(c.Name, target, key) > 0:
			p.origin, p.rank = interfaces.DRAFT_ORIGIN_NAME_MATCH, nameMatch(c.Name, target, key)
		case d.stemMatch(sourceType, c.Name, target, key) > 0:
			p.origin, p.rank = interfaces.DRAFT_ORIGIN_STEM_MATCH, d.stemMatch(sourceType, c.Name, target, key)
		default:
			continue
		}
		pairs = append(pairs, p)
	}
	return pairs
}

// relationCandidateOf is a relation as the save body of a new relation has it, with how it was found.
func relationCandidateOf(rt *interfaces.DraftRelationType, origin string, relationTypeID string, name string,
	evidence interfaces.DraftJoinEvidence) interfaces.DraftRelationCandidate {
	candidate := interfaces.DraftRelationCandidate{SourceObjectTypeID: rt.Source, TargetObjectTypeID: rt.Target,
		Type: interfaces.DRAFT_RELATION_DIRECT, MappingRules: []interfaces.DraftRelationMapping{}, Name: name,
		Origin: origin, RelationTypeID: relationTypeID, Evidence: evidence}
	for _, m := range rt.Mapping {
		candidate.MappingRules = append(candidate.MappingRules, interfaces.DraftRelationMapping{
			SourceProperty: interfaces.DraftMappedField{Name: m.SourceProperty},
			TargetProperty: interfaces.DraftMappedField{Name: m.TargetProperty}})
	}
	return candidate
}
