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
		businessContext, apiErr := parseHTTPBusinessContext(input)
		if apiErr != nil {
			writeLifecycleHTTPError(c, lifecycleHTTPStatus(apiErr.Code), *apiErr)
			return
		}
		delete(input, "bkn_context")
		downstreamBody, _ := json.Marshal(input)
		c.Request.Body = io.NopCloser(bytes.NewReader(downstreamBody))
		c.Request.ContentLength = int64(len(downstreamBody))

		ctx, state, disposition, coreErr, err := guard.Begin(c.Request.Context(), bkntrace.GuardIntent{
			Context:             businessContext,
			ToolName:            lifecycleHTTPToolName(c),
			NormalizedInputHash: normalizedHTTPInputHash(input, businessContext),
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
					_, _, _ = guard.Finish(
						ctx, state, hashLifecyclePayload(buffered.body.Bytes()), true, false,
					)
					panic(recovered)
				}
			}()
			c.Next()
		}()
		c.Writer = originalWriter

		payloadHash := hashLifecyclePayload(buffered.body.Bytes())
		failed := buffered.status >= http.StatusBadRequest
		finished, coreErr, err := guard.Finish(
			ctx, state, payloadHash, failed, buffered.status >= http.StatusInternalServerError,
		)
		if err != nil {
			writeLifecycleHTTPError(c, http.StatusServiceUnavailable, lifecycleUnavailableError(client))
			return
		}
		if coreErr != nil {
			if coreErr.Code == "receipt_pending" {
				writeLifecycleHTTPResult(c, lifecycleHTTPStatus(coreErr.Code), map[string]any{
					"error":   *coreErr,
					"receipt": finished.Receipt,
				})
				return
			}
			writeLifecycleHTTPError(c, lifecycleHTTPStatus(coreErr.Code), *coreErr)
			return
		}
		writeBufferedLifecycleResponse(c, buffered, finished.Receipt)
	}
}

func isLifecycleBusinessRequest(request *http.Request) bool {
	if request.Method != http.MethodPost {
		return false
	}
	return strings.Contains(request.URL.Path, "/kn/") ||
		(strings.Contains(request.URL.Path, "/mcp/proxy/") && strings.HasSuffix(request.URL.Path, "/call"))
}

func parseHTTPBusinessContext(input map[string]any) (bkntrace.BusinessContext, *bkntrace.APIError) {
	raw, _ := input["bkn_context"].(map[string]any)
	value := bkntrace.BusinessContext{
		ConversationID:    httpString(raw["conversation_id"]),
		InteractionID:     httpString(raw["interaction_id"]),
		OperationKey:      httpString(raw["operation_key"]),
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
	case value.OperationKey == "":
		return value, &bkntrace.APIError{
			Code: "operation_required", Message: "operation_key is required",
			RequiredAction: "ensure_operation",
		}
	default:
		return value, nil
	}
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
	message := "BKN Trace Core is unavailable"
	if client == nil || !client.Enabled() {
		message = "BKN Trace Core is not configured"
	}
	return bkntrace.APIError{
		Code: "feature_not_installed", Message: message,
		RequiredAction: "install_enterprise_implementation",
	}
}

func lifecycleHTTPStatus(code string) int {
	switch code {
	case "conversation_required", "interaction_required", "operation_required":
		return http.StatusBadRequest
	case "conversation_not_found", "resource_not_disclosed":
		return http.StatusNotFound
	case "conversation_owner_mismatch", "permission_denied", "capability_not_licensed":
		return http.StatusForbidden
	case "feature_not_installed":
		return http.StatusNotImplemented
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
