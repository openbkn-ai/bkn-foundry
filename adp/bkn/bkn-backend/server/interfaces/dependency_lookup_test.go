// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"context"
	"testing"
)

func TestVerifiedDependencyAccountUsesExactBindingIdentity(t *testing.T) {
	ctx := WithVerifiedDependencySources(context.Background(), []ProxyGrantResolvedSource{
		{
			ProxyGrantSourceSpec: ProxyGrantSourceSpec{
				ResourceType: "resource", ResourceID: "resource-1", Operation: OPERATION_TYPE_QUERY_DATA,
				KNID: "kn-1", BindingType: MODULE_TYPE_OBJECT_TYPE, BindingID: "ot-1",
			},
			GrantedBy: "object-grantor",
		},
		{
			ProxyGrantSourceSpec: ProxyGrantSourceSpec{
				ResourceType: "resource", ResourceID: "resource-1", Operation: OPERATION_TYPE_QUERY_DATA,
				KNID: "kn-1", BindingType: MODULE_TYPE_RELATION_TYPE, BindingID: "rt-1",
			},
			GrantedBy: "relation-grantor",
		},
	})

	objectCtx := WithDependencyBindingScope(ctx, "kn-1", MODULE_TYPE_OBJECT_TYPE, "ot-1")
	objectAccount, ok := VerifiedDependencyAccount(objectCtx, "resource", "resource-1", OPERATION_TYPE_QUERY_DATA)
	if !ok || objectAccount.ID != "object-grantor" {
		t.Fatalf("object binding account = (%+v, %v), want object-grantor", objectAccount, ok)
	}

	relationCtx := WithDependencyBindingScope(ctx, "kn-1", MODULE_TYPE_RELATION_TYPE, "rt-1")
	relationAccount, ok := VerifiedDependencyAccount(relationCtx, "resource", "resource-1", OPERATION_TYPE_QUERY_DATA)
	if !ok || relationAccount.ID != "relation-grantor" {
		t.Fatalf("relation binding account = (%+v, %v), want relation-grantor", relationAccount, ok)
	}

	if _, ok := VerifiedDependencyAccount(ctx, "resource", "resource-1", OPERATION_TYPE_QUERY_DATA); ok {
		t.Fatal("unscoped lookup unexpectedly selected one of multiple binding delegators")
	}
}
