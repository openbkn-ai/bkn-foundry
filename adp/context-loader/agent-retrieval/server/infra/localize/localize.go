// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package localize language resources.
package localize

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"strings"

	"github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
)

var (
	// trMap   = map[language.Tag]*I18nTranslator{}
	langMap = map[string]language.Tag{
		"zh_CN": language.SimplifiedChinese,
		"zh_TW": language.TraditionalChinese,
		"en_US": language.AmericanEnglish,
	}
	matcher = language.NewMatcher([]language.Tag{
		language.SimplifiedChinese,
		language.TraditionalChinese,
		language.AmericanEnglish,
	})

	defaultLang = "zh_CN"
	//go:embed locales/*.json
	locales         embed.FS
	loadMessageFile = func(bundle *i18n.Bundle, resources fs.FS, path string) error {
		_, err := bundle.LoadMessageFileFS(resources, path)
		return err
	}
)

// I18nTranslator translator.
type I18nTranslator struct {
	current language.Tag
	loc     *i18n.Localizer
}

// NewI18nTranslator New translator.
func NewI18nTranslator(lang string) *I18nTranslator {
	return newI18nTranslator(lang, locales)
}

func newI18nTranslator(lang string, resources fs.FS) *I18nTranslator {
	lang = normalizeLanguageKey(lang)
	lt, ok := langMap[lang]
	if !ok {
		lt = langMap[defaultLang]
	}
	return newTranslator(lt, resources)
}

func newTranslator(requested language.Tag, resources fs.FS) *I18nTranslator {
	active := requested
	bundle := newBundle(active)
	if err := loadLocale(bundle, resources, active); err != nil {
		log.Printf("WARN: cannot load i18n resource for %s: %v; falling back to %s", active, err, langMap[defaultLang])
		active = langMap[defaultLang]
		bundle = newBundle(active)
		if err := loadLocale(bundle, resources, active); err != nil {
			log.Printf("WARN: cannot load fallback i18n resource for %s: %v; using message IDs", active, err)
		}
	}
	tr := &I18nTranslator{
		current: active,
	}
	tr.loc = i18n.NewLocalizer(bundle, tr.current.String())
	return tr
}

func newBundle(lang language.Tag) *i18n.Bundle {
	bundle := i18n.NewBundle(lang)
	bundle.RegisterUnmarshalFunc("json", json.Unmarshal)
	return bundle
}

func loadLocale(bundle *i18n.Bundle, resources fs.FS, lang language.Tag) error {
	return loadMessageFile(bundle, resources, fmt.Sprintf("locales/%s.json", lang.String()))
}

func normalizeLanguageKey(lang string) string {
	lang = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(lang), "-", "_"))
	if lang == "" {
		return defaultLang
	}

	switch lang {
	case "zh_cn":
		return "zh_CN"
	case "zh_tw":
		return "zh_TW"
	case "en_us":
		return "en_US"
	default:
		return lang
	}
}

// Trans translation.
func (tr *I18nTranslator) Trans(msg string, params ...interface{}) string {
	l := len(params)
	localizeConf := &i18n.LocalizeConfig{
		MessageID: msg,
	}
	if l > 0 {
		localizeConf.TemplateData = params[0]
	}
	if l > 1 {
		localizeConf.PluralCount = params[1]
	}
	str, err := tr.loc.Localize(localizeConf)
	if err != nil {
		str = msg
		if l > 0 {
			str = strings.TrimRight(fmt.Sprintln(msg, params[0]), "\n")
		}
	}
	return str
}

func getLang(lang string) (lt language.Tag, l string, err error) {
	tag, _ := language.MatchStrings(matcher, lang)
	b, _ := tag.Base()
	r, _ := tag.Region()
	l = fmt.Sprintf("%s_%s", b, r)
	lt, ok := langMap[l]
	if !ok {
		err = fmt.Errorf("not support lang %s", lang)
	}
	return
}

func SetDefaultLang(lang string) (err error) {
	_, l, err := getLang(lang)
	if err != nil {
		return
	}
	defaultLang = l
	return
}

// GetI18nTranslator Get the translator.
// func GetI18nTranslator(lang string) *I18nTranslator {
// 	lt, l, err := getLang(lang)
// 	if err != nil {
// 		lt = langMap[defaultLang]
// 	}
// 	tr, ok := trMap[lt]
// 	if !ok {
// 		tr = NewI18nTranslator(l)
// 		trMap[lt] = tr
// 	}
// 	return tr
// }
