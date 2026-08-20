// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mcp

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"testing"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
)

func TestDefaultAgentLifecycleSurfaceIsTwoTools(t *testing.T) {
	got := make([]string, 0, len(lifecycleToolNames))
	for name := range lifecycleToolNames {
		got = append(got, name)
	}
	sort.Strings(got)
	want := []string{
		"bkn_finish_interaction",
		"bkn_start_interaction",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("default lifecycle tools = %v, want %v", got, want)
	}
}

func TestStartInteractionSchemaRequiresQuestionAndAgentName(t *testing.T) {
	input, _ := loadToolSchemas("bkn_start_interaction")
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err := json.Unmarshal(input, &schema); err != nil {
		t.Fatal(err)
	}
	if !sameStringSet(schema.Required, []string{"question", "agent_name"}) {
		t.Fatalf("start required fields = %v", schema.Required)
	}
	want := []string{"agent_name", "conversation_id", "question"}
	got := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		got = append(got, name)
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("start properties = %v, want %v", got, want)
	}
}

func TestBusinessToolContextRequiresLifecycleIDsAndKeepsEvidenceHintsOptional(t *testing.T) {
	input, _ := loadToolSchemas("search_schema")
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(input, &schema); err != nil {
		t.Fatal(err)
	}
	var contextSchema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err := json.Unmarshal(schema.Properties["bkn_context"], &contextSchema); err != nil {
		t.Fatal(err)
	}
	if !sameStringSet(contextSchema.Required, []string{"conversation_id", "interaction_id"}) {
		t.Fatalf("required bkn_context fields = %v", contextSchema.Required)
	}
	want := []string{
		"business_refs", "causation_event_ids", "conversation_id",
		"interaction_id", "parent_operation_id",
	}
	got := make([]string, 0, len(contextSchema.Properties))
	for name := range contextSchema.Properties {
		got = append(got, name)
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("bkn_context properties = %v, want %v", got, want)
	}
}

func TestFinishInteractionSchemaHidesCoreConcurrencyAndClosureFields(t *testing.T) {
	input, _ := loadToolSchemas("bkn_finish_interaction")
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err := json.Unmarshal(input, &schema); err != nil {
		t.Fatal(err)
	}
	if !sameStringSet(schema.Required, []string{"interaction_id", "outcome"}) {
		t.Fatalf("finish required fields = %v", schema.Required)
	}
	for _, field := range []string{
		"claims",
		"idempotency_key",
		"lease_token", "lease_epoch", "completion_manifest_version",
		"expected_operations", "expected_receipts", "assembler_deadline",
	} {
		if _, ok := schema.Properties[field]; ok {
			t.Fatalf("finish schema leaks internal field %q", field)
		}
	}
}

func TestAgentLifecycleResultReturnsAuthoritativeIDsInTextAndStructuredContent(t *testing.T) {
	view := agentLifecycleView("bkn_start_interaction", &bkntrace.Interaction{
		InteractionID:   "int-real-1",
		ConversationID:  "conv-real-1",
		ExecutionStatus: "active",
		LeaseToken:      "lease-internal",
	})
	result, err := lifecycleSuccessResult(view)
	if err != nil {
		t.Fatal(err)
	}

	structured, ok := result.StructuredContent.(map[string]any)
	if !ok || structured["conversation_id"] != "conv-real-1" ||
		structured["interaction_id"] != "int-real-1" {
		t.Fatalf("structured result omitted authoritative IDs: %#v", result.StructuredContent)
	}
	if _, leaked := structured["lease_token"]; leaked {
		t.Fatalf("structured result leaked internal lease: %#v", structured)
	}

	textContent, ok := mcpsdk.AsTextContent(result.Content[0])
	if !ok {
		t.Fatalf("fallback result is not text: %#v", result.Content)
	}
	var fallback map[string]any
	if err := json.Unmarshal([]byte(textContent.Text), &fallback); err != nil {
		t.Fatalf("fallback result is not JSON: %q: %v", textContent.Text, err)
	}
	if fallback["conversation_id"] != "conv-real-1" ||
		fallback["interaction_id"] != "int-real-1" {
		t.Fatalf("fallback result omitted authoritative IDs: %#v", fallback)
	}
}

func TestManagedFinishIdempotencyIsStableAcrossTransportRetries(t *testing.T) {
	first := map[string]any{
		"interaction_id": "int-1", "outcome": "completed", "answer": "库存充足",
	}
	second := map[string]any{
		"interaction_id": "int-1", "outcome": "completed", "answer": "库存充足",
	}
	changed := map[string]any{
		"interaction_id": "int-1", "outcome": "completed", "answer": "库存不足",
	}
	firstContext := common.SetTraceContextToCtx(context.Background(), common.TraceContext{RequestID: "req-finish-1"})
	secondContext := common.SetTraceContextToCtx(context.Background(), common.TraceContext{RequestID: "req-finish-2"})

	ensureLifecycleIdempotency(firstContext, "bkn_finish_interaction", first, hostLifecycleHints{})
	ensureLifecycleIdempotency(secondContext, "bkn_finish_interaction", second, hostLifecycleHints{})
	ensureLifecycleIdempotency(secondContext, "bkn_finish_interaction", changed, hostLifecycleHints{})

	if first["idempotency_key"] != second["idempotency_key"] {
		t.Fatalf("transport retry changed finish idempotency: %q != %q", first["idempotency_key"], second["idempotency_key"])
	}
	if first["idempotency_key"] == changed["idempotency_key"] {
		t.Fatalf("different finish payload reused idempotency key: %q", first["idempotency_key"])
	}
}

func TestLifecycleErrorReturnsRecoveryGuidanceWithoutSuccessStructuredContent(t *testing.T) {
	result := lifecycleToolError(lifecycleError{
		Code: "interaction_in_progress", Message: "an interaction is already active",
		CurrentStatus: "active", CurrentInteractionID: "int-active-1",
		RequiredAction: "bkn_finish_interaction",
	})
	if result.StructuredContent != nil {
		t.Fatalf("error result must not be validated against the success output schema: %#v", result.StructuredContent)
	}
	textContent, ok := mcpsdk.AsTextContent(result.Content[0])
	if !ok {
		t.Fatalf("error fallback is not text: %#v", result.Content)
	}
	var fallback map[string]any
	if err := json.Unmarshal([]byte(textContent.Text), &fallback); err != nil {
		t.Fatalf("error fallback is not JSON: %q: %v", textContent.Text, err)
	}
	textError := fallback["error"].(map[string]any)
	if textError["current_interaction_id"] != "int-active-1" ||
		textError["required_action"] != "bkn_finish_interaction" {
		t.Fatalf("text error omitted recovery guidance: %#v", textError)
	}
}
