// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package ontology_manager

import (
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
)

func TestRunningVersionHeaderMakesAnExplicitNumberTheRunningVersion(t *testing.T) {
	backend := newVersionedBackend(t, func(_ int, w http.ResponseWriter) {
		w.Header().Set(interfaces.HTTPHeaderKNVersion, "3")
		w.Header().Set(interfaces.HTTPHeaderKNRunningVersion, "3")
		objectTypeBody(w)
	})
	ctx, pin := pinnedContext(t, "3", "")
	if _, _, err := backend.accessor.GetObjectType(ctx, "kn-1", "main", "ot-1"); err != nil {
		t.Fatal(err)
	}
	if pin.UsesCallerIdentity() || pin.ProxyBranch() != "v3" {
		t.Fatalf("explicit kn_version=3 equal to the running version must use the proxy: caller=%v", pin.UsesCallerIdentity())
	}
}
