// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package lineage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/bkn-specification/bkn"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"
	"gopkg.in/yaml.v3"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/common"
	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/projecthooks"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/capability"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/drift"
)

const (
	// maxNetworkIDLength is the bkn-backend limit on a network id.
	maxNetworkIDLength = 128
	// maxProjectIDRunes and maxNotesBytes are what the release table holds:
	// f_project_id VARCHAR(40) and f_notes TEXT.
	maxProjectIDRunes = 40
	maxNotesBytes     = 65535
	// maxVersionLength leaves room for the lineage in the copy suffix.
	maxVersionLength = 32
)

var (
	versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	// releaseNameSuffix matches the version a release of the one-network-per-release scheme
	// appended to the name.
	releaseNameSuffix = regexp.MustCompile(`\s*·\s*[0-9]+\.[0-9]+\.[0-9]+$`)
)

// PrepareRelease checks a project branch and writes it, as version, into the draft of the lineage's
// versioned network under one hold of the lineage lock: the functions and Skills it uses are copied
// for the version first, then the whole draft is replaced. The running version does not change;
// the release is recorded as prepared, to be published once its regression was read. Preparing
// the same version again, while it is not published, replaces the prepared release.
func (s *lineageService) PrepareRelease(ctx context.Context, token string, lineageID string,
	req interfaces.PublishReleaseRequest, publisher interfaces.AccountInfo) (*interfaces.Release, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "PrepareRelease")
	defer span.End()

	if err := checkReleaseRequest(ctx, req.Branch, req.Version); err != nil {
		span.SetStatus(codes.Error, "Invalid request")
		return nil, err
	}
	if utf8.RuneCountInString(req.ProjectID) > maxProjectIDRunes {
		span.SetStatus(codes.Error, "Invalid project id")
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Release_InvalidProjectID)
	}
	if len(req.Notes) > maxNotesBytes {
		span.SetStatus(codes.Error, "Invalid notes")
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Release_InvalidNotes)
	}

	var release *interfaces.Release
	err := s.gs.WithLineageLock(ctx, lineageID, func(gs interfaces.GitStore) error {
		var err error
		release, err = s.prepare(ctx, gs, token, lineageID, req, publisher)
		return err
	})
	if err != nil {
		span.SetStatus(codes.Error, "Prepare release failed")
		return nil, toHTTPError(ctx, "Prepare release failed", err)
	}
	span.SetStatus(codes.Ok, "")
	return release, nil
}

// PreviewRelease runs the checks of a release without the lock: it changes nothing, and what it
// reads may move before a release, which checks it all again under the lock.
func (s *lineageService) PreviewRelease(ctx context.Context, token string, lineageID string, branch string,
	version string) (*interfaces.ReleasePreview, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "PreviewRelease")
	defer span.End()

	if err := checkReleaseRequest(ctx, branch, version); err != nil {
		span.SetStatus(codes.Error, "Invalid request")
		return nil, err
	}
	checked, err := s.checkRelease(ctx, s.gs, token, lineageID, branch, version, true)
	if err != nil {
		span.SetStatus(codes.Error, "Preview release failed")
		return nil, toHTTPError(ctx, "Preview release failed", err)
	}
	span.SetStatus(codes.Ok, "")
	return checked.preview, nil
}

// checkReleaseRequest refuses a branch or version no release can have.
func checkReleaseRequest(ctx context.Context, branch string, version string) error {
	if err := checkProjectBranch(ctx, branch); err != nil {
		return err
	}
	if len(version) > maxVersionLength || !versionPattern.MatchString(version) {
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Release_InvalidVersion)
	}
	return nil
}

// releaseCheck is what the checks of a release read and decided, for preparing to go on from.
type releaseCheck struct {
	preview *interfaces.ReleasePreview
	// refusals are the replies preparing is refused with, in the order they were found.
	refusals []error
	// copyFrom names the copies of the functions and Skills the latest release made.
	copyFrom string
	// ownDraft is set when the draft holds only what a prepared release of the lineage wrote.
	ownDraft   bool
	versioning *interfaces.KNVersioning
	previous   *capability.Manifest
	pkg        map[string][]byte
	steps      []*capability.Step
}

