// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httperrors

import (
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
)

func TestEmbeddedPublicErrorsContainBothLocales(t *testing.T) {
	for _, code := range publicErrorCodes {
		for _, lang := range []i18n.Language{i18n.SimplifiedChinese, i18n.AmericanEnglish} {
			e := NewHTTPError(i18n.WithLanguage(t.Context(), lang), 400, code)
			err := e.Render()
			expected, lookupErr := i18n.Render(lang, code, nil)
			if err != nil || lookupErr != nil || e.Description == "" || e.Description != expected {
				t.Fatalf("render %s/%s: %#v, %v", code, lang, e.BaseError, err)
			}
		}
	}
}
