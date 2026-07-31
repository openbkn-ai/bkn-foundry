package boot

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCoreReadinessRequiresNoBusinessContext(t *testing.T) {
	response := httptest.NewRecorder()
	coreReady(response, httptest.NewRequest(http.MethodGet, "/health/ready", http.NoBody))
	if response.Code != http.StatusOK || response.Body.String() != `{"status":"ready"}` {
		t.Fatalf("unexpected Core readiness response: %d %s", response.Code, response.Body.String())
	}
}
