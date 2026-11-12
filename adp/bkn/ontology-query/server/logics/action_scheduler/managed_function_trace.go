// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_scheduler

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
)

const actionTraceAPI = "/api/agent-observability/v1"

type managedFunctionTrace interface {
	Open(context.Context, *interfaces.ActionExecution, interfaces.CallerRuntimeCredential) (context.Context, func(string), error)
	Run(context.Context, string, func(context.Context) (any, error)) (any, error)
}

type actionTraceClient struct {
	baseURL string
	http    *http.Client
}

type actionTraceSession struct {
	client         *actionTraceClient
	execution      *interfaces.ActionExecution
	credential     interfaces.CallerRuntimeCredential
	conversationID string
	interactionID  string
	leaseToken     string
	leaseEpoch     uint64
}

type actionTraceSessionKey struct{}
type actionInvocationKey struct{}

func newManagedFunctionTraceFromEnv() managedFunctionTrace {
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("BKN_TRACE_CORE_URL")), "/")
	if baseURL == "" {
		return nil
	}
	return &actionTraceClient{baseURL: baseURL, http: &http.Client{Timeout: 10 * time.Second}}
}

func withActionInvocation(ctx context.Context, ordinal int) context.Context {
	return context.WithValue(ctx, actionInvocationKey{}, ordinal)
}

// Open owns a one-shot Interaction for the asynchronous action. The caller's
// Interaction may be terminal before the Function starts, so it is retained
// only as provenance in the Function operation's input.
func (c *actionTraceClient) Open(ctx context.Context, execution *interfaces.ActionExecution,
	credential interfaces.CallerRuntimeCredential) (context.Context, func(string), error) {
	noop := func(string) {}
	if credential.Authorization == "" || credential.ApplicationPrincipalID == "" {
		return ctx, noop, nil
	}
	if execution == nil || execution.Executor.ID == "" {
		return ctx, noop, errors.New("managed action Function requires an authenticated executor")
	}
	session := &actionTraceSession{client: c, execution: execution, credential: credential}
	if credential.ConversationID != "" || credential.InteractionID != "" || credential.ParentOperationID != "" {
		if credential.ConversationID == "" || credential.InteractionID == "" || credential.ParentOperationID == "" {
			return ctx, noop, errors.New("origin managed interaction is incomplete")
		}
		var origin struct {
			ConversationID string `json:"conversation_id"`
			InteractionID  string `json:"interaction_id"`
		}
		if err := c.request(ctx, session, http.MethodGet,
			"/operations/"+url.PathEscape(credential.ParentOperationID), nil, &origin); err != nil {
			return ctx, noop, fmt.Errorf("verify origin managed operation: %w", err)
		}
		if origin.ConversationID != credential.ConversationID || origin.InteractionID != credential.InteractionID {
			return ctx, noop, errors.New("origin managed operation does not belong to the supplied interaction")
		}
	}
	conversationKey := "action-execution:" + execution.ID
	var conversation struct {
		ID string `json:"conversation_id"`
	}
	if err := c.request(ctx, session, http.MethodPost, "/conversations:ensure-current", map[string]any{
		"external_conversation_key": conversationKey,
		"idempotency_key":           conversationKey,
		"one_shot":                  true,
	}, &conversation); err != nil {
		return ctx, noop, fmt.Errorf("create managed action conversation: %w", err)
	}
	if conversation.ID == "" {
		return ctx, noop, errors.New("managed action conversation has no ID")
	}
	session.conversationID = conversation.ID
	var interaction struct {
		ID         string `json:"interaction_id"`
		LeaseToken string `json:"lease_token"`
		LeaseEpoch uint64 `json:"lease_epoch"`
	}
	if err := c.request(ctx, session, http.MethodPost,
		"/conversations/"+url.PathEscape(conversation.ID)+"/interactions", map[string]any{
			"idempotency_key": conversationKey,
			"lease_seconds":   360,
		}, &interaction); err != nil {
		return ctx, noop, fmt.Errorf("start managed action interaction: %w", err)
	}
	if interaction.ID == "" || interaction.LeaseToken == "" || interaction.LeaseEpoch == 0 {
		return ctx, noop, errors.New("managed action interaction has no usable lease")
	}
	session.interactionID = interaction.ID
	session.leaseToken = interaction.LeaseToken
	session.leaseEpoch = interaction.LeaseEpoch
	managed := common.SetTraceContextToCtx(ctx, common.TraceContext{
		ConversationID: session.conversationID, InteractionID: session.interactionID,
	})
	managedCredential := credential
	managedCredential.ConversationID = session.conversationID
	managedCredential.InteractionID = session.interactionID
	managedCredential.ParentOperationID = ""
	managed = interfaces.WithCallerRuntimeCredential(managed, managedCredential)
	managed = context.WithValue(managed, actionTraceSessionKey{}, session)
	return managed, func(outcome string) {
		finishCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if outcome != "failed" && outcome != "cancelled" {
			// The Function result belongs to the action execution log. This
			// Interaction hands that result off instead of claiming an Agent answer.
			outcome = "handed_off"
		}
		err := c.request(finishCtx, session, http.MethodPost,
			"/interactions/"+url.PathEscape(session.interactionID)+"/finish", map[string]any{
				"outcome": outcome, "idempotency_key": conversationKey + ":finish",
				"reason": "action_execution_" + outcome,
			}, nil)
		if err != nil {
			logger.Warnf("Failed to finish managed action interaction %s: %v", session.interactionID, err)
		}
	}, nil
}

