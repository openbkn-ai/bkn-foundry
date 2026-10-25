// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package httphandler

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
)

func TestTraceSpanCursorBindsEveryAuthorizationInputAndBounds(t *testing.T) {
	key := []byte("cursor-key")
	scope := spanHandlerScope()
	now := time.Now()
	value, err := encodeTraceSpanCursor(key, "trace", "span-100", scope, now)
	if err != nil {
		t.Fatal(err)
	}
	if after, err := decodeTraceSpanCursor(key, value, "trace", scope, now); err != nil || after != "span-100" {
		t.Fatalf("round trip: %q %v", after, err)
	}
	for _, tc := range []struct {
		name   string
		change func(*evidencevo.QueryScope)
	}{
		{"account_type", func(s *evidencevo.QueryScope) { s.AccountType = "service" }},
		{"role", func(s *evidencevo.QueryScope) { s.AccessProfile.Roles = []string{"super_admin"} }},
		{"subject", func(s *evidencevo.QueryScope) { s.AccessProfile.EffectiveSubjectID = "other" }},
		{"application", func(s *evidencevo.QueryScope) { s.AccessProfile.ApplicationPrincipalID = "other-app" }},
		{"delegation", func(s *evidencevo.QueryScope) { s.AccessProfile.DelegationID = "changed" }},
		{"permissions", func(s *evidencevo.QueryScope) {
			s.AccessProfile.Permissions = []evidencevo.Permission{{ResourceType: "*", ResourceID: "*", Operations: []string{"read"}}}
		}},
		{"active", func(s *evidencevo.QueryScope) { s.AccessProfile.AccountActive = false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := spanHandlerScope()
			tc.change(&changed)
			if _, err := decodeTraceSpanCursor(key, value, "trace", changed, now); err == nil {
				t.Fatal("changed authorization reused cursor")
			}
		})
	}
	if _, err := decodeTraceSpanCursor([]byte("another-key"), value, "trace", scope, now); err == nil {
		t.Fatal("wrong signing key accepted")
	}
	if _, err := decodeTraceSpanCursor(key, value, "trace", scope, now.Add(15*time.Minute)); err == nil {
		t.Fatal("expired cursor accepted")
	}
	for _, after := range []string{"", strings.Repeat("a", 129), "bad\nkey"} {
		if _, err := encodeTraceSpanCursor(key, "trace", after, scope, now); err == nil {
			t.Fatal("unbounded cursor after accepted")
		}
	}
	a, b := newTraceSpanCursorKey(), newTraceSpanCursorKey()
	if len(a) != 32 || !bytes.Equal(a, b) {
		t.Fatal("default process signing key is unstable")
	}
	a[0] ^= 1
	if bytes.Equal(a, newTraceSpanCursorKey()) {
		t.Fatal("caller mutated shared default signing key")
	}
}
