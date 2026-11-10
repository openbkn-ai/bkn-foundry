// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package proxy_context

import (
	"context"
	"net/http"
	"testing"

	oerrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
)

func pinnedCallerContext(t *testing.T, knVersion, observed, revision string) context.Context {
	t.Helper()
	pin, err := interfaces.NewKNVersionPin(knVersion, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := pin.Observe(observed, revision); err != nil {
		t.Fatal(err)
	}
	return interfaces.WithKNVersionPin(callerContext(), pin)
}

func TestResolveAsksForTheBranchOfThePinnedRunningVersion(t *testing.T) {
	access := &proxyAccessStub{mapping: readyMapping()}
	got, err := NewProxyContextResolver(access).Resolve(pinnedCallerContext(t, "", "3", ""), validBinding())
	if err != nil {
		t.Fatal(err)
	}
	if access.binding.Branch != "v3" || got.Binding.Branch != "v3" {
		t.Fatalf("branch asked = %q, resolved = %q, want v3", access.binding.Branch, got.Binding.Branch)
	}

	// A network without versioning keeps resolving main, as it always did.
	access = &proxyAccessStub{mapping: readyMapping()}
	if _, err := NewProxyContextResolver(access).Resolve(pinnedCallerContext(t, "", "", ""), validBinding()); err != nil {
		t.Fatal(err)
	}
	if access.binding != validBinding() {
		t.Fatalf("binding sent for an unversioned network = %#v", access.binding)
	}
}

func TestResolveRefusesTheDraftAndHistoryVersions(t *testing.T) {
	for name, ctx := range map[string]context.Context{
		"draft":   pinnedCallerContext(t, "draft", "draft", "7"),
		"history": pinnedCallerContext(t, "2", "2", ""),
	} {
		t.Run(name, func(t *testing.T) {
			access := &proxyAccessStub{mapping: readyMapping()}
			got, err := NewProxyContextResolver(access).Resolve(ctx, validBinding())
			assertProxyError(t, got, err, http.StatusForbidden, oerrors.OntologyQuery_Proxy_BindingInvalid)
			if access.binding.KNID != "" {
				t.Fatalf("the proxy lookup must not be called for this version: %#v", access.binding)
			}
		})
	}
}