func (s *lineageService) prepare(ctx context.Context, gs interfaces.GitStore, token string, lineageID string,
	req interfaces.PublishReleaseRequest, publisher interfaces.AccountInfo) (*interfaces.Release, error) {
	checked, err := s.checkRelease(ctx, gs, token, lineageID, req.Branch, req.Version, false)
	if err != nil {
		return nil, err
	}
	if len(checked.refusals) > 0 {
		return nil, checked.refusals[0]
	}
	preview := checked.preview
	if req.ExpectedBranchCommit != "" && req.ExpectedBranchCommit != preview.BranchCommit {
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Release_ReviewOutdated)
	}

	publication, err := s.publication(ctx, gs, token, lineageID, req, preview.BranchCommit, publisher)
	if err != nil {
		return nil, err
	}
	if publication != nil {
		req.ProjectID = publication.ProjectID()
	}

	copyTo := copySuffix(lineageID, req.Version)
	pkg, released, err := s.applyCapabilities(ctx, token, checked.pkg, checked.steps, checked.copyFrom, copyTo)
	if err != nil {
		return nil, err
	}
	// Every write into the draft comes first; the regression then reads the draft at the revision
	// the last of them made. Edits someone made to the draft and did not publish are dropped only
	// when the owner said so, or when they are what an earlier preparing of this lineage wrote.
	imported, revision, err := s.kva.ReplaceDraft(ctx, token, pkg, interfaces.KNReplaceImport{
		ExpectedDraftRevision: checked.versioning.Draft.Revision,
		Overwrite:             req.OverwriteDraftChanges || checked.ownDraft,
	})
	if err != nil {
		return nil, err
	}
	if imported.Capabilities != nil && len(imported.Capabilities.Skipped) > 0 {
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Release_CapabilitiesSkipped).
			WithErrorDetails(map[string]any{"network_id": lineageID, "version": req.Version, "skipped": imported.Capabilities.Skipped})
	}
	if publication != nil {
		if err := publication.Mount(ctx, token); err != nil {
			return nil, err
		}
		// Mounting is the edition's own write into the draft; the revision it leaves is the one
		// prepared.
		written, err := s.kva.GetVersioning(ctx, token, lineageID)
		if err != nil {
			return nil, err
		}
		revision = written.Draft.Revision
	}
	// The concept index of the draft is built ahead, so the regression's searches find it ready
	// and publishing reuses it; the regression waits for it.
	if err := s.kva.BuildDraftIndex(ctx, token, lineageID); err != nil {
		return nil, err
	}

	// release.yaml is what the next release compares against; a lineage that never used a
	// function or Skill gets none.
	var recorded map[string][]byte
	if len(released) > 0 || !checked.previous.Empty() {
		manifest, err := capability.NextManifest(checked.previous, released, copyTo).Marshal()
		if err != nil {
			return nil, err
		}
		recorded = capability.Files(released)
		recorded[interfaces.GIT_MANIFEST_FILE] = manifest
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	now := time.Now().UnixMilli()
	release := &interfaces.Release{
		ID: id.String(), LineageID: lineageID, NetworkID: lineageID, Version: req.Version,
		BaseVersion: preview.BaseVersion, ProjectID: req.ProjectID, Publisher: publisher, Notes: req.Notes,
		State: interfaces.RELEASE_STATE_PREPARED, BranchCommit: preview.BranchCommit,
		DraftRevision: revision, Capabilities: recorded, CreateTime: now, UpdateTime: now,
	}
	stored, err := s.ra.PrepareRelease(ctx, release)
	if err != nil {
		return nil, err
	}
	if !stored {
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Release_VersionExists).
			WithErrorDetails(map[string]any{"lineage_id": lineageID, "version": req.Version})
	}
	return release, nil
}

