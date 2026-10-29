// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package app

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/driveradapters"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics"
)

// Unit tests build services with mocks, so a dependency the process never sets goes unseen until
// it is used. These tests follow the production wiring instead.

// Boot sets every dependency logics holds: each Set function of driven_access.go is called, before
// any service is built.
func TestBootSetsEveryDependency(t *testing.T) {
	fset := token.NewFileSet()
	access, err := parser.ParseFile(fset, "../logics/driven_access.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	setters := []string{}
	for _, decl := range access.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && strings.HasPrefix(fn.Name.Name, "Set") {
			setters = append(setters, fn.Name.Name)
		}
	}
	boot, err := parser.ParseFile(fset, "app.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Services are built once and copy the dependencies they take from logics, so every one is
	// set before the first service or the handler is built.
	called := map[string]token.Pos{}
	firstBuilt, builtBy := token.NoPos, ""
	ast.Inspect(boot, func(n ast.Node) bool {
		if fn, ok := n.(*ast.FuncDecl); ok && fn.Name.Name != "Boot" {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		name := sel.Sel.Name
		switch {
		case pkg.Name == "logics" && strings.HasPrefix(name, "Set"):
			called[name] = call.Pos()
		case strings.HasPrefix(name, "New") && strings.HasSuffix(name, "Service") ||
			pkg.Name == "driveradapters" && name == "NewRestHandler":
			if firstBuilt == token.NoPos || call.Pos() < firstBuilt {
				firstBuilt, builtBy = call.Pos(), pkg.Name+"."+name
			}
		}
		return true
	})
	if firstBuilt == token.NoPos {
		t.Fatal("Boot builds no service")
	}
	if len(setters) == 0 {
		t.Fatal("no Set functions found in driven_access.go")
	}
	for _, setter := range setters {
		pos, ok := called[setter]
		switch {
		case !ok:
			t.Errorf("Boot does not call logics.%s", setter)
		case pos > firstBuilt:
			t.Errorf("Boot calls logics.%s after %s, which already took its dependencies", setter, builtBy)
		}
	}
}

type pinger struct{}

func (pinger) PingContext(context.Context) error { return nil }

// With every dependency set, the services the routes use have every dependency of theirs: no
// interface, pointer, function or map field is left nil.
func TestServicesHaveEveryDependency(t *testing.T) {
	ctrl := gomock.NewController(t)
	logics.SetAuthAccess(pmock.NewMockAuthAccess(ctrl))
	logics.SetReleaseAccess(pmock.NewMockReleaseAccess(ctrl))
	logics.SetProjectAccess(pmock.NewMockProjectAccess(ctrl))
	logics.SetDraftAccess(pmock.NewMockDraftAccess(ctrl))
	logics.SetCheckAccess(pmock.NewMockCheckAccess(ctrl))
	logics.SetIDRegistryAccess(pmock.NewMockIDRegistryAccess(ctrl))
	logics.SetVegaAccess(pmock.NewMockVegaAccess(ctrl))
	logics.SetIndexBuildAccess(pmock.NewMockIndexBuildAccess(ctrl))
	logics.SetGitStore(pmock.NewMockGitStore(ctrl))
	logics.SetBknBackendAccess(pmock.NewMockBknBackendAccess(ctrl))
	logics.SetExecutionFactoryAccess(pmock.NewMockExecutionFactoryAccess(ctrl))
	logics.SetBknAgentAccess(pmock.NewMockBknAgentAccess(ctrl))
	logics.SetRequirementAccess(pmock.NewMockRequirementAccess(ctrl))

	handler := driveradapters.NewRestHandler(&common.AppSetting{}, pinger{})

	seen := map[uintptr]bool{}
	var walk func(path string, v reflect.Value, depth int)
	walk = func(path string, v reflect.Value, depth int) {
		for v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer {
			if v.IsNil() {
				t.Errorf("%s is nil", path)
				return
			}
			if v.Kind() == reflect.Pointer {
				if seen[v.Pointer()] {
					return
				}
				seen[v.Pointer()] = true
			}
			v = v.Elem()
		}
		// Mocks and the setting are leaves; only the services' own fields are followed.
		if v.Kind() != reflect.Struct || depth > 4 || strings.Contains(v.Type().PkgPath(), "/interfaces/mock") ||
			!strings.Contains(v.Type().PkgPath(), "/bkn-project/server/") || v.Type() == reflect.TypeOf(common.AppSetting{}) {
			return
		}
		for i := 0; i < v.NumField(); i++ {
			field, name := v.Field(i), path+"."+v.Type().Field(i).Name
			switch field.Kind() {
			case reflect.Interface, reflect.Pointer:
				walk(name, field, depth+1)
			case reflect.Func, reflect.Map:
				if field.IsNil() {
					t.Errorf("%s is nil", name)
				}
			}
		}
	}
	walk("restHandler", reflect.ValueOf(handler), 0)
}
