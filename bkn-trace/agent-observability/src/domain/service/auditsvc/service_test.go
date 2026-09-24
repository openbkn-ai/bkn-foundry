// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root.

package auditsvc

import (
	"context"
	"testing"
	"time"
)

type fakeReader struct{ called bool }

func (r *fakeReader) Query(context.Context, Query) (Page, error) { r.called = true; return Page{}, nil }

func baseQuery() Query {
	return Query{Categories: []string{"audit.admin"}, From: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC), Limit: 50}
}

func TestQueryAllowsOnlyAuthorizedCenterCategories(t *testing.T) {
	reader := &fakeReader{}
	service, err := New(reader)
	if err != nil {
		t.Fatal(err)
	}
	principal := Principal{SubjectID: "admin", AllowedCategories: map[string]bool{"audit.admin": true}}
	if _, err := service.Query(context.Background(), principal, baseQuery()); err != nil {
		t.Fatal(err)
	}
	if !reader.called {
		t.Fatal("center reader was not called")
	}
}

func TestQueryRejectsUnauthorizedSecurityAndOversizedWindow(t *testing.T) {
	service, err := New(&fakeReader{})
	if err != nil {
		t.Fatal(err)
	}
	principal := Principal{SubjectID: "admin", AllowedCategories: map[string]bool{"audit.security": true}}
	security := baseQuery()
	security.Categories = []string{"audit.security"}
	if _, err := service.Query(context.Background(), principal, security); err != ErrUnauthorized {
		t.Fatalf("security err = %v", err)
	}
	tooWide := baseQuery()
	tooWide.To = tooWide.From.Add(31 * 24 * time.Hour)
	principal.AllowedCategories = map[string]bool{"audit.admin": true}
	if _, err := service.Query(context.Background(), principal, tooWide); err == nil {
		t.Fatal("oversized query accepted")
	}
}

func TestQueryRejectsUnknownCategoryAndInvalidCursor(t *testing.T) {
	service, err := New(&fakeReader{})
	if err != nil {
		t.Fatal(err)
	}
	principal := Principal{SubjectID: "admin", AllowedCategories: map[string]bool{"audit.admin": true}}
	unknown := baseQuery()
	unknown.Categories = []string{"runtime.logs"}
	if _, err := service.Query(context.Background(), principal, unknown); err != ErrUnauthorized {
		t.Fatalf("unknown category err = %v", err)
	}
	invalidCursor := baseQuery()
	invalidCursor.Cursor = &Position{EventID: "evt-only"}
	if _, err := service.Query(context.Background(), principal, invalidCursor); err == nil {
		t.Fatal("invalid cursor accepted")
	}
}
