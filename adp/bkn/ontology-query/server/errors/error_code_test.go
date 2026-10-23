// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package errors_test

import (
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

	oerrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/errors"
)

func TestImportInitializesErrorCodes(t *testing.T) {

	code := oerrors.OntologyQuery_InvalidParameter_Condition
	for _, language := range []i18n.Language{i18n.SimplifiedChinese, i18n.AmericanEnglish} {
		httpErr := httperrors.NewHTTPError(i18n.WithLanguage(t.Context(), language), 400, code)
		if err := httpErr.Render(); err != nil || httpErr.ErrorCode != code || httpErr.Description == "" {
			t.Fatalf("initialized error = %#v, %v", httpErr, err)
		}
	}
}
