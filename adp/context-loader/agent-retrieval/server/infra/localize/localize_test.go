// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package localize

import (
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
