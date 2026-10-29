// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package outboundtls separates registration-time trust from runtime verification.
// This stdlib-only package is mirrored in the three independently built services.
package outboundtls

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var ErrConflict = fault("TLS_TRUST_CONFLICT: trust changed concurrently; read it again")

type Policy struct {
	Fingerprint string `json:"fingerprint" yaml:"fingerprint"`
	SkipVerify  bool   `json:"skip_verify" yaml:"skip_verify"`
}

// Change is an explicit compare-and-swap request on an existing registration.
type Change struct {
	Endpoint string  `json:"endpoint,omitempty"`
	Expected *Policy `json:"expected"`
	Policy   Policy  `json:"policy"`
}

// Apply participates in the registration transaction when a transactional Store
// is supplied. Omitting a change preserves an existing pin, including on upgrade.
func Apply(ctx context.Context, scope, raw string, change *Change, store Store) error {
	if change != nil && change.Endpoint != "" {
		raw = change.Endpoint
	}
	if !strings.HasPrefix(strings.ToLower(raw), "https://") {
		if change != nil {
			return fault("TLS_ENDPOINT_INVALID: TLS policy requires HTTPS")
		}
		return nil
	}
	m := Default()
	if m == nil {
		return fault("TLS_TRUST_UNAVAILABLE")
	}
	copy := *m
	if store != nil {
		copy.store = store
	}
	if change != nil {
		if change.Expected == nil {
			return fault("TLS_POLICY_INVALID: expected policy is required")
		}
		return copy.Replace(ctx, scope, raw, *change.Expected, change.Policy)
	}
	return copy.Enroll(ctx, scope, raw)
}

type Endpoint struct {
	URL    string `json:"url" yaml:"url"`
	Policy `yaml:",inline"`
}

type Config struct {
	CAFile    string     `yaml:"ca_file" envconfig:"OUTBOUND_TLS_CA_FILE"`
	Endpoints []Endpoint `yaml:"endpoints"`
}

// Store must retain records across restarts and serialize concurrent enrollment.
type Store interface {
	Get(context.Context, string, string) (Policy, bool, error)
	Create(context.Context, string, string, Policy) error
	Replace(context.Context, string, string, Policy, Policy) error
}

type Manager struct {
	roots     *x509.CertPool
	store     Store
	endpoints map[string]Policy
}

var current atomic.Pointer[Manager]

func SetDefault(m *Manager) { current.Store(m) }
func Default() *Manager     { return current.Load() }

func New(cfg Config, store Store) (*Manager, error) {
	m := &Manager{store: store, endpoints: make(map[string]Policy)}
	if cfg.CAFile != "" {
		pem, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("TLS_CA_INVALID: %w", err)
		}
		m.roots, err = x509.SystemCertPool()
		if err != nil || m.roots == nil {
			m.roots = x509.NewCertPool()
		}
		if !m.roots.AppendCertsFromPEM(pem) {
			return nil, fault("TLS_CA_INVALID: no CA certificates in bundle")
		}
	}
	for _, endpoint := range cfg.Endpoints {
		origin, err := Origin(endpoint.URL)
		if err != nil {
			return nil, err
		}
		if err = validate(endpoint.Policy); err != nil {
			return nil, err
		}
		if _, ok := m.endpoints[origin]; ok {
			return nil, fault("TLS_POLICY_INVALID: duplicate endpoint")
		}
		m.endpoints[origin] = endpoint.Policy
	}
	return m, nil
}

// Origin deliberately excludes paths: a certificate authenticates a TLS authority.
// Registration scope is a separate key, so exceptions never spread to other registrations.
func Origin(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Scheme, "https") || u.Hostname() == "" || u.Opaque != "" {
		return "", fault("TLS_ENDPOINT_INVALID: expected an HTTPS URL with a valid authority")
	}
	port := u.Port()
	if port == "" {
		port = "443"
	}
	if number, err := strconv.Atoi(port); err != nil || number < 1 || number > 65535 {
		return "", fault("TLS_ENDPOINT_INVALID: invalid port")
	}
	return "https://" + net.JoinHostPort(strings.ToLower(u.Hostname()), port), nil
}

