// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package release

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// acceptable are the failed checks an owner may accept to publish.
var acceptable = []string{
	interfaces.RELEASE_ACCEPT_SIMILAR_NAMES,
	interfaces.RELEASE_ACCEPT_CHECK_PARTIAL,
	interfaces.RELEASE_ACCEPT_INDEX_NOT_READY,
}

// PrepareRelease reviews the release again and writes it into the network's draft through the
// lineage only when the review is the one the owner read and every check passed or was accepted.
// What was accepted is recorded in the release notes, as codes. The lineage checks drift and the
// rest once more under its lock. The regression then answers the project's questions on the draft.
func (s *projectReleaseService) PrepareRelease(ctx context.Context, token string, projectID string,
	req interfaces.ProjectPrepare, caller interfaces.AccountInfo) (*interfaces.Release, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "PrepareProjectRelease")
	defer span.End()

	entry, err := s.ownedProject(ctx, token, projectID, caller)
	if err != nil {
		span.SetStatus(codes.Error, "Project not publishable")
		return nil, err
	}
	for _, a := range req.Accepts {
		if !slices.Contains(acceptable, a) {
			span.SetStatus(codes.Error, "Invalid accepts")
			return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Release_InvalidAccepts).
				WithErrorDetails(map[string]any{"accepts": acceptable})
		}
	}
	review, err := s.reviewer.ReviewRelease(ctx, token, projectID, req.Version, caller)
	if err != nil {
		span.SetStatus(codes.Error, "Review release failed")
		return nil, err
	}
	if review.Signature != req.Signature {
		span.SetStatus(codes.Error, "Review outdated")
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Release_ReviewOutdated).
			WithErrorDetails(map[string]any{"signature": review.Signature})
	}
	accepted := true
	for _, a := range review.Accepts {
		accepted = accepted && slices.Contains(req.Accepts, a)
	}
	if review.Blocked || !accepted {
		span.SetStatus(codes.Error, "Checks failed")
		failing := []interfaces.ReleaseCheck{}
		for _, c := range review.Checks {
			if !c.Passed {
				failing = append(failing, c)
			}
		}
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Release_ChecksFailed).
			WithErrorDetails(map[string]any{"checks": failing, "refusals": review.Refusals, "accepts": review.Accepts})
	}

	notes := ""
	if len(review.Accepts) > 0 {
		notes = interfaces.RELEASE_NOTES_ACCEPTED + strings.Join(review.Accepts, ", ")
	}
	release, err := s.ls.PrepareRelease(ctx, token, entry.KNID, interfaces.PublishReleaseRequest{
		ExpectedBranchCommit: review.Release.BranchCommit, Branch: entry.Branch, Version: req.Version, ProjectID: projectID,
		Notes: notes, OverwriteDraftChanges: req.OverwriteDraftChanges,
	}, caller)
	if err != nil {
		span.SetStatus(codes.Error, "Prepare release failed")
		return nil, err
	}
	// The regression never fails the preparing; one that cannot start is run again later.
	if _, _, err := s.rgs.StartRegression(ctx, token, release, caller); err != nil {
		otellog.LogWarn(ctx, fmt.Sprintf("Start regression of %s %s: %v", release.LineageID, release.Version, err))
	}
	span.SetStatus(codes.Ok, "")
	return release, nil
}

// PublishRelease publishes the project's prepared release as the network's running version.
func (s *projectReleaseService) PublishRelease(ctx context.Context, token string, projectID string,
	req interfaces.ProjectPublish, caller interfaces.AccountInfo,
	author interfaces.GitAuthor) (*interfaces.PublishReleaseResult, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "PublishProjectRelease")
	defer span.End()

	entry, err := s.ownedProject(ctx, token, projectID, caller)
	if err != nil {
		span.SetStatus(codes.Error, "Project not publishable")
		return nil, err
	}
	if _, err := s.projectRelease(ctx, entry, req.Version); err != nil {
		span.SetStatus(codes.Error, "Not a release of the project")
		return nil, err
	}
	result, err := s.ls.PublishRelease(ctx, token, entry.KNID, interfaces.PublishReleaseRequest{
		Branch: entry.Branch, Version: req.Version, ProjectID: projectID, Notes: req.Notes, AllowOutdatedBase: req.AllowOutdatedBase,
	}, caller, author)
	if err != nil {
		span.SetStatus(codes.Error, "Publish release failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return result, nil
}

// GetRegression returns the regression of a release of the project.
func (s *projectReleaseService) GetRegression(ctx context.Context, token string, projectID string,
	version string) (*interfaces.Regression, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "GetRegression")
	defer span.End()

	entry, err := s.ps.GetProject(ctx, token, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get project failed")
		return nil, err
	}
	release, err := s.projectRelease(ctx, entry, version)
	if err != nil {
		span.SetStatus(codes.Error, "Not a release of the project")
		return nil, err
	}
	r, exist, err := s.rga.GetRegression(ctx, release.ID)
	if err != nil {
		span.SetStatus(codes.Error, "Get regression failed")
		return nil, internalError(ctx, "Get regression failed", err)
	}
	if !exist {
		span.SetStatus(codes.Error, "Regression not found")
		return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Release_RegressionNotFound)
	}
	span.SetStatus(codes.Ok, "")
	return r, nil
}

