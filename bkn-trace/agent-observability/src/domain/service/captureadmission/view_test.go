// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package captureadmission

import (
	"testing"
	"time"
)

func TestViewUnknownAndTransitions(t *testing.T) {
	v := New(0, "disabled")
	if v.Known() || v.AllowsNewRecords() || v.Disabled() {
		t.Fatal("unknown view must not be treated as intentional disabled")
	}
	v.Update(1, "enabled")
	if !v.Known() || !v.AllowsNewRecords() || v.Disabled() {
		t.Fatal("enabled transition not reflected")
	}
	v.Update(2, "disabling")
	if !v.AllowsNewRecords() {
		t.Fatal("invalid intermediate state must not rewrite verified admission")
	}
	v.Update(2, "disabled")
	if !v.Known() || v.AllowsNewRecords() || !v.Disabled() {
		t.Fatal("disabled transition not reflected")
	}
}

func TestViewExpiresWithoutRefreshingFromBusinessPath(t *testing.T) {
	v := New(1, "enabled")
	v.updated = time.Now().Add(-31 * time.Second)
	if v.Known() || v.AllowsNewRecords() {
		t.Fatal("stale view must fail closed")
	}
}

func TestViewSameRevisionUpdateRefreshesVerifiedState(t *testing.T) {
	v := New(1, "enabled")
	v.updated = time.Now().Add(-31 * time.Second)
	if v.Known() {
		t.Fatal("stale view should expire before controller refresh")
	}
	v.Update(1, "enabled")
	if !v.Known() || !v.AllowsNewRecords() {
		t.Fatal("same-revision verified controller refresh did not restore freshness")
	}
}

func TestStableViewRemainsKnownAfterFreshnessWindow(t *testing.T) {
	v := NewStable(1, "enabled")
	v.updated = time.Now().Add(-time.Minute)
	if !v.Known() || !v.AllowsNewRecords() {
		t.Fatal("stable process-local admission view expired")
	}
}
