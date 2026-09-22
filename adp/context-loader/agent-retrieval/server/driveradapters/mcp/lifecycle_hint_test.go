// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
)

// A model that copied an interaction_id wrong got resource_not_disclosed and
// read it as a missing permission. The refusal now says how to fix the ID; the
// code, the action and every other refusal stay as Trace Core sent them.
func TestNotDisclosedRefusalSaysToCopyTheIDs(t *testing.T) {
	core := bkntrace.APIError{
		Code: "resource_not_disclosed", Message: "not within the authorized scope",
		RequiredAction: "verify_scope_or_identifier",
	}
	got := traceCoreError(core)
	if got.Code != core.Code || got.RequiredAction != core.RequiredAction {
		t.Fatalf("refusal changed shape: %+v", got)
	}
	if !strings.HasPrefix(got.Message, core.Message) || !strings.Contains(got.Message, "bkn_start_interaction") {
		t.Fatalf("message = %q", got.Message)
	}

	other := bkntrace.APIError{Code: "conversation_owner_mismatch", Message: "owner mismatch"}
	if traceCoreError(other).Message != other.Message {
		t.Fatalf("another refusal was rewritten: %q", traceCoreError(other).Message)
	}

	result, err := lifecycleCallResult(nil, &core, nil)
	if err != nil || !result.IsError || !strings.Contains(resultText(result), "bkn_start_interaction") {
		t.Fatalf("finish path did not carry the hint: %+v %v", result, err)
	}
}
