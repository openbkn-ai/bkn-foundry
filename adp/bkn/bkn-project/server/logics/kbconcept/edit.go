// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbconcept

import (
	"encoding/json"
	"reflect"
	"slices"

	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// hydrateQuotes restores location-only editor proofs from the current verified
// debug input. It never alters stored metadata or broadens the sample scope.
func hydrateQuotes(input *SampleInput, quotes []interfaces.DraftSourceCitation) error {
	for i := range quotes {
		q := &quotes[i]
		found := false
		for _, fragment := range input.Request.Samples {
			p := fragment.Citation
			if p.KnowledgeBaseID != q.KnowledgeBaseID || p.FileID != q.FileID || p.ContentVersionID != q.ContentVersionID || p.BlockID != q.BlockID || q.Start < p.Start || q.End > p.End || q.End <= q.Start {
				continue
			}
			text := []rune(p.Quote)
			from, to := q.Start-p.Start, q.End-p.Start
			if from < 0 || to > len(text) {
				return ErrUnverifiedCitation
			}
			actual := string(text[from:to])
			if q.Quote != "" && q.Quote != actual {
				return ErrUnverifiedCitation
			}
			q.Quote = actual
			found = true
			break
		}
		if !found {
			return ErrUnverifiedCitation
		}
	}
	return nil
}

// ValidateEditedObject reuses model rules for a human field edit, but preserves
// prior adoption only when identity is complete. Managed columns remain fixed.
func ValidateEditedObject(input *SampleInput, items interfaces.DraftItems, original, candidate *interfaces.DraftObjectType) (*interfaces.DraftObjectType, error) {
	if input == nil || original == nil || candidate == nil || original.Document == nil || candidate.Document == nil || candidate.ID != original.ID || candidate.Document.Role != original.Document.Role || candidate.Document.Reason != original.Document.Reason {
		return nil, ErrInvalidModel
	}
	raw, err := json.Marshal(candidate)
	if err != nil {
		return nil, ErrInvalidModel
	}
	edited := &interfaces.DraftObjectType{}
	if json.Unmarshal(raw, edited) != nil {
		return nil, ErrInvalidModel
	}
	fixed := map[string]*interfaces.DraftProperty{}
	for _, p := range original.Properties {
		if p != nil && p.ReadOnly {
			fixed[p.Name] = p
		}
	}
	properties := []*interfaces.DraftProperty{}
	for _, p := range edited.Properties {
		if p == nil {
			return nil, ErrInvalidModel
		}
		if expected := fixed[p.Name]; expected != nil {
			if !reflect.DeepEqual(p, expected) {
				return nil, ErrInvalidModel
			}
			continue
		}
		if p.ReadOnly {
			return nil, ErrInvalidModel
		}
		if p.Extraction != nil {
			if err := hydrateQuotes(input, p.Extraction.Examples); err != nil {
				return nil, err
			}
		}
		properties = append(properties, p)
	}
	edited.Properties = properties
	edited.Aliases = nil
	for _, p := range edited.Properties {
		p.Aliases = nil
	}
	if err := hydrateQuotes(input, edited.Document.Citations); err != nil {
		return nil, err
	}
	existing := items
	existing.ObjectTypes = nil
	for _, object := range items.ObjectTypes {
		if object != nil && object.ID != original.ID {
			existing.ObjectTypes = append(existing.ObjectTypes, object)
		}
	}
	checked, err := ValidateModel(input, existing, kb.ConceptProposal{ObjectTypes: []*interfaces.DraftObjectType{edited}})
	if err != nil {
		return nil, err
	}
	result := checked.ObjectTypes[0]
	result.Aliases = append([]string(nil), original.Aliases...)
	if slices.Equal(result.PrimaryKeys, original.PrimaryKeys) {
		result.Key = original.Key
	}
	for _, p := range result.Properties {
		if old := property(original, p.Name); old != nil {
			p.Aliases = append([]string(nil), old.Aliases...)
		}
	}
	// Every existing join remains valid after removal, type and identity edits.
	objects := map[string]*interfaces.DraftObjectType{result.ID: result}
	for _, object := range existing.ObjectTypes {
		if object != nil {
			objects[object.ID] = object
		}
	}
	for _, relation := range items.RelationTypes {
		if relation == nil || (relation.Source != result.ID && relation.Target != result.ID) {
			continue
		}
		source, target := objects[relation.Source], objects[relation.Target]
		if source == nil || target == nil {
			return nil, ErrInvalidModel
		}
		for _, mapping := range relation.Mapping {
			a, b := property(source, mapping.SourceProperty), property(target, mapping.TargetProperty)
			if a == nil || b == nil || a.Type != b.Type {
				return nil, ErrInvalidModel
			}
		}
		if m := relation.DocumentMapping; m != nil && m.Mode == "linking" && relation.Target == result.ID {
			if len(result.PrimaryKeys) != 1 || result.PrimaryKeys[0] != m.IdentityProperty {
				return nil, ErrInvalidModel
			}
		}
	}
	result.Adopted = original.Adopted && !result.NeedsIdentity
	return result, nil
}

// ValidateEditedRelation keeps source proof server-owned and rechecks both
// mapping modes without table reads or instance-link counts.
func ValidateEditedRelation(input *SampleInput, items interfaces.DraftItems, original, candidate *interfaces.DraftRelationType) (*interfaces.DraftRelationType, error) {
	if input == nil || original == nil || candidate == nil || original.Document == nil || original.Document.Role != "relation" || candidate.Document == nil || candidate.ID != original.ID || candidate.Document.Role != original.Document.Role || candidate.Document.Reason != original.Document.Reason {
		return nil, ErrInvalidModel
	}
	raw, err := json.Marshal(candidate)
	if err != nil {
		return nil, ErrInvalidModel
	}
	edited := &interfaces.DraftRelationType{}
	if json.Unmarshal(raw, edited) != nil {
		return nil, ErrInvalidModel
	}
	edited.Aliases = nil
	if err := hydrateQuotes(input, edited.Document.Citations); err != nil {
		return nil, err
	}
	existing := items
	existing.RelationTypes = nil
	for _, relation := range items.RelationTypes {
		if relation != nil && relation.ID != original.ID {
			existing.RelationTypes = append(existing.RelationTypes, relation)
		}
	}
	checked, err := ValidateModel(input, existing, kb.ConceptProposal{RelationTypes: []*interfaces.DraftRelationType{edited}})
	if err != nil {
		return nil, err
	}
	result := checked.RelationTypes[0]
	for _, other := range existing.RelationTypes {
		if other != nil && other.Source == result.Source && other.Target == result.Target && reflect.DeepEqual(other.Mapping, result.Mapping) {
			return nil, ErrInvalidModel
		}
	}

	result.Aliases = append([]string(nil), original.Aliases...)
	result.Origin = original.Origin
	result.ProposedBecause = original.ProposedBecause
	result.Adopted = original.Adopted
	result.Verification = "confirmed"
	for _, object := range items.ObjectTypes {
		if object == nil || object.Document == nil || object.Document.Role != "event" {
			continue
		}
		for _, id := range object.Document.ParticipantRelations {
			if id == result.ID && (result.Source == result.Target || (result.Source != object.ID && result.Target != object.ID)) {
				return nil, ErrInvalidModel
			}
		}
	}
	return result, nil
}
