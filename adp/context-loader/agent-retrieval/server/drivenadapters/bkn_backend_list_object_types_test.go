// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package drivenadapters

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// listRoute answers the two reads ListObjectTypes makes, the way bkn-backend
// does: the list endpoint returns summaries -- its SELECT carries no property
// columns and its handler does not honour include_detail -- and the by-id
// endpoint returns the whole object type.
type listRoute struct {
	listCalls  int
	listQuery  url.Values
	detailCall string
	listBody   string
	detailBody string
}

func (s *listRoute) serve(_ context.Context, target string, query url.Values, _ map[string]string) (int, []byte, error) {
	if strings.HasSuffix(target, "/object-types") {
		s.listCalls++
		s.listQuery = query
		return 200, []byte(s.listBody), nil
	}
	s.detailCall = target
	return 200, []byte(s.detailBody), nil
}

// A listed page is an index: one read, name order, and the ids a caller needs to
// name next. It is deliberately not a page of definitions -- those run about 60KB
// an object type on the network of #1877, so twenty would be over a megabyte and
// each would cost a property-plan read to produce (#1889 review).
func TestListObjectTypesReadsTheIndexOnce(t *testing.T) {
	client, mockHTTP, ctrl := newKNDetailTestClient(t)
	defer ctrl.Finish()

	route := &listRoute{
		listBody: `{"entries":[{"id":"ot_b","name":"beta"},{"id":"ot_a","name":"alpha"}],"total_count":42}`,
	}
	mockHTTP.EXPECT().GetNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(route.serve).Times(1)

	page, err := client.ListObjectTypes(context.Background(), "kn1", 20, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if route.listCalls != 1 || route.detailCall != "" {
		t.Fatalf("expected the index read alone, got list=%d detail=%q", route.listCalls, route.detailCall)
	}
	for key, want := range map[string]string{"offset": "20", "limit": "2", "sort": "name", "direction": "asc"} {
		if got := route.listQuery.Get(key); got != want {
			t.Fatalf("%s = %q, want %q -- a walk needs a repeatable order", key, got, want)
		}
	}
	if page.TotalCount != 42 || page.Scanned != 2 || len(page.Entries) != 2 {
		t.Fatalf("total=%d scanned=%d entries=%d, want 42/2/2", page.TotalCount, page.Scanned, len(page.Entries))
	}
	// The order the index gave, which is the order the walk depends on.
	if page.Entries[0].ID != "ot_b" || page.Entries[1].ID != "ot_a" {
		t.Fatalf("index order lost: %s, %s", page.Entries[0].ID, page.Entries[1].ID)
	}
}

// An empty window costs one read, not two, and ends the walk.
func TestListObjectTypesPastTheEndReadsOnce(t *testing.T) {
	client, mockHTTP, ctrl := newKNDetailTestClient(t)
	defer ctrl.Finish()

	route := &listRoute{listBody: `{"entries":[],"total_count":42}`}
	mockHTTP.EXPECT().GetNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(route.serve).Times(1)

	page, err := client.ListObjectTypes(context.Background(), "kn1", 100, 20)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if page.Scanned != 0 || len(page.Entries) != 0 || page.TotalCount != 42 {
		t.Fatalf("scanned=%d entries=%d total=%d", page.Scanned, len(page.Entries), page.TotalCount)
	}
	if interfaces.NextObjectTypeOffset(100, page.Scanned, page.TotalCount) != nil {
		t.Fatal("a window that scanned nothing must end the walk")
	}
}

// The walk advances by what the index covered, which is also what the page
// carries today. Keeping the two apart is what lets the page shrink later --
// a filter, a projection -- without the walk quietly skipping or repeating.
func TestWalkAdvancesByWhatWasScanned(t *testing.T) {
	client, mockHTTP, ctrl := newKNDetailTestClient(t)
	defer ctrl.Finish()

	route := &listRoute{listBody: `{"entries":[{"id":"ot_a"},{"id":"ot_b"},{"id":"ot_c"}],"total_count":42}`}
	mockHTTP.EXPECT().GetNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(route.serve).Times(1)

	page, err := client.ListObjectTypes(context.Background(), "kn1", 0, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if page.Scanned != 3 {
		t.Fatalf("scanned=%d, want 3", page.Scanned)
	}
	next := interfaces.NextObjectTypeOffset(0, page.Scanned, page.TotalCount)
	if next == nil || *next != 3 {
		t.Fatalf("next offset = %v, want 3 -- the window covered three rows", next)
	}
}
