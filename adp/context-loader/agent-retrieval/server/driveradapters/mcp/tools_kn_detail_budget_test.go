// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/knmetrics"
)

// setAsideKnDetailBudget lifts the character limit for a test written against the
// count caps, which a fixture at those caps passes on size alone.
func setAsideKnDetailBudget(t *testing.T) {
	t.Helper()
	saved := interfaces.MaxKnDetailChars
	interfaces.MaxKnDetailChars = 1 << 30
	t.Cleanup(func() { interfaces.MaxKnDetailChars = saved })
}

// A network under both count caps whose summary is still too long to read in one
// result: sixty object types of forty properties each.
func propertyHeavyNetwork() *interfaces.KnowledgeNetworkDetail {
	detail := &interfaces.KnowledgeNetworkDetail{
		ID:            "kn-001",
		Name:          "供应链",
		Comment:       "采购、库存与交付",
		ConceptGroups: []*interfaces.ConceptGroup{{ID: "cg-1", Name: "采购", Comment: "采购订单与供应商"}},
		ActionTypes:   []*interfaces.ActionType{{ID: "at-1", Name: "下单"}},
	}
	for i := range 60 {
		o := &interfaces.ObjectType{
			ID:         fmt.Sprintf("ot-%04d", i),
			Name:       fmt.Sprintf("对象类-%04d", i),
			DataSource: &interfaces.ResourceInfo{Type: "resource", ID: "res"},
		}
		for j := range 40 {
			o.DataProperties = append(o.DataProperties, &interfaces.DataProperty{Name: fmt.Sprintf("property_%02d", j), Type: "string"})
		}
		detail.ObjectTypes = append(detail.ObjectTypes, o)
		detail.RelationTypes = append(detail.RelationTypes, &interfaces.RelationType{
			ID: fmt.Sprintf("rt-%04d", i), Name: "属于", SourceObjectTypeID: o.ID, TargetObjectTypeID: "ot-0000",
		})
	}
	return detail
}

// grantingSchemaAccess grants every property of propertyHeavyNetwork in full, so the
// properties survive authorization and the answer is as large as it is for its owner.
type grantingSchemaAccess struct{ countingSchemaAccess }

func (g *grantingSchemaAccess) GetObjectTypeSchema(ctx context.Context, knID, otID string) (*interfaces.ObjectTypeSchemaResp, error) {
	resp, err := g.countingSchemaAccess.GetObjectTypeSchema(ctx, knID, otID)
	for j := range 40 {
		resp.EffectivePermissions[fmt.Sprintf("property_%02d", j)] = interfaces.PropertyAccessFull
	}
	return resp, err
}

func callKnDetail(t *testing.T, detail *interfaces.KnowledgeNetworkDetail, args map[string]any) (*mcp.CallToolResult, int64) {
	t.Helper()
	access := &grantingSchemaAccess{}
	bkn := &stubMetricBknBackend{detail: detail}
	handler := handleGetKnDetail(bkn, knmetrics.NewKnMetricsServiceWith(nil, bkn, nil), access, stubKnAuthz{})
	result, err := handler(context.Background(), mcpReq(args))
	if err != nil || result.IsError {
		t.Fatalf("get_kn_detail failed: %v %+v", err, result)
	}
	return result, access.calls.Load()
}

// The report: a summary under both count caps still overflowed the caller's host,
// which stored it to a file or dropped it, and parts of the model went missing
// without a word. The answer now fits, and says what it left out.
func TestKnDetailSummaryPastTheCharacterLimitNarrowsToItsOutline(t *testing.T) {
	for _, format := range []string{"json", "toon"} {
		result, reads := callKnDetail(t, propertyHeavyNetwork(), map[string]any{
			"kn_id": "kn-001", "response_format": format,
		})
		if n := utf8.RuneCountInString(resultText(result)); n > interfaces.MaxKnDetailChars {
			t.Fatalf("%s: answer is %d characters, past the %d limit", format, n, interfaces.MaxKnDetailChars)
		}
		// The outline is narrowed after the per-object-type reads, which are what make
		// the properties it no longer lists safe to count.
		if reads != 60 {
			t.Fatalf("%s: read %d object type schemas, want 60", format, reads)
		}
		m := resultToMap(t, result)
		objectTypes, _ := m["object_types"].([]any)
		if len(objectTypes) != 60 {
			t.Fatalf("%s: outline carries %d object types, want all 60", format, len(objectTypes))
		}
		first, _ := objectTypes[0].(map[string]any)
		if _, present := first["data_properties"]; present {
			t.Fatalf("%s: outline still lists properties: %v", format, first)
		}
		if first["data_property_count"] != float64(40) {
			t.Fatalf("%s: data_property_count = %v, want 40", format, first["data_property_count"])
		}
		if relations, _ := m["relation_types"].([]any); len(relations) != 60 {
			t.Fatalf("%s: outline carries %d relation types, want 60", format, len(relations))
		}
		notice, _ := m["notice"].(string)
		if !strings.Contains(notice, "get_object_types") {
			t.Fatalf("%s: notice must say where the properties went, got %q", format, notice)
		}
		if _, present := m["object_type_count"]; present {
			t.Fatalf("%s: the arrays are the count on an outline", format)
		}
	}
}

