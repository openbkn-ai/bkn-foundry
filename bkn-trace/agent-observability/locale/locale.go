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

func init() {
	Register()
}
