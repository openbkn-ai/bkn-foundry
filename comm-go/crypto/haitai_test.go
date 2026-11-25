// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package crypto

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHaitaiRequestsJSON(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		body       map[string]string
	}{
		{"decrypt", "/ded-service/api/decrypt", map[string]string{"data": "<密文>&", "dataKey": "test-key"}},
		{"sign", "/ded-service/api/sign", map[string]string{"data": "<签名>&"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tc.path || r.Method != http.MethodPost {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				data, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("read body: %v", err)
				}
				var body map[string]string
				if err := json.Unmarshal(data, &body); err != nil {
					t.Errorf("decode: %v", err)
				}
				cipher := haitaiCipher{key: "00112233"}
				if got, want := r.Header.Get("hmac"), cipher.hmacSha256(data); got != want {
					t.Errorf("HMAC does not match the transmitted body: got %q, want %q", got, want)
				}
				for key, want := range tc.body {
					if body[key] != want {
						t.Errorf("%s = %q, want %q", key, body[key], want)
					}
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"data":"result"}`))
			}))
			defer server.Close()
			ci := haitaiCipher{key: "00112233", host: server.URL, realm: "test", dataKey: "test-key"}
			var got string
			var err error
			if tc.name == "decrypt" {
				got, err = ci.Decrypt(tc.body["data"])
			} else {
				got, err = ci.Signature(tc.body["data"])
			}
			if err != nil || got != "result" {
				t.Fatalf("result = %q, error = %v", got, err)
			}
		})
	}
}
