package rest

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestDefaultClientRejectsUntrustedTLSBeforeSendingRequest(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	client := NewRawHTTPClientWithOptions(HTTPClientOptions{TimeOut: 5})
	defer client.CloseIdleConnections()
	resp, err := client.Get(upstream.URL)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err == nil || calls.Load() != 0 {
		t.Fatalf("untrusted endpoint received a request: error=%v, calls=%d", err, calls.Load())
	}
}
