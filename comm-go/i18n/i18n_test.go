// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package i18n

import (
	"errors"
	"fmt"
	"maps"
	"reflect"
	"strings"
	"testing"
	"text/template"
)

func testMessages(zh, en map[string]string) []I18nMessages {
	return []I18nMessages{{Locale: "zh-CN", Messages: zh}, {Locale: "en-US", Messages: en}}
}

func installTestMessages(t *testing.T, definitions []I18nMessages) {
	t.Helper()
	previous := maps.Clone(compiledI18nMessagesBundle)
	previousMessages := make(map[string]map[string]*template.Template)
	for locale, bundle := range previous {
		previousMessages[locale] = maps.Clone(bundle.Messages)
	}
	previousLocale := DefaultLanguage
	t.Cleanup(func() {
		clear(compiledI18nMessagesBundle)
		for locale, bundle := range previous {
			clear(bundle.Messages)
			maps.Copy(bundle.Messages, previousMessages[locale])
			compiledI18nMessagesBundle[locale] = bundle
		}
		DefaultLanguage = previousLocale
	})
	for _, definition := range definitions {
		RegisterI18n(&definition)
	}
}

func TestRegisterPublicMessagesUsesTheSharedPipeline(t *testing.T) {
	installTestMessages(t, nil)
	if err := ValidateMessages("Public.BadRequest"); err != nil {
		t.Fatal(err)
	}
	for locale, messages := range compiledI18nMessagesBundle {
		if messages.Locale != locale {
			t.Fatalf("bundle %q has locale %q", locale, messages.Locale)
		}
		if text, err := Render(locale, "Public.BadRequest", nil); err != nil || text == "" {
			t.Fatalf("public message = %q, %v", text, err)
		}
	}
	before := maps.Clone(compiledI18nMessagesBundle)
	if err := ValidateMessageBundles(); err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(before, compiledI18nMessagesBundle) {
		t.Fatal("public-only validation published state")
	}
}

func TestCompileMessagesRejectsInvalidDefinitions(t *testing.T) {
	cases := []struct {
		name       string
		definition I18nMessages
		want       string
	}{
		{"empty ID", I18nMessages{Locale: "en-US", Messages: map[string]string{"": "text"}}, "empty message ID"},
		{"trimmed key collision", I18nMessages{Locale: "en-US", Messages: map[string]string{" Code ": "first", "Code": "second"}}, "already exists after trimming"},
		{"whitespace ID", I18nMessages{Locale: "en-US", Messages: map[string]string{" \t ": "text"}}, "empty message ID"},
		{"empty text", I18nMessages{Locale: "en-US", Messages: map[string]string{"Code": " "}}, "empty string"},
		{"invalid template", I18nMessages{Locale: "en-US", Messages: map[string]string{"Code": "{{"}}, "invalid template"},
	}
	before := maps.Clone(compiledI18nMessagesBundle)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := compileMessages(&tc.definition); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("compilation = %v; want %s", err, tc.want)
			}
			if !maps.Equal(before, compiledI18nMessagesBundle) {
				t.Fatal("compilation changed the global bundle")
			}
		})
	}
}

