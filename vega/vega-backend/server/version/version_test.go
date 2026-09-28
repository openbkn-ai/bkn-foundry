package version

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestVersionMetadata(t *testing.T) {
	t.Run("exports runtime metadata", func(t *testing.T) {
		assert.Equal(t, "vega-backend", ServerName)
		assert.NotEmpty(t, ServerVersion)
		assert.Equal(t, "go", LanguageGo)
		assert.Equal(t, runtime.Version(), GoVersion)
		assert.Equal(t, runtime.GOARCH, GoArch)
	})
}
