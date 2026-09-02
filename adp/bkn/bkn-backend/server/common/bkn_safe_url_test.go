// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package common

import (
	"strings"
	"testing"
)

func TestNormalizeBknSafeURL(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "HTTP", raw: "http://bkn-safe:3000", want: "http://bkn-safe:3000"},
		{name: "HTTPS and whitespace", raw: "  https://safe.example.com/base/  ", want: "https://safe.example.com/base"},
		{name: "case-insensitive scheme", raw: "HTTP://bkn-safe:3000/", want: "http://bkn-safe:3000"},
		{name: "IPv6 host", raw: "http://[::1]:3000/", want: "http://[::1]:3000"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeBknSafeURL(tt.raw)
			if err != nil {
				t.Fatalf("NormalizeBknSafeURL() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("NormalizeBknSafeURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNormalizeBknSafeURLRejectsInvalidValuesWithoutEchoingThem(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{name: "empty", raw: ""},
		{name: "whitespace", raw: "   "},
		{name: "relative", raw: "bkn-safe:3000"},
		{name: "scheme relative", raw: "//bkn-safe:3000"},
		{name: "missing host", raw: "http:///api"},
		{name: "unsupported scheme", raw: "ftp://bkn-safe:3000"},
		{name: "credentials", raw: "http://user:secret@bkn-safe:3000"},
		{name: "query", raw: "http://bkn-safe:3000?token=secret"},
		{name: "empty query", raw: "http://bkn-safe:3000?"},
		{name: "fragment", raw: "http://bkn-safe:3000#secret"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeBknSafeURL(tt.raw)
			if err == nil {
				t.Fatalf("NormalizeBknSafeURL(%q) error = nil", tt.raw)
			}
			if got != "" {
				t.Fatalf("NormalizeBknSafeURL(%q) = %q, want empty", tt.raw, got)
			}
			if tt.raw != "" && strings.Contains(err.Error(), tt.raw) {
				t.Fatalf("error exposes configured URL: %v", err)
			}
		})
	}
}
