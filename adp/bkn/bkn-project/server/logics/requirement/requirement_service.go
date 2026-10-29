// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package requirement serves a project's requirement documents and its requirement
// specification, and the scenario questions publishing reads from it.
package requirement

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/project"
)

var (
	rsOnce sync.Once
	rs     interfaces.RequirementService
)

type requirementService struct {
	ps interfaces.ProjectService
	ra interfaces.RequirementAccess
	// da reads the ontology draft whose items questions need.
	da interfaces.DraftAccess
	// va lists the tables of the project's catalogs, and ba runs the drafting Agent, as the caller.
	va interfaces.VegaAccess
	ba interfaces.BknAgentAccess
	// now, newID, run and sleep are replaced in tests: run drafts in the background, sleep waits
	// between reads of the Agent's task.
	now   func() int64
	newID func() string
	run   func(fn func())
	sleep func(time.Duration)
}

func NewRequirementService() interfaces.RequirementService {
	rsOnce.Do(func() {
		rs = &requirementService{
			ps:    project.NewProjectService(),
			ra:    logics.RQA,
			da:    logics.DA,
			va:    logics.VA,
			ba:    logics.BAA,
			now:   func() int64 { return time.Now().UnixMilli() },
			newID: func() string { return uuid.Must(uuid.NewV7()).String() },
			run:   func(fn func()) { go fn() },
			sleep: time.Sleep,
		}
	})
	return rs
}

func internalError(ctx context.Context, operation string, err error) error {
	otellog.LogError(ctx, operation, err)
	return httperrors.NewHTTPError(ctx, http.StatusInternalServerError, perrors.BknProject_Requirement_InternalError).
		WithErrorDetails(err.Error())
}

// ownedProject returns the project the caller can see, refusing it unless the caller owns it and
// it is not archived.
func (s *requirementService) ownedProject(ctx context.Context, token string, projectID string,
	caller interfaces.AccountInfo) (*interfaces.ProjectEntry, error) {
	entry, err := s.ps.GetProject(ctx, token, projectID)
	if err != nil {
		return nil, err
	}
	if entry.Owner.ID != caller.ID {
		return nil, httperrors.NewHTTPError(ctx, http.StatusForbidden, perrors.BknProject_Requirement_NotOwner)
	}
	if entry.Archived {
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Requirement_ProjectArchived)
	}
	return entry, nil
}

func (s *requirementService) ScenarioQuestions(ctx context.Context, projectID string) (*interfaces.ScenarioQuestions, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "ScenarioQuestions")
	defer span.End()

	result := &interfaces.ScenarioQuestions{Questions: []interfaces.ScenarioQuestion{}}
	req, ok, err := s.ra.GetRequirement(ctx, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get requirement failed")
		return nil, internalError(ctx, "Get requirement failed", err)
	}
	// A specification drafting or failed has no questions yet: drafting replaced the last one.
	if !ok || req.Status != interfaces.REQUIREMENT_STATUS_READY {
		span.SetStatus(codes.Ok, "")
		return result, nil
	}
	draft, ok, err := s.da.GetDraft(ctx, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get draft failed")
		return nil, internalError(ctx, "Get draft failed", err)
	}
	drafted := map[interfaces.RequirementItemRef]bool{}
	if ok {
		for _, ot := range draft.Items.ObjectTypes {
			drafted[interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: ot.ID}] = true
		}
		for _, rt := range draft.Items.RelationTypes {
			drafted[interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_RELATION_TYPE, ID: rt.ID}] = true
		}
	}
	// The test questions asked are those that passed their check, of guidance generated for the
	// question as it is now.
	list, err := s.ra.ListGuidance(ctx, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "List guidance failed")
		return nil, internalError(ctx, "List guidance failed", err)
	}
	guidance := map[string]*interfaces.QuestionGuidance{}
	for _, g := range list {
		guidance[g.QuestionID] = g
	}
	result.SpecVersion, result.UpdateTime = req.Version, req.UpdateTime
	for _, q := range req.Spec.Questions {
		question := interfaces.ScenarioQuestion{
			ID: q.ID, Text: q.Text, Layer: q.Layer,
			Needs:        append([]interfaces.RequirementItemRef{}, q.Needs...),
			MissingItems: []interfaces.RequirementItemRef{},
			Tests:        []interfaces.ScenarioTest{},
		}
		for _, need := range q.Needs {
			if !drafted[need] {
				question.MissingItems = append(question.MissingItems, need)
			}
		}
		if g := guidance[q.ID]; g != nil && g.Status == interfaces.GUIDANCE_STATUS_READY &&
			g.SpecVersion == req.Version && g.Question == q.Text {
			for _, t := range g.Tests {
				if t.Check.Outcome == interfaces.TEST_CHECK_PASSED {
					question.Tests = append(question.Tests, interfaces.ScenarioTest{ID: t.ID, Text: t.Text})
				}
			}
		}
		result.Questions = append(result.Questions, question)
	}
	span.SetStatus(codes.Ok, "")
	return result, nil
}
