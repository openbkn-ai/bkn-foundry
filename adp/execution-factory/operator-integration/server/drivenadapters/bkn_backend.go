// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package drivenadapters

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/config"
	oerrors "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

var (
	bknBackendOnce     sync.Once
	bknBackendInstance interfaces.BknBackendClient
)

type bknBackendClient struct {
	baseURL    string
	logger     interfaces.Logger
	httpClient interfaces.HTTPClient
}

// NewBknBackendClient returns the bkn-backend internal client.
func NewBknBackendClient() interfaces.BknBackendClient {
	bknBackendOnce.Do(func() {
		conf := config.NewConfigLoader()
		bknBackendInstance = &bknBackendClient{
			baseURL: fmt.Sprintf("%s://%s:%d/api/bkn-backend/in", conf.BknBackend.PrivateProtocol,
				conf.BknBackend.PrivateHost, conf.BknBackend.PrivatePort),
			logger:     conf.GetLogger(),
			httpClient: rest.NewHTTPClient(),
		}
	})
	return bknBackendInstance
}

// RunCypher posts one statement to the internal cypher-queries endpoint.
//
// The internal endpoint trusts x-account-id and x-account-type, so they must be
// the caller this service already authenticated. Unlike other internal clients
// here there is no administrator fallback: a Cypher function reads data under
// its caller's row and column permissions, and running it as anyone else would
// hand out data the caller cannot read.
func (b *bknBackendClient) RunCypher(ctx context.Context, knID string, req *interfaces.CypherQueryReq) (*interfaces.CypherQueryResult, error) {
	accountID, accountType, ok := cypherCaller(ctx)
	if !ok {
		return nil, oerrors.DefaultHTTPError(ctx, http.StatusUnauthorized, "cypher function caller identity is unavailable")
	}
	headers := common.MergeTraceHeaders(ctx, map[string]string{})
	headers[string(interfaces.HeaderXAccountID)] = accountID
	headers[string(interfaces.HeaderXAccountType)] = accountType
	headers["Content-Type"] = "application/json"

	src := fmt.Sprintf("%s/v1/knowledge-networks/%s/cypher-queries", b.baseURL, url.PathEscape(knID))
	code, body, err := b.httpClient.PostNoUnmarshal(ctx, src, headers, req)
	if err != nil {
		b.logger.WithContext(ctx).Errorf("run cypher failed, kn_id=%s, explain=%v, err=%v", knID, req.Explain, err)
		return nil, oerrors.DefaultHTTPError(ctx, http.StatusBadGateway, "bkn-backend cypher query is unavailable")
	}
	return &interfaces.CypherQueryResult{StatusCode: code, Body: body}, nil
}

// cypherCaller is the account a statement runs as. Through a knowledge network's
// proxy (a logic property, an agent's execute_tool) the authenticated account is
// the network's proxy, which only vouches that the binding may call this tool;
// the caller it acts for, validated at the proxy boundary, is who reads. This is
// the same account a Python function reads BKN as, through the caller's own
// credential.
func cypherCaller(ctx context.Context) (accountID, accountType string, ok bool) {
	if proxy, isProxy := interfaces.ProxyExecutionContextFromContext(ctx); isProxy {
		accountID, accountType = strings.TrimSpace(proxy.CallerID), strings.TrimSpace(proxy.CallerType)
		return accountID, accountType, accountID != "" && accountType != ""
	}
	authContext, found := common.GetAccountAuthContextFromCtx(ctx)
	if !found || authContext == nil {
		return "", "", false
	}
	accountID, accountType = strings.TrimSpace(authContext.AccountID), string(authContext.AccountType)
	return accountID, accountType, accountID != "" && accountType != ""
}
