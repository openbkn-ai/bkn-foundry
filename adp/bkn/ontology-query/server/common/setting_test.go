// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package common

import (
	"reflect"
	"testing"
)

func TestGetKNProxyRollout(t *testing.T) {
	t.Setenv("KN_PROXY_MODE", " allowlist ")
	t.Setenv("KN_PROXY_KN_ALLOWLIST", "kn-1, kn-2,,kn-1")

	mode, allowlist := GetKNProxyRollout()
	if mode != "allowlist" || !reflect.DeepEqual(allowlist, []string{"kn-1", "kn-2", "kn-1"}) {
		t.Fatalf("GetKNProxyRollout() = %q, %#v", mode, allowlist)
	}
}
