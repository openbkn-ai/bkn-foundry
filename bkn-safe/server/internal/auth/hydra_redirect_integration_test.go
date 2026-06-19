//go:build integration

package auth

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"testing"
)

// TestHydraRedirectLive exercises the redirect_uri admin methods against a REAL
// hydra admin API — the path the stubbed httpapi unit tests cannot cover.
//
// Bring up the dev stack and point the test at hydra's admin port:
//
//	cd bkn-safe/dev && docker compose up -d postgres hydra-migrate hydra
//	HYDRA_ADMIN_URL=http://127.0.0.1:4445 go test -tags integration \
//	  -run TestHydraRedirectLive ./internal/auth/ -v
//
// Skips when HYDRA_ADMIN_URL is unset so the normal suite stays hermetic.
func TestHydraRedirectLive(t *testing.T) {
	base := os.Getenv("HYDRA_ADMIN_URL")
	if base == "" {
		t.Skip("HYDRA_ADMIN_URL unset — start bkn-safe/dev compose and set it to run this")
	}

	const clientID = "it-redirect-test"
	const initial = "https://init.example/callback"
	const added = "http://localhost:8000/studio/callback"

	createClient(t, base, clientID, initial)
	t.Cleanup(func() { deleteClient(t, base, clientID) })

	h := NewHydraAdmin(base)
	ctx := context.Background()

	// baseline
	got, err := h.GetClientRedirectURIs(ctx, clientID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !has(got, initial) {
		t.Fatalf("baseline missing %q: %v", initial, got)
	}

	// add
	got, err = h.AddClientRedirectURI(ctx, clientID, added)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if !has(got, added) || !has(got, initial) {
		t.Fatalf("after add want both, got %v", got)
	}

	// add again -> idempotent (no duplicate)
	got, err = h.AddClientRedirectURI(ctx, clientID, added)
	if err != nil {
		t.Fatalf("re-add: %v", err)
	}
	if count(got, added) != 1 {
		t.Fatalf("re-add not idempotent, %q appears %d times: %v", added, count(got, added), got)
	}

	// persisted? re-read
	got, err = h.GetClientRedirectURIs(ctx, clientID)
	if err != nil {
		t.Fatalf("get after add: %v", err)
	}
	if !has(got, added) {
		t.Fatalf("add did not persist: %v", got)
	}

	// remove
	got, err = h.RemoveClientRedirectURI(ctx, clientID, added)
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if has(got, added) {
		t.Fatalf("after remove still present: %v", got)
	}
	if !has(got, initial) {
		t.Fatalf("remove dropped the wrong uri: %v", got)
	}
}

func createClient(t *testing.T, base, id, redirect string) {
	t.Helper()
	deleteClient(t, base, id) // idempotent: clear any leftover from a prior run
	body := `{"client_id":"` + id + `","grant_types":["authorization_code"],` +
		`"response_types":["code"],"token_endpoint_auth_method":"none",` +
		`"redirect_uris":["` + redirect + `"]}`
	req, _ := http.NewRequest(http.MethodPost, base+"/admin/clients", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create client: hydra %d: %s", resp.StatusCode, b)
	}
}

func deleteClient(t *testing.T, base, id string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodDelete, base+"/admin/clients/"+id, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}

func has(s []string, v string) bool { return count(s, v) > 0 }

func count(s []string, v string) int {
	n := 0
	for _, x := range s {
		if x == v {
			n++
		}
	}
	return n
}
