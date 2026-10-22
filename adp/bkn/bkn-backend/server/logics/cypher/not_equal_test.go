// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"testing"
)

// != is read as <> wherever it is the operator, and left alone inside
// strings, quoted names and comments.
func TestNotEqualAsStandard(t *testing.T) {
	for query, want := range map[string]string{
		"MATCH (o:Order) WHERE o.region != 'east' RETURN o.id": "MATCH (o:Order) WHERE o.region <> 'east' RETURN o.id",
		"WHERE a.x != b.y AND a.z!=1":                          "WHERE a.x <> b.y AND a.z<>1",
		"WHERE a.x = 'it != that' AND a.y != 2":                "WHERE a.x = 'it != that' AND a.y <> 2",
		`WHERE a.x = "say \"!=\"" AND a.y != 2`:                `WHERE a.x = "say \"!=\"" AND a.y <> 2`,
		"WHERE a.x = 'it\\'s != that' AND a.y != 2":            "WHERE a.x = 'it\\'s != that' AND a.y <> 2",
		"MATCH (n:`a!=b`) WHERE n.x != 1":                      "MATCH (n:`a!=b`) WHERE n.x <> 1",
		"MATCH (n:`a``!=b`) WHERE n.x != 1":                    "MATCH (n:`a``!=b`) WHERE n.x <> 1",
		"WHERE a.x != 1 // not != here\nAND a.y != 2":          "WHERE a.x <> 1 // not != here\nAND a.y <> 2",
		"WHERE /* != */ a.x != 1":                              "WHERE /* != */ a.x <> 1",
		"WHERE a.x <> 1":                                       "WHERE a.x <> 1",
	} {
		if got := notEqualAsStandard(query); got != want {
			t.Errorf("notEqualAsStandard(%q) = %q, want %q", query, got, want)
		}
	}
}

// A query written with != compiles to the statement the same query with <>
// does.
func TestNotEqualCompilesAsStandard(t *testing.T) {
	bang, err := compileComparison(t, "MATCH (o:Order) WHERE o.region != 'east' AND o.amount != o.id RETURN o.id", nil)
	if err != nil {
		t.Fatalf("!=: %v", err)
	}
	standard, err := compileComparison(t, "MATCH (o:Order) WHERE o.region <> 'east' AND o.amount <> o.id RETURN o.id", nil)
	if err != nil {
		t.Fatalf("<>: %v", err)
	}
	if bang != standard {
		t.Errorf("!= gives %s, <> gives %s", bang, standard)
	}
}
