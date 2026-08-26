// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package locale

import (
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"github.com/stretchr/testify/require"
)

func TestLocaleCatalogs(t *testing.T) {
	require.NoError(t, i18n.ValidateLocaleDir(".", rest.SimplifiedChinese, rest.SimplifiedChinese, rest.AmericanEnglish))
}
