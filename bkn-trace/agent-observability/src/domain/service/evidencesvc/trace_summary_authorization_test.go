// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package evidencesvc

import (
	"context"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/evidencestore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionsource"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

// Hiding the optional failure interface exercises the legacy reader. Keep the
// independent source capability so these assertions concern error reads alone.
type authorizationLegacyStore struct{ *sessionstore.Store }
type authorizationLegacyTx struct {
	isessionstore.Transaction
	source isessionstore.TraceSourceMetadataTransaction
}

func (s authorizationLegacyStore) WithinTransaction(ctx context.Context, fn func(isessionstore.Transaction) error) error {
	return s.Store.WithinTransaction(ctx, func(tx isessionstore.Transaction) error {
		return fn(authorizationLegacyTx{tx, tx.(isessionstore.TraceSourceMetadataTransaction)})
	})
}
func (tx authorizationLegacyTx) ListFirstTraceSourceMetadata(scopes []isessionstore.TraceMetadataScope) []sessionvo.OperationCallFact {
	return tx.source.ListFirstTraceSourceMetadata(scopes)
}

func TestTraceSummaryFailureRequiresAuthorizedTuple(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		path := "narrow"
		if legacy {
			path = "legacy"
		}
		for _, allowed := range []bool{false, true} {
			scenario := "synthetic-tuple-denied"
			if allowed {
				scenario = "authorized-tuple-allowed"
			}
			t.Run(path+"/"+scenario, func(t *testing.T) {
				store := sessionstore.New()
				payload := func(raw string) *sessionvo.PayloadEnvelope {
					p, err := sessionvo.InlineJSONPayload([]byte(raw))
					if err != nil {
						t.Fatal(err)
					}
					return &p
				}
				if err := store.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
					tx.SaveOperationCallFact(sessionvo.OperationCallFact{OperationID: "private", Attempt: 1, TraceID: "t", ConversationID: "convA", InteractionID: "intB", Status: sessionvo.AttemptFailed, StartedAt: time.Unix(1, 0), Error: payload(`"private-collision-marker"`)})
					tx.SaveOperationCallFact(sessionvo.OperationCallFact{OperationID: "allowed", Attempt: 1, TraceID: "t", ConversationID: "convB", InteractionID: "intB", Status: sessionvo.AttemptFailed, StartedAt: time.Unix(2, 0), Error: payload(`"authorized-error-marker"`)})
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				svc := New(evidencestore.New(), WithSessionStore(store))
				if legacy {
					svc = New(evidencestore.New(), WithSessionStore(authorizationLegacyStore{store}))
				}
				conversation := "convA"
				if allowed {
					conversation = "convB"
				}
				traces := []evidencevo.TraceSummary{{TraceID: "t", ConversationID: conversation, InteractionID: "intB", Status: "error"}}
				visible := map[string][]iprojectionsource.CanonicalTraceScope{"t": {{ConversationID: "convA", InteractionID: ""}, {ConversationID: "convB", InteractionID: "intB"}}}
				if err := svc.applyTraceMetadata(context.Background(), traces, evidencevo.QueryScope{AccountID: "owner", AccountType: "user"}, false, visible); err != nil {
					t.Fatal(err)
				}
				if matchesTraceFilters(traces[0], evidencevo.SummaryQueryOptions{ErrorKeyword: "private-collision-marker"}) {
					t.Error("unauthorized tuple influenced keyword membership")
				}
				if allowed {
					if traces[0].ErrorSummary != "authorized-error-marker" || !matchesTraceFilters(traces[0], evidencevo.SummaryQueryOptions{ErrorKeyword: "authorized-error-marker"}) {
						t.Fatalf("authorized error lost: %q", traces[0].ErrorSummary)
					}
				} else if traces[0].ErrorSummary != "" {
					t.Fatalf("synthetic tuple leaked error: %q", traces[0].ErrorSummary)
				}
			})
		}
	}
}
