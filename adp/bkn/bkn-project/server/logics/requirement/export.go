// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"
	"gopkg.in/yaml.v3"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// Whom a specification is exported for: Workshop builds the capabilities, bkn-eval evaluates the
// knowledge network. Neither has an import yet, so the export is a file.
const (
	exportTargetWorkshop = "workshop"
	exportTargetEval     = "eval"
)

type workshopExport struct {
	Format      string             `yaml:"format"`
	ProjectID   string             `yaml:"project_id"`
	SpecVersion int                `yaml:"spec_version"`
	Questions   []workshopQuestion `yaml:"questions"`
	Rules       []exportRule       `yaml:"rules"`
}

type workshopQuestion struct {
	ID          string      `yaml:"id"`
	Question    string      `yaml:"question"`
	AnswerShape string      `yaml:"answer_shape,omitempty"`
	Scenarios   []string    `yaml:"scenarios,omitempty"`
	Basis       exportBasis `yaml:"basis"`
}

type exportBasis struct {
	Concepts  []string `yaml:"concepts,omitempty"`
	Metrics   []string `yaml:"metrics,omitempty"`
	Functions []string `yaml:"functions,omitempty"`
	Skills    []string `yaml:"skills,omitempty"`
}

type exportRule struct {
	ID         string `yaml:"id"`
	Statement  string `yaml:"statement"`
	CompilesTo string `yaml:"compiles_to,omitempty"`
}

// evalExport holds no answer: bkn-eval computes the answers itself once the data is frozen, and
// keeps them where the project cannot read them (answer_set_access: forbidden).
type evalExport struct {
	Format          string         `yaml:"format"`
	AnswerSetAccess string         `yaml:"answer_set_access"`
	ProjectID       string         `yaml:"project_id"`
	SpecVersion     int            `yaml:"spec_version"`
	Questions       []evalQuestion `yaml:"questions"`
}

type evalQuestion struct {
	ID          string        `yaml:"id"`
	Question    string        `yaml:"question"`
	AnswerShape string        `yaml:"answer_shape,omitempty"`
	Inputs      []exportInput `yaml:"inputs,omitempty"`
	Tests       []exportTest  `yaml:"tests,omitempty"`
}

type exportInput struct {
	Name     string `yaml:"name"`
	Concept  string `yaml:"concept"`
	Property string `yaml:"property,omitempty"`
	Role     string `yaml:"role"`
}

type exportTest struct {
	Kind     string `yaml:"kind,omitempty"`
	Question string `yaml:"question"`
	Expect   string `yaml:"expect"`
}

