// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"context"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// The supplement note is one paragraph of the owner's on what the project is for and what matters,
// shared by the kinetic modeling and business scenarios pages. Kinetic analysis, scenario drafting
// and Ask AI read it as it is when they start.

func (s *requirementService) GetSupplement(ctx context.Context, token string,
	projectID string) (*interfaces.RequirementSupplement, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "GetRequirementSupplement")
	defer span.End()

	if _, err := s.ps.GetProject(ctx, token, projectID); err != nil {
		span.SetStatus(codes.Error, "Get project failed")
		return nil, err
	}
	supplement, err := s.ra.GetSupplement(ctx, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get supplement failed")
		return nil, internalError(ctx, "Get supplement failed", err)
	}
	span.SetStatus(codes.Ok, "")
	return supplement, nil
}

func (s *requirementService) SetSupplement(ctx context.Context, token string, projectID string,
	edit interfaces.RequirementSupplementEdit, caller interfaces.AccountInfo) (*interfaces.RequirementSupplement, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "SetRequirementSupplement")
	defer span.End()

	if _, err := s.ownedProject(ctx, token, projectID, caller); err != nil {
		span.SetStatus(codes.Error, "Project not writable")
		return nil, err
	}
	text := strings.TrimSpace(edit.Text)
	if utf8.RuneCountInString(text) > interfaces.REQUIREMENT_SUPPLEMENT_MAX_LENGTH {
		span.SetStatus(codes.Error, "Supplement too long")
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Requirement_InvalidSupplement)
	}
	supplement := &interfaces.RequirementSupplement{Text: text, Time: s.now(),
		By: &interfaces.RequirementAuthor{ID: caller.ID, Type: caller.Type}}
	if err := s.ra.SetSupplement(ctx, projectID, supplement); err != nil {
		span.SetStatus(codes.Error, "Set supplement failed")
		return nil, internalError(ctx, "Set supplement failed", err)
	}
	span.SetStatus(codes.Ok, "")
	return supplement, nil
}

// supplementOf is the project's supplement note for a run to read; one that cannot be read leaves
// the run without it.
func (s *requirementService) supplementOf(ctx context.Context, projectID string) string {
	supplement, err := s.ra.GetSupplement(ctx, projectID)
	if err != nil {
		logger.Warnf("Read the supplement note of project %s failed: %v", projectID, err)
		return ""
	}
	return supplement.Text
}
