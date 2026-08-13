package common

import (
	"context"

	"github.com/gin-gonic/gin"
	sharedrest "github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

type Language = sharedrest.Language

const (
	SimplifiedChinese = sharedrest.SimplifiedChinese
	AmericanEnglish   = sharedrest.AmericanEnglish
)

var (
	Languages       = sharedrest.Languages
	DefaultLanguage = sharedrest.DefaultLanguage
)

func SetLang(langStr string) {
	sharedrest.SetLang(langStr)
	DefaultLanguage = sharedrest.DefaultLanguage
}

func GetLanguageInfo(c *gin.Context) Language {
	return sharedrest.ResolveLanguage(c.GetHeader(sharedrest.AcceptLanguageHeader))
}

func GetLanguageByCtx(ctx context.Context) Language {
	return sharedrest.GetLanguageByCtx(ctx)
}

func SetLanguageByCtx(ctx context.Context, lang Language) context.Context {
	return sharedrest.WithLanguage(ctx, lang)
}
