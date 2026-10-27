// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"encoding/json"
	"gorm.io/gorm"
	"net/http"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

func TestAuthorizationRegistryReturnsPersistedContract(t *testing.T) {
	r, e, db := newTestServer(t)
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

	if err := e.InitializeRegistry(t.Context()); err != nil {
		t.Fatal(err)
	}
	count := countStaticQueries(t, db)
	t.Cleanup(func() {
		if count.Load() != 0 {
			t.Errorf("registry API queried static tables %d times", count.Load())
		}
	})
	w := do(t, r, http.MethodGet, "/api/safe/v1/authz/registry", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("catalog = %d body=%s", w.Code, w.Body.String())
	}
	wantBody := w.Body.String()
	var readers sync.WaitGroup
	for range 8 {
		readers.Go(func() {
			for range 10 {
				got := do(t, r, http.MethodGet, "/api/safe/v1/authz/registry", nil)
				if got.Code != http.StatusOK || got.Body.String() != wantBody {
					t.Errorf("concurrent registry response differs: %d %s", got.Code, got.Body.String())
					return
				}
			}
		})
	}
	readers.Wait()
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
	r, e, db, _ := newAdminServer(t)
	if err := db.Create(&model.User{ID: "catalog-reader", Account: "catalog-reader", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.ResourceType{ID: "catalog", Name: "Catalog"}).Error; err != nil {
		t.Fatal(err)
	}

	if err := e.InitializeRegistry(t.Context()); err != nil {
		t.Fatal(err)
	}
	count := countStaticQueries(t, db)
	t.Cleanup(func() {
		if count.Load() != 0 {
			t.Errorf("me registry API queried static tables %d times", count.Load())
		}
	})

	const path = "/api/safe/v1/me/authorization-registry"
	if w := tokReq(t, r, http.MethodGet, path, nil, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated catalog = %d, want %d", w.Code, http.StatusUnauthorized)
	}
	if w := tokReq(t, r, http.MethodGet, path, nil, "catalog-reader"); w.Code != http.StatusOK {
		t.Fatalf("authenticated catalog = %d body=%s", w.Code, w.Body.String())
	}
}

func TestAuthorizationRegistryExpandsRequirementChains(t *testing.T) {
	r, e, db := newTestServer(t)
	if err := db.Create(&model.ResourceType{ID: "catalog", Name: "Catalog"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&[]model.Operation{
		{ResourceTypeID: "catalog", ID: "view_detail"},
		{ResourceTypeID: "catalog", ID: "resource_manage", RequiredOperationIDs: "view_detail"},
		{ResourceTypeID: "catalog", ID: "task_manage", RequiredOperationIDs: "resource_manage"},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := e.InitializeRegistry(t.Context()); err != nil {
		t.Fatal(err)
	}
	count := countStaticQueries(t, db)
	t.Cleanup(func() {
		if count.Load() != 0 {
			t.Errorf("registry API queried static tables %d times", count.Load())
		}
	})
	w := do(t, r, http.MethodGet, "/api/safe/v1/authz/registry", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var response struct {
		ResourceTypes []authorizationRegistryResourceType `json:"resource_types"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	for _, operation := range response.ResourceTypes[0].Operations {
		if operation.ID == "task_manage" {
			if want := []string{"resource_manage", "view_detail"}; !reflect.DeepEqual(operation.Requires, want) {
				t.Fatalf("task requirements=%v, want %v", operation.Requires, want)
			}
		}
	}
}

func countStaticQueries(t *testing.T, db *gorm.DB) *atomic.Int64 {
	t.Helper()
	count := &atomic.Int64{}
	const callback = "test:registry-api-static-queries"
	if err := db.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "operations" || tx.Statement.Table == "resource_types" {
			count.Add(1)
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Callback().Query().Remove(callback) })
	return count
}
