// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbconcept

import (
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/locale"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
)

var ErrInvalidModel = errors.New("document model proposal failed validation")
var modelID = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)
var propertyTypes = map[string]bool{"string": true, "text": true, "integer": true, "unsigned integer": true, "float": true, "decimal": true, "boolean": true, "date": true, "time": true, "datetime": true, "timestamp": true, "json": true}
var temporalTypes = map[string]bool{"date": true, "time": true, "datetime": true, "timestamp": true}
var roleLayers = map[string]string{"entity": "entity", "file": "fact", "in_document": "fact", "fact": "fact", "event": "fact", "relation": "fact"}

// ValidatedModel is checked proposal data, still awaiting a human decision.
// No proposal is adopted by validation and no Dataset or draft is written here.
type ValidatedModel struct {
	ObjectTypes   []*interfaces.DraftObjectType
	RelationTypes []*interfaces.DraftRelationType
}

func boundedText(text string, minimum, maximum int) bool {
	return utf8.ValidString(text) && utf8.RuneCountInString(strings.TrimSpace(text)) >= minimum && utf8.RuneCountInString(text) <= maximum
}
func property(object *interfaces.DraftObjectType, name string) *interfaces.DraftProperty {
	for _, p := range object.Properties {
		if p != nil && p.Name == name {
			return p
		}
	}
	return nil
}

// RelationScopeError identifies a legacy join that a person must repair before
// editing either endpoint. Its text carries no source content or internal id.
type RelationScopeError struct {
	ID   string
	Name string
}

func (*RelationScopeError) Error() string {
	return "same-document relation has a global entity endpoint"
}
func (*RelationScopeError) Unwrap() error { return ErrInvalidModel }

// File-scoped claims have one document identity. A canonical entity can merge
// properties from several files, so its document_id cannot define this join.
func fileScopedObject(object *interfaces.DraftObjectType) bool {
	if object == nil || object.Document == nil {
		return false
	}
	switch object.Document.Role {
	case "file", "in_document", "fact", "event":
		return true
	default:
		return false
	}
}

func validatedQuotes(input *SampleInput, quotes []interfaces.DraftSourceCitation) ([]interfaces.DraftSourceCitation, error) {
	refs := make([]kb.Citation, 0, len(quotes))
	for _, q := range quotes {
		refs = append(refs, kb.Citation{KnowledgeBaseID: q.KnowledgeBaseID, VersionRef: kb.VersionRef{FileID: q.FileID, ContentVersionID: q.ContentVersionID}, BlockID: q.BlockID, Start: q.Start, End: q.End, Quote: q.Quote})
	}
	checked, err := ValidateCitations(input, refs)
	if err != nil {
		return nil, err
	}
	result := make([]interfaces.DraftSourceCitation, 0, len(checked))
	for _, q := range checked {
		result = append(result, interfaces.DraftSourceCitation{KnowledgeBaseID: q.KnowledgeBaseID, FileID: q.FileID, ContentVersionID: q.ContentVersionID, BlockID: q.BlockID, Start: q.Start, End: q.End, Quote: q.Quote})
	}
	return result, nil
}
func validateDocument(input *SampleInput, model *interfaces.DraftDocumentModel, relation bool) error {
	if model == nil || !boundedText(model.Reason, 1, 1000) {
		return ErrInvalidModel
	}
	layer, ok := roleLayers[model.Role]
	if !ok || (model.Role == "relation") != relation || (model.Layer != "" && model.Layer != layer) {
		return ErrInvalidModel
	}
	quotes, err := validatedQuotes(input, model.Citations)
	if err != nil {
		return err
	}
	model.Layer, model.SelectionID, model.SelectionRevision, model.Citations = layer, input.SelectionID, input.SelectionRevision, quotes
	return nil
}

type managedField struct{ Name, Type, Label string }

var commonFields = []managedField{
	{"_id", "string", "Record identifier"}, {"entity_id", "string", "Entity identifier"}, {"document_id", "string", "Document"},
	{"block_id", "string", "Source block"}, {"content_version_id", "string", "Content version"}, {"source_quote", "text", "Source quote"},
	{"source_start", "integer", "Quote start"}, {"source_end", "integer", "Quote end"}, {"extraction_version", "string", "Extraction version"},
}
var factFields = []managedField{{"effective_from", "datetime", "Effective from"}, {"effective_to", "datetime", "Effective to"}, {"status", "string", "Status"}, {"recorded_at", "datetime", "Recorded at"}}

