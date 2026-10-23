// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import (
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
)

func TestMessageBundles(t *testing.T) {
	i18n.RegisterI18n(&TextMessagesEnUS)
	i18n.RegisterI18n(&TextMessagesZhCN)
	i18n.RegisterI18n(&ErrorMessagesEnUS)
	i18n.RegisterI18n(&ErrorMessagesZhCN)
	if err := i18n.ValidateMessageBundles(); err != nil {
		t.Fatal(err)
	}
}
