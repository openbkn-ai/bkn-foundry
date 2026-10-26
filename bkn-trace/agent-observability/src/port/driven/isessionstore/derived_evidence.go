// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package isessionstore

import "github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"

// DerivedEvidenceTransaction appends a Core-created Claim event atomically with
// its assembly revision. The acknowledgment becomes durable only when the outer
// lifecycle transaction commits. Unsupported stores must not emulate this with
// a second, independently committed Ledger write.
type DerivedEvidenceTransaction interface {
	AppendDerivedEvidence(ledgervo.Event) (ledgervo.DurableAck, error)
}
