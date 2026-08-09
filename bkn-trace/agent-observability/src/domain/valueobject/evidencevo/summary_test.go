package evidencevo

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildExecutionSummariesJoinsMultipleTracesAndArtifactsByRequest(t *testing.T) {
	traces := []NormalizedTrace{
		summaryTrace("trace_summary_a", "req_summary", "2026-07-26T08:00:00Z", "2026-07-26T08:00:02Z"),
		summaryTrace("trace_summary_b", "req_summary", "2026-07-26T08:00:03Z", "2026-07-26T08:00:05Z"),
	}
	traces[0].SchemaVersion = ArtifactContractVersion
	traces[0].Events[0].SchemaVersion = ArtifactContractVersion
	traces[0].Events[0].EventType = "agent.interaction.started"
	traces[0].Events[0].Payload["question_artifact_ref"] = "artifact:artifact_question"
	traces[0].Events = append(traces[0].Events, EvidenceEvent{
		EventID: "event_trace_summary_a_completed", EventType: "action.result_recorded",
		SchemaVersion: ArtifactContractVersion,
		ObservedAt:    "2026-07-26T08:00:02Z", EmittedAt: "2026-07-26T08:00:02Z",
		TraceID: "trace_summary_a", RequestID: "req_summary", OperationName: "action.complete",
		Payload: map[string]any{"status": "completed"},
	})
	traces[1].SchemaVersion = ArtifactContractVersion
	traces[1].Events[0].SchemaVersion = ArtifactContractVersion
	traces[1].Events[0].Payload["result_artifact_ref"] = "artifact:artifact_result"
	artifacts := []EvidenceArtifact{
		summaryArtifact(t, "artifact_question", ArtifactTypeQuestion, "trace_summary_a", map[string]any{"text": "7 月有多少张预测单？"}),
		summaryArtifact(t, "artifact_result", ArtifactTypeResult, "trace_summary_b", map[string]any{"text": "0 张；当前数据不包含 2024 年记录。"}),
	}

	requests, traceExecutions := BuildExecutionSummaries(traces, artifacts)

	if len(requests) != 1 {
		t.Fatalf("expected one request projection, got %+v", requests)
	}
	request := requests[0]
	if request.RequestID != "req_summary" || request.TraceCount != 2 {
		t.Fatalf("request must join both traces: %+v", request)
	}
	if request.QuestionPreview != "7 月有多少张预测单？" || request.ResultPreview != "0 张；当前数据不包含 2024 年记录。" {
		t.Fatalf("expected business previews from authorized artifacts: %+v", request)
	}
	if request.EvidenceCompleteness != "complete" || len(request.BusinessRefs) != 1 || request.BusinessRefs[0] != "object:kn_supply:forecast" {
		t.Fatalf("unexpected evidence projection: %+v", request)
	}
	if request.DurationMS != 5000 || request.StartedAt != "2026-07-26T08:00:00Z" || request.CompletedAt != "2026-07-26T08:00:05Z" {
		t.Fatalf("unexpected request timing: %+v", request)
	}
	if len(traceExecutions) != 2 {
		t.Fatalf("expected two trace projections, got %+v", traceExecutions)
	}
	for _, trace := range traceExecutions {
		if trace.RequestID != request.RequestID || trace.TraceID == "" {
			t.Fatalf("trace must reverse-link request: %+v", trace)
		}
	}
}

func TestBuildExecutionSummariesExposesConversationAndInteractionIdentity(t *testing.T) {
	trace := summaryTrace("trace_identity", "req_identity", "2026-07-27T08:00:00Z", "")
	trace.ConversationID = "conversation_supply_chain"
	trace.Events[0].InteractionID = "interaction_june_forecast"

	requests, executions := BuildExecutionSummaries([]NormalizedTrace{trace}, nil)

	if len(requests) != 1 || len(executions) != 1 {
		t.Fatalf("unexpected summary counts: requests=%d traces=%d", len(requests), len(executions))
	}
	if requests[0].ConversationID != "conversation_supply_chain" ||
		requests[0].InteractionID != "interaction_june_forecast" {
		t.Fatalf("request identity missing: %+v", requests[0])
	}
	if executions[0].ConversationID != "conversation_supply_chain" ||
		executions[0].InteractionID != "interaction_june_forecast" {
		t.Fatalf("trace identity missing: %+v", executions[0])
	}
}

func TestBuildExecutionSummariesUsesAuthenticatedApplicationInsteadOfDownstreamProducer(t *testing.T) {
	trace := summaryTrace("trace_application", "req_application", "2026-08-03T08:00:00Z", "2026-08-03T08:00:01Z")
	trace.ApplicationPrincipalID = "openbkn-sdk"
	trace.Events[0].EventType = "retrieval.completed"
	trace.Events[0].OperationName = "context.run_sql"
	trace.Events[0].Payload = map[string]any{"candidate_count": 1}
	artifact := summaryArtifact(
		t,
		"artifact_downstream",
		ArtifactTypeDataResult,
		trace.TraceID,
		map[string]any{"entries": []any{map[string]any{"id": "row-1"}}},
	)
	artifact.RequestID = trace.RequestID
	artifact.AgentOrApp = "vega-data"

	requests, executions := BuildExecutionSummaries([]NormalizedTrace{trace}, []EvidenceArtifact{artifact})

	if requests[0].AgentOrApp != "openbkn-sdk" || executions[0].AgentOrApp != "openbkn-sdk" {
		t.Fatalf("authenticated caller application must win over downstream producer: request=%+v trace=%+v", requests[0], executions[0])
	}
}

