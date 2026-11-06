// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package driveradapters holds the HTTP surface the community skeleton serves
// itself — probes and shared middleware — and the helpers the paid build uses
// to mount Workshop's business routes the same way.
package driveradapters

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	libCommon "github.com/openbkn-ai/bkn-foundry/comm-go/common"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/middleware"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"github.com/openbkn-ai/licverify"

	"github.com/openbkn-ai/bkn-foundry/workshop/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/workshop/server/version"
)

const (
	// PublicPrefix is the Ingress-exposed API.
	PublicPrefix = "/api/workshop/v1"
	// InternalPrefix is cluster-internal only: it is never routed by the Ingress.
	InternalPrefix = "/api/workshop/in/v1"

	readyTimeout = 5 * time.Second
	visitorKey   = "workshop.visitor"
)

// Pinger is the part of *sql.DB the readiness probe needs.
type Pinger interface {
	PingContext(ctx context.Context) error
}

// RegisterBase installs the shared middleware and the probes. Run calls it
// before any extension's routes, so every route gets the same middleware.
func RegisterBase(engine *gin.Engine, db Pinger) {
	// The router redirects a trailing slash before any route middleware runs,
	// so an extension's edition gate could not hide a route it covers.
	engine.RedirectTrailingSlash = false
	engine.Use(accessLog())
	engine.Use(middleware.TracingMiddleware())
	engine.Use(rest.LanguageMiddleware())

	engine.GET(PublicPrefix+"/health", healthCheck)
	engine.GET(PublicPrefix+"/ready", readyCheck(db))
	engine.GET(InternalPrefix+"/health", healthCheck)

	logger.Info("Workshop base routes registered")
}

func healthCheck(c *gin.Context) {
	rest.ReplyOK(c, http.StatusOK, gin.H{
		"ServerName":    version.ServerName,
		"ServerVersion": version.ServerVersion,
		"Language":      version.LanguageGo,
		"GoVersion":     version.GoVersion,
		"GoArch":        version.GoArch,
	})
}

func readyCheck(db Pinger) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), readyTimeout)
		defer cancel()

		if err := db.PingContext(ctx); err != nil {
			logger.Warnf("Readiness check failed: database ping failed: %v", err)
			rest.ReplyError(c, httperrors.NewHTTPError(c.Request.Context(), http.StatusServiceUnavailable, httperrors.PublicError_ServiceUnavailable).
				WithErrorDetails(render(c.Request.Context(), "Workshop.Validation.Detail.DependencyUnavailable")))
			return
		}
		rest.ReplyOK(c, http.StatusOK, gin.H{"status": "ready"})
	}
}

// RequireEdition hides a route group below min: the caller gets exactly what
// the community binary answers for a route it does not have — gin's plain 404
// and no Cache-Control — so an unlicensed paid binary is indistinguishable
// from the community one. Mount it before any middleware that writes headers.
func RequireEdition(min licverify.Edition) gin.HandlerFunc {
	return func(c *gin.Context) {
		if entitlement.AtLeast(min) {
			c.Next()
			return
		}
		logger.Infof("workshop: route hidden because licence is below the required tier: min_edition=%s edition=%s method=%s path=%s",
			min, entitlement.Current(), c.Request.Method, c.Request.URL.Path)
		c.Writer.Header().Del("Cache-Control")
		c.Abort()
		c.Data(http.StatusNotFound, gin.MIMEPlain, []byte("404 page not found"))
	}
}

// Authenticate verifies the bearer token and stores the visitor for
// VisitorFrom. Identity headers sent by the caller (x-account-*) are never
// read on public routes.
func Authenticate(aa interfaces.AuthAccess) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		visitor, err := aa.VerifyToken(ctx, c)
		if err != nil {
			c.Abort()
			rest.ReplyError(c, httperrors.NewHTTPError(ctx, http.StatusUnauthorized, httperrors.PublicError_Unauthorized).
				WithErrorDetails(render(ctx, "Workshop.Validation.Detail.AuthenticationFailed")))
			return
		}
		c.Set(visitorKey, visitor)
		c.Next()
	}
}

// VisitorFrom returns the visitor Authenticate stored.
func VisitorFrom(c *gin.Context) (hydra.Visitor, bool) {
	value, ok := c.Get(visitorKey)
	if !ok {
		return hydra.Visitor{}, false
	}
	visitor, ok := value.(hydra.Visitor)
	return visitor, ok
}

func render(ctx context.Context, id string) string {
	text, err := i18n.Render(i18n.GetLanguageByCtx(ctx), id, nil)
	if err != nil {
		logger.Errorf("localized message rendering failed: %v", err)
	}
	return text
}

func accessLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		beginTime := time.Now()
		c.Next()
		endTime := time.Now()
		logger.Debugf("access log: url: %s, method: %s, status: %d, begin_time: %s, end_time: %s, subTime: %f",
			c.Request.URL.Path,
			c.Request.Method,
			c.Writer.Status(),
			beginTime.Format(libCommon.RFC3339Milli),
			endTime.Format(libCommon.RFC3339Milli),
			endTime.Sub(beginTime).Seconds(),
		)
	}
}
