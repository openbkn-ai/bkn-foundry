// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package i18n

import (
	"bytes"
	"fmt"
	"maps"
	"strings"
	gotemplate "text/template"
)

// I18nMessages is one module's messages for one language. Startup copies and compiles
// its data; subsequent changes to the input map cannot alter installed templates.
type I18nMessages struct {
	Locale   Language
	Messages map[string]string
}

type compiledI18nMessages struct {
	Locale   Language
	Messages map[string]*gotemplate.Template
}

var compiledI18nMessagesBundle = make(map[Language]*compiledI18nMessages)

func init() {
	RegisterI18n(&publicErrorsZhCN)
	RegisterI18n(&publicErrorsEnUS)
}

// RegisterI18n compiles and appends one module's messages for one language.
// Call it serially during startup; existing keys cannot be overwritten.
// Cross-language consistency is checked separately by ValidateMessageBundles.
func RegisterI18n(definition *I18nMessages) {
	compiled, err := compileMessages(definition)
	if err != nil {
		panic(fmt.Errorf("i18n definition validation: %w", err))
	}
	if err := appendMessages(compiled); err != nil {
		panic(err)
	}
}

// compileMessages converts one definition without registering or comparing languages.
func compileMessages(definition *I18nMessages) (*compiledI18nMessages, error) {
	messages := &compiledI18nMessages{
		Locale:   definition.Locale,
		Messages: make(map[string]*gotemplate.Template),
	}
	for id, value := range definition.Messages {
		id = strings.TrimSpace(id)
		value = strings.TrimSpace(value)
		if id == "" {
			return nil, fmt.Errorf("locale %s has an empty message ID", definition.Locale)
		}
		if value == "" {
			return nil, fmt.Errorf("locale %s messageId %s is an empty string", definition.Locale, id)
		}
		if _, exists := messages.Messages[id]; exists {
			return nil, fmt.Errorf("locale %s messageId %s already exists after trimming", definition.Locale, id)
		}
		parsed, err := gotemplate.New(id).Option("missingkey=default").Parse(value)
		if err != nil {
			return nil, fmt.Errorf("locale %s messageId %s has an invalid template: %w", definition.Locale, id, err)
		}
		messages.Messages[id] = parsed
	}
	return messages, nil
}

// appendMessages rejects all conflicting keys before appending any content.
func appendMessages(messages *compiledI18nMessages) error {
	current, exists := compiledI18nMessagesBundle[messages.Locale]
	if !exists {
		compiledI18nMessagesBundle[messages.Locale] = messages
		return nil
	}
	for id := range messages.Messages {
		if _, exists := current.Messages[id]; exists {
			return fmt.Errorf("locale %s messageId %s already exists", messages.Locale, id)
		}
	}
	maps.Copy(current.Messages, messages.Messages)
	return nil
}

// ValidateMessageBundles checks required languages and message IDs,
// returning the first inconsistency.
// Call it after all startup registrations; it does not modify the global bundle.
func ValidateMessageBundles() error {
	for _, required := range Languages {
		if _, exists := compiledI18nMessagesBundle[required]; !exists {
			return fmt.Errorf("required locale %s is not present", required)
		}
	}
	baselineLocale := DefaultLanguage
	baseline, exists := compiledI18nMessagesBundle[baselineLocale]
	if !exists {
		return fmt.Errorf("default locale %s is not present", baselineLocale)
	}
	for lang, messages := range compiledI18nMessagesBundle {
		if lang == baselineLocale {
			continue
		}
		for id := range baseline.Messages {
			if _, exists := messages.Messages[id]; !exists {
				return fmt.Errorf("locale %s is missing messageId %s from %s", lang, id, baselineLocale)
			}
		}
		if len(baseline.Messages) != len(messages.Messages) {
			for id := range messages.Messages {
				if _, exists := baseline.Messages[id]; !exists {
					return fmt.Errorf("locale %s has unexpected messageId %s", lang, id)
				}
			}
		}
	}
	return nil
}

// RenderFailure identifies a lookup or execution defect. Missing map keys are
// deliberately permitted by Go's missingkey=default policy.
type RenderFailure string

const (
	LocaleMissing     RenderFailure = "locale_missing"
	MessageMissing    RenderFailure = "message_missing"
	TemplateExecution RenderFailure = "template_execute"
)

// RenderError contains identifiers only, never template contents or parameter values.
type RenderError struct {
	Locale    Language
	MessageID string
	Kind      RenderFailure
}

func (e *RenderError) Error() string {
	return fmt.Sprintf("i18n %s: locale=%s message_id=%s", e.Kind, e.Locale, e.MessageID)
}

// Render resolves a message in the requested language and executes its template.
// Missing languages and messages return errors; no default-language fallback is applied.
func Render(lang Language, messageID string, templateData map[string]any) (string, error) {
	locale := lang
	messages, exists := compiledI18nMessagesBundle[locale]
	if !exists {
		return "", &RenderError{Locale: locale, MessageID: messageID, Kind: LocaleMissing}
	}
	message, exists := messages.Messages[messageID]
	if !exists {
		return "", &RenderError{Locale: locale, MessageID: messageID, Kind: MessageMissing}
	}
	var buf bytes.Buffer
	if err := message.Execute(&buf, templateData); err != nil {
		return "", &RenderError{Locale: locale, MessageID: messageID, Kind: TemplateExecution}
	}
	return buf.String(), nil
}

// ValidateMessages checks each ID in all registered languages without executing templates.
func ValidateMessages(messageIDs ...string) error {
	for locale, messages := range compiledI18nMessagesBundle {
		for _, id := range messageIDs {
			if _, exists := messages.Messages[id]; !exists {
				return &RenderError{Locale: locale, MessageID: id, Kind: MessageMissing}
			}
		}
	}
	return nil
}
