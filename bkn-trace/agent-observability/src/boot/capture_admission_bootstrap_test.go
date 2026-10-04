// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package boot

import (
	"context"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/captureadmission"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/capturepolicysvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/capturepolicystore"
)

func TestMemoryCapturePolicyCommanderRefreshesAdmission(t *testing.T) {
	store := capturepolicystore.New(capturepolicysvc.Snapshot{
		Revision: 1, DesiredState: capturepolicysvc.StateEnabled,
		EffectiveState: capturepolicysvc.StateEnabled, LastStableRevision: 1,
		Operation: capturepolicysvc.Operation{Phase: capturepolicysvc.PhaseSucceeded},
	})
	view := captureadmission.New(0, string(capturepolicysvc.StateDisabled))
	commander := memoryCapturePolicyCommander{store: store, admission: view}
	if _, err := commander.Request(context.Background(), capturepolicysvc.ChangeRequest{DesiredState: capturepolicysvc.StateDisabled, ExpectedRevision: 1}); err != nil {
		t.Fatalf("disable memory capture policy: %v", err)
	}
	if view.AllowsNewRecords() {
		t.Fatal("memory capture policy disable did not refresh admission")
	}
}
