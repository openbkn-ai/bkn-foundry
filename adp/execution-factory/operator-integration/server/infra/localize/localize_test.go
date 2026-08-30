// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package localize

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestNewI18nTranslatorFallsBackWhenLocaleResourceIsUnavailable(t *testing.T) {
	resources := fstest.MapFS{
		"locales/zh-Hans.json": &fstest.MapFile{Data: []byte(`{"desc":{"BadRequest":"参数错误"}}`)},
	}

	translator := newI18nTranslator("en_US", resources)

	if got := translator.Trans("desc.BadRequest"); got != "参数错误" {
		t.Fatalf("fallback translation = %q, want %q", got, "参数错误")
	}
}

func TestNewI18nTranslatorFallsBackWhenLocaleResourceIsMalformed(t *testing.T) {
	resources := fstest.MapFS{
		"locales/en-US.json":   &fstest.MapFile{Data: []byte(`{`)},
		"locales/zh-Hans.json": &fstest.MapFile{Data: []byte(`{"desc":{"BadRequest":"参数错误"}}`)},
	}

	translator := newI18nTranslator("en_US", resources)

	if got := translator.Trans("desc.BadRequest"); got != "参数错误" {
		t.Fatalf("fallback translation = %q, want %q", got, "参数错误")
	}
}

func TestNewI18nTranslatorKeepsMessageIDWhenBaselineIsUnavailable(t *testing.T) {
	translator := newI18nTranslator("en_US", fstest.MapFS{})

	if got := translator.Trans("desc.BadRequest"); got != "desc.BadRequest" {
		t.Fatalf("translation = %q, want message ID", got)
	}
}

// The python scaffold is served through go-i18n's Trans, which runs the message
// as a text/template. Any literal `{{` in the scaffold makes Localize fail and
// the endpoint returns the raw key "template.python" instead of the code. Both
// locales must resolve to real @tool code.
func TestPythonScaffoldResolvesInBothLocales(t *testing.T) {
	for _, lang := range []string{"zh-Hans", "en-US"} {
		got := NewI18nTranslator(lang).Trans("template.python")
		if got == "template.python" {
			t.Fatalf("%s: scaffold did not resolve — a literal {{ likely broke the go-i18n template", lang)
		}
		if !strings.Contains(got, "@tool") {
			t.Fatalf("%s: scaffold resolved but is not the @tool form: %.60q", lang, got)
		}
	}
}
