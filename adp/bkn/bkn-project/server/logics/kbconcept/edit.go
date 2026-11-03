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
	if edited.Document.DualSource != nil && !reflect.DeepEqual(edited.Document.DualSource, original.Document.DualSource) {
		return nil, ErrInvalidModel
	}
	edited.Document.DualSource = nil
	if !reflect.DeepEqual(edited.Document.FactResolution, original.Document.FactResolution) {
		return nil, ErrInvalidModel
	}
	edited.Document.FactResolution = nil
	if !reflect.DeepEqual(edited.Document.FilePeriod, original.Document.FilePeriod) {
		return nil, ErrInvalidModel
	}
	edited.Document.FilePeriod = nil
	if !reflect.DeepEqual(edited.Document.FileIdentity, original.Document.FileIdentity) {
		return nil, ErrInvalidModel
	}
	edited.Document.FileIdentity = nil
	if !reflect.DeepEqual(edited.Document.SentenceIdentity, original.Document.SentenceIdentity) {
		return nil, ErrInvalidModel
	}
	edited.Document.SentenceIdentity = nil
	if edited.Table.ResourceID != "" && !reflect.DeepEqual(edited.Table, original.Table) {
		return nil, ErrInvalidModel
	}
	// Storage is derived from the confirmed model. Revalidate the editable
	// logical definition, then let the next confirmation prepare its generation.
	edited.Table = interfaces.DraftTable{}
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
		if p.MappedField != "" {
			old := property(original, p.Name)
			if old == nil || p.MappedField != old.MappedField {
				return nil, ErrInvalidModel
			}
			p.MappedField = ""
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
	if proof := original.Document.FactResolution; ValidFactResolution(items, result, proof) {
		copy := *proof
		result.Document.FactResolution = &copy
	}
	if proof := original.Document.FilePeriod; ValidFilePeriod(original, proof) && ValidFilePeriod(result, proof) {
		unchanged := true
		for _, name := range []string{proof.From, proof.Until} {
			if name != "" && property(original, name).Type != property(result, name).Type {
				unchanged = false
			}
		}
		if unchanged {
			copy := *proof
			result.Document.FilePeriod = &copy
		}
	}
	if proof := original.Document.FileIdentity; proof != nil && fileKeyField(result, proof.Property) {
		old, current := property(original, proof.Property), property(result, proof.Property)
		if old != nil && current != nil && old.Type == current.Type {
			copy := *proof
			result.Document.FileIdentity = &copy
		}
	}
	if proof := original.Document.SentenceIdentity; proof != nil {
		valid := true
		for _, name := range proof.Properties {
			old, current := property(original, name), property(result, name)
			if old == nil || current == nil || old.Type != current.Type || !sentenceKeyField(result, name) {
				valid = false
			}
		}
		if valid {
			copy := *proof
			copy.Properties = slices.Clone(proof.Properties)
			result.Document.SentenceIdentity = &copy
		}
	}
	if original.Document.DualSource != nil {
		encoded, _ := json.Marshal(original.Document.DualSource)
		result.Document.DualSource = &interfaces.DraftDualSourcePlan{}
		_ = json.Unmarshal(encoded, result.Document.DualSource)
	}
	result.Aliases = append([]string(nil), original.Aliases...)
	if slices.Equal(result.PrimaryKeys, original.PrimaryKeys) {
		result.Key = original.Key
	}
	for _, p := range result.Properties {
		if old := property(original, p.Name); old != nil {
			p.Aliases = append([]string(nil), old.Aliases...)
			if p.Type == old.Type && (old.SourceOfRecord == "document" || old.SourceOfRecord == "fusion") {
				p.SourceOfRecord = old.SourceOfRecord
			}
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
		if relation.DocumentRepair != "" {
			// A pending scope/identity repair does not freeze harmless object
			// edits, but its referenced fields must remain grounded and typed.
			m := relation.DocumentMapping
			if m == nil {
				return nil, ErrInvalidModel
			}
			var from, to *interfaces.DraftProperty
			switch m.Mode {
			case "linking":
				from, to = property(source, m.MentionProperty), property(target, m.IdentityProperty)
			case "same_document":
				from, to = property(source, m.SourceNameProperty), property(target, m.TargetNameProperty)
			default:
				return nil, ErrInvalidModel
			}
			if from == nil || to == nil || from.ReadOnly || from.Type != to.Type {
				return nil, ErrInvalidModel
			}
			continue
		}
		if m := relation.DocumentMapping; m != nil && m.Mode == "same_document" && (!fileScopedObject(source) || !fileScopedObject(target)) {
			return nil, &RelationScopeError{ID: relation.ID, Name: relation.Name}
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
	return validateEditedRelation(input, items, original, candidate, false)
}

// ReviewRelationForAdvice permits only a previously blocked, grounded base to
// reach the read-only assistant. Suggestions and saves still validate strictly.
func ReviewRelationForAdvice(input *SampleInput, items interfaces.DraftItems, original, candidate *interfaces.DraftRelationType) (*interfaces.DraftRelationType, error) {
	if original == nil || original.DocumentRepair == "" {
		return nil, ErrInvalidModel
	}
	return validateEditedRelation(input, items, original, candidate, true)
}

func validateEditedRelation(input *SampleInput, items interfaces.DraftItems, original, candidate *interfaces.DraftRelationType, review bool) (*interfaces.DraftRelationType, error) {

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
	if (edited.LinkTable != nil && !reflect.DeepEqual(edited.LinkTable, original.LinkTable)) ||
		(len(edited.SourceMapping) > 0 && !reflect.DeepEqual(edited.SourceMapping, original.SourceMapping)) ||
		(len(edited.TargetMapping) > 0 && !reflect.DeepEqual(edited.TargetMapping, original.TargetMapping)) ||
		(len(edited.Properties) > 0 && !reflect.DeepEqual(edited.Properties, original.Properties)) {
		return nil, ErrInvalidModel
	}
	edited.LinkTable = nil
	edited.SourceMapping = nil
	edited.TargetMapping = nil
	edited.Properties = nil
	// The marker belongs to the stored proposal. Only this strict revalidation
	// may clear it; clients cannot declare their own proposal repaired.
	edited.DocumentRepair = ""
	if edited.DocumentMapping != nil {
		if edited.DocumentMapping.BusinessIdentity != nil && (original.DocumentMapping == nil || !reflect.DeepEqual(edited.DocumentMapping.BusinessIdentity, original.DocumentMapping.BusinessIdentity)) {
			return nil, ErrInvalidModel
		}
		edited.DocumentMapping.BusinessIdentity = nil
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
	checked, err := validateModel(input, existing, kb.ConceptProposal{RelationTypes: []*interfaces.DraftRelationType{edited}}, review)
	if err != nil {
		return nil, err
	}
	result := checked.RelationTypes[0]
	if original.DocumentMapping != nil && original.DocumentMapping.BusinessIdentity != nil {
		proof := *original.DocumentMapping.BusinessIdentity
		result.DocumentMapping.BusinessIdentity = &proof
		if !businessIdentityReady(items, result) {
			result.DocumentMapping.BusinessIdentity = nil
		}
	}
	for _, other := range existing.RelationTypes {
		if other != nil && other.Source == result.Source && other.Target == result.Target && reflect.DeepEqual(other.Mapping, result.Mapping) {
			return nil, ErrInvalidModel
		}
	}

	result.Aliases = append([]string(nil), original.Aliases...)
	result.Origin = original.Origin
	result.ProposedBecause = original.ProposedBecause
	result.Adopted = original.Adopted && result.DocumentRepair == ""
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
