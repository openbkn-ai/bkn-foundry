// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import (
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"testing"
)

func TestLocaleMessagesAreComplete(t *testing.T) {
	Register()
	Register()
	if err := i18n.ValidateMessageBundles(); err != nil {
		t.Fatal(err)
	}
}
