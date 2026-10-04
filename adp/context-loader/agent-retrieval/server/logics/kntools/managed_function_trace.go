// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kntools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode"

	"github.com/bytedance/sonic"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/drivenadapters"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/permission"
)

const managedFunctionSourceModule = "context-loader-managed-function/v1"

// ManagedFunctionTraceInput contains only server-validated function identity and business data.
type ManagedFunctionTraceInput struct {
	KnowledgeNetworkID string
	ToolboxID          string
	ToolID             string
	Descriptor         interfaces.ManagedFunctionDescriptor
	Arguments          map[string]any
}

// ManagedFunctionTrace adds one Function Operation around the existing proxy call.
type ManagedFunctionTrace interface {
	Execute(
		ctx context.Context,
		input ManagedFunctionTraceInput,
		run func(context.Context) (map[string]any, error),
	) (map[string]any, error)
}

type managedFunctionGuard struct {
	guard   *bkntrace.Guard
	enabled bool
}

// NewManagedKnToolsService creates the Context Loader MCP service with the same lifecycle client
// used by the outer execute_tool guard.
func NewManagedKnToolsService(lifecycleClient *bkntrace.LifecycleClient) KnToolsService {
	conf := config.NewConfigLoader()
	return &knToolsService{
		logger: conf.GetLogger(), operator: drivenadapters.NewOperatorIntegrationClient(),
		bknBackend: drivenadapters.NewBknBackendAccess(),
		knAuthz:    permission.NewKnowledgeNetworkAuthorizer(conf),
		managedFunctionTrace: &managedFunctionGuard{
			guard: bkntrace.NewGuard(lifecycleClient), enabled: lifecycleClient != nil && lifecycleClient.Enabled(),
		},
	}
}

func (m *managedFunctionGuard) Execute(
	ctx context.Context,
	input ManagedFunctionTraceInput,
	run func(context.Context) (map[string]any, error),
) (map[string]any, error) {
	if run == nil {
		return nil, errors.New("managed function callback is unavailable")
	}
	ctx = bkntrace.WithTraceAvailability(ctx)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if m == nil || !m.enabled || m.guard == nil {
		bkntrace.MarkTraceUnavailable(ctx, "function_begin", "execute_tool", "feature_not_installed")
		return runAndCleanTraceGaps(bkntrace.ClearManagedTraceContext(ctx), run)
	}
	traceContext, ok := common.GetTraceContextFromCtx(ctx)
	if !ok || traceContext.ConversationID == "" || traceContext.InteractionID == "" || traceContext.OperationID == "" {
		return runAndCleanTraceGaps(bkntrace.ClearManagedTraceContext(ctx), run)
	}
	inputPayload := map[string]any{
		"kn_id": input.KnowledgeNetworkID, "toolbox_id": input.ToolboxID, "tool_id": input.ToolID,
		"function_name": input.Descriptor.Name, "function_description": input.Descriptor.Description,
		"function_version": normalizedFunctionVersion(input.Descriptor.Version),
		"arguments":        redactFunctionPayload(input.Arguments),
	}
	rawInput, err := sonic.ConfigStd.Marshal(inputPayload)
	if err != nil {
		return nil, fmt.Errorf("serialize managed function input: %w", err)
	}
	functionRef := bkntrace.BusinessRef{
		RefType: "function", RefID: "function:" + input.KnowledgeNetworkID + ":" + input.ToolID,
		Version: normalizedFunctionVersion(input.Descriptor.Version), DisplayHint: input.Descriptor.Name,
	}
	lifecycleContext, state, disposition, apiErr, err := m.guard.Begin(ctx, bkntrace.GuardIntent{
		Context: bkntrace.BusinessContext{
			ConversationID: traceContext.ConversationID, InteractionID: traceContext.InteractionID,
			OperationKey:      managedFunctionOperationKey(traceContext, input),
			ParentOperationID: traceContext.OperationID,
			BusinessRefs: []bkntrace.BusinessRef{
				{RefType: "knowledge_network", RefID: "kn:" + input.KnowledgeNetworkID, Version: "unversioned"},
				functionRef,
			},
		},
		ToolName: input.ToolID, Protocol: "internal", SourceModule: managedFunctionSourceModule,
		Input: rawInput, CapabilityProfile: managedFunctionCapabilityProfile(input),
	})
	if err != nil || apiErr != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if bkntrace.IsTraceInfrastructureFailure(apiErr, err) {
			code := "trace_core_unavailable"
			if apiErr != nil {
				code = apiErr.Code
			}
			bkntrace.MarkTraceUnavailable(ctx, "function_begin", "execute_tool", code)
			return runAndCleanTraceGaps(bkntrace.ClearManagedTraceContext(ctx), run)
		}
		if err != nil {
			return nil, fmt.Errorf("start managed function trace: %w", err)
		}
	}
	if apiErr != nil {
		return nil, fmt.Errorf("start managed function trace: %s: %s", apiErr.Code, apiErr.Message)
	}
	if disposition != bkntrace.GuardExecute {
		return nil, fmt.Errorf("managed function trace is not executable: %s", disposition)
	}

	// The registration is real even if the caller cancelled during Core I/O.
	// Keep the receipt pending rather than executing or reporting a false result.
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if lifecycleContext.Err() != nil {
		return nil, lifecycleContext.Err()
	}

	// Finish the inner function even when the outer MCP panic guard would catch
	// the callback panic. Keep arbitrary panic content out of persisted errors.
	result, runErr := func() (result map[string]any, runErr error) {
		defer func() {
			if recover() != nil {
				runErr = errors.New("managed function execution panicked")
			}
		}()
		return runAndCleanTraceGaps(lifecycleContext, run)
	}()
	failed := runErr != nil || managedFunctionResultFailed(result)
	payload := any(redactFunctionPayload(result))
	if failed {
		payload = managedFunctionFailurePayload(result)
	}
	_, finishAPIErr, finishErr := m.guard.Finish(lifecycleContext, state, payload, failed, false)
	if runErr != nil {
		if finishErr != nil || finishAPIErr != nil {
			bkntrace.MarkTraceUnavailable(ctx, "function_finish", "execute_tool", "trace_finish_unconfirmed")
		}
		return nil, runErr
	}
	if finishErr != nil {
		bkntrace.MarkTraceUnavailable(ctx, "function_finish", "execute_tool", "trace_finish_unconfirmed")
		logger.DefaultLogger().WithContext(ctx).Errorf(
			"[BKN Trace] failed to finalize managed function %q: %v", input.ToolID, finishErr,
		)
		return result, nil
	}
	if finishAPIErr != nil {
		bkntrace.MarkTraceUnavailable(ctx, "function_finish", "execute_tool", finishAPIErr.Code)
		logger.DefaultLogger().WithContext(ctx).Errorf(
			"[BKN Trace] failed to finalize managed function %q: %s", input.ToolID, finishAPIErr.Code,
		)
		return result, nil
	}
	return result, nil
}

