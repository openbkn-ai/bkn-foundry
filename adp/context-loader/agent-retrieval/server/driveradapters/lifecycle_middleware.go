// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
)

func middlewareLifecycle(client *bkntrace.LifecycleClient) gin.HandlerFunc {
	guard := bkntrace.NewGuard(client)
	return func(c *gin.Context) {
		if !isLifecycleBusinessRequest(c.Request) {
			c.Next()
			return
		}
		raw, err := io.ReadAll(c.Request.Body)
		if err != nil {
			writeLifecycleHTTPError(c, http.StatusBadRequest, bkntrace.APIError{
				Code: "operation_required", Message: "business request body is invalid",
				RequiredAction: "ensure_operation",
			})
			return
		}
		var input map[string]any
		if len(bytes.TrimSpace(raw)) == 0 {
			input = map[string]any{}
		} else if err := json.Unmarshal(raw, &input); err != nil {
			writeLifecycleHTTPError(c, http.StatusBadRequest, bkntrace.APIError{
				Code: "operation_required", Message: "business request body must be a JSON object",
				RequiredAction: "ensure_operation",
			})
			return
		}
		businessContext, apiErr := parseHTTPBusinessContext(input, httpKnowledgeNetworkID(c, input))
		if apiErr != nil {
			writeLifecycleHTTPError(c, lifecycleHTTPStatus(apiErr.Code), *apiErr)
			return
		}
		delete(input, "bkn_context")
		toolName := lifecycleHTTPToolName(c)
		inputHash := normalizedHTTPInputHash(input, businessContext)
		operationKey, apiErr := managedHTTPOperationKey(c, toolName, businessContext, inputHash)
		if apiErr != nil {
			writeLifecycleHTTPError(c, lifecycleHTTPStatus(apiErr.Code), *apiErr)
			return
		}
		businessContext.OperationKey = operationKey
		downstreamBody, _ := json.Marshal(input)
		c.Request.Body = io.NopCloser(bytes.NewReader(downstreamBody))
		c.Request.ContentLength = int64(len(downstreamBody))

		ctx, state, disposition, coreErr, err := guard.Begin(c.Request.Context(), bkntrace.GuardIntent{
			Context:      businessContext,
			ToolName:     toolName,
			Protocol:     "sdk",
			SourceModule: "context-loader",
			Input:        downstreamBody,
		})
		if err != nil {
			writeLifecycleHTTPError(c, http.StatusServiceUnavailable, lifecycleUnavailableError(client))
			return
		}
		if coreErr != nil {
			writeLifecycleHTTPError(c, lifecycleHTTPStatus(coreErr.Code), *coreErr)
			return
		}
		switch disposition {
		case bkntrace.GuardPending:
			writeLifecycleHTTPResult(c, http.StatusConflict, map[string]any{
				"error": bkntrace.APIError{
					Code: "receipt_pending", Message: "operation receipt is still pending",
					Retryable: true, RequiredAction: "poll_receipt",
				},
				"receipt": state.Result.Receipt,
			})
			return
		case bkntrace.GuardReplay:
			status := http.StatusOK
			if state.Result.Receipt.ReceiptStatus == "failed" {
				status = http.StatusConflict
			}
			writeLifecycleHTTPResult(c, status, map[string]any{
				"operation": state.Result.Operation,
				"receipt":   state.Result.Receipt,
			})
			return
		}

		c.Request = c.Request.WithContext(ctx)
		originalWriter := c.Writer
		buffered := &lifecycleResponseWriter{ResponseWriter: originalWriter, status: http.StatusOK}
		c.Writer = buffered
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					c.Writer = originalWriter
					panicPayload, _ := json.Marshal(map[string]any{
						"code": "handler_panic", "message": fmt.Sprint(recovered), "stage": "handler",
					})
					_, _, _ = guard.Finish(
						ctx, state, panicPayload, true, false,
					)
					panic(recovered)
				}
			}()
			c.Next()
		}()
		c.Writer = originalWriter

		payload := traceLifecyclePayload(buffered.body.Bytes(), buffered.status)
		failed := buffered.status >= http.StatusBadRequest
		finished, coreErr, err := guard.Finish(
			ctx, state, payload, failed, buffered.status >= http.StatusInternalServerError,
		)
		if err != nil || coreErr != nil {
			writeBufferedLifecycleResponse(c, buffered, state.Result.Receipt)
			return
		}
		writeBufferedLifecycleResponse(c, buffered, finished.Receipt)
	}
}

