// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package capturepolicysnapshot

import (
	"crypto/ed25519"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/capturepolicysvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/traceadmissionsvc"
)

func TestSignerProducesGatewayVerifiableSnapshot(t *testing.T) {
	privateKey := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	now := time.Date(2026, 9, 22, 8, 0, 0, 0, time.UTC)
	signer := Signer{PrivateKey: privateKey, KeyID: "capture-2026", Audience: "cluster-a", TTL: 15 * time.Minute, Now: func() time.Time { return now }}
	snapshot, err := signer.Sign(42, capturepolicysvc.StateEnabled)
	if err != nil {
		t.Fatalf("sign snapshot: %v", err)
	}
	gateway := traceadmissionsvc.NewGateway(traceadmissionsvc.GatewayConfig{Audience: "cluster-a", CurrentKeyID: "capture-2026", CurrentKey: privateKey.Public().(ed25519.PublicKey), Now: func() time.Time { return now }})
	if err := gateway.Apply(snapshot); err != nil {
		t.Fatalf("gateway rejected signer output: %v", err)
	}
	if decision := gateway.Admit(3); decision.Accepted != 3 || decision.Dropped != 0 {
		t.Fatalf("unexpected admission decision: %+v", decision)
	}
}

func TestSignerFailsClosedWhenIdentityIsIncomplete(t *testing.T) {
	_, err := (Signer{TTL: time.Minute}).Sign(1, capturepolicysvc.StateEnabled)
	if err != ErrNotConfigured {
		t.Fatalf("err = %v, want %v", err, ErrNotConfigured)
	}
}
