// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package locale loads and resolves OSS Gateway translation resources.
package locale

import (
	"context"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
)

var registerOnce sync.Once

// Register validates the compiled-in resources during startup.
func Register() {
	registerOnce.Do(func() {
		i18n.RegisterI18n(&ossGatewayEnUS)
		i18n.RegisterI18n(&ossGatewayZhCN)

		if err := i18n.ValidateMessageBundles(); err != nil {
			panic(err)
		}
	})
}

// Translate renders a resource with the effective request locale.
func Translate(ctx context.Context, messageID string, templateData map[string]interface{}) string {
	Register()
	text, err := i18n.Render(i18n.GetLanguageByCtx(ctx), messageID, templateData)
	if err != nil {
		logger.Errorf("localized service message failed: %v", err)
	}
	return text
}
