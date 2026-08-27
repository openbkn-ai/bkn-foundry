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

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	verrors "vega-backend/errors"
	"vega-backend/interfaces"
)

// EnsureResourceQueryable validates that a resource is in a queryable state and has usable metadata.
//
//	enabled=false        → return 409 HTTPError (VegaBackend.Resource.NotQueryable)
//	stale                → return 409 HTTPError (VegaBackend.Resource.NotQueryable)
//	missing              → return 409 HTTPError (VegaBackend.Resource.MetadataUnavailable)
//	empty schema         → return 409 HTTPError (VegaBackend.Resource.MetadataUnavailable)
//	deprecated           → pass, return non-empty warning string
//	active / other state → pass, no warning
//
// Unknown statuses are treated as queryable to avoid blocking legitimate
// traffic when new statuses are introduced.
func EnsureResourceQueryable(ctx context.Context, r *interfaces.Resource) (string, error) {
	if r == nil {
		return "", nil
	}
	if !r.Enabled {
		return "", rest.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_Resource_NotQueryable).
			WithErrorDetails(fmt.Sprintf("resource %s is disabled and cannot be queried", r.ID))
	}
	if r.Status == interfaces.ResourceStatusStale {
		return "", rest.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_Resource_NotQueryable).
			WithErrorDetails(fmt.Sprintf("resource %s is %s and cannot be queried", r.ID, r.Status))
	}
	if r.LastDiscoverStatus == interfaces.DiscoverStatusMissing {
		return "", rest.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_Resource_MetadataUnavailable).
			WithErrorDetails("resource is missing from its source; run discovery and restore the source resource before querying")
	}
	if len(r.SchemaDefinition) == 0 {
		details := "resource schema definition is empty; refresh the resource schema before querying"
		if r.LastDiscoverStatus == interfaces.DiscoverStatusError {
			details = "resource metadata discovery failed; refresh the resource schema before querying"
		}
		return "", rest.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_Resource_MetadataUnavailable).
			WithErrorDetails(details)
	}
	if r.Status == interfaces.ResourceStatusDeprecated {
		return fmt.Sprintf("resource %s (%s) is deprecated", r.ID, r.Name), nil
	}
	return "", nil
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
