// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.
package main

import (
	"strings"
	"testing"
)

func TestRecheckIDsRequireExplicitUniqueBoundedTargets(t *testing.T) {
	for _, s := range []string{`[]`, `[""]`, `["a","a"]`, `[" a"]`, `["a"] {}`, `["` + strings.Repeat("x", 129) + `"]`} {
		if _, err := readIDs(strings.NewReader(s)); err == nil {
			t.Fatalf("accepted %s", s)
		}
	}
	ids, err := readIDs(strings.NewReader(`["int_a","int_b"]`))
	if err != nil || len(ids) != 2 {
		t.Fatalf("ids=%v err=%v", ids, err)
	}
}
