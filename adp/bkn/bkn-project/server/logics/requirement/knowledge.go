// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"context"

	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/draft"
)

// The knowledge of a project is what its sources beyond tables hold, as the edition reads it from
// the draft: the concepts drafted from knowledge-base files and the rules they state. Kinetic
// analysis, scenario drafting and Ask AI read it beside the documents and the model, and the AI
// assisted pages list it among what AI reads. The open core has none.

// readKnowledge reads the knowledge of a draft; tests replace it.
var readKnowledge = draft.Knowledge

// messageKnowledge is the knowledge as the Agent reads it: data, by name, like the rest.
type messageKnowledge struct {
	Concepts []messageKnowledgeItem `json:"concepts"`
	Rules    []messageKnowledgeItem `json:"rules"`
}

type messageKnowledgeItem struct {
	Name       string `json:"name"`
	Kind       string `json:"kind,omitempty"`
	Definition string `json:"definition,omitempty"`
	// InModel says the model adopted the concept's item, so questions and needs may rest on it.
	InModel bool `json:"in_model,omitempty"`
}

// messageKnowledgeOf is the knowledge for a message; nil, and left out, when there is none.
func messageKnowledgeOf(k interfaces.RequirementKnowledge) *messageKnowledge {
	if k.Empty() {
		return nil
	}
	msg := &messageKnowledge{Concepts: []messageKnowledgeItem{}, Rules: []messageKnowledgeItem{}}
	for _, c := range k.Concepts {
		msg.Concepts = append(msg.Concepts, messageKnowledgeItem{Name: c.Name, Kind: c.Kind,
			Definition: c.Description, InModel: c.Adopted})
	}
	for _, r := range k.Rules {
		msg.Rules = append(msg.Rules, messageKnowledgeItem{Name: r.Name, Definition: r.Description})
	}
	return msg
}

// knowledgeOf is the knowledge of the project's draft for a run to read; none without a draft.
func knowledgeOf(ctx context.Context, d *interfaces.Draft) interfaces.RequirementKnowledge {
	return readKnowledge(ctx, d)
}

// GetKnowledge returns the knowledge of the project as kinetic analysis, scenario drafting and Ask
// AI read it now; none before there is a draft.
func (s *requirementService) GetKnowledge(ctx context.Context, token string,
	projectID string) (*interfaces.RequirementKnowledge, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "GetRequirementKnowledge")
	defer span.End()

	if _, err := s.ps.GetProject(ctx, token, projectID); err != nil {
		span.SetStatus(codes.Error, "Get project failed")
		return nil, err
	}
	d, found, err := s.da.GetDraft(ctx, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get draft failed")
		return nil, internalError(ctx, "Get draft failed", err)
	}
	if !found {
		d = nil
	}
	knowledge := knowledgeOf(ctx, d)
	span.SetStatus(codes.Ok, "")
	return &knowledge, nil
}
