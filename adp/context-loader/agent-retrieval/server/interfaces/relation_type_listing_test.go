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

func TestRelationTypeIndexKeepsTheStartOfALongComment(t *testing.T) {
	long := strings.Repeat("订单属于客户。", 40)
	short := "订单属于客户"
	rts := []*RelationType{{ID: "a", Comment: long}, {ID: "b", Comment: "  " + short + "  "}, {ID: "c"}}
	ReduceToRelationTypeIndex(rts)

	if got := utf8.RuneCountInString(rts[0].Comment); got != RelationTypeIndexCommentRunes+1 {
		t.Fatalf("preview is %d runes, want %d and the ellipsis", got, RelationTypeIndexCommentRunes)
	}
	if !strings.HasSuffix(rts[0].Comment, "…") || !strings.HasPrefix(long, strings.TrimSuffix(rts[0].Comment, "…")) {
		t.Fatalf("preview must be the start of the comment, marked as cut: %q", rts[0].Comment)
	}
	if rts[1].Comment != short || rts[2].Comment != "" {
		t.Fatalf("a short comment stays whole: %q, %q", rts[1].Comment, rts[2].Comment)
	}
}

func jsonRunes(r *RelationTypesResp) (int, error) {
	b, err := json.Marshal(r)
	return utf8.RuneCount(b), err
}

func pageOf(n int) *RelationTypesResp {
	resp := &RelationTypesResp{KnID: "kn"}
	for i := 0; i < n; i++ {
		resp.RelationTypes = append(resp.RelationTypes, &RelationType{ID: strings.Repeat("r", 90)})
	}
	return resp
}

func TestFitRelationTypePageLeavesAPageThatFits(t *testing.T) {
	resp := pageOf(10)
	next := 10
	resp.NextOffset = &next
	kept, err := FitRelationTypePage(resp, 0, 1<<20, func(int) string { return "cut" }, jsonRunes)
	if err != nil || kept != 10 || len(resp.RelationTypes) != 10 || *resp.NextOffset != 10 || resp.Notice != "" {
		t.Fatalf("kept %d, next %v, notice %q, err %v", kept, resp.NextOffset, resp.Notice, err)
	}
}

func TestFitRelationTypePageKeepsTheLongestFittingStart(t *testing.T) {
	resp := pageOf(50)
	resp.Notice = "clamped"
	limit := 2000
	kept, err := FitRelationTypePage(resp, 100, limit, func(k int) string { return "cut" }, jsonRunes)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := jsonRunes(resp); n > limit {
		t.Fatalf("cut page is %d characters, over %d", n, limit)
	}
	// One more entry would not have fitted.
	more := pageOf(kept + 1)
	more.Notice, more.NextOffset = resp.Notice, resp.NextOffset
	if n, _ := jsonRunes(more); n <= limit {
		t.Fatalf("kept %d, but %d fit as well", kept, kept+1)
	}
	if *resp.NextOffset != 100+kept || resp.Notice != "clamped cut" {
		t.Fatalf("next = %d, notice %q; want %d and both notices", *resp.NextOffset, resp.Notice, 100+kept)
	}
}

// An entry too big alone still comes back, so a walk moves past it.
func TestFitRelationTypePageAlwaysKeepsOne(t *testing.T) {
	resp := pageOf(3)
	kept, err := FitRelationTypePage(resp, 7, 10, func(int) string { return "cut" }, jsonRunes)
	if err != nil || kept != 1 || len(resp.RelationTypes) != 1 || *resp.NextOffset != 8 {
		t.Fatalf("kept %d, next %v, err %v", kept, resp.NextOffset, err)
	}
}
