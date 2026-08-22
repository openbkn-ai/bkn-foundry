// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package tracesvc

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/oteltracevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
)

func TestBuildTechnicalTraceDetailKeepsOperationWithoutSpanAndSpanWithoutOperation(t *testing.T) {
	t.Parallel()

	started := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	graph := oteltracevo.TraceGraphResponse{
		TraceID: "trace-1", Status: "ok",
		Data: oteltracevo.TraceGraphData{Nodes: []oteltracevo.TraceGraphNode{{
			SpanID: "span-only", Name: "POST /chat", Status: "ok",
		}}},
	}
	executions := []sessionvo.OperationExecution{{
		Fact: sessionvo.OperationCallFact{
			OperationID: "op-without-span", Attempt: 1, TraceID: "trace-1",
			ToolName: "run_sql", StartedAt: started, Status: sessionvo.AttemptCompleted,
			Input: sessionvo.PayloadEnvelope{
				Mode: sessionvo.PayloadInline, MediaType: "application/json",
				ByteLength: 27, Inline: json.RawMessage(`{"sql":"SELECT * FROM t"}`),
			},
		},
		Receipt:           sessionvo.Receipt{Status: sessionvo.ReceiptCompleted},
		InteractionStatus: sessionvo.InteractionCompleted,
	}}

	detail := BuildTechnicalTraceDetail(
		evidencevo.TraceSummary{TraceID: "trace-1", Status: "completed"},
		&graph,
		executions,
	)

	if detail.Graph == nil || len(detail.Graph.Data.Nodes) != 1 || detail.Graph.Data.Nodes[0].SpanID != "span-only" {
		t.Fatalf("span-only node must remain visible: %+v", detail.Graph)
	}
	if len(detail.Operations) != 1 || detail.Operations[0].Fact.OperationID != "op-without-span" {
		t.Fatalf("operation without span must remain visible: %+v", detail.Operations)
	}
	if detail.Operations[0].State != "completed" {
		t.Fatalf("unexpected operation state: %+v", detail.Operations[0])
	}
}

func TestBuildTechnicalTraceDetailMarksPendingOperationOnTerminalInteractionMissingTerminal(t *testing.T) {
	t.Parallel()

	detail := BuildTechnicalTraceDetail(
		evidencevo.TraceSummary{TraceID: "trace-missing", Status: "completed"},
		nil,
		[]sessionvo.OperationExecution{{
			Fact: sessionvo.OperationCallFact{
				OperationID: "op-missing", Attempt: 1, TraceID: "trace-missing",
				Status: sessionvo.AttemptPending,
			},
			Receipt:           sessionvo.Receipt{Status: sessionvo.ReceiptPending},
			InteractionStatus: sessionvo.InteractionCompleted,
		}},
	)

	if len(detail.Operations) != 1 || detail.Operations[0].State != "missing_terminal" {
		t.Fatalf("terminal interaction pending operation must be missing_terminal: %+v", detail.Operations)
	}
	if !detail.Partial || !contains(detail.PartialReasons, "missing_terminal") {
		t.Fatalf("missing terminal must make the typed detail partial: %+v", detail)
	}
}

func TestBuildTechnicalTraceDetailReportsUnavailableSpanWithoutDroppingOperation(t *testing.T) {
	t.Parallel()

	detail := BuildTechnicalTraceDetail(
		evidencevo.TraceSummary{TraceID: "trace-no-span", Status: "unknown"},
		nil,
		[]sessionvo.OperationExecution{{
			Fact: sessionvo.OperationCallFact{
				OperationID: "op-no-span", TraceID: "trace-no-span", Status: sessionvo.AttemptCompleted,
			},
			Receipt:           sessionvo.Receipt{Status: sessionvo.ReceiptCompleted},
			InteractionStatus: sessionvo.InteractionCompleted,
		}},
	)

	if len(detail.Operations) != 1 || !detail.Partial || !contains(detail.PartialReasons, "span_unavailable") {
		t.Fatalf("missing Span must be explicit without dropping Operation: %+v", detail)
	}
}

func TestBuildTechnicalTraceDetailReportsUnavailableSpanForSummaryOnlyTrace(t *testing.T) {
	t.Parallel()

	detail := BuildTechnicalTraceDetail(
		evidencevo.TraceSummary{TraceID: "trace-summary-only", Status: "unknown"}, nil, nil,
	)

	if !detail.Partial || !contains(detail.PartialReasons, "span_unavailable") {
		t.Fatalf("summary without readable Span data must be explicit: %+v", detail)
	}
}