func validate(p Policy) error {
	if p.SkipVerify && p.Fingerprint != "" {
		return fault("TLS_POLICY_INVALID: pin and skip_verify are mutually exclusive")
	}
	if p.Fingerprint != "" {
		b, err := hex.DecodeString(p.Fingerprint)
		if err != nil || len(b) != sha256.Size {
			return fault("TLS_POLICY_INVALID: fingerprint must be a SHA-256 hex digest")
		}
	}
	return nil
}

func Fingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:])
}

func (m *Manager) tlsConfig(origin string, p Policy) (*tls.Config, error) {
	if err := validate(p); err != nil {
		return nil, err
	}
	u, _ := url.Parse(origin)
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: m.roots, ServerName: u.Hostname()}
	// A pinned connection substitutes exact certificate identity for CA/hostname
	// verification. VerifyConnection runs on resumed sessions too.
	cfg.InsecureSkipVerify = p.SkipVerify || p.Fingerprint != ""
	if p.Fingerprint != "" {
		want, _ := hex.DecodeString(p.Fingerprint)
		cfg.VerifyConnection = func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) == 0 {
				return fault("TLS_CERTIFICATE_MISSING")
			}
			cert := state.PeerCertificates[0]
			got := sha256.Sum256(cert.Raw)
			if subtle.ConstantTimeCompare(got[:], want) != 1 {
				return fault("TLS_CERTIFICATE_CHANGED: certificate differs from the saved fingerprint; inspect the endpoint and explicitly re-trust this registration")
			}
			return validTime(cert)
		}
	}
	return cfg, nil
}

func validTime(cert *x509.Certificate) error {
	now := time.Now()
	if now.Before(cert.NotBefore) || now.After(cert.NotAfter) {
		return fault("TLS_CERTIFICATE_EXPIRED: certificate is outside its validity period")
	}
	return nil
}

// Discover performs a bounded, credential-free TLS handshake. It sends no HTTP
// request. Only registration/upgrade/re-trust paths may call this function.
func (m *Manager) Discover(ctx context.Context, raw string) (Policy, error) {
	origin, err := Origin(raw)
	if err != nil {
		return Policy{}, err
	}
	u, _ := url.Parse(origin)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: u.Hostname(), RootCAs: m.roots}
	dialer := tls.Dialer{Config: cfg}
	conn, err := dialer.DialContext(ctx, "tcp", u.Host)
	if err == nil {
		_ = conn.Close()
		return Policy{}, nil
	}
	// A failed verified handshake still exposes the presented chain. Capture it
	// only for this explicit enrollment; the next handshake must prove possession
	// of the pinned certificate's key before any business request is sent.
	var verification *tls.CertificateVerificationError
	if !errors.As(err, &verification) || len(verification.UnverifiedCertificates) == 0 {
		return Policy{}, fault(fmt.Sprintf("TLS_ENROLLMENT_FAILED: could not inspect endpoint certificate; check HTTPS reachability and retry: %v", err))
	}
	cert := verification.UnverifiedCertificates[0]
	if err := validTime(cert); err != nil {
		return Policy{}, err
	}
	return Policy{Fingerprint: Fingerprint(cert)}, nil
}

func (m *Manager) Enroll(ctx context.Context, scope, raw string) error {
	if !strings.HasPrefix(strings.ToLower(raw), "https://") {
		return nil
	}
	origin, err := Origin(raw)
	if err != nil {
		return err
	}
	if m == nil || m.store == nil || scope == "" {
		return fault("TLS_TRUST_UNAVAILABLE: persistent trust store is not configured")
	}
	_, found, err := m.store.Get(ctx, scope, origin)
	if err != nil || found {
		return err
	}
	p, err := m.Discover(ctx, raw)
	if err != nil {
		return err
	}
	return m.store.Create(ctx, scope, origin, p)
}

