// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package locale

import (
	"net/http"

	sharedrest "github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

type effectiveLocaleTransport struct {
	next http.RoundTripper
}

func (t effectiveLocaleTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	cloned := request.Clone(request.Context())
	cloned.Header = request.Header.Clone()
	cloned.Header.Set(
		sharedrest.AcceptLanguageHeader,
		sharedrest.GetLanguageByCtx(request.Context()),
	)
	return t.next.RoundTrip(cloned)
}

// WrapHTTPClient returns a shallow copy that overwrites any raw language range
// with the request context's single effective locale.
func WrapHTTPClient(client *http.Client) *http.Client {
	if client == nil {
		client = http.DefaultClient
	}
	cloned := *client
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	cloned.Transport = effectiveLocaleTransport{next: transport}
	return &cloned
}