func TestBuildExecutionSummariesKeepsTopLevelOperationWhenChildEvidenceUsesAnotherOperation(t *testing.T) {
	trace := summaryTrace("trace_nested_operation", "req_nested_operation", "2026-08-03T08:00:00Z", "2026-08-03T08:00:02Z")
	trace.Events[0].EventType = "retrieval.completed"
	trace.Events[0].OperationID = "op_context_run_sql"
	trace.Events[0].OperationName = "context.run_sql"
	trace.Events = append(trace.Events, EvidenceEvent{
		EventID: "event_vega_data_query", EventType: "data.query.observed",
		ObservedAt: "2026-08-03T08:00:01Z", EmittedAt: "2026-08-03T08:00:01Z",
		TraceID: trace.TraceID, RequestID: trace.RequestID,
		OperationID: "op_vega_child", OperationName: "data.raw_query",
		Payload: map[string]any{"query_hash": "sha256:query"},
	})

	requests, _ := BuildExecutionSummaries([]NormalizedTrace{trace}, nil)

	if len(requests) != 1 || requests[0].OperationID != "op_context_run_sql" || requests[0].ToolName != "context.run_sql" {
		t.Fatalf("request summary must preserve the top-level OpenBKN operation: %+v", requests)
	}
}

func TestBuildExecutionSummariesMarksTwoPointOneWithoutArtifactsContentUnavailable(t *testing.T) {
	trace := summaryTrace("trace_summary_legacy", "req_summary_legacy", "2026-07-26T08:00:00Z", "2026-07-26T08:00:01Z")

	requests, _ := BuildExecutionSummaries([]NormalizedTrace{trace}, nil)

	if len(requests) != 1 {
		t.Fatalf("expected request projection, got %+v", requests)
	}
	request := requests[0]
	if request.QuestionPreview != "" || request.ResultPreview != "" || request.EvidenceCompleteness != "content_unavailable" {
		t.Fatalf("2.1 evidence without artifacts must be readable and honest: %+v", request)
	}
	if !containsSummaryReason(request.PartialReasons, "content_unavailable") {
		t.Fatalf("expected explicit content_unavailable reason: %+v", request.PartialReasons)
	}
}

func TestBuildExecutionSummariesDoesNotInventUnavailableFields(t *testing.T) {
	trace := summaryTrace("trace_summary_sparse", "req_summary_sparse", "", "")
	trace.Events[0].Producer = ""
	trace.BusinessDomain = ""
	trace.Events[0].ObservedAt = ""
	trace.Events[0].EmittedAt = ""

	requests, executions := BuildExecutionSummaries([]NormalizedTrace{trace}, nil)

	if requests[0].StartedAt != "" || requests[0].CompletedAt != "" || requests[0].AgentOrApp != "" ||
		requests[0].DurationMS != 0 || requests[0].BusinessDomain != "" {
		t.Fatalf("sparse source fields must remain empty: %+v", requests[0])
	}
	if executions[0].StartedAt != "" || executions[0].CompletedAt != "" || executions[0].DurationMS != 0 {
		t.Fatalf("sparse trace fields must remain empty: %+v", executions[0])
	}
}

func TestBuildExecutionSummariesDoesNotInferAgentRootOrCompletionFromProducerOperationAndEmission(t *testing.T) {
	trace := NormalizedTrace{
		TraceID: "trace_running", RequestID: "req_running",
		TenantID: "tenant_demo", BusinessDomain: "bd_demo", AccountID: "acct_demo", AccountType: "app",
		SchemaVersion: ArtifactContractVersion,
		Events: []EvidenceEvent{{
			EventID: "event_running", EventType: "data.query.observed",
			ObservedAt: "2026-07-26T08:00:00Z", EmittedAt: "2026-07-26T08:00:01Z",
			Producer: "vega-backend", TraceID: "trace_running", SpanID: "span_running",
			RequestID: "req_running", OperationName: "data.query", OperationID: "op_query",
			Payload: map[string]any{
				"query_artifact_ref":  "artifact:query_running",
				"result_artifact_ref": "artifact:data_running",
			},
		}},
	}

	requests, executions := BuildExecutionSummaries([]NormalizedTrace{trace}, nil)

	if requests[0].AgentOrApp != "" || executions[0].AgentOrApp != "" {
		t.Fatalf("producer_module must not be projected as agent_or_app: request=%+v trace=%+v", requests[0], executions[0])
	}
	if executions[0].RootOperation != "" {
		t.Fatalf("ordinary operation must not be projected as root_operation: %+v", executions[0])
	}
	if requests[0].CompletedAt != "" || executions[0].CompletedAt != "" ||
		requests[0].DurationMS != 0 || executions[0].DurationMS != 0 {
		t.Fatalf("running execution must not get a synthetic completion: request=%+v trace=%+v", requests[0], executions[0])
	}
	if requests[0].Status != "running" || executions[0].Status != "running" {
		t.Fatalf("non-terminal observed events must remain running: request=%+v trace=%+v", requests[0], executions[0])
	}
}

