// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package license

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/config"
)

// ErrActivatedElsewhere is the issuer's 409: the license is already bound to a
// different instance fingerprint. Recovery is an admin unbind on the issuer
// side, not a retry.
var ErrActivatedElsewhere = errors.New("license: already activated by another instance")

// httpClientFor builds the egress client towards the issuer. The issuer
// currently serves a self-signed certificate on a bare IP, so the trust root is
// configurable (extra CA file) or — logged loudly — verification can be skipped.
// Either way license authenticity is untouched: certificates are Ed25519-signed,
// a hijacked transport can only deny renewal.
func httpClientFor(cfg config.LicenseConfig) (*http.Client, error) {
	tlsCfg := &tls.Config{}
	if cfg.CAFile != "" {
		pem, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("license: read ca_file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("license: ca_file %s contains no usable PEM certificate", cfg.CAFile)
		}
		tlsCfg.RootCAs = pool
	}
	if cfg.InsecureSkipVerify {
		tlsCfg.InsecureSkipVerify = true
		slog.Warn("license: TLS verification towards the license server is DISABLED (insecure_skip_verify); remove once the issuer has a trusted certificate")
	}
	return &http.Client{
		Timeout:   30 * time.Second,
		Transport: &http.Transport{TLSClientConfig: tlsCfg},
	}, nil
}

// activate reports this instance to the issuer and returns the reissued
// license (hw_fingerprint embedded). 409 maps to ErrActivatedElsewhere.
func activate(ctx context.Context, hc *http.Client, serverURL, licText, fp string) (string, error) {
	body, _ := json.Marshal(map[string]string{"license": licText, "instance_fp": fp})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, serverURL+"/api/licenses/activate", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("license: activate request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		License string `json:"license"`
		Error   string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("license: activate response: %w", err)
	}
	if resp.StatusCode == http.StatusConflict {
		if out.Error != "" {
			return "", fmt.Errorf("%w: %s", ErrActivatedElsewhere, out.Error)
		}
		return "", ErrActivatedElsewhere
	}
	if resp.StatusCode != http.StatusOK || out.License == "" {
		if out.Error != "" {
			return "", fmt.Errorf("license: activate refused: %s", out.Error)
		}
		return "", fmt.Errorf("license: activate http %d", resp.StatusCode)
	}
	return out.License, nil
}

// Issuer binding answers. Anything else the issuer might say is not one of
// these and is treated as "no answer".
const (
	BindingBound   = "bound"
	BindingUnbound = "unbound"
	BindingRevoked = "revoked"
)

// checkBinding asks the issuer whether licText is still bound to fp. It
// returns an error for every reply that is not a definitive 200 — transport
// failures, rate limits, an older issuer without the endpoint (404) — so the
// caller can leave its state alone: an issuer outage must never read as an
// unbind.
func checkBinding(ctx context.Context, hc *http.Client, serverURL, licText, fp string) (string, error) {
	body, _ := json.Marshal(map[string]string{"license": licText, "instance_fp": fp})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, serverURL+"/api/licenses/status", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("license: binding status request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("license: binding status http %d", resp.StatusCode)
	}
	var out struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("license: binding status response: %w", err)
	}
	switch out.Status {
	case BindingBound, BindingUnbound, BindingRevoked:
		return out.Status, nil
	}
	return "", fmt.Errorf("license: binding status %q not understood", out.Status)
}
