// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

// SSOExchangeRegistration describes only bkn-safe's dedicated JWT bearer client.
// Provider credentials and assertion private keys never go to Hydra's admin API.
type SSOExchangeRegistration struct {
	ClientID     string
	ClientSecret string `json:"-"`
	Issuer       string
	KeyID        string
	Scopes       []string
	PublicJWK    map[string]any
}

// ReconcileSSOExchange initializes the client and renews its issuer trust. The
// caller serializes calls and bounds ctx. It never touches other clients or
// trusts with a different issuer/key. Retrying a partially completed run is safe.
func (h *HydraAdmin) ReconcileSSOExchange(ctx context.Context, cfg SSOExchangeRegistration) error {
	clientPath := "/admin/clients/" + url.PathEscape(cfg.ClientID)
	status, err := h.ssoAdmin(ctx, http.MethodGet, clientPath, nil, nil)
	if err != nil {
		return err
	}
	payload := map[string]any{
		"client_id": cfg.ClientID, "client_name": "bkn-safe SSO exchange",
		"client_secret":  cfg.ClientSecret,
		"grant_types":    []string{"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"response_types": []string{}, "token_endpoint_auth_method": "client_secret_basic",
		"scope": strings.Join(cfg.Scopes, " "), "audience": []string{"bkn-safe"},
	}
	method, path := http.MethodPut, clientPath
	if status == http.StatusNotFound {
		method, path = http.MethodPost, "/admin/clients"
	}
	if status != http.StatusOK && status != http.StatusNotFound {
		return fmt.Errorf("Hydra SSO client lookup status %d", status)
	}
	if status, err = h.ssoAdmin(ctx, method, path, payload, nil); err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("Hydra SSO client registration status %d", status)
	}

	const trustsPath = "/admin/trust/grants/jwt-bearer/issuers"
	var trusts []struct {
		ID        string `json:"id"`
		Issuer    string `json:"issuer"`
		PublicKey struct {
			Kid string `json:"kid"`
		} `json:"public_key"`
		Scope           []string  `json:"scope"`
		AllowAnySubject bool      `json:"allow_any_subject"`
		ExpiresAt       time.Time `json:"expires_at"`
	}
	// Hydra's issuer filter limits this list to our private issuer namespace.
	status, err = h.ssoAdmin(ctx, http.MethodGet, trustsPath+"?issuer="+url.QueryEscape(cfg.Issuer), nil, &trusts)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("Hydra SSO trust lookup status %d", status)
	}
	for _, trust := range trusts {
		if trust.Issuer != cfg.Issuer || trust.PublicKey.Kid != cfg.KeyID {
			continue
		}
		actual, expected := slices.Clone(trust.Scope), slices.Clone(cfg.Scopes)
		slices.Sort(actual)
		slices.Sort(expected)
		if trust.AllowAnySubject && trust.ExpiresAt.After(time.Now().Add(30*24*time.Hour)) && slices.Equal(actual, expected) {
			return nil
		}
		// Hydra has no trust-update API. Replace only this dedicated trust. On a
		// failed POST, the next exchange retries registration; no success is cached.
		status, err = h.ssoAdmin(ctx, http.MethodDelete, trustsPath+"/"+url.PathEscape(trust.ID), nil, nil)
		if err != nil {
			return err
		}
		if status != http.StatusNotFound && (status < 200 || status >= 300) {
			return fmt.Errorf("Hydra SSO trust renewal status %d", status)
		}
	}
	status, err = h.ssoAdmin(ctx, http.MethodPost, trustsPath, map[string]any{
		"issuer": cfg.Issuer, "allow_any_subject": true, "scope": cfg.Scopes,
		"expires_at": time.Now().UTC().Add(365 * 24 * time.Hour), "jwk": cfg.PublicJWK,
	}, nil)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("Hydra SSO trust registration status %d", status)
	}
	return nil
}

// Never include Hydra response bodies in errors: they can contain credentials.
func (h *HydraAdmin) ssoAdmin(ctx context.Context, method, path string, input, output any) (int, error) {
	var body io.Reader
	if input != nil {
		raw, err := json.Marshal(input)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, h.adminURL+path, body)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("Hydra SSO setup request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && output != nil {
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(output); err != nil {
			return resp.StatusCode, fmt.Errorf("decode Hydra SSO setup response: %w", err)
		}
	}
	return resp.StatusCode, nil
}
