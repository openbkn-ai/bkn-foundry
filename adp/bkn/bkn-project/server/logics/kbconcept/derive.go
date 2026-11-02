// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbconcept

import (
	"encoding/json"
	"strings"

	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// DeriveObject creates a separate, unadopted view from a person's explicit field
// selection. It neither changes the original proposal nor invents extraction
// instructions, citations, relationships or field-authority decisions.
func DeriveObject(input *SampleInput, items interfaces.DraftItems, original *interfaces.DraftObjectType, request interfaces.DraftDocumentDerivation) (*interfaces.DraftObjectType, error) {
	if input == nil || original == nil || original.Document == nil || !request.Confirm ||
		original.Document.SelectionID != input.SelectionID || original.Document.SelectionRevision != input.SelectionRevision ||
		(original.Document.Role != "entity" && original.Document.Role != "file" && original.Document.Role != "in_document") ||
		(request.Role != "entity" && request.Role != "file") || len(request.Fields) == 0 || len(request.Fields) > 100 ||
		(request.Role == "entity" && (request.Identity == "" || !strings.HasSuffix(request.ID, "_doc"))) || (request.Role == "file" && request.Identity != "") {
		return nil, ErrInvalidModel
	}
	raw, err := json.Marshal(original)
	if err != nil {
		return nil, ErrInvalidModel
	}
	var source interfaces.DraftObjectType
	if json.Unmarshal(raw, &source) != nil {
		return nil, ErrInvalidModel
	}
	fields := []*interfaces.DraftProperty{}
	seen := map[string]bool{}
	for _, name := range request.Fields {
		p := property(&source, name)
		if p == nil || p.ReadOnly || seen[name] || p.Extraction == nil {
			return nil, ErrInvalidModel
		}
		seen[name] = true
		p.MappedField = ""
		p.Aliases = nil
		if err := hydrateQuotes(input, p.Extraction.Examples); err != nil {
			return nil, err
		}
		fields = append(fields, p)
	}
	if request.Role == "entity" && !seen[request.Identity] {
		return nil, ErrInvalidModel
	}
	if err := hydrateQuotes(input, source.Document.Citations); err != nil {
		return nil, err
	}
	model := &interfaces.DraftObjectType{
		ID: request.ID, Name: strings.TrimSpace(request.Name), Description: source.Description, Properties: fields,
		DisplayKey: fields[0].Name,
		Document:   &interfaces.DraftDocumentModel{Role: request.Role, Reason: source.Document.Reason, Citations: source.Document.Citations},
	}
	if request.Role == "entity" {
		model.PrimaryKeys = []string{request.Identity}
		model.DisplayKey = request.Identity
	}
	checked, err := ValidateModel(input, items, kb.ConceptProposal{ObjectTypes: []*interfaces.DraftObjectType{model}})
	if err != nil {
		return nil, err
	}
	return checked.ObjectTypes[0], nil
}
