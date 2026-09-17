// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

func TestAuthorizationRegistryReturnsPersistedContract(t *testing.T) {
	r, _, db := newTestServer(t)
	notGrantable := false
	if err := db.Create(&model.ResourceType{ID: "parent", Name: "Parent"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.ResourceType{ID: "child", Name: "Child", ParentTypeID: "parent"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Operation{
		ResourceTypeID: "parent", ID: "manage", Name: "Manage",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Operation{
		ResourceTypeID: "child", ID: "view", Name: "View",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Operation{
		ResourceTypeID: "child", ID: "modify", Name: "Modify", Description: "Modify child resources.",
		Grantable: &notGrantable, ParentOperationID: "manage", RequiredOperationIDs: "view",
	}).Error; err != nil {
		t.Fatal(err)
	}

	w := do(t, r, http.MethodGet, "/api/safe/v1/authz/registry", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("catalog = %d body=%s", w.Code, w.Body.String())
	}
	var response struct {
		ResourceTypes []authorizationRegistryResourceType `json:"resource_types"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.ResourceTypes) != 2 {
		t.Fatalf("resource type count = %d, want 2", len(response.ResourceTypes))
	}
	child := response.ResourceTypes[0]
	if child.ID != "child" || child.ParentType != "parent" {
		t.Fatalf("child = %#v", child)
	}
	if len(child.Operations) != 2 || child.Operations[0].ID != "modify" {
		t.Fatalf("child operations = %#v", child.Operations)
	}
	modify := child.Operations[0]
	if modify.Description != "Modify child resources." || modify.Grantable || modify.ParentOperation != "manage" || len(modify.Requires) != 1 || modify.Requires[0] != "view" {
		t.Fatalf("modify = %#v", modify)
	}
	if !child.Operations[1].Grantable {
		t.Fatalf("view = %#v, want grantable default true", child.Operations[1])
	}
}

func TestMeAuthorizationRegistryRequiresToken(t *testing.T) {
	r, _, db, _ := newAdminServer(t)
	if err := db.Create(&model.ResourceType{ID: "catalog", Name: "Catalog"}).Error; err != nil {
		t.Fatal(err)
	}

	const path = "/api/safe/v1/me/authorization-registry"
	if w := tokReq(t, r, http.MethodGet, path, nil, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated catalog = %d, want %d", w.Code, http.StatusUnauthorized)
	}
	if w := tokReq(t, r, http.MethodGet, path, nil, "catalog-reader"); w.Code != http.StatusOK {
		t.Fatalf("authenticated catalog = %d body=%s", w.Code, w.Body.String())
	}
}
