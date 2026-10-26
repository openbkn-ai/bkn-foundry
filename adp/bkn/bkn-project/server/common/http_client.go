// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package common

import (
	"sync"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

var (
	httpClientOnce sync.Once
	httpClient     rest.HTTPClient
)

// NewHTTPClient returns the client shared by calls to other services, as in bkn-backend.
func NewHTTPClient() rest.HTTPClient {
	httpClientOnce.Do(func() {
		httpClient = rest.NewHTTPClientWithOptions(rest.HttpClientOptions{
			TimeOut: 900,
		})
	})

	return httpClient
}
