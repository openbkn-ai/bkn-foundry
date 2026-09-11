// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore

import (
	"crypto/sha256"
	"encoding/hex"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"testing"
)

func sealedInputFixture() sessionvo.SealedRevisionInput {
	b := []byte("fixed package")
	h := sha256.Sum256(b)
	return sessionvo.SealedRevisionInput{RevisionID: "r", InteractionID: "i", Hash: "sha256:" + hex.EncodeToString(h[:]), Package: b}
}
func TestStoredRevisionInputRejectsTamperingAndBudget(t *testing.T) {
	good := sealedInputFixture()
	if err := validateStoredRevisionInput(good, 100); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*sessionvo.SealedRevisionInput){func(v *sessionvo.SealedRevisionInput) { v.Hash = "bad" }, func(v *sessionvo.SealedRevisionInput) { v.Package = []byte("changed") }, func(v *sessionvo.SealedRevisionInput) { v.RevisionID = "" }, func(v *sessionvo.SealedRevisionInput) { v.InteractionID = "" }} {
		v := sealedInputFixture()
		mutate(&v)
		if validateStoredRevisionInput(v, 100) == nil {
			t.Fatal("invalid input accepted")
		}
	}
	if validateStoredRevisionInput(good, 1) == nil {
		t.Fatal("budget ignored")
	}
}