func TestValidateMessageBundlesChecksConsistencyAfterRegistration(t *testing.T) {
	cases := []struct {
		name        string
		definitions []I18nMessages
		wantAny     []string
	}{
		{"missing language", []I18nMessages{{Locale: "zh-CN", Messages: map[string]string{"Code": "中文"}}}, []string{"required locale"}},
		{"missing message", testMessages(map[string]string{"Code": "中文"}, map[string]string{}), []string{"missing messageId Code"}},
		{"extra message", testMessages(map[string]string{"Code": "中文"}, map[string]string{"Code": "English", "Extra": "extra"}), []string{"unexpected messageId Extra"}},
		{"same length different keys", testMessages(map[string]string{"OnlyZh": "中文"}, map[string]string{"OnlyEn": "English"}), []string{"missing messageId OnlyZh"}},
		{"longer language has missing and extra keys", testMessages(map[string]string{"Shared": "共享", "OnlyZh": "中文"}, map[string]string{"Shared": "shared", "Extra1": "first", "Extra2": "second"}), []string{"missing messageId OnlyZh"}},
		{"shorter language has extra keys too", testMessages(map[string]string{"Shared": "共享", "OnlyZh1": "第一", "OnlyZh2": "第二"}, map[string]string{"Shared": "shared", "OnlyEn": "English"}), []string{"missing messageId OnlyZh1", "missing messageId OnlyZh2"}},
		{"multiple extra keys report only one", testMessages(map[string]string{"Code": "中文"}, map[string]string{"Code": "English", "Extra1": "first", "Extra2": "second"}), []string{"unexpected messageId Extra1", "unexpected messageId Extra2"}},
		{"different parameters allowed", testMessages(map[string]string{"Code": "{{.name}}"}, map[string]string{"Code": "{{.id}}"}), nil},
		{"consistent", testMessages(map[string]string{"Code": "{{.field}} {{.limit}}"}, map[string]string{"Code": "{{.limit}} {{.field}} {{.field}}"}), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			installTestMessages(t, nil)
			clear(compiledI18nMessagesBundle)
			// Even incomplete or inconsistent languages can be registered independently.
			for _, definition := range tc.definitions {
				RegisterI18n(&definition)
			}
			before := maps.Clone(compiledI18nMessagesBundle)
			err := ValidateMessageBundles()
			if len(tc.wantAny) == 0 {
				if err != nil {
					t.Fatalf("validation = %v; want nil", err)
				}
			} else {
				if err == nil {
					t.Fatal("inconsistent messages passed validation")
				}
				matched := false
				for _, want := range tc.wantAny {
					if strings.Contains(err.Error(), want) {
						matched = true
						break
					}
				}
				if !matched {
					t.Errorf("validation = %v; want one of %q", err, tc.wantAny)
				}
				if strings.Contains(err.Error(), "\n") {
					t.Errorf("validation returned multiple errors: %v", err)
				}
			}
			if !maps.Equal(before, compiledI18nMessagesBundle) {
				t.Fatal("consistency validation changed the bundle")
			}
		})
	}
}

func TestLanguageTemplatesMayUseDifferentParameters(t *testing.T) {
	installTestMessages(t, testMessages(map[string]string{"Different.Params": "名称 {{.name}}"}, map[string]string{"Different.Params": "ID {{.id}}"}))
	if err := ValidateMessageBundles(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		locale string
		data   map[string]any
		want   string
	}{
		{"zh-CN", map[string]any{"name": "测试"}, "名称 测试"},
		{"en-US", map[string]any{"id": "example"}, "ID example"},
	} {
		got, err := Render(tc.locale, "Different.Params", tc.data)
		if err != nil || got != tc.want {
			t.Fatalf("Render(%s) = %q, %v; want %q", tc.locale, got, err, tc.want)
		}
	}
}

func TestRegistrationFailureDoesNotModifyCompiledMessages(t *testing.T) {
	installTestMessages(t, testMessages(map[string]string{"Test.Code": "参数"}, map[string]string{"Test.Code": "Parameter"}))
	before := maps.Clone(compiledI18nMessagesBundle["en-US"].Messages)
	assertPanicContains(t, "invalid template", func() {
		RegisterI18n(&I18nMessages{Locale: "en-US", Messages: map[string]string{"Other": "other", "ZBroken": "{{"}})
	})
	if !maps.Equal(before, compiledI18nMessagesBundle["en-US"].Messages) {
		t.Fatal("compilation failure appended partial messages")
	}
	assertPanicContains(t, "already exists", func() { RegisterI18n(&publicErrorsEnUS) })
	if !maps.Equal(before, compiledI18nMessagesBundle["en-US"].Messages) {
		t.Fatal("rejected overwrite changed registered messages")
	}
}

