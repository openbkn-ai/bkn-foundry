// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package common

import (
	"context"
	"crypto/tls"
	"net/http/httptest"
	"testing"
)

func TestPublicOriginFromRequest(t *testing.T) {
	cases := []struct {
		name    string
		host    string
		headers map[string]string
		tls     bool
		want    string
	}{
		{name: "in-cluster call has no browsable host", host: "agent-retrieval:30779", want: ""},
		{name: "proto alone does not make a service-name Host public", host: "agent-retrieval:30779",
			headers: map[string]string{"X-Forwarded-Proto": "https"}, want: ""},
		{name: "forwarded host wins over Host", host: "agent-retrieval:30779",
			headers: map[string]string{"X-Forwarded-Host": "bkn.example.com:8443", "X-Forwarded-Proto": "https"},
			want:    "https://bkn.example.com:8443"},
		{name: "proxy chain keeps the client-most entry", host: "internal",
			headers: map[string]string{"X-Forwarded-Host": "a.example.com, b.internal", "X-Forwarded-Proto": "HTTPS, http"},
			want:    "https://a.example.com"},
		{name: "scheme falls back to TLS", host: "internal", tls: true,
			headers: map[string]string{"X-Forwarded-Host": "10.0.0.8"}, want: "https://10.0.0.8"},
		{name: "IPv6 host", host: "internal",
			headers: map[string]string{"X-Forwarded-Host": "[fd00::1]:443", "X-Forwarded-Proto": "https"},
			want:    "https://[fd00::1]:443"},
		{name: "host with a path is refused", host: "internal",
			headers: map[string]string{"X-Forwarded-Host": "evil.example.com/phish?", "X-Forwarded-Proto": "https"}, want: ""},
		{name: "host with credentials is refused", host: "internal",
			headers: map[string]string{"X-Forwarded-Host": "user@evil.example.com", "X-Forwarded-Proto": "https"}, want: ""},
		{name: "non-web scheme is refused", host: "internal",
			headers: map[string]string{"X-Forwarded-Host": "bkn.example.com", "X-Forwarded-Proto": "javascript"}, want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "http://placeholder/mcp", nil)
			r.Host = tc.host
			for key, value := range tc.headers {
				r.Header.Set(key, value)
			}
			if tc.tls {
				r.TLS = &tls.ConnectionState{}
			}
			if got := PublicOriginFromRequest(r); got != tc.want {
				t.Fatalf("PublicOriginFromRequest() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPublicOriginSurvivesMCPContextCopy(t *testing.T) {
	from := SetPublicOriginToCtx(context.Background(), "https://bkn.example.com")
	onto := CopyRequestScopedValues(from, context.Background())
	if got := GetPublicOriginFromCtx(onto); got != "https://bkn.example.com" {
		t.Fatalf("origin after copy = %q", got)
	}
	if got := GetPublicOriginFromCtx(SetPublicOriginToCtx(context.Background(), "")); got != "" {
		t.Fatalf("empty origin must not be stored, got %q", got)
	}
}
