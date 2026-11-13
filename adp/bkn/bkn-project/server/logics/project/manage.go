// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package project

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-git/go-git/v5"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/projecthooks"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// preparedPage bounds the prepared releases read of a lineage; a lineage prepares one at a time.
const preparedPage = 100

// RenameProject renames the project. Its owner may also when its network was deleted.
func (s *projectService) RenameProject(ctx context.Context, token string, id string, name string,
	caller interfaces.AccountInfo) (*interfaces.ProjectEntry, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "RenameProject")
	defer span.End()

	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > maxNameRunes {
		span.SetStatus(codes.Error, "Invalid name")
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Project_InvalidName)
	}
	project, _, err := s.ownersProject(ctx, token, id, caller)
	if err != nil {
		span.SetStatus(codes.Error, "Get project failed")
		return nil, err
	}
	if !s.canManage(ctx, token, caller, project) {
		span.SetStatus(codes.Error, "Not the owner")
		return nil, httperrors.NewHTTPError(ctx, http.StatusForbidden, perrors.BknProject_Project_NotOwner)
	}
	if project.Name != name {
		project.Name = name
		project.UpdateTime = time.Now().UnixMilli()
		if err = s.pa.RenameProject(ctx, id, name, project.UpdateTime); err != nil {
			span.SetStatus(codes.Error, "Rename project failed")
			return nil, internalError(ctx, "Rename project failed", err)
		}
	}
	entries, err := s.withOtherProjects(ctx, []*interfaces.Project{project})
	if err != nil {
		span.SetStatus(codes.Error, "Count projects failed")
		return nil, internalError(ctx, "Count projects failed", err)
	}
	span.SetStatus(codes.Ok, "")
	return entries[0], nil
}

// DeleteProject withdraws the project's prepared release, lets the edition release what it holds,
// deletes the project's rows and then its branch. Its owner may also when its network was deleted.
func (s *projectService) DeleteProject(ctx context.Context, token string, id string, caller interfaces.AccountInfo) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "DeleteProject")
	defer span.End()

	project, networkMissing, err := s.ownersProject(ctx, token, id, caller)
	if err != nil {
		span.SetStatus(codes.Error, "Get project failed")
		return err
	}
	if !s.canManage(ctx, token, caller, project) {
		span.SetStatus(codes.Error, "Not the owner")
		return httperrors.NewHTTPError(ctx, http.StatusForbidden, perrors.BknProject_Project_NotOwner)
	}
	if !networkMissing {
		if err = s.withdrawPrepared(ctx, token, project); err != nil {
			span.SetStatus(codes.Error, "Withdraw release failed")
			return err
		}
	}
	if deleter, ok := projecthooks.Current().Projects.(projecthooks.ProjectDeleter); ok {
		if err = deleter.Deleting(ctx, token, id, caller); err != nil {
			span.SetStatus(codes.Error, "Release edition data failed")
			return err
		}
	}
	if err = s.pa.DeleteProject(ctx, id); err != nil {
		span.SetStatus(codes.Error, "Delete project failed")
		return internalError(ctx, "Delete project failed", err)
	}
	// The branch goes last: nothing reaches it without the project, and a failure only leaves it unused.
	if s.gs != nil {
		if err = s.gs.DeleteBranch(ctx, project.KNID, project.Branch); err != nil && !errors.Is(err, git.ErrRepositoryNotExists) {
			otellog.LogWarn(ctx, fmt.Sprintf("Delete branch %s of lineage %s failed: %v", project.Branch, project.KNID, err))
		}
	}
	span.SetStatus(codes.Ok, "")
	return nil
}

// withdrawPrepared takes the project's prepared, unpublished release back out of the network's
// draft, which preparing wrote it into: the draft goes back to the running version while no one
// edited it since. A release whose publish was sent may be published any moment, so deleting waits.
func (s *projectService) withdrawPrepared(ctx context.Context, token string, project *interfaces.Project) error {
	prepared, err := s.ra.ListReleases(ctx, interfaces.ReleasesQuery{
		LineageID: project.KNID, State: interfaces.RELEASE_STATE_PREPARED, Limit: preparedPage,
	})
	if err != nil {
		return internalError(ctx, "List releases failed", err)
	}
	for _, release := range prepared {
		if release.ProjectID != project.ID {
			continue
		}
		if release.PublishRequest != nil {
			return httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Project_ReleasePublishing).
				WithErrorDetails(map[string]any{"version": release.Version})
		}
		versioning, err := s.kva.GetVersioning(ctx, token, project.KNID)
		if err != nil {
			return backendHTTPError(ctx, "Get versioning failed", err)
		}
		if versioning.Draft.Revision != release.DraftRevision || versioning.RunningVersion < 1 {
			// Someone edited the draft since, or the network has no version to go back to: what
			// is in the draft is no longer only this release's, so it stays.
			otellog.LogWarn(ctx, fmt.Sprintf("Release %s of project %s left in the draft of %s: draft revision %d, prepared %d, running version %d",
				release.Version, project.ID, project.KNID, versioning.Draft.Revision, release.DraftRevision, versioning.RunningVersion))
			continue
		}
		if err = s.kva.ResetDraft(ctx, token, project.KNID, versioning.RunningVersion, release.DraftRevision, true); err != nil {
			return backendHTTPError(ctx, "Withdraw release failed", err)
		}
	}
	return nil
}

// MarkManageable sets whether the caller may rename and delete each project.
func (s *projectService) MarkManageable(ctx context.Context, token string, caller interfaces.AccountInfo,
	entries ...*interfaces.ProjectEntry) {
	admin, asked := false, false
	for _, entry := range entries {
		if entry == nil || entry.Project == nil {
			continue
		}
		if entry.Owner == caller {
			entry.CanManage = true
			continue
		}
		if !asked {
			admin, asked = s.isAdmin(ctx, token), true
		}
		entry.CanManage = admin
	}
}

// canManage tells whether the caller owns or administers the project.
func (s *projectService) canManage(ctx context.Context, token string, caller interfaces.AccountInfo,
	project *interfaces.Project) bool {
	return project.Owner == caller || s.isAdmin(ctx, token)
}

func (s *projectService) isAdmin(ctx context.Context, token string) bool {
	return s.adm != nil && s.adm.IsAdmin(ctx, token)
}
