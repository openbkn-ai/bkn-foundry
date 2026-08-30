// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package httphandler

import (
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
	RetiredBusinessDomainID string `json:"business_domain_id,omitempty"`
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
