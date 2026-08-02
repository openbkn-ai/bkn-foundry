// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.

package bkntrace

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResolveAccessProfile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("Authorization header = %q", r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/api/safe/v1/me":
			_, _ = w.Write([]byte(`{"id":"user-1","enabled":true,"roles":["normal_user","admin","admin","custom"]}`))
		case "/api/safe/v1/me/permissions":
			_, _ = w.Write([]byte(`{"permissions":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("BKN_SAFE_BASE_URL", server.URL)
	t.Setenv("BKN_SAFE_URL", "")

	profile, err := ResolveAccessProfile(context.Background(), "Bearer test-token", "user-1")
	if err != nil {
		t.Fatalf("ResolveAccessProfile() error = %v", err)
	}
	if !profile.IsOutboxAdmin() {
		t.Fatal("IsOutboxAdmin() = false, want true")
	}
	if len(profile.Roles) != 2 || profile.Roles[0] != "admin" || profile.Roles[1] != "normal_user" {
		t.Fatalf("Roles = %#v, want sorted built-in roles", profile.Roles)
	}
}

func TestResolveAccessProfileRejectsDisabledIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/safe/v1/me" {
			_, _ = w.Write([]byte(`{"id":"user-1","enabled":false}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	t.Setenv("BKN_SAFE_BASE_URL", server.URL)
	t.Setenv("BKN_SAFE_URL", "")

	_, err := ResolveAccessProfile(context.Background(), "Bearer test-token", "user-1")
	if !errors.Is(err, ErrAccessProfileDenied) {
		t.Fatalf("ResolveAccessProfile() error = %v, want ErrAccessProfileDenied", err)
	}
}
