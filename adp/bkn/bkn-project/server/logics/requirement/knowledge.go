// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/locale"
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

// knowledgeDocument is the knowledge as a document a run reads and cites beside the uploaded ones:
// each rule, then each concept, by name and definition. A need resting on a rule cites it there, so
// the numbers it names are checked against the rule, as against any quote. Nil when there is none.
func knowledgeDocument(projectID string, language string, k interfaces.RequirementKnowledge) *interfaces.RequirementDocument {
	if k.Empty() {
		return nil
	}
	texts, ok := locale.KnowledgeDocumentTexts[language]
	if !ok {
		texts = locale.KnowledgeDocumentTexts[interfaces.NAMING_LANGUAGE_EN_US]
	}
	var b strings.Builder
	section := func(title string, items []*interfaces.RequirementKnowledgeItem) {
		if len(items) == 0 {
			return
		}
		b.WriteString("## " + title + "\n\n")
		for _, item := range items {
			line := strings.TrimSpace(item.Name)
			if definition := strings.TrimSpace(item.Description); definition != "" {
				line += ": " + definition
			}
			b.WriteString("- " + line + "\n")
		}
		b.WriteString("\n")
	}
	section(texts["rules"], k.Rules)
	section(texts["concepts"], k.Concepts)
	content := strings.TrimSpace(b.String())
	sum := sha256.Sum256([]byte(content))
	return &interfaces.RequirementDocument{
		ID: interfaces.REQUIREMENT_KNOWLEDGE_DOCUMENT_ID, ProjectID: projectID, Name: texts["name"],
		DocType: interfaces.REQUIREMENT_DOC_OTHER, Format: interfaces.REQUIREMENT_FORMAT_MARKDOWN,
		Content: content, Blocks: segment(content, interfaces.REQUIREMENT_FORMAT_MARKDOWN),
		SHA256: hex.EncodeToString(sum[:]), Size: int64(len(content)),
	}
}

// withKnowledgeDocument is what a run cites with the knowledge as a document, when there is any.
func withKnowledgeDocument(docs []*interfaces.RequirementDocument, projectID string, language string,
	k interfaces.RequirementKnowledge) []*interfaces.RequirementDocument {
	doc := knowledgeDocument(projectID, language, k)
	if doc == nil {
		return docs
	}
	return append(append([]*interfaces.RequirementDocument{}, docs...), doc)
}
