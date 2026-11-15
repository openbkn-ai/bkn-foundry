// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func objectJSONRunes(r *ObjectTypesResp) (int, error) {
	b, err := json.Marshal(r)
	return utf8.RuneCount(b), err
}

func objectPageOf(n int) *ObjectTypesResp {
	resp := &ObjectTypesResp{KnID: "kn"}
	for i := 0; i < n; i++ {
		resp.ObjectTypes = append(resp.ObjectTypes, &ObjectType{ID: strings.Repeat("o", 90)})
	}
	return resp
}

func TestShortenObjectTypeCommentsKeepsTheStart(t *testing.T) {
	long := strings.Repeat("订单是客户下的购买记录。", 30)
	ots := []*ObjectType{{ID: "a", Comment: long}, {ID: "b", Comment: "  short  "}, {ID: "c"}, nil}
	ShortenObjectTypeComments(ots)
	if got := utf8.RuneCountInString(ots[0].Comment); got != ObjectTypeIndexCommentRunes+1 ||
		!strings.HasSuffix(ots[0].Comment, "…") || !strings.HasPrefix(long, strings.TrimSuffix(ots[0].Comment, "…")) {
		t.Fatalf("preview is %d runes: %q", got, ots[0].Comment)
	}
	if ots[1].Comment != "short" || ots[2].Comment != "" {
		t.Fatalf("a short comment stays whole: %q, %q", ots[1].Comment, ots[2].Comment)
	}
}

func TestFitObjectTypePageLeavesAPageThatFits(t *testing.T) {
	resp := objectPageOf(10)
	next := 10
	resp.NextOffset = &next
	kept, err := FitObjectTypePage(resp, 0, 1<<20, func(int) string { return "cut" }, objectJSONRunes)
	if err != nil || kept != 10 || len(resp.ObjectTypes) != 10 || *resp.NextOffset != 10 || resp.Notice != "" {
		t.Fatalf("kept %d, next %v, notice %q, err %v", kept, resp.NextOffset, resp.Notice, err)
	}
}

// The cut continues at the first entry it left out -- not at the offset computed for the
// whole page, which points past it.
func TestFitObjectTypePageContinuesAtTheFirstEntryLeftOut(t *testing.T) {
	resp := objectPageOf(50)
	resp.Notice = "clamped"
	whole := NextObjectTypeOffset(100, 50, 1000)
	resp.NextOffset = whole
	limit := 2000
	kept, err := FitObjectTypePage(resp, 100, limit, func(int) string { return "cut" }, objectJSONRunes)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := objectJSONRunes(resp); n > limit {
		t.Fatalf("cut page is %d characters, over %d", n, limit)
	}
	more := objectPageOf(kept + 1)
	more.Notice, more.NextOffset = resp.Notice, resp.NextOffset
	if n, _ := objectJSONRunes(more); n <= limit {
		t.Fatalf("kept %d, but %d fit as well", kept, kept+1)
	}
	if *resp.NextOffset != 100+kept || *resp.NextOffset == *whole || resp.Notice != "clamped cut" {
		t.Fatalf("next = %d (whole page: %d), notice %q; want %d and both notices", *resp.NextOffset, *whole, resp.Notice, 100+kept)
	}
}

// An entry too big alone still comes back, so a walk moves past it.
func TestFitObjectTypePageAlwaysKeepsOne(t *testing.T) {
	resp := objectPageOf(3)
	kept, err := FitObjectTypePage(resp, 7, 10, func(int) string { return "cut" }, objectJSONRunes)
	if err != nil || kept != 1 || len(resp.ObjectTypes) != 1 || *resp.NextOffset != 8 {
		t.Fatalf("kept %d, next %v, err %v", kept, resp.NextOffset, err)
	}
}
