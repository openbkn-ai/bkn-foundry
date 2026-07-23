package driveradapters

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	infraCommon "github.com/openbkn-ai/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/smartystreets/goconvey/convey"
)

func TestMiddlewareTraceContext(t *testing.T) {
	convey.Convey("middlewareTraceContext keeps a valid request id and strips forbidden baggage", t, func() {
		gin.SetMode(gin.TestMode)
		router := gin.New()
		router.Use(middlewareTraceContext)
		router.GET("/trace", func(c *gin.Context) {
			traceCtx, ok := infraCommon.GetTraceContextFromCtx(c.Request.Context())
			convey.So(ok, convey.ShouldBeTrue)
			convey.So(traceCtx.RequestID, convey.ShouldEqual, "req_01JZVALIDREQUESTID000000214")
			convey.So(traceCtx.Baggage, convey.ShouldResemble, map[string]string{
				"bkn.account.type": "service",
				"bkn.runtime.env":  "test",
			})
			convey.So(c.Request.Header.Get(infraCommon.HeaderBKNRequestID), convey.ShouldEqual, "req_01JZVALIDREQUESTID000000214")
			c.Status(http.StatusNoContent)
		})

		req := httptest.NewRequest(http.MethodGet, "/trace", http.NoBody)
		req.Header.Set(infraCommon.HeaderBKNRequestID, "req_01JZVALIDREQUESTID000000214")
		req.Header.Set(infraCommon.HeaderBaggage, "bkn.account.type=service,bkn.actor.id=user-1,bkn.runtime.env=test")
		resp := httptest.NewRecorder()

		router.ServeHTTP(resp, req)

		convey.So(resp.Code, convey.ShouldEqual, http.StatusNoContent)
	})

	convey.Convey("middlewareTraceContext generates a request id when the inbound one is invalid", t, func() {
		gin.SetMode(gin.TestMode)
		router := gin.New()
		router.Use(middlewareTraceContext)
		router.GET("/trace", func(c *gin.Context) {
			traceCtx, ok := infraCommon.GetTraceContextFromCtx(c.Request.Context())
			convey.So(ok, convey.ShouldBeTrue)
			convey.So(infraCommon.IsValidBKNRequestID(traceCtx.RequestID), convey.ShouldBeTrue)
			convey.So(c.Request.Header.Get(infraCommon.HeaderLegacyRequestID), convey.ShouldEqual, traceCtx.RequestID)
			c.Status(http.StatusNoContent)
		})

		req := httptest.NewRequest(http.MethodGet, "/trace", http.NoBody)
		req.Header.Set(infraCommon.HeaderBKNRequestID, "bad id")
		resp := httptest.NewRecorder()

		router.ServeHTTP(resp, req)

		convey.So(resp.Code, convey.ShouldEqual, http.StatusNoContent)
	})
}

func TestSafeRequestBodySummary(t *testing.T) {
	convey.Convey("safeRequestBodySummary records hash and length without raw body", t, func() {
		summary := safeRequestBodySummary("application/json", []byte(`{"token":"secret","action":"execute"}`))

		convey.So(summary["content_type"], convey.ShouldEqual, "application/json")
		convey.So(summary["length"], convey.ShouldEqual, 37)
		hash, ok := summary["hash"].(string)
		convey.So(ok, convey.ShouldBeTrue)
		convey.So(strings.HasPrefix(hash, "sha256:"), convey.ShouldBeTrue)
		convey.So(hash, convey.ShouldNotContainSubstring, "secret")
		convey.So(hash, convey.ShouldNotContainSubstring, "execute")
	})
}
