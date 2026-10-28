// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package knsearch

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// fallbackBackend serves relation types by id and the endpoint search that finds fallback paths.
type fallbackBackend struct {
	*mockBknBackend
	relations   []*interfaces.RelationType
	detailIDs   [][]string
	detailErr   error
	deniedIDs   map[string]bool
	searches    []*interfaces.QueryConceptsReq
	searchError error
}

func (b *fallbackBackend) GetRelationTypeDetail(_ context.Context, _ string, ids []string, _ bool) ([]*interfaces.RelationType, error) {
	b.detailIDs = append(b.detailIDs, ids)
	if b.detailErr != nil {
		return nil, b.detailErr
	}
	for _, id := range ids {
		if b.deniedIDs[id] {
			return nil, errors.New("403 " + id)
		}
	}
	var out []*interfaces.RelationType
	for _, id := range ids {
		for _, rel := range b.relations {
			if rel.ID == id {
				out = append(out, rel)
			}
		}
	}
	return out, nil
}

func (b *fallbackBackend) SearchRelationTypes(_ context.Context, req *interfaces.QueryConceptsReq) (*interfaces.RelationTypeConcepts, error) {
	b.searches = append(b.searches, req)
	if b.searchError != nil {
		return nil, b.searchError
	}
	return &interfaces.RelationTypeConcepts{Entries: b.relations}, nil
}

// The MES case of the design: reports belong to work orders directly, or by a derived path when
// the report has no order number.
func primaryRelation() *interfaces.RelationType {
	return &interfaces.RelationType{
		ID: "report_belongs_to_order", Name: "报工记录归属生产工单", Type: "direct",
		SourceObjectTypeID: "work_report", TargetObjectTypeID: "work_order",
		ReverseName: "工单报工", Cardinality: "many_to_one", Verification: "confirmed",
	}
}

func fallbackRelation() *interfaces.RelationType {
	return &interfaces.RelationType{
		ID: "report_derived_order", Name: "报工推导归属生产工单", Comment: "按批次推导", Type: "indirect",
		SourceObjectTypeID: "work_report", TargetObjectTypeID: "work_order",
		Importance: "secondary", FallbackFor: "report_belongs_to_order", Verification: "unconfirmed",
	}
}

func otherRelation() *interfaces.RelationType {
	return &interfaces.RelationType{
		ID: "order_for_material", Name: "工单对应物料", Type: "direct",
		SourceObjectTypeID: "work_order", TargetObjectTypeID: "material",
	}
}

func relationIDs(relations []*interfaces.RelationType) []string {
	ids := make([]string, len(relations))
	for i, rel := range relations {
		ids[i] = rel.ID
	}
	return ids
}

