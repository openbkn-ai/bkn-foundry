// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package authzseed

import (
	"reflect"
	"testing"
)

func TestRegisteredOperationsAreServedAsCopies(t *testing.T) {
	ResetForTest()
	t.Cleanup(ResetForTest)
	if _, ok := BundleOperations("tool_box"); ok {
		t.Fatal("empty registry served a type")
	}
	Register(Extension{
		BundleOperations: map[string][]string{"tool_box": {"view", "execute"}},
		ProxyOperations:  map[string][]string{"tool_box": {"execute"}},
	})
	got, ok := BundleOperations("tool_box")
	if !ok || !reflect.DeepEqual(got, []string{"view", "execute"}) {
		t.Fatalf("BundleOperations = %v, %v", got, ok)
	}
	got[0] = "mutated"
	if again, _ := BundleOperations("tool_box"); again[0] != "view" {
		t.Fatal("BundleOperations exposed its stored slice")
	}
	if ops, ok := ProxyOperations("tool_box"); !ok || !reflect.DeepEqual(ops, []string{"execute"}) {
		t.Fatalf("ProxyOperations = %v, %v", ops, ok)
	}
	if _, ok := ProxyOperations("mcp"); ok {
		t.Fatal("unregistered type served")
	}
}

func TestRegisterAfterSealPanics(t *testing.T) {
	ResetForTest()
	t.Cleanup(ResetForTest)
	Seal()
	defer func() {
		if recover() == nil {
			t.Fatal("Register after Seal did not panic")
		}
	}()
	Register(Extension{})
}
