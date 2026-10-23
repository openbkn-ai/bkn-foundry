// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package errors

import (
	"context"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
)

func TestDefaultHTTPErrorUsesBCP47Locale(t *testing.T) {
	ctx := common.SetLanguageToCtx(context.Background(), i18n.AmericanEnglish)
	err := DefaultHTTPError(ctx, http.StatusUnauthorized, "token is invalid")
	if renderErr := err.Render(); renderErr != nil {
		t.Fatal(renderErr)
	}
	if err.Description != "Authentication failed. Provide valid authentication credentials." {
		t.Fatalf("description = %q", err.Description)
	}
}

func TestLocalizedDetailUsesBCP47Locale(t *testing.T) {
	tests := []struct {
		locale string
		want   string
	}{
		{locale: "en-US", want: "Code is required for sandbox execution."},
		{locale: "zh-CN", want: "沙箱执行必须提供代码。"},
	}
	for _, tt := range tests {
		t.Run(tt.locale, func(t *testing.T) {
			ctx := common.SetLanguageToCtx(context.Background(), i18n.ResolveLanguage(tt.locale))
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "agentRetrieval.Detail.FunctionCodeRequired", map[string]any{})
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			if got := localizedText; got != tt.want {
				t.Fatalf("LocalizedDetail() = %q, want %q", got, tt.want)
			}
		})
	}
}