// publication readies what the edition adds to the release, if anything.
func (s *lineageService) publication(ctx context.Context, gs interfaces.GitStore, token string, lineageID string,
	req interfaces.PublishReleaseRequest, branchCommit string, publisher interfaces.AccountInfo) (projecthooks.Publication, error) {
	hooks := projecthooks.Current().Publications
	if hooks == nil {
		return nil, nil
	}
	projectID := strings.TrimPrefix(req.Branch, "project/")
	publication, err := hooks.Prepare(ctx, gs, token, projecthooks.ReleaseTarget{DeclaredProjectID: req.ProjectID, ProjectID: projectID,
		LineageID: lineageID, Branch: req.Branch, BranchCommit: branchCommit, NetworkID: lineageID, Version: req.Version, Publisher: publisher})
	if err != nil {
		return nil, err
	}
	if publication != nil && req.ProjectID != "" && req.ProjectID != publication.ProjectID() {
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Release_InvalidProjectID)
	}
	return publication, nil
}

// PublishRelease publishes the prepared release of req.Version: the network's draft becomes its
// running version, labelled v<version>, at once for every consumer. Then the release's definitions
// are tagged and main moves to them. A release found published already, by an earlier attempt
// whose reply was lost, only has what follows the publish done again.
func (s *lineageService) PublishRelease(ctx context.Context, token string, lineageID string,
	req interfaces.PublishReleaseRequest, publisher interfaces.AccountInfo,
	author interfaces.GitAuthor) (*interfaces.PublishReleaseResult, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "PublishRelease")
	defer span.End()

	if err := checkReleaseRequest(ctx, req.Branch, req.Version); err != nil {
		span.SetStatus(codes.Error, "Invalid request")
		return nil, err
	}
	if len(req.Notes) > maxNotesBytes {
		span.SetStatus(codes.Error, "Invalid notes")
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Release_InvalidNotes)
	}
	var result *interfaces.PublishReleaseResult
	err := s.gs.WithLineageLock(ctx, lineageID, func(gs interfaces.GitStore) error {
		var err error
		result, err = s.publish(ctx, gs, token, lineageID, req, publisher, author)
		return err
	})
	if err != nil {
		span.SetStatus(codes.Error, "Publish release failed")
		return nil, toHTTPError(ctx, "Publish release failed", err)
	}
	span.SetStatus(codes.Ok, "")
	return result, nil
}

func (s *lineageService) publish(ctx context.Context, gs interfaces.GitStore, token string, lineageID string,
	req interfaces.PublishReleaseRequest, publisher interfaces.AccountInfo,
	author interfaces.GitAuthor) (*interfaces.PublishReleaseResult, error) {
	release, found, err := s.ra.GetRelease(ctx, lineageID, req.Version)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Release_NotPrepared)
	}
	result := &interfaces.PublishReleaseResult{Release: release, NetworkID: lineageID, Tag: "v" + release.Version}
	if release.State == interfaces.RELEASE_STATE_PREPARED {
		notes := req.Notes
		if release.Notes != "" {
			notes = strings.TrimSpace(notes + "\n\n" + release.Notes)
		}
		// Refused before the version is published: once it is, the notes must still fit the row.
		if len(notes) > maxNotesBytes {
			return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Release_InvalidNotes)
		}
		published, sent, err := s.publishVersion(ctx, token, release, notes, req.AllowOutdatedBase)
		if err != nil {
			return nil, err
		}
		// A retry resent the first request: its notes are what the version carries.
		notes = sent.Notes
		result.KNVersion, result.Warnings = published.Version, published.Warnings
		result.PublishedMetrics, result.PurgedVersion = published.PublishedMetrics, published.PurgedVersion
		now := time.Now().UnixMilli()
		if err := s.ra.MarkPublished(context.WithoutCancel(ctx), release.ID, published.Version, notes, now); err != nil {
			return nil, err
		}
		release.State, release.KNVersion, release.Notes, release.UpdateTime = interfaces.RELEASE_STATE_PUBLISHED, published.Version, notes, now
		publication, err := s.publication(ctx, gs, token, lineageID, req, release.BranchCommit, publisher)
		if err == nil && publication != nil {
			err = publication.Activate(ctx)
		}
		if err != nil {
			// The network version is already running; a private acknowledgement must not stop
			// main from following it.
			otellog.LogWarn(ctx, fmt.Sprintf("Activate knowledge publication of %s %s failed: %v", lineageID, release.Version, err))
		}
	} else {
		result.KNVersion = release.KNVersion
	}
	commit, err := s.recordInGit(ctx, gs, token, release, req.Branch, author)
	if err != nil {
		return nil, err
	}
	result.Commit = commit
	return result, nil
}

