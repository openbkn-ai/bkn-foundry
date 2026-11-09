// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package release

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/drift"
)

// rollbackNotes is what bkn-backend records with a rollback bkn-project made.
const rollbackNotes = "Rolled back by BKN-project"

// versionError words a failed read of the network's versions.
func versionError(ctx context.Context, operation string, err error) error {
	if versionErr, ok := logics.KNVersionHTTPError(ctx, err); ok {
		return versionErr
	}
	return internalError(ctx, operation, err)
}

// rollbackSignature stands for the running version and the target.
func rollbackSignature(running int, target int) string {
	sum := sha256.Sum256(fmt.Appendf(nil, "%d\n%d\n", running, target))
	return hex.EncodeToString(sum[:])
}

// rollbackVersion names a network version, with the project release that published it, if any.
func rollbackVersion(v *interfaces.KNVersion, releases map[int]*interfaces.Release) interfaces.RollbackVersion {
	named := interfaces.RollbackVersion{KNVersion: v.Version, Label: v.Label}
	if r := releases[v.Version]; r != nil {
		named.Version = r.Version
	}
	return named
}

// planRollback works out switching the running version of entry's network back to toVersion, 0
// for the previous version. Only reading the plan's refusals tells whether it may go.
func (s *projectReleaseService) planRollback(ctx context.Context, token string, entry *interfaces.ProjectEntry,
	toVersion int) (*interfaces.RollbackPlan, int, error) {
	plan := &interfaces.RollbackPlan{
		ProjectID: entry.ID,
		Questions: releaseQuestions(nil, nil, nil, interfaces.PathDiff{}),
		Refusals:  []interfaces.ReleaseRefusal{},
	}
	refuse := func(code string, details any, running int, target int) (*interfaces.RollbackPlan, int, error) {
		plan.Refusals = append(plan.Refusals, interfaces.ReleaseRefusal{Code: code, Details: details})
		plan.Signature = rollbackSignature(running, target)
		return plan, running, nil
	}
	versioning, err := s.kva.GetVersioning(ctx, token, entry.KNID)
	if err != nil {
		return nil, 0, versionError(ctx, "Get versioning failed", err)
	}
	plan.FullHistory = versioning.FullHistory
	running := versioning.RunningVersion
	target := toVersion
	if target == 0 {
		target = versioning.PreviousVersion
	}
	versions, err := s.kva.ListVersions(ctx, token, entry.KNID)
	if err != nil {
		return nil, 0, versionError(ctx, "List versions failed", err)
	}
	byNumber := map[int]*interfaces.KNVersion{}
	for _, v := range versions {
		byNumber[v.Version] = v
	}
	published, err := s.lineageReleases(ctx, entry.KNID)
	if err != nil {
		return nil, 0, internalError(ctx, "List releases failed", err)
	}
	releases := map[int]*interfaces.Release{}
	for _, r := range published {
		if r.State == interfaces.RELEASE_STATE_PUBLISHED {
			releases[r.KNVersion] = r
		}
	}

	from := byNumber[running]
	if running == 0 || from == nil {
		return refuse(perrors.BknProject_Rollback_NoPrevious, nil, running, target)
	}
	plan.From = rollbackVersion(from, releases)
	if r := releases[running]; r == nil || r.ProjectID != entry.ID {
		return refuse(perrors.BknProject_Rollback_NotProjectRelease, map[string]any{"project_id": from.Source.ProjectID}, running, target)
	}
	if target == 0 {
		return refuse(perrors.BknProject_Rollback_NoPrevious, nil, running, target)
	}
	to := byNumber[target]
	switch {
	case !versioning.FullHistory && target != versioning.PreviousVersion:
		return refuse(perrors.BknProject_Rollback_EditionRestricted, map[string]any{"previous_version": versioning.PreviousVersion}, running, target)
	case to == nil:
		return refuse(perrors.BknProject_Release_NotFound, map[string]any{"kn_version": target}, running, target)
	case to.State == interfaces.KN_VERSION_STATE_PURGED:
		return refuse(perrors.BknProject_Release_VersionPurged, map[string]any{"kn_version": target}, running, target)
	case to.State != interfaces.KN_VERSION_STATE_HISTORY:
		return refuse(perrors.BknProject_Rollback_NotActivatable, map[string]any{"kn_version": target, "status": to.State}, running, target)
	}
	named := rollbackVersion(to, releases)
	plan.To = &named

	// The scenario questions are placed as a release review places them, with the target in place
	// of the branch and the running version in place of what is live.
	var questions *interfaces.ScenarioQuestions
	if s.rs != nil {
		if questions, err = s.rs.ScenarioQuestions(ctx, entry.ID); err != nil {
			return nil, 0, internalError(ctx, "Get scenario questions failed", err)
		}
	}
	fromFiles, err := s.kva.ExportVersion(ctx, token, entry.KNID, strconv.Itoa(running))
	if err != nil {
		return nil, 0, versionError(ctx, "Read definitions failed", err)
	}
	toFiles, err := s.kva.ExportVersion(ctx, token, entry.KNID, strconv.Itoa(target))
	if err != nil {
		return nil, 0, versionError(ctx, "Read definitions failed", err)
	}
	plan.Questions = releaseQuestions(questions, toFiles, fromFiles, drift.DiffFiles(fromFiles, toFiles))
	plan.Signature = rollbackSignature(running, target)
	return plan, running, nil
}

