// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package extensions validates entity-level and property-level extensions.
package extensions

import (
	"context"
	"net/http"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	verrors "vega-backend/errors"
	"vega-backend/interfaces"
	"vega-backend/locale"
)

const (
	MaxEntityExtensionPairs   = 64
	MaxPropertyExtensionPairs = 32
	MaxExtensionKeyLen        = 128
	MaxExtensionValueLen      = 512
	MaxExtensionFilterPairs   = 5
	ReservedKeyPrefix         = "vega_"
)

// ValidateEntityExtensionsMap validates root extensions, including quotas, lengths, and reserved keys.
func ValidateEntityExtensionsMap(ctx context.Context, m map[string]string) error {
	if len(m) > MaxEntityExtensionPairs {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Extensions_QuotaExceeded).
			WithErrorDetails(locale.ValidationDetail(ctx, "EntityExtensionsQuota", map[string]interface{}{"Limit": MaxEntityExtensionPairs}))
	}
	for k, v := range m {
		if err := validateOnePair(ctx, k, v); err != nil {
			return err
		}
	}
	return nil
}

// ValidatePropertyExtensionsMap validates property extensions with the property quota.
func ValidatePropertyExtensionsMap(ctx context.Context, m map[string]string) error {
	if len(m) > MaxPropertyExtensionPairs {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Extensions_PropertyQuotaExceeded).
			WithErrorDetails(locale.ValidationDetail(ctx, "PropertyExtensionsQuota", map[string]interface{}{"Limit": MaxPropertyExtensionPairs}))
	}
	for k, v := range m {
		if err := validateOnePair(ctx, k, v); err != nil {
			return err
		}
	}
	return nil
}

// ValidateSchemaPropertiesExtensions validates extensions on schema properties.
func ValidateSchemaPropertiesExtensions(ctx context.Context, props []*interfaces.Property) error {
	for _, p := range props {
		if p == nil || len(p.Extensions) == 0 {
			continue
		}
		if err := ValidatePropertyExtensionsMap(ctx, p.Extensions); err != nil {
			return err
		}
	}
	return nil
}

func validateOnePair(ctx context.Context, k, v string) error {
	if k == "" {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Extensions_InvalidFormat).
			WithErrorDetails(locale.ValidationDetail(ctx, "ExtensionKeyRequired", nil))
	}
	if len(k) > MaxExtensionKeyLen {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Extensions_InvalidFormat).
			WithErrorDetails(locale.ValidationDetail(ctx, "ExtensionKeyLength", map[string]interface{}{"Limit": MaxExtensionKeyLen}))
	}
	if strings.HasPrefix(strings.ToLower(k), ReservedKeyPrefix) {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Extensions_ReservedKey).
			WithErrorDetails(locale.ValidationDetail(ctx, "ExtensionReservedKey", nil))
	}
	if len(v) > MaxExtensionValueLen {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Extensions_InvalidFormat).
			WithErrorDetails(locale.ValidationDetail(ctx, "ExtensionValueLength", map[string]interface{}{"Limit": MaxExtensionValueLen}))
	}
	return nil
}

// ValidateExtensionQueryPairs validates paired extension filters and their quota.
func ValidateExtensionQueryPairs(ctx context.Context, keys, values []string) error {
	if len(keys) == 0 && len(values) == 0 {
		return nil
	}
	if len(keys) != len(values) {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Extensions_MismatchedQueryPairs).
			WithErrorDetails(locale.ValidationDetail(ctx, "ExtensionQueryPairs", nil))
	}
	if len(keys) > MaxExtensionFilterPairs {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Extensions_TooManyFilterPairs).
			WithErrorDetails(locale.ValidationDetail(ctx, "ExtensionFilterPairs", map[string]interface{}{"Limit": MaxExtensionFilterPairs}))
	}
	for i := range keys {
		if keys[i] == "" || values[i] == "" {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Extensions_MismatchedQueryPairs).
				WithErrorDetails(locale.ValidationDetail(ctx, "ExtensionQueryValuesRequired", nil))
		}
	}
	return nil
}