// publishVersion publishes the draft at the revision the release prepared, refusing a source the
// network's agents could not read. The request is kept with the release before it is sent, with
// an idempotency key of its own content: a retry whose first reply was lost sends it again byte for
// byte, under the same key, and gets the first result, never a second publish. Once bkn-backend
// has answered it with a refusal, nothing was published under it and it is forgotten: the next
// attempt, after the owner confirmed something or prepared again, is a new request with a new key.
func (s *lineageService) publishVersion(ctx context.Context, token string, release *interfaces.Release, notes string,
	allowOutdated bool) (*interfaces.KNVersionPublished, *interfaces.KNVersionPublish, error) {
	req := release.PublishRequest
	if req == nil {
		versioning, err := s.kva.GetVersioning(ctx, token, release.LineageID)
		if err != nil {
			return nil, nil, err
		}
		built := interfaces.KNVersionPublish{
			ExpectedRunningVersion: versioning.RunningVersion, ExpectedDraftRevision: release.DraftRevision, Notes: notes,
			Label: "v" + release.Version, AllowOutdatedBase: allowOutdated, FailOnSkippedSources: true,
			Source: interfaces.KNVersionSource{Type: interfaces.KN_VERSION_SOURCE_TYPE, ProjectID: release.ProjectID,
				Ref: release.BranchCommit, ReleaseID: release.ID},
		}
		if built.IdempotencyKey, err = publishKey(release, built); err != nil {
			return nil, nil, err
		}
		if err := s.ra.SavePublishRequest(ctx, release.ID, &built); err != nil {
			return nil, nil, err
		}
		req = &built
	}
	published, err := s.kva.Publish(ctx, token, release.LineageID, *req)
	var backendErr *interfaces.BknBackendError
	if errors.As(err, &backendErr) && backendErr.HTTPCode >= http.StatusBadRequest && backendErr.HTTPCode < http.StatusInternalServerError {
		if forgetErr := s.ra.SavePublishRequest(context.WithoutCancel(ctx), release.ID, nil); forgetErr != nil {
			otellog.LogWarn(ctx, fmt.Sprintf("Forget the refused publish of %s %s: %v", release.LineageID, release.Version, forgetErr))
		}
	}
	return published, req, err
}

// publishKey is the idempotency key of a publish request: the release's project, or its lineage
// when no project made it, its version, and a digest of the request, so that another request for
// the same release never reuses it.
func publishKey(release *interfaces.Release, req interfaces.KNVersionPublish) (string, error) {
	req.IdempotencyKey = ""
	body, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	owner := release.ProjectID
	if owner == "" {
		owner = release.LineageID
	}
	return fmt.Sprintf("%s:%s:%s", owner, release.Version, hex.EncodeToString(sum[:8])), nil
}

