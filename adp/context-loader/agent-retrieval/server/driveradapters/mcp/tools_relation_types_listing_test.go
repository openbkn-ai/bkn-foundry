// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// relationListingBackend answers relation type listings and reads, and records what
// it was asked for: the window, the object types and the branch are what a caller's
// inputs have to survive into.
type relationListingBackend struct {
	stubMetricBknBackend
	total          int64
	gotBranch      string
	gotObjectTypes []string
	gotOffset      int
	gotLimit       int
	mainReads      int
	branchReads    int
	// comment, when set, is every relation type's comment in place of the short one.
	comment string
}

func (b *relationListingBackend) commentOr(fallback string) string {
	if b.comment != "" {
		return b.comment
	}
	return fallback
}

func (b *relationListingBackend) ListRelationTypesBranch(_ context.Context, _, branch string, objectTypeIDs []string,
	offset, limit int) (*interfaces.RelationTypePage, error) {
	b.gotBranch, b.gotObjectTypes, b.gotOffset, b.gotLimit = branch, objectTypeIDs, offset, limit
	entries := []*interfaces.RelationType{}
	for i := offset; i < offset+limit && int64(i) < b.total; i++ {
		// As bkn-backend lists them: with mapping rules and endpoint details, which
		// an index entry leaves out.
		entries = append(entries, &interfaces.RelationType{
			ID: fmt.Sprintf("rt-%04d", i), Name: "属于", Comment: b.commentOr("订单属于客户"), ModuleType: "relation_type",
			SourceObjectTypeID: "ot-order", TargetObjectTypeID: "ot-customer", Type: "direct",
			MappingRules:     []any{map[string]any{"source": "customer_id", "target": "id"}},
			SourceObjectType: map[string]any{"name": "订单"}, Cardinality: "many_to_one",
		})
	}
	return &interfaces.RelationTypePage{Entries: entries, TotalCount: b.total, Scanned: len(entries)}, nil
}

func (b *relationListingBackend) GetRelationTypeDetail(_ context.Context, _ string, ids []string, _ bool) ([]*interfaces.RelationType, error) {
	b.mainReads++
	return relationsNamed(ids, b.comment), nil
}

func (b *relationListingBackend) GetRelationTypeDetailBranch(_ context.Context, _, branch string, ids []string, _ bool) ([]*interfaces.RelationType, error) {
	b.branchReads++
	b.gotBranch = branch
	return relationsNamed(ids, b.comment), nil
}

func relationsNamed(ids []string, comment string) []*interfaces.RelationType {
	out := make([]*interfaces.RelationType, 0, len(ids))
	for _, id := range ids {
		out = append(out, &interfaces.RelationType{ID: id, Name: id, Comment: comment,
			MappingRules: []any{map[string]any{"source": "a", "target": "b"}}})
	}
	return out
}

func callRelationTypes(t *testing.T, bkn interfaces.BknBackendAccess, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	args["response_format"] = "json"
	result, err := handleGetRelationTypes(bkn)(context.Background(), mcpReq(args))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return result
}