// full asked for more than summary, so the same network is at least as large.
func TestKnDetailFullPastTheCharacterLimitNarrowsToo(t *testing.T) {
	result, _ := callKnDetail(t, propertyHeavyNetwork(), map[string]any{
		"kn_id": "kn-001", "response_format": "json", "detail_level": interfaces.DetailLevelFull,
	})
	if n := utf8.RuneCountInString(resultText(result)); n > interfaces.MaxKnDetailChars {
		t.Fatalf("full answer is %d characters, past the %d limit", n, interfaces.MaxKnDetailChars)
	}
	if notice, _ := resultToMap(t, result)["notice"].(string); notice == "" {
		t.Fatalf("a narrowed full answer must say so")
	}
}

// Relations are already as small as a summary makes them, so a relation-heavy network
// under its count cap goes past the outline to the shell.
func TestKnDetailRelationHeavyNetworkUnderTheCapNarrowsToItsShell(t *testing.T) {
	detail := propertyHeavyNetwork()
	for i := range interfaces.MaxSummaryRelationTypes - len(detail.RelationTypes) {
		detail.RelationTypes = append(detail.RelationTypes, &interfaces.RelationType{
			ID: fmt.Sprintf("rt-extra-%04d", i), Name: fmt.Sprintf("关联关系-%04d", i),
			SourceObjectTypeID: "ot-0001", TargetObjectTypeID: "ot-0002", Type: "direct",
		})
	}
	result, _ := callKnDetail(t, detail, map[string]any{"kn_id": "kn-001", "response_format": "json"})
	if n := utf8.RuneCountInString(resultText(result)); n > interfaces.MaxKnDetailChars {
		t.Fatalf("answer is %d characters, past the %d limit", n, interfaces.MaxKnDetailChars)
	}
	m := resultToMap(t, result)
	if got, present := m["relation_types"]; !present || got != nil {
		t.Fatalf("relation_types should be present and null, got %v", got)
	}
	if m["relation_type_count"] != float64(interfaces.MaxSummaryRelationTypes) || m["object_type_count"] != float64(60) {
		t.Fatalf("counts = %v / %v", m["object_type_count"], m["relation_type_count"])
	}
	if notice, _ := m["notice"].(string); !strings.Contains(notice, "concept_groups") {
		t.Fatalf("notice must point at the groups to narrow by, got %q", notice)
	}
}

// brief is what a caller asks for to learn what a network is about: its name and
// description, without reading the model or paying a read per object type for it.
func TestKnDetailBriefAnswersWithTheShellOutright(t *testing.T) {
	result, reads := callKnDetail(t, networkOfSize(3), map[string]any{
		"kn_id": "kn-001", "response_format": "json", "detail_level": interfaces.DetailLevelBrief,
	})
	if reads != 0 {
		t.Fatalf("brief read %d object type schemas for an answer that carries none", reads)
	}
	m := resultToMap(t, result)
	if m["name"] != "medical" {
		t.Fatalf("name = %v, want the network's", m["name"])
	}
	for _, field := range []string{"object_types", "relation_types", "action_types"} {
		if got, present := m[field]; !present || got != nil {
			t.Fatalf("%s should be present and null, got %v", field, got)
		}
	}
	if m["object_type_count"] != float64(3) || m["relation_type_count"] != float64(3) {
		t.Fatalf("counts = %v / %v, want 3 / 3", m["object_type_count"], m["relation_type_count"])
	}
	if groups, _ := m["concept_groups"].([]any); len(groups) != 1 {
		t.Fatalf("concept_groups must survive brief, got %v", m["concept_groups"])
	}
	notice, _ := m["notice"].(string)
	if !strings.Contains(notice, "brief") || !strings.Contains(notice, "search_schema") {
		t.Fatalf("notice must say this is brief and where to go next, got %q", notice)
	}
}

