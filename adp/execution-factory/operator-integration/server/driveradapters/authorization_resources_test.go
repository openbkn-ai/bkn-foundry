package driveradapters

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestAuthorizationResourcePage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name       string
		url        string
		wantOffset int
		wantLimit  int
		wantErr    bool
	}{
		{name: "defaults", url: "/", wantOffset: 0, wantLimit: 20},
		{name: "explicit page", url: "/?offset=20&limit=50", wantOffset: 20, wantLimit: 50},
		{name: "negative offset", url: "/?offset=-1", wantErr: true},
		{name: "limit exceeds maximum", url: "/?limit=101", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest("GET", test.url, nil)
			offset, limit, err := authorizationResourcePage(ctx)
			if (err != nil) != test.wantErr {
				t.Fatalf("error = %v, want error %v", err, test.wantErr)
			}
			if !test.wantErr && (offset != test.wantOffset || limit != test.wantLimit) {
				t.Fatalf("page = (%d, %d), want (%d, %d)", offset, limit, test.wantOffset, test.wantLimit)
			}
		})
	}
}
