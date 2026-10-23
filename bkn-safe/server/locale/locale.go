// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package locale loads BKN Safe's translation resources.
package locale

import (
	"sync"

	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
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

// init installs service messages before consumers render them.
func init() {
	Register()
}
