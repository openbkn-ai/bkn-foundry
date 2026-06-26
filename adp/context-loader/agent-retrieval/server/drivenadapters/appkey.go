// Copyright 2026 openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package drivenadapters

import (
	"context"
	"net/http"
	"sync"

	jsoniter "github.com/json-iterator/go"

	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/infra/config"
	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/infra/rest"
	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/utils"
)

// appKeyIntrospectURI is bkn-safe's internal AppKey verification endpoint
// (tokenless, ClusterIP). Response mirrors OAuth2 introspection.
const appKeyIntrospectURI = "/api/safe/v1/api-keys/introspect"

type appKeyVerifier struct {
	introspectURL string
	logger        interfaces.Logger
	httpClient    interfaces.HTTPClient
}

var (
	appKeyOnce sync.Once
	appKeyInst interfaces.AppKeyVerifier
)

// appKeyIntrospectResp is bkn-safe's verify response: 200 {active:false} on any
// failure, otherwise the resolved owner identity.
type appKeyIntrospectResp struct {
	Active      bool   `json:"active"`
	Sub         string `json:"sub"`          // owner accessor id
	AccountType string `json:"account_type"` // bkn-safe account_type of the owner
	KeyID       string `json:"key_id"`
}

// NewAppKeyVerifier builds the bkn-safe-backed AppKey verifier. Returns nil when
// AUTH_ENABLED=false (AppKey verification is disabled together with auth); the
// caller treats a nil verifier as "no AppKey support" and falls back to hydra.
func NewAppKeyVerifier() interfaces.AppKeyVerifier {
	appKeyOnce.Do(func() {
		if !config.GetAuthEnabled() {
			return // leave appKeyInst nil
		}
		conf := config.NewConfigLoader()
		appKeyInst = &appKeyVerifier{
			introspectURL: conf.BknSafe.BuildURL(appKeyIntrospectURI),
			logger:        conf.GetLogger(),
			httpClient:    rest.NewHTTPClient(),
		}
	})
	return appKeyInst
}

// Verify resolves an AppKey to the owner's TokenInfo via bkn-safe. The result is
// shaped exactly like a hydra introspection of the owner's OAuth token, so the
// downstream AccountAuthContext (and all authz) is identical.
func (v *appKeyVerifier) Verify(ctx context.Context, key string) (*interfaces.TokenInfo, error) {
	body, _ := jsoniter.Marshal(map[string]string{"token": key})
	header := map[string]string{"Content-Type": "application/json"}
	_, resp, err := v.httpClient.Post(ctx, v.introspectURL, header, body)
	if err != nil {
		v.logger.WithContext(ctx).Error(err)
		return nil, err
	}

	introspect := &appKeyIntrospectResp{}
	if err := jsoniter.Unmarshal(utils.ObjectToByte(resp), introspect); err != nil {
		v.logger.WithContext(ctx).Warnf("AppKey introspect decode failed: %+v, resp:%+v", err, resp)
		return nil, errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
	}
	if !introspect.Active {
		return nil, errors.DefaultHTTPError(ctx, http.StatusUnauthorized, "api key is invalid")
	}

	info := &interfaces.TokenInfo{
		Active:     true,
		VisitorID:  introspect.Sub,
		VisitorTyp: appKeyVisitorType(introspect.AccountType),
		AccountTyp: appKeyAccountType(introspect.AccountType),
	}
	return info, nil
}

// appKeyVisitorType maps a bkn-safe account_type to the gateway's VisitorType.
// "app" (application account) -> Business; every other owner is a person-like
// accessor -> RealName. This yields AccountAuthContext.AccountType == "user" for
// normal users (matching the OAuth-token path).
func appKeyVisitorType(accountType string) interfaces.VisitorType {
	if accountType == "app" {
		return interfaces.Business
	}
	return interfaces.RealName
}

// appKeyAccountType maps a bkn-safe account_type to the gateway's AccountType.
func appKeyAccountType(accountType string) interfaces.AccountType {
	if accountType == "id_card" {
		return interfaces.IDCard
	}
	return interfaces.Other
}
