// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package locale

import (
	"context"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocaleCatalogs(t *testing.T) {
	require.NoError(t, i18n.ValidateLocaleDir(".", rest.SimplifiedChinese, rest.SimplifiedChinese, rest.AmericanEnglish))
}

func TestValidationDetailUsesRequestLanguage(t *testing.T) {
	Register()

	data := map[string]interface{}{"Limit": 5}
	english := rest.WithLanguage(context.Background(), rest.AmericanEnglish)
	chinese := rest.WithLanguage(context.Background(), rest.SimplifiedChinese)

	assert.Equal(t, "At most 5 extension filter pairs are supported", ValidationDetail(english, "ExtensionFilterPairs", data))
	assert.Equal(t, "扩展筛选条件最多 5 组", ValidationDetail(chinese, "ExtensionFilterPairs", data))
}
