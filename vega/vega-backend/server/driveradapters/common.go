// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import "strings"

// parseRawIDs normalizes a comma-separated path segment while preserving the
// first occurrence order. All batch endpoints use it before any lookup or
// mutation so whitespace, empty values, and duplicate IDs have one meaning.
func parseRawIDs(rawIDs string) []string {
	parts := strings.Split(rawIDs, ",")
	ids := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		id := strings.TrimSpace(part)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids
}