func TestSetDefaultLocaleValidatesBeforeUpdating(t *testing.T) {
	installTestMessages(t, testMessages(map[string]string{"Test.Code": "中文"}, map[string]string{"Test.Code": "English"}))
	SetDefaultLocale(AmericanEnglish)
	if DefaultLanguage != "en-US" {
		t.Fatalf("default locale = %q", DefaultLanguage)
	}
	before := maps.Clone(compiledI18nMessagesBundle)
	for _, locale := range []string{"", "???", "fr-FR", "EN-us"} {
		t.Run(locale, func(t *testing.T) {
			assertPanicContains(t, "default locale", func() { SetDefaultLocale(locale) })
			if DefaultLanguage != "en-US" || !maps.Equal(before, compiledI18nMessagesBundle) {
				t.Fatal("rejected default changed startup state")
			}
			if text, err := Render("en-US", "Test.Code", nil); err != nil || text != "English" {
				t.Fatalf("registered language after rejected default = %q, %v", text, err)
			}
		})
	}
}

func TestValidateMessageBundlesRejectsMissingDefaultLanguage(t *testing.T) {
	installTestMessages(t, nil)
	before := maps.Clone(compiledI18nMessagesBundle)
	DefaultLanguage = "fr-FR"
	if err := ValidateMessageBundles(); err == nil || !strings.Contains(err.Error(), "default locale fr-FR is not present") {
		t.Fatalf("validation = %v; want missing default locale", err)
	}
	if DefaultLanguage != "fr-FR" || !maps.Equal(before, compiledI18nMessagesBundle) {
		t.Fatal("validation changed startup state")
	}
}

func TestCompiledLanguageBundlesUseLanguageConstants(t *testing.T) {
	definitions := testMessages(map[string]string{"Test.Code": "中文"}, map[string]string{"Test.Code": "English"})
	definitions[0].Locale = SimplifiedChinese
	definitions[1].Locale = AmericanEnglish
	installTestMessages(t, definitions)
	SetDefaultLocale(AmericanEnglish)
	for key, bundle := range compiledI18nMessagesBundle {
		if bundle.Locale != key {
			t.Fatalf("language key %q differs from locale %q", key, bundle.Locale)
		}
	}
	before := maps.Clone(compiledI18nMessagesBundle)
	if err := ValidateMessageBundles(); err != nil {
		t.Fatal(err)
	}
	if DefaultLanguage != "en-US" || !maps.Equal(before, compiledI18nMessagesBundle) {
		t.Fatal("validation changed startup state")
	}
	RegisterI18n(&I18nMessages{Locale: "en-US", Messages: map[string]string{"Extra": "extra"}})
	if err := ValidateMessageBundles(); err == nil || !strings.Contains(err.Error(), "locale zh-CN is missing messageId Extra from en-US") {
		t.Fatalf("validation did not use the en-US default baseline: %v", err)
	}
	if DefaultLanguage != "en-US" {
		t.Fatal("registration changed the default locale")
	}
	SetDefaultLocale("zh-CN")
	if err := ValidateMessageBundles(); err == nil || !strings.Contains(err.Error(), "locale en-US has unexpected messageId Extra") {
		t.Fatalf("validation did not follow the changed default locale: %v", err)
	}
}

func TestCompileMessagesUsesTrimmedKeysAndValues(t *testing.T) {
	installTestMessages(t, nil)
	const rawID = " \t Trim.Code \n"
	const rawText = " \n hello {{.name}} \t "
	definition := &I18nMessages{Locale: "en-US", Messages: map[string]string{rawID: rawText}}
	RegisterI18n(definition)
	compiled := compiledI18nMessagesBundle["en-US"].Messages["Trim.Code"]
	if compiled == nil || compiled.Name() != "Trim.Code" {
		t.Fatal("trimmed key was not used for storage and template name")
	}
	if text, err := Render("en-US", "Trim.Code", map[string]any{"name": "Ada"}); err != nil || text != "hello Ada" {
		t.Fatalf("trimmed message = %q, %v", text, err)
	}
	if len(definition.Messages) != 1 || definition.Messages[rawID] != rawText {
		t.Fatal("compilation changed the caller's message map")
	}
	assertPanicContains(t, "already exists", func() {
		RegisterI18n(&I18nMessages{Locale: "en-US", Messages: map[string]string{" Trim.Code ": "overwrite", "Never.Trim": "must not publish"}})
	})
	if err := ValidateMessages("Never.Trim"); err == nil {
		t.Fatal("normalized key collision appended partial messages")
	}
}