func traceLifecyclePayload(raw []byte, status int) json.RawMessage {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && json.Valid(trimmed) {
		return append(json.RawMessage(nil), trimmed...)
	}
	encoded, _ := json.Marshal(map[string]any{
		"status_code": status,
		"body":        string(raw),
	})
	return encoded
}

func isLifecycleBusinessRequest(request *http.Request) bool {
	if request.Method != http.MethodPost {
		return false
	}
	return strings.Contains(request.URL.Path, "/kn/") ||
		(strings.Contains(request.URL.Path, "/mcp/proxy/") && strings.HasSuffix(request.URL.Path, "/call"))
}

func parseHTTPBusinessContext(input map[string]any, currentKNID string) (bkntrace.BusinessContext, *bkntrace.APIError) {
	raw, _ := input["bkn_context"].(map[string]any)
	for field := range raw {
		if field != "conversation_id" && field != "interaction_id" &&
			field != "parent_operation_id" && field != "causation_event_ids" && field != "business_refs" {
			return bkntrace.BusinessContext{}, &bkntrace.APIError{
				Code: "invalid_business_context", Message: "bkn_context contains an unsupported field",
				RequiredAction: "correct_bkn_context",
			}
		}
	}
	value := bkntrace.BusinessContext{
		ConversationID:    httpString(raw["conversation_id"]),
		InteractionID:     httpString(raw["interaction_id"]),
		ParentOperationID: httpString(raw["parent_operation_id"]),
		CausationEventIDs: httpStringSlice(raw["causation_event_ids"]),
	}
	switch {
	case value.ConversationID == "":
		return value, &bkntrace.APIError{
			Code: "conversation_required", Message: "conversation_id is required",
			RequiredAction: "create_conversation",
		}
	case value.InteractionID == "":
		return value, &bkntrace.APIError{
			Code: "interaction_required", Message: "interaction_id is required",
			RequiredAction: "start_interaction",
		}
	}
	refs, apiErr := bkntrace.ParseBusinessRefs(raw["business_refs"], currentKNID)
	if apiErr != nil {
		return value, apiErr
	}
	value.BusinessRefs = refs
	return value, nil
}

func httpKnowledgeNetworkID(c *gin.Context, input map[string]any) string {
	if value := httpString(input["kn_id"]); value != "" {
		return value
	}
	return strings.TrimSpace(c.GetHeader("X-Kn-ID"))
}

// managedHTTPOperationKey derives the idempotency identity from trusted
// request correlation. A caller may supply an opaque invocation header to
// preserve the identity across a transport retry; it is never accepted in the
// request body and is not used for authorization.
func managedHTTPOperationKey(
	c *gin.Context,
	toolName string,
	businessContext bkntrace.BusinessContext,
	inputHash string,
) (string, *bkntrace.APIError) {
	invocationID := strings.TrimSpace(c.GetHeader(common.HeaderBKNClientInvocationID))
	if invocationID != "" {
		if !validHTTPLifecycleHint(invocationID) {
			return "", &bkntrace.APIError{
				Code: "lifecycle_hint_invalid", Message: "X-OpenBKN-Client-Invocation-Id must be printable ASCII without spaces and at most 256 bytes",
				RequiredAction: "fix_host_lifecycle_hint",
			}
		}
		return hashHTTPOperationKey("http-invocation", businessContext, toolName, hashLifecyclePayload([]byte(invocationID))), nil
	}
	requestID := ""
	if traceContext, ok := common.GetTraceContextFromCtx(c.Request.Context()); ok {
		requestID = traceContext.RequestID
	}
	return hashHTTPOperationKey("http-request", businessContext, toolName, requestID+"\x00"+inputHash), nil
}

func hashHTTPOperationKey(scope string, businessContext bkntrace.BusinessContext, toolName, identity string) string {
	payload, _ := json.Marshal(struct {
		Scope          string
		ConversationID string
		InteractionID  string
		ToolName       string
		Identity       string
	}{scope, businessContext.ConversationID, businessContext.InteractionID, toolName, identity})
	sum := sha256.Sum256(payload)
	return "http:" + hex.EncodeToString(sum[:16])
}

func validHTTPLifecycleHint(value string) bool {
	if len(value) > 256 {
		return false
	}
	for index := 0; index < len(value); index++ {
		if value[index] < 0x21 || value[index] > 0x7e {
			return false
		}
	}
	return true
}

