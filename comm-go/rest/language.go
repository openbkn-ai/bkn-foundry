// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package rest

import (
	"context"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
)

// Language identifies a supported response language.
type Language = string

const (
	SimplifiedChinese Language = "zh-CN"
	AmericanEnglish   Language = "en-US"
)

type key string

const (
	LanguageKey           key = "language"
	AcceptLanguageHeader      = "Accept-Language"
	ContentLanguageHeader     = "Content-Language"
)

var (
	Languages = map[Language]Language{
		SimplifiedChinese: SimplifiedChinese,
		AmericanEnglish:   AmericanEnglish,
	}
	DefaultLanguage = SimplifiedChinese
)

type languageRange struct {
	language Language
	quality  float64
	position int
	wildcard bool
}

// SetLang configures the service fallback language during startup.
func SetLang(langStr string) {
	if lang, ok := normalizeLanguage(langStr); ok {
		DefaultLanguage = lang
	}
}

// ResolveLanguage selects the first supported language from Accept-Language.
// Unsupported, malformed, and explicitly rejected candidates never fail a request.
func ResolveLanguage(acceptLanguage string) Language {
	ranges := parseAcceptLanguage(acceptLanguage)
	if len(ranges) == 0 {
		return DefaultLanguage
	}

	explicitRanges := make(map[Language]languageRange, len(ranges))
	var wildcard languageRange
	hasWildcard := false
	for _, item := range ranges {
		if item.wildcard {
			wildcard = item
			hasWildcard = true
			continue
		}
		explicitRanges[item.language] = item
	}

	candidates := make([]languageRange, 0, len(Languages))
	for _, lang := range supportedLanguagesByDefault() {
		if item, ok := explicitRanges[lang]; ok {
			if item.quality > 0 {
				candidates = append(candidates, item)
			}
			continue
		}
		if hasWildcard && wildcard.quality > 0 {
			wildcard.language = lang
			candidates = append(candidates, wildcard)
		}
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].quality == candidates[j].quality {
			return candidates[i].position < candidates[j].position
		}
		return candidates[i].quality > candidates[j].quality
	})

	if len(candidates) > 0 {
		return candidates[0].language
	}

	if lang, ok := defaultAllowedLanguage(explicitRanges, wildcard, hasWildcard); ok {
		return lang
	}
	return DefaultLanguage
}

func supportedLanguagesByDefault() []Language {
	languages := []Language{DefaultLanguage}
	for _, lang := range []Language{SimplifiedChinese, AmericanEnglish} {
		if lang != DefaultLanguage {
			languages = append(languages, lang)
		}
	}
	return languages
}

// GetLanguageCtx resolves the request language once and stores it in the request context.
func GetLanguageCtx(c *gin.Context) context.Context {
	return WithLanguage(c.Request.Context(), ResolveLanguage(c.GetHeader(AcceptLanguageHeader)))
}

// WithLanguage attaches a supported language to a context.
func WithLanguage(ctx context.Context, lang Language) context.Context {
	canonical, ok := normalizeLanguage(lang)
	if !ok {
		lang = DefaultLanguage
	} else {
		lang = canonical
	}
	return context.WithValue(ctx, LanguageKey, lang)
}

// GetLanguageByCtx returns the effective language attached to a context.
func GetLanguageByCtx(ctx context.Context) Language {
	if ctx == nil {
		return DefaultLanguage
	}
	lang, ok := ctx.Value(LanguageKey).(Language)
	if !ok {
		return DefaultLanguage
	}
	if _, ok := Languages[lang]; !ok {
		return DefaultLanguage
	}
	return lang
}

func parseAcceptLanguage(header string) []languageRange {
	merged := make(map[string]languageRange)
	for position, item := range strings.Split(header, ",") {
		parts := strings.Split(item, ";")
		if len(parts) == 0 {
			continue
		}
		rangeValue := strings.TrimSpace(parts[0])
		if rangeValue == "" {
			continue
		}

		quality, valid := parseQuality(parts[1:])
		if !valid {
			continue
		}

		wildcard := rangeValue == "*"
		lang, supported := normalizeLanguage(rangeValue)
		if !wildcard && !supported {
			continue
		}
		key := string(lang)
		if wildcard {
			key = "*"
		}

		candidate := languageRange{language: lang, quality: quality, position: position, wildcard: wildcard}
		previous, exists := merged[key]
		if !exists || candidate.quality > previous.quality {
			merged[key] = candidate
		}
	}

	ranges := make([]languageRange, 0, len(merged))
	for _, item := range merged {
		ranges = append(ranges, item)
	}
	return ranges
}

func parseQuality(params []string) (float64, bool) {
	quality := 1.0
	qualitySeen := false
	for _, param := range params {
		name, value, ok := strings.Cut(strings.TrimSpace(param), "=")
		if !ok || !strings.EqualFold(strings.TrimSpace(name), "q") {
			return 0, false
		}
		if qualitySeen {
			return 0, false
		}
		parsed, ok := parseQValue(strings.TrimSpace(value))
		if !ok {
			return 0, false
		}
		qualitySeen = true
		quality = parsed
	}
	return quality, true
}

func parseQValue(value string) (float64, bool) {
	if value == "" || (value[0] != '0' && value[0] != '1') {
		return 0, false
	}
	if len(value) == 1 {
		return float64(value[0] - '0'), true
	}
	if len(value) < 3 || len(value) > 5 || value[1] != '.' {
		return 0, false
	}

	quality := 0.0
	place := 0.1
	for _, digit := range value[2:] {
		if digit < '0' || digit > '9' {
			return 0, false
		}
		if value[0] == '1' && digit != '0' {
			return 0, false
		}
		quality += float64(digit-'0') * place
		place /= 10
	}
	if value[0] == '1' {
		return 1, true
	}
	return quality, true
}

func normalizeLanguage(value string) (Language, bool) {
	normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), "_", "-"))
	if !validLanguageRange(normalized) {
		return "", false
	}
	switch {
	case normalized == "zh", normalized == "zh-cn", normalized == "zh-hans",
		strings.HasPrefix(normalized, "zh-cn-"), strings.HasPrefix(normalized, "zh-hans-"):
		return SimplifiedChinese, true
	case normalized == "en", strings.HasPrefix(normalized, "en-"):
		return AmericanEnglish, true
	default:
		return "", false
	}
}

func validLanguageRange(value string) bool {
	if value == "" || strings.HasPrefix(value, "-") || strings.HasSuffix(value, "-") {
		return false
	}
	for _, subtag := range strings.Split(value, "-") {
		if len(subtag) == 0 || len(subtag) > 8 {
			return false
		}
		for _, char := range subtag {
			if (char < 'a' || char > 'z') && (char < '0' || char > '9') {
				return false
			}
		}
	}
	return true
}

func isRejected(lang Language, explicitRanges map[Language]languageRange, wildcard languageRange, hasWildcard bool) bool {
	if item, ok := explicitRanges[lang]; ok {
		return item.quality == 0
	}
	return hasWildcard && wildcard.quality == 0
}

func defaultAllowedLanguage(explicitRanges map[Language]languageRange, wildcard languageRange, hasWildcard bool) (Language, bool) {
	if !isRejected(DefaultLanguage, explicitRanges, wildcard, hasWildcard) {
		return DefaultLanguage, true
	}
	for _, lang := range []Language{SimplifiedChinese, AmericanEnglish} {
		if !isRejected(lang, explicitRanges, wildcard, hasWildcard) {
			return lang, true
		}
	}
	return "", false
}