// buildExport writes the specification for a target as YAML. It holds no value of a record: a
// question is asked as its template, where each value it is asked about is a {name}, and a test's
// values, filled in or still to fill, are written {concept.property}. guidance is read for eval
// only, and a question's tests go only while its guidance is ready and current.
func buildExport(target string, req *interfaces.Requirement, guidance []*interfaces.QuestionGuidance) ([]byte, error) {
	spec := &req.Spec
	terms := map[string]string{}
	for _, c := range spec.Concepts {
		terms[c.ID] = c.Term
	}
	asked := func(q *interfaces.RequirementQuestion) string {
		if strings.TrimSpace(q.Template) != "" {
			return q.Template
		}
		return q.Text
	}
	if target == exportTargetWorkshop {
		out := workshopExport{Format: "bkn-project-requirements-workshop/v1", ProjectID: req.ProjectID,
			SpecVersion: req.Version, Questions: []workshopQuestion{}, Rules: []exportRule{}}
		scenarios := map[string]string{}
		for _, s := range spec.Scenarios {
			scenarios[s.ID] = s.Name
		}
		for _, q := range spec.Questions {
			if q.Layer != interfaces.REQUIREMENT_LAYER_CAPABILITY {
				continue
			}
			wq := workshopQuestion{ID: q.ID, Question: asked(q), AnswerShape: q.AnswerShape,
				Basis: exportBasis{Metrics: q.Basis.Metrics, Functions: q.Basis.Functions, Skills: q.Basis.Skills}}
			for _, id := range q.ScenarioIDs {
				if name, ok := scenarios[id]; ok {
					wq.Scenarios = append(wq.Scenarios, name)
				}
			}
			for _, id := range q.Basis.ConceptIDs {
				if term, ok := terms[id]; ok {
					wq.Basis.Concepts = append(wq.Basis.Concepts, term)
				}
			}
			out.Questions = append(out.Questions, wq)
		}
		for _, r := range spec.Rules {
			out.Rules = append(out.Rules, exportRule{ID: r.ID, Statement: r.Statement, CompilesTo: r.CompilesTo})
		}
		return yaml.Marshal(out)
	}

	byQuestion := map[string]*interfaces.QuestionGuidance{}
	for _, g := range guidance {
		byQuestion[g.QuestionID] = g
	}
	out := evalExport{Format: "bkn-project-requirements-eval/v1", AnswerSetAccess: "forbidden",
		ProjectID: req.ProjectID, SpecVersion: req.Version, Questions: []evalQuestion{}}
	for _, q := range spec.Questions {
		if q.Layer != interfaces.REQUIREMENT_LAYER_KNOWLEDGE {
			continue
		}
		eq := evalQuestion{ID: q.ID, Question: asked(q), AnswerShape: q.AnswerShape}
		if q.Inputs != nil {
			for _, in := range *q.Inputs {
				eq.Inputs = append(eq.Inputs, exportInput{Name: in.Name, Concept: terms[in.ConceptID],
					Property: in.Property, Role: in.Role})
			}
		}
		if g, ok := byQuestion[q.ID]; ok && g.Status == interfaces.GUIDANCE_STATUS_READY && guidanceCurrent(req, q, g) {
			for _, t := range g.Tests {
				eq.Tests = append(eq.Tests, exportTest{Kind: t.Kind, Question: withoutValues(t, terms), Expect: t.Expect})
			}
		}
		out.Questions = append(out.Questions, eq)
	}
	return yaml.Marshal(out)
}

// withoutValues is a test's text with each slot, filled in or still to fill, written as the
// concept and property it is a value of. A longer value goes first, so one that contains another
// is replaced whole.
func withoutValues(t *interfaces.QuestionTest, terms map[string]string) string {
	text := t.Text
	order := make([]int, len(t.Slots))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int { return cmp.Compare(len(t.Slots[b].Value), len(t.Slots[a].Value)) })
	for _, i := range order {
		s := t.Slots[i]
		name := "{" + terms[s.ConceptID] + "." + s.Property + "}"
		text = strings.ReplaceAll(text, slotPlaceholder(i), name)
		if v := strings.TrimSpace(s.Value); v != "" {
			text = strings.ReplaceAll(text, v, name)
		}
	}
	return text
}

// ExportRequirement writes the project's ready specification for Workshop or bkn-eval as YAML,
// read as the caller, with the file's name.
func (s *requirementService) ExportRequirement(ctx context.Context, token string, projectID string,
	target string) ([]byte, string, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "ExportRequirement")
	defer span.End()

	if target != exportTargetWorkshop && target != exportTargetEval {
		span.SetStatus(codes.Error, "Invalid export target")
		return nil, "", httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Requirement_InvalidExportTarget)
	}
	if _, err := s.ps.GetProject(ctx, token, projectID); err != nil {
		span.SetStatus(codes.Error, "Get project failed")
		return nil, "", err
	}
	req, ok, err := s.ra.GetRequirement(ctx, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get requirement failed")
		return nil, "", internalError(ctx, "Get requirement failed", err)
	}
	if !ok || req.Status != interfaces.REQUIREMENT_STATUS_READY {
		span.SetStatus(codes.Error, "Requirement not found")
		return nil, "", httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Requirement_NotFound)
	}
	var guidance []*interfaces.QuestionGuidance
	if target == exportTargetEval {
		if guidance, err = s.ra.ListGuidance(ctx, projectID); err != nil {
			span.SetStatus(codes.Error, "List guidance failed")
			return nil, "", internalError(ctx, "List guidance failed", err)
		}
	}
	body, err := buildExport(target, req, guidance)
	if err != nil {
		span.SetStatus(codes.Error, "Write export failed")
		return nil, "", internalError(ctx, "Write export failed", err)
	}
	span.SetStatus(codes.Ok, "")
	return body, fmt.Sprintf("%s-rq-%d-%s.yaml", projectID, req.Version, target), nil
}
