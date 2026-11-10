// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package logics

import (
	"context"
	"errors"
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"

	oerrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
)

// KNVersionError returns the error a failed bkn-backend read must be answered with when the
// failure is about the version of the query: bkn-backend's own refusal (purged version,
// changed draft, unknown version, ...) or a read that came back from another version than
// the query is pinned to. Such a query fails as a whole; the other failures keep the
// caller's own wrapping.
func KNVersionError(ctx context.Context, err error) (*httperrors.HTTPError, bool) {
	var changed *interfaces.KNVersionChangedError
	if errors.As(err, &changed) {
		return httperrors.NewHTTPError(ctx, http.StatusConflict, oerrors.OntologyQuery_KNVersion_Changed).
			WithErrorDetails(changed.Error()), true
	}
	var httpErr *httperrors.HTTPError
	if errors.As(err, &httpErr) && oerrors.IsPassThroughKNVersionError(httpErr.ErrorCode) {
		return httpErr, true
	}
	return nil, false
}

// ResolveDataProxy resolves the proxy context a data read goes through. It returns nil
// when the query reads the draft or a history version: the proxy's grants cover only the
// running version, so those reads run with the caller's own identity and Vega checks the
// caller's own permissions. Callers must treat a nil context as "no proxy", never as an error.
func ResolveDataProxy(ctx context.Context, resolver interfaces.ProxyContextResolver,
	binding interfaces.TrustedProxyBinding) (*interfaces.TrustedProxyContext, error) {

	if interfaces.KNVersionPinFromContext(ctx).UsesCallerIdentity() {
		return nil, nil
	}
	return resolver.Resolve(ctx, binding)
}

// ReadVersionToken names the model version a data read was served from, for cursors and
// cached results. A versioned network reports the version it was pinned to (v3, draft@7);
// a network without versioning keeps the version its proxy has published.
func ReadVersionToken(ctx context.Context, proxyContext *interfaces.TrustedProxyContext) string {
	if token := interfaces.KNVersionPinFromContext(ctx).Token(); token != "" {
		return token
	}
	if proxyContext != nil {
		return proxyContext.PublishedModelVersion
	}
	return ""
}
