// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// A deployment with no licence service at all — a community install, or an
// enterprise one whose licence hub failed to start — must still answer with a
// complete, unambiguous shape.
//
// The regression this guards against is an empty edition. It used to be the
// zero value, which left every frontend to decide for itself whether "" meant
// community, and they would not all have decided the same way.
func TestCapabilitiesWithoutLicenceReportsCommunity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	registerCapabilities(r.Group("/api/safe/v1"), nil)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/safe/v1/capabilities", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", w.Code)
	}

	var resp capabilitiesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body %s)", err, w.Body.String())
	}
	if resp.Licensed {
		t.Error("licensed: got true, want false — no licence service is present")
	}
	if resp.Edition != "community" {
		t.Errorf("edition: got %q, want \"community\" — a deployment without a valid certificate behaves as community", resp.Edition)
	}
	if resp.State != "unlicensed" {
		t.Errorf("state: got %q, want \"unlicensed\"", resp.State)
	}

	// Collections must serialise as [] and {}, never null: a frontend that does
	// resp.capabilities.includes(...) breaks on null, and the difference is
	// invisible until an unlicensed deployment hits it.
	body := w.Body.String()
	for _, want := range []string{`"features":[]`, `"capabilities":[]`, `"limits":{}`, `"extensions":[]`} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %s: %s", want, body)
		}
	}
}
