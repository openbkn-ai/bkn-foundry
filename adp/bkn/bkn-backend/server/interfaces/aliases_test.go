// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// Whether a request carries aliases decides the interactive update: left out keeps the stored
// ones; null and [] clear them. Aliases of a data property do not count for the definition.
func TestDeclaresAliases(t *testing.T) {
	cases := []struct {
		request  string
		declared bool
		aliases  []string
	}{
		{`{"id":"order","name":"订单"}`, false, nil},
		{`{"id":"order","data_properties":[{"name":"id","aliases":["编号"]}]}`, false, nil},
		{`{"id":"order","aliases":null}`, true, nil},
		{`{"id":"order","aliases":[]}`, true, []string{}},
		{`{"id":"order","aliases":["订货单","PO"]}`, true, []string{"订货单", "PO"}},
	}
	for _, c := range cases {
		var ot ObjectType
		if err := json.Unmarshal([]byte(c.request), &ot); err != nil {
			t.Fatal(err)
		}
		if ot.DeclaresAliases() != c.declared || !reflect.DeepEqual(ot.Aliases, c.aliases) {
			t.Fatalf("object type %s: declared %v aliases %#v", c.request, ot.DeclaresAliases(), ot.Aliases)
		}
		var at ActionType
		if err := json.Unmarshal([]byte(c.request), &at); err != nil {
			t.Fatal(err)
		}
		if at.DeclaresAliases() != c.declared || !reflect.DeepEqual(at.Aliases, c.aliases) {
			t.Fatalf("action type %s: declared %v aliases %#v", c.request, at.DeclaresAliases(), at.Aliases)
		}
	}
	if (*ObjectType)(nil).DeclaresAliases() || (*ActionType)(nil).DeclaresAliases() {
		t.Fatal("nil declares nothing")
	}
}

// A definition without aliases, or a network without value types, marshals as before: no new key
// appears.
func TestDefinitionsWithoutAliasesMarshalAsBefore(t *testing.T) {
	for _, v := range []any{ObjectType{}, ActionType{}, DataProperty{Name: "id"}, KN{}} {
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "aliases") || strings.Contains(string(data), "value_types") {
			t.Fatalf("%T marshals an aliases key: %s", v, data)
		}
	}
}

func TestDataPropertyKeepUndeclaredAliases(t *testing.T) {
	stored := &DataProperty{Name: "amount", Aliases: []string{"金额"}}
	for request, want := range map[string][]string{
		`{"name":"amount","display_name":"金额（元）"}`: {"金额"},
		`{"name":"amount","aliases":["价款"]}`:       {"价款"},
		`{"name":"amount","aliases":null}`:         nil,
		`{"name":"amount","aliases":[]}`:           nil,
	} {
		var p DataProperty
		if err := json.Unmarshal([]byte(request), &p); err != nil {
			t.Fatal(err)
		}
		p.KeepUndeclared(stored)
		if !reflect.DeepEqual(p.Aliases, want) {
			t.Fatalf("%s: aliases %#v, want %#v", request, p.Aliases, want)
		}
	}
	if !(&DataProperty{Name: "amount", Aliases: []string{"金额"}}).HasDeclarations(nil) {
		t.Fatal("aliases are a declaration")
	}
}

func TestNormalizeAliases(t *testing.T) {
	if NormalizeAliases([]string{}) != nil || NormalizeAliases(nil) != nil {
		t.Fatal("no aliases are stored as nil")
	}
	if got := NormalizeAliases([]string{"PO"}); !reflect.DeepEqual(got, []string{"PO"}) {
		t.Fatalf("got %#v", got)
	}
}
