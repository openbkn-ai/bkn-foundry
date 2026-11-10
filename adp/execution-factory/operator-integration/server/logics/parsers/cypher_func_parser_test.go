// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package parsers

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/validator"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/utils"
)

func TestCypherParameterNames(t *testing.T) {
	cases := []struct {
		name  string
		query string
		want  []string
	}{
		{"none", "MATCH (s:supplier) RETURN count(s)", []string{}},
		{"first use order, distinct",
			"MATCH (o:order {order_id: $order_id, line_no: $line_no}) WHERE o.qty > $min OR o.id = $order_id RETURN o",
			[]string{"order_id", "line_no", "min"}},
		{"list parameter", "MATCH (s:supplier) WHERE s.id IN $ids RETURN s.name", []string{"ids"}},
		{"escaped name", "MATCH (s:supplier {id: $`supplier id`}) RETURN s", []string{"supplier id"}},
		{"escaped name with doubled backtick", "RETURN $`a``b`", []string{"a`b"}},
		{"unicode name", "MATCH (s:supplier {id: $供应商}) RETURN s", []string{"供应商"}},
		{"dollar in single-quoted string", "MATCH (s:supplier) WHERE s.note = 'costs $id' RETURN s", []string{}},
		{"dollar in double-quoted string with escape", `MATCH (s) WHERE s.note = "say \"$x\"" AND s.id = $id RETURN s`, []string{"id"}},
		{"dollar in escaped identifier", "MATCH (s) RETURN s.`$weird` AS v, $real", []string{"real"}},
		{"line comment", "MATCH (s) // filter by $old\nWHERE s.id = $id RETURN s", []string{"id"}},
		{"block comment", "MATCH (s) /* was $old\n still */ WHERE s.id = $id RETURN s", []string{"id"}},
		{"unterminated block comment", "MATCH (s) WHERE s.id = $id /* $later", []string{"id"}},
		{"bare dollar at end", "RETURN $", []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := CypherParameterNames(tc.query)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("names = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestCypherParameterNamesRefusesPositionalAndEmpty(t *testing.T) {
	for _, query := range []string{"MATCH (s {id: $0}) RETURN s", "RETURN $``"} {
		if _, err := CypherParameterNames(query); err == nil {
			t.Fatalf("%q: expected an error", query)
		}
	}
}

func newCypherTestParser() *pythonFunctionParser {
	return &pythonFunctionParser{Validator: validator.NewValidator()}
}

func cypherInput() *interfaces.FunctionInput {
	return &interfaces.FunctionInput{
		Name:        "supplier_material_count",
		Description: "How many materials a supplier supplies",
		ScriptType:  interfaces.ScriptTypeCypher,
		KnID:        "kn-supply",
		ResultShape: interfaces.CypherResultShapeValue,
		Code:        "MATCH (s:supplier {supplier_id: $id})-[:supplies]->(m:material) RETURN count(m)",
		Inputs: []*interfaces.ParameterDef{
			{Name: "id", Type: interfaces.ParameterTypeString, Required: true, Description: "supplier_id of the supplier"},
		},
	}
}

func TestParseCypherFunction(t *testing.T) {
	metadatas, err := newCypherTestParser().Parse(context.Background(), cypherInput())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(metadatas) != 1 {
		t.Fatalf("got %d metadata, want 1", len(metadatas))
	}
	got, ok := metadatas[0].(*model.FunctionMetadataDB)
	if !ok {
		t.Fatalf("metadata is %T", metadatas[0])
	}
	if !got.IsCypher() || got.KnID != "kn-supply" || got.ResultShape != "value" || got.Code != cypherInput().Code {
		t.Fatalf("metadata = %+v", got)
	}
	if got.Dependencies != "" || got.DependenciesURL != "" {
		t.Fatalf("a cypher function stores no dependencies, got %q %q", got.Dependencies, got.DependenciesURL)
	}
	// The inputs read back from the stored contract exactly as a Python function's do,
	// so the detail page and the agent schema need nothing new.
	inputs, _ := FunctionParamsFromAPISpec(got.APISpec)
	if len(inputs) != 1 || inputs[0].Name != "id" || !inputs[0].Required || inputs[0].Type != interfaces.ParameterTypeString {
		t.Fatalf("inputs read back = %s", utils.ObjectToJSON(inputs))
	}
	spec := utils.JSONToObject[interfaces.APISpec](got.APISpec)
	if !strings.Contains(utils.ObjectToJSON(spec.Responses), `"value"`) {
		t.Fatalf("value shape response does not describe value: %s", utils.ObjectToJSON(spec.Responses))
	}
}

func TestParseCypherFunctionRefuses(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*interfaces.FunctionInput)
		wantErr string
	}{
		{"empty statement", func(in *interfaces.FunctionInput) { in.Code = "  " }, "statement is empty"},
		{"no network", func(in *interfaces.FunctionInput) { in.KnID = "" }, "kn_id"},
		{"no shape", func(in *interfaces.FunctionInput) { in.ResultShape = "" }, "result_shape"},
		{"unknown shape", func(in *interfaces.FunctionInput) { in.ResultShape = "rows" }, "result_shape"},
		{"dependencies", func(in *interfaces.FunctionInput) {
			in.Dependencies = []*interfaces.DependencyInfo{{Name: "requests"}}
		}, "no dependencies"},
		{"dependency index", func(in *interfaces.FunctionInput) { in.DependenciesURL = "https://pypi.org/simple" }, "no dependencies"},
		{"statement parameter not declared", func(in *interfaces.FunctionInput) {
			in.Code += " + $extra"
		}, "uses $extra with no input parameter"},
		{"declared parameter not used", func(in *interfaces.FunctionInput) {
			in.Inputs = append(in.Inputs, &interfaces.ParameterDef{Name: "stale", Type: "string", Description: "x"})
		}, "stale are not used"},
		{"duplicate parameter", func(in *interfaces.FunctionInput) {
			in.Inputs = append(in.Inputs, &interfaces.ParameterDef{Name: "id", Type: "string", Description: "x"})
		}, "more than once"},
		{"object parameter", func(in *interfaces.FunctionInput) { in.Inputs[0].Type = interfaces.ParameterTypeObject }, "must be a string"},
		{"array of objects", func(in *interfaces.FunctionInput) {
			in.Inputs[0].Type = interfaces.ParameterTypeArray
			in.Inputs[0].SubParameters = []*interfaces.ParameterDef{{Name: "items", Type: interfaces.ParameterTypeObject}}
		}, "must be a string"},
		{"array with no item type", func(in *interfaces.FunctionInput) { in.Inputs[0].Type = interfaces.ParameterTypeArray }, "must be a string"},
		{"undescribed parameter", func(in *interfaces.FunctionInput) { in.Inputs[0].Description = "" }, "has no description"},
		{"positional parameter", func(in *interfaces.FunctionInput) { in.Code = "MATCH (s {id: $0}) RETURN s" }, "positional"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := cypherInput()
			tc.mutate(input)
			_, err := newCypherTestParser().Parse(context.Background(), input)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestParseCypherFunctionAcceptsListAndUntypedParameters(t *testing.T) {
	input := cypherInput()
	input.ResultShape = interfaces.CypherResultShapeTable
	input.Code = "MATCH (s:supplier) WHERE s.supplier_id IN $ids AND s.region = $region RETURN s.name"
	input.Inputs = []*interfaces.ParameterDef{
		{Name: "ids", Type: interfaces.ParameterTypeArray, Required: true, Description: "supplier ids",
			SubParameters: []*interfaces.ParameterDef{{Name: "items", Type: interfaces.ParameterTypeString}}},
		{Name: "region", Required: true, Description: "region code"},
	}
	if _, err := newCypherTestParser().Parse(context.Background(), input); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if input.Inputs[1].Type != interfaces.ParameterTypeString {
		t.Fatalf("untyped parameter type = %q, want string", input.Inputs[1].Type)
	}
}

// A Python function is untouched by the Cypher branch: its own checks still run.
func TestParsePythonFunctionStillNeedsAnEntryPoint(t *testing.T) {
	_, err := newCypherTestParser().Parse(context.Background(), &interfaces.FunctionInput{
		Name: "f", Description: "d", ScriptType: interfaces.ScriptTypePython, Code: "x = 1",
	})
	if err == nil {
		t.Fatal("expected the missing entry point to be refused")
	}
}