func TestBuildExecutionSummariesCompletesFinishedRetrievalAndDataQueryRequests(t *testing.T) {
	retrieval := NormalizedTrace{
		TraceID: "trace_retrieval_completed", RequestID: "req_retrieval_completed",
		SchemaVersion: ArtifactContractVersion,
		Events: []EvidenceEvent{{
			EventID: "event_retrieval_completed", EventType: "retrieval.completed",
			ObservedAt: "2026-07-27T09:00:00Z", EmittedAt: "2026-07-27T09:00:01Z",
			TraceID: "trace_retrieval_completed", RequestID: "req_retrieval_completed",
			OperationName: "context.search_schema",
			Payload:       map[string]any{"candidate_count": 1, "truncated": false},
		}, {
			EventID: "event_retrieval_completed_duplicate", EventType: "retrieval.completed",
			ObservedAt: "2026-07-27T09:00:01Z", EmittedAt: "2026-07-27T09:00:01Z",
			TraceID: "trace_retrieval_completed", RequestID: "req_retrieval_completed",
			OperationName: "context.search_schema",
			Payload:       map[string]any{"truncated": false},
		}},
	}
	data := NormalizedTrace{
		TraceID: "trace_data_completed", RequestID: "req_data_completed",
		SchemaVersion: ArtifactContractVersion,
		Events: []EvidenceEvent{{
			EventID: "event_data_completed", EventType: "data.query.observed",
			ObservedAt: "2026-07-27T09:00:02Z", EmittedAt: "2026-07-27T09:00:03Z",
			TraceID: "trace_data_completed", RequestID: "req_data_completed",
			OperationName: "data.query",
			Payload: map[string]any{
				"result_artifact_ref": "artifact:data_query_result",
			},
		}},
	}
	result := summaryArtifact(
		t,
		"data_query_result",
		ArtifactTypeDataResult,
		data.TraceID,
		map[string]any{"entries": []any{}},
	)
	result.RequestID = data.RequestID
	result.ObservedAt = "2026-07-27T09:00:03Z"

	requests, traces := BuildExecutionSummaries(
		[]NormalizedTrace{retrieval, data},
		[]EvidenceArtifact{result},
	)
	var retrievalSummary *RequestSummary
	for index := range requests {
		if requests[index].RequestID == retrieval.RequestID {
			retrievalSummary = &requests[index]
			break
		}
	}
	if retrievalSummary == nil || retrievalSummary.ResultCount == nil || *retrievalSummary.ResultCount != 1 {
		t.Fatalf("retrieval result count must be projected and retained: %+v", retrievalSummary)
	}

	for _, request := range requests {
		if request.Status != "completed" || request.CompletedAt == "" {
			t.Fatalf("finished operation request must be terminal: %+v", request)
		}
	}
	for _, trace := range traces {
		if trace.Status != "completed" || trace.CompletedAt == "" {
			t.Fatalf("finished operation trace must be terminal: %+v", trace)
		}
	}
}

func TestBuildExecutionSummariesUsesOnlyArtifactsExplicitlyReferencedByEvents(t *testing.T) {
	trace := NormalizedTrace{
		TraceID: "trace_linked", RequestID: "req_summary",
		TenantID: "tenant_demo", BusinessDomain: "bd_demo", AccountID: "acct_demo", AccountType: "app",
		SchemaVersion: ArtifactContractVersion,
		Events: []EvidenceEvent{
			{
				EventID: "event_question", EventType: "agent.interaction.started",
				ObservedAt: "2026-07-26T08:00:00Z", EmittedAt: "2026-07-26T08:00:00Z",
				TraceID: "trace_linked", RequestID: "req_summary", OperationName: "agent.run",
				Payload: map[string]any{"agent_id": "agent-explicit", "question_artifact_ref": "artifact:linked_question"},
			},
			{
				EventID: "event_result", EventType: "claim.created",
				ObservedAt: "2026-07-26T08:00:02Z", EmittedAt: "2026-07-26T08:00:02Z",
				TraceID: "trace_linked", RequestID: "req_summary", OperationName: "claim.create",
				Payload: map[string]any{"claim_id": "claim_linked", "result_artifact_ref": "artifact:linked_result"},
			},
		},
	}
	question := summaryArtifact(t, "linked_question", ArtifactTypeQuestion, trace.TraceID, map[string]any{"text": "被引用的问题"})
	result := summaryArtifact(t, "linked_result", ArtifactTypeResult, trace.TraceID, map[string]any{"text": "被引用的结果"})
	unreferenced := summaryArtifact(t, "unreferenced_result", ArtifactTypeResult, trace.TraceID, map[string]any{"text": "同 request 但未被引用"})
	unreferenced.BusinessRefs = []string{"object:other:secret"}

	requests, executions := BuildExecutionSummaries([]NormalizedTrace{trace}, []EvidenceArtifact{unreferenced, result, question})

	if requests[0].QuestionPreview != "被引用的问题" || requests[0].ResultPreview != "被引用的结果" {
		t.Fatalf("summary must follow event artifact refs exactly: %+v", requests[0])
	}
	if strings.Contains(strings.Join(requests[0].BusinessRefs, ","), "object:other:secret") {
		t.Fatalf("unreferenced artifact must not be mixed into request projection: %+v", requests[0])
	}
	if requests[0].AgentOrApp != "agent-explicit" || executions[0].RootOperation != "agent.run" {
		t.Fatalf("explicit agent interaction must provide agent and root operation: request=%+v trace=%+v", requests[0], executions[0])
	}
	if requests[0].CompletedAt != "2026-07-26T08:00:02Z" || executions[0].CompletedAt != "2026-07-26T08:00:02Z" {
		t.Fatalf("explicit result event must provide completion: request=%+v trace=%+v", requests[0], executions[0])
	}
}

