// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knversion

import (
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
)

func TestRefusalsRenderInBothLocales(t *testing.T) {
	if len(RefusalCodes()) != 7 {
		t.Fatalf("RefusalCodes() = %v, want the 7 version refusals", RefusalCodes())
	}
	for _, code := range RefusalCodes() {
		if !IsRefusal(code) {
			t.Errorf("IsRefusal(%s) = false", code)
		}
		for _, lang := range []i18n.Language{i18n.SimplifiedChinese, i18n.AmericanEnglish} {
			e := httperrors.NewHTTPError(i18n.WithLanguage(t.Context(), lang), http.StatusConflict, code)
			if err := e.Render(); err != nil || e.Description == "" {
				t.Errorf("render %s/%s: %q, %v", code, lang, e.Description, err)
			}
		}
	}
	if IsRefusal("BknBackend.KNVersion.DraftHasChanges") || IsRefusal("") {
		t.Error("IsRefusal accepts a code that is not a version refusal")
	}
}

// A service that also declared one of these codes would stop every program
// that links it with another such service from starting.
func TestRefusalCodesCannotBeRegisteredAgain(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("registering a version refusal code a second time did not panic")
		}
	}()
	httperrors.Register([]string{VersionPurged})
}

func TestRefusalCodesIsACopy(t *testing.T) {
	codes := RefusalCodes()
	codes[0] = "changed"
	if RefusalCodes()[0] != NotVersioned {
		t.Fatal("RefusalCodes() exposes the package's own slice")
	}
}
