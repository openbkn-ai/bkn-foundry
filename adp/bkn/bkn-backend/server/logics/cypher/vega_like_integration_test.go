//go:build integration

// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package cypher

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	vega "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	sb "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/sqlbuilder"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/connector/local/index/opensearch"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/connector/local/table/mariadb"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/connector/local/table/postgresql"
	fc "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/filter_condition"
)

// TestBKNVegaLikeContract feeds BKN's outgoing condition to SQL and index connectors.
func TestBKNVegaLikeContract(t *testing.T) {
	field := &vega.Property{Name: "name", OriginalName: "name", Type: vega.DataType_String}
	for _, tc := range []struct {
		name      string
		predicate PlanPredicate
		operation string
		pattern   string
		wildcard  string
	}{
		{"contains", PlanStringMatch{Column: "name", Operator: Contains, Value: "苹果"}, "like", "%苹果%", "*苹果*"},
		{"literal wildcard", PlanStringMatch{Column: "name", Operator: Contains, Value: `50%_`}, "like", `%50\%\_%`, `*50%_*`},
		{"literal backslash", PlanStringMatch{Column: "name", Operator: Contains, Value: `a\b`}, "like", `%a\\b%`, `*a\\b*`},
		{"empty", PlanStringMatch{Column: "name", Operator: Contains, Value: ""}, "like", "%%", "**"},
		{"not contains", PlanNegation{Operand: PlanStringMatch{Column: "name", Operator: Contains, Value: "苹果"}}, "not_like", "%苹果%", "*苹果*"},
		{"stored exact", PlanStringMatch{Column: "name", Operator: LikePattern, Value: "苹果"}, "like", "苹果", "苹果"},
		{"stored pattern", PlanStringMatch{Column: "name", Operator: LikePattern, Value: "苹果_%"}, "like", "苹果_%", "苹果?*"},
		{"stored negation", PlanNegation{Operand: PlanStringMatch{Column: "name", Operator: LikePattern, Value: "%苹果%"}}, "not_like", "%苹果%", "*苹果*"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pushed, ok := pushFilter(tc.predicate)
			if !ok {
				t.Fatal("BKN refused a supported LIKE predicate")
			}
			if pushed.condition["operation"] != tc.operation || pushed.condition["value"] != tc.pattern {
				t.Fatalf("outgoing BKN condition = %#v", pushed.condition)
			}
			cfg := &vega.FilterCondCfg{Field: "name", Operation: tc.operation,
				ValueFrom: vega.ValueFrom_Const, Value: pushed.condition["value"]}
			condition, err := fc.NewFilterCondition(context.Background(), cfg, map[string]*vega.Property{"name": field})
			if err != nil {
				t.Fatal(err)
			}
			node, err := (&mariadb.MariaDBConnector{}).ConvertFilterCondition(condition, []*vega.Property{field}, nil)
			if err != nil {
				t.Fatal(err)
			}
			like, ok := node.(*sb.Like)
			if !ok || like.Pattern.(*sb.Param).Value != tc.pattern || like.Escape == nil || *like.Escape != `\` || like.Negated != (tc.operation == "not_like") {
				t.Fatalf("Vega SQL node = %#v", node)
			}
			pgNode, err := (&postgresql.PostgresqlConnector{}).ConvertFilterCondition(condition, []*vega.Property{field}, nil)
			if err != nil {
				t.Fatal(err)
			}
			pgSQL, pgArgs, err := postgresql.ToSql(sb.Select(pgNode))
			if err != nil {
				t.Fatal(err)
			}
			pgOperator := "LIKE"
			if tc.operation == "not_like" {
				pgOperator = "NOT LIKE"
			}
			if pgSQL != fmt.Sprintf(`SELECT ("name" %s $1 ESCAPE E'\\')`, pgOperator) ||
				len(pgArgs) != 1 || pgArgs[0] != tc.pattern {
				t.Fatalf("Vega PostgreSQL query = %q with args %#v, want pattern %q", pgSQL, pgArgs, tc.pattern)
			}
			index, err := (&opensearch.OpenSearchConnector{}).ConvertFilterCondition(condition, []*vega.Property{field})
			if err != nil {
				t.Fatal(err)
			}
			wildcard := map[string]any{"wildcard": map[string]any{"name": tc.wildcard}}
			want := wildcard
			if tc.operation == "not_like" {
				want = map[string]any{"bool": map[string]any{
					"filter":   map[string]any{"exists": map[string]any{"field": "name"}},
					"must_not": wildcard,
				}}
			}
			if !reflect.DeepEqual(index, want) {
				t.Fatalf("Vega index query = %#v, want %#v", index, want)
			}
		})
	}
}