func lifecycleHTTPToolName(c *gin.Context) string {
	if value := c.Param("tool_name"); value != "" {
		return value
	}
	return path.Base(c.Request.URL.Path)
}

func normalizedHTTPInputHash(input map[string]any, businessContext bkntrace.BusinessContext) string {
	causationIDs := append([]string(nil), businessContext.CausationEventIDs...)
	sort.Strings(causationIDs)
	value := map[string]any{
		"input": input,
		"bkn_causality": map[string]any{
			"parent_operation_id": businessContext.ParentOperationID,
			"causation_event_ids": causationIDs,
		},
	}
	raw, _ := json.Marshal(value)
	return hashLifecyclePayload(raw)
}

func hashLifecyclePayload(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func lifecycleUnavailableError(client *bkntrace.LifecycleClient) bkntrace.APIError {
	if client == nil || !client.Enabled() {
		return bkntrace.APIError{
			Code: "feature_not_installed", Message: "BKN Trace Core is not configured",
		}
	}
	return bkntrace.APIError{
		Code: "trace_core_unavailable", Message: "BKN Trace Core is temporarily unavailable",
		Retryable: true, RequiredAction: "retry_later",
	}
}

func lifecycleHTTPStatus(code string) int {
	switch code {
	case "conversation_required", "interaction_required", "operation_required", "invalid_business_context", "invalid_business_ref", "lifecycle_hint_invalid":
		return http.StatusBadRequest
	// capability_not_licensed belongs with these, not with permission_denied:
	// a licence gap must be indistinguishable from the resource not existing,
	// while a permission gap is told plainly (ee-design.md §4.5 — Missing Certificate →.
	// Pretending not to; lack of permission → explicitly denied). Sharing an arm with permission_denied made.
	// an entitlement boundary answer like an authorization one, which is the
	// one distinction the two-binary contract depends on.
	case "conversation_not_found", "resource_not_disclosed", "capability_not_licensed":
		return http.StatusNotFound
	case "conversation_owner_mismatch", "permission_denied":
		return http.StatusForbidden
	case "evidence_capture_denied":
		return http.StatusForbidden
	case "feature_not_installed":
		return http.StatusNotImplemented
	case "trace_core_unavailable":
		return http.StatusServiceUnavailable
	case "evidence_capture_failed":
		return http.StatusBadGateway
	default:
		return http.StatusConflict
	}
}

func writeLifecycleHTTPError(c *gin.Context, status int, value bkntrace.APIError) {
	writeLifecycleHTTPResult(c, status, map[string]any{"error": value})
}

func writeLifecycleHTTPResult(c *gin.Context, status int, value any) {
	c.Header("Content-Type", "application/json")
	c.Status(status)
	_ = json.NewEncoder(c.Writer).Encode(value)
	c.Abort()
}

type lifecycleResponseWriter struct {
	gin.ResponseWriter
	body    bytes.Buffer
	status  int
	written bool
}

func (w *lifecycleResponseWriter) WriteHeader(code int) {
	if !w.written {
		w.status = code
	}
}

func (w *lifecycleResponseWriter) WriteHeaderNow() {
	w.written = true
}

func (w *lifecycleResponseWriter) Write(data []byte) (int, error) {
	w.WriteHeaderNow()
	return w.body.Write(data)
}

func (w *lifecycleResponseWriter) WriteString(value string) (int, error) {
	w.WriteHeaderNow()
	return w.body.WriteString(value)
}

func (w *lifecycleResponseWriter) Status() int { return w.status }
func (w *lifecycleResponseWriter) Size() int   { return w.body.Len() }
func (w *lifecycleResponseWriter) Written() bool {
	return w.written
}
func (w *lifecycleResponseWriter) Flush() {}

func writeBufferedLifecycleResponse(
	c *gin.Context,
	buffered *lifecycleResponseWriter,
	receipt bkntrace.Receipt,
) {
	c.Header(common.HeaderBKNReceiptID, receipt.ReceiptID)
	c.Header(common.HeaderBKNOperationID, receipt.OperationID)
	c.Status(buffered.status)
	_, _ = c.Writer.Write(buffered.body.Bytes())
}

func httpString(value any) string {
	result, _ := value.(string)
	return result
}

func httpStringSlice(value any) []string {
	items, _ := value.([]any)
	result := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			result = append(result, text)
		}
	}
	return result
}
