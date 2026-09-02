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
)

// NormalizeBknSafeURL validates and normalizes the bkn-safe service base URL.
// Error messages deliberately omit the configured value because URLs may carry
// sensitive data in misconfigured environments.
func NormalizeBknSafeURL(rawURL string) (string, error) {
	normalized := strings.TrimRight(strings.TrimSpace(rawURL), "/")
	if normalized == "" {
		return "", errBknSafeURLRequired
	}

	parsed, err := url.Parse(normalized)
	if err != nil || !parsed.IsAbs() || parsed.Hostname() == "" {
		return "", errBknSafeURLInvalid
	}

	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", errBknSafeURLInvalid
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return "", errBknSafeURLInvalid
	}

	parsed.Scheme = scheme
	return strings.TrimRight(parsed.String(), "/"), nil
}
