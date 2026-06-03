// Package httpapi wires bkn-safe's HTTP surface: health, the authz API, the
// user-directory API, and the hydra login/consent/device provider pages.
package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"bkn-safe/internal/authz"
)

// Deps are the collaborators the HTTP layer needs.
type Deps struct {
	Enforcer *authz.Enforcer
	DB       *gorm.DB
}

// New builds the gin engine with all routes mounted.
func New(deps Deps) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())

	r.GET("/health/ready", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })
	r.GET("/health/alive", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })

	// authz API (clean redesign). Provider pages (login/consent/device) and the
	// user-directory API are mounted in later phases.
	registerAuthz(r, deps.Enforcer, deps.DB)

	return r
}