func TestBuildExecutionSummariesKeepsOperationTimingSeparateFromInteractionArtifacts(t *testing.T) {
	trace := NormalizedTrace{
		TraceID: "trace_operation_timing", RequestID: "req_operation_timing",
		TenantID: "tenant_demo", BusinessDomain: "bd_demo", AccountID: "acct_demo", AccountType: "app",
		SchemaVersion: ArtifactContractVersion,
		Events: []EvidenceEvent{
			{
				EventID: "event_question_link", EventType: "agent.interaction.started",
				ObservedAt: "2026-08-02T09:00:01Z", EmittedAt: "2026-08-02T09:00:01Z",
				TraceID: "trace_operation_timing", RequestID: "req_operation_timing",
				Payload: map[string]any{"question_artifact_ref": "artifact:question_operation_timing"},
			},
			{
				EventID: "event_result_link", EventType: "claim.created",
				ObservedAt: "2026-08-02T09:00:02Z", EmittedAt: "2026-08-02T09:00:02Z",
				TraceID: "trace_operation_timing", RequestID: "req_operation_timing",
				Payload: map[string]any{
					"claim_id":            "claim_operation_timing",
					"result_artifact_ref": "artifact:result_operation_timing",
					"business_refs":       []any{map[string]any{"ref_id": "object:kn_demo:forecast"}},
				},
			},
		},
	}
	question := summaryArtifact(t, "question_operation_timing", ArtifactTypeQuestion, trace.TraceID, map[string]any{"text": "六月预测是多少？"})
	question.RequestID = trace.RequestID
	question.ObservedAt = "2026-08-02T09:00:00Z"
	result := summaryArtifact(t, "result_operation_timing", ArtifactTypeResult, trace.TraceID, map[string]any{"text": "合计 11594"})
	result.RequestID = trace.RequestID
	result.ObservedAt = "2026-08-02T09:00:03Z"

	requests, _ := BuildExecutionSummaries([]NormalizedTrace{trace}, []EvidenceArtifact{question, result})

	if len(requests) != 1 || requests[0].StartedAt != "2026-08-02T09:00:01Z" ||
		requests[0].CompletedAt != "2026-08-02T09:00:02Z" || requests[0].DurationMS != 1000 {
		t.Fatalf("operation timing must come from execution events, not interaction artifacts: %+v", requests)
	}
}

func TestSummaryCollectionEnvelopeUsesEntriesAndEmptyArray(t *testing.T) {
	body, err := json.Marshal(RequestSummaryPage{Entries: []RequestSummary{}, Total: 0})
	if err != nil {
		t.Fatalf("marshal request page: %v", err)
	}
	encoded := string(body)
	if !strings.Contains(encoded, `"entries":[]`) || !strings.Contains(encoded, `"total":0`) || strings.Contains(encoded, `"items"`) {
		t.Fatalf("collection envelope must use entries and preserve empty array: %s", encoded)
	}
}

func TestBuildExecutionSummariesDoesNotCallQuestionAndResultAloneCompleteEvidence(t *testing.T) {
	trace := summaryTrace("trace_summary_unsupported", "req_summary", "2026-07-26T08:00:00Z", "2026-07-26T08:00:01Z")
	trace.SchemaVersion = ArtifactContractVersion
	trace.Events[0].SchemaVersion = ArtifactContractVersion
	trace.Events[0].Payload = map[string]any{"claim_id": "claim_trace_summary_unsupported", "visibility": "visible"}
	trace.Events[0].Payload["result_artifact_ref"] = "artifact:artifact_result_only"
	trace.Events = append([]EvidenceEvent{{
		EventID: "event_question_only", EventType: "agent.interaction.started",
		SchemaVersion: ArtifactContractVersion,
		ObservedAt:    "2026-07-26T08:00:00Z", EmittedAt: "2026-07-26T08:00:00Z",
		TraceID: trace.TraceID, RequestID: trace.RequestID, OperationName: "agent.run",
		Payload: map[string]any{
			"agent_id": "agent-explicit", "question_artifact_ref": "artifact:artifact_question_only",
		},
	}}, trace.Events...)
	question := summaryArtifact(t, "artifact_question_only", ArtifactTypeQuestion, trace.TraceID, map[string]any{"text": "问题"})
	result := summaryArtifact(t, "artifact_result_only", ArtifactTypeResult, trace.TraceID, map[string]any{"text": "结果"})
	question.BusinessRefs = nil
	result.BusinessRefs = nil

	requests, _ := BuildExecutionSummaries([]NormalizedTrace{trace}, []EvidenceArtifact{question, result})

	if requests[0].EvidenceCompleteness != "partial" ||
		!containsSummaryReason(requests[0].PartialReasons, "supporting_evidence_unavailable") {
		t.Fatalf("question and result without supporting evidence must remain partial: %+v", requests[0])
	}
}

