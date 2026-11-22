// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
)

// apiKeyPrefix marks an API key a person issued for themselves in bkn-safe.
// Anything else is an OAuth token for Hydra.
const apiKeyPrefix = "bak_"

// apiKeyIntrospectPath is bkn-safe's internal, tokenless key verification, the
// one the knowledge network's MCP server calls too.
const apiKeyIntrospectPath = "/api/safe/v1/api-keys/introspect"

var errAPIKeyInactive = errors.New("api key is not active")

type apiKeyVerifier struct {
	url    string
	client *http.Client
}

func newAPIKeyVerifier(bknSafeURL string) *apiKeyVerifier {
	return &apiKeyVerifier{url: bknSafeURL + apiKeyIntrospectPath, client: &http.Client{Timeout: 10 * time.Second}}
}

// introspectResponse is bkn-safe's answer: {active:false} for any key it does
// not accept, otherwise the key's owner.
type introspectResponse struct {
	Active      bool   `json:"active"`
	Sub         string `json:"sub"`
	AccountType string `json:"account_type"`
}

// owner resolves a key to the account that issued it.
func (v *apiKeyVerifier) owner(ctx context.Context, key string) (introspectResponse, error) {
	body, err := json.Marshal(map[string]string{"token": key})
	if err != nil {
		return introspectResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.url, bytes.NewReader(body))
	if err != nil {
		return introspectResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := v.client.Do(req)
	if err != nil {
		return introspectResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return introspectResponse{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return introspectResponse{}, fmt.Errorf("api key introspection: status %d", resp.StatusCode)
	}
	var result introspectResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return introspectResponse{}, err
	}
	if !result.Active || result.Sub == "" {
		return introspectResponse{}, errAPIKeyInactive
	}
	return result, nil
}

// visitorType gives a key the type its owner's OAuth token would carry: an
// application account's key acts as the application, any other as a person.
func visitorType(accountType string) hydra.VisitorType {
	if accountType == string(hydra.VisitorType_App) {
		return hydra.VisitorType_App
	}
	return hydra.VisitorType_User
}