// PlanRollback reports what switching the running version back would do, read by anyone who can
// read the project.
func (s *projectReleaseService) PlanRollback(ctx context.Context, token string, projectID string,
	toVersion int) (*interfaces.RollbackPlan, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "PlanRollback")
	defer span.End()

	entry, err := s.ps.GetProject(ctx, token, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get project failed")
		return nil, err
	}
	plan, _, err := s.planRollback(ctx, token, entry, toVersion)
	if err != nil {
		span.SetStatus(codes.Error, "Plan rollback failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return plan, nil
}

// Rollback switches the running version back, for the owner, only as the plan read says. Nothing is
// imported and nothing in the draft or the project's branch changes.
func (s *projectReleaseService) Rollback(ctx context.Context, token string, projectID string,
	req interfaces.ProjectRollback, caller interfaces.AccountInfo) (*interfaces.RollbackResult, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Rollback")
	defer span.End()

	if req.Signature == "" {
		span.SetStatus(codes.Error, "No precheck")
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Rollback_PrecheckRequired)
	}
	entry, err := s.ownedProject(ctx, token, projectID, caller)
	if err != nil {
		span.SetStatus(codes.Error, "Project not rollbackable")
		return nil, err
	}
	plan, running, err := s.planRollback(ctx, token, entry, req.ToVersion)
	if err != nil {
		span.SetStatus(codes.Error, "Plan rollback failed")
		return nil, err
	}
	if len(plan.Refusals) > 0 {
		span.SetStatus(codes.Error, "Rollback refused")
		status := http.StatusConflict
		if plan.Refusals[0].Code == perrors.BknProject_Rollback_EditionRestricted {
			status = http.StatusForbidden
		}
		return nil, httperrors.NewHTTPError(ctx, status, plan.Refusals[0].Code).WithErrorDetails(plan.Refusals[0].Details)
	}
	if req.Signature != plan.Signature {
		span.SetStatus(codes.Error, "Plan outdated")
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Rollback_PlanOutdated)
	}
	// bkn-backend checks, as it switches, that the running version is still the one the plan read.
	activated, err := s.kva.Activate(ctx, token, entry.KNID, plan.To.KNVersion, running, rollbackNotes)
	var backendErr *interfaces.BknBackendError
	if errors.As(err, &backendErr) && backendErr.ErrorCode == interfaces.KN_VERSION_ERROR_RUNNING_MISMATCH {
		span.SetStatus(codes.Error, "Plan outdated")
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Rollback_PlanOutdated)
	}
	if err != nil {
		span.SetStatus(codes.Error, "Activate version failed")
		return nil, versionError(ctx, "Activate version failed", err)
	}
	span.SetStatus(codes.Ok, "")
	return &interfaces.RollbackResult{RunningVersion: activated.RunningVersion, Warnings: activated.Warnings}, nil
}

// lineageReleases are the lineage's releases, newest first.
func (s *projectReleaseService) lineageReleases(ctx context.Context, lineageID string) ([]*interfaces.Release, error) {
	all := []*interfaces.Release{}
	query := interfaces.ReleasesQuery{LineageID: lineageID, Limit: interfaces.MAX_RELEASE_LIMIT}
	for {
		page, err := s.ra.ListReleases(ctx, query)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if len(page) < query.Limit {
			return all, nil
		}
		last := page[len(page)-1]
		query.Cursor = &interfaces.ReleaseCursor{CreateTime: last.CreateTime, ID: last.ID}
	}
}
