// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package bkntrace

import (
	"context"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/knversion"
)

func TestStampKnVersionRecordsTheVersionReadOnNetworkRefsOnly(t *testing.T) {
	refs := []BusinessRef{
		{RefType: "knowledge_network", RefID: "kn:a", Version: "unversioned"},
		{RefType: "object_type", RefID: "object:a:b", Version: "unversioned"},
		{RefType: "relation_type", RefID: "relation:a:c", Version: "unversioned"},
		{RefType: "data_resource", RefID: "resource:r", Version: "unversioned"},
		{RefType: "metric", RefID: "metric:a:m:v2", Version: "v2"},
	}
	got := stampKnVersion(refs, "draft@12")
	for i, want := range []string{"draft@12", "draft@12", "draft@12", "unversioned", "v2"} {
		if got[i].Version != want {
			t.Errorf("%s version = %q, want %q", got[i].RefType, got[i].Version, want)
		}
	}
	if refs[0].Version != "unversioned" {
		t.Error("the input slice must not be modified")
	}
	// An unversioned network (nothing pinned) keeps "unversioned".
	if same := stampKnVersion(refs, ""); same[0].Version != "unversioned" {
		t.Errorf("empty label changed the ref: %q", same[0].Version)
	}
}

func TestKnVersionStatusFollowsWhatWasRead(t *testing.T) {
	ctx := context.Background()
	if knVersionStatus(ctx) != "unversioned" {
		t.Fatal("no selector must stay unversioned")
	}
	sel, _ := knversion.Resolve("", "")
	ctx = knversion.WithSelector(ctx, sel)
	if knVersionStatus(ctx) != "unversioned" {
		t.Fatal("nothing pinned must stay unversioned")
	}
	h := http.Header{}
	h.Set(knversion.HeaderVersion, "7")
	sel.Observe(200, h, nil)
	if knVersionStatus(ctx) != "versioned" || knVersionLabel(ctx) != "7" {
		t.Fatalf("status = %q, label = %q", knVersionStatus(ctx), knVersionLabel(ctx))
	}
}
