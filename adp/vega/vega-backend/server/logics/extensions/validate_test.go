// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package extensions

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-comm-go/rest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	verrors "vega-backend/errors"
	"vega-backend/interfaces"
)

func TestValidateEntityExtensionsMap(t *testing.T) {
	ctx := context.Background()

	require.NoError(t, ValidateEntityExtensionsMap(ctx, map[string]string{
		"owner": "team-a",
		"env":   "prod",
	}))

	cases := []struct {
		name string
		in   map[string]string
		code string
	}{
		{
			name: "too many pairs",
			in:   repeatExtensionPairs(MaxEntityExtensionPairs + 1),
			code: verrors.VegaBackend_Extensions_QuotaExceeded,
		},
		{
			name: "empty key",
			in:   map[string]string{"": "value"},
			code: verrors.VegaBackend_Extensions_InvalidFormat,
		},
		{
			name: "key too long",
			in:   map[string]string{strings.Repeat("k", MaxExtensionKeyLen+1): "value"},
			code: verrors.VegaBackend_Extensions_InvalidFormat,
		},
		{
			name: "reserved key prefix is case insensitive",
			in:   map[string]string{"VeGa_owner": "value"},
			code: verrors.VegaBackend_Extensions_ReservedKey,
		},
		{
			name: "value too long",
			in:   map[string]string{"owner": strings.Repeat("v", MaxExtensionValueLen+1)},
			code: verrors.VegaBackend_Extensions_InvalidFormat,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateEntityExtensionsMap(ctx, tc.in)

			assertHTTPError(t, err, http.StatusBadRequest, tc.code)
		})
	}
}

func TestValidatePropertyAndSchemaExtensions(t *testing.T) {
	t.Run("validate property and schema extensions", func(t *testing.T) {
		ctx := context.Background()

		require.NoError(t, ValidatePropertyExtensionsMap(ctx, map[string]string{"feature": "keyword"}))
		assertHTTPError(t,
			ValidatePropertyExtensionsMap(ctx, repeatExtensionPairs(MaxPropertyExtensionPairs+1)),
			http.StatusBadRequest,
			verrors.VegaBackend_Extensions_PropertyQuotaExceeded,
		)

		require.NoError(t, ValidateSchemaPropertiesExtensions(ctx, []*interfaces.Property{
			nil,
			{Name: "id"},
			{Name: "name", Extensions: map[string]string{"owner": "team-a"}},
		}))
		assertHTTPError(t,
			ValidateSchemaPropertiesExtensions(ctx, []*interfaces.Property{{Name: "name", Extensions: map[string]string{"vega_bad": "x"}}}),
			http.StatusBadRequest,
			verrors.VegaBackend_Extensions_ReservedKey,
		)
	})
}

func TestValidateExtensionQueryPairs(t *testing.T) {
	ctx := context.Background()

	require.NoError(t, ValidateExtensionQueryPairs(ctx, nil, nil))
	require.NoError(t, ValidateExtensionQueryPairs(ctx, []string{"env"}, []string{"prod"}))

	cases := []struct {
		name   string
		keys   []string
		values []string
		code   string
	}{
		{
			name:   "mismatched lengths",
			keys:   []string{"env"},
			values: nil,
			code:   verrors.VegaBackend_Extensions_MismatchedQueryPairs,
		},
		{
			name:   "too many pairs",
			keys:   []string{"a", "b", "c", "d", "e", "f"},
			values: []string{"1", "2", "3", "4", "5", "6"},
			code:   verrors.VegaBackend_Extensions_TooManyFilterPairs,
		},
		{
			name:   "empty key",
			keys:   []string{""},
			values: []string{"prod"},
			code:   verrors.VegaBackend_Extensions_MismatchedQueryPairs,
		},
		{
			name:   "empty value",
			keys:   []string{"env"},
			values: []string{""},
			code:   verrors.VegaBackend_Extensions_MismatchedQueryPairs,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertHTTPError(t, ValidateExtensionQueryPairs(ctx, tc.keys, tc.values), http.StatusBadRequest, tc.code)
		})
	}
}

func repeatExtensionPairs(n int) map[string]string {
	out := make(map[string]string, n)
	for i := 0; i < n; i++ {
		out[fmt.Sprintf("key_%d", i)] = "value"
	}
	return out
}

func assertHTTPError(t *testing.T, err error, httpCode int, code string) {
	t.Helper()

	require.Error(t, err)
	var httpErr *rest.HTTPError
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, httpCode, httpErr.HTTPCode)
	assert.Equal(t, code, httpErr.BaseError.ErrorCode)
}
