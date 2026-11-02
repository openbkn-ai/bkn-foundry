// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/auth"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/sso"
)

func registerSSOExchange(r *gin.Engine, exchange *sso.Exchange) {
	r.POST("/api/safe/v1/auth/token-exchange", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		bearer := c.GetHeader("Authorization")
		if !strings.HasPrefix(bearer, "Bearer ") {
			c.Status(http.StatusUnauthorized)
			return
		}
		ip, _, err := net.SplitHostPort(c.Request.RemoteAddr)
		if err != nil {
			ip = c.Request.RemoteAddr
		}
		result, err := exchange.Swap(c.Request.Context(), c.Query("provider_id"),
			strings.TrimPrefix(bearer, "Bearer "), ip)
		if err != nil {
			switch {
			case errors.Is(err, sso.ErrInvalidToken):
				c.Status(http.StatusUnauthorized)
			case errors.Is(err, sso.ErrForbidden):
				c.Status(http.StatusForbidden)
			default:
				c.Status(http.StatusBadGateway)
			}
			return
		}
		c.JSON(http.StatusOK, result)
	})
	// ClusterIP-only callback for Hydra. The caller authenticates with a secret
	// header and the assertion signature is checked before deriving claims.
	r.POST("/internal/sso/token-hook", func(c *gin.Context) {
		body, err := io.ReadAll(io.LimitReader(c.Request.Body, 32<<10+1))
		if err != nil || len(body) > 32<<10 {
			c.Status(http.StatusBadRequest)
			return
		}
		ext, modified, err := exchange.Hook(c.Request.Context(), c.GetHeader("X-SSO-Hook-Key"), body)
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

func showLoginWithSSO(c *gin.Context, h *auth.HydraAdmin, service *sso.Service) {
	if service != nil && c.Query("local") != "1" && c.Query("login_challenge") != "" {
		providers, err := service.LoginProviders(c.Request.Context())
		if err != nil {
			abortInternalError(c)
			return
		}
		options := make([]ssoLoginOption, 0, len(providers))
		defaultID := ""
		for _, provider := range providers {
			uri := "/sso/login/" + provider.ID + "?login_challenge=" + url.QueryEscape(c.Query("login_challenge"))
			name := provider.DisplayName
			if name == "" {
				name = provider.ID
			}
			options = append(options, ssoLoginOption{Name: name, URL: uri})
			if provider.DefaultForLogin {
				if defaultID != "" {
					abortInternalError(c) // ambiguous active defaults
					return
				}
				defaultID = uri
			}
		}
		if defaultID != "" {
			c.Redirect(http.StatusFound, defaultID)
			return
		}
		c.Set("sso_login_options", options)
	}
	showLogin(c, h)
}

func registerSSO(r *gin.Engine, service *sso.Service, hydra *auth.HydraAdmin, browserPublicURL string) {
	r.GET("/sso/login/:provider", func(c *gin.Context) {
		providerID := c.Param("provider")
		target, state, err := service.Begin(c.Request.Context(), hydra, providerID, c.Query("login_challenge"))
		if err != nil {
			c.Status(http.StatusBadRequest)
			return
		}
		p, err := service.Provider(c.Request.Context(), providerID)
		if err != nil {
			abortInternalError(c)
			return
		}
		cookie := &http.Cookie{Name: "bkn_sso_state_" + providerID, Value: state,
			Path: "/sso/callback/" + providerID, MaxAge: 300, HttpOnly: true,
			SameSite: http.SameSiteLaxMode, Secure: strings.HasPrefix(p.RedirectURI, "https://")}
		http.SetCookie(c.Writer, cookie)
		c.Header("Cache-Control", "no-store")
		c.Redirect(http.StatusFound, target)
	})
	r.GET("/sso/callback/:provider", func(c *gin.Context) {
		providerID := c.Param("provider")
		cookie, err := c.Request.Cookie("bkn_sso_state_" + providerID)
		if err != nil || c.Query("error") != "" {
			c.Status(http.StatusUnauthorized)
			return
		}
		http.SetCookie(c.Writer, &http.Cookie{Name: cookie.Name, Value: "", Path: cookie.Path,
			MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: cookie.Secure})
		redirect, err := service.Complete(c.Request.Context(), hydra, providerID,
			c.Query("state"), cookie.Value, c.Query("code"))
		if err != nil {
			c.Status(http.StatusUnauthorized)
			return
		}
		c.Set(ctxHydraBrowserPublicURL, browserPublicURL)
		redirectToHydra(c, redirect)
	})
}
