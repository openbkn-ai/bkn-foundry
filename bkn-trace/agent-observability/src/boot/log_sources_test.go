// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package boot

import (
	"context"
	"reflect"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/logsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
)

type namedLogSource string

func (source namedLogSource) ID() string { return string(source) }

func (namedLogSource) Search(context.Context, observabilityvo.LogQuery) (observabilityvo.SourcePage, error) {
	return observabilityvo.SourcePage{}, nil
}

func TestAssembleLogSourcesUsesCenterLedgerInsteadOfLegacyAuditSources(t *testing.T) {
	runtimeSources := []logsvc.Source{namedLogSource("runtime"), namedLogSource("access-user")}
	legacyAuditSources := []logsvc.Source{namedLogSource("legacy-audit"), namedLogSource("legacy-security")}
	central := namedLogSource("audit-ledger")

	enabled := assembleLogSources(runtimeSources, legacyAuditSources, central, true)
	if got := logSourceIDs(enabled); !reflect.DeepEqual(got, []string{"audit-ledger", "runtime", "access-user"}) {
		t.Fatalf("Kafka Audit enabled sources=%v", got)
	}
	disabled := assembleLogSources(runtimeSources, legacyAuditSources, central, false)
	if got := logSourceIDs(disabled); !reflect.DeepEqual(got, []string{"runtime", "access-user", "legacy-audit", "legacy-security"}) {
		t.Fatalf("Kafka Audit disabled sources=%v", got)
	}
}

func logSourceIDs(sources []logsvc.Source) []string {
	ids := make([]string, 0, len(sources))
	for _, source := range sources {
		ids = append(ids, source.ID())
	}
	return ids
}