// RunRegression starts the regression of a release of the project again, as the caller.
func (s *projectReleaseService) RunRegression(ctx context.Context, token string, projectID string,
	version string, caller interfaces.AccountInfo) (*interfaces.Regression, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "RunRegression")
	defer span.End()

	entry, err := s.ownedProject(ctx, token, projectID, caller)
	if err != nil {
		span.SetStatus(codes.Error, "Project not runnable")
		return nil, err
	}
	release, err := s.projectRelease(ctx, entry, version)
	if err != nil {
		span.SetStatus(codes.Error, "Not a release of the project")
		return nil, err
	}
	r, started, err := s.rgs.StartRegression(ctx, token, release, caller)
	if err != nil {
		span.SetStatus(codes.Error, "Start regression failed")
		return nil, internalError(ctx, "Start regression failed", err)
	}
	if !started {
		span.SetStatus(codes.Error, "Regression running")
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Release_RegressionRunning).
			WithErrorDetails(map[string]any{"version": version})
	}
	span.SetStatus(codes.Ok, "")
	return r, nil
}

// SyncBranch brings main, the live version, into the project's branch.
func (s *projectReleaseService) SyncBranch(ctx context.Context, token string, projectID string,
	caller interfaces.AccountInfo, author interfaces.GitAuthor) (*interfaces.SyncBranchResult, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "SyncProjectBranch")
	defer span.End()

	entry, err := s.ownedProject(ctx, token, projectID, caller)
	if err != nil {
		span.SetStatus(codes.Error, "Project not syncable")
		return nil, err
	}
	result, err := s.ls.SyncBranch(ctx, entry.KNID, entry.Branch, author)
	if err != nil {
		span.SetStatus(codes.Error, "Sync branch failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return result, nil
}

// CommitDrift commits what was edited in the network's draft to main, then brings main into the
// project's branch, so a release does not lose those edits.
func (s *projectReleaseService) CommitDrift(ctx context.Context, token string, projectID string,
	caller interfaces.AccountInfo, author interfaces.GitAuthor) (*interfaces.ProjectDriftCommit, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "CommitProjectDrift")
	defer span.End()

	entry, err := s.ownedProject(ctx, token, projectID, caller)
	if err != nil {
		span.SetStatus(codes.Error, "Project not syncable")
		return nil, err
	}
	drift, err := s.ls.CheckDrift(ctx, token, entry.KNID, entry.KNID, true, author)
	if err != nil {
		span.SetStatus(codes.Error, "Commit drift failed")
		return nil, err
	}
	synced, err := s.ls.SyncBranch(ctx, entry.KNID, entry.Branch, author)
	if err != nil {
		span.SetStatus(codes.Error, "Sync branch failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return &interfaces.ProjectDriftCommit{Drift: drift, Sync: synced}, nil
}

// ownedProject returns the project when the caller owns it and it is not archived.
func (s *projectReleaseService) ownedProject(ctx context.Context, token string, projectID string,
	caller interfaces.AccountInfo) (*interfaces.ProjectEntry, error) {
	entry, err := s.ps.GetProject(ctx, token, projectID)
	if err != nil {
		return nil, err
	}
	if entry.Archived {
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Release_ProjectArchived)
	}
	if entry.Owner.ID != caller.ID {
		return nil, httperrors.NewHTTPError(ctx, http.StatusForbidden, perrors.BknProject_Release_NotOwner)
	}
	return entry, nil
}

// projectRelease returns the project's release of version, refusing a version no release of the
// project made.
func (s *projectReleaseService) projectRelease(ctx context.Context, entry *interfaces.ProjectEntry,
	version string) (*interfaces.Release, error) {
	release, exist, err := s.ra.GetRelease(ctx, entry.KNID, version)
	if err != nil {
		return nil, internalError(ctx, "Get release failed", err)
	}
	if !exist || release.ProjectID != entry.ID {
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Release_NotProjectRelease).
			WithErrorDetails(map[string]any{"version": version})
	}
	return release, nil
}
