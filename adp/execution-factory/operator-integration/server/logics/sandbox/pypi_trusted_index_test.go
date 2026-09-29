package sandbox

import (
	"context"
	stderrors "errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
)

func TestResolveTrustedPypiIndex(t *testing.T) {
	trusted := []string{"https://pypi.org/simple", "https://mirror.example.com/pypi/simple/"}
	cases := []struct {
		name     string
		repoURL  string
		trusted  []string
		wantURL  string
		wantFail bool
	}{
		{name: "empty selects first trusted", repoURL: "", trusted: trusted, wantURL: "https://pypi.org"},
		{name: "default list when none configured", repoURL: "https://pypi.org/simple/", wantURL: "https://pypi.org"},
		{name: "simple suffix and slash are equivalent", repoURL: "https://pypi.org", trusted: trusted, wantURL: "https://pypi.org"},
		{name: "host compare is case-insensitive", repoURL: "https://PyPI.org/simple", trusted: trusted, wantURL: "https://pypi.org"},
		{name: "second trusted mirror", repoURL: "https://mirror.example.com/pypi/simple", trusted: trusted, wantURL: "https://mirror.example.com/pypi"},
		{name: "internal host rejected", repoURL: "http://169.254.169.254/latest", trusted: trusted, wantFail: true},
		{name: "look-alike host rejected", repoURL: "https://pypi.org.evil.example/simple", trusted: trusted, wantFail: true},
		{name: "scheme downgrade rejected", repoURL: "http://pypi.org/simple", trusted: trusted, wantFail: true},
		{name: "userinfo rejected", repoURL: "https://pypi.org@evil.example/simple", trusted: trusted, wantFail: true},
		{name: "other path on trusted host rejected", repoURL: "https://pypi.org/admin", trusted: trusted, wantFail: true},
		{name: "non-http scheme rejected", repoURL: "file:///etc/passwd", trusted: trusted, wantFail: true},
		{name: "no valid trusted entry", repoURL: "", trusted: []string{"not a url"}, wantFail: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := resolveTrustedPypiIndex(c.repoURL, c.trusted)
			if c.wantFail {
				if err == nil {
					t.Fatalf("resolveTrustedPypiIndex(%q) = %v, want error", c.repoURL, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveTrustedPypiIndex(%q) error: %v", c.repoURL, err)
			}
			if got.String() != c.wantURL {
				t.Fatalf("resolveTrustedPypiIndex(%q) = %s, want %s", c.repoURL, got, c.wantURL)
			}
		})
	}
}

func TestParsePypiQueriesOnlyTrustedIndex(t *testing.T) {
	var hits atomic.Int32
	var gotPath atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		gotPath.Store(r.URL.Path)
		_, _ = w.Write([]byte(`{"info":{"name":"requests","requires_python":">=3.8"},"releases":{"2.31.0":[{"requires_python":">=3.7"}]}}`))
	}))
	defer srv.Close()
	trusted := []string{srv.URL + "/simple"}

	// A caller-supplied index that is not on the allowlist is rejected before any request.
	_, err := ParsePypi(context.Background(), &ParsePypiReq{
		PypiRepoURL: "http://127.0.0.1:1/internal", PackageName: "requests", PythonVersion: "3.10",
	}, trusted)
	var httpErr *errors.HTTPError
	if !stderrors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusBadRequest {
		t.Fatalf("untrusted index error = %v, want 400", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("untrusted index triggered %d upstream requests, want 0", hits.Load())
	}

	// An invalid package name is rejected before any request.
	_, err = ParsePypi(context.Background(), &ParsePypiReq{PackageName: "../admin", PythonVersion: "3.10"}, trusted)
	if !stderrors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusBadRequest || hits.Load() != 0 {
		t.Fatalf("invalid package name error = %v, hits = %d; want 400 and no request", err, hits.Load())
	}

	// Omitting pypi_repo_url uses the first trusted index.
	resp, err := ParsePypi(context.Background(), &ParsePypiReq{PackageName: "requests", PythonVersion: "3.10"}, trusted)
	if err != nil {
		t.Fatalf("ParsePypi error: %v", err)
	}
	if hits.Load() != 1 || gotPath.Load() != "/pypi/requests/json" {
		t.Fatalf("hits = %d path = %v, want one request to /pypi/requests/json", hits.Load(), gotPath.Load())
	}
	if len(resp.Versions) != 1 || resp.Versions[0] != "2.31.0" {
		t.Fatalf("versions = %v, want [2.31.0]", resp.Versions)
	}
}
