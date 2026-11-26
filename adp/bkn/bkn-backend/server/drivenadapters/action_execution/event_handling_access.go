// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// eventHandlingTimeout bounds one execution request; the execution itself runs on afterwards.
const eventHandlingTimeout = 30 * time.Second

// Error codes of ontology-query that decide how an event's handling went.
const (
	governanceRequired = "OntologyQuery.ActionExecution.GovernanceRequired"
	duplicateExecution = "OntologyQuery.ActionExecution.DuplicateExecution"
	triggerTypeEvent   = "event"
)

// NewEventHandlingAccess executes the handling actions of events through ontology-query's
// internal execution route, the route the platform's schedules use.
func NewEventHandlingAccess(appSetting *common.AppSetting) interfaces.EventHandlingAccess {
	return &actionExecutionAccess{baseURL: appSetting.OntologyQueryUrl, httpClient: common.NewHTTPClient()}
}

func (a *actionExecutionAccess) ExecuteForEvent(ctx context.Context, knID, actionTypeID string,
	identity, inputs json.RawMessage) interfaces.EventHandlingOutcome {
	if a == nil || a.httpClient == nil || a.baseURL == "" {
		return interfaces.EventHandlingOutcome{Unavailable: errors.New("ontology-query is not configured")}
	}
	account, ok := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	if !ok || account.ID == "" || account.Type == "" {
		return interfaces.EventHandlingOutcome{Refused: "the event class has no execution subject"}
	}
	target := fmt.Sprintf("%s/api/ontology-query/in/v1/knowledge-networks/%s/action-types/%s/execute",
		a.baseURL, url.PathEscape(knID), url.PathEscape(actionTypeID))
	requestCtx, cancel := context.WithTimeout(ctx, eventHandlingTimeout)
	defer cancel()
	status, body, err := a.httpClient.PostNoUnmarshal(requestCtx, target, map[string]string{
		interfaces.CONTENT_TYPE_NAME:        interfaces.CONTENT_TYPE_JSON,
		interfaces.HTTP_HEADER_ACCOUNT_ID:   account.ID,
		interfaces.HTTP_HEADER_ACCOUNT_TYPE: account.Type,
	}, eventExecutionRequest(identity, inputs))
	if err != nil {
		return interfaces.EventHandlingOutcome{Unavailable: fmt.Errorf("execute action type %s: %w", actionTypeID, err)}
	}
	return eventHandlingOutcome(status, body)
}

// eventExecutionRequest asks ontology-query to execute an action type on the object of an event,
// with the class's values for its inputs when it gives any.
func eventExecutionRequest(identity, inputs json.RawMessage) map[string]any {
	request := map[string]any{
		"trigger_type":         triggerTypeEvent,
		"_instance_identities": []json.RawMessage{identity},
	}
	if len(inputs) > 0 && string(inputs) != "{}" && string(inputs) != "null" {
		request["input_bindings"] = inputs
	}
	return request
}

// eventHandlingOutcome reads ontology-query's answer to an execution request.
func eventHandlingOutcome(status int, body []byte) interfaces.EventHandlingOutcome {
	if status == http.StatusAccepted || status == http.StatusOK {
		var reply struct {
			ExecutionID string `json:"execution_id"`
		}
		if err := json.Unmarshal(body, &reply); err != nil || reply.ExecutionID == "" {
			return interfaces.EventHandlingOutcome{Refused: "ontology-query started the execution without naming it"}
		}
		return interfaces.EventHandlingOutcome{ExecutionID: reply.ExecutionID}
	}
	var reply httperrors.BaseError
	_ = json.Unmarshal(body, &reply)
	reason := upstreamReason(body)
	switch {
	case status == http.StatusConflict && reply.ErrorCode == governanceRequired:
		return interfaces.EventHandlingOutcome{NeedsPerson: reason}
	case status == http.StatusConflict && reply.ErrorCode == duplicateExecution:
		return interfaces.EventHandlingOutcome{Running: true}
	case status >= http.StatusBadRequest && status < http.StatusInternalServerError && status != http.StatusTooManyRequests:
		return interfaces.EventHandlingOutcome{Refused: reason}
	}
	return interfaces.EventHandlingOutcome{Unavailable: fmt.Errorf("ontology-query answered %d: %s", status, reason)}
}