func TestBuildExecutionSummariesKeepsRecoveredToolFailureOnOperation(t *testing.T) {
	trace := summaryTrace(
		"trace_recovered_tool_failure",
		"req_summary",
		"2026-07-26T08:00:00Z",
		"2026-07-26T08:00:03Z",
	)
	trace.SchemaVersion = ArtifactContractVersion
	trace.Events[0].SchemaVersion = ArtifactContractVersion
	trace.Events[0].Payload["result_artifact_ref"] = "artifact:artifact_recovered_result"
	trace.Events = append([]EvidenceEvent{{
		EventID: "event_recoverable_tool_failure", EventType: "tool.result.observed",
		SchemaVersion: ArtifactContractVersion,
		ObservedAt:    "2026-07-26T08:00:01Z", EmittedAt: "2026-07-26T08:00:01Z",
		TraceID: trace.TraceID, RequestID: trace.RequestID, OperationName: "agent.tool.call",
		Payload: map[string]any{
			"status": "failed", "error_code": "RELATION_QUERY_INVALID",
		},
	}}, trace.Events...)
	result := summaryArtifact(
		t,
		"artifact_recovered_result",
		ArtifactTypeResult,
		trace.TraceID,
		map[string]any{"text": "Agent 使用其他数据完成了业务分析"},
	)

	requests, executions := BuildExecutionSummaries(
		[]NormalizedTrace{trace},
		[]EvidenceArtifact{result},
	)

	if requests[0].Status != "error" || requests[0].ResultPreview == "" {
		t.Fatalf("an operation result must not hide the operation failure: %+v", requests[0])
	}
	if executions[0].Status != "error" || executions[0].ErrorSummary != "RELATION_QUERY_INVALID" {
		t.Fatalf("the recovered technical failure must remain visible on the trace: %+v", executions[0])
	}
}

func TestBuildExecutionSummariesDoesNotCopyInteractionResultIntoOperation(t *testing.T) {
	trace := NormalizedTrace{
		TraceID: "trace_failed_operation", RequestID: "req_failed_operation",
		TenantID: "tenant_demo", BusinessDomain: "bd_demo", AccountID: "acct_demo", AccountType: "app",
		SchemaVersion:  ArtifactContractVersion,
		ConversationID: "conv_supply_chain",
		Events: []EvidenceEvent{
			{
				EventID: "event_operation_failed", EventType: "tool.result.observed",
				ObservedAt: "2026-08-04T08:00:00Z", EmittedAt: "2026-08-04T08:00:01Z",
				TraceID: "trace_failed_operation", RequestID: "req_failed_operation",
				InteractionID: "int_supply_chain", OperationID: "op_run_sql", OperationName: "run_sql",
				Payload: map[string]any{"status": "failed", "error_code": "READ_POLICY_REJECTED"},
			},
			{
				EventID: "event_interaction_result_link", EventType: "claim.created",
				ObservedAt: "2026-08-04T08:00:02Z", EmittedAt: "2026-08-04T08:00:02Z",
				TraceID: "trace_failed_operation", RequestID: "req_failed_operation",
				InteractionID: "int_supply_chain",
				Payload:       map[string]any{"result_artifact_ref": "artifact:interaction_result"},
			},
		},
	}
	result := summaryArtifact(
		t,
		"interaction_result",
		ArtifactTypeResult,
		trace.TraceID,
		map[string]any{"text": "Agent 改用其他 OpenBKN 能力完成了本轮回答"},
	)
	result.RequestID = trace.RequestID
	result.InteractionID = "int_supply_chain"
	result.OperationID = ""

	requests, executions := BuildExecutionSummaries([]NormalizedTrace{trace}, []EvidenceArtifact{result})

	if len(requests) != 1 || requests[0].Status != "error" {
		t.Fatalf("operation failure must not be hidden by the interaction result: %+v", requests)
	}
	if requests[0].ResultPreview != "" {
		t.Fatalf("interaction result must not be copied into an OpenBKN operation: %+v", requests[0])
	}
	if requests[0].InteractionResultArtifactRef != "artifact:interaction_result" {
		t.Fatalf("interaction result artifact must remain available to the interaction read model: %+v", requests[0])
	}
	if len(executions) != 1 || executions[0].Status != "error" {
		t.Fatalf("failed trace must remain visible: %+v", executions)
	}
}

