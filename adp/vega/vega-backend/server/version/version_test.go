package version

import (
	"runtime"
	"testing"

	"github.com/openbkn-ai/bkn-comm-go/audit"
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

func TestAuditDefaultSource(t *testing.T) {
	t.Run("sets default audit source", func(t *testing.T) {
		assert.Equal(t, audit.AuditLogFrom{
			Package: "Vega",
			Service: audit.AuditLogFromService{
				Name: "vega-backend",
			},
		}, audit.DEFAULT_AUDIT_LOG_FROM)
	})
}