func managedName(name, role string) bool {
	lists := [][]managedField{commonFields}
	if role == "fact" || role == "event" {
		lists = append(lists, factFields)
	}
	for _, list := range lists {
		for _, f := range list {
			if f.Name == name {
				return true
			}
		}
	}
	return false
}
func appendManaged(object *interfaces.DraftObjectType, language string) {
	locale.Register()
	fields := slices.Clone(commonFields)
	if object.Document.Role == "fact" || object.Document.Role == "event" {
		fields = append(fields, factFields...)
	}
	for _, f := range fields {
		label, err := i18n.Render(i18n.Language(language), "BknProject.Draft.DocumentField."+f.Name, nil)
		if err != nil {
			label = f.Label
		}
		object.Properties = append(object.Properties, &interfaces.DraftProperty{Name: f.Name, DisplayName: label, Type: f.Type, SourceOfRecord: "document", ReadOnly: true})
	}
}

// ValidateModel checks the whole proposal before it can enter the existing
// candidate list. It returns a separate copy, so transient quotes remain usable
// during this request while the returned data follows source retention policy.
func ValidateModel(input *SampleInput, existing interfaces.DraftItems, proposal kb.ConceptProposal) (*ValidatedModel, error) {
	if input == nil || input.SelectionID == "" || input.SelectionRevision < 1 || len(proposal.ObjectTypes) > 30 || len(proposal.RelationTypes) > 60 {
		return nil, ErrInvalidModel
	}
	raw, err := json.Marshal(proposal)
	if err != nil || len(raw) > 1<<20 {
		return nil, ErrInvalidModel
	}
	var copied kb.ConceptProposal
	if json.Unmarshal(raw, &copied) != nil {
		return nil, ErrInvalidModel
	}
	objects := map[string]*interfaces.DraftObjectType{}
	for _, object := range existing.ObjectTypes {
		if object != nil {
			objects[object.ID] = object
		}
	}
	properties := 0
	for _, object := range copied.ObjectTypes {
		if object == nil || !modelID.MatchString(object.ID) || objects[object.ID] != nil || !boundedText(object.Name, 1, 128) || !boundedText(object.Description, 0, 1000) || object.Table.ResourceID != "" || object.Table.CatalogID != "" || len(object.Aliases) > 0 || len(object.Properties) == 0 || len(object.Properties) > 100 || len(object.PrimaryKeys) > 5 {
			return nil, ErrInvalidModel
		}
		if err := validateDocument(input, object.Document, false); err != nil {
			return nil, err
		}
		seen := map[string]bool{}
		for _, p := range object.Properties {
			if p == nil || !modelID.MatchString(p.Name) || managedName(p.Name, object.Document.Role) || seen[p.Name] || p.ReadOnly || !propertyTypes[p.Type] || p.MappedField != "" || p.ValueTypeID != "" || !boundedText(p.DisplayName, 1, 128) || !boundedText(p.Description, 0, 1000) || p.Extraction == nil || !boundedText(p.Extraction.Instruction, 1, 1000) || len(p.Extraction.Examples) > 3 || len(p.Aliases) > 0 {
				return nil, ErrInvalidModel
			}
			examples, err := validatedQuotes(input, p.Extraction.Examples)
			if err != nil {
				return nil, err
			}
			p.Extraction.Examples = examples
			p.SourceOfRecord = "document"
			seen[p.Name] = true
			properties++
		}
		if properties > 500 {
			return nil, ErrInvalidModel
		}
		object.Table = interfaces.DraftTable{}
		object.Adopted = false
		object.Key = interfaces.DraftKeyEvidence{Source: "document"}
		object.IncrementalKey = ""
		object.AddedByCatalog = ""
		if object.DisplayKey != "" && !seen[object.DisplayKey] {
			return nil, ErrInvalidModel
		}
		if object.DisplayKey == "" {
			object.DisplayKey = object.Properties[0].Name
		}
		if object.Document.Role == "entity" {
			keys := map[string]bool{}
			for _, name := range object.PrimaryKeys {
				p := property(object, name)
				if p == nil || keys[name] || !(p.Type == "string" || p.Type == "text" || p.Type == "integer" || p.Type == "unsigned integer") {
					return nil, ErrInvalidModel
				}
				keys[name] = true
			}
			object.NeedsIdentity = len(object.PrimaryKeys) == 0
		} else {
			object.PrimaryKeys = []string{"_id"}
			object.NeedsIdentity = false
		}
		if object.Document.Role == "event" {
			p := property(object, object.Document.TimeProperty)
			if p == nil || !temporalTypes[p.Type] || len(object.Document.ParticipantRelations) == 0 || len(object.Document.ParticipantRelations) > 60 {
				return nil, ErrInvalidModel
			}
		}
		if object.Document.Role != "event" && (object.Document.TimeProperty != "" || len(object.Document.ParticipantRelations) > 0) {
			return nil, ErrInvalidModel
		}
		appendManaged(object, input.Request.NamingLanguage)
		objects[object.ID] = object
	}
	relations := map[string]*interfaces.DraftRelationType{}
	for _, r := range existing.RelationTypes {
		if r != nil {
			relations[r.ID] = r
		}
	}
	for _, r := range copied.RelationTypes {
		if r == nil || !modelID.MatchString(r.ID) || relations[r.ID] != nil || !boundedText(r.Name, 1, 128) || !boundedText(r.Description, 0, 1000) || len(r.Aliases) > 0 || r.LinkTable != nil || len(r.SourceMapping) > 0 || len(r.TargetMapping) > 0 || r.DocumentMapping == nil {
			return nil, ErrInvalidModel
		}
		if r.Document != nil && (r.Document.TimeProperty != "" || len(r.Document.ParticipantRelations) > 0) {
			return nil, ErrInvalidModel
		}
		if r.Cardinality != "one_to_one" && r.Cardinality != "one_to_many" && r.Cardinality != "many_to_one" && r.Cardinality != "many_to_many" {
			return nil, ErrInvalidModel
		}
		if err := validateDocument(input, r.Document, true); err != nil {
			return nil, err
		}
		source, target := objects[r.Source], objects[r.Target]
		if source == nil || target == nil || source.Document == nil {
			return nil, ErrInvalidModel
		}
		m := r.DocumentMapping
		switch m.Mode {
		case "linking":
			from, to := property(source, m.MentionProperty), property(target, m.IdentityProperty)
			if (r.Source == r.Target && m.MentionProperty == m.IdentityProperty) || from == nil || to == nil || from.ReadOnly || len(target.PrimaryKeys) != 1 || target.PrimaryKeys[0] != m.IdentityProperty || from.Type != to.Type || m.SourceNameProperty != "" || m.TargetNameProperty != "" {
				return nil, ErrInvalidModel
			}
			r.Mapping = []interfaces.DraftFieldMapping{{SourceProperty: m.MentionProperty, TargetProperty: m.IdentityProperty}}
		case "same_document":
			from, to := property(source, m.SourceNameProperty), property(target, m.TargetNameProperty)
			if !fileScopedObject(source) || !fileScopedObject(target) || (r.Source == r.Target && m.SourceNameProperty == m.TargetNameProperty) || from == nil || to == nil || from.ReadOnly || to.ReadOnly || (from.Type != "string" && from.Type != "text") || from.Type != to.Type || m.MentionProperty != "" || m.IdentityProperty != "" {
				return nil, ErrInvalidModel
			}
			r.Mapping = []interfaces.DraftFieldMapping{{SourceProperty: "document_id", TargetProperty: "document_id"}, {SourceProperty: m.SourceNameProperty, TargetProperty: m.TargetNameProperty}}
		default:
			return nil, ErrInvalidModel
		}
		r.Adopted = false
		r.Origin = "document"
		r.Verification = "unconfirmed"
		r.Evidence = interfaces.DraftJoinEvidence{}
		r.ProposedBecause = "human_review"
		r.AddedByCatalog = ""
		relations[r.ID] = r
	}
	for _, object := range copied.ObjectTypes {
		if object.Document.Role != "event" {
			continue
		}
		seen := map[string]bool{}
		for _, id := range object.Document.ParticipantRelations {
			r := relations[id]
			if r == nil || (r.Document != nil && r.Document.Role != "relation") || seen[id] || (r.Source == object.ID && r.Target == object.ID) || (r.Source != object.ID && r.Target != object.ID) {
				return nil, ErrInvalidModel
			}
			seen[id] = true
		}
	}
	return &ValidatedModel{ObjectTypes: copied.ObjectTypes, RelationTypes: copied.RelationTypes}, nil
}