// The gap: get_kn_detail's navigation shell carries relation_types as null, and off
// main search_schema cannot recall them, so a caller without ids had no way to the
// relation types at all. It is now answered with a page instead of refused.
func TestGetRelationTypesWithoutIDsListsAPage(t *testing.T) {
	bkn := &relationListingBackend{total: 120}
	result := callRelationTypes(t, bkn, map[string]any{"kn_id": "kn-001"})
	if result.IsError {
		t.Fatalf("listing was refused: %s", resultText(result))
	}
	if bkn.gotOffset != 0 || bkn.gotLimit != interfaces.DefaultObjectTypePageSize || bkn.gotBranch != "main" {
		t.Fatalf("window = %d/%d on %q, want 0/%d on main", bkn.gotOffset, bkn.gotLimit, bkn.gotBranch, interfaces.DefaultObjectTypePageSize)
	}
	m := resultToMap(t, result)
	relations, _ := m["relation_types"].([]any)
	if len(relations) != interfaces.DefaultObjectTypePageSize {
		t.Fatalf("got %d relation types, want a page of %d", len(relations), interfaces.DefaultObjectTypePageSize)
	}
	if m["total_count"] != float64(120) || m["next_offset"] != float64(interfaces.DefaultObjectTypePageSize) {
		t.Fatalf("total_count = %v, next_offset = %v", m["total_count"], m["next_offset"])
	}
	entry, _ := relations[0].(map[string]any)
	for _, kept := range []string{"id", "name", "comment", "source_object_type_id", "target_object_type_id", "type"} {
		if entry[kept] == nil || entry[kept] == "" {
			t.Fatalf("an index entry must keep %s, got %v", kept, entry)
		}
	}
	for _, dropped := range []string{"mapping_rules", "source_object_type", "module_type", "cardinality"} {
		if _, present := entry[dropped]; present {
			t.Fatalf("an index entry must leave out %s, got %v", dropped, entry)
		}
	}
	if bkn.mainReads != 0 || bkn.branchReads != 0 {
		t.Fatalf("a listing must not read definitions, got %d/%d reads", bkn.mainReads, bkn.branchReads)
	}
}

// What a caller who has picked object types wants: the relation types between them,
// without walking the network's list in name order.
func TestGetRelationTypesListsTheRelationsOfObjectTypes(t *testing.T) {
	bkn := &relationListingBackend{total: 3}
	result := callRelationTypes(t, bkn, map[string]any{
		"kn_id": "kn-001", "object_type_ids": []any{"ot-order", "ot-customer"}, "branch": "release",
	})
	if result.IsError {
		t.Fatalf("listing was refused: %s", resultText(result))
	}
	if !reflect.DeepEqual(bkn.gotObjectTypes, []string{"ot-order", "ot-customer"}) || bkn.gotBranch != "release" {
		t.Fatalf("asked for %v on %q, want both object types on release", bkn.gotObjectTypes, bkn.gotBranch)
	}
	m := resultToMap(t, result)
	if _, present := m["next_offset"]; present {
		t.Fatalf("a listing that fits one page has no next_offset, got %v", m["next_offset"])
	}
}

func TestGetRelationTypesWalkEnds(t *testing.T) {
	bkn := &relationListingBackend{total: 120}
	m := resultToMap(t, callRelationTypes(t, bkn, map[string]any{"kn_id": "kn-001", "offset": 100, "limit": 50}))
	if relations, _ := m["relation_types"].([]any); len(relations) != 20 {
		t.Fatalf("got %d relation types on the last page, want 20", len(relations))
	}
	if _, present := m["next_offset"]; present {
		t.Fatalf("the last page must not carry next_offset, got %v", m["next_offset"])
	}
}

// A limit past the maximum is answered with the maximum, and says so: a short page
// with nothing said reads as a network that ran out.
func TestGetRelationTypesSaysWhenItCutTheLimit(t *testing.T) {
	bkn := &relationListingBackend{total: 1000}
	m := resultToMap(t, callRelationTypes(t, bkn, map[string]any{"kn_id": "kn-001", "limit": 5000}))
	if bkn.gotLimit != interfaces.MaxObjectTypePageSize {
		t.Fatalf("limit = %d, want the maximum %d", bkn.gotLimit, interfaces.MaxObjectTypePageSize)
	}
	notice, _ := m["notice"].(string)
	if !strings.Contains(notice, "next_offset") || !strings.Contains(notice, "关系类") {
		t.Fatalf("notice must say the limit was cut and how to go on, got %q", notice)
	}
}

