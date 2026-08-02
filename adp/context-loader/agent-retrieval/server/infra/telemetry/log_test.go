package telemetry

import (
	"context"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

func TestOperationLogAttributesCarryTrustedBusinessOperationScope(t *testing.T) {
	ctx := common.SetTraceContextToCtx(context.Background(), common.TraceContext{
		TenantID:       "tenant-local",
		BusinessDomain: "bd_public",
		RequestID:      "req_11111111-1111-4111-8111-111111111111",
		ConversationID: "conv-1",
		InteractionID:  "int-1",
		OperationID:    "op-1",
		ToolName:       "run_sql",
	})
	ctx = common.SetAccountAuthContextToCtx(ctx, &interfaces.AccountAuthContext{
		AccountID:   "user-1",
		AccountType: interfaces.AccessorTypeUser,
		TokenInfo:   &interfaces.TokenInfo{ClientID: "third-party-agent"},
	})

	values := map[string]string{}
	for _, attr := range operationLogAttributes(ctx, false, "source-log-1") {
		values[attr.Key] = attr.Value.String()
	}
	want := map[string]string{
		"log_id":               "context-loader:source-log-1",
		"source_id":            "context-loader",
		"source_log_id":        "source-log-1",
		"tenant_id":            "tenant-local",
		"business_domain_id":   "bd_public",
		"effective_subject_id": "user-1",
		"application_id":       "third-party-agent",
		"request_id":           "req_11111111-1111-4111-8111-111111111111",
		"conversation_id":      "conv-1",
		"interaction_id":       "int-1",
		"operation_id":         "op-1",
		"tool_name":            "run_sql",
		"trust_level":          "trusted",
		"log_category":         "runtime.business",
		"event_name":           "operation.completed",
		"outcome":              "success",
	}
	for key, expected := range want {
		if values[key] != expected {
			t.Fatalf("%s=%q, want %q (all=%v)", key, values[key], expected, values)
		}
	}
}

func TestContextLogAttributesDoNotPretendInternalLinesAreCompletedOperations(t *testing.T) {
	ctx := common.SetTraceContextToCtx(context.Background(), common.TraceContext{
		TenantID: "tenant-local", OperationID: "op-1",
	})

	values := map[string]string{}
	for _, attr := range contextLogAttributes(ctx) {
		values[attr.Key] = attr.Value.String()
	}
	for _, forbidden := range []string{"log_id", "source_log_id", "trust_level", "log_category", "event_name", "outcome"} {
		if values[forbidden] != "" {
			t.Fatalf("internal log unexpectedly carries governed %s=%q", forbidden, values[forbidden])
		}
	}
}

func TestInternalLogMessageOmitsGovernedBusinessContent(t *testing.T) {
	raw := "[KnSearchLocal] query=6月份有哪些需求预测单？"
	ctx := common.SetTraceContextToCtx(context.Background(), common.TraceContext{OperationID: "op-1"})

	if got := internalLogMessage(ctx, raw); got != "OpenBKN operation internal detail omitted" {
		t.Fatalf("governed internal message=%q", got)
	}
	if got := internalLogMessage(context.Background(), raw); got != raw {
		t.Fatalf("ungoverned internal message=%q, want original", got)
	}
}
