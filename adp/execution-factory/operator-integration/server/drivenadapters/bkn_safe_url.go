// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package drivenadapters

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

func mustBknSafeURL() string {
	baseURL, err := normalizeBknSafeURL(os.Getenv("BKN_SAFE_URL"))
	if err != nil {
		panic(err)
	}
	return baseURL
}

func normalizeBknSafeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("BKN_SAFE_URL is required when AUTH_ENABLED=true")
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid BKN_SAFE_URL: %w", err)
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", fmt.Errorf("BKN_SAFE_URL must be an absolute HTTP(S) URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("BKN_SAFE_URL must not contain user info, query, or fragment")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", fmt.Errorf("BKN_SAFE_URL must not contain a path")
	}

	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed.String(), nil
}
