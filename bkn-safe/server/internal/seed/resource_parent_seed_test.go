// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package seed

import (
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

func TestCatalogResourceDataInheritanceRequiresRegisteredParent(t *testing.T) {
	db := newDB(t)
	e, err := authz.New(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(db, e); err != nil {
		t.Fatalf("apply: %v", err)
	}

	const user, catalogID = "catalog-reader", "catalog-1"
	for _, operation := range []string{"view_detail", "query_data", "data_write"} {
		mustNoErrSeed(t, e.GrantObjectPermission(user, "catalog", catalogID, operation))
	}
	if err := db.Create(&model.ResourceParent{
		ResourceTypeID: "resource", ResourceID: "owned-resource",
		ParentTypeID: "catalog", ParentID: catalogID,
	}).Error; err != nil {
		t.Fatal(err)
	}

	for _, operation := range []string{"view_detail", "query_data", "data_write"} {
		if allowed, err := e.Check(user, "resource", "owned-resource", operation); err != nil || !allowed {
			t.Fatalf("registered resource parent %s = %v, %v; want inherited allow", operation, allowed, err)
		}
		if allowed, err := e.Check(user, "resource", "unregistered-resource", operation); err != nil || allowed {
			t.Fatalf("unregistered resource parent %s = %v, %v; want no implicit inheritance", operation, allowed, err)
		}
		ids, err := e.AccessibleResources(user, "resource", operation)
		if err != nil || len(ids) != 1 || ids[0] != "owned-resource" {
			t.Fatalf("AccessibleResources(resource/%s) = %v, %v; want [owned-resource]", operation, ids, err)
		}
	}
	filtered, err := e.FilterResourceOps(user, []authz.ResourceRef{
		{Type: "resource", ID: "owned-resource"},
		{Type: "resource", ID: "unregistered-resource"},
	}, nil, []string{"view_detail", "query_data", "data_write"})
	if err != nil || len(filtered) != 2 ||
		len(filtered[0].Operations) != 3 || filtered[0].Operations[0] != "view_detail" ||
		filtered[0].Operations[1] != "query_data" || filtered[0].Operations[2] != "data_write" ||
		len(filtered[1].Operations) != 0 {
		t.Fatalf("FilterResourceOps(resource data) = %+v, %v; want inherited operations only for registered child", filtered, err)
	}
}
