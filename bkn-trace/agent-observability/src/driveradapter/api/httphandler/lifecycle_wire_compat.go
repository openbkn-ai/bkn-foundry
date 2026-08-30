// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package httphandler

import (
	"time"

	sessionvo "github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
)

// businessRefRequest is the wire shape of a business ref on the lifecycle and
// evidence-ledger bodies.
//
// business_domain_id left the contract in 0.1.5, but these bodies are decoded
// with DisallowUnknownFields: a producer that has not been upgraded yet — an
// older service during a rolling upgrade, or an out-of-tree SDK — would get a
// 400 for a field the platform simply no longer cares about. Accepting the key
// here and dropping it keeps the strict contract for everything else while the
// retired field degrades to "ignored". Delete this shim once every producer is
// known to be on 0.1.5.
type businessRefRequest struct {
	sessionvo.BusinessRef
	// Kept out of the published contract: 0.1.5 documents the tenant-only shape,
	// and this field only exists so an older producer is not rejected for sending
	// a key the platform now ignores.
	RetiredBusinessDomainID string `json:"business_domain_id,omitempty" swaggerignore:"true"`
}

func businessRefsFromWire(refs []businessRefRequest) []sessionvo.BusinessRef {
	if len(refs) == 0 {
		return nil
	}
	result := make([]sessionvo.BusinessRef, 0, len(refs))
	for _, ref := range refs {
		result = append(result, ref.BusinessRef)
	}
	return result
}

// operationBusinessEdgeRequest carries the same tolerance one level down: the
// edge embeds a business ref, and DisallowUnknownFields applies to nested
// objects as well, so an un-upgraded producer that sends operation_business_edges
// would be rejected even though its business_refs are accepted.
type operationBusinessEdgeRequest struct {
	OperationID string                          `json:"operation_id" binding:"required"`
	BusinessRef businessRefRequest              `json:"business_ref" binding:"required"`
	Role        sessionvo.OperationBusinessRole `json:"role" binding:"required"`
	ObservedAt  time.Time                       `json:"observed_at" binding:"required"`
}

func operationBusinessEdgesFromWire(edges []operationBusinessEdgeRequest) []sessionvo.OperationBusinessEdge {
	if len(edges) == 0 {
		return nil
	}
	result := make([]sessionvo.OperationBusinessEdge, 0, len(edges))
	for _, edge := range edges {
		result = append(result, sessionvo.OperationBusinessEdge{
			OperationID: edge.OperationID, BusinessRef: edge.BusinessRef.BusinessRef,
			Role: edge.Role, ObservedAt: edge.ObservedAt,
		})
	}
	return result
}
