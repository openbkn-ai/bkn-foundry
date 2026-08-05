// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/license"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
)

// capabilitiesResponse is what the frontend reads to show or hide paid entry
// points. Enforcement is never here — every gated call site checks the license
// itself. This endpoint only spares users a menu full of things that would
// refuse them.
type capabilitiesResponse struct {
	// Licensed reports whether a valid licence is in force right now. False
	// covers "never installed", "expired past grace" (State fallback_community)
	// and "failed verification" alike, because the product behaves identically
	// in all three. It exists so a frontend does not have to enumerate State
	// values to work that out — one that does will get it wrong the day a new
	// State appears.
	//
	// It is read straight off the gate every gated call site consults, not
	// derived from the presence of a payload: an expired certificate still has
	// one, and reporting that as licensed would put the frontend back in
	// exactly the state this field was added to prevent — a full menu where
	// every call refuses.
	Licensed bool `json:"licensed"`
	// Edition is the tier in force, and "community" whenever Licensed is false:
	// a deployment without a valid certificate behaves as community, and there
	// is deliberately no separate "unlicensed" tier. State carries the detail
	// (valid, grace, fallback_community, trial, unlicensed, invalid) for the
	// activation banner.
	Edition string `json:"edition"`
	State   string `json:"state"`
	// Features is the raw feature list the certificate carries, for display and
	// audit reconciliation ONLY.
	//
	// NOTHING MAY GATE ON IT — not this service, not another service, and above
	// all not a frontend, where `features.includes('audit')` has no force at
	// all and reintroduces fine-grained licensing through the back door. Tiers
	// are what decide (ee-design.md §3.1/§3.2). To show or hide an entry point,
	// read Capabilities: it is the server's own answer to "what works right
	// now", already reconciled against the tier and against what this binary
	// actually contains.
	Features []string `json:"features"`
	// Capabilities is what this binary can actually do right now: assembled
	// into this build AND covered by the tier in force. This is the list the
	// frontend should drive off.
	Capabilities []string `json:"capabilities"`
	// Limits are the numeric caps the license carries.
	Limits map[string]int64 `json:"limits"`
	// Extensions lists the paid capabilities compiled into this build,
	// regardless of licence. Empty in every community binary — the code is not
	// there to register them.
	//
	// The pair matters when a customer reports a missing feature: something in
	// Extensions but not in Capabilities means "right image, wrong certificate";
	// missing from both means "wrong image". Collapsing the two into one list
	// loses exactly the distinction support needs.
	Extensions []string `json:"extensions"`
}

// registerCapabilities mounts GET /capabilities.
//
// The design calls this endpoint /api/capabilities; on bkn-safe it lands under
// the service's own prefix, and the gateway exposes it. Any authenticated user
// may read it: it describes the deployment, carries no per-user authorization,
// and the frontend needs it on every login to lay out the menu.
func registerCapabilities(g *gin.RouterGroup, svc *license.Service) {
	g.GET("/capabilities", func(c *gin.Context) {
		resp := capabilitiesResponse{
			// Defaults describe a deployment with no valid certificate, which is
			// what a community install and an unlicensed enterprise one both are.
			// Written here rather than left to the zero value: an empty edition
			// pushes the "does empty mean community?" question onto every caller,
			// and the callers are frontends that will each answer it differently.
			Licensed:     false,
			Edition:      "community",
			State:        "unlicensed",
			Features:     []string{},
			Capabilities: []string{},
			Limits:       map[string]int64{},
			Extensions:   []string{},
		}

		// What this build contains, and what the tier in force actually unlocks.
		// Derived from the assembly table rather than from the certificate's
		// features[]: the tier is the only authorisation input (ee-design.md
		// §3.1), and a feature key in a certificate says nothing about whether
		// this image carries the code for it. Computed per request, never
		// cached, so an imported or lapsed certificate shows up immediately.
		for _, cap := range entitlement.Assembled() {
			resp.Extensions = append(resp.Extensions, cap.Name)
			if entitlement.AtLeast(cap.MinEdition) {
				resp.Capabilities = append(resp.Capabilities, cap.Name)
			}
		}

		// Everything the deployment is JUDGED by comes from the gate, not from
		// the certificate: it is the same value every gated call site reads, so
		// this endpoint cannot describe a deployment that behaves differently.
		// The earlier version derived Licensed from "the payload exists", which
		// answered true for a certificate expired past its grace window while
		// every gated call refused — a full menu where nothing works, the exact
		// failure this field was added to prevent.
		//
		// Snap, not Licensed()+Current()+State(): three separate reads can
		// straddle a certificate import or a renewal, and the response would
		// then mix fields from two certificates — a state no deployment was
		// ever in, and one no bug report could be reproduced from.
		snap := entitlement.Snap()
		resp.Licensed = snap.Licensed
		resp.Edition = string(snap.Edition)
		if snap.State != "" {
			// Empty means no gate was installed at all: a community deployment
			// with no licence hub. The default above already says "unlicensed",
			// which is what that is.
			resp.State = string(snap.State)
		}

		// Everything DESCRIPTIVE comes from the installed certificate, in force
		// or not: support needs to see what it carries even when it grants
		// nothing, and Licensed already says that it grants nothing.
		//
		// These come from the hub's snapshot rather than the gate's because the
		// gate deliberately drops limits, and drops features once a certificate
		// lapses. Reading them straight from the payload is the point.
		if svc != nil {
			if lic := svc.State(); lic.Payload != nil {
				if lic.Payload.Features != nil {
					resp.Features = lic.Payload.Features
				}
				if lic.Payload.Limits != nil {
					resp.Limits = lic.Payload.Limits
				}
			}
		}

		c.JSON(http.StatusOK, resp)
	})
}
