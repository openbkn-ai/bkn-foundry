// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package knsearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// valueTypeBackend serves the network's value types next to the usual mock.
type valueTypeBackend struct {
	*mockBknBackend
	valueTypes []*interfaces.ValueType
	listErr    error
	listCalls  int
}

func (b *valueTypeBackend) ListValueTypes(_ context.Context, _ string) ([]*interfaces.ValueType, error) {
	b.listCalls++
	return b.valueTypes, b.listErr
}

// applyStatus is the design's lis_apply_status: X is the old code that also means "revoked".
func applyStatus() *interfaces.ValueType {
	return &interfaces.ValueType{ID: "apply_status", BaseType: "keyword", Enum: []*interfaces.ValueTypeEnum{
		{Value: "N", Display: "新建"},
		{Value: "S", Display: "已采样"},
		{Value: "R", Display: "已报告"},
		{Value: "C", Display: "已撤销"},
		{Value: "X", Display: "已撤销", Deprecated: true},
	}}
}

// departments is a dictionary over any expansion limit used below.
func departments(n int) *interfaces.ValueType {
	vt := &interfaces.ValueType{ID: "dept", BaseType: "keyword"}
	for i := 0; i < n; i++ {
		vt.Enum = append(vt.Enum, &interfaces.ValueTypeEnum{Value: fmt.Sprintf("D%03d", i), Display: fmt.Sprintf("科室%03d", i)})
	}
	vt.Enum[7].Display = "检验科"
	return vt
}

func declaredProperties() []*interfaces.KnSearchDataProperty {
	return []*interfaces.KnSearchDataProperty{
		{Name: "status", Type: "string", ValueType: "apply_status"},
		{Name: "dept", Type: "string", ValueType: "dept"},
		{Name: "amount", Type: "decimal", ValueType: "amount_cny"},
		{Name: "note", Type: "string"},
	}
}

func newEnumSearch(backend *valueTypeBackend, limit int) *localSearchImpl {
	cfg := &config.Config{}
	cfg.ConceptSearchConfig.EnumExpandLimit = limit
	return &localSearchImpl{logger: &mockLogger{}, bknBackend: backend, config: cfg}
}

