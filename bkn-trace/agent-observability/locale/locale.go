// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

// Package locale provides request-scoped localization for Agent Observability.
package locale

import (
	"context"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
)

const messagePrefix = "AgentObservability.Message."

var registerOnce sync.Once

type negotiatedLocaleKey struct{}

// Register validates the compiled-in resources during startup.
func Register() {
	registerOnce.Do(func() {
		i18n.RegisterI18n(&agentObservabilityEnUS)
		i18n.RegisterI18n(&agentObservabilityZhCN)
		if err := i18n.ValidateMessageBundles(); err != nil {
			panic(err)
		}
	})
}

// Translate localizes an existing English response message without changing
// its surrounding machine-readable error contract.
func Translate(ctx context.Context, message string) string {
	return TranslateWithData(ctx, message, nil)
}

// TranslateWithData localizes a message and renders its runtime template data.
func TranslateWithData(ctx context.Context, message string, data map[string]any) string {
	Register()
	text, err := i18n.Render(i18n.GetLanguageByCtx(ctx), messagePrefix+message, data)
	if err != nil {
		logger.Errorf("localized service message failed: %v", err)
	}
	return text
}

// WithLanguage attaches an explicitly negotiated locale. The marker lets
// adapter-level unit tests that call handlers directly retain their legacy
// English fixtures unless they opt into the HTTP language contract.
func WithLanguage(ctx context.Context, language i18n.Language) context.Context {
	ctx = i18n.WithLanguage(ctx, language)
	return context.WithValue(ctx, negotiatedLocaleKey{}, true)
}

// IsNegotiated reports whether the request passed through language negotiation.
func IsNegotiated(ctx context.Context) bool {
	negotiated, _ := ctx.Value(negotiatedLocaleKey{}).(bool)
	return negotiated
}
