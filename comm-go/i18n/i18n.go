// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package i18n

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	gotemplate "text/template"
	"text/template/parse"

	"github.com/BurntSushi/toml"
	"golang.org/x/text/language"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
)

const (
	defaultLocale          = "zh-CN"
	genericChineseMessage  = "请求失败。"
	genericEnglishMessage  = "Request failed."
	genericChineseSolution = "请查看请求详情或联系管理员。"
	genericEnglishSolution = "See the request details or contact an administrator."
)

type Message struct {
	Data     string
	template *gotemplate.Template
}

type catalog struct {
	defaultLocale string
	messages      map[string]map[string]*Message
}

var (
	catalogMu      sync.RWMutex
	activeCatalog  = &catalog{defaultLocale: defaultLocale, messages: map[string]map[string]*Message{}}
	warnedFallback sync.Map
)

// RegisterI18n loads a locale directory. Resource validation failures are logged and
// the valid portion remains available so an invalid translation cannot stop a service.
// The optional default locale keeps the original API compatible with existing callers.
func RegisterI18n(localeDir string, configuredDefaultLocale ...string) {
	locale := defaultLocale
	if len(configuredDefaultLocale) > 0 && configuredDefaultLocale[0] != "" {
		locale = configuredDefaultLocale[0]
	}

	loaded, err := loadCatalog(localeDir, locale)
	if err != nil {
		logger.Warnf("i18n resource validation failed; using runtime fallback: %v", err)
	}

	catalogMu.Lock()
	activeCatalog = loaded
	catalogMu.Unlock()
}

// SetDefaultLocale updates the fallback locale after a service loads its startup
// configuration. It only accepts a locale with a loaded catalog.
func SetDefaultLocale(locale string) {
	normalized := normalizeLocale(locale)
	catalogMu.Lock()
	defer catalogMu.Unlock()
	if len(activeCatalog.messages) == 0 {
		return
	}
	if normalized == "" || activeCatalog.messages[normalized] == nil {
		warnFallback(normalized, "", "default_locale_missing")
		return
	}
	activeCatalog = &catalog{defaultLocale: normalized, messages: activeCatalog.messages}
}

// ValidateLocaleDir performs deterministic, strict catalog validation for CI.
// requiredLocales allows a service to declare the locale set it must ship.
func ValidateLocaleDir(localeDir string, configuredDefaultLocale string, requiredLocales ...string) error {
	loaded, err := loadCatalog(localeDir, configuredDefaultLocale)
	var errs []error
	if err != nil {
		errs = append(errs, err)
	}
	for _, requiredLocale := range requiredLocales {
		normalized := normalizeLocale(requiredLocale)
		if normalized == "" || loaded.messages[normalized] == nil {
			errs = append(errs, fmt.Errorf("required locale %s is not present", requiredLocale))
		}
	}
	return errors.Join(errs...)
}

func loadCatalog(localeDir string, configuredDefaultLocale string) (*catalog, error) {
	locale := normalizeLocale(configuredDefaultLocale)
	if locale == "" {
		locale = defaultLocale
	}
	loaded := &catalog{defaultLocale: locale, messages: make(map[string]map[string]*Message)}

	entries, err := os.ReadDir(localeDir)
	if err != nil {
		return loaded, fmt.Errorf("read locale directory %s: %w", localeDir, err)
	}

	var errs []error
	for _, entry := range entries {
		if entry.IsDir() || strings.HasSuffix(entry.Name(), ".go") {
			continue
		}

		_, lang, ok := parseLocaleFilename(entry.Name())
		if !ok {
			errs = append(errs, fmt.Errorf("locale file %s has invalid filename; expected <module>.<language>.toml", entry.Name()))
			continue
		}
		if loaded.messages[lang] == nil {
			loaded.messages[lang] = make(map[string]*Message)
		}

		filename := filepath.Join(localeDir, entry.Name())
		logger.Infof("load locale file: %s", filename)
		contents, readErr := os.ReadFile(filename)
		if readErr != nil {
			errs = append(errs, fmt.Errorf("read locale file %s: %w", filename, readErr))
			continue
		}

		var raw interface{}
		if unmarshalErr := toml.Unmarshal(contents, &raw); unmarshalErr != nil {
			errs = append(errs, fmt.Errorf("parse locale file %s: %w", filename, unmarshalErr))
			continue
		}
		if flattenErr := flattenMessages(loaded.messages[lang], "", raw); flattenErr != nil {
			errs = append(errs, fmt.Errorf("validate locale file %s: %w", filename, flattenErr))
		}
	}

	if validationErr := validateCatalog(loaded); validationErr != nil {
		errs = append(errs, validationErr)
	}
	return loaded, errors.Join(errs...)
}

func parseLocaleFilename(filename string) (string, string, bool) {
	parts := strings.Split(filename, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] != "toml" {
		return "", "", false
	}
	lang := normalizeLocale(parts[1])
	if lang == "" {
		return "", "", false
	}
	return parts[0], lang, true
}

func normalizeLocale(raw string) string {
	tag, err := language.Parse(raw)
	if err != nil {
		return ""
	}
	return tag.String()
}