func TestPairFallbackRelations(t *testing.T) {
	ctx := context.Background()

	t.Run("both recalled: the fallback path is attached to its primary and leaves the list", func(t *testing.T) {
		backend := &fallbackBackend{mockBknBackend: &mockBknBackend{}}
		svc := &localSearchImpl{logger: &mockLogger{}, bknBackend: backend}
		out, fallbacks := svc.pairFallbackRelations(ctx, "kn1",
			[]*interfaces.RelationType{fallbackRelation(), otherRelation(), primaryRelation()})
		if got := strings.Join(relationIDs(out), ","); got != "order_for_material,report_belongs_to_order" {
			t.Fatalf("relations = %s", got)
		}
		if fallbacks["report_belongs_to_order"].ID != "report_derived_order" {
			t.Fatalf("fallbacks = %v", fallbacks)
		}
		if len(backend.detailIDs) != 0 || len(backend.searches) != 0 {
			t.Fatalf("no lookup expected, got detail=%v searches=%d", backend.detailIDs, len(backend.searches))
		}
	})

	t.Run("only the fallback path recalled: its primary takes its place", func(t *testing.T) {
		backend := &fallbackBackend{mockBknBackend: &mockBknBackend{}, relations: []*interfaces.RelationType{primaryRelation()}}
		svc := &localSearchImpl{logger: &mockLogger{}, bknBackend: backend}
		out, fallbacks := svc.pairFallbackRelations(ctx, "kn1",
			[]*interfaces.RelationType{fallbackRelation(), otherRelation()})
		if got := strings.Join(relationIDs(out), ","); got != "report_belongs_to_order,order_for_material" {
			t.Fatalf("relations = %s", got)
		}
		if fallbacks["report_belongs_to_order"] == nil {
			t.Fatal("fallback path not paired")
		}
		if len(backend.detailIDs) != 1 || backend.detailIDs[0][0] != "report_belongs_to_order" {
			t.Fatalf("detail lookups = %v", backend.detailIDs)
		}
	})

	t.Run("only the primary recalled: its fallback path is looked up by endpoints", func(t *testing.T) {
		backend := &fallbackBackend{mockBknBackend: &mockBknBackend{},
			relations: []*interfaces.RelationType{primaryRelation(), fallbackRelation()}}
		svc := &localSearchImpl{logger: &mockLogger{}, bknBackend: backend}
		out, fallbacks := svc.pairFallbackRelations(ctx, "kn1",
			[]*interfaces.RelationType{primaryRelation(), otherRelation()})
		if got := strings.Join(relationIDs(out), ","); got != "report_belongs_to_order,order_for_material" {
			t.Fatalf("relations = %s", got)
		}
		if fallbacks["report_belongs_to_order"] == nil {
			t.Fatal("fallback path not found")
		}
		if len(backend.searches) != 1 {
			t.Fatalf("searches = %d, want 1", len(backend.searches))
		}
		req := backend.searches[0]
		cond, _ := json.Marshal(req.Cond)
		if !req.ExcludeDeprecated || !strings.Contains(string(cond), `"work_report"`) || strings.Contains(string(cond), `"material"`) {
			t.Fatalf("search = %s exclude_deprecated=%v", cond, req.ExcludeDeprecated)
		}
	})

	t.Run("no relation may have a fallback path: no lookup", func(t *testing.T) {
		backend := &fallbackBackend{mockBknBackend: &mockBknBackend{}}
		svc := &localSearchImpl{logger: &mockLogger{}, bknBackend: backend}
		out, fallbacks := svc.pairFallbackRelations(ctx, "kn1", []*interfaces.RelationType{otherRelation()})
		if len(out) != 1 || len(fallbacks) != 0 || len(backend.detailIDs) != 0 || len(backend.searches) != 0 {
			t.Fatalf("out=%v fallbacks=%v detail=%v searches=%d", relationIDs(out), fallbacks, backend.detailIDs, len(backend.searches))
		}
	})

	t.Run("lookups that fail leave the answer without the pair", func(t *testing.T) {
		backend := &fallbackBackend{mockBknBackend: &mockBknBackend{}, detailErr: errors.New("down"), searchError: errors.New("down")}
		svc := &localSearchImpl{logger: &mockLogger{}, bknBackend: backend}
		out, fallbacks := svc.pairFallbackRelations(ctx, "kn1",
			[]*interfaces.RelationType{fallbackRelation(), otherRelation()})
		if got := strings.Join(relationIDs(out), ","); got != "order_for_material" || len(fallbacks) != 0 {
			t.Fatalf("relations = %s fallbacks = %v", got, fallbacks)
		}

		out, fallbacks = svc.pairFallbackRelations(ctx, "kn1", []*interfaces.RelationType{primaryRelation()})
		if len(out) != 1 || len(fallbacks) != 0 {
			t.Fatalf("relations = %v fallbacks = %v", relationIDs(out), fallbacks)
		}
	})
}

// bkn-backend refuses a whole batch for one denied id; the other primaries still pair.
func TestPairFallbackRelationsReadsPrimariesOneByOneAfterABatchFails(t *testing.T) {
	other := fallbackRelation()
	other.ID, other.FallbackFor = "other_fallback", "other_primary"
	otherPrimary := primaryRelation()
	otherPrimary.ID = "other_primary"
	backend := &fallbackBackend{mockBknBackend: &mockBknBackend{},
		relations: []*interfaces.RelationType{primaryRelation(), otherPrimary},
		deniedIDs: map[string]bool{"other_primary": true}}
	svc := &localSearchImpl{logger: &mockLogger{}, bknBackend: backend}
	out, fallbacks := svc.pairFallbackRelations(context.Background(), "kn1",
		[]*interfaces.RelationType{fallbackRelation(), other})
	if got := strings.Join(relationIDs(out), ","); got != "report_belongs_to_order" {
		t.Fatalf("relations = %s", got)
	}
	if fallbacks["report_belongs_to_order"] == nil || fallbacks["other_primary"] != nil {
		t.Fatalf("fallbacks = %v", fallbacks)
	}
	if len(backend.detailIDs) != 3 {
		t.Fatalf("detail lookups = %v, want the batch then one per id", backend.detailIDs)
	}
}

