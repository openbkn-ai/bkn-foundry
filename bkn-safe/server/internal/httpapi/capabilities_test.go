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

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/adminwrite"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/permobject"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
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
	// A community binary: no gate installed and nothing assembled. The reset is
	// not hygiene — the assembly table is process-global by design, and without
	// it this test inherits whatever an earlier test in this package registered.
	entitlement.ResetForTest()
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

// The three deployments a customer can be in, and the two fields that have to
// tell them apart. This is the whole reason capabilities and extensions are
// separate lists.
//
// The endpoint derives both from the assembly table, never from the
// certificate's features[] (ee-design.md §3.2): a feature key in a certificate
// says nothing about whether this image carries the code for it. The old
// derivation asked "does the licence carry this key", which reported
// rbac_basic as usable on a community binary that has no write routes at all —
// a menu entry that 404s.
func TestCapabilitiesSeparatesWhatIsInstalledFromWhatIsUnlocked(t *testing.T) {
	// What an enterprise image assembles at startup. The sockets' own tests
	// cover that registering puts these here; this test starts from the table.
	enterpriseBuild := func() {
		entitlement.MarkAssembled(adminwrite.Capability, licverify.EditionProfessional)
		entitlement.MarkAssembled(permobject.Capability, licverify.EditionEnterprise)
	}

	for _, tc := range []struct {
		name             string
		assemble         func()
		edition          licverify.Edition
		licensed         bool
		wantExtensions   []string
		wantCapabilities []string
	}{
		{
			name: "社区镜像", assemble: func() {}, edition: licverify.EditionCommunity,
			wantExtensions: []string{}, wantCapabilities: []string{},
		},
		{
			// The distinction support needs: right image, wrong certificate.
			name: "企业镜像无证", assemble: enterpriseBuild, edition: licverify.EditionCommunity,
			wantExtensions:   []string{adminwrite.Capability, permobject.Capability},
			wantCapabilities: []string{},
		},
		{
			name: "企业镜像专业证", assemble: enterpriseBuild, edition: licverify.EditionProfessional, licensed: true,
			wantExtensions:   []string{adminwrite.Capability, permobject.Capability},
			wantCapabilities: []string{adminwrite.Capability},
		},
		{
			name: "企业镜像企业证", assemble: enterpriseBuild, edition: licverify.EditionEnterprise, licensed: true,
			wantExtensions:   []string{adminwrite.Capability, permobject.Capability},
			wantCapabilities: []string{adminwrite.Capability, permobject.Capability},
		},
		{
			// Industry inherits everything enterprise has. An == comparison
			// would take capability away from the customer paying the most
			// (ee-design.md §3.3).
			name: "企业镜像行业证", assemble: enterpriseBuild, edition: licverify.EditionIndustry, licensed: true,
			wantExtensions:   []string{adminwrite.Capability, permobject.Capability},
			wantCapabilities: []string{adminwrite.Capability, permobject.Capability},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			entitlement.SetGateForTest(entitlement.GateFunc(func() entitlement.Snapshot {
				return entitlement.Snapshot{Licensed: tc.licensed, Edition: tc.edition}
			}))
			tc.assemble()

			r := gin.New()
			registerCapabilities(r.Group("/api/safe/v1"), nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/safe/v1/capabilities", nil))

			var resp capabilitiesResponse
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode: %v (body %s)", err, w.Body.String())
			}
			if !sameSet(resp.Extensions, tc.wantExtensions) {
				t.Errorf("extensions = %v, want %v —— 这个字段答的是「这个镜像装了什么」，与证书无关",
					resp.Extensions, tc.wantExtensions)
			}
			if !sameSet(resp.Capabilities, tc.wantCapabilities) {
				t.Errorf("capabilities = %v, want %v —— 这个字段答的是「现在真能用什么」",
					resp.Capabilities, tc.wantCapabilities)
			}
			if resp.Edition != string(tc.edition) {
				t.Errorf("edition = %q, want %q", resp.Edition, tc.edition)
			}
		})
	}
}

func sameSet(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := map[string]bool{}
	for _, g := range got {
		seen[g] = true
	}
	for _, w := range want {
		if !seen[w] {
			return false
		}
	}
	return true
}

// The licence hub is running but nothing has been imported yet — the shape the
// VM reproduced, and a different branch from the nil-service test above: there
// the gate is absent entirely, here it is installed and reporting a hub with no
// certificate in it.
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
