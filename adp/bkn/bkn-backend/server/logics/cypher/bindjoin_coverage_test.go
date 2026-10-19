// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// The bind-join executor has to run every query the compiler accepts: the SQL
// path compiles and runs the same plans, so anything the executor could not
// run would be a capability the switch takes away. This test collects every
// Cypher string written anywhere in this package's tests, compiles each one
// against both test models, and runs whatever compiles. The only failure
// allowed is the read budget, which the service answers through SQL.

// cypherLiterals returns every string in the package's test files that reads
// as a Cypher query, folding literals joined with + into one.
func cypherLiterals(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	fset := token.NewFileSet()
	for _, name := range files {
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			expr, ok := node.(ast.Expr)
			if !ok {
				return true
			}
			text, ok := foldString(expr)
			if !ok {
				return true
			}
			text = strings.TrimSpace(text)
			upper := strings.ToUpper(text)
			if strings.HasPrefix(upper, "MATCH ") || strings.HasPrefix(upper, "OPTIONAL MATCH ") {
				seen[text] = true
			}
			// A literal joined with + is collected whole; its parts are not.
			_, binary := node.(*ast.BinaryExpr)
			return !binary
		})
	}
	queries := make([]string, 0, len(seen))
	for query := range seen {
		queries = append(queries, query)
	}
	sort.Strings(queries)
	return queries
}

func foldString(expr ast.Expr) (string, bool) {
	switch node := expr.(type) {
	case *ast.BasicLit:
		if node.Kind != token.STRING {
			return "", false
		}
		text, err := strconv.Unquote(node.Value)
		return text, err == nil
	case *ast.BinaryExpr:
		if node.Op != token.ADD {
			return "", false
		}
		left, ok := foldString(node.X)
		if !ok {
			return "", false
		}
		right, ok := foldString(node.Y)
		return left + right, ok
	case *ast.ParenExpr:
		return foldString(node.X)
	default:
		return "", false
	}
}

var queryParameterName = regexp.MustCompile(`\$([A-Za-z_][A-Za-z0-9_]*)`)

// parameterCandidates offers values for a query's parameters: strings, then
// integers, then lists, which between them satisfy every parameter the tests
// write.
func parameterCandidates(query string) []map[string]any {
	names := queryParameterName.FindAllStringSubmatch(query, -1)
	if len(names) == 0 {
		return []map[string]any{nil}
	}
	var candidates []map[string]any
	for _, value := range []any{"x", int64(1), []any{"x"}, []any{int64(1)}, 1.5} {
		parameters := map[string]any{}
		for _, name := range names {
			parameters[name[1]] = value
		}
		candidates = append(candidates, parameters)
	}
	return candidates
}

func modelSchemaData() map[string][]map[string]any {
	return map[string][]map[string]any{
		"res_order": {
			{"f_id": "o1", "f_prev": nil, "f_cust_code": "c1", "f_region": "east", "f_total": "10"},
			{"f_id": "o2", "f_prev": "o1", "f_cust_code": "c1", "f_region": "west", "f_total": "20"},
			{"f_id": "o3", "f_prev": "o2", "f_cust_code": "c2", "f_region": nil, "f_total": nil},
		},
		"res_customer": {
			{"f_code": "c1", "f_region": "east", "f_name": "Ann"},
			{"f_code": "c1", "f_region": "west", "f_name": "Bo"},
			{"f_code": "c2", "f_region": "east", "f_name": "Cy"},
		},
		"res_item": {
			{"f_id": "i1", "f_order": "o1"},
			{"f_id": "i2", "f_order": "o2"},
		},
	}
}

func TestBindJoinRunsEveryQueryTheCompilerAccepts(t *testing.T) {
	models := []struct {
		name   string
		schema func(*testing.T) *Schema
		data   func() map[string][]map[string]any
	}{
		{name: "model", schema: modelSchema, data: modelSchemaData},
		{name: "typed", schema: bindJoinSchema, data: bindJoinData},
	}

	queries := cypherLiterals(t)
	compiled, ran := 0, 0
	var failures []string
	for _, query := range queries {
		tree, err := Parse(query)
		if err != nil {
			continue
		}
		analyzed, err := Analyze(tree)
		if err != nil {
			continue
		}
		for _, model := range models {
			for _, parameters := range parameterCandidates(query) {
				schema := model.schema(t)
				plan, err := Compile(analyzed, schema, CompileOptions{Parameters: parameters})
				if err != nil {
					continue
				}
				if len(plan.Totals) > 0 {
					// Totals over a WITH's groups are the SQL path's alone;
					// the service never hands them to this executor.
					break
				}
				compiled++
				api := newFakeDataAPI(model.data())
				_, _, err = runBindJoin(context.Background(), plan, schema, api.read, defaultBindJoinLimits,
					interfaces.CYPHER_DEFAULT_LIMIT)
				var budget *ReadBudgetError
				if err != nil && !errors.As(err, &budget) {
					failures = append(failures, model.name+": "+query+": "+err.Error())
				} else {
					ran++
				}
				break
			}
		}
	}
	t.Logf("%d Cypher strings in the tests; %d compiled against a test model; %d ran", len(queries), compiled, ran)
	if compiled < 100 {
		t.Fatalf("only %d queries compiled; the collection is not finding the tests' queries", compiled)
	}
	for _, failure := range failures {
		t.Error(failure)
	}
}
