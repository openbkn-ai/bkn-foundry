// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package entitlement

import (
	"crypto/ed25519"

	"github.com/openbkn-ai/licverify"
)

// evaluate turns a signed licence text into the snapshot the product gates on.
//
// Only two of licverify's states carry paid capability: valid, and grace (a
// licence that expired recently — the renewal has not landed yet, and taking
// the customer's capability away over that is a support incident, not
// enforcement). Everything else lands on community:
//
//	fallback_community  once licensed, now well past grace
//	invalid             malformed, bad signature, unknown key
//	trial / unlicensed  no licence at all
//
// The distinction between those is preserved in State for operator surfaces —
// "your licence expired" and "no licence was ever installed" need different
// responses from a human — but none of them changes what the product does.
func evaluate(text string, keys map[string]ed25519.PublicKey) Snapshot {
	state, payload := licverify.Eval(text, keys)

	snap := Snapshot{State: state, Edition: licverify.EditionCommunity}
	if payload != nil {
		// Features come from the certificate even when it is not in force:
		// an operator comparing "what we bought" against "what is running"
		// needs to see the list either way. Nothing branches on it.
		snap.Features = payload.Features
	}

	switch state {
	case licverify.StateValid, licverify.StateGrace:
		if payload != nil {
			snap.Licensed = true
			snap.Edition = payload.Edition
		}
	}
	return snap
}
