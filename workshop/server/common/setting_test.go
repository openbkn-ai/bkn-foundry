// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package common

import "testing"

func TestNormalizeServiceURL(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"http://bkn-safe:3000", "http://bkn-safe:3000", true},
		{" HTTPS://bkn-safe:3000// ", "https://bkn-safe:3000", true},
		{"", "", false},
		{"bkn-safe:3000", "", false},
		{"ftp://bkn-safe", "", false},
		{"http://user:pw@bkn-safe", "", false},
		{"http://bkn-safe?x=1", "", false},
		{"http://bkn-safe#top", "", false},
	}
	for _, c := range cases {
		got, err := NormalizeServiceURL(c.in)
		if (err == nil) != c.ok || got != c.want {
			t.Errorf("NormalizeServiceURL(%q) = %q, %v; want %q, ok=%v", c.in, got, err, c.want, c.ok)
		}
	}
}
