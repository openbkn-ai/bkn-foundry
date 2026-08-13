// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package rest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestResolveLanguage(t *testing.T) {
	originalDefault := DefaultLanguage
	DefaultLanguage = SimplifiedChinese
	t.Cleanup(func() { DefaultLanguage = originalDefault })

	tests := []struct {
		name   string
		header string
		want   Language
	}{
		{name: "selects first supported candidate", header: "fr, en-US;q=0.8", want: AmericanEnglish},
		{name: "honors weights", header: "zh-CN;q=0.5, en-US;q=0.9", want: AmericanEnglish},
		{name: "matches English regional variants", header: "en-GB", want: AmericanEnglish},
		{name: "normalizes aliases", header: "zh_Hans", want: SimplifiedChinese},
		{name: "excludes rejected language from wildcard", header: "en;q=0, *;q=1", want: SimplifiedChinese},
		{name: "preserves explicit language when wildcard rejects others", header: "en-US, *;q=0", want: AmericanEnglish},
		{name: "uses wildcard only for unmatched languages", header: "zh-CN;q=0.2, *;q=0.9", want: AmericanEnglish},
		{name: "uses another supported language when default is rejected", header: "zh-CN;q=0", want: AmericanEnglish},
		{name: "uses highest repeated weight", header: "en;q=0, en;q=1", want: AmericanEnglish},
		{name: "ignores invalid weight", header: "en-US;q=2, zh-CN;q=0.8", want: SimplifiedChinese},
		{name: "ignores NaN weight", header: "en-US;q=NaN, zh-CN;q=0.5", want: SimplifiedChinese},
		{name: "ignores exponent weight", header: "en-US;q=1e-1, zh-CN;q=0.5", want: SimplifiedChinese},
		{name: "ignores signed weight", header: "en-US;q=+0.5, zh-CN;q=0.4", want: SimplifiedChinese},
		{name: "ignores over precise weight", header: "en-US;q=0.1234, zh-CN;q=0.2", want: SimplifiedChinese},
		{name: "uses default for empty header", header: "", want: SimplifiedChinese},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResolveLanguage(tt.header); got != tt.want {
				t.Fatalf("ResolveLanguage(%q) = %q, want %q", tt.header, got, tt.want)
			}
		})
	}
}

func TestResolveLanguageDoesNotMapTraditionalChineseToSimplifiedChinese(t *testing.T) {
	originalDefault := DefaultLanguage
	DefaultLanguage = AmericanEnglish
	t.Cleanup(func() { DefaultLanguage = originalDefault })

	if got := ResolveLanguage("zh-Hant"); got != AmericanEnglish {
		t.Fatalf("ResolveLanguage(zh-Hant) = %q, want default %q", got, AmericanEnglish)
	}
}

func TestGetLanguageCtx(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request.Header.Set(AcceptLanguageHeader, "en-US")

	if got := GetLanguageByCtx(GetLanguageCtx(c)); got != AmericanEnglish {
		t.Fatalf("GetLanguageCtx() = %q, want %q", got, AmericanEnglish)
	}
}

func TestWithLanguage(t *testing.T) {
	ctx := WithLanguage(context.Background(), AmericanEnglish)
	if got := GetLanguageByCtx(ctx); got != AmericanEnglish {
		t.Fatalf("GetLanguageByCtx() = %q, want %q", got, AmericanEnglish)
	}
}