func TestBuildExecutionSummariesExplainsFailedOperationEvidenceWithoutRequiringTurnContent(t *testing.T) {
	trace := NormalizedTrace{
		TraceID: "trace_failed_receipt", RequestID: "req_failed_receipt",
		TenantID: "tenant_demo", BusinessDomain: "bd_demo", AccountID: "acct_demo", AccountType: "app",
		SchemaVersion: ArtifactContractVersion, ConversationID: "conv_supply_chain",
		Events: []EvidenceEvent{
			{
				EventID: "receipt:failed", EventType: "retrieval.completed",
				ObservedAt: "2026-08-04T08:00:00Z", EmittedAt: "2026-08-04T08:00:01Z",
				TraceID: "trace_failed_receipt", RequestID: "req_failed_receipt",
				InteractionID: "int_supply_chain", OperationID: "op_run_sql", OperationName: "run_sql",
				Payload: map[string]any{
					"status": "failed", "evidence_durability": "failed",
				},
			},
		},
	}

	requests, _ := BuildExecutionSummaries([]NormalizedTrace{trace}, nil)

	if len(requests) != 1 || requests[0].EvidenceCompleteness != "partial" ||
		len(requests[0].PartialReasons) != 1 ||
		requests[0].PartialReasons[0] != "evidence_durability_failed" {
		t.Fatalf("failed operation evidence must use an operation-scoped reason: %+v", requests)
	}
	if requests[0].ErrorSummary != "" {
		t.Fatalf("a terminal receipt event type must not be exposed as an error summary: %+v", requests[0])
	}
}

func TestBuildExecutionSummariesKeepsInteractionQuestionOutOfOperation(t *testing.T) {
	trace := NormalizedTrace{
		TraceID: "trace_schema_search", RequestID: "req_schema_search",
		TenantID: "tenant_demo", BusinessDomain: "bd_demo", AccountID: "acct_demo", AccountType: "app",
		SchemaVersion:  ArtifactContractVersion,
		ConversationID: "conv_supply_chain",
		Events: []EvidenceEvent{
			{
				EventID: "event_schema_search", EventType: "retrieval.completed",
				ObservedAt: "2026-08-04T08:00:00Z", EmittedAt: "2026-08-04T08:00:01Z",
				TraceID: "trace_schema_search", RequestID: "req_schema_search",
				InteractionID: "int_supply_chain", OperationID: "op_schema_search", OperationName: "search_schema",
				Payload: map[string]any{
					"question_artifact_ref": "artifact:interaction_question",
					"candidate_count":       3,
				},
			},
		},
	}
	question := summaryArtifact(
		t,
		"interaction_question",
		ArtifactTypeQuestion,
		trace.TraceID,
		map[string]any{"text": "900-000044 是否可生产？"},
	)
	question.RequestID = trace.RequestID
	question.InteractionID = "int_supply_chain"
	question.OperationID = ""

	requests, _ := BuildExecutionSummaries([]NormalizedTrace{trace}, []EvidenceArtifact{question})

	if len(requests) != 1 || requests[0].QuestionPreview != "" {
		t.Fatalf("interaction question must not be presented as operation input: %+v", requests)
	}
}

func TestBuildExecutionSummariesUsesRunSQLRowCount(t *testing.T) {
	trace := NormalizedTrace{
		TraceID: "trace_run_sql", RequestID: "req_run_sql",
		TenantID: "tenant_demo", BusinessDomain: "bd_demo", AccountID: "acct_demo", AccountType: "app",
		Events: []EvidenceEvent{{
			EventID: "event_run_sql", EventType: "data.query.observed",
			ObservedAt: "2026-08-08T08:00:00Z", EmittedAt: "2026-08-08T08:00:01Z",
			TraceID: "trace_run_sql", RequestID: "req_run_sql",
			InteractionID: "int_run_sql", OperationID: "op_run_sql", OperationName: "context.run_sql",
			Payload: map[string]any{"row_count": 10, "query_type": "sql"},
		}},
	}

	requests, _ := BuildExecutionSummaries([]NormalizedTrace{trace}, nil)

	if len(requests) != 1 || requests[0].ResultCount == nil || *requests[0].ResultCount != 10 {
		t.Fatalf("run_sql result count must come from row_count: %+v", requests)
	}
	if requests[0].ToolName != "context.run_sql" || requests[0].OperationID != "op_run_sql" {
		t.Fatalf("run_sql operation identity was lost: %+v", requests[0])
	}
}

