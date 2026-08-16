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

func TestLocaleCatalogsAreComplete(t *testing.T) {
	if err := i18n.ValidateLocaleDir(".", "zh-CN", "zh-CN", "en-US"); err != nil {
		t.Fatalf("locale catalog validation failed: %v", err)
	}
}