func TestCompileMessagesOnlyConvertsInput(t *testing.T) {
	before := maps.Clone(compiledI18nMessagesBundle)
	compiled, err := compileMessages(&I18nMessages{Locale: "EN-us", Messages: map[string]string{"Compile.Only": "{{.value}}"}})
	if err != nil {
		t.Fatal(err)
	}
	if compiled.Locale != "EN-us" || len(compiled.Messages) != 1 || compiled.Messages["Compile.Only"] == nil {
		t.Fatalf("compiled messages differ from input: %#v", compiled)
	}
	if !maps.Equal(before, compiledI18nMessagesBundle) {
		t.Fatal("compilation changed the global bundle")
	}
	if err := ValidateMessages("Compile.Only"); err == nil {
		t.Fatal("compilation registered the message")
	}
}

func TestRegistrationsAppendToTheSameBundle(t *testing.T) {
	installTestMessages(t, nil)
	bundle := compiledI18nMessagesBundle
	english := bundle["en-US"]
	public := english.Messages["Public.BadRequest"]
	RegisterI18n(&I18nMessages{Locale: "zh-CN", Messages: map[string]string{"First": "第一"}})
	RegisterI18n(&I18nMessages{Locale: "en-US", Messages: map[string]string{"First": "first"}})
	first := english.Messages["First"]
	RegisterI18n(&I18nMessages{Locale: "zh-CN", Messages: map[string]string{"Second": "第二"}})
	RegisterI18n(&I18nMessages{Locale: "en-US", Messages: map[string]string{"Second": "second"}})
	if reflect.ValueOf(bundle).Pointer() != reflect.ValueOf(compiledI18nMessagesBundle).Pointer() || bundle["en-US"] != english || compiledI18nMessagesBundle["en-US"] != english || english.Messages["First"] != first || english.Messages["Public.BadRequest"] != public {
		t.Fatal("registration replaced the global bundle or existing language messages")
	}
	for id, want := range map[string]string{"First": "first", "Second": "second"} {
		if text, err := Render("en-US", id, nil); err != nil || text != want {
			t.Fatalf("Render(%s) = %q, %v", id, text, err)
		}
	}
	before := maps.Clone(english.Messages)
	assertPanicContains(t, "already exists", func() {
		RegisterI18n(&I18nMessages{Locale: "en-US", Messages: map[string]string{"First": "overwrite", "Never": "must not publish"}})
	})
	if !maps.Equal(before, english.Messages) {
		t.Fatal("failed registration changed existing language messages")
	}
	if err := ValidateMessages("Never"); err == nil {
		t.Fatal("failed registration appended partial messages")
	}
}

func TestDefaultMissingKeysAndConditions(t *testing.T) {
	installTestMessages(t, testMessages(
		map[string]string{"Test.Code": "字段 {{.name}}", "Test.Condition": "{{if .name}}{{.name}}{{end}}"},
		map[string]string{"Test.Code": "Field {{.name}}", "Test.Condition": "{{if .name}}{{.name}}{{end}}"},
	))
	for _, tc := range []struct {
		id   string
		data map[string]any
		want string
	}{
		{"Test.Code", nil, "Field <no value>"},
		{"Test.Code", map[string]any{"name": nil}, "Field <no value>"},
		{"Test.Code", map[string]any{"name": "title"}, "Field title"},
		{"Test.Condition", nil, ""},
		{"Test.Condition", map[string]any{"name": "title"}, "title"},
	} {
		got, err := Render("en-US", tc.id, tc.data)
		if err != nil || got != tc.want {
			t.Fatalf("Render = %q, %v; want %q", got, err, tc.want)
		}
	}
}

