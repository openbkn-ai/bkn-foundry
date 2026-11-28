// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const authzPathPrefix = "/api/safe/v1/authz/"

// requestLogMiddleware writes one diagnostic line for each request bkn-safe
// answers with a failure. Error bodies carry only a localized public message,
// so this line is where the cause is kept: the status, the duration, the
// request id, and any errors handlers attached with c.Error.
//
// Every 5xx is logged. On the authz API, whose callers are other services, 4xx
// replies are logged as well. Successful requests are not: resource-filter
// alone can run thousands of times a minute.
//
// It must run outside the panic recovery, so a recovered panic is logged too.
func requestLogMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		status := c.Writer.Status()
		path := c.Request.URL.Path
		if status < http.StatusBadRequest ||
			(status < http.StatusInternalServerError && !strings.HasPrefix(path, authzPathPrefix)) {
			return
		}
		attrs := []any{
			"method", c.Request.Method,
			"path", path,
			"route", c.FullPath(),
			"status", status,
			"duration", time.Since(start),
			"request_id", c.Request.Header.Get("x-request-id"),
			"client_ip", c.ClientIP(),
		}
		if len(c.Errors) > 0 {
			attrs = append(attrs, "err", strings.Join(c.Errors.Errors(), "; "))
		}
		ctx := c.Request.Context()
		level := slog.LevelWarn
		// A request the caller cancelled or timed out is the caller's outcome,
		// not a bkn-safe fault, so it stays at warning.
		if status >= http.StatusInternalServerError && ctx.Err() == nil {
			level = slog.LevelError
		}
		if ctx.Err() != nil {
			attrs = append(attrs, "request_ctx", ctx.Err().Error())
		}
		slog.Log(ctx, level, "safe request failed", attrs...)
	}
}
