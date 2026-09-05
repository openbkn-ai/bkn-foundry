// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"strings"
	"testing"

	"github.com/bytedance/sonic"

	"ontology-query/interfaces"
)

func TestPublicActionExecutionRedactsProxySnapshot(t *testing.T) {
	execution := &interfaces.ActionExecution{
		ID: "execution-1",
		Proxy: &interfaces.AccountInfo{
			ID: "managed-proxy-1", Type: interfaces.ProxyAccountTypeApp,
		},
		ProxyVersion:      7,
		ProxyModelVersion: "model-v7",
		ProxyPermissionSnapshot: []interfaces.PermissionRequirement{{
			ResourceType: interfaces.PermissionResourceTypeToolBox,
			ResourceID:   "box-secret",
			Operation:    interfaces.PermissionOperationExecute,
		}},
	}

	redacted := redactActionExecutionProxyContext(execution)
	wire, err := sonic.Marshal(redacted)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"managed-proxy-1", "proxy_subject", "proxy_version", "proxy_model_version",
		"proxy_permission_snapshot", "box-secret",
	} {
		if strings.Contains(string(wire), forbidden) {
			t.Fatalf("public execution response leaks %q: %s", forbidden, wire)
		}
	}
	if execution.Proxy == nil || execution.Proxy.ID != "managed-proxy-1" || execution.ProxyVersion != 7 {
		t.Fatalf("redaction mutated the persisted execution: %#v", execution)
	}
}

func TestPublicActionExecutionListRedactsCopiesOnly(t *testing.T) {
	list := &interfaces.ActionExecutionList{Entries: []interfaces.ActionExecution{{
		ID: "execution-1", Proxy: &interfaces.AccountInfo{ID: "managed-proxy-1"}, ProxyVersion: 7,
	}}}

	redacted := redactActionExecutionListProxyContext(list)
	if redacted.Entries[0].Proxy != nil || redacted.Entries[0].ProxyVersion != 0 {
		t.Fatalf("list proxy context was not redacted: %#v", redacted)
	}
	if list.Entries[0].Proxy == nil || list.Entries[0].Proxy.ID != "managed-proxy-1" || list.Entries[0].ProxyVersion != 7 {
		t.Fatalf("list redaction mutated the persisted result: %#v", list)
	}
}
