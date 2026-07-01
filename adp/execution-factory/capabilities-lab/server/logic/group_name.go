// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package logic

import (
	"net/url"
	"regexp"
	"strings"
)

var nonToolboxName = regexp.MustCompile(`[^a-zA-Z0-9_\p{Han}]+`)

// DeriveAutoGroupName builds a stable toolbox name from a service base URL.
// Toolbox names only allow Chinese, letters, digits, and underscores.
func DeriveAutoGroupName(serviceURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(serviceURL))
	if err != nil || parsed.Hostname() == "" {
		return "default_http_group"
	}

	host := strings.ToLower(parsed.Hostname())
	host = strings.ReplaceAll(host, ".", "_")
	host = strings.ReplaceAll(host, "-", "_")
	host = nonToolboxName.ReplaceAllString(host, "_")
	host = strings.Trim(host, "_")
	if host == "" {
		return "default_http_group"
	}

	return host + "_group"
}
