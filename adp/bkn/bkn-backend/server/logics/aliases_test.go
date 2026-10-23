// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package logics

import "testing"

func TestAliasConflict(t *testing.T) {
	order := NamedDefinition{ID: "order", Name: "订单", Aliases: []string{"客户"}}
	customer := NamedDefinition{ID: "customer", Name: "客户"}
	cases := []struct {
		name                     string
		incoming, named, aliased []NamedDefinition
		want                     string
	}{
		{"an alias naming a stored definition", []NamedDefinition{order}, []NamedDefinition{customer}, nil,
			`object type "order" alias "客户" is the name of object type "customer"`},
		{"a new name that is a stored alias", []NamedDefinition{customer}, nil, []NamedDefinition{order},
			`object type "customer" is named "客户", which is an alias of object type "order"`},
		{"both in one save", []NamedDefinition{customer, order}, nil, nil,
			`object type "order" alias "客户" is the name of object type "customer"`},
		{"a rename in the same save frees the name", []NamedDefinition{order, {ID: "customer", Name: "顾客"}},
			[]NamedDefinition{customer}, nil, ""},
		{"an alias dropped in the same save frees it", []NamedDefinition{customer, {ID: "order", Name: "订单"}},
			nil, []NamedDefinition{order}, ""},
		{"its own name is no conflict", []NamedDefinition{{ID: "order", Name: "订单", Aliases: []string{"订单"}}}, nil, nil, ""},
		{"a conflict between stored definitions is not this save's", []NamedDefinition{{ID: "product", Name: "产品"}},
			[]NamedDefinition{customer}, []NamedDefinition{order}, ""},
		{"no aliases anywhere", []NamedDefinition{customer}, nil, nil, ""},
	}
	for _, c := range cases {
		if got := AliasConflict("object type", c.incoming, c.named, c.aliased); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