// recordInGit tags the release's definitions, brings main to them with what the running version
// reads back and the functions and Skills it uses, and the project branch up to main. Each step
// is skipped once done, so a retry finishes what an earlier attempt left.
func (s *lineageService) recordInGit(ctx context.Context, gs interfaces.GitStore, token string, release *interfaces.Release,
	branch string, author interfaces.GitAuthor) (string, error) {
	tag := "v" + release.Version
	tagged, err := gs.ResolveRef(ctx, release.LineageID, tagRef(tag))
	switch {
	case errors.Is(err, plumbing.ErrReferenceNotFound):
		if err := gs.Tag(ctx, release.LineageID, tag, release.BranchCommit); err != nil {
			return "", err
		}
	case err != nil:
		return "", err
	case tagged != release.BranchCommit:
		return "", httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Release_VersionExists).
			WithErrorDetails(map[string]any{"tag": tag, "commit": tagged})
	}
	mainHead, err := gs.ResolveRef(ctx, release.LineageID, branchRef(interfaces.GIT_MAIN_BRANCH))
	if err != nil {
		return "", err
	}
	commit := mainHead
	if base, err := gs.MergeBase(ctx, release.LineageID, mainHead, release.BranchCommit); err != nil {
		return "", err
	} else if base != release.BranchCommit {
		if commit, err = gs.FastForward(ctx, release.LineageID, interfaces.GIT_MAIN_BRANCH, release.BranchCommit); err != nil {
			return "", err
		}
		// bkn-backend re-serializes what it stores, network.bkn above all; committing what the
		// running version reads back keeps the next drift check clean. Should the export fail,
		// that check reports the difference instead, so the release stands.
		exported, err := s.kva.ExportVersion(ctx, token, release.LineageID, interfaces.KN_VERSION_RUNNING)
		if err != nil {
			otellog.LogWarn(ctx, fmt.Sprintf("Export running version of %s failed, main keeps the tag: %v", release.LineageID, err))
		} else if commit, err = gs.CommitFiles(ctx, release.LineageID, interfaces.GIT_MAIN_BRANCH, exported,
			fmt.Sprintf("sync live definitions after release %s", tag), author); err != nil {
			return "", err
		}
		if len(release.Capabilities) > 0 {
			if commit, err = gs.CommitCapabilities(ctx, release.LineageID, interfaces.GIT_MAIN_BRANCH, release.Capabilities,
				fmt.Sprintf("record functions and Skills of release %s", tag), author); err != nil {
				return "", err
			}
		}
	}
	// The project's next release starts from the version it published, not behind it. Should this
	// fail, the next release review shows the branch behind and syncing brings it up.
	if _, err := gs.FastForward(ctx, release.LineageID, branch, commit); err != nil {
		otellog.LogWarn(ctx, fmt.Sprintf("Bring branch %s up to main after release %s failed: %v", branch, tag, err))
	}
	return commit, nil
}

// ownDraft tells whether the draft at versioning holds only what a prepared release of the
// lineage wrote into it.
func (s *lineageService) ownDraft(ctx context.Context, lineageID string, versioning *interfaces.KNVersioning) (bool, error) {
	if !versioning.Draft.HasChanges {
		return true, nil
	}
	prepared, err := s.ra.ListReleases(ctx, interfaces.ReleasesQuery{LineageID: lineageID,
		State: interfaces.RELEASE_STATE_PREPARED, Limit: interfaces.MAX_RELEASE_LIMIT})
	if err != nil {
		return false, err
	}
	for _, r := range prepared {
		if r.DraftRevision == versioning.Draft.Revision {
			return true, nil
		}
	}
	return false, nil
}

