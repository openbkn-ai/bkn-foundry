// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package tracesvc

import (
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/oteltracevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
)

type TechnicalTraceDetail struct {
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
