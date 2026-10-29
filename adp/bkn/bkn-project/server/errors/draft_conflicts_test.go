// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package errors_test

import (
	"slices"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
)

// Confirming a draft while requirement conflicts are unresolved needs the owner to accept them.
func TestConflictsNotAcceptedIsRegisteredAndWorded(t *testing.T) {
	code := perrors.BknProject_Draft_ConflictsNotAccepted
	if !slices.Contains(perrors.DraftErrCodeList, code) {
		t.Fatalf("%s is not in DraftErrCodeList", code)
	}
	words := map[i18n.Language]string{}
	for _, language := range []i18n.Language{i18n.SimplifiedChinese, i18n.AmericanEnglish} {
		httpErr := httperrors.NewHTTPError(i18n.WithLanguage(t.Context(), language), 409, code)
		if err := httpErr.Render(); err != nil || httpErr.Description == "" {
			t.Fatalf("%s in %s = %#v, %v", code, language, httpErr, err)
		}
		words[language] = httpErr.Description
	}
	if words[i18n.SimplifiedChinese] == words[i18n.AmericanEnglish] {
		t.Fatalf("%s has one wording for both languages", code)
	}
}