func TestBuildExecutionSummariesUsesStructuredRunSQLFailure(t *testing.T) {
	trace := NormalizedTrace{
		TraceID: "trace_run_sql_failed", RequestID: "req_run_sql_failed",
		TenantID: "tenant_demo", BusinessDomain: "bd_demo", AccountID: "acct_demo", AccountType: "app",
		SchemaVersion: ArtifactContractVersion,
		Events: []EvidenceEvent{{
			EventID: "event_run_sql_failed", EventType: "data.query.observed",
			SchemaVersion: ArtifactContractVersion,
			ObservedAt:    "2026-08-08T08:00:00Z", EmittedAt: "2026-08-08T08:00:01Z",
			TraceID: "trace_run_sql_failed", RequestID: "req_run_sql_failed",
			InteractionID: "int_run_sql", OperationID: "op_run_sql", OperationName: "context.run_sql",
			Payload: map[string]any{
				"status": "error", "error_stage": "vega_query",
				"error_code": "RUN_SQL_VEGA_QUERY_FAILED", "safe_error_summary": "unknown column available_qty",
				"row_count": 0, "query_type": "sql",
			},
		}},
	}

	requests, executions := BuildExecutionSummaries([]NormalizedTrace{trace}, nil)

	if len(requests) != 1 || requests[0].Status != "error" || requests[0].ErrorSummary != "unknown column available_qty" {
		t.Fatalf("request summary lost structured run_sql failure: %+v", requests)
	}
	if len(executions) != 1 || executions[0].ErrorSummary != "unknown column available_qty" {
		t.Fatalf("trace summary lost structured run_sql failure: %+v", executions)
	}
}

func TestBuildExecutionSummariesUsesDurableReceiptAsOperationCompletenessAuthority(t *testing.T) {
	trace := NormalizedTrace{
		TraceID: "trace_durable_receipt", RequestID: "req_durable_receipt",
		TenantID: "tenant_demo", BusinessDomain: "bd_demo", AccountID: "acct_demo", AccountType: "app",
		SchemaVersion: ArtifactContractVersion,
		Events: []EvidenceEvent{{
			EventID: "receipt:receipt_durable", EventType: "retrieval.completed",
			ObservedAt: "2026-08-04T08:00:00Z", EmittedAt: "2026-08-04T08:00:01Z",
			TraceID: "trace_durable_receipt", RequestID: "req_durable_receipt",
			InteractionID: "int_supply_chain", OperationID: "op_search_schema", OperationName: "search_schema",
			Payload: map[string]any{
				"status": "completed", "evidence_durability": "durable",
				"partial_reasons": []string{},
			},
		}},
	}

	requests, _ := BuildExecutionSummaries([]NormalizedTrace{trace}, nil)

	if len(requests) != 1 || requests[0].EvidenceCompleteness != "complete" {
		t.Fatalf("durable receipt without partial reasons must be complete: %+v", requests)
	}
}

func TestBuildExecutionSummariesKeepsReceiptPartialReasons(t *testing.T) {
	trace := NormalizedTrace{
		TraceID: "trace_partial_receipt", RequestID: "req_partial_receipt",
		TenantID: "tenant_demo", BusinessDomain: "bd_demo", AccountID: "acct_demo", AccountType: "app",
		SchemaVersion: ArtifactContractVersion,
		Events: []EvidenceEvent{{
			EventID: "receipt:receipt_partial", EventType: "retrieval.completed",
			ObservedAt: "2026-08-04T08:00:00Z", EmittedAt: "2026-08-04T08:00:01Z",
			TraceID: "trace_partial_receipt", RequestID: "req_partial_receipt",
			InteractionID: "int_supply_chain", OperationID: "op_run_sql", OperationName: "run_sql",
			Payload: map[string]any{
				"status": "completed", "evidence_durability": "durable",
				"partial_reasons": []any{"business_refs_unresolved"},
			},
		}},
	}

	requests, _ := BuildExecutionSummaries([]NormalizedTrace{trace}, nil)

	if len(requests) != 1 || requests[0].EvidenceCompleteness != "partial" ||
		!containsSummaryReason(requests[0].PartialReasons, "business_refs_unresolved") {
		t.Fatalf("receipt partial reason must remain visible: %+v", requests)
	}
}

func TestBuildExecutionSummariesRequiresAllTracesTerminalBeforeRequestCompletion(t *testing.T) {
	completed := summaryTrace("trace_completed", "req_summary", "2026-07-26T08:00:00Z", "2026-07-26T08:00:02Z")
	completed.SchemaVersion = ArtifactContractVersion
	completed.Events[0].SchemaVersion = ArtifactContractVersion
	completed.Events[0].Payload["result_artifact_ref"] = "artifact:artifact_completed_result"
	result := summaryArtifact(t, "artifact_completed_result", ArtifactTypeResult, completed.TraceID, map[string]any{"text": "完成结果"})
	running := NormalizedTrace{
		TraceID: "trace_running_mixed", RequestID: "req_summary",
		TenantID: "tenant_demo", BusinessDomain: "bd_demo", AccountID: "acct_demo", AccountType: "app",
		SchemaVersion: ArtifactContractVersion,
		Events: []EvidenceEvent{{
			EventID: "event_running_mixed", EventType: "data.query.observed",
			ObservedAt: "2026-07-26T08:00:03Z", EmittedAt: "2026-07-26T08:00:03Z",
			TraceID: "trace_running_mixed", RequestID: "req_summary", OperationName: "data.query",
			Payload: map[string]any{"status": "running"},
		}},
	}
	unknown := NormalizedTrace{
		TraceID: "trace_unknown_mixed", RequestID: "req_summary",
		TenantID: "tenant_demo", BusinessDomain: "bd_demo", AccountID: "acct_demo", AccountType: "app",
		SchemaVersion: ArtifactContractVersion,
	}
	failed := running
	failed.TraceID = "trace_failed_mixed"
	failed.Events = append([]EvidenceEvent{}, running.Events...)
	failed.Events[0].TraceID = failed.TraceID
	failed.Events[0].EventID = "event_failed_mixed"
	failed.Events[0].Payload = map[string]any{"status": "error", "error_code": "QUERY_FAILED"}

	testCases := []struct {
		name       string
		traces     []NormalizedTrace
		wantStatus string
	}{
		{name: "completed and running stays running", traces: []NormalizedTrace{completed, running}, wantStatus: "running"},
		{name: "error wins but running prevents completion time", traces: []NormalizedTrace{completed, failed, running}, wantStatus: "error"},
		{name: "completed and unknown stays unknown", traces: []NormalizedTrace{completed, unknown}, wantStatus: "unknown"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			requests, _ := BuildExecutionSummaries(testCase.traces, []EvidenceArtifact{result})

			if len(requests) != 1 || requests[0].Status != testCase.wantStatus {
				t.Fatalf("unexpected mixed trace request status: %+v", requests)
			}
			if requests[0].CompletedAt != "" || requests[0].DurationMS != 0 {
				t.Fatalf("non-terminal mixed request must not expose completion timing: %+v", requests[0])
			}
		})
	}
}

