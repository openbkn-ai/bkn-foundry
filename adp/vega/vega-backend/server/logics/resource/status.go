// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package resource

import (
	"context"
	"fmt"
	"net/http"

	"github.com/kweaver-ai/kweaver-go-lib/rest"

	verrors "vega-backend/errors"
	"vega-backend/interfaces"
)

// EnsureResourceQueryable validates that a resource is in a queryable status.
//
//	active     → pass, no warning
//	deprecated → pass, return non-empty warning string
//	disabled / stale → return 409 HTTPError (VegaBackend.Resource.NotQueryable)
//
// Unknown statuses are treated as queryable to avoid blocking legitimate
// traffic when new statuses are introduced.
func EnsureResourceQueryable(ctx context.Context, r *interfaces.Resource) (string, error) {
	if r == nil {
		return "", nil
	}
	switch r.Status {
	case interfaces.ResourceStatusDisabled, interfaces.ResourceStatusStale:
		return "", rest.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_Resource_NotQueryable).
			WithErrorDetails(fmt.Sprintf("resource %s is %s and cannot be queried", r.ID, r.Status))
	case interfaces.ResourceStatusDeprecated:
		return fmt.Sprintf("resource %s (%s) is deprecated", r.ID, r.Name), nil
	default:
		return "", nil
	}
}

// EnsureResourcesQueryable applies EnsureResourceQueryable across a slice and
// aggregates warnings. Returns the first error encountered.
func EnsureResourcesQueryable(ctx context.Context, resources []*interfaces.Resource) ([]string, error) {
	var warnings []string
	for _, r := range resources {
		w, err := EnsureResourceQueryable(ctx, r)
		if err != nil {
			return nil, err
		}
		if w != "" {
			warnings = append(warnings, w)
		}
	}
	return warnings, nil
}
