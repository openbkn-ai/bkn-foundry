package outboundtls

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type memoryStore struct {
	mu     sync.Mutex
	values map[string]Policy
	fail   bool
}

func newMemory() *memoryStore { return &memoryStore{values: make(map[string]Policy)} }
func (s *memoryStore) Get(_ context.Context, scope, origin string) (Policy, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail {
		return Policy{}, false, errors.New("store unavailable")
	}
	p, ok := s.values[scope+origin]
	return p, ok, nil
}
func (s *memoryStore) Create(_ context.Context, scope, origin string, p Policy) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := scope + origin
	if old, ok := s.values[k]; ok && old != p {
		return ErrConflict
	}
	s.values[k] = p
	return nil
}
func (s *memoryStore) Replace(_ context.Context, scope, origin string, old, next Policy) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := scope + origin
	if got, ok := s.values[k]; !ok || got != old {
		return ErrConflict
	}
	s.values[k] = next
	return nil
}

func certificate(t *testing.T, hostname string, expired bool) tls.Certificate {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	cert := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: hostname}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true}
	if ip := net.ParseIP(hostname); ip != nil {
		cert.IPAddresses = []net.IP{ip}
	} else {
		cert.DNSNames = []string{hostname}
	}
	if expired {
		cert.NotBefore = time.Now().Add(-2 * time.Hour)
		cert.NotAfter = time.Now().Add(-time.Hour)
	}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}
}

func tlsServer(t *testing.T, cert tls.Certificate) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	calls := new(atomic.Int32)
	s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(200) }))
	s.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	s.StartTLS()
	t.Cleanup(s.Close)
	return s, calls
}

func useManager(t *testing.T, m *Manager) *http.Client {
	t.Helper()
	old := Default()
	SetDefault(m)
	t.Cleanup(func() { SetDefault(old) })
	c := &http.Client{Transport: Wrap(&http.Transport{}), Timeout: 3 * time.Second}
	t.Cleanup(c.CloseIdleConnections)
	return c
}

func request(c *http.Client, scope, raw string) error {
	r, _ := http.NewRequestWithContext(WithScope(context.Background(), scope), http.MethodGet, raw, nil)
	resp, err := c.Do(r)
	if resp != nil {
		_ = resp.Body.Close()
	}
	return err
}

func TestEnrollmentPinRotationAndRestart(t *testing.T) {
	ctx := context.Background()
	first := certificate(t, "ingress.local", false)
	upstream, calls := tlsServer(t, first)
	store := newMemory()
	m, _ := New(Config{}, store)
	c := useManager(t, m)
	if err := request(c, "mcp:one", upstream.URL); err == nil {
		t.Fatal("unregistered request accepted")
	}
	if err := m.Enroll(ctx, "mcp:one", upstream.URL); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("enrollment sent HTTP credentials/business request")
	}
	p, err := m.Get(ctx, "mcp:one", upstream.URL)
	if err != nil || p.Fingerprint != Fingerprint(first.Leaf) {
		t.Fatalf("saved policy: %+v %v", p, err)
	}
	if err := request(c, "mcp:one", upstream.URL); err != nil {
		t.Fatal(err)
	}
	// A different registration cannot reuse the first registration's trust.
	if err := request(c, "mcp:two", upstream.URL); err == nil {
		t.Fatal("trust leaked between registrations")
	}
	restarted, _ := New(Config{}, store)
	SetDefault(restarted)
	if err := request(c, "mcp:one", upstream.URL); err != nil {
		t.Fatal("restart lost pin:", err)
	}
	second := certificate(t, "ingress.local", false)
	// The recorded pin must reject another certificate even when the server is
	// publicly trusted later; it may never silently fall back to another policy.
	origin, _ := Origin(upstream.URL)
	cfg, _ := m.tlsConfig(origin, p)
	if err := cfg.VerifyConnection(tls.ConnectionState{PeerCertificates: []*x509.Certificate{second.Leaf}}); err == nil || !strings.Contains(err.Error(), "TLS_CERTIFICATE_CHANGED") {
		t.Fatalf("rotation accepted: %v", err)
	}
	if err := m.Enroll(ctx, "mcp:one", upstream.URL); err != nil {
		t.Fatal(err)
	}
	after, _ := m.Get(ctx, "mcp:one", upstream.URL)
	if after != p {
		t.Fatal("repeated upgrade replaced an existing pin")
	}
	// Explicit policy changes must invalidate even an existing keep-alive pool.
	next := Policy{Fingerprint: Fingerprint(second.Leaf)}
	if err := m.Replace(ctx, "mcp:one", upstream.URL, p, next); err != nil {
		t.Fatal(err)
	}
	before := calls.Load()
	if err := request(c, "mcp:one", upstream.URL); err == nil {
		t.Fatal("old pooled connection survived policy replacement")
	}
	if calls.Load() != before {
		t.Fatal("wrong certificate received HTTP request")
	}
	if err := m.Replace(ctx, "mcp:one", upstream.URL, p, Policy{}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale re-trust overwrote policy: %v", err)
	}
}

