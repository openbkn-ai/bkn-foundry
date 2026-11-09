// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package release

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// ListVersions lists the versions of the project's network, newest first, each with the release of
// the lineage that published it, if one did, and the network's draft as it stands.
func (s *projectReleaseService) ListVersions(ctx context.Context, token string, projectID string) (*interfaces.ProjectVersions, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "ListProjectVersions")
	defer span.End()

	entry, err := s.ps.GetProject(ctx, token, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get project failed")
		return nil, err
	}
	versioning, err := s.kva.GetVersioning(ctx, token, entry.KNID)
	if err != nil {
		span.SetStatus(codes.Error, "Get versioning failed")
		return nil, versionError(ctx, "Get versioning failed", err)
	}
	versions, err := s.kva.ListVersions(ctx, token, entry.KNID)
	if err != nil {
		span.SetStatus(codes.Error, "List versions failed")
		return nil, versionError(ctx, "List versions failed", err)
	}
	releases, err := s.lineageReleases(ctx, entry.KNID)
	if err != nil {
		span.SetStatus(codes.Error, "List releases failed")
		return nil, internalError(ctx, "List releases failed", err)
	}
	byID := map[string]*interfaces.Release{}
	result := &interfaces.ProjectVersions{Versioning: *versioning, Entries: []interfaces.ProjectVersion{}}
	for _, r := range releases {
		byID[r.ID] = r
		// The project's newest prepared release waits to be published.
		if r.State == interfaces.RELEASE_STATE_PREPARED && r.ProjectID == entry.ID && result.Prepared == nil {
			result.Prepared = r
		}
	}
	for _, v := range versions {
		pv := interfaces.ProjectVersion{KNVersion: *v, Origin: interfaces.VERSION_ORIGIN_WORKSPACE}
		if r := byID[v.Source.ReleaseID]; r != nil {
			pv.Release = r.Version
			pv.Origin = interfaces.VERSION_ORIGIN_OTHER_PROJECT
			if r.ProjectID == entry.ID {
				pv.Origin = interfaces.VERSION_ORIGIN_PROJECT
			}
		}
		result.Entries = append(result.Entries, pv)
	}
	span.SetStatus(codes.Ok, "")
	return result, nil
}

// CopyVersion puts a version's definitions back into the project's branch, for the owner, to be
// changed and published as a new release. A version the network still keeps is copied into the
// network's draft too; one the community edition keeps only the record of is read from the tag of
// the release that published it.
func (s *projectReleaseService) CopyVersion(ctx context.Context, token string, projectID string, knVersion int,
	req interfaces.ProjectCopyVersion, caller interfaces.AccountInfo, author interfaces.GitAuthor) (*interfaces.CommitDefinitionsResult, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "CopyProjectVersion")
	defer span.End()

	entry, err := s.ownedProject(ctx, token, projectID, caller)
	if err != nil {
		span.SetStatus(codes.Error, "Project not writable")
		return nil, err
	}
	versioning, err := s.kva.GetVersioning(ctx, token, entry.KNID)
	if err != nil {
		span.SetStatus(codes.Error, "Get versioning failed")
		return nil, versionError(ctx, "Get versioning failed", err)
	}
	// The list keeps the record of every version; reading one by number is refused in the community
	// edition beyond the previous version, and for one cleaned up.
	versions, err := s.kva.ListVersions(ctx, token, entry.KNID)
	if err != nil {
		span.SetStatus(codes.Error, "List versions failed")
		return nil, versionError(ctx, "List versions failed", err)
	}
	var version *interfaces.KNVersion
	for _, v := range versions {
		if v.Version == knVersion {
			version = v
		}
	}
	if version == nil {
		span.SetStatus(codes.Error, "Version not found")
		return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Release_NotFound).
			WithErrorDetails(map[string]any{"kn_version": knVersion})
	}
	kept := version.State != interfaces.KN_VERSION_STATE_PURGED &&
		(versioning.FullHistory || knVersion == versioning.RunningVersion || knVersion == versioning.PreviousVersion)

	var files map[string][]byte
	if kept {
		if err := s.kva.ResetDraft(ctx, token, entry.KNID, knVersion, versioning.Draft.Revision, req.OverwriteDraftChanges); err != nil {
			span.SetStatus(codes.Error, "Copy into draft failed")
			return nil, versionError(ctx, "Copy into draft failed", err)
		}
		if files, err = s.kva.ExportVersion(ctx, token, entry.KNID, interfaces.KN_VERSION_DRAFT); err != nil {
			span.SetStatus(codes.Error, "Export draft failed")
			return nil, versionError(ctx, "Export draft failed", err)
		}
	} else {
		release, found, err := s.releaseOf(ctx, entry.KNID, version)
		if err != nil {
			span.SetStatus(codes.Error, "Get release failed")
			return nil, internalError(ctx, "Get release failed", err)
		}
		if !found {
			span.SetStatus(codes.Error, "Version purged")
			return nil, httperrors.NewHTTPError(ctx, http.StatusGone, perrors.BknProject_Release_VersionPurged).
				WithErrorDetails(map[string]any{"kn_version": knVersion})
		}
		if files, err = s.gs.ReadTree(ctx, entry.KNID, "refs/tags/v"+release.Version); err != nil {
			span.SetStatus(codes.Error, "Read release failed")
			return nil, internalError(ctx, "Read release failed", err)
		}
	}
	name := version.Label
	if name == "" {
		name = "version " + strconv.Itoa(knVersion)
	}
	result, err := s.ls.CommitDefinitions(ctx, entry.KNID, entry.Branch, files, fmt.Sprintf("copy %s as draft", name), author)
	if err != nil {
		span.SetStatus(codes.Error, "Commit definitions failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return result, nil
}

// releaseOf returns the release of the lineage that published version.
func (s *projectReleaseService) releaseOf(ctx context.Context, lineageID string, version *interfaces.KNVersion) (*interfaces.Release, bool, error) {
	releases, err := s.lineageReleases(ctx, lineageID)
	if err != nil {
		return nil, false, err
	}
	for _, r := range releases {
		if r.State == interfaces.RELEASE_STATE_PUBLISHED && (r.ID == version.Source.ReleaseID || r.KNVersion == version.Version) {
			return r, true, nil
		}
	}
	return nil, false, nil
}
