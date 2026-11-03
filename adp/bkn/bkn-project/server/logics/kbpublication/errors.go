// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbpublication

import (
	"context"
	"errors"
	"net/http"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbinstance"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
)

// PublicError exposes a business action, never private snapshot/hold identities.
func PublicError(ctx context.Context, err error) error {
	var httpErr *httperrors.HTTPError
	if errors.As(err, &httpErr) {
		return httpErr
	}
	var source *kb.SourceError
	if errors.As(err, &source) && (source.StatusCode == 401 || source.StatusCode == 403) {
		return httperrors.NewHTTPError(ctx, http.StatusForbidden, perrors.BknProject_Release_SourcePermission)
	}
	var backend *interfaces.BknBackendError
	if errors.As(err, &backend) && (backend.HTTPCode == 401 || backend.HTTPCode == 403) {
		return httperrors.NewHTTPError(ctx, http.StatusForbidden, perrors.BknProject_Release_SourcePermission)
	}
	if errors.Is(err, ErrInvalidSnapshot) || errors.Is(err, kb.ErrVersionGone) || errors.Is(err, kbinstance.ErrAcceptanceRequired) || errors.Is(err, kbinstance.ErrBuildChanged) || errors.Is(err, kbinstance.ErrInvalidBuild) {
		return httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Release_KnowledgeBaseNotReady)
	}
	if source != nil || errors.Is(err, kb.ErrUnavailable) || errors.Is(err, kb.ErrPublishedModelMissing) {
		return httperrors.NewHTTPError(ctx, http.StatusServiceUnavailable, perrors.BknProject_Release_KnowledgeBaseUnavailable)
	}
	// Git/database failures also occur before any KB operation, including in
	// table-only releases. Preserve their existing outer error classification.
	return err
}
