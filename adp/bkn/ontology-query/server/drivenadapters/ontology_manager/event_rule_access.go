// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package ontology_manager

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	neturl "net/url"
	"strings"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/comm-go/jsoncodec"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
)

var (
	eventRuleAccessOnce sync.Once
	eventRuleAccess     interfaces.EventRuleAccess
)

// eventRuleAccessClient calls the bkn-backend routes a record-event action uses, as the account
// in the request context: the internal Cypher query and the event ledger's rule check.
type eventRuleAccessClient struct {
	bknBackendURL string
	httpClient    rest.HTTPClient
}

// NewEventRuleAccess returns the bkn-backend client of record-event actions.
func NewEventRuleAccess(appSetting *common.AppSetting) interfaces.EventRuleAccess {
	eventRuleAccessOnce.Do(func() {
		eventRuleAccess = &eventRuleAccessClient{
			bknBackendURL: appSetting.BKNBackendUrl,
			httpClient:    newVersionAwareHTTPClient(),
		}
	})
	return eventRuleAccess
}

func accountHeaders(ctx context.Context) map[string]string {
	account, _ := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	return map[string]string{
		interfaces.CONTENT_TYPE_NAME:        interfaces.CONTENT_TYPE_JSON,
		interfaces.HTTP_HEADER_ACCOUNT_ID:   account.ID,
		interfaces.HTTP_HEADER_ACCOUNT_TYPE: account.Type,
	}
}

// QueryCypher runs a read-only Cypher query of a branch of the network
// (POST {bkn-backend}/in/v1/knowledge-networks/{kn_id}/cypher-queries).
func (c *eventRuleAccessClient) QueryCypher(ctx context.Context, knID, branch string,
	query *interfaces.CypherQueryRequest) (*interfaces.CypherQueryResult, error) {
	if branch == "" {
		branch = interfaces.MAIN_BRANCH
	}
	url := fmt.Sprintf("%s/%s/cypher-queries?branch=%s", c.bknBackendURL, knID, neturl.QueryEscape(branch)) + versionQuery(ctx)
	body := *query
	if body.Parameters == nil {
		body.Parameters = map[string]any{}
	}
	var result interfaces.CypherQueryResult
	if err := c.post(ctx, "Run Cypher query", url, accountHeaders(ctx), &body, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// RecordRuleCheck hands one check to the event ledger
// (POST {bkn-backend}/in/v1/knowledge-networks/{kn_id}/rule-checks).
func (c *eventRuleAccessClient) RecordRuleCheck(ctx context.Context, knID, executionID string,
	check *interfaces.EventRuleCheckRequest) (*interfaces.EventRuleCheckResult, error) {
	url := fmt.Sprintf("%s/%s/rule-checks", c.bknBackendURL, knID)
	headers := accountHeaders(ctx)
	headers[interfaces.HTTPHeaderBKNExecutionID] = executionID
	var result interfaces.EventRuleCheckResult
	if err := c.post(ctx, "Record rule check", url, headers, check, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *eventRuleAccessClient) post(ctx context.Context, name, url string, headers map[string]string,
	body any, result any) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, name)
	defer span.End()
	oteltrace.AddAttrs4InternalHttp(span, oteltrace.TraceAttrs{
		HttpUrl: url, HttpMethod: http.MethodPost, HttpContentType: rest.ContentTypeJson,
	})
	// Encoded here so that json.Number values go as they were written.
	encoded, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("%s: encode the request: %w", strings.ToLower(name), err)
	}
	status, response, err := c.httpClient.PostNoUnmarshal(ctx, url, headers, encoded)
	if err != nil {
		oteltrace.AddHttpAttrs4Error(span, status, "InternalError", "HTTP POST failed")
		return fmt.Errorf("%s: %w", strings.ToLower(name), err)
	}
	if status != http.StatusOK {
		oteltrace.AddHttpAttrs4Error(span, status, "DependencyError", "HTTP status is not 200")
		return bknBackendRefusal(ctx, status, response)
	}
	if err := jsoncodec.Unmarshal(response, result); err != nil {
		oteltrace.AddHttpAttrs4Error(span, status, "InternalError", "Unmarshal response failed")
		return fmt.Errorf("%s: invalid response: %w", strings.ToLower(name), err)
	}
	oteltrace.AddHttpAttrs4Ok(span, status)
	return nil
}

// bknBackendRefusal keeps bkn-backend's status and error, so the execution record says why the
// call was refused (a bad query, a missing permission, an event class that is gone).
func bknBackendRefusal(ctx context.Context, status int, body []byte) error {
	var source httperrors.BaseError
	if len(body) == 0 || json.Unmarshal(body, &source) != nil || source.ErrorCode == "" {
		return &httperrors.HTTPError{HTTPCode: status, BaseError: httperrors.BaseError{
			ErrorCode:    httperrors.PublicError_InternalServerError,
			Description:  fmt.Sprintf("bkn-backend answered %d", status),
			ErrorDetails: strings.TrimSpace(string(body)),
		}}
	}
	if versionErr := versionRefusal(ctx, body); versionErr != nil {
		return versionErr
	}
	return &httperrors.HTTPError{HTTPCode: status, BaseError: source}
}