func enumJSON(t *testing.T, p *interfaces.KnSearchDataProperty) string {
	t.Helper()
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestExpandEnums(t *testing.T) {
	valueTypes := []*interfaces.ValueType{applyStatus(), departments(60),
		{ID: "amount_cny", BaseType: "decimal"}}

	cases := []struct {
		name      string
		brief     bool
		enumQuery string
		want      map[string]string
	}{
		{
			name: "full mode lists a small enum with its deprecated item and counts a large one",
			want: map[string]string{
				"status": `{"name":"status","type":"string","value_type":"apply_status","enum":[{"value":"N","display":"新建"},{"value":"S","display":"已采样"},{"value":"R","display":"已报告"},{"value":"C","display":"已撤销"},{"value":"X","display":"已撤销","deprecated":true}]}`,
				"dept":   `{"name":"dept","type":"string","value_type":"dept","enum_count":60}`,
				"amount": `{"name":"amount","type":"decimal"}`,
				"note":   `{"name":"note","type":"string"}`,
			},
		},
		{
			name:      "enum_query looks up a large enum and leaves a small one whole",
			enumQuery: "检验 撤销",
			want: map[string]string{
				"status": `{"name":"status","type":"string","value_type":"apply_status","enum":[{"value":"N","display":"新建"},{"value":"S","display":"已采样"},{"value":"R","display":"已报告"},{"value":"C","display":"已撤销"},{"value":"X","display":"已撤销","deprecated":true}]}`,
				"dept":   `{"name":"dept","type":"string","value_type":"dept","enum":[{"value":"D007","display":"检验科"}],"enum_count":60}`,
			},
		},
		{
			name:  "brief mode counts every enum",
			brief: true,
			want: map[string]string{
				"status": `{"name":"status","type":"string","value_type":"apply_status","enum_count":5}`,
				"dept":   `{"name":"dept","type":"string","value_type":"dept","enum_count":60}`,
			},
		},
		{
			name:      "brief mode still answers enum_query, by display name or code",
			brief:     true,
			enumQuery: "已撤销 d059",
			want: map[string]string{
				"status": `{"name":"status","type":"string","value_type":"apply_status","enum":[{"value":"C","display":"已撤销"},{"value":"X","display":"已撤销","deprecated":true}],"enum_count":5}`,
				"dept":   `{"name":"dept","type":"string","value_type":"dept","enum":[{"value":"D059","display":"科室059"}],"enum_count":60}`,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			backend := &valueTypeBackend{mockBknBackend: &mockBknBackend{}, valueTypes: valueTypes}
			props := declaredProperties()
			obj := &interfaces.KnSearchObjectType{ConceptID: "apply", DataProperties: props[:2]}
			rel := &interfaces.KnSearchRelationType{ConceptID: "r", DataProperties: props[2:]}
			newEnumSearch(backend, 50).expandEnums(context.Background(), "kn", []*interfaces.KnSearchObjectType{obj},
				[]*interfaces.KnSearchRelationType{rel}, tc.brief, tc.enumQuery)
			for _, p := range props {
				if want, ok := tc.want[p.Name]; ok {
					if got := enumJSON(t, p); got != want {
						t.Errorf("%s:\n got %s\nwant %s", p.Name, got, want)
					}
				}
			}
			if backend.listCalls != 1 {
				t.Fatalf("value types read %d times, want once", backend.listCalls)
			}
		})
	}
}

// The matches of enum_query are capped at the expansion limit like any listed enum.
func TestExpandEnumsCapsMatchesAtTheLimit(t *testing.T) {
	backend := &valueTypeBackend{mockBknBackend: &mockBknBackend{}, valueTypes: []*interfaces.ValueType{departments(60)}}
	p := &interfaces.KnSearchDataProperty{Name: "dept", ValueType: "dept"}
	newEnumSearch(backend, 10).expandEnums(context.Background(), "kn",
		[]*interfaces.KnSearchObjectType{{DataProperties: []*interfaces.KnSearchDataProperty{p}}}, nil, false, "科室")
	if len(p.Enum) != 10 || p.EnumCount != 60 {
		t.Fatalf("got %d items of %d, want 10 of 60", len(p.Enum), p.EnumCount)
	}
}

// A network without value type references reads no value types and answers as before; one whose
// value types cannot be read keeps the value_type id.
func TestExpandEnumsWithoutValueTypes(t *testing.T) {
	backend := &valueTypeBackend{mockBknBackend: &mockBknBackend{}}
	p := &interfaces.KnSearchDataProperty{Name: "note", Type: "string"}
	newEnumSearch(backend, 0).expandEnums(context.Background(), "kn",
		[]*interfaces.KnSearchObjectType{{DataProperties: []*interfaces.KnSearchDataProperty{p}}}, nil, false, "x")
	if backend.listCalls != 0 || enumJSON(t, p) != `{"name":"note","type":"string"}` {
		t.Fatalf("calls=%d property=%s", backend.listCalls, enumJSON(t, p))
	}

	backend.listErr = errors.New("bkn-backend unavailable")
	p = &interfaces.KnSearchDataProperty{Name: "status", ValueType: "apply_status"}
	newEnumSearch(backend, 0).expandEnums(context.Background(), "kn",
		[]*interfaces.KnSearchObjectType{{DataProperties: []*interfaces.KnSearchDataProperty{p}}}, nil, false, "")
	if got := enumJSON(t, p); got != `{"name":"status","value_type":"apply_status"}` {
		t.Fatalf("got %s", got)
	}
}

// Aliases of object types, relation types and action types come at either level; property aliases
// and the property's value type id follow the property rules.
func TestConceptRetrievalAliasesAndEnums(t *testing.T) {
	detail := createMockNetworkDetail(1, 0, 1)
	ot := detail.ObjectTypes[0]
	ot.Aliases = []string{"病人", "就诊人"}
	ot.DataProperties = []*interfaces.DataProperty{{
		Name: "status", Type: "string", Aliases: []string{"状态码"},
		Constraints: &interfaces.PropertyConstraints{ValueType: "apply_status"},
	}}
	detail.ActionTypes[0].ObjectTypeID = ot.ID
	detail.ActionTypes[0].Aliases = []string{"作废"}
	backend := &valueTypeBackend{mockBknBackend: &mockBknBackend{networkDetail: detail},
		valueTypes: []*interfaces.ValueType{applyStatus()}}

	for _, brief := range []bool{true, false} {
		cfg := DefaultConceptRetrievalConfig()
		cfg.SchemaBrief = &brief
		res, err := newEnumSearch(backend, 0).conceptRetrieval(context.Background(),
			&interfaces.KnSearchLocalRequest{KnID: "129", Query: "q"}, cfg)
		if err != nil {
			t.Fatalf("brief=%v: %v", brief, err)
		}
		if len(res.ObjectTypes) != 1 || len(res.ActionTypes) != 1 {
			t.Fatalf("brief=%v: got %d object types, %d action types", brief, len(res.ObjectTypes), len(res.ActionTypes))
		}
		if got := res.ObjectTypes[0].Aliases; !equalStrings(got, []string{"病人", "就诊人"}) {
			t.Errorf("brief=%v: object type aliases %v", brief, got)
		}
		if got := res.ActionTypes[0].Aliases; !equalStrings(got, []string{"作废"}) {
			t.Errorf("brief=%v: action type aliases %v", brief, got)
		}
		p := res.ObjectTypes[0].DataProperties[0]
		if brief && (p.Aliases != nil || p.Enum != nil || p.EnumCount != 5) {
			t.Errorf("brief: property %s", enumJSON(t, p))
		}
		if !brief && (!equalStrings(p.Aliases, []string{"状态码"}) || len(p.Enum) != 5 || p.EnumCount != 0) {
			t.Errorf("full: property %s", enumJSON(t, p))
		}
	}
}

func TestConvertRelationTypesToLocalAliases(t *testing.T) {
	svc := &localSearchImpl{logger: &mockLogger{}}
	got := svc.convertRelationTypesToLocal(context.Background(),
		[]*interfaces.RelationType{{ID: "r", Name: "属于", Aliases: []string{"归属"}}}, nil, true)
	if !equalStrings(got[0].Aliases, []string{"归属"}) {
		t.Fatalf("relation aliases %v", got[0].Aliases)
	}
}

// Every other answer leaves aliases out, as it does the other declarations.
func TestOmitDeclarationsDropsAliases(t *testing.T) {
	ot := &interfaces.ObjectType{Aliases: []string{"病人"},
		DataProperties: []*interfaces.DataProperty{{Name: "id", Aliases: []string{"病历号"}}}}
	rt := &interfaces.RelationType{Aliases: []string{"归属"}}
	at := &interfaces.ActionType{Aliases: []string{"作废"}}
	interfaces.OmitPropertyDeclarations([]*interfaces.ObjectType{ot})
	interfaces.OmitRelationDeclarations([]*interfaces.RelationType{rt})
	(&interfaces.KnowledgeNetworkDetail{ActionTypes: []*interfaces.ActionType{at}}).Slim(interfaces.DetailLevelFull)
	if ot.Aliases != nil || ot.DataProperties[0].Aliases != nil || rt.Aliases != nil || at.Aliases != nil {
		t.Fatalf("aliases kept: %v %v %v %v", ot.Aliases, ot.DataProperties[0].Aliases, rt.Aliases, at.Aliases)
	}
}
