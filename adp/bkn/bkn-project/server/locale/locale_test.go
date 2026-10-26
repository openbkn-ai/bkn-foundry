// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import (
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
)

func TestLocaleMessagesAreComplete(t *testing.T) {
	Register()
	Register()
	if err := i18n.ValidateMessageBundles(); err != nil {
		t.Fatal(err)
	}
}

func TestLimitDetailRenders(t *testing.T) {
	Register()
	for language, want := range map[i18n.Language]string{
		i18n.SimplifiedChinese: "limit 必须是 1 到 100 之间的整数。",
		i18n.AmericanEnglish:   "limit must be an integer from 1 to 100.",
	} {
		got, err := i18n.Render(language, "BknProject.Validation.Detail.LimitOutOfRange", map[string]any{"max": 100})
		if err != nil || got != want {
			t.Fatalf("Render(%s) = %q, %v; want %q", language, got, err, want)
		}
	}
}
