// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package logics

import (
	"context"
	"errors"
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// proxyPermissionMissing is bkn-backend's refusal when the publisher cannot read a data source the
// network's agents would read through it.
const proxyPermissionMissing = "BknBackend.KnowledgeNetwork.Proxy.PermissionMissing"

// proxySyncPending is bkn-backend's reply when another sync holds the network's proxy lease.
const proxySyncPending = "BknBackend.KnowledgeNetwork.Proxy.SyncPending"

// proxySyncFailed and proxyUnavailable are the network's proxy not ready for the version.
const (
	proxySyncFailed  = "BknBackend.KnowledgeNetwork.Proxy.SyncFailed"
	proxyUnavailable = "BknBackend.KnowledgeNetwork.Proxy.Unavailable"
)

type versionRefusal struct {
	status int
	code   string
}

// versionRefusals are the bkn-backend version refusals bkn-project answers in its own words.
var versionRefusals = map[string]versionRefusal{
	interfaces.KN_VERSION_ERROR_NOT_VERSIONED:           {http.StatusConflict, perrors.BknProject_Release_NotVersioned},
	interfaces.KN_VERSION_ERROR_DRAFT_HAS_CHANGES:       {http.StatusConflict, perrors.BknProject_Release_DraftHasChanges},
	interfaces.KN_VERSION_ERROR_DRAFT_REVISION_MISMATCH: {http.StatusConflict, perrors.BknProject_Release_DraftChanged},
	interfaces.KN_VERSION_ERROR_DRAFT_BASE_OUTDATED:     {http.StatusConflict, perrors.BknProject_Release_DraftOutdated},
	interfaces.KN_VERSION_ERROR_RUNNING_MISMATCH:        {http.StatusConflict, perrors.BknProject_Release_RunningChanged},
	interfaces.KN_VERSION_ERROR_NOTHING_TO_PUBLISH:      {http.StatusConflict, perrors.BknProject_Release_NothingToPublish},
	interfaces.KN_VERSION_ERROR_VALIDATION_FAILED:       {http.StatusBadRequest, perrors.BknProject_Release_ValidationFailed},
	interfaces.KN_VERSION_ERROR_BROKEN_REFERENCES:       {http.StatusConflict, perrors.BknProject_Rollback_BrokenReferences},
	interfaces.KN_VERSION_ERROR_NOT_ACTIVATABLE:         {http.StatusConflict, perrors.BknProject_Rollback_NotActivatable},
	interfaces.KN_VERSION_ERROR_EDITION_RESTRICTED:      {http.StatusForbidden, perrors.BknProject_Rollback_EditionRestricted},
	interfaces.KN_VERSION_ERROR_PURGED:                  {http.StatusGone, perrors.BknProject_Release_VersionPurged},
	interfaces.KN_VERSION_ERROR_CONCEPT_INDEX_FAILED:    {http.StatusServiceUnavailable, perrors.BknProject_Release_NotReady},
	interfaces.KN_VERSION_ERROR_PROXY_SYNC_REQUIRED:     {http.StatusServiceUnavailable, perrors.BknProject_Release_NotReady},
	interfaces.KN_VERSION_ERROR_CONCEPT_INDEX_BUILDING:  {http.StatusServiceUnavailable, perrors.BknProject_Release_NotReady},
	proxySyncPending: {http.StatusServiceUnavailable, perrors.BknProject_Release_NotReady},
	proxySyncFailed:  {http.StatusServiceUnavailable, perrors.BknProject_Release_NotReady},
	proxyUnavailable: {http.StatusServiceUnavailable, perrors.BknProject_Release_NotReady},
	// The key is the project and the version, one operation each; reusing it is a fault here.
	interfaces.KN_VERSION_ERROR_IDEMPOTENCY_KEY_REUSED: {http.StatusInternalServerError, perrors.BknProject_Release_InternalError},
	proxyPermissionMissing:                             {http.StatusForbidden, perrors.BknProject_Release_SourcePermission},
}

// KNVersionHTTPError words a bkn-backend version refusal as bkn-project's own, keeping the details
// bkn-backend gave (the problems found, the versions involved). ok is false for any other error.
func KNVersionHTTPError(ctx context.Context, err error) (error, bool) {
	var backendErr *interfaces.BknBackendError
	if !errors.As(err, &backendErr) {
		return nil, false
	}
	refusal, ok := versionRefusals[backendErr.ErrorCode]
	if !ok {
		return nil, false
	}
	httpErr := httperrors.NewHTTPError(ctx, refusal.status, refusal.code)
	if backendErr.ErrorDetails != nil {
		httpErr = httpErr.WithErrorDetails(backendErr.ErrorDetails)
	}
	return httpErr, true
}
