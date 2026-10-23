// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package locale loads BKN Safe's translation resources.
package locale

import (
	"sync"

	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
)

var registerOnce sync.Once

// Register validates the compiled-in resources during startup.
func Register() {
	registerOnce.Do(func() {
		i18n.RegisterI18n(&bknSafeEnUS)
		i18n.RegisterI18n(&bknSafeZhCN)

		if err := i18n.ValidateMessageBundles(); err != nil {
			panic(err)
		}
	})
}

// Translate resolves a BKN Safe message after ensuring the bundled message bundle is
// registered. Callers pass the effective request language selected by the
// platform language middleware.
func Translate(language i18n.Language, messageID string) string {
	Register()
	text, err := i18n.Render(language, messageID, nil)
	if err != nil {
		logger.Errorf("localized service message failed: %v", err)
	}
	return text
}
