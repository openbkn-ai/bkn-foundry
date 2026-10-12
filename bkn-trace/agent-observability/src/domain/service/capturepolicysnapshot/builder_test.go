// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package capturepolicysnapshot

import (
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/capturepolicysvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/traceadmissionsvc"
)

func TestBuilderProducesUnsignedSnapshot(t *testing.T) {
	now := time.Date(2026, 9, 22, 8, 0, 0, 0, time.UTC)
	builder := Builder{TTL: 15 * time.Minute, Now: func() time.Time { return now }}
	snapshot, err := builder.Build(42, capturepolicysvc.StateEnabled)
	if err != nil {
		t.Fatalf("build snapshot: %v", err)
	}
	gateway := traceadmissionsvc.NewGateway(traceadmissionsvc.GatewayConfig{Now: func() time.Time { return now }})
	if err := gateway.Apply(snapshot); err != nil {
		t.Fatalf("gateway rejected builder output: %v", err)
	}
	if decision := gateway.Admit(3); decision.Accepted != 3 || decision.Dropped != 0 {
		t.Fatalf("unexpected admission decision: %+v", decision)
	}
}

func TestBuilderRequiresPositiveTTL(t *testing.T) {
	_, err := (Builder{}).Build(1, capturepolicysvc.StateEnabled)
	if err != ErrNotConfigured {
		t.Fatalf("err = %v, want %v", err, ErrNotConfigured)
	}
}
