package middleware

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
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

		c.Next()

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
		otellog.LogInfo(c.Request.Context(), "http.request.completed",
			operationLogAttributes(method, route, statusCode)...)
		span.End()
	}
}

func operationSpanAttributes(method, route string, status int) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("http.request.method", method),
		attribute.String("http.route", route),
		attribute.Int("http.response.status_code", status),
	}
}

func operationLogAttributes(method, route string, status int) []attribute.KeyValue {
	sourceLogID := uuid.NewString()
	outcome := "success"
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		outcome = "denied"
	} else if status >= http.StatusBadRequest {
		outcome = "failure"
	}
	return []attribute.KeyValue{
		attribute.String("schema_version", "1.0.0"),
		attribute.String("log_id", sourceLogID),
		attribute.String("source_log_id", sourceLogID),
		attribute.String("source_id", "oss-gateway"),
		attribute.String("log_category", "runtime.system"),
		attribute.String("event_name", "http.request.completed"),
		attribute.String("outcome", outcome),
		attribute.String("safe_summary", fmt.Sprintf("%s %s completed with HTTP %d", method, route, status)),
		attribute.String("http.request.method", method),
		attribute.String("http.route", route),
		attribute.Int("http.response.status_code", status),
	}
}
