// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"encoding/json"
	"reflect"
	"testing"
)

func storedDeclaredProperty() *DataProperty {
	return &DataProperty{
		Name: "amount", DisplayName: "金额", Type: "decimal",
		Semantic: "status", Required: true,
		Constraints: &PropertyConstraints{Range: &ValueRange{Min: float64(0)}, Unit: "元"},
	}
}

// The update semantics of a declaration field depend on whether the request carries it: left out
// keeps the stored value; a value replaces it whole; null, "", {} clear it; required: false sets
// false.
func TestDataPropertyKeepUndeclared(t *testing.T) {
	stored := storedDeclaredProperty()
	cases := []struct {
		name    string
		request string
		want    DataProperty
	}{
		{"an old client that edits the display name keeps every declaration",
			`{"name":"amount","display_name":"金额（元）","type":"decimal"}`,
			DataProperty{Name: "amount", DisplayName: "金额（元）", Type: "decimal",
				Semantic: stored.Semantic, Required: true, Constraints: stored.Constraints}},
		{"a value replaces the field whole, nothing merged inside it",
			`{"name":"amount","type":"decimal","constraints":{"unique":true}}`,
			DataProperty{Name: "amount", Type: "decimal", Semantic: stored.Semantic, Required: true,
				Constraints: &PropertyConstraints{Unique: true}}},
		{"null clears",
			`{"name":"amount","type":"decimal","semantic":null,"constraints":null,"required":null}`,
			DataProperty{Name: "amount", Type: "decimal"}},
		{"{} clears constraints",
			`{"name":"amount","type":"decimal","constraints":{}}`,
			DataProperty{Name: "amount", Type: "decimal", Semantic: stored.Semantic, Required: true}},
		{`"" clears semantic`,
			`{"name":"amount","type":"decimal","semantic":""}`,
			DataProperty{Name: "amount", Type: "decimal", Required: true, Constraints: stored.Constraints}},
		{"required false sets false",
			`{"name":"amount","type":"decimal","required":false}`,
			DataProperty{Name: "amount", Type: "decimal", Semantic: stored.Semantic, Constraints: stored.Constraints}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var p DataProperty
			if err := json.Unmarshal([]byte(c.request), &p); err != nil {
				t.Fatal(err)
			}
			p.KeepUndeclared(storedDeclaredProperty())
			p.declared = nil
			if !reflect.DeepEqual(p, c.want) {
				t.Fatalf("got %+v\nwant %+v", p, c.want)
			}
		})
	}
}

// A property without declarations marshals exactly as before: no new keys appear in the stored
// JSON or in a response.
func TestDataPropertyWithoutDeclarationsMarshalsAsBefore(t *testing.T) {
	p := DataProperty{Name: "id", DisplayName: "ID", Type: "string", Comment: "主键"}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), `{"name":"id","display_name":"ID","type":"string","comment":"主键"}`; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestDataPropertyHasDeclarations(t *testing.T) {
	if (&DataProperty{Name: "id", Required: true}).HasDeclarations([]string{"id"}) {
		t.Fatal("Required on a primary key is implied, not a declaration")
	}
	if !(&DataProperty{Name: "name", Required: true}).HasDeclarations([]string{"id"}) {
		t.Fatal("Required on another property is a declaration")
	}
	if (&DataProperty{Name: "x", Constraints: &PropertyConstraints{}}).HasDeclarations(nil) {
		t.Fatal("empty constraints declare nothing")
	}
}

// In the concept index constraints travel as text: a range bound is a number on one property and a
// string on another, and the index would fix the field's type by the first document it saw.
func TestIndexedConstraintsRoundTrip(t *testing.T) {
	ot := ObjectType{ObjectTypeWithKeyField: ObjectTypeWithKeyField{OTID: "order", DataProperties: []*DataProperty{
		{Name: "amount", Type: "decimal", Constraints: &PropertyConstraints{Range: &ValueRange{Min: float64(0)}, Unit: "元"}},
		{Name: "placed_at", Type: "datetime", Constraints: &PropertyConstraints{Range: &ValueRange{Min: "2020-01-01T00:00:00Z"}}},
		{Name: "id", Type: "string"},
	}}}
	data, err := json.Marshal(ot)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if err := EncodeIndexedConstraints(doc); err != nil {
		t.Fatal(err)
	}
	props := doc["data_properties"].([]any)
	for i, want := range []string{`{"range":{"min":0},"unit":"元"}`, `{"range":{"min":"2020-01-01T00:00:00Z"}}`} {
		if got := props[i].(map[string]any)["constraints"]; got != want {
			t.Fatalf("property %d constraints indexed as %#v, want the text %s", i, got, want)
		}
	}
	if _, exists := props[2].(map[string]any)["constraints"]; exists {
		t.Fatal("a property without constraints gains no key")
	}

	if err := DecodeIndexedConstraints(doc); err != nil {
		t.Fatal(err)
	}
	data, err = json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var back ObjectType
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back.DataProperties[0].Constraints, ot.DataProperties[0].Constraints) ||
		!reflect.DeepEqual(back.DataProperties[1].Constraints, ot.DataProperties[1].Constraints) {
		t.Fatalf("constraints did not read back: %+v %+v", back.DataProperties[0].Constraints, back.DataProperties[1].Constraints)
	}
}
