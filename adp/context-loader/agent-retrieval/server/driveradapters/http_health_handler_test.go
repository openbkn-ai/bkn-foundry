package driveradapters

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/bkntrace"
)

type healthRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn healthRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestReadinessIncludesLifecycleCore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name   string
		client *bkntrace.LifecycleClient
		status int
	}{
		{
			name:   "unconfigured",
			client: bkntrace.NewLifecycleClient("", nil),
			status: http.StatusServiceUnavailable,
		},
		{
			name: "reachable",
			client: bkntrace.NewLifecycleClient("http://core.test", &http.Client{
				Transport: healthRoundTripFunc(func(*http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     make(http.Header),
						Body:       io.NopCloser(strings.NewReader(`{"status":"ready"}`)),
					}, nil
				}),
			}),
			status: http.StatusOK,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router := gin.New()
			handler := newHTTPHealthHandler(test.client)
			handler.RegisterRouter(router.Group("/health"))
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/health/ready", http.NoBody)
			request = request.WithContext(context.Background())
			router.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("readiness status = %d, want %d: %s",
					response.Code, test.status, response.Body.String())
			}
		})
	}
}