// checkRelease runs every check of preparing the branch as version, in the order preparing needs
// them, without acting on any. A check that refuses is recorded as a refusal; with all it goes on
// to the next, otherwise it stops there. Definitions that make no release package stop it either
// way: nothing is left to plan. A failure to read is an error.
func (s *lineageService) checkRelease(ctx context.Context, gs interfaces.GitStore, token string, lineageID string,
	branch string, version string, all bool) (*releaseCheck, error) {
	checked := &releaseCheck{preview: &interfaces.ReleasePreview{
		LineageID: lineageID, Branch: branch, Version: version, Tag: "v" + version, NetworkID: lineageID,
		LiveNetworkID: lineageID, Capabilities: []interfaces.CapabilityPlan{}, Refusals: []interfaces.ReleaseRefusal{},
	}}
	preview := checked.preview
	// refuse records a refusal and tells whether to stop.
	refuse := func(err *httperrors.HTTPError) bool {
		checked.refusals = append(checked.refusals, err)
		preview.Refusals = append(preview.Refusals, interfaces.ReleaseRefusal{Code: err.BaseError.ErrorCode, Details: err.ErrorDetails})
		return !all
	}

	versioning, err := s.kva.GetVersioning(ctx, token, lineageID)
	if err != nil {
		return nil, err
	}
	checked.versioning = versioning
	if !versioning.Enabled && refuse(httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Release_NotVersioned).
		WithErrorDetails(map[string]any{"network_id": lineageID})) {
		return checked, nil
	}

	existing, found, err := s.ra.GetRelease(ctx, lineageID, version)
	if err != nil {
		return nil, err
	}
	// Any release of the version but one still prepared has taken it, a record of the
	// one-network-per-release scheme included.
	// A publish sent for it with no answer may have gone through: preparing again would build
	// another request and publish the version twice. Publishing again finishes it.
	if found && existing.State == interfaces.RELEASE_STATE_PREPARED && existing.PublishRequest != nil &&
		refuse(httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Release_PublishPending).
			WithErrorDetails(map[string]any{"lineage_id": lineageID, "version": version})) {
		return checked, nil
	}
	if found && existing.State != interfaces.RELEASE_STATE_PREPARED && refuse(httperrors.NewHTTPError(ctx, http.StatusConflict,
		perrors.BknProject_Release_VersionExists).WithErrorDetails(map[string]any{"lineage_id": lineageID, "version": version})) {
		return checked, nil
	}

	branchHead, err := gs.ResolveRef(ctx, lineageID, branchRef(branch))
	if err != nil {
		return nil, err
	}
	preview.BranchCommit = branchHead

	// The copies of functions and Skills follow the latest release. The network keeps its id, so
	// nothing in them or in the definitions is rewritten to another network: the key they are named
	// by never stands in their text (see copySuffix). Before the first release there is no key yet.
	checked.copyFrom = noCopyKey(lineageID)
	latest, err := s.ra.ListReleases(ctx, interfaces.ReleasesQuery{LineageID: lineageID, State: interfaces.RELEASE_STATE_PUBLISHED, Limit: 1})
	if err != nil {
		return nil, err
	}
	if len(latest) > 0 {
		preview.BaseVersion, checked.copyFrom = latest[0].Version, copySuffix(lineageID, latest[0].Version)
	}

	mainHead, err := gs.ResolveRef(ctx, lineageID, branchRef(interfaces.GIT_MAIN_BRANCH))
	if err != nil {
		return nil, err
	}
	preview.MainCommit = mainHead
	base, err := gs.MergeBase(ctx, lineageID, mainHead, branchHead)
	if err != nil {
		return nil, err
	}
	if base != mainHead && refuse(httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Release_BranchBehind).
		WithErrorDetails(map[string]any{"main": mainHead, "branch": branchHead})) {
		return checked, nil
	}

	stored, err := gs.ReadTree(ctx, lineageID, mainHead)
	if err != nil {
		return nil, err
	}
	// Edits made in the draft since main was last synced would be lost silently; what an earlier
	// preparing wrote there is the project's own.
	if checked.ownDraft, err = s.ownDraft(ctx, lineageID, versioning); err != nil {
		return nil, err
	}
	diff := interfaces.PathDiff{}
	if !checked.ownDraft {
		draft, err := s.kva.ExportVersion(ctx, token, lineageID, interfaces.KN_VERSION_DRAFT)
		if err != nil {
			return nil, err
		}
		diff = drift.DiffFiles(stored, draft)
	}
	// So would edits made to the functions and Skills the last release recorded.
	previous, tracked, capabilityDiff, err := drift.CheckCapabilities(ctx, gs, s.efa, token, lineageID, mainHead)
	if err != nil {
		return nil, err
	}
	checked.previous = previous
	if !diff.Empty() || !capabilityDiff.Empty() {
		if refuse(httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Release_Drift).
			WithErrorDetails(&interfaces.DriftReport{LineageID: lineageID, KNID: lineageID, PathDiff: diff, Capabilities: capabilityDiff})) {
			return checked, nil
		}
	}

	files, err := gs.ReadTree(ctx, lineageID, branchHead)
	if err != nil {
		return nil, err
	}
	preview.Changes = drift.DiffFiles(stored, files)
	pkg, err := buildPackage(files, lineageID)
	if err != nil {
		refuse(httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Lineage_InvalidDefinitions).
			WithErrorDetails(err.Error()))
		return checked, nil
	}
	if header, err := bkn.ParseNetworkFile(string(pkg[bkn.RootFileName]), bkn.RootFileName); err == nil {
		preview.NetworkName = header.Name
	}
	// Functions and Skills follow the network. What to reuse or copy, and whether the package's
	// references can move to the copies, is settled here; the copies are made when preparing,
	// before the draft is written, so the draft points at them.
	steps, err := s.planCapabilities(ctx, token, pkg, previous, tracked, checked.copyFrom, copySuffix(lineageID, version))
	var refusal *httperrors.HTTPError
	if errors.As(err, &refusal) {
		refuse(refusal)
		return checked, nil
	}
	if err != nil {
		return nil, err
	}
	checked.pkg, checked.steps = pkg, steps
	for _, step := range steps {
		preview.Capabilities = append(preview.Capabilities, interfaces.CapabilityPlan{
			Kind: string(step.Item.Kind), ID: step.Item.ID, Name: step.Item.Name, Copy: step.Copy,
			Reasons: append([]string{}, step.Reasons...),
		})
	}
	return checked, nil
}

