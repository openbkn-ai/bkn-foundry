// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/knmetrics"
)

func setAsideObjectPageBound(t *testing.T, chars int) {
	t.Helper()
	saved := interfaces.MaxObjectTypePageChars
	interfaces.MaxObjectTypePageChars = chars
	t.Cleanup(func() { interfaces.MaxObjectTypePageChars = saved })
}

func callObjectTypesRaw(t *testing.T, bkn *listingBknBackend, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	handler := handleGetObjectTypes(bkn, knmetrics.NewKnMetricsServiceWith(nil, bkn, nil),
		&mcpObjectSchemaAccessStub{permissions: map[string]interfaces.PropertyAccessLevel{"id": interfaces.PropertyAccessFull}})
	result, err := handler(context.Background(), mcpReq(args))
	if err != nil || result.IsError {
		t.Fatalf("listing failed: %v %+v", err, result)
	}
	return result
}

// A listed entry carries the start of its comment; named by id, the whole of it.
func TestGetObjectTypesListingShortensComments(t *testing.T) {
	bkn := &listingBknBackend{total: 3, comment: longRelationComment}
	listed, _ := callGetObjectTypes(t, bkn, map[string]any{"kn_id": "kn-001"})["object_types"].([]any)
	preview, _ := listed[0].(map[string]any)["comment"].(string)
	if utf8.RuneCountInString(preview) != interfaces.ObjectTypeIndexCommentRunes+1 || !strings.HasSuffix(preview, "…") {
		t.Fatalf("a listed comment must be its first %d runes and an ellipsis, got %d runes: %q",
			interfaces.ObjectTypeIndexCommentRunes, utf8.RuneCountInString(preview), preview)
	}
	read, _ := callGetObjectTypes(t, bkn, map[string]any{"kn_id": "kn-001", "ids": []any{"ot-0000"}})["object_types"].([]any)
	if whole, _ := read[0].(map[string]any)["comment"].(string); whole != longRelationComment {
		t.Fatalf("an object type named by id keeps its whole comment, got %d runes", utf8.RuneCountInString(whole))
	}
}

// A page past the size bound is cut where it still fits, and continues at the first
// entry it left out.
func TestGetObjectTypesCutsAPagePastTheSizeBound(t *testing.T) {
	setAsideObjectPageBound(t, 3000)
	bkn := &listingBknBackend{total: 120, comment: longRelationComment}
	result := callObjectTypesRaw(t, bkn, map[string]any{"kn_id": "kn-001", "response_format": "json", "offset": 10})
	if n := utf8.RuneCountInString(resultText(result)); n > 3000 {
		t.Fatalf("the cut page is %d characters, past the bound", n)
	}
	m := resultToMap(t, result)
	kept := len(m["object_types"].([]any))
	if kept == 0 || kept >= interfaces.DefaultObjectTypePageSize {
		t.Fatalf("kept %d of a page of %d, want a cut page", kept, interfaces.DefaultObjectTypePageSize)
	}
	if m["next_offset"] != float64(10+kept) || m["total_count"] != float64(120) {
		t.Fatalf("next_offset = %v, total_count = %v; want %d and 120", m["next_offset"], m["total_count"], 10+kept)
	}
	if notice, _ := m["notice"].(string); !strings.Contains(notice, "next_offset") || !strings.Contains(notice, "3000") {
		t.Fatalf("notice must say why and how to go on, got %q", notice)
	}
}

// A walk over cut pages returns every object type once.
func TestGetObjectTypesWalkOverCutPagesSkipsNothing(t *testing.T) {
	setAsideObjectPageBound(t, 3000)
	bkn := &listingBknBackend{total: 120, comment: longRelationComment}
	seen := map[string]bool{}
	offset, calls := 0, 0
	for {
		calls++
		if calls > 120 {
			t.Fatal("the walk does not end")
		}
		m := callGetObjectTypes(t, bkn, map[string]any{"kn_id": "kn-001", "offset": offset, "limit": 200})
		for _, o := range m["object_types"].([]any) {
			id, _ := o.(map[string]any)["id"].(string)
			if seen[id] {
				t.Fatalf("%s came back twice", id)
			}
			seen[id] = true
		}
		next, more := m["next_offset"].(float64)
		if !more {
			break
		}
		offset = int(next)
	}
	if len(seen) != 120 || calls < 3 {
		t.Fatalf("the walk saw %d of 120 object types in %d calls", len(seen), calls)
	}
}

// Clamped and then cut: both reasons are said.
func TestGetObjectTypesSaysBothWhenClampedAndCut(t *testing.T) {
	setAsideObjectPageBound(t, 3000)
	bkn := &listingBknBackend{total: 1000, comment: longRelationComment}
	notice, _ := callGetObjectTypes(t, bkn, map[string]any{"kn_id": "kn-001", "limit": 5000})["notice"].(string)
	if !strings.Contains(notice, "5000") || !strings.Contains(notice, "3000") {
		t.Fatalf("notice must say the limit was clamped and the page cut, got %q", notice)
	}
}

// The bound holds for the structuredContent JSON a host may read in place of the TOON text.
func TestGetObjectTypesPageBoundHoldsForTheStructuredCopy(t *testing.T) {
	setAsideObjectPageBound(t, 3000)
	bkn := &listingBknBackend{total: 120, comment: longRelationComment}
	result := callObjectTypesRaw(t, bkn, map[string]any{"kn_id": "kn-001", "response_format": "toon"})
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if n := utf8.RuneCount(raw); n > 3000 {
		t.Fatalf("structuredContent is %d characters, past the bound", n)
	}
	if n := utf8.RuneCountInString(resultText(result)); n > 3000 {
		t.Fatalf("text is %d characters, past the bound", n)
	}
}

func TestGetObjectTypesOutputSchemaAcceptsACutPage(t *testing.T) {
	setAsideObjectPageBound(t, 3000)
	_, rawSchema := loadToolSchemas(toolKeyGetObjectTypes)
	schema, err := compileExecutableSchema(rawSchema)
	if err != nil {
		t.Fatalf("compile get_object_types output schema: %v", err)
	}
	m := callGetObjectTypes(t, &listingBknBackend{total: 300, comment: longRelationComment},
		map[string]any{"kn_id": "kn-001", "limit": 200})
	if err := schema.Validate(m); err != nil {
		t.Fatalf("a cut page does not match the advertised output schema: %v", err)
	}
}
