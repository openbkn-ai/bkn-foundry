// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
)

func TestResourcesAreComplete(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve locale test path")
	}
	if err := i18n.ValidateLocaleDir(filepath.Dir(filename), "zh-CN", "zh-CN", "en-US"); err != nil {
		t.Fatalf("validate locale resources: %v", err)
	}
}
