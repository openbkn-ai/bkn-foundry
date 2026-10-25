// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package tracesvc

import (
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/oteltracevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
)

// SpanDisplayRange describes only the loaded graph window. The summary's
// distinct-span cardinality is an estimate, never a cursor or exhaustion test.
type SpanDisplayRange struct {
	LoadedCount    int        `json:"loaded_count"`
	DisplayLimit   int        `json:"display_limit"`
	RawRecordLimit int        `json:"raw_record_limit"`
	RawRecordCount int        `json:"raw_record_count"`
	TotalCount     *int       `json:"total_count"`
	TotalAccuracy  string     `json:"total_accuracy"`
	Sort           string     `json:"sort"`
	Truncated      bool       `json:"truncated"`
	FirstStartAt   *time.Time `json:"first_start_at"`
	LastStartAt    *time.Time `json:"last_start_at"`
	BrowseAllowed  bool       `json:"browse_allowed"`
	BrowseReason   string     `json:"browse_reason,omitempty"`
}

type TechnicalTraceDetail struct {
	SpanDisplay    SpanDisplayRange                `json:"span_display"`
	Summary        evidencevo.TraceSummary         `json:"summary"`
	Graph          *oteltracevo.TraceGraphResponse `json:"graph,omitempty"`
	Operations     []TechnicalOperation            `json:"operations"`
	Partial        bool                            `json:"partial"`
	PartialReasons []string                        `json:"partial_reasons,omitempty"`
}

type TechnicalOperation struct {
	Fact           sessionvo.OperationCallFact `json:"fact"`
	Receipt        sessionvo.Receipt           `json:"receipt"`
	State          string                      `json:"state"`
	PartialReasons []string                    `json:"partial_reasons,omitempty"`
}

func BuildTechnicalTraceDetail(
	summary evidencevo.TraceSummary,
	graph *oteltracevo.TraceGraphResponse,
	executions []sessionvo.OperationExecution,
) TechnicalTraceDetail {
	detail := TechnicalTraceDetail{
		Summary: summary, Graph: graph, Operations: make([]TechnicalOperation, 0, len(executions)),
		SpanDisplay: SpanDisplayRange{DisplayLimit: DefaultTraceGraphSpanLimit, RawRecordLimit: DefaultTraceGraphSpanLimit + 1, TotalAccuracy: "unknown", Sort: "loaded_start_time_asc_span_id_asc"},
	}
	if graph != nil {
		detail.SpanDisplay.LoadedCount = len(graph.Data.Nodes)
		detail.SpanDisplay.RawRecordCount = graph.Page.RawRecordCount
		detail.SpanDisplay.Truncated = graph.Page.Truncated
		if len(graph.Data.Nodes) > 0 {
			first, last := graph.Data.Nodes[0].StartNano, graph.Data.Nodes[len(graph.Data.Nodes)-1].StartNano
			if first > 0 {
				t := time.Unix(0, first).UTC()
				detail.SpanDisplay.FirstStartAt = &t
			}
			if last > 0 {
				t := time.Unix(0, last).UTC()
				detail.SpanDisplay.LastStartAt = &t
			}
		}
	}
	if summary.SpanCountStatus == "available" && summary.SpanCount > 0 && summary.SpanCount >= detail.SpanDisplay.LoadedCount {
		count := summary.SpanCount
		detail.SpanDisplay.TotalCount = &count
		detail.SpanDisplay.TotalAccuracy = "estimated"
	}
	if graph == nil {
		detail.Partial = true
		detail.PartialReasons = append(detail.PartialReasons, "span_unavailable")
	}
	if graph != nil && graph.Partial {
		detail.Partial = true
		for _, reason := range graph.PartialReasons {
			detail.PartialReasons = appendUniqueReason(detail.PartialReasons, reason)
		}
	}
	for _, execution := range executions {
		state := string(execution.Fact.Status)
		reasons := append([]string(nil), execution.Receipt.PartialReasons...)
		if execution.Fact.Status == sessionvo.AttemptPending &&
			execution.InteractionStatus != sessionvo.InteractionActive {
			state = "missing_terminal"
			reasons = appendUniqueReason(reasons, "missing_terminal")
		}
		if len(reasons) > 0 {
			detail.Partial = true
			for _, reason := range reasons {
				detail.PartialReasons = appendUniqueReason(detail.PartialReasons, reason)
			}
		}
		detail.Operations = append(detail.Operations, TechnicalOperation{
			Fact: execution.Fact, Receipt: execution.Receipt, State: state, PartialReasons: reasons,
		})
	}
	return detail
}

func appendUniqueReason(values []string, value string) []string {
	if value == "" {
		return values
	}
	for _, current := range values {
		if current == value {
			return values
		}
	}
	return append(values, value)
}
