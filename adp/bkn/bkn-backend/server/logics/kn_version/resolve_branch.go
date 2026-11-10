// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kn_version

import (
	"context"
	"net/http"
	"strconv"

	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

const knVersionRunning = "running"

func invalidVersion(ctx context.Context, detail string) error {
	return newHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_KNVersion_InvalidVersion).WithErrorDetails(detail)
}

func (s *service) ResolveBranch(ctx context.Context, knID string, req interfaces.KNBranchRequest) (*interfaces.KNResolvedBranch, error) {
	if s.va == nil {
		return resolveUnversioned(ctx, req)
	}
	state, err := s.va.GetState(ctx, nil, knID, false)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	if state == nil {
		return resolveUnversioned(ctx, req)
	}
	if state.Mode == interfaces.KNVersioningDisabling && req.Write {
		return nil, newHTTPError(ctx, http.StatusConflict, berrors.BknBackend_KNVersion_VersioningDisabling)
	}
	if req.Write {
		if req.KNVersion != "" {
			return nil, invalidVersion(ctx, "writes to a versioned knowledge network go to its draft; kn_version is not accepted")
		}
		if req.Branch != "" && req.Branch != interfaces.MAIN_BRANCH && req.Branch != interfaces.DRAFT_BRANCH {
			return nil, invalidVersion(ctx, "a versioned knowledge network accepts only the main or draft branch")
		}
		return &interfaces.KNResolvedBranch{Branch: interfaces.DRAFT_BRANCH, Versioned: true}, nil
	}

	target := req.KNVersion
	switch {
	case target != "" && req.Branch != "" && req.Branch != interfaces.MAIN_BRANCH:
		return nil, invalidVersion(ctx, "use either kn_version or branch, not both")
	case target == "" && (req.Branch == "" || req.Branch == interfaces.MAIN_BRANCH):
		target = knVersionRunning
	case target == "" && req.Branch == interfaces.DRAFT_BRANCH:
		target = interfaces.DRAFT_BRANCH
	case target == "":
		return nil, invalidVersion(ctx, "a versioned knowledge network is read by kn_version, not by branch")
	}

	switch target {
	case knVersionRunning:
		if state.RunningVersion == 0 {
			// An unpublished network reads as empty: main has only its network row.
			return &interfaces.KNResolvedBranch{Branch: interfaces.MAIN_BRANCH, Versioned: true}, nil
		}
		n := state.RunningVersion
		return &interfaces.KNResolvedBranch{Branch: interfaces.VersionBranch(n), Versioned: true, Version: strconv.Itoa(n)}, nil
	case interfaces.DRAFT_BRANCH:
		if state.Mode == interfaces.KNVersioningDisabling {
			return nil, newHTTPError(ctx, http.StatusConflict, berrors.BknBackend_KNVersion_VersioningDisabling)
		}
		if err := s.checkPermission(ctx, knID, interfaces.OPERATION_TYPE_MODIFY); err != nil {
			return nil, err
		}
		if req.DraftRevision != nil && *req.DraftRevision != state.DraftRevision {
			return nil, revisionMismatch(ctx, state)
		}
		return &interfaces.KNResolvedBranch{Branch: interfaces.DRAFT_BRANCH, Versioned: true,
			Version: interfaces.DRAFT_BRANCH, DraftRevision: state.DraftRevision}, nil
	}

	n, err := strconv.Atoi(target)
	if err != nil || n <= 0 {
		return nil, invalidVersion(ctx, "kn_version must be draft, running or a version number")
	}
	if n != state.RunningVersion && state.Mode == interfaces.KNVersioningDisabling {
		return nil, newHTTPError(ctx, http.StatusConflict, berrors.BknBackend_KNVersion_VersioningDisabling)
	}
	v, err := s.va.GetVersion(ctx, nil, knID, n, false)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	if v == nil {
		return nil, newHTTPError(ctx, http.StatusNotFound, berrors.BknBackend_KNVersion_VersionNotFound)
	}
	if v.Status == interfaces.KNVersionPurged {
		return nil, newHTTPError(ctx, http.StatusGone, berrors.BknBackend_KNVersion_VersionPurged)
	}
	if !s.editionAllows(state, n) {
		return nil, editionRestricted(ctx)
	}
	return &interfaces.KNResolvedBranch{Branch: interfaces.VersionBranch(n), Versioned: true, Version: strconv.Itoa(n)}, nil
}

// resolveUnversioned keeps the branch parameter as it was; kn_version=running means main.
func resolveUnversioned(ctx context.Context, req interfaces.KNBranchRequest) (*interfaces.KNResolvedBranch, error) {
	switch req.KNVersion {
	case "":
		branch := req.Branch
		if branch == "" {
			branch = interfaces.MAIN_BRANCH
		}
		return &interfaces.KNResolvedBranch{Branch: branch}, nil
	case knVersionRunning:
		if req.Branch != "" && req.Branch != interfaces.MAIN_BRANCH {
			return nil, invalidVersion(ctx, "use either kn_version or branch, not both")
		}
		return &interfaces.KNResolvedBranch{Branch: interfaces.MAIN_BRANCH}, nil
	}
	return nil, newHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_KNVersion_NotVersioned)
}

func (s *service) CheckReadEnd(ctx context.Context, knID string, resolved *interfaces.KNResolvedBranch) error {
	state, err := s.va.GetState(ctx, nil, knID, false)
	if err != nil {
		return internalError(ctx, err)
	}
	if resolved.Branch == interfaces.DRAFT_BRANCH {
		// Disabling deletes the state together with the draft.
		if state == nil || state.DraftRevision != resolved.DraftRevision {
			return newHTTPError(ctx, http.StatusConflict, berrors.BknBackend_KNVersion_DraftRevisionMismatch)
		}
		return nil
	}
	n, err := strconv.Atoi(resolved.Version)
	if err != nil {
		return nil
	}
	v, err := s.va.GetVersion(ctx, nil, knID, n, false)
	if err != nil {
		return internalError(ctx, err)
	}
	if v == nil || v.Status == interfaces.KNVersionPurged {
		return newHTTPError(ctx, http.StatusGone, berrors.BknBackend_KNVersion_VersionPurged)
	}
	return nil
}
