// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionsvc

import (
	"errors"
	"testing"
)

// TestResourceNotDisclosedCarriesCause locks the two halves of the contract:
// every caller gets the same opaque answer, and the server can still tell the
// cases apart. A conversation that does not exist and one that exists under a
// different owner are the pair that matters - four rounds of a supply-chain run
// were spent chasing the second while reading the error as the first.
func TestResourceNotDisclosedCarriesCause(t *testing.T) {
	missing := resourceNotDisclosed(CauseConversationNotFound)
	mismatch := resourceNotDisclosed(CauseConversationOwnerMismatch)

	var a, b *DomainError
	if !errors.As(missing, &a) || !errors.As(mismatch, &b) {
		t.Fatal("resourceNotDisclosed must return a *DomainError")
	}

	if a.Code != b.Code {
		t.Fatalf("callers must not be able to tell the cases apart: %s vs %s", a.Code, b.Code)
	}
	if a.Message != b.Message {
		t.Fatalf("messages diverged: %q vs %q", a.Message, b.Message)
	}
	if a.Cause == b.Cause {
		t.Fatalf("both causes are %q; the server cannot tell them apart either", a.Cause)
	}
	if a.Cause != CauseConversationNotFound || b.Cause != CauseConversationOwnerMismatch {
		t.Fatalf("causes not carried: %q %q", a.Cause, b.Cause)
	}
}

// TestDomainErrorCauseIsNotSerialised guards the disclosure boundary: the
// transport encodes lifecycleError, never DomainError, so the cause can only
// escape if someone starts marshalling the domain type directly.
func TestDomainErrorCauseIsNotSerialised(t *testing.T) {
	err := resourceNotDisclosed(CauseConversationOwnerMismatch)
	var domainErr *DomainError
	if !errors.As(err, &domainErr) {
		t.Fatal("expected a *DomainError")
	}
	if domainErr.Error() != domainErr.Message {
		t.Fatalf("Error() must expose only the opaque message, got %q", domainErr.Error())
	}
}