func runAndCleanTraceGaps(ctx context.Context, run func(context.Context) (map[string]any, error)) (map[string]any, error) {
	result, err := run(ctx)
	if result == nil {
		return result, err
	}
	body, ok := result["body"].(map[string]any)
	if !ok {
		return result, err
	}
	stderr, ok := body["stderr"].(string)
	if !ok {
		return result, err
	}
	cleaned, reasons := bkntrace.ExtractTraceGapMarkers(stderr)
	body["stderr"] = cleaned
	bkntrace.MergeTracePartialReasons(ctx, reasons)
	return result, err
}

func managedFunctionFailurePayload(result map[string]any) map[string]any {
	payload := map[string]any{"code": "managed_function_failed", "message": "managed function execution failed"}
	if status, ok := managedFunctionResultCode(result["status_code"]); ok {
		payload["status_code"] = status
	}
	if body, ok := result["body"].(map[string]any); ok {
		if exitCode, ok := managedFunctionResultCode(body["exit_code"]); ok {
			payload["exit_code"] = exitCode
		}
	}
	return payload
}

func managedFunctionResultFailed(result map[string]any) bool {
	if result == nil {
		return false
	}
	if status, ok := managedFunctionResultCode(result["status_code"]); ok && status >= http.StatusBadRequest {
		return true
	}
	body, ok := result["body"].(map[string]any)
	if !ok {
		return false
	}
	exitCode, ok := managedFunctionResultCode(body["exit_code"])
	return ok && exitCode != 0
}

func managedFunctionResultCode(raw any) (int, bool) {
	switch value := raw.(type) {
	case int:
		return value, true
	case int64:
		return int(value), true
	case float64:
		return int(value), true
	case string:
		status, err := strconv.Atoi(strings.TrimSpace(value))
		return status, err == nil
	}
	return 0, false
}

func managedFunctionCapabilityProfile(input ManagedFunctionTraceInput) []byte {
	profile := map[string]any{
		"manifest_id": "openbkn.context-loader.managed-function", "manifest_version": "1.0.0",
		"canonical_tool_name": input.ToolID, "tool_version": normalizedFunctionVersion(input.Descriptor.Version),
		"input_schema_digest":  managedFunctionContractDigest("managed-function-input/v1"),
		"output_schema_digest": managedFunctionContractDigest("managed-function-output/v1"),
		"execution_role":       "business_function", "evidence_contract": "managed_function_execution/v1",
		"child_evidence_policy": "managed_descendants", "minimum_trace_schema": "3.0.0",
		"required_trace_fields": []string{"receipt", "business_refs", "result_completeness"},
		"failure_policy":        "preserve_execution_and_downgrade", "resolution": "matched",
	}
	raw, _ := sonic.ConfigStd.Marshal(profile)
	return raw
}

func managedFunctionContractDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func managedFunctionOperationKey(traceContext common.TraceContext, input ManagedFunctionTraceInput) string {
	seed := strings.Join([]string{
		traceContext.OperationID, fmt.Sprintf("%d", traceContext.Attempt),
		input.KnowledgeNetworkID, input.ToolboxID, input.ToolID,
	}, "|")
	sum := sha256.Sum256([]byte(seed))
	return "function:" + hex.EncodeToString(sum[:16])
}

func normalizedFunctionVersion(value string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return "unversioned"
}

func redactFunctionPayload(value any) any {
	switch current := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(current))
		for key, item := range current {
			if isSensitiveFunctionField(key) {
				out[key] = "[redacted]"
				continue
			}
			out[key] = redactFunctionPayload(item)
		}
		return out
	case []any:
		out := make([]any, len(current))
		for index, item := range current {
			out[index] = redactFunctionPayload(item)
		}
		return out
	default:
		return value
	}
}

func isSensitiveFunctionField(value string) bool {
	normalized := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, strings.TrimSpace(value))
	for _, token := range []string{"authorization", "password", "passwd", "secret", "token", "apikey", "cookie", "credential"} {
		if normalized == token || strings.HasSuffix(normalized, token) {
			return true
		}
	}
	return false
}
