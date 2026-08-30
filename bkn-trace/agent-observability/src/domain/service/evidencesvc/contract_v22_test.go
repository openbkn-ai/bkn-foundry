// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package evidencesvc

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/evidencestore"
)

func TestIngestAcceptsTwoPointTwoArtifactLinkedCoreEvents(t *testing.T) {
	response, validationErrors, err := New(evidencestore.New()).Ingest(
		context.Background(),
		mustJSON(t, twoPointTwoBatch(t)),
	)
	if err != nil {
		t.Fatalf("unexpected ingest error: %v", err)
	}
	if len(validationErrors) != 0 {
		t.Fatalf("2.2 artifact-linked events must be accepted: %+v", validationErrors)
	}
	if response.SchemaVersion != evidencevo.ArtifactContractVersion {
		t.Fatalf("unexpected schema version: %s", response.SchemaVersion)
	}
}

func TestIngestAcceptsTwoPointTwoStructuredDataQueryFailure(t *testing.T) {
	batch := twoPointTwoBatch(t)
	events := batch["events"].([]map[string]any)
	for _, event := range events {
		if event["event_type"] != "data.query.observed" {
			continue
		}
		payload := event["payload"].(map[string]any)
		payload["status"] = "error"
		payload["error_stage"] = "vega_query"
		payload["error_code"] = "RUN_SQL_VEGA_QUERY_FAILED"
		payload["safe_error_summary"] = "unknown column available_qty"
	}

	_, validationErrors, err := New(evidencestore.New()).Ingest(context.Background(), mustJSON(t, batch))
	if err != nil {
		t.Fatalf("unexpected ingest error: %v", err)
	}
	if len(validationErrors) != 0 {
		t.Fatalf("2.2 structured data query failure must be accepted: %+v", validationErrors)
	}
}

func TestIngestAcceptsTwoPointTwoUnresolvedEmptyBusinessRefs(t *testing.T) {
	batch := twoPointTwoBatch(t)
	events := batch["events"].([]map[string]any)
	for _, event := range events {
		if event["event_type"] == "business.refs.resolved" {
			payload := event["payload"].(map[string]any)
			payload["resolver_status"] = "unresolved"
			payload["business_refs"] = []any{}
		}
	}

	_, validationErrors, err := New(evidencestore.New()).Ingest(
		context.Background(),
		mustJSON(t, batch),
	)
	if err != nil {
		t.Fatalf("unexpected ingest error: %v", err)
	}
	if len(validationErrors) != 0 {
		t.Fatalf("2.2 unresolved business refs must allow an empty array: %+v", validationErrors)
	}
}

func TestIngestKeepsTwoPointOneArtifactLinkAllowlistStrict(t *testing.T) {
	batch := twoPointOneBatch(validTwoPointOneEvents())
	events := batch["events"].([]map[string]any)
	events[0]["payload"].(map[string]any)["question_artifact_ref"] = "artifact:question_001"

	_, validationErrors, err := New(evidencestore.New()).Ingest(context.Background(), mustJSON(t, batch))
	if err != nil {
		t.Fatalf("unexpected ingest error: %v", err)
	}
	if !hasValidationCode(validationErrors, "BKN_TRACE_PAYLOAD_FIELD_UNSUPPORTED") {
		t.Fatalf("2.1 must continue rejecting 2.2-only fields: %+v", validationErrors)
	}
}

func TestIngestRejectsInvalidTwoPointTwoArtifactReference(t *testing.T) {
	batch := twoPointTwoBatch(t)
	events := batch["events"].([]map[string]any)
	events[0]["payload"].(map[string]any)["question_artifact_ref"] = "https://storage.example/question.json"

	_, validationErrors, err := New(evidencestore.New()).Ingest(context.Background(), mustJSON(t, batch))
	if err != nil {
		t.Fatalf("unexpected ingest error: %v", err)
	}
	if !hasValidationPath(validationErrors, "question_artifact_ref") {
		t.Fatalf("invalid artifact reference must be rejected at its field: %+v", validationErrors)
	}
}