// Named ids are unchanged on main: the same reader, the full definition, and the
// listing inputs ignored.
func TestGetRelationTypesByIDsReadsDefinitions(t *testing.T) {
	bkn := &relationListingBackend{total: 10}
	m := resultToMap(t, callRelationTypes(t, bkn, map[string]any{
		"kn_id": "kn-001", "ids": []any{"rt-a", "rt-b"}, "object_type_ids": []any{"ot-x"}, "limit": 5,
	}))
	if bkn.mainReads != 1 || bkn.branchReads != 0 || bkn.gotLimit != 0 {
		t.Fatalf("main reads = %d, branch reads = %d, listed with limit %d; want one main read and no listing",
			bkn.mainReads, bkn.branchReads, bkn.gotLimit)
	}
	relations, _ := m["relation_types"].([]any)
	if len(relations) != 2 {
		t.Fatalf("got %d relation types, want 2", len(relations))
	}
	if entry, _ := relations[0].(map[string]any); entry["mapping_rules"] == nil {
		t.Fatalf("a definition read by id keeps its mapping rules, got %v", entry)
	}
	for _, field := range []string{"total_count", "next_offset", "notice"} {
		if _, present := m[field]; present {
			t.Fatalf("%s belongs to a listing only, got %v", field, m[field])
		}
	}
}

func TestGetRelationTypesByIDsReadsTheRequestedBranch(t *testing.T) {
	bkn := &relationListingBackend{}
	result := callRelationTypes(t, bkn, map[string]any{"kn_id": "kn-001", "ids": []any{"rt-a"}, "branch": "release"})
	if result.IsError || bkn.branchReads != 1 || bkn.mainReads != 0 || bkn.gotBranch != "release" {
		t.Fatalf("branch reads = %d, main reads = %d, branch %q: %s", bkn.branchReads, bkn.mainReads, bkn.gotBranch, resultText(result))
	}
}

// A backend without the reader cannot list, and says so rather than answering empty.
func TestGetRelationTypesListingNeedsTheReader(t *testing.T) {
	result := callRelationTypes(t, &stubMetricBknBackend{}, map[string]any{"kn_id": "kn-001"})
	if !result.IsError {
		t.Fatalf("a listing without a reader must fail, got %s", resultText(result))
	}
}

func TestGetRelationTypesOutputSchemaAcceptsAListing(t *testing.T) {
	_, rawSchema := loadToolSchemas(toolKeyGetRelationTypes)
	schema, err := compileExecutableSchema(rawSchema)
	if err != nil {
		t.Fatalf("compile get_relation_types output schema: %v", err)
	}
	m := resultToMap(t, callRelationTypes(t, &relationListingBackend{total: 300}, map[string]any{"kn_id": "kn-001", "limit": 500}))
	if err := schema.Validate(m); err != nil {
		t.Fatalf("listing does not match the advertised output schema: %v", err)
	}
}

// longRelationComment is about the size of the comments a real network writes: a
// semantics paragraph and a modeling one, around 400 characters.
var longRelationComment = "**语义**：在「出线球队资格」事实中，字段 tournament_id 引用「世界杯赛事」的主键，表示该记录隶属于哪一届世界杯赛事。" +
	strings.Repeat("\n\n**建模用途**：多表联接、Agent 问答与统计时的 join 路径，按届次聚合出线球队。", 5)

// A listed entry carries the start of its comment: enough to choose by. Named by
// id, the same relation type comes back with the whole of it.
func TestGetRelationTypesListingShortensComments(t *testing.T) {
	bkn := &relationListingBackend{total: 3, comment: longRelationComment}
	listed, _ := resultToMap(t, callRelationTypes(t, bkn, map[string]any{"kn_id": "kn-001"}))["relation_types"].([]any)
	entry, _ := listed[0].(map[string]any)
	preview, _ := entry["comment"].(string)
	if utf8.RuneCountInString(preview) != interfaces.RelationTypeIndexCommentRunes+1 || !strings.HasSuffix(preview, "…") {
		t.Fatalf("a listed comment must be its first %d runes and an ellipsis, got %d runes: %q",
			interfaces.RelationTypeIndexCommentRunes, utf8.RuneCountInString(preview), preview)
	}
	read, _ := resultToMap(t, callRelationTypes(t, bkn, map[string]any{"kn_id": "kn-001", "ids": []any{"rt-0000"}}))["relation_types"].([]any)
	if whole, _ := read[0].(map[string]any)["comment"].(string); whole != longRelationComment {
		t.Fatalf("a relation type named by id keeps its whole comment, got %d runes", utf8.RuneCountInString(whole))
	}
}

