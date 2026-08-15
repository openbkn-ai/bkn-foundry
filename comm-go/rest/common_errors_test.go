// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package rest

import "testing"

func TestEmbeddedPublicErrorsContainBothLocales(t *testing.T) {
	for _, code := range publicErrorCodes {
		messages, ok := PublicErrorI18n[code]
		if !ok {
			t.Fatalf("missing public error code %s", code)
		}
		for _, lang := range []Language{SimplifiedChinese, AmericanEnglish} {
			message, ok := messages[lang]
			if !ok {
				t.Fatalf("missing locale %s for %s", lang, code)
			}
			if message.Description == "" || message.Solution == "" {
				t.Fatalf("incomplete message for %s in %s: %#v", code, lang, message)
			}
			if message.ErrorLink != "" {
				t.Fatalf("ErrorLink for %s in %s = %q, want empty", code, lang, message.ErrorLink)
			}
		}
	}
}

func TestPublicHTTPErrorUsesEmbeddedLocaleMessage(t *testing.T) {
	for _, lang := range []Language{SimplifiedChinese, AmericanEnglish} {
		err := NewHTTPError(WithLanguage(t.Context(), lang), 400, PublicError_BadRequest)
		if err == nil || err.BaseError.ErrorLink != "" {
			t.Fatalf("unexpected public error for %s: %#v", lang, err)
		}
		if err.BaseError.Description != PublicErrorI18n[PublicError_BadRequest][lang].Description {
			t.Fatalf("description for %s = %q", lang, err.BaseError.Description)
		}
	}
}