func TestIngestRequiresTwoPointTwoLogicArtifactReferences(t *testing.T) {
	batch := twoPointTwoBatch(t)
	events := batch["events"].([]map[string]any)
	for _, event := range events {
		if event["event_type"] == "logic.execution.observed" {
			delete(event["payload"].(map[string]any), "input_artifact_ref")
		}
	}

	_, validationErrors, err := New(evidencestore.New()).Ingest(context.Background(), mustJSON(t, batch))
	if err != nil {
		t.Fatalf("unexpected ingest error: %v", err)
	}
	if !hasValidationPath(validationErrors, "input_artifact_ref") {
		t.Fatalf("logic input artifact reference must be required: %+v", validationErrors)
	}
}

func TestIngestTwoPointTwoRejectsLegacyActionArtifactRefField(t *testing.T) {
	batch := twoPointTwoBatch(t)
	events := batch["events"].([]map[string]any)
	for _, event := range events {
		if event["event_type"] == "action.result_recorded" {
			event["payload"].(map[string]any)["artifact_ref"] = "artifact:legacy_action_result"
		}
	}

	_, validationErrors, err := New(evidencestore.New()).Ingest(context.Background(), mustJSON(t, batch))
	if err != nil {
		t.Fatalf("unexpected ingest error: %v", err)
	}
	if !hasValidationPath(validationErrors, "artifact_ref") ||
		!hasValidationCode(validationErrors, "BKN_TRACE_PAYLOAD_FIELD_UNSUPPORTED") {
		t.Fatalf("2.2 must reject the legacy action artifact_ref field: %+v", validationErrors)
	}
}

func TestTwoPointTwoEvidenceAndBusinessGraphsExposeExactArtifactRelationships(t *testing.T) {
	store := evidencestore.New()
	service := New(store)
	if _, validationErrors, err := service.Ingest(context.Background(), mustJSON(t, twoPointTwoBatch(t))); err != nil || len(validationErrors) != 0 {
		t.Fatalf("seed 2.2 trace: errors=%+v err=%v", validationErrors, err)
	}
	scope := evidencevo.QueryScope{
		TenantID: "tenant_e2e", AccountID: "account_e2e_admin", AccountType: "user",
	}

	chain, found, err := service.GetEvidenceChainByTraceID(context.Background(), "11111111111111111111111111111111", evidencevo.EvidenceQueryOptions{Scope: scope})
	if err != nil || !found {
		t.Fatalf("get evidence chain: found=%v err=%v", found, err)
	}
	if !hasArtifactLink(chain.Data.ArtifactLinks, "evt_data", "query_artifact_ref", "artifact:query_001") ||
		!hasArtifactLink(chain.Data.ArtifactLinks, "evt_data", "result_artifact_ref", "artifact:data_result_001") ||
		!hasArtifactLink(chain.Data.ArtifactLinks, "evt_logic", "input_artifact_ref", "artifact:data_result_001") ||
		!hasArtifactLink(chain.Data.ArtifactLinks, "evt_logic", "result_artifact_ref", "artifact:logic_result_001") ||
		!hasArtifactLink(chain.Data.ArtifactLinks, "evt_action_recommended", "reason_artifact_ref", "artifact:action_reason_001") ||
		!hasArtifactLink(chain.Data.ArtifactLinks, "evt_action_recommended", "input_artifact_ref", "artifact:action_input_001") ||
		!hasArtifactLink(chain.Data.ArtifactLinks, "evt_action_result", "result_artifact_ref", "artifact:action_result_001") {
		t.Fatalf("evidence chain omitted exact artifact relationships: %+v", chain.Data.ArtifactLinks)
	}

	graph, found, err := service.GetBusinessGraphByTraceID(context.Background(), "11111111111111111111111111111111", evidencevo.EvidenceQueryOptions{Scope: scope})
	if err != nil || !found {
		t.Fatalf("get business graph: found=%v err=%v", found, err)
	}
	if !hasGraphNode(graph.Data.Nodes, "artifact:query_001", "artifact") {
		t.Fatalf("business graph omitted query artifact node: %+v", graph.Data.Nodes)
	}
	if !hasGraphEdge(graph.Data.Edges, "artifact:query_001", "event:evt_data", "provides_input_to") {
		t.Fatalf("business graph omitted query input edge: %+v", graph.Data.Edges)
	}
	if !hasGraphEdge(graph.Data.Edges, "event:evt_logic", "artifact:logic_result_001", "produces") {
		t.Fatalf("business graph omitted logic result edge: %+v", graph.Data.Edges)
	}
}