// planCapabilities decides for each toolbox and Skill the package references
// whether it is reused or copied (see capability.Plan), and checks that the
// package's references can move to the copies, without making any. tracked
// are the live items the previous release recorded, already exported.
func (s *lineageService) planCapabilities(ctx context.Context, token string, pkg map[string][]byte,
	previous *capability.Manifest, tracked []*capability.Item, oldNetworkID string,
	networkID string) ([]*capability.Step, error) {
	network, err := loadNetwork(pkg)
	if err != nil {
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Lineage_InvalidDefinitions).
			WithErrorDetails(err.Error())
	}
	boxIDs, skillIDs := capability.References(network)
	known := map[string]*capability.Item{}
	for _, it := range tracked {
		known[it.Dir()] = it
	}
	items := []*capability.Item{}
	var newBoxes, newSkills []string
	for _, id := range boxIDs {
		if it, ok := known[(&capability.Item{Kind: capability.KIND_FUNCTION, ID: id}).Dir()]; ok {
			items = append(items, it)
		} else {
			newBoxes = append(newBoxes, id)
		}
	}
	for _, id := range skillIDs {
		if it, ok := known[(&capability.Item{Kind: capability.KIND_SKILL, ID: id}).Dir()]; ok {
			items = append(items, it)
		} else {
			newSkills = append(newSkills, id)
		}
	}
	exported, err := capability.Export(ctx, s.efa, token, newBoxes, newSkills)
	if err != nil {
		return nil, err
	}
	items = append(items, exported...)

	steps := capability.Plan(previous, items, oldNetworkID)
	copies, err := capability.Preview(steps, oldNetworkID, networkID)
	if err != nil {
		return nil, err
	}
	if _, err := rewriteToCopies(pkg, oldNetworkID, networkID, copies); err != nil {
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Lineage_InvalidDefinitions).
			WithErrorDetails(err.Error())
	}
	return steps, nil
}

// applyCapabilities makes the copies the steps call for and points the package
// at them. It returns the package and the items the new network uses, as git
// keeps them.
func (s *lineageService) applyCapabilities(ctx context.Context, token string, pkg map[string][]byte,
	steps []*capability.Step, oldNetworkID string, networkID string) (map[string][]byte, []*capability.Item, error) {
	copies, released, err := capability.Apply(ctx, s.efa, token, steps, oldNetworkID, networkID)
	if err != nil {
		return nil, nil, err
	}
	rewritten, err := rewriteToCopies(pkg, oldNetworkID, networkID, copies)
	if err != nil {
		return nil, nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Lineage_InvalidDefinitions).
			WithErrorDetails(err.Error())
	}
	return rewritten, released, nil
}

