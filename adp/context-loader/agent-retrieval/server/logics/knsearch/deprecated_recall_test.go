// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package knsearch

import (
	"strings"
	"testing"

	"github.com/bytedance/sonic"
)

// Recall asks bkn-backend to leave deprecated concepts out.
func TestCoarseRecallExcludesDeprecatedConcepts(t *testing.T) {
	req := (&localSearchImpl{}).buildCoarseRecallQuery("kn1", "订单", 20, nil)
	body, err := sonic.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"exclude_deprecated":true`) {
		t.Fatalf("recall request = %s, want exclude_deprecated true", body)
	}
}
