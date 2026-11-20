// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTokenExpiryReadsExpFromIntrospection(t *testing.T) {
	var sent string
	active := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		sent = r.PostForm.Get("token")
		if r.URL.Path != "/admin/oauth2/introspect" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if !active {
			_, _ = w.Write([]byte(`{"active":false}`))
			return
		}
		_, _ = w.Write([]byte(`{"active":true,"sub":"u1","exp":1795161600}`))
	}))
	defer server.Close()
	haa := &hydraAuthAccess{adminURL: server.URL, client: server.Client()}

	expires, err := haa.TokenExpiry(context.Background(), "Bearer ory_at_x")
	if err != nil || !expires.Equal(time.Unix(1795161600, 0)) || sent != "ory_at_x" {
		t.Fatal(expires, err, sent)
	}
	active = false
	if _, err = haa.TokenExpiry(context.Background(), "Bearer ory_at_x"); err == nil {
		t.Fatal("an inactive token has an expiry")
	}
}