func setAsideRelationPageBound(t *testing.T, chars int) {
	t.Helper()
	saved := interfaces.MaxRelationTypePageChars
	interfaces.MaxRelationTypePageChars = chars
	t.Cleanup(func() { interfaces.MaxRelationTypePageChars = saved })
}

// A page past the size bound is cut where it still fits, and continues there: the
// caller walks on with next_offset and nothing is skipped.
func TestGetRelationTypesCutsAPagePastTheSizeBound(t *testing.T) {
	setAsideRelationPageBound(t, 6000)
	bkn := &relationListingBackend{total: 120, comment: longRelationComment}
	result := callRelationTypes(t, bkn, map[string]any{"kn_id": "kn-001"})
	if n := utf8.RuneCountInString(resultText(result)); n > 6000 {
		t.Fatalf("the cut page is %d characters, past the bound", n)
	}
	m := resultToMap(t, result)
	relations, _ := m["relation_types"].([]any)
	kept := len(relations)
	if kept == 0 || kept >= interfaces.DefaultObjectTypePageSize {
		t.Fatalf("kept %d of a page of %d, want a cut page", kept, interfaces.DefaultObjectTypePageSize)
	}
	if m["next_offset"] != float64(kept) || m["total_count"] != float64(120) {
		t.Fatalf("next_offset = %v, total_count = %v; want %d and 120", m["next_offset"], m["total_count"], kept)
	}
	notice, _ := m["notice"].(string)
	if !strings.Contains(notice, "next_offset") || !strings.Contains(notice, "object_type_ids") {
		t.Fatalf("notice must say how to go on, got %q", notice)
	}
}

func TestGetRelationTypesWalkOverCutPagesSkipsNothing(t *testing.T) {
	setAsideRelationPageBound(t, 6000)
	bkn := &relationListingBackend{total: 120, comment: longRelationComment}
	seen := map[string]bool{}
	offset, calls := 0, 0
	for {
		calls++
		if calls > 120 {
			t.Fatal("the walk does not end")
		}
		m := resultToMap(t, callRelationTypes(t, bkn, map[string]any{"kn_id": "kn-001", "offset": offset, "limit": 200}))
		relations, _ := m["relation_types"].([]any)
		for _, r := range relations {
			id, _ := r.(map[string]any)["id"].(string)
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
	if len(seen) != 120 {
		t.Fatalf("the walk saw %d of 120 relation types", len(seen))
	}
}

// Clamped and then cut: both reasons are said.
func TestGetRelationTypesSaysBothWhenClampedAndCut(t *testing.T) {
	setAsideRelationPageBound(t, 6000)
	bkn := &relationListingBackend{total: 1000, comment: longRelationComment}
	notice, _ := resultToMap(t, callRelationTypes(t, bkn, map[string]any{"kn_id": "kn-001", "limit": 5000}))["notice"].(string)
	if !strings.Contains(notice, "limit=5000") || !strings.Contains(notice, "6000") {
		t.Fatalf("notice must say the limit was clamped and the page cut, got %q", notice)
	}
}

func TestGetRelationTypesOutputSchemaAcceptsACutPage(t *testing.T) {
	setAsideRelationPageBound(t, 6000)
	_, rawSchema := loadToolSchemas(toolKeyGetRelationTypes)
	schema, err := compileExecutableSchema(rawSchema)
	if err != nil {
		t.Fatalf("compile get_relation_types output schema: %v", err)
	}
	m := resultToMap(t, callRelationTypes(t, &relationListingBackend{total: 300, comment: longRelationComment},
		map[string]any{"kn_id": "kn-001", "limit": 200}))
	if err := schema.Validate(m); err != nil {
		t.Fatalf("a cut page does not match the advertised output schema: %v", err)
	}
}
