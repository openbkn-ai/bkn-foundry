// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// reservedWords reads the words of oC_ReservedWord from the grammar, so a word
// added there is covered here too.
func reservedWords(t *testing.T) []string {
	t.Helper()
	grammar, err := os.ReadFile("parser/Cypher.g4")
	if err != nil {
		t.Fatal(err)
	}
	rule := regexp.MustCompile(`(?s)\noC_ReservedWord\s*:(.*?);`).FindSubmatch(grammar)
	if rule == nil {
		t.Fatal("oC_ReservedWord not found in the grammar")
	}
	var words []string
	for _, token := range regexp.MustCompile(`[A-Z_]+`).FindAllString(string(rule[1]), -1) {
		words = append(words, strings.ToLower(strings.TrimPrefix(token, "L_")))
	}
	if len(words) < 40 {
		t.Fatalf("read only %d reserved words: %v", len(words), words)
	}
	return words
}

// A parameter may be named after a reserved word, as Neo4j allows. The
// openCypher reference grammar refuses it, and a query with $end or $limit
// failed to parse.
func TestReservedWordParameterNames(t *testing.T) {
	for _, word := range reservedWords(t) {
		t.Run(word, func(t *testing.T) {
			for _, written := range []string{word, strings.ToUpper(word)} {
				sql, err := compileWithParameters(t,
					"MATCH (o:Order) WHERE o.region = $"+written+" RETURN o.id",
					map[string]any{written: "eu"})
				if err != nil {
					t.Fatalf("$%s: %v", written, err)
				}
				if !strings.Contains(sql, "t0.`f_region` = 'eu'") {
					t.Fatalf("$%s is not bound:\n%s", written, sql)
				}
			}
		})
	}
}

func TestReservedWordParametersWhereverAParameterGoes(t *testing.T) {
	for _, tc := range []struct {
		name       string
		query      string
		parameters map[string]any
		want       []string
	}{
		{
			name:       "SKIP and LIMIT",
			query:      "MATCH (o:Order) RETURN o.id SKIP $skip LIMIT $limit",
			parameters: map[string]any{"skip": 3, "limit": 2},
			want:       []string{"LIMIT 2 OFFSET 3"},
		},
		{
			name:       "a list after IN",
			query:      "MATCH (o:Order) WHERE o.region IN $in RETURN o.id",
			parameters: map[string]any{"in": []any{"eu", "us"}},
			want:       []string{"t0.`f_region` IN ('eu', 'us')"},
		},
		{
			name:       "an inline property",
			query:      "MATCH (o:Order {region: $end}) RETURN o.id",
			parameters: map[string]any{"end": "eu"},
			want:       []string{"t0.`f_region` = 'eu'"},
		},
		{
			name:       "an optional condition given a value",
			query:      "MATCH (o:Order) WHERE ($end IS NULL OR o.region = $end) RETURN o.id",
			parameters: map[string]any{"end": "eu"},
			want:       []string{"t0.`f_region` = 'eu'"},
		},
		{
			// Names keep their case, as other parameter names do.
			name:       "$END and $end are two parameters",
			query:      "MATCH (o:Order) WHERE o.region = $END OR o.region = $end RETURN o.id",
			parameters: map[string]any{"END": "eu", "end": "us"},
			want:       []string{"'eu'", "'us'"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sql, err := compileWithParameters(t, tc.query, tc.parameters)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range tc.want {
				if !strings.Contains(sql, want) {
					t.Fatalf("SQL lacks %q:\n%s", want, sql)
				}
			}
		})
	}

	t.Run("an optional condition left out", func(t *testing.T) {
		sql, err := compileWithParameters(t,
			"MATCH (o:Order) WHERE ($end IS NULL OR o.region = $end) RETURN o.id", nil)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(sql, "WHERE") {
			t.Fatalf("the condition was not dropped:\n%s", sql)
		}
	})

	t.Run("a reserved-word parameter still has to be supplied", func(t *testing.T) {
		_, err := compileWithParameters(t,
			"MATCH (o:Order) WHERE o.region = $END RETURN o.id", map[string]any{"end": "eu"})
		if err == nil || !strings.Contains(err.Error(), `parameter "END" was not supplied`) {
			t.Fatalf("err = %v", err)
		}
	})
}
