package telemetry

import (
	"net/http"
	"testing"

	"github.com/smartystreets/goconvey/convey"
)

func TestSanitizeHeadersForSpan(t *testing.T) {
	convey.Convey("sanitizeHeadersForSpan redacts sensitive headers", t, func() {
		headers := http.Header{}
		headers.Set("Authorization", "Bearer token-secret")
		headers.Set("Cookie", "session=secret")
		headers.Set("X-Authorization", "Bearer x-secret")
		headers.Set("Bkn-Request-Id", "req_01JZVALIDREQUESTID000000004")

		sanitized := sanitizeHeadersForSpan(headers)

		convey.So(sanitized, convey.ShouldContainSubstring, "req_01JZVALIDREQUESTID000000004")
		convey.So(sanitized, convey.ShouldContainSubstring, "[redacted]")
		convey.So(sanitized, convey.ShouldNotContainSubstring, "token-secret")
		convey.So(sanitized, convey.ShouldNotContainSubstring, "session=secret")
		convey.So(sanitized, convey.ShouldNotContainSubstring, "x-secret")
	})
}

func TestRequestBodyPolicyForSpan(t *testing.T) {
	convey.Convey("requestBodyPolicyForSpan never returns raw request body content", t, func() {
		value := requestBodyPolicyForSpan(128)

		convey.So(value, convey.ShouldContainSubstring, "redacted")
		convey.So(value, convey.ShouldNotContainSubstring, "select * from")
		convey.So(value, convey.ShouldNotContainSubstring, "prompt")
	})
}
