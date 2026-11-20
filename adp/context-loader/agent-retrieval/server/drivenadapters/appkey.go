// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package drivenadapters

import (
	"context"
	"net/http"
	"strings"
	"sync"

	jsoniter "github.com/json-iterator/go"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/utils"
)

// appKeyIntrospectURI is bkn-safe's internal AppKey verification endpoint
// (tokenless, ClusterIP). Response mirrors OAuth2 introspection.
const appKeyIntrospectURI = "/api/safe/v1/api-keys/introspect"

// executionGrantIntrospectURI verifies an execution credential the same way.
const executionGrantIntrospectURI = "/api/safe/v1/execution-grants/introspect"

type appKeyVerifier struct {
	introspectURL          string
	executionIntrospectURL string
	logger                 interfaces.Logger
	httpClient             interfaces.HTTPClient
}

var (
	appKeyOnce sync.Once
	appKeyInst interfaces.AppKeyVerifier
)

// appKeyIntrospectResp is bkn-safe's verify response: 200 {active:false} on any
// failure, otherwise the resolved owner identity.
type appKeyIntrospectResp struct {
	Active      bool   `json:"active"`
	Sub         string `json:"sub"` // owner accessor id
	SubjectName string `json:"subject_name"`
	AccountType string `json:"account_type"` // bkn-safe account_type of the owner
	KeyID       string `json:"key_id"`
	KeyName     string `json:"key_name"`
	// An execution credential's grant and the network it is bound to.
	DelegationID string `json:"delegation_id"`
	KnID         string `json:"kn_id"`
}

// NewAppKeyVerifier builds the bkn-safe-backed AppKey verifier.
func NewAppKeyVerifier() interfaces.AppKeyVerifier {
	appKeyOnce.Do(func() {
		conf := config.NewConfigLoader()
		appKeyInst = &appKeyVerifier{
			introspectURL:          conf.BknSafe.BuildURL(appKeyIntrospectURI),
			executionIntrospectURL: conf.BknSafe.BuildURL(executionGrantIntrospectURI),
			logger:                 conf.GetLogger(),
			httpClient:             rest.NewHTTPClient(),
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
	url, execution := v.introspectURL, strings.HasPrefix(key, interfaces.ExecutionGrantPrefix)
	if execution {
		url = v.executionIntrospectURL
	}
	_, resp, err := v.httpClient.Post(ctx, url, header, body)
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
	// An execution credential that names no network or grant is not one.
	if execution && (introspect.KnID == "" || introspect.DelegationID == "" || introspect.AccountType == "app") {
		return nil, errors.DefaultHTTPError(ctx, http.StatusUnauthorized, "api key is invalid")
	}

	info := &interfaces.TokenInfo{
		Active:         true,
		VisitorID:      introspect.Sub,
		VisitorName:    introspect.SubjectName,
		VisitorTyp:     appKeyVisitorType(introspect.AccountType),
		AccountTyp:     appKeyAccountType(introspect.AccountType),
		CredentialID:   introspect.KeyID,
		CredentialName: introspect.KeyName,
	}
	if execution {
		info.CredentialID, info.CredentialName = introspect.DelegationID, ""
		info.DelegationID, info.DelegatedKnID = introspect.DelegationID, introspect.KnID
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
