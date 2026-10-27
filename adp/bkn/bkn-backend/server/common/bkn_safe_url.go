// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package common

import (
	"errors"
	"net/url"
	"strings"
)

var (
	errBknSafeURLRequired = errors.New("BKN_SAFE_URL is required when authentication is enabled")
	errBknSafeURLInvalid  = errors.New("BKN_SAFE_URL must be an absolute HTTP or HTTPS URL with a host and without credentials, query, or fragment")
	errKBAPIURLInvalid    = errors.New("KB_API_URL must be an absolute HTTP or HTTPS URL with a host and without credentials, query, or fragment")
)

// NormalizeBknSafeURL validates and normalizes the bkn-safe service base URL.
// Error messages deliberately omit the configured value because URLs may carry
// sensitive data in misconfigured environments.
func NormalizeBknSafeURL(rawURL string) (string, error) {
	if strings.TrimSpace(rawURL) == "" {
		return "", errBknSafeURLRequired
	}
	return normalizeServiceURL(rawURL, errBknSafeURLInvalid)
}

// NormalizeKBAPIURL validates the optional Enterprise kb-api base URL by the same rules as
// BKN_SAFE_URL. Empty is valid: kb-api is not deployed, and knowledge bases cannot be mounted.
// A malformed value fails startup rather than every later call, where it would look like kb-api
// being down.
func NormalizeKBAPIURL(rawURL string) (string, error) {
	if strings.TrimSpace(rawURL) == "" {
		return "", nil
	}
	return normalizeServiceURL(rawURL, errKBAPIURLInvalid)
}

// normalizeServiceURL requires an absolute HTTP(S) URL with a host and no credentials, query
// or fragment, and answers invalid with errInvalid so the value is never echoed.
func normalizeServiceURL(rawURL string, errInvalid error) (string, error) {
	normalized := strings.TrimRight(strings.TrimSpace(rawURL), "/")
	parsed, err := url.Parse(normalized)
	if err != nil || !parsed.IsAbs() || parsed.Hostname() == "" {
		return "", errInvalid
	}

	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", errInvalid
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return "", errInvalid
	}

	parsed.Scheme = scheme
	return strings.TrimRight(parsed.String(), "/"), nil
}
