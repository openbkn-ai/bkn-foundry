// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package common

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
)

type Language = i18n.Language

const (
	SimplifiedChinese = i18n.SimplifiedChinese
	AmericanEnglish   = i18n.AmericanEnglish
)

var (
	Languages       = i18n.Languages
	DefaultLanguage = i18n.DefaultLanguage
)

func SetLang(langStr string) {
	i18n.SetLang(langStr)
	DefaultLanguage = i18n.DefaultLanguage
}

func GetLanguageInfo(c *gin.Context) Language {
	return i18n.ResolveLanguage(c.GetHeader(i18n.AcceptLanguageHeader))
}

func GetLanguageByCtx(ctx context.Context) Language {
	return i18n.GetLanguageByCtx(ctx)
}

func SetLanguageByCtx(ctx context.Context, lang Language) context.Context {
	return i18n.WithLanguage(ctx, lang)
}