func flattenMessages(messages map[string]*Message, messageID string, raw interface{}) error {
	switch value := raw.(type) {
	case string:
		if value == "" {
			return fmt.Errorf("messageId %s is an empty string", messageID)
		}
		if _, ok := messages[messageID]; ok {
			return fmt.Errorf("messageId %s already exists", messageID)
		}

		parsed, err := gotemplate.New(messageID).Option("missingkey=error").Parse(value)
		if err != nil {
			return fmt.Errorf("messageId %s has an invalid template: %w", messageID, err)
		}
		messages[messageID] = &Message{Data: value, template: parsed}
		return nil
	case map[string]interface{}:
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		var errs []error
		for _, key := range keys {
			nextID := key
			if messageID != "" {
				nextID = messageID + "." + key
			}
			if err := flattenMessages(messages, nextID, value[key]); err != nil {
				errs = append(errs, err)
			}
		}
		return errors.Join(errs...)
	default:
		return fmt.Errorf("messageId %s uses unsupported data format %T", messageID, raw)
	}
}

func validateCatalog(loaded *catalog) error {
	baseline, ok := loaded.messages[loaded.defaultLocale]
	if !ok {
		return fmt.Errorf("default locale %s is not present", loaded.defaultLocale)
	}

	langs := make([]string, 0, len(loaded.messages))
	for lang := range loaded.messages {
		langs = append(langs, lang)
	}
	sort.Strings(langs)

	var errs []error
	for _, lang := range langs {
		if lang == loaded.defaultLocale {
			continue
		}
		messages := loaded.messages[lang]
		for messageID, baseMessage := range baseline {
			message, exists := messages[messageID]
			if !exists {
				errs = append(errs, fmt.Errorf("locale %s is missing messageId %s from %s", lang, messageID, loaded.defaultLocale))
				continue
			}
			if err := validateTemplateFields(loaded.defaultLocale, lang, messageID, baseMessage, message); err != nil {
				errs = append(errs, err)
			}
		}
		for messageID := range messages {
			if _, exists := baseline[messageID]; !exists {
				errs = append(errs, fmt.Errorf("locale %s has unexpected messageId %s", lang, messageID))
			}
		}
	}
	return errors.Join(errs...)
}

func validateTemplateFields(baseLocale, locale, messageID string, baseMessage, message *Message) error {
	baseFields := templateFields(baseMessage.template.Tree.Root)
	fields := templateFields(message.template.Tree.Root)
	if len(baseFields) != len(fields) {
		return fmt.Errorf("locale %s messageId %s template field count differs from %s", locale, messageID, baseLocale)
	}
	for field, count := range baseFields {
		if fields[field] != count {
			return fmt.Errorf("locale %s messageId %s template field %s differs from %s", locale, messageID, field, baseLocale)
		}
	}
	return nil
}

func templateFields(node parse.Node) map[string]int {
	fields := make(map[string]int)
	var walk func(parse.Node)
	walk = func(current parse.Node) {
		if current == nil {
			return
		}
		switch value := current.(type) {
		case *parse.ListNode:
			for _, child := range value.Nodes {
				walk(child)
			}
		case *parse.ActionNode:
			walk(value.Pipe)
		case *parse.IfNode:
			walk(value.Pipe)
			walk(value.List)
			walk(value.ElseList)
		case *parse.RangeNode:
			walk(value.Pipe)
			walk(value.List)
			walk(value.ElseList)
		case *parse.WithNode:
			walk(value.Pipe)
			walk(value.List)
			walk(value.ElseList)
		case *parse.PipeNode:
			for _, command := range value.Cmds {
				walk(command)
			}
		case *parse.CommandNode:
			for _, arg := range value.Args {
				walk(arg)
			}
		case *parse.FieldNode:
			fields[strings.Join(value.Ident, ".")]++
		case *parse.ChainNode:
			walk(value.Node)
			if len(value.Field) > 0 {
				fields[strings.Join(value.Field, ".")]++
			}
		}
	}
	walk(node)
	return fields
}

// Translate returns a localized message. Missing or malformed resources use a stable
// generic fallback and are logged once instead of terminating the process.
func Translate(lang string, messageID string, templateData map[string]interface{}) string {
	catalogMu.RLock()
	loaded := activeCatalog
	catalogMu.RUnlock()

	locale := normalizeLocale(lang)
	message := lookupMessage(loaded, locale, messageID)
	if message == nil {
		warnFallback(locale, messageID, "message_missing")
		return fallbackMessage(locale, messageID)
	}

	var buf bytes.Buffer
	if err := message.template.Execute(&buf, templateData); err != nil {
		warnFallback(locale, messageID, "template_execute")
		return fallbackMessage(locale, messageID)
	}
	return buf.String()
}

func lookupMessage(loaded *catalog, locale, messageID string) *Message {
	if messages := loaded.messages[locale]; messages != nil {
		if message := messages[messageID]; message != nil {
			return message
		}
	}
	if locale != loaded.defaultLocale {
		if messages := loaded.messages[loaded.defaultLocale]; messages != nil {
			if message := messages[messageID]; message != nil {
				warnFallback(locale, messageID, "locale_fallback")
				return message
			}
		}
	}
	return nil
}

func fallbackMessage(locale, messageID string) string {
	english := strings.HasPrefix(normalizeLocale(locale), "en")
	switch {
	case strings.HasSuffix(messageID, ".ErrorLink"):
		return ""
	case strings.HasSuffix(messageID, ".Solution"):
		if english {
			return genericEnglishSolution
		}
		return genericChineseSolution
	default:
		if english {
			return genericEnglishMessage
		}
		return genericChineseMessage
	}
}

func warnFallback(locale, messageID, reason string) {
	key := locale + "|" + messageID + "|" + reason
	if _, loaded := warnedFallback.LoadOrStore(key, struct{}{}); !loaded {
		logger.Warnf("i18n runtime fallback: locale=%s message_id=%s reason=%s", locale, messageID, reason)
	}
}
