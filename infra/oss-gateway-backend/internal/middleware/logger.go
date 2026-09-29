package middleware

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

func Logger(log *logrus.Entry) gin.HandlerFunc {
	return func(c *gin.Context) {
		startTime := time.Now()
		parent := otel.GetTextMapPropagator().Extract(
			c.Request.Context(), propagation.HeaderCarrier(c.Request.Header),
		)
		ctx, span := otel.Tracer("oss-gateway/http").Start(
			parent, "HTTP request", trace.WithSpanKind(trace.SpanKindServer),
		)
		c.Request = c.Request.WithContext(ctx)
		defer func() {
			latency := time.Since(startTime)
			statusCode := c.Writer.Status()
			clientIP := c.ClientIP()
			method := c.Request.Method
			path := c.Request.URL.Path

			entry := log.WithFields(logrus.Fields{
				"status_code": statusCode,
				"latency":     latency,
				"client_ip":   clientIP,
				"method":      method,
				"path":        path,
			})
			if privateErrors := c.Errors.ByType(gin.ErrorTypePrivate); len(privateErrors) > 0 {
				entry = entry.WithField("errors", privateErrors.Errors())
			}

			if statusCode >= 500 {
				entry.Error("Server error")
			} else if statusCode >= 400 {
				entry.Warn("Client error")
			} else {
				entry.Info("Request completed")
			}
			route := c.FullPath()
			if route == "" {
				route = "unmatched"
			}
			span.SetName(method + " " + route)
			span.SetAttributes(operationSpanAttributes(method, route, statusCode)...)
			if statusCode >= http.StatusInternalServerError {
				span.SetStatus(codes.Error, http.StatusText(statusCode))
			}
			span.End()
		}()

		c.Next()
	}
}

func operationSpanAttributes(method, route string, status int) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("http.request.method", method),
		attribute.String("http.route", route),
		attribute.Int("http.response.status_code", status),
	}
}
