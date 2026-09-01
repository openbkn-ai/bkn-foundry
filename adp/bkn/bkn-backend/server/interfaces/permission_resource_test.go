// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"reflect"
	"testing"
)

func TestKNChildResourceID(t *testing.T) {
	if got, want := KNChildResourceID("kn-a", "child-1"), "kn-a/child-1"; got != want {
		t.Fatalf("KNChildResourceID() = %q, want %q", got, want)
	}
	if KNChildResourceID("kn-a", "shared") == KNChildResourceID("kn-b", "shared") {
		t.Fatal("the same child ID in different knowledge networks must not collide")
	}
	resource := KNChildPermissionResource(RESOURCE_TYPE_OBJECT_TYPE, "kn-a", "child-1")
	if resource.Type != RESOURCE_TYPE_OBJECT_TYPE || resource.ID != "kn-a/child-1" {
		t.Fatalf("unexpected permission resource: %#v", resource)
	}
}

func TestKNChildResourceParents(t *testing.T) {
	got := KNChildResourceParents("kn-a", []string{"one", "two"})
	want := []PermissionResourceParent{
		{ResourceID: "kn-a/one", ParentID: "kn-a"},
		{ResourceID: "kn-a/two", ParentID: "kn-a"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("KNChildResourceParents() = %#v, want %#v", got, want)
	}
}

func TestIsValidAuthorizationID(t *testing.T) {
	tests := []struct {
		id    string
		valid bool
	}{
		{id: "d5uvv2s9p6s8a2b3c4d5", valid: true},
		{id: "", valid: false},
		{id: " child", valid: false},
		{id: "child ", valid: false},
		{id: "child/id", valid: false},
		{id: "child*", valid: false},
	}
	for _, tt := range tests {
		if got := IsValidAuthorizationID(tt.id); got != tt.valid {
			t.Errorf("IsValidAuthorizationID(%q) = %v, want %v", tt.id, got, tt.valid)
		}
	}
}