// Run registers the Function before invoking it. SDK reads then use this real
// Operation as their parent, instead of an unregistered HTTP child identifier.
func (c *actionTraceClient) Run(ctx context.Context, toolID string,
	run func(context.Context) (any, error)) (any, error) {
	session, ok := ctx.Value(actionTraceSessionKey{}).(*actionTraceSession)
	if !ok || session == nil {
		return run(ctx)
	}
	ordinal, _ := ctx.Value(actionInvocationKey{}).(int)
	operationKey := fmt.Sprintf("action-function:%s:%d", session.execution.ID, ordinal)
	input := map[string]any{
		"kn_id": session.execution.KNID, "action_type_id": session.execution.ActionTypeID,
		"action_execution_id": session.execution.ID, "invocation": ordinal,
	}
	if session.credential.ConversationID != "" && session.credential.InteractionID != "" {
		input["origin_conversation_id"] = session.credential.ConversationID
		input["origin_interaction_id"] = session.credential.InteractionID
		input["origin_operation_id"] = session.credential.ParentOperationID
	}
	var ensured struct {
		Operation struct {
			ID      string `json:"operation_id"`
			Attempt uint32 `json:"attempt"`
		} `json:"operation"`
		Receipt struct {
			ID string `json:"receipt_id"`
		} `json:"receipt"`
		Execute bool `json:"execute"`
	}
	err := c.request(ctx, session, http.MethodPost,
		"/conversations/"+url.PathEscape(session.conversationID)+"/interactions/"+
			url.PathEscape(session.interactionID)+"/operations:ensure", map[string]any{
			"operation_key": operationKey, "tool_name": toolID,
			"protocol": "internal", "source_module": "ontology-query-action-function/v1",
			"input": tracePayload(input), "required": true,
			"lease_token": session.leaseToken, "lease_epoch": session.leaseEpoch,
		}, &ensured)
	if err != nil {
		return nil, fmt.Errorf("register managed action Function: %w", err)
	}
	if !ensured.Execute || ensured.Operation.ID == "" || ensured.Receipt.ID == "" {
		return nil, errors.New("managed action Function operation is not executable")
	}
	traceContext, _ := common.GetTraceContextFromCtx(ctx)
	traceContext.OperationID = ensured.Operation.ID
	runCtx := common.SetTraceContextToCtx(ctx, traceContext)
	credential, _ := interfaces.CallerRuntimeCredentialFromContext(ctx)
	credential.ParentOperationID = ensured.Operation.ID
	runCtx = interfaces.WithCallerRuntimeCredential(runCtx, credential)
	result, runErr := run(runCtx)
	terminal := "complete"
	terminalPayload := map[string]any{"action_execution_id": session.execution.ID, "invocation": ordinal}
	if runErr != nil {
		terminal = "fail"
		terminalPayload["code"] = "action_function_failed"
	}
	requestID := common.NewBKNRequestID()
	traceID, spanID := newActionTraceIDs()
	finishBody := map[string]any{
		"receipt_id": ensured.Receipt.ID, "request_id": requestID,
		"trace_id": traceID, "span_id": spanID,
		"evidence_durability": "durable", "retryable": false,
	}
	if terminal == "complete" {
		finishBody["output"] = tracePayload(terminalPayload)
	} else {
		finishBody["error"] = tracePayload(terminalPayload)
	}
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	finishErr := c.request(finishCtx, session, http.MethodPost,
		"/operations/"+url.PathEscape(ensured.Operation.ID)+"/attempts/"+
			strconv.FormatUint(uint64(ensured.Operation.Attempt), 10)+":"+terminal,
		finishBody, nil)
	if finishErr != nil {
		logger.Warnf("Failed to finish managed action Function operation %s: %v", ensured.Operation.ID, finishErr)
	}
	return result, runErr
}

func tracePayload(value any) map[string]any {
	raw, _ := json.Marshal(value)
	return map[string]any{"mode": "inline", "media_type": "application/json",
		"byte_length": len(raw), "inline": value}
}

func newActionTraceIDs() (string, string) {
	var raw [24]byte
	if _, err := rand.Read(raw[:]); err != nil {
		sum := sha256.Sum256([]byte(common.NewBKNRequestID()))
		copy(raw[:], sum[:24])
	}
	return hex.EncodeToString(raw[:16]), hex.EncodeToString(raw[16:])
}

func (c *actionTraceClient) request(ctx context.Context, session *actionTraceSession,
	method, path string, body any, target any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+actionTraceAPI+path, reader)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	accountType := session.execution.Executor.Type
	subjectType := "user"
	if accountType == "app" {
		subjectType = "service"
	}
	request.Header.Set("x-account-id", session.execution.Executor.ID)
	request.Header.Set("x-account-type", accountType)
	request.Header.Set("X-BKN-Application-Principal-ID", session.credential.ApplicationPrincipalID)
	request.Header.Set("X-BKN-Effective-Subject-Type", subjectType)
	request.Header.Set("X-BKN-Effective-Subject-ID", session.execution.Executor.ID)
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var failure struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&failure)
		return fmt.Errorf("Trace Core rejected %s with status %d, code %s", path, response.StatusCode, failure.Error.Code)
	}
	if target != nil {
		if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(target); err != nil {
			return err
		}
	}
	return nil
}
