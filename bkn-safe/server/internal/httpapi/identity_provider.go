// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
package httpapi

import (
	"crypto/subtle"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/federation"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/auth"
)

func showLoginWithIdentityProvider(c *gin.Context, h *auth.HydraAdmin, extension *federation.Extension) {
	if extension != nil && extension.BeforeLogin != nil && c.Query("local") != "1" && c.Query("login_challenge") != "" {
		page, err := extension.BeforeLogin(c)
		if err != nil {
			abortInternalError(c)
			return
		}
		if page.Redirect != "" {
			c.Redirect(http.StatusFound, page.Redirect)
			return
		}
		c.Set("identity_login_options", page.Options)
	}
	showLogin(c, h)
}

// The shared Chart configures this authenticated hook for both images. A
// community build leaves token sessions untouched; EE can supply enrichment.
// Never expose this route through Ingress, and never accept an empty key.
func registerTokenHook(r *gin.Engine, key string, extension *federation.Extension) {
	r.POST("/internal/sso/token-hook", func(c *gin.Context) {
		if key == "" || subtle.ConstantTimeCompare([]byte(key), []byte(c.GetHeader("X-SSO-Hook-Key"))) != 1 {
			c.Status(http.StatusForbidden)
			return
		}
		body, err := io.ReadAll(io.LimitReader(c.Request.Body, (32<<10)+1))
		if err != nil || len(body) > 32<<10 {
			c.Status(http.StatusBadRequest)
			return
		}
		if extension == nil || extension.TokenHook == nil {
			c.Status(http.StatusNoContent)
			return
		}
		ext, modified, err := extension.TokenHook(c.Request.Context(), body)
		if err != nil {
			c.Status(http.StatusForbidden)
			return
		}
		if !modified {
			c.Status(http.StatusNoContent)
			return
		}
		c.JSON(http.StatusOK, gin.H{"session": gin.H{"access_token": ext}})
	})
}

// RedirectIdentityContinuation preserves the browser origin for Hydra continuations.
func RedirectIdentityContinuation(c *gin.Context, target, publicURL string) {
	c.Redirect(http.StatusFound, relativeHydraRedirect(target, publicURL))
}
