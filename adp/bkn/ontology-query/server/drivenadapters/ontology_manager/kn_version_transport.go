// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package ontology_manager

import (
	"context"
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
)

// knVersionTransport hands the version headers of bkn-backend responses to the query's pin.
// The shared REST client returns only the status and the body, so the headers are read here,
// where the request context (and with it the pin) is still at hand.
type knVersionTransport struct {
	base http.RoundTripper
}

func (t *knVersionTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return resp, err
	}
	pin := interfaces.KNVersionPinFromContext(req.Context())
	if pin == nil || resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return resp, nil
	}
	pin.ObserveRunning(resp.Header.Get(interfaces.HTTPHeaderKNRunningVersion))
	if err := pin.Observe(resp.Header.Get(interfaces.HTTPHeaderKNVersion),
		resp.Header.Get(interfaces.HTTPHeaderKNDraftRevision)); err != nil {
		_ = resp.Body.Close()
		return nil, err
	}
	return resp, nil
}

// newVersionAwareHTTPClient is the REST client for bkn-backend reads.
func newVersionAwareHTTPClient() rest.HTTPClient {
	raw := rest.NewRawHTTPClient()
	raw.Transport = &knVersionTransport{base: raw.Transport}
	return rest.NewHTTPClientWithRawClient(raw)
}

// versionQuery returns the version parameters of the query in ctx, to append to a URL that
// already has a query string. It is empty when the request named no version and none was
// reported yet, which keeps unversioned networks on the exact request they always got.
func versionQuery(ctx context.Context) string {
	return interfaces.KNVersionPinFromContext(ctx).QueryString()
}