func TestCAAndDefaultVerification(t *testing.T) {
	cert := certificate(t, "127.0.0.1", false)
	upstream, calls := tlsServer(t, cert)
	m, _ := New(Config{}, nil)
	c := useManager(t, m)
	if err := request(c, "", upstream.URL); err == nil || calls.Load() != 0 {
		t.Fatal("default verification disabled")
	}
	bundle := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(bundle, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]}), 0600); err != nil {
		t.Fatal(err)
	}
	trusted, err := New(Config{CAFile: bundle}, nil)
	if err != nil {
		t.Fatal(err)
	}
	SetDefault(trusted)
	if err := request(c, "", upstream.URL); err != nil {
		t.Fatal("custom CA rejected:", err)
	}
	p, err := trusted.Discover(context.Background(), upstream.URL)
	if err != nil || p != (Policy{}) {
		t.Fatalf("trusted CA pinned unnecessarily: %+v %v", p, err)
	}
	for _, bad := range []string{"missing.pem", filepath.Join(t.TempDir(), "bad.pem")} {
		if strings.HasSuffix(bad, "bad.pem") {
			_ = os.WriteFile(bad, []byte("invalid"), 0600)
		}
		if _, err := New(Config{CAFile: bad}, nil); err == nil {
			t.Fatal("invalid CA silently ignored")
		}
	}
}

func TestExplicitSkipIsScopedAndStoreFailureIsClosed(t *testing.T) {
	upstream, _ := tlsServer(t, certificate(t, "ingress.local", false))
	other, _ := tlsServer(t, certificate(t, "ingress.local", false))
	store := newMemory()
	origin, _ := Origin(upstream.URL)
	_ = store.Create(context.Background(), "mcp:one", origin, Policy{SkipVerify: true})
	m, _ := New(Config{}, store)
	c := useManager(t, m)
	if err := request(c, "mcp:one", upstream.URL); err != nil {
		t.Fatal(err)
	}
	if err := request(c, "mcp:one", other.URL); err == nil {
		t.Fatal("skip leaked across authorities")
	}
	if err := request(c, "mcp:two", upstream.URL); err == nil {
		t.Fatal("skip leaked across registrations")
	}
	store.fail = true
	if err := request(c, "mcp:one", upstream.URL); err == nil {
		t.Fatal("store failure reused a cached trusted connection")
	}
}

func TestRejectInvalidPinsAndExpiredCertificates(t *testing.T) {
	m, _ := New(Config{}, newMemory())
	for _, p := range []Policy{{Fingerprint: "xyz"}, {Fingerprint: strings.Repeat("0", 64), SkipVerify: true}} {
		if _, err := m.tlsConfig("https://localhost:443", p); err == nil {
			t.Fatal("invalid policy accepted")
		}
	}
	upstream, _ := tlsServer(t, certificate(t, "ingress.local", true))
	if _, err := m.Discover(context.Background(), upstream.URL); err == nil {
		t.Fatal("expired certificate enrolled")
	}
	for _, raw := range []string{"http://example.com", "https://example.com:bad", "https:///"} {
		if _, err := Origin(raw); err == nil {
			t.Fatal("invalid origin accepted:", raw)
		}
	}
	credentialsOrigin, err := Origin("https://user:secret@example.com/path")
	if err != nil || credentialsOrigin != "https://example.com:443" {
		t.Fatal("enrollment did not strip URL credentials from the saved authority")
	}
	a, _ := Origin("https://EXAMPLE.COM/path")
	b, _ := Origin("https://example.com:443/other")
	if a != b {
		t.Fatal("default port/case normalization differs")
	}
}

