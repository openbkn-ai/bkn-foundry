// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package errors

import (
	"context"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	sharedrest "github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

func TestDefaultHTTPErrorUsesBCP47Locale(t *testing.T) {
	for _, locale := range []string{sharedrest.AmericanEnglish, "en-us"} {
		t.Run(locale, func(t *testing.T) {
			ctx := common.SetLanguageToCtx(context.Background(), locale)
			err := DefaultHTTPError(ctx, http.StatusUnauthorized, "token is invalid")

			if err.Description != "Authentication failed" {
				t.Errorf("Description = %q, want English translation", err.Description)
			}
			if err.Solution != "Contact administrator" {
				t.Errorf("Solution = %q, want English translation", err.Solution)
			}
		})
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
			ctx := common.SetLanguageToCtx(context.Background(), tt.locale)
			if got := LocalizedDetail(ctx, "FunctionCodeRequired"); got != tt.want {
				t.Fatalf("LocalizedDetail() = %q, want %q", got, tt.want)
			}
		})
	}
}