func (m *Manager) Get(ctx context.Context, scope, raw string) (Policy, error) {
	origin, err := Origin(raw)
	if err != nil {
		return Policy{}, err
	}
	if m == nil || m.store == nil {
		return Policy{}, fault("TLS_TRUST_UNAVAILABLE")
	}
	p, ok, err := m.store.Get(ctx, scope, origin)
	if err != nil {
		return Policy{}, fault("TLS_TRUST_UNAVAILABLE: persistent certificate trust could not be read")
	}
	if !ok {
		// An unregistered redirect/authority gets normal CA + hostname verification.
		// It must never inherit another authority's pin or skip switch.
		return Policy{}, nil
	}
	return p, validate(p)
}

// Replace requires the caller to confirm both the saved policy and the new
// fingerprint. It never fetches a certificate and silently trusts what it finds.
func (m *Manager) Replace(ctx context.Context, scope, raw string, old, next Policy) error {
	if err := validate(old); err != nil {
		return err
	}
	if err := validate(next); err != nil {
		return err
	}
	origin, err := Origin(raw)
	if err != nil {
		return err
	}
	if m == nil || m.store == nil {
		return fault("TLS_TRUST_UNAVAILABLE")
	}
	return m.store.Replace(ctx, scope, origin, old, next)
}

type scopeKey struct{}

func WithScope(ctx context.Context, scope string) context.Context {
	return context.WithValue(ctx, scopeKey{}, scope)
}

// Wrap keeps each registration/policy's connection pool separate. Policy changes
// take effect on the next request, including when an old keep-alive socket exists.
func Wrap(base *http.Transport) http.RoundTripper {
	return &transport{base: base, pools: make(map[string]*http.Transport)}
}

// Bind is used by clients whose requests belong to one registration (MCP/S3).
// A preview may supply a transient policy after its own create-permission check.
func Bind(base *http.Transport, scope, raw string, preview *Policy) http.RoundTripper {
	t := &transport{base: base, pools: make(map[string]*http.Transport), scope: scope}
	if preview != nil {
		t.previewOrigin, _ = Origin(raw)
		p := *preview
		t.preview = &p
	}
	return t
}

type transport struct {
	manager       *Manager
	base          *http.Transport
	mu            sync.Mutex
	pools         map[string]*http.Transport
	scope         string
	previewOrigin string
	preview       *Policy
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	m := Default()
	if req.URL.Scheme != "https" || m == nil {
		return t.base.RoundTrip(req)
	}
	origin, err := Origin(req.URL.String())
	if err != nil {
		return nil, err
	}
	p := m.endpoints[origin]
	scope, _ := req.Context().Value(scopeKey{}).(string)
	if t.scope != "" {
		scope = t.scope
	}
	if scope != "" {
		p, err = m.Get(req.Context(), scope, origin)
		if err != nil {
			return nil, err
		}
	}
	if scope == "" && t.preview != nil && t.previewOrigin == origin {
		p = *t.preview
	}
	key := fmt.Sprintf("%p|%s|%s|%s|%t", m, scope, origin, p.Fingerprint, p.SkipVerify)
	t.mu.Lock()
	if t.manager != m {
		for key, pool := range t.pools {
			pool.CloseIdleConnections()
			delete(t.pools, key)
		}
		t.manager = m
	}
	pool := t.pools[key]
	if pool == nil {
		cfg, configErr := m.tlsConfig(origin, p)
		if configErr != nil {
			t.mu.Unlock()
			return nil, configErr
		}
		pool = t.base.Clone()
		pool.TLSClientConfig = cfg
		if len(t.pools) >= 128 {
			for k, old := range t.pools {
				old.CloseIdleConnections()
				delete(t.pools, k)
			}
		}
		t.pools[key] = pool
	}
	t.mu.Unlock()
	return pool.RoundTrip(req)
}

func (t *transport) CloseIdleConnections() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.base.CloseIdleConnections()
	for _, pool := range t.pools {
		pool.CloseIdleConnections()
	}
}
