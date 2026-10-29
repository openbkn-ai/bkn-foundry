// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package app

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// Drafting and checks run in the service process, so work a previous run left going never
// finishes: Boot fails it, for ontology drafts and requirement specifications alike.
func TestBootFailsInterruptedWork(t *testing.T) {
	boot, err := parser.ParseFile(token.NewFileSet(), "app.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	failed := map[string]bool{}
	ast.Inspect(boot, func(n ast.Node) bool {
		if fn, ok := n.(*ast.FuncDecl); ok && fn.Name.Name != "Boot" {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "FailInterrupted" {
			return true
		}
		if inner, ok := sel.X.(*ast.CallExpr); ok {
			if ctor, ok := inner.Fun.(*ast.SelectorExpr); ok {
				if pkg, ok := ctor.X.(*ast.Ident); ok {
					failed[pkg.Name+"."+ctor.Sel.Name] = true
				}
			}
		}
		return true
	})
	for _, service := range []string{"draft.NewDraftService", "requirement.NewRequirementService"} {
		if !failed[service] {
			t.Errorf("Boot does not call %s(...).FailInterrupted", service)
		}
	}
}
