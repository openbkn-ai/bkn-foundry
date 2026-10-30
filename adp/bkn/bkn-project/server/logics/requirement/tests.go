// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// People review the test questions the Agent wrote: they reword or correct one, which is checked
// against the data again, or delete one, so the regression no longer asks it.

func invalidTest(ctx context.Context, detail string) error {
	return httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Requirement_InvalidTest).
		WithErrorDetails(detail)
}

// changeTests reads a question's ready guidance and writes it after change, over the guidance as
// the caller read it.
func (s *requirementService) changeTests(ctx context.Context, token string, projectID string, questionID string,
	updateTime int64, caller interfaces.AccountInfo,
	change func(g *interfaces.QuestionGuidance) error) (*interfaces.QuestionGuidance, error) {
	if _, err := s.ownedProject(ctx, token, projectID, caller); err != nil {
		return nil, err
	}
	if updateTime == 0 {
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Requirement_MissingUpdateTime)
	}
	g, ok, err := s.ra.GetGuidance(ctx, projectID, questionID)
	if err != nil {
		return nil, internalError(ctx, "Get guidance failed", err)
	}
	switch {
	case !ok || g.Status == interfaces.GUIDANCE_STATUS_FAILED:
		return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Requirement_TestNotFound)
	case g.Status == interfaces.GUIDANCE_STATUS_GENERATING:
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Requirement_Generating)
	case g.UpdateTime != updateTime:
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Requirement_Stale)
	}
	if err := change(g); err != nil {
		return nil, err
	}
	g.UpdateTime = s.now()
	written, err := s.ra.UpdateGuidance(ctx, g, updateTime)
	if err != nil {
		return nil, internalError(ctx, "Update guidance failed", err)
	}
	if !written {
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Requirement_Stale)
	}
	return g, nil
}

func testIndex(g *interfaces.QuestionGuidance, testID string) int {
	return slices.IndexFunc(g.Tests, func(t *interfaces.QuestionTest) bool { return t.ID == testID })
}

func (s *requirementService) EditTest(ctx context.Context, token string, projectID string, questionID string,
	testID string, edit interfaces.RequirementTestEdit, caller interfaces.AccountInfo) (*interfaces.QuestionGuidance, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "EditQuestionTest")
	defer span.End()

	g, err := s.changeTests(ctx, token, projectID, questionID, edit.UpdateTime, caller,
		func(g *interfaces.QuestionGuidance) error {
			i := testIndex(g, testID)
			if i < 0 {
				return httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Requirement_TestNotFound)
			}
			// Checked against the specification as drafted: none while it is drafted again.
			req, err := s.readySpecOf(ctx, projectID)
			if err != nil {
				return err
			}
			test := *g.Tests[i]
			if edit.Text == nil && edit.Slots == nil && edit.Expect == nil {
				return invalidTest(ctx, "the edit sets no field")
			}
			if edit.Text != nil {
				text := strings.TrimSpace(*edit.Text)
				if text == "" || utf8.RuneCountInString(text) > maxItemTextRunes {
					return invalidTest(ctx, fmt.Sprintf("text is empty or longer than %d characters", maxItemTextRunes))
				}
				test.Text = text
			}
			if edit.Expect != nil {
				if !slices.Contains([]string{interfaces.TEST_EXPECT_EXISTS, interfaces.TEST_EXPECT_NOT_EXISTS,
					interfaces.TEST_EXPECT_SAME_RECORD}, *edit.Expect) {
					return invalidTest(ctx, "expect "+*edit.Expect+" is unknown")
				}
				test.Expect = *edit.Expect
			}
			if edit.Slots != nil {
				for _, slot := range *edit.Slots {
					if !slices.ContainsFunc(req.Spec.Concepts, func(c *interfaces.RequirementConcept) bool {
						return c.ID == slot.ConceptID
					}) {
						return invalidTest(ctx, "concept "+slot.ConceptID+" does not exist")
					}
				}
				test.Slots = append([]interfaces.QuestionTestSlot{}, *edit.Slots...)
				// Where a value came from is the server's to say, when it fills one.
				for i := range test.Slots {
					test.Slots[i].ValueFrom = ""
				}
			}
			draft, found, err := s.da.GetDraft(ctx, projectID)
			if err != nil {
				return internalError(ctx, "Get draft failed", err)
			}
			if !found {
				draft = nil
			}
			test.Edited = true
			q := questionOf(&req.Spec, g.QuestionID)
			if q == nil {
				q = &interfaces.RequirementQuestion{ID: g.QuestionID, Text: g.Question}
			}
			test.Check = s.checkAnchored(ctx, caller, &req.Spec, draft, q, &test, anchorOf(g.Tests))
			g.Tests[i] = &test
			return nil
		})
	if err != nil {
		span.SetStatus(codes.Error, "Edit test failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return g, nil
}

func (s *requirementService) DeleteTest(ctx context.Context, token string, projectID string, questionID string,
	testID string, updateTime int64, caller interfaces.AccountInfo) (*interfaces.QuestionGuidance, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "DeleteQuestionTest")
	defer span.End()

	g, err := s.changeTests(ctx, token, projectID, questionID, updateTime, caller,
		func(g *interfaces.QuestionGuidance) error {
			i := testIndex(g, testID)
			if i < 0 {
				return httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Requirement_TestNotFound)
			}
			g.Tests = slices.Delete(g.Tests, i, i+1)
			g.DeletedTests++
			return nil
		})
	if err != nil {
		span.SetStatus(codes.Error, "Delete test failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return g, nil
}
