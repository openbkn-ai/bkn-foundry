// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import (
	"sync"

	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
)

var registerOnce sync.Once

// Register installs the service messages once before error code registration.
func Register() {
	registerOnce.Do(func() {
		i18n.RegisterI18n(&errorcodeEnUS)
		i18n.RegisterI18n(&errorcodeZhCN)
		i18n.RegisterI18n(&releaseEnUS)
		i18n.RegisterI18n(&releaseZhCN)
		i18n.RegisterI18n(&lineageEnUS)
		i18n.RegisterI18n(&lineageZhCN)
		i18n.RegisterI18n(&projectEnUS)
		i18n.RegisterI18n(&projectZhCN)
		i18n.RegisterI18n(&draftEnUS)
		i18n.RegisterI18n(&draftZhCN)

		if err := i18n.ValidateMessageBundles(); err != nil {
			panic(err)
		}
	})
}
