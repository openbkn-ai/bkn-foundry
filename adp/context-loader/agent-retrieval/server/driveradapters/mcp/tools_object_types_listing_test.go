// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/knmetrics"
)

// listingBknBackend answers a listing and records the window it was asked for,
// which is the part a caller's paging inputs have to survive into.
type listingBknBackend struct {
	stubMetricBknBackend
	total      int64
	gotOffset  int
	gotLimit   int
	listCalls  int
	byIDCalls  int
	namedByIDs []string
}

func (s *listingBknBackend) ListObjectTypes(_ context.Context, _ string, offset, limit int) (*interfaces.ObjectTypePage, error) {
	s.listCalls++
	s.gotOffset, s.gotLimit = offset, limit
	entries := make([]*interfaces.ObjectType, 0, limit)
	for i := offset; i < offset+limit && int64(i) < s.total; i++ {
		entries = append(entries, &interfaces.ObjectType{ID: fmt.Sprintf("ot-%04d", i),
			DataSource: &interfaces.ResourceInfo{Type: "resource", ID: "res"}})
	}
	return &interfaces.ObjectTypePage{Entries: entries, TotalCount: s.total}, nil
}

func (s *listingBknBackend) GetObjectTypeDetail(_ context.Context, _ string, ids []string, _ bool) ([]*interfaces.ObjectType, error) {
	s.byIDCalls++
	s.namedByIDs = ids
	out := make([]*interfaces.ObjectType, 0, len(ids))
	for _, id := range ids {
		out = append(out, &interfaces.ObjectType{ID: id, DataSource: &interfaces.ResourceInfo{Type: "resource", ID: "res"}})
	}
	return out, nil
}

func callGetObjectTypes(t *testing.T, bkn *listingBknBackend, args map[string]any) map[string]any {
	t.Helper()
	handler := handleGetObjectTypes(bkn, knmetrics.NewKnMetricsServiceWith(nil, bkn, nil), &mcpObjectSchemaAccessStub{})
	args["response_format"] = "json"
	result, err := handler(context.Background(), mcpReq(args))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %+v", result)
	}
	return resultToMap(t, result)
}

// The gap #1889 reported: past get_kn_detail's caps the concept arrays are
// withheld, and nothing on this surface could produce an object type id. Without
// ids, get_object_types now lists instead of refusing.
func TestGetObjectTypesWithoutIDsListsAPage(t *testing.T) {
	bkn := &listingBknBackend{total: 1000}
	m := callGetObjectTypes(t, bkn, map[string]any{"kn_id": "kn-001"})

	if bkn.listCalls != 1 || bkn.byIDCalls != 0 {
		t.Fatalf("list=%d byID=%d, want the listing path alone", bkn.listCalls, bkn.byIDCalls)
	}
	if bkn.gotOffset != 0 || bkn.gotLimit != interfaces.DefaultObjectTypePageSize {
		t.Fatalf("window = offset %d limit %d, want 0/%d", bkn.gotOffset, bkn.gotLimit, interfaces.DefaultObjectTypePageSize)
	}
	got, _ := m["object_types"].([]any)
	if len(got) != interfaces.DefaultObjectTypePageSize {
		t.Fatalf("page carried %d object types, want %d", len(got), interfaces.DefaultObjectTypePageSize)
	}
	if m["total_count"] != float64(1000) {
		t.Fatalf("total_count = %v, want 1000", m["total_count"])
	}
	if m["next_offset"] != float64(interfaces.DefaultObjectTypePageSize) {
		t.Fatalf("next_offset = %v, want %d", m["next_offset"], interfaces.DefaultObjectTypePageSize)
	}
}

// The walk has to end. A caller that derives offset+len would loop forever on a
// total it cannot see move, which is why the server says when to stop.
func TestGetObjectTypesLastPageHasNoNextOffset(t *testing.T) {
	bkn := &listingBknBackend{total: 25}
	m := callGetObjectTypes(t, bkn, map[string]any{"kn_id": "kn-001", "offset": 20, "limit": 20})

	got, _ := m["object_types"].([]any)
	if len(got) != 5 {
		t.Fatalf("last page carried %d, want 5", len(got))
	}
	if next, present := m["next_offset"]; present {
		t.Fatalf("the walk is over; next_offset should be absent, got %v", next)
	}
	if m["total_count"] != float64(25) {
		t.Fatalf("total_count = %v, want 25", m["total_count"])
	}
}

// A page is bounded whatever the caller asks for: every entry is a full
// definition and every page costs one property-plan fan-out.
func TestGetObjectTypesClampsThePageWindow(t *testing.T) {
	bkn := &listingBknBackend{total: 5000}
	m := callGetObjectTypes(t, bkn, map[string]any{"kn_id": "kn-001", "limit": 9999, "offset": -5})

	if bkn.gotLimit != interfaces.MaxObjectTypePageSize {
		t.Fatalf("limit = %d, want it clamped to %d", bkn.gotLimit, interfaces.MaxObjectTypePageSize)
	}
	if bkn.gotOffset != 0 {
		t.Fatalf("offset = %d, want a negative offset read as the start", bkn.gotOffset)
	}
	// A page shorter than the one asked for, with nothing said about it, reads as a
	// network that ran out.
	notice, _ := m["notice"].(string)
	if !strings.Contains(notice, "9999") || !strings.Contains(notice, fmt.Sprint(interfaces.MaxObjectTypePageSize)) {
		t.Fatalf("a cut-down limit must be said out loud, got %q", notice)
	}
}

// The notice is for a request that was not honoured, so an ordinary page must not
// carry one -- a notice on every call is not a notice.
func TestGetObjectTypesPageWithinTheLimitSaysNothing(t *testing.T) {
	bkn := &listingBknBackend{total: 5000}
	for _, args := range []map[string]any{
		{"kn_id": "kn-001"},
		{"kn_id": "kn-001", "limit": interfaces.MaxObjectTypePageSize},
		{"kn_id": "kn-001", "limit": 5, "offset": 10},
	} {
		if got, present := callGetObjectTypes(t, bkn, args)["notice"]; present {
			t.Fatalf("%v: nothing was narrowed, got notice %v", args, got)
		}
	}
}

// Naming ids is the precise request and stays the primary path; the paging
// inputs are meaningless there and must not divert it.
func TestGetObjectTypesWithIDsIgnoresPaging(t *testing.T) {
	bkn := &listingBknBackend{total: 1000}
	m := callGetObjectTypes(t, bkn, map[string]any{
		"kn_id": "kn-001", "ids": []any{"ot-a", "ot-b"}, "offset": 400, "limit": 100})

	if bkn.listCalls != 0 || bkn.byIDCalls != 1 {
		t.Fatalf("list=%d byID=%d, want the by-id path alone", bkn.listCalls, bkn.byIDCalls)
	}
	if len(bkn.namedByIDs) != 2 || bkn.namedByIDs[0] != "ot-a" {
		t.Fatalf("ids reached the backend as %v", bkn.namedByIDs)
	}
	for _, field := range []string{"total_count", "next_offset"} {
		if got, present := m[field]; present {
			t.Fatalf("%s belongs to a listing, not a by-id read, got %v", field, got)
		}
	}
}