// Networks that fit are answered exactly as before.
func TestKnDetailUnderTheCharacterLimitIsUnchanged(t *testing.T) {
	result, _ := callKnDetail(t, networkOfSize(10), map[string]any{"kn_id": "kn-001", "response_format": "json"})
	m := resultToMap(t, result)
	if _, present := m["notice"]; present {
		t.Fatalf("nothing was narrowed, got notice %v", m["notice"])
	}
	objectTypes, _ := m["object_types"].([]any)
	first, _ := objectTypes[0].(map[string]any)
	if len(objectTypes) != 10 || first["data_properties"] == nil {
		t.Fatalf("want all 10 object types with their properties, got %v", objectTypes)
	}
}

// structuredRunes is the size of the structuredContent copy a host may read in place
// of the text.
func structuredRunes(t *testing.T, result *mcp.CallToolResult) int {
	t.Helper()
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	return utf8.RuneCount(raw)
}

// The text a TOON caller reads sits beside a structuredContent JSON copy that is
// larger, and a host may read either. A limit that held for the text alone let the
// JSON copy pass it.
func TestKnDetailLimitHoldsForTheStructuredCopyToo(t *testing.T) {
	saved := interfaces.MaxKnDetailChars
	t.Cleanup(func() { interfaces.MaxKnDetailChars = saved })

	interfaces.MaxKnDetailChars = 1 << 30
	whole, _ := callKnDetail(t, networkOfSize(40), map[string]any{"kn_id": "kn-001", "response_format": "toon"})
	toon, structured := utf8.RuneCountInString(resultText(whole)), structuredRunes(t, whole)
	if structured <= toon {
		t.Fatalf("fixture: the JSON copy (%d) must be larger than the TOON text (%d)", structured, toon)
	}

	// Between the two: the TOON text fits, the JSON copy does not.
	interfaces.MaxKnDetailChars = (toon + structured) / 2
	result, _ := callKnDetail(t, networkOfSize(40), map[string]any{"kn_id": "kn-001", "response_format": "toon"})
	if n := structuredRunes(t, result); n > interfaces.MaxKnDetailChars {
		t.Fatalf("structuredContent is %d characters, past the %d limit", n, interfaces.MaxKnDetailChars)
	}
	if n := utf8.RuneCountInString(resultText(result)); n > interfaces.MaxKnDetailChars {
		t.Fatalf("text is %d characters, past the %d limit", n, interfaces.MaxKnDetailChars)
	}
	if notice, _ := resultToMap(t, result)["notice"].(string); notice == "" {
		t.Fatalf("a narrowed answer must say so")
	}
}

// A shell built for brief, before any authorization, and one the size limit forces
// after it, count the same network the same way: authorization narrows an object
// type's properties, never the object types themselves.
func TestKnDetailShellsCountTheNetworkAlike(t *testing.T) {
	saved := interfaces.MaxKnDetailChars
	t.Cleanup(func() { interfaces.MaxKnDetailChars = saved })

	brief, _ := callKnDetail(t, propertyHeavyNetwork(), map[string]any{
		"kn_id": "kn-001", "response_format": "json", "detail_level": interfaces.DetailLevelBrief,
	})
	interfaces.MaxKnDetailChars = 3000
	sized, reads := callKnDetail(t, propertyHeavyNetwork(), map[string]any{"kn_id": "kn-001", "response_format": "json"})
	if reads == 0 {
		t.Fatalf("fixture: the size-forced shell must come after authorization")
	}
	b, s := resultToMap(t, brief), resultToMap(t, sized)
	if _, isShell := s["object_types"]; !isShell || s["object_types"] != nil {
		t.Fatalf("fixture: want the size limit to force the shell, got %v", s["object_types"])
	}
	for _, field := range []string{"object_type_count", "relation_type_count", "action_type_count"} {
		if b[field] != s[field] {
			t.Fatalf("%s: brief says %v, the size-forced shell %v", field, b[field], s[field])
		}
	}
	if s["object_type_count"] != float64(60) {
		t.Fatalf("object_type_count = %v, want the network's 60", s["object_type_count"])
	}
}
