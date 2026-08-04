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
	"time"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/licverify"
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

// The licence hub is running but nothing has been imported yet — the shape the
// VM reproduced, and the branch the nil-service test above skips entirely
// (licenseStateOrUnlicensed returns early on nil).
func TestCapabilitiesWithHubButNoLicenceReportsCommunity(t *testing.T) {
	r, _, _ := newLicenseServer(t)

	w := adminReq(t, r, http.MethodGet, "/api/safe/v1/capabilities", nil)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (body %s)", w.Code, w.Body.String())
	}
	var resp capabilitiesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Licensed || resp.Edition != "community" {
		t.Errorf("hub up, no certificate: got licensed=%v edition=%q, want false/community",
			resp.Licensed, resp.Edition)
	}
}

// An expired certificate past its grace window keeps its payload, so a check
// written as "payload != nil" reports the deployment as licensed while every
// gated call refuses — a full menu where nothing works, which is the state this
// field exists to prevent. The predicate must be the gate's own: State ∈
// {valid, grace}.
func TestCapabilitiesPastGraceIsNotLicensed(t *testing.T) {
	r, _, priv := newLicenseServer(t)

	// Expired well beyond licverify.GracePeriod, so Eval lands on
	// fallback_community with the payload still attached.
	past := time.Now().Add(-2 * licverify.GracePeriod).Unix()
	lic := signTestLic(t, priv, func(p map[string]any) {
		p["issued_at"] = past - 86400
		p["expires_at"] = past
		p["contract_expires_at"] = past
	})
	adminReq(t, r, http.MethodPost, "/api/safe/v1/admin/license/import",
		map[string]string{"license": lic})

	w := adminReq(t, r, http.MethodGet, "/api/safe/v1/capabilities", nil)

	var resp capabilitiesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body %s)", err, w.Body.String())
	}
	if resp.Licensed {
		t.Error("licensed: got true past the grace window — the gate refuses everything in this state")
	}
	if resp.Edition != "community" {
		t.Errorf("edition: got %q, want \"community\" — an expired certificate grants nothing", resp.Edition)
	}
	// Features stay visible on purpose: support needs to see what the installed
	// certificate carries even when it no longer grants anything.
	if len(resp.Features) == 0 {
		t.Error("features: emptied — the installed certificate still lists them, and support reads this")
	}
}
