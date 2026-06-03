// Package httpapi wires bkn-safe's HTTP surface: health, the authz API, the
// user-directory API, and the hydra login/consent/device provider pages.
package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"bkn-safe/internal/auth"
	"bkn-safe/internal/authz"
	"bkn-safe/internal/directory"
)

// Deps are the collaborators the HTTP layer needs.
type Deps struct {
	Enforcer  *authz.Enforcer
	DB        *gorm.DB
	Provider  *auth.Provider
	Hydra     *auth.HydraAdmin
	Directory *directory.Service
}

// New builds the gin engine with all routes mounted.
func New(deps Deps) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())

	r.GET("/health/ready", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })
	r.GET("/health/alive", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })

	// authz API (clean redesign).
	registerAuthz(r, deps.Enforcer, deps.DB)

	// hydra login/consent/device provider pages.
	if deps.Provider != nil && deps.Hydra != nil {
		registerAuth(r, deps.Provider, deps.Hydra)
	}

	// user-directory API.
	if deps.Directory != nil {
		registerDirectory(r, deps.Directory)
	}

	return r
}
