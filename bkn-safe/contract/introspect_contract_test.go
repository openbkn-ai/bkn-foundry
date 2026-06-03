// Package contract holds executable freeze tests for the ISF replacement.
//
// introspect_contract_test.go proves that the frozen hydra introspect golden
// responses (docs/isf-replacement/contracts/introspect/*.json) parse through
// the REAL kweaver-go-lib hydra client into the expected TokenIntrospectInfo.
//
// Why this matters: the lib's Introspect() parses with unchecked type
// assertions (no nil checks). A missing claim panics. bkn-safe, acting as
// hydra's consent provider, MUST inject the session ext claims so introspect
// returns these exact shapes — otherwise every protected request panics.
package contract

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/kweaver-ai/kweaver-go-lib/hydra"
)

// goldenDir resolves docs/isf-replacement/contracts/introspect relative to
// this test file, so the test reads the single frozen source (no duplication).
func goldenDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// bkn-safe/contract/<file> -> repo root is two dirs up.
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")
	return filepath.Join(root, "docs", "isf-replacement", "contracts", "introspect")
}

// newHydraServing spins an httptest server that answers the hydra admin
// introspect endpoint with the given golden JSON, and returns a hydra client
// pointed at it.
func newHydraServing(t *testing.T, goldenJSON []byte) (hydra.Hydra, func()) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/admin/oauth2/introspect" {
			http.Error(w, "unexpected path: "+r.URL.Path, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(goldenJSON)
	}))

	host, portStr, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		srv.Close()
		t.Fatalf("split host port: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		srv.Close()
		t.Fatalf("atoi port: %v", err)
	}

	h := hydra.NewHydra(hydra.HydraAdminSetting{
		HydraAdminProcotol: "http",
		HydraAdminHost:     host,
		HydraAdminPort:     port,
	})
	return h, srv.Close
}

func loadGolden(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(goldenDir(t), name))
	if err != nil {
		t.Fatalf("read golden %s: %v", name, err)
	}
	return b
}

// TestIntrospectGoldenParses asserts each frozen golden parses without panic
// into the expected TokenIntrospectInfo. This is the bkn-safe acceptance bar.
func TestIntrospectGoldenParses(t *testing.T) {
	cases := []struct {
		file string
		want hydra.TokenIntrospectInfo
	}{
		{
			file: "user.json",
			want: hydra.TokenIntrospectInfo{
				Active:     true,
				VisitorID:  "f6ae435c-0000-0000-0000-000000000000",
				Scope:      "openid offline",
				ClientID:   "kweaver-cli",
				VisitorTyp: hydra.VisitorType_User,
				LoginIP:    "10.0.0.5",
				Udid:       "device-abc-123",
				AccountTyp: hydra.AccountType_Other,
				ClientTyp:  hydra.ClientType_Web,
			},
		},
		{
			file: "app.json",
			want: hydra.TokenIntrospectInfo{
				Active:     true,
				VisitorID:  "ci-runner-app",
				Scope:      "authz.write",
				ClientID:   "ci-runner-app",
				VisitorTyp: hydra.VisitorType_App,
			},
		},
		{
			file: "anonymous.json",
			want: hydra.TokenIntrospectInfo{
				Active:     true,
				VisitorID:  "anon-0001",
				Scope:      "",
				ClientID:   "public-web",
				VisitorTyp: hydra.VisitorType_Anonymous,
				ClientTyp:  hydra.ClientType_Web,
			},
		},
		{
			file: "inactive.json",
			want: hydra.TokenIntrospectInfo{Active: false},
		},
	}

	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			h, closeFn := newHydraServing(t, loadGolden(t, tc.file))
			defer closeFn()

			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("lib panicked parsing %s — contract violated: %v", tc.file, r)
				}
			}()

			got, err := h.Introspect(context.Background(), "dummy-token")
			if err != nil {
				t.Fatalf("Introspect(%s) error: %v", tc.file, err)
			}
			if got != tc.want {
				t.Errorf("Introspect(%s) mismatch\n got: %+v\nwant: %+v", tc.file, got, tc.want)
			}
		})
	}
}

// TestIntrospectMissingExtPanics proves the contract is real, not cosmetic:
// a user-type token missing the required ext claims panics the lib. bkn-safe
// must never emit such a response for a non-app token.
func TestIntrospectMissingExtPanics(t *testing.T) {
	// user-type (sub != client_id) but ext.login_ip / udid / etc. absent.
	bad := []byte(`{"active":true,"sub":"u1","scope":"","client_id":"c1","ext":{"visitor_type":"user"}}`)
	h, closeFn := newHydraServing(t, bad)
	defer closeFn()

	panicked := func() (p bool) {
		defer func() {
			if r := recover(); r != nil {
				p = true
			}
		}()
		_, _ = h.Introspect(context.Background(), "dummy-token")
		return false
	}()

	if !panicked {
		t.Fatal("expected lib to panic on user token missing ext claims; it did not — " +
			"contract assumption about required ext fields is wrong, update the freeze spec")
	}
}