func TestRenderingFailuresHaveSafeTypesAndNoPartialOutput(t *testing.T) {
	installTestMessages(t, testMessages(
		map[string]string{"Test.Code": "前缀 {{index .items 4}}"},
		map[string]string{"Test.Code": "prefix {{index .items 4}}"},
	))
	for _, tc := range []struct {
		id   string
		kind RenderFailure
	}{{"Missing", MessageMissing}, {"Test.Code", TemplateExecution}} {
		text, err := Render("en-US", tc.id, map[string]any{"items": []string{"private"}})
		var failure *RenderError
		if text != "" || !errors.As(err, &failure) || failure.Kind != tc.kind {
			t.Fatalf("Render = %q, %v", text, err)
		}
		if strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "prefix") {
			t.Fatal("render diagnostic leaked data")
		}
	}
}

func TestCompiledMessagesOwnInput(t *testing.T) {
	definitions := testMessages(map[string]string{"Test.Code": "中文"}, map[string]string{"Test.Code": "English"})
	installTestMessages(t, definitions)
	definitions[1].Messages["Test.Code"] = "modified"
	delete(definitions[0].Messages, "Test.Code")
	for locale, want := range map[string]string{AmericanEnglish: "English", SimplifiedChinese: "中文"} {
		if got, err := Render(locale, "Test.Code", nil); err != nil || got != want {
			t.Fatalf("language bundle %s: got %q, %v; want %q", locale, got, err, want)
		}
	}
	if got, err := Render("en-US", "Public.BadRequest", nil); err != nil || got == "" {
		t.Fatalf("public message unavailable: %q %v", got, err)
	}
}

func TestRenderRejectsMissingLanguagesWithoutDefaultFallback(t *testing.T) {
	installTestMessages(t, testMessages(map[string]string{"Test.Code": "中文"}, map[string]string{"Test.Code": "English"}))
	SetDefaultLocale("en-US")
	for _, locale := range []string{"fr-FR", "en-GB", "", "???", "EN-us"} {
		t.Run(locale, func(t *testing.T) {
			text, err := Render(locale, "Test.Code", nil)
			var failure *RenderError
			if text != "" || !errors.As(err, &failure) || failure.Kind != LocaleMissing {
				t.Fatalf("Render(%q) = %q, %v; want missing locale error", locale, text, err)
			}
		})
	}
	if text, err := Render(AmericanEnglish, "Test.Code", nil); err != nil || text != "English" {
		t.Fatalf("registered language = %q, %v", text, err)
	}
}

func TestValidateMessagesRequiresOwnershipWithoutExecution(t *testing.T) {
	installTestMessages(t, testMessages(map[string]string{"Test.Code": "{{index .items 4}}"}, map[string]string{"Test.Code": "{{index .items 4}}"}))
	if err := ValidateMessages("Test.Code"); err != nil {
		t.Fatalf("validation executed template: %v", err)
	}
	RegisterI18n(&I18nMessages{Locale: "fr-FR", Messages: map[string]string{"Other": "other"}})
	err := ValidateMessages("Test.Code")
	var failure *RenderError
	if !errors.As(err, &failure) || failure.Locale != "fr-FR" || failure.MessageID != "Test.Code" || failure.Kind != MessageMissing {
		t.Fatalf("missing message in an additional registered language: %v", err)
	}
	RegisterI18n(&I18nMessages{Locale: "fr-FR", Messages: map[string]string{"Test.Code": "{{index .items 4}}"}})
	if err := ValidateMessages("Test.Code"); err != nil {
		t.Fatalf("complete registered languages rejected: %v", err)
	}
	err = ValidateMessages("Test.Code", "Missing")
	if !errors.As(err, &failure) || failure.MessageID != "Missing" {
		t.Fatalf("not all message IDs were checked: %v", err)
	}
}

func assertPanicContains(t *testing.T, want string, run func()) {
	t.Helper()
	defer func() {
		recovered := recover()
		if recovered == nil || !strings.Contains(fmt.Sprint(recovered), want) {
			t.Errorf("panic = %v; want %s", recovered, want)
		}
	}()
	run()
}