// rewriteToCopies points the package at the copies and checks that no
// reference the rewrite did not reach would leave the new network on an original.
func rewriteToCopies(pkg map[string][]byte, oldNetworkID string, networkID string,
	copies []*capability.Copy) (map[string][]byte, error) {
	rewritten, err := capability.RewritePackage(pkg, oldNetworkID, networkID, copies)
	if err != nil {
		return nil, err
	}
	network, err := loadNetwork(rewritten)
	if err != nil {
		return nil, err
	}
	boxIDs, skillIDs := capability.References(network)
	for _, c := range copies {
		if slices.Contains(boxIDs, c.OldID) || slices.Contains(skillIDs, c.OldID) {
			return nil, fmt.Errorf("%s %s was copied to %s, but the definitions still reference it", c.Kind, c.OldID, c.NewID)
		}
	}
	return rewritten, nil
}

// loadNetwork reads a definition set the way bkn-backend import does.
func loadNetwork(files map[string][]byte) (*bkn.BknNetwork, error) {
	fsys := bkn.NewMemoryFileSystem()
	fsys.AddFiles(files)
	return bkn.LoadNetworkWithFS(fsys, ".")
}

// tagRef names a tag in full, so a branch of the same name never shadows it.
func tagRef(name string) string {
	return plumbing.NewTagReferenceName(name).String()
}

// copySuffix is <lineage_id>_v<major>_<minor>_<patch>, what the copies of functions and Skills a
// release makes are named after (capability.Preview). The capability package rewrites the key a
// copy is made from to the one it is made for wherever it stands as a word; since the network id
// never changes, the keys are never the network id and never stand in any text, so nothing is
// rewritten: the copies and the definitions keep naming the lineage's network. It fits the 128 characters of a network id,
// which it once was: a long lineage id is cut, and the "_" or "-" the cut leaves at its end
// dropped. The version always survives whole.
func copySuffix(lineageID string, version string) string {
	suffix := "_v" + strings.ReplaceAll(version, ".", "_")
	prefix := lineageID
	if keep := maxNetworkIDLength - len(suffix); len(prefix) > keep {
		prefix = strings.TrimRight(prefix[:keep], "_-")
	}
	return prefix + suffix
}

// noCopyKey is the key before any release made copies: no version has it, and no function, Skill or
// definition names it.
func noCopyKey(lineageID string) string {
	return copySuffix(lineageID, "none")
}

// buildPackage gives the definitions of a release the network id of the lineage, drops the version
// an earlier release of the one-network-per-release scheme appended to the name, and checks that
// the BKN loader reads the result as that network.
func buildPackage(files map[string][]byte, networkID string) (map[string][]byte, error) {
	text, ok := files[bkn.RootFileName]
	if !ok {
		return nil, fmt.Errorf("%s not found", bkn.RootFileName)
	}
	current, err := bkn.ParseNetworkFile(string(text), bkn.RootFileName)
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", bkn.RootFileName, err)
	}
	name := strings.TrimSpace(releaseNameSuffix.ReplaceAllString(current.Name, ""))
	rewritten, err := setNetworkHeader(string(text), networkID, name)
	if err != nil {
		return nil, err
	}

	pkg := make(map[string][]byte, len(files))
	for p, data := range files {
		pkg[p] = data
	}
	pkg[bkn.RootFileName] = []byte(rewritten)

	network, err := loadNetwork(pkg)
	if err != nil {
		return nil, err
	}
	if network.ID != networkID || network.Name != name {
		return nil, fmt.Errorf("%s reads as %q (%q) after the rewrite, not %q (%q)",
			bkn.RootFileName, network.ID, network.Name, networkID, name)
	}
	return pkg, nil
}

// setNetworkHeader replaces the lines of the id and name keys in the frontmatter
// of network.bkn and keeps every other byte. Serializing the parsed network
// instead would rebuild the whole file: the SDK serializer writes 2.x
// frontmatter and needs every definition loaded to write the overview tables.
func setNetworkHeader(text string, id string, name string) (string, error) {
	return common.EditFrontmatter(text, bkn.RootFileName, func(root *yaml.Node, set common.FrontmatterSetter) error {
		for key, replacement := range map[string]string{"id": id, "name": name} {
			k, v := common.MappingValue(root, key)
			if k == nil {
				return fmt.Errorf("%s frontmatter has no id or name", bkn.RootFileName)
			}
			if err := set(k, v, replacement); err != nil {
				return err
			}
		}
		return nil
	})
}
