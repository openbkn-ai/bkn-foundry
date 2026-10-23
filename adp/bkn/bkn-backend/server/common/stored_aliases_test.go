// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package common

import (
	"context"
	"reflect"
	"testing"
)

// Rows written before f_aliases existed read NULL; a definition without aliases writes NULL too.
func TestStoredAliases(t *testing.T) {
	for _, aliases := range [][]string{nil, {}} {
		if v, err := EncodeAliases(aliases); err != nil || v != nil {
			t.Fatalf("no aliases encode as NULL, got %#v, %v", v, err)
		}
	}
	v, err := EncodeAliases([]string{"订货单", "PO"})
	if err != nil || v != `["订货单","PO"]` {
		t.Fatalf("got %#v, %v", v, err)
	}

	ctx := context.Background()
	for _, raw := range [][]byte{nil, []byte(""), []byte("[]"), []byte("null"), []byte("not json")} {
		if got := DecodeStoredAliases(ctx, raw); got != nil {
			t.Fatalf("%q decodes as %#v, want no aliases", raw, got)
		}
	}
	if got := DecodeStoredAliases(ctx, []byte(`["订货单","PO"]`)); !reflect.DeepEqual(got, []string{"订货单", "PO"}) {
		t.Fatalf("got %#v", got)
	}
}