func hasArtifactLink(links []evidencevo.ArtifactLink, eventID, role, artifactRef string) bool {
	for _, link := range links {
		if link.EventID == eventID && link.Role == role && link.ArtifactRef == artifactRef {
			return true
		}
	}
	return false
}

func hasGraphNode(nodes []evidencevo.BusinessGraphNode, nodeID, nodeType string) bool {
	for _, node := range nodes {
		if node.ID == nodeID && node.NodeType == nodeType {
			return true
		}
	}
	return false
}

func hasGraphEdge(edges []evidencevo.BusinessGraphEdge, sourceID, targetID, edgeType string) bool {
	for _, edge := range edges {
		if edge.SourceID == sourceID && edge.TargetID == targetID && edge.EdgeType == edgeType {
			return true
		}
	}
	return false
}

func twoPointTwoBatch(t *testing.T) map[string]any {
	t.Helper()
	body, err := json.Marshal(validTwoPointOneEvents())
	if err != nil {
		t.Fatalf("clone 2.1 events: %v", err)
	}
	var events []map[string]any
	if err := json.Unmarshal(body, &events); err != nil {
		t.Fatalf("decode cloned events: %v", err)
	}
	for _, event := range events {
		event["bkn.trace.schema.version"] = evidencevo.ArtifactContractVersion
		payload := event["payload"].(map[string]any)
		switch event["event_type"] {
		case "agent.interaction.started":
			payload["question_artifact_ref"] = "artifact:question_001"
		case "data.query.observed":
			payload["query_artifact_ref"] = "artifact:query_001"
			payload["result_artifact_ref"] = "artifact:data_result_001"
		case "claim.created":
			payload["result_artifact_ref"] = "artifact:result_001"
		case "action.recommended":
			payload["reason_artifact_ref"] = "artifact:action_reason_001"
			payload["input_artifact_ref"] = "artifact:action_input_001"
		case "action.result_recorded":
			delete(payload, "task_ref")
			payload["result_artifact_ref"] = "artifact:action_result_001"
		}
	}
	logic := map[string]any{
		"event_id": "evt_logic", "event_type": "logic.execution.observed",
		"bkn.trace.schema.version": evidencevo.ArtifactContractVersion,
		"observed_at":              "2026-07-25T08:00:00.000000000Z",
		"emitted_at":               "2026-07-25T08:00:00.001000000Z",
		"producer_module":          "bkn-logic",
		"trace_id":                 "11111111111111111111111111111111",
		"span_id":                  "1000000000000002",
		"bkn.request.id":           "req_biz_001",
		"bkn.operation.name":       "logic.execute",
		"interaction_id":           "int_001",
		"operation_id":             "op_logic_001",
		"causation_event_id":       "evt_data",
		"attempt":                  1,
		"payload": map[string]any{
			"logic_ref":           "logic:supplychain:forecast",
			"input_artifact_ref":  "artifact:data_result_001",
			"result_artifact_ref": "artifact:logic_result_001",
			"status":              "success",
		},
	}
	events = append(events[:2], append([]map[string]any{logic}, events[2:]...)...)
	for _, event := range events {
		if event["event_type"] == "claim.created" {
			event["causation_event_id"] = "evt_logic"
			payload := event["payload"].(map[string]any)
			payload["source_event_ids"] = []any{"evt_data", "evt_logic", "evt_model"}
			payload["operation_ids"] = []any{"op_data_001", "op_logic_001", "op_model_001"}
		}
	}
	batch := twoPointOneBatch(events)
	batch["bkn.trace.schema.version"] = evidencevo.ArtifactContractVersion
	return batch
}
