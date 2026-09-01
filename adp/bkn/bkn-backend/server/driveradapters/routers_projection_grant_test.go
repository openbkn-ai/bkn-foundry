// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import "testing"

func TestProjectionGrantVerifierConfigured(t *testing.T) {
	for _, test := range []struct {
		name   string
		issuer string
		aud    string
		keys   string
		want   bool
	}{
		{name: "all unset", want: false},
		{name: "issuer set", issuer: "trace-core-projection", want: true},
		{name: "audience set", aud: "bkn-projection-read", want: true},
		{name: "keys set", keys: "key-1=base64", want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("BKN_TRACE_PROJECTION_GRANT_ISSUER", test.issuer)
			t.Setenv("BKN_TRACE_PROJECTION_GRANT_AUDIENCE", test.aud)
			t.Setenv("BKN_TRACE_PROJECTION_GRANT_PUBLIC_KEYS", test.keys)
			if got := projectionGrantVerifierConfigured(); got != test.want {
				t.Fatalf("projectionGrantVerifierConfigured() = %t, want %t", got, test.want)
			}
		})
	}
}
