// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package utils

import "strings"

// SanitizeThirdPartyHeaders copies business headers while removing internal
// identity, authorization transport, and trace-control headers.
func SanitizeThirdPartyHeaders[T any](headers map[string]T) map[string]T {
	if len(headers) == 0 {
		return headers
	}
	sanitized := make(map[string]T, len(headers))
	for name, value := range headers {
		if isPlatformHeader(name) {
			continue
		}
		sanitized[name] = value
	}
	return sanitized
}

func isPlatformHeader(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	switch name {
	case "x-account-id", "x-account-type", "user_id", "x-authorization", "traceparent", "tracestate":
		return true
	default:
		return strings.HasPrefix(name, "bkn-") || strings.HasPrefix(name, "x-bkn-")
	}
}
