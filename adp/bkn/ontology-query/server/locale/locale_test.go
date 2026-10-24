// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package locale

import (
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
)

func TestLocaleMessagesAreComplete(t *testing.T) {
	Register()
	Register()
	if err := i18n.ValidateMessageBundles(); err != nil {
		t.Fatalf("locale message bundle validation failed: %v", err)
	}
}
