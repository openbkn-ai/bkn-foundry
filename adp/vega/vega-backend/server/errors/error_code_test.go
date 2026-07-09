// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package errors

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestErrCodeLists(t *testing.T) {
	lists := map[string][]string{
		"common":            commonErrCodeList,
		"catalog":           CatalogErrCodeList,
		"resource":          ResourceErrCodeList,
		"connector_type":    ConnectorTypeErrCodeList,
		"build_task":        BuildTaskErrCodeList,
		"discover_task":     DiscoverTaskErrCodeList,
		"query":             QueryErrCodeList,
		"logic_view":        LogicViewErrCodeList,
		"dataset":           DatasetErrCodeList,
		"discover_schedule": DiscoverScheduleErrCodeList,
		"extensions":        ExtensionsErrCodeList,
	}

	allCodes := make(map[string]string)
	for name, codes := range lists {
		t.Run(name+" list is registered without duplicate codes", func(t *testing.T) {
			require.NotEmpty(t, codes)

			seen := make(map[string]struct{})
			for _, code := range codes {
				require.NotEmpty(t, code)
				assert.NotContains(t, seen, code)
				seen[code] = struct{}{}

				if previousList, ok := allCodes[code]; ok {
					t.Fatalf("error code %q appears in both %s and %s", code, previousList, name)
				}
				allCodes[code] = name
			}
		})
	}
}

func TestExtensionsErrCodeList(t *testing.T) {
	assert.ElementsMatch(t, []string{
		VegaBackend_Extensions_InvalidFormat,
		VegaBackend_Extensions_QuotaExceeded,
		VegaBackend_Extensions_PropertyQuotaExceeded,
		VegaBackend_Extensions_ReservedKey,
		VegaBackend_Extensions_MismatchedQueryPairs,
		VegaBackend_Extensions_TooManyFilterPairs,
	}, ExtensionsErrCodeList)
}
