// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package drivenadapters defines driver adapters.
// @file appkey.go
// @description: Implement the AppKey verification interface, and the verification is completed by bkn-safe.
package drivenadapters

import (
	"context"
	"net/http"
	"sync"

	jsoniter "github.com/json-iterator/go"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/utils"
)

// appKeyIntrospectURI is the bkn-safe AppKey verification endpoint (in-cluster ClusterIP, token-free).
// Response structure alignment OAuth2 introspection.
const appKeyIntrospectURI = "/api/safe/v1/api-keys/introspect"

// appKeyIntrospectTimeout limits the time taken for verification requests.
// The default timeout of rest.NewHTTPClient() is 600 seconds, which used on the authentication path means that once bkn-safe hangs,
// A single request will tie up the connection for 10 minutes. Authentication must fail quickly, so it is the same as authorization_safe.go vs. bkn-safe.
// The call times out in the same file.
const appKeyIntrospectTimeout = 5

type appKeyVerifier struct {
	introspectURL string
	logger        interfaces.Logger
	httpClient    interfaces.HTTPClient
}

var (
	appKeyOnce sync.Once
	appKeyInst interfaces.AppKeyVerifier
)

// appKeyIntrospectResp is the bkn-safe validation response: any failure returns 200 {active:false},
// On success, the owner's identity is revealed.
type appKeyIntrospectResp struct {
	Active      bool   `json:"active"`
	Sub         string `json:"sub"`          // holder accessor id.
	AccountType string `json:"account_type"` // The account_type of the holder in bkn-safe.
	KeyID       string `json:"key_id"`
}

// NewAppKeyVerifier constructs an AppKey verifier supported by bkn-safe.
// It returns nil only when authentication is explicitly disabled.
func NewAppKeyVerifier() interfaces.AppKeyVerifier {
	appKeyOnce.Do(func() {
		if !config.GetAuthEnabled() {
			return // appKeyInst remains nil.
		}
		baseURL := mustBknSafeURL()
		appKeyInst = &appKeyVerifier{
			introspectURL: baseURL + appKeyIntrospectURI,
			logger:        config.NewConfigLoader().GetLogger(),
			httpClient:    rest.NewHTTPClientWithOptions(rest.HTTPClientOptions{TimeOut: appKeyIntrospectTimeout}),
		}
	})
	return appKeyInst
}

// Verify parses the AppKey into the holder's TokenInfo via bkn-safe.
// The result structure is exactly the same as the output of hydra introspection holder OAuth token,
// Therefore the downstream AccountAuthContext and all authorization decisions are not affected by the credential type.
func (v *appKeyVerifier) Verify(ctx context.Context, key string) (*interfaces.TokenInfo, error) {
	header := map[string]string{"Content-Type": "application/json"}
	_, resp, err := v.httpClient.Post(ctx, v.introspectURL, header, map[string]string{"token": key})
	if err != nil {
		v.logger.WithContext(ctx).Errorf("AppKey introspect request failed: %v", err)
		return nil, errors.DefaultHTTPError(ctx, http.StatusUnauthorized, "api key is invalid")
	}

	introspect := &appKeyIntrospectResp{}
	if err := jsoniter.Unmarshal(utils.ObjectToByte(resp), introspect); err != nil {
		v.logger.WithContext(ctx).Warnf("AppKey introspect decode failed: %+v, resp:%+v", err, resp)
		return nil, errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
	}
	if !introspect.Active {
		return nil, errors.DefaultHTTPError(ctx, http.StatusUnauthorized, "api key is invalid")
	}

	return &interfaces.TokenInfo{
		Active:     true,
		VisitorID:  introspect.Sub,
		VisitorTyp: appKeyVisitorType(introspect.AccountType),
		AccountTyp: appKeyAccountType(introspect.AccountType),
	}, nil
}

// appKeyVisitorType maps bkn-safe's account_type to the visitor type.
// "app" (application account) is mapped to Business, and other holders are regarded as real-name visitors and mapped to RealName.
// This makes the normal user's AccountAuthContext.AccountType consistent with the OAuth token path.
func appKeyVisitorType(accountType string) interfaces.VisitorType {
	if accountType == "app" {
		return interfaces.Business
	}
	return interfaces.RealName
}

// appKeyAccountType maps bkn-safe's account_type to the account type.
func appKeyAccountType(accountType string) interfaces.AccountType {
	if accountType == "id_card" {
		return interfaces.IDCard
	}
	return interfaces.Other
}
