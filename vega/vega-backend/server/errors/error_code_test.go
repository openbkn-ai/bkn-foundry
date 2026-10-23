// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package errors

import (
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

func TestImportInitializesErrorCodes(t *testing.T) {

	code := VegaBackend_InvalidParameter_Name
	for _, language := range []i18n.Language{i18n.SimplifiedChinese, i18n.AmericanEnglish} {
		httpErr := rest.NewHTTPError(i18n.WithLanguage(t.Context(), language), 400, code)
		if err := httpErr.Render(); err != nil || httpErr.BaseError.ErrorCode != code || httpErr.BaseError.Description == "" {
			t.Fatalf("initialized error = %#v, %v", httpErr, err)
		}
	}
}