func TestConvertRelationTypesToLocal_Declarations(t *testing.T) {
	ctx := context.Background()
	svc := &localSearchImpl{}

	primary := primaryRelation()
	primary.Importance = "primary"
	primary.Constraints = &interfaces.RelationConstraints{Required: []string{"source"}}
	primary.Lifecycle = &interfaces.Lifecycle{Status: "active"}
	employment := &interfaces.RelationType{
		ID: "employee_in_dept", Name: "任职", Type: "indirect",
		SourceObjectTypeID: "employee", TargetObjectTypeID: "dept", Cardinality: "many_to_many",
		MappingRules: map[string]any{"properties": []any{
			map[string]any{"name": "title", "type": "string", "comment": "职务"},
			map[string]any{"name": "start_date", "type": "date", "semantic": "valid_from", "required": true},
		}},
	}
	fallbacks := map[string]*interfaces.RelationType{primary.ID: fallbackRelation()}

	for _, brief := range []bool{false, true} {
		got := svc.convertRelationTypesToLocal(ctx, []*interfaces.RelationType{primary, employment}, fallbacks, brief)
		p := got[0]
		if p.ReverseName != "工单报工" || p.Cardinality != "many_to_one" || p.Verification != "confirmed" ||
			strings.Join(p.Required, ",") != "source" {
			t.Fatalf("brief=%v declarations = %+v", brief, p)
		}
		if p.Importance != "" || p.Lifecycle != nil {
			t.Fatalf("brief=%v primary importance and active lifecycle must be omitted: %+v", brief, p)
		}
		fb := p.Fallback
		if fb == nil || fb.RelationID != "report_derived_order" || fb.Verification != "unconfirmed" ||
			fb.Comment != "按批次推导" || !strings.Contains(fb.Rule, "report_derived_order") {
			t.Fatalf("brief=%v fallback = %+v", brief, fb)
		}

		props, _ := json.Marshal(got[1].DataProperties)
		want := `[{"name":"title","comment":"职务","type":"string"},{"name":"start_date","type":"date","semantic":"valid_from","required":true}]`
		if brief {
			want = `[{"name":"title","type":"string"},{"name":"start_date","type":"date","semantic":"valid_from"}]`
		}
		if string(props) != want {
			t.Fatalf("brief=%v relation properties\n got: %s\nwant: %s", brief, props, want)
		}
	}
}

// A relation without 3.0 declarations is answered exactly as before they existed.
func TestConvertRelationTypesToLocal_UndeclaredUnchanged(t *testing.T) {
	got := (&localSearchImpl{}).convertRelationTypesToLocal(context.Background(),
		[]*interfaces.RelationType{otherRelation()}, nil, false)
	out, _ := json.Marshal(got)
	want := `[{"concept_type":"relation_type","concept_id":"order_for_material","concept_name":"工单对应物料",` +
		`"source_object_type_id":"work_order","target_object_type_id":"material"}]`
	if string(out) != want {
		t.Fatalf("\n got: %s\nwant: %s", out, want)
	}
}

// Recall leaves deprecated concepts out; one looked up by id says what replaces it. A fallback path
// listed beside its primary stays marked as one, so the two are not added up.
func TestOmitRelationDeclarationsKeepsLifecycleAndFallbackMarking(t *testing.T) {
	rel := fallbackRelation()
	rel.ReverseName, rel.Cardinality = "reverse", "many_to_one"
	rel.Lifecycle = &interfaces.Lifecycle{Status: "deprecated", Reason: "merged", ReplacedBy: "order_reports_merged"}
	interfaces.OmitRelationDeclarations([]*interfaces.RelationType{rel})
	out, _ := json.Marshal(rel)
	for _, key := range []string{"reverse_name", "cardinality", "verification"} {
		if strings.Contains(string(out), `"`+key+`"`) {
			t.Fatalf("%s kept: %s", key, out)
		}
	}
	for _, kept := range []string{`"replaced_by":"order_reports_merged"`, `"importance":"secondary"`,
		`"fallback_for":"report_belongs_to_order"`} {
		if !strings.Contains(string(out), kept) {
			t.Fatalf("%s lost: %s", kept, out)
		}
	}
}
