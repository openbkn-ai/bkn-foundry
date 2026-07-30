// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package rest

import (
	"context"
	"strings"

	"github.com/gin-gonic/gin"
	"golang.org/x/text/language"
)

// Language 语言类型
type Language = string

// 语言类型
const (
	SimplifiedChinese Language = "zh-CN" // 简体中文
	//TraditionalChinese          // 繁体中文
	AmericanEnglish Language = "en-US" // 美国英语
)

type key string

const XLangKey key = "X-Language"

const (
	XLangHeader = "X-Language"
)

var (
	// Langs 支持的语言
	Languages = map[Language]Language{
		SimplifiedChinese: SimplifiedChinese,
		AmericanEnglish:   AmericanEnglish,
	}
	DefaultLanguage = SimplifiedChinese
)

var (
	langMatcher = language.NewMatcher([]language.Tag{
		language.SimplifiedChinese,
		language.AmericanEnglish,
	})

	langTagMap = map[language.Tag]Language{
		language.SimplifiedChinese: SimplifiedChinese,
		language.AmericanEnglish:   AmericanEnglish,
	}
)

// SetLang 设置语言
func SetLang(langStr string) {
	lang := GetBCP47(langStr)
	DefaultLanguage = lang
}

// getXLang 解析获取 Header x-language
func GetXLang(c *gin.Context) Language {
	lang := GetBCP47(c.GetHeader(XLangHeader))
	langTag, _ := language.MatchStrings(langMatcher, string(lang))
	return langTagMap[langTag]
}

// getBCP47 将约定的语言标签转换为符合BCP47标准的语言标签
// 默认值为 zh-CN, 中国大陆简体中文
// https://www.rfc-editor.org/info/bcp47
func GetBCP47(langStr string) Language {
	switch strings.ToLower(langStr) {
	case "zh_cn", "zh-cn":
		return SimplifiedChinese
	case "en_us", "en-us":
		return AmericanEnglish
	default:
		return DefaultLanguage
	}
}

func GetLanguageCtx(c *gin.Context) context.Context {
	lang := GetBCP47(c.GetHeader(XLangHeader))
	return context.WithValue(c.Request.Context(), XLangKey, lang)
}

func GetLanguageByCtx(ctx context.Context) Language {
	lang := DefaultLanguage
	langV := ctx.Value(XLangKey)
	if langV != nil {
		lang = langV.(Language)
	}
	if _, ok := Languages[lang]; !ok {
		lang = DefaultLanguage
	}
	return lang
}
