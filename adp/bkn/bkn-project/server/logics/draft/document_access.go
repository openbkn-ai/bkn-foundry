// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"errors"

	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbconcept"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
)

func checkDocumentReadAccess(ctx context.Context, token string, draft *interfaces.Draft) error {
	if draft == nil || len(kbconcept.DraftSourceIDs(draft.Items)) == 0 {
		return nil
	}
	providers, enabled := kb.Lookup()
	var err error
	if !enabled {
		err = kb.ErrUnavailable
	} else {
		err = kbconcept.CheckDraftSources(kb.WithOperatorAuthorization(ctx, token), providers.Source, draft.Items)
	}
	if err == nil {
		return nil
	}
	status, code, key := 503, httperrors.PublicError_ServiceUnavailable, "SourceUnavailable"
	var upstream *kb.SourceError
	if errors.As(err, &upstream) {
		switch upstream.StatusCode {
		case 401:
			status, code, key = 401, httperrors.PublicError_Unauthorized, "AuthenticationExpired"
		case 403:
			status, code, key = 403, httperrors.PublicError_Forbidden, "PermissionDenied"
		case 404:
			status, code, key = 404, httperrors.PublicError_NotFound, "SourceMissing"
		}
	}
	message, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknProject.KnowledgeBase."+key, nil)
	result := httperrors.NewHTTPError(ctx, status, code)
	if renderErr == nil {
		return result.WithErrorDetails(message)
	}
	return result
}
