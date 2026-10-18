// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package authz

import (
	"reflect"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

func TestNormalizeOperations(t *testing.T) {
	e, db := newTestEnforcerDB(t)
	if err := db.Create(&[]model.Operation{
		{ResourceTypeID: "catalog", ID: "view_detail"},
		{ResourceTypeID: "catalog", ID: "resource_manage", RequiredOperationIDs: "view_detail"},
		{ResourceTypeID: "catalog", ID: "task_manage", RequiredOperationIDs: "resource_manage"},
	}).Error; err != nil {
		t.Fatal(err)
	}
	mustNoErr(t, db.Create(&model.ResourceType{ID: "catalog"}).Error)
	mustNoErr(t, e.InitializeRegistry(t.Context()))
	count := countRegistryQueries(t, db)
	got, err := e.NormalizeOperations(t.Context(), "catalog", []string{"task_manage", "task_manage"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"task_manage", "resource_manage", "view_detail"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("normalized = %v, want %v", got, want)
	}

	if count.Load() != 0 {
		t.Fatalf("normalization queried the static registry %d times", count.Load())
	}
}

func TestRequiringOperationsByRequirement(t *testing.T) {
	e, db := newTestEnforcerDB(t)
	if err := db.Create(&[]model.Operation{
		{ResourceTypeID: "catalog", ID: "view_detail"},
		{ResourceTypeID: "catalog", ID: "resource_manage", RequiredOperationIDs: "view_detail"},
		{ResourceTypeID: "catalog", ID: "task_manage", RequiredOperationIDs: "resource_manage"},
	}).Error; err != nil {
		t.Fatal(err)
	}
	got, err := e.RequiringOperationsByRequirement(t.Context(), "catalog", []string{"view_detail", "resource_manage"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"view_detail":     {"resource_manage", "task_manage"},
		"resource_manage": {"task_manage"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reverse dependencies = %v, want %v", got, want)
	}
}

func TestExpandRequirements(t *testing.T) {
	graph := map[string][]string{"task": {"manage", "view"}, "manage": {"view"}, "view": nil}
	if got, want := ExpandRequirements(graph, "task"), []string{"manage", "view"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("requirements=%v, want %v", got, want)
	}
	if got := ExpandRequirements(graph, "view"); len(got) != 0 {
		t.Fatalf("leaf requirements=%v", got)
	}
}