func TestBuildExecutionSummariesIgnoresArtifactWhoseTypeDoesNotMatchLinkRole(t *testing.T) {
	trace := NormalizedTrace{
		TraceID: "trace_type_mismatch", RequestID: "req_summary",
		TenantID: "tenant_demo", BusinessDomain: "bd_demo", AccountID: "acct_demo", AccountType: "app",
		SchemaVersion: ArtifactContractVersion,
		Events: []EvidenceEvent{{
			EventID: "event_type_mismatch", EventType: "claim.created",
			SchemaVersion: ArtifactContractVersion,
			ObservedAt:    "2026-07-26T08:00:00Z", EmittedAt: "2026-07-26T08:00:00Z",
			TraceID: "trace_type_mismatch", RequestID: "req_summary", OperationName: "claim.create",
			Payload: map[string]any{
				"claim_id":            "claim_type_mismatch",
				"result_artifact_ref": "artifact:artifact_wrong_claim_result",
			},
		}},
	}
	wrongType := summaryArtifact(
		t,
		"artifact_wrong_claim_result",
		ArtifactTypeQuestion,
		trace.TraceID,
		map[string]any{"text": "不应作为结论"},
	)

	requests, executions := BuildExecutionSummaries([]NormalizedTrace{trace}, []EvidenceArtifact{wrongType})

	if requests[0].QuestionPreview != "" || requests[0].ResultPreview != "" ||
		requests[0].Status == "completed" || executions[0].Status == "completed" {
		t.Fatalf("type-mismatched artifact must not affect content or state: request=%+v trace=%+v", requests[0], executions[0])
	}
	if requests[0].EvidenceCompleteness != "partial" ||
		!containsSummaryReason(requests[0].PartialReasons, "artifact_type_mismatch") {
		t.Fatalf("type mismatch must be explicit partial evidence: %+v", requests[0])
	}
}

func summaryTrace(traceID, requestID, observedAt, emittedAt string) NormalizedTrace {
	return NormalizedTrace{
		TraceID: traceID, RequestID: requestID,
		TenantID: "tenant_demo", BusinessDomain: "bd_demo", AccountID: "acct_demo", AccountType: "app",
		SchemaVersion: ContractVersion,
		Events: []EvidenceEvent{{
			EventID: "event_" + traceID, EventType: "claim.created",
			ObservedAt: observedAt, EmittedAt: emittedAt, Producer: "supply-chain-agent",
			TraceID: traceID, SpanID: "span_" + traceID, RequestID: requestID,
			OperationName: "agent.answer", ClaimID: "claim_" + traceID,
			Payload: map[string]any{
				"claim_id": "claim_" + traceID, "visibility": "visible",
				"business_refs": []any{map[string]any{"ref_id": "object:kn_supply:forecast", "visibility": "visible"}},
			},
		}},
	}
}

func summaryArtifact(t *testing.T, artifactID string, artifactType ArtifactType, traceID string, content any) EvidenceArtifact {
	t.Helper()
	artifact, validationErrors := NormalizeArtifact(EvidenceArtifact{
		ArtifactID: artifactID, ArtifactType: artifactType,
		RequestID: "req_summary", TraceID: traceID,
		ContentType: "application/json", SchemaVersion: ArtifactContractVersion,
		ObservedAt: "2026-07-26T08:00:01Z", Content: content,
		BusinessRefs: []string{"object:kn_supply:forecast"},
		TenantID:     "tenant_demo", BusinessDomain: "bd_demo", AccountID: "acct_demo", AccountType: "app",
		AgentOrApp: "supply-chain-agent", Initiator: "studio-user",
	})
	if len(validationErrors) != 0 {
		t.Fatalf("normalize summary artifact: %+v", validationErrors)
	}
	return artifact
}

func containsSummaryReason(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