func TestRealCertificateRotationRequiresExplicitRetrust(t *testing.T) {
	first := certificate(t, "ingress.local", false)
	second := certificate(t, "ingress.local", false)
	var active atomic.Pointer[tls.Certificate]
	active.Store(&first)
	var calls atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(200) }))
	server.TLS = &tls.Config{GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
		return &tls.Config{Certificates: []tls.Certificate{*active.Load()}}, nil
	}}
	server.StartTLS()
	defer server.Close()
	store := newMemory()
	m, _ := New(Config{}, store)
	client := useManager(t, m)
	ctx := context.Background()
	if err := m.Enroll(ctx, "mcp:rotating", server.URL); err != nil {
		t.Fatal(err)
	}
	if err := request(client, "mcp:rotating", server.URL); err != nil {
		t.Fatal(err)
	}
	old, _ := m.Get(ctx, "mcp:rotating", server.URL)
	active.Store(&second)
	server.CloseClientConnections()
	if err := request(client, "mcp:rotating", server.URL); err == nil || !strings.Contains(err.Error(), "TLS_CERTIFICATE_CHANGED") {
		t.Fatalf("changed certificate accepted: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatal("changed certificate received business traffic")
	}
	if err := m.Enroll(ctx, "mcp:rotating", server.URL); err != nil {
		t.Fatal(err)
	}
	if err := request(client, "mcp:rotating", server.URL); err == nil {
		t.Fatal("ordinary enrollment replaced an existing pin")
	}
	next, err := m.Discover(ctx, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if next.Fingerprint != Fingerprint(second.Leaf) {
		t.Fatal("inspection returned the wrong certificate")
	}
	if err := m.Replace(ctx, "mcp:rotating", server.URL, old, next); err != nil {
		t.Fatal(err)
	}
	if err := request(client, "mcp:rotating", server.URL); err != nil {
		t.Fatal("explicit re-trust did not recover:", err)
	}
	if calls.Load() != 2 {
		t.Fatal("inspection or failed handshake sent HTTP traffic")
	}
}

func TestRedirectCannotReuseAnotherAuthoritysSkipPolicy(t *testing.T) {
	target, calls := tlsServer(t, certificate(t, "ingress.local", false))
	redirect := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer redirect.Close()
	store := newMemory()
	origin, _ := Origin(redirect.URL)
	_ = store.Create(context.Background(), "toolbox:one", origin, Policy{SkipVerify: true})
	m, _ := New(Config{}, store)
	client := useManager(t, m)
	if err := request(client, "toolbox:one", redirect.URL); err == nil || calls.Load() != 0 {
		t.Fatalf("redirect inherited an exception: err=%v calls=%d", err, calls.Load())
	}
}

func TestBoundClientCannotUseAnotherRegistrationsContext(t *testing.T) {
	cert := certificate(t, "ingress.local", false)
	server, calls := tlsServer(t, cert)
	store := newMemory()
	origin, _ := Origin(server.URL)
	_ = store.Create(context.Background(), "storage:one", origin, Policy{Fingerprint: Fingerprint(cert.Leaf)})
	_ = store.Create(context.Background(), "storage:two", origin, Policy{Fingerprint: strings.Repeat("0", 64)})
	m, _ := New(Config{}, store)
	_ = useManager(t, m)
	good := &http.Client{Transport: Bind(&http.Transport{}, "storage:one", server.URL, nil)}
	defer good.CloseIdleConnections()
	bad := &http.Client{Transport: Bind(&http.Transport{}, "storage:two", server.URL, nil)}
	defer bad.CloseIdleConnections()
	if err := request(good, "storage:two", server.URL); err != nil {
		t.Fatal("request context replaced the bound registration:", err)
	}
	if err := request(bad, "storage:one", server.URL); err == nil {
		t.Fatal("request context borrowed another registration's pin")
	}
	if calls.Load() != 1 {
		t.Fatal("wrong bound registration received HTTP traffic")
	}
}
