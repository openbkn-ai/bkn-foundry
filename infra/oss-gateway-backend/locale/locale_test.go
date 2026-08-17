package locale

import (
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
)

func TestLocaleCatalogsAreComplete(t *testing.T) {
	if err := i18n.ValidateLocaleDir(".", "zh-CN", "zh-CN", "en-US"); err != nil {
		t.Fatalf("validate locale catalogs: %v", err)
	}
}
