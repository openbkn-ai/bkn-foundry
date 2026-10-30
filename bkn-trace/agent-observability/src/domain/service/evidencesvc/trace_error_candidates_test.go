// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package evidencesvc

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/evidencestore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/memoryaccess/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionsource"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

func TestErrorCandidateFilterPreservesFirstFailureAndUnicodeSummary(t *testing.T) {
	wrapped, _ := json.Marshal(map[string]any{"stderr": "removed-prefix" + strings.Repeat("界", 513) + "尾部 NEEDLE", "exit_code": 1})
	for _, tc := range []struct {
		name, message, keyword string
		want                   bool
	}{
		{"case-and-unicode", "失败 Needle 错误", "NEEDLE 错误", true},
		{"empty-failure-fallback", "", "openbkn operation failed", true},
		{"ordinary-prefix", "Needle" + strings.Repeat("界", 513), "needle", true},
		{"ordinary-truncated-tail", strings.Repeat("界", 512) + "needle", "needle", false},
		{"wrapped-stderr-tail", string(wrapped), "尾部 needle", true},
		{"wrapped-stderr-truncated-head", string(wrapped), "removed-prefix", false},
		{"later-failure-only", "first error", "second-needle", false},
		{"foreign-tuple-only", "first error", "private-marker", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := sessionstore.New()
			err := store.WithinTransaction(context.Background(), func(tx isessionstore.Transaction) error {
				payload := func(text string) *sessionvo.PayloadEnvelope {
					raw, _ := json.Marshal(text)
					return &sessionvo.PayloadEnvelope{Mode: sessionvo.PayloadInline, Inline: raw}
				}
				for _, fact := range []sessionvo.OperationCallFact{
					{TraceID: "t", ConversationID: "c", InteractionID: "i", OperationID: "first", Attempt: 1, StartedAt: time.Unix(1, 0), Status: sessionvo.AttemptFailed, Error: payload(tc.message)},
					{TraceID: "t", ConversationID: "c", InteractionID: "i", OperationID: "second", Attempt: 1, StartedAt: time.Unix(2, 0), Status: sessionvo.AttemptFailed, Error: payload("second-needle")},
					{TraceID: "t", ConversationID: "foreign", InteractionID: "private", OperationID: "private", Attempt: 1, StartedAt: time.Unix(0, 0), Status: sessionvo.AttemptFailed, Error: payload("private-marker")},
				} {
					tx.SaveOperationCallFact(fact)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			svc := New(evidencestore.New(), WithSessionStore(store))
			scope := summaryScope("owner")
			scope.AccessProfile = &evidencevo.AccessProfile{AccountActive: true, EffectiveSubjectID: "owner", Roles: []string{"admin"}}
			filter := svc.traceErrorCandidateFilter(evidencevo.SummaryQueryOptions{Scope: scope, ErrorKeyword: tc.keyword})
			got, err := filter(context.Background(), []iprojectionsource.TraceSummaryCandidate{{TraceID: "t", ConversationID: "c", InteractionID: "i"}, {TraceID: "t", ConversationID: "c", InteractionID: "i"}, {TraceID: "other", ConversationID: "c", InteractionID: "i"}})
			if err != nil || tc.want && (len(got) != 1 || got[0] != "t") || !tc.want && len(got) != 0 {
				t.Fatalf("candidate membership=%v want_match=%v err=%v", got, tc.want, err)
			}
		})
	}
}

func TestErrorCandidateFilterLegacyAndSharedScopesKeepCompleteScanner(t *testing.T) {
	store := sessionstore.New()
	svc := New(evidencestore.New(), WithSessionStore(authorizationLegacyStore{store}))
	scope := summaryScope("owner")
	scope.AccessProfile = &evidencevo.AccessProfile{AccountActive: true, EffectiveSubjectID: "owner", Roles: []string{"admin"}}
	filter := svc.traceErrorCandidateFilter(evidencevo.SummaryQueryOptions{Scope: scope, ErrorKeyword: "absent"})
	candidates := []iprojectionsource.TraceSummaryCandidate{{TraceID: "a"}, {TraceID: "b"}, {TraceID: "a"}}
	got, err := filter(context.Background(), candidates)
	if err != nil || !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("legacy store pruned without failure metadata: %v %v", got, err)
	}
	opts := evidencevo.SummaryQueryOptions{Scope: summaryScope("owner"), ErrorKeyword: "needle"}
	if svc.traceErrorCandidateFilter(opts) != nil {
		t.Fatal("owner scopes must preserve mixed-owner Interaction siblings")
	}
	opts.Scope.AccessProfile = &evidencevo.AccessProfile{AccountActive: true, EffectiveSubjectID: "owner", Roles: []string{"network_builder"}, ManagedKnowledgeNetworkIDs: []string{"managed"}}
	if svc.traceErrorCandidateFilter(opts) != nil || New(evidencestore.New()).traceErrorCandidateFilter(opts) != nil {
		t.Fatal("shared/no-store scope must preserve the complete scanner")
	}
}
