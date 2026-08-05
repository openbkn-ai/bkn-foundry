// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/licverify"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/license"
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
	// It is derived from the same predicate the gate uses (State ∈ {valid,
	// grace}, see license.Service.FeatureEnabled), not from the presence of a
	// payload: an expired certificate still has one, and reporting that as
	// licensed would put the frontend back in exactly the state this field was
	// added to prevent — a full menu where every call refuses.
	Licensed bool `json:"licensed"`
	// Edition is the tier in force, and "community" whenever Licensed is false:
	// a deployment without a valid certificate behaves as community, and there
	// is deliberately no separate "unlicensed" tier. State carries the detail
	// (valid, grace, fallback_community, trial, unlicensed, invalid) for the
	// activation banner.
	Edition string `json:"edition"`
	State   string `json:"state"`
	// Features is what the license carries — including enterprise features
	// this binary cannot serve. Kept separate from Capabilities so support can
	// tell "not licensed" apart from "licensed, wrong image".
	Features []string `json:"features"`
	// Capabilities is what this binary can actually do right now: a licensed
	// mode ① feature, or a mode ② feature whose ee implementation is plugged
	// in. This is the list the frontend should drive off.
	Capabilities []string `json:"capabilities"`
	// Limits are the numeric caps the license carries.
	Limits map[string]int64 `json:"limits"`
	// Extensions lists the enterprise sockets filled in this build. Empty in
	// every community binary — the code is not there to fill them.
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
			State:        string(licenseStateOrUnlicensed(svc)),
			Features:     []string{},
			Capabilities: []string{},
			Limits:       map[string]int64{},
			Extensions:   extension.Registered(),
		}

		// Same predicate as the gate: a licence is in force only while the
		// certificate verifies and has not run past its grace window. Features
		// and limits are read from the payload regardless — support needs to see
		// what the installed certificate carries even when it no longer grants
		// anything, and Licensed already says it grants nothing.
		if svc != nil {
			snap := svc.State()
			inForce := snap.State == licverify.StateValid || snap.State == licverify.StateGrace
			if snap.Payload != nil {
				if inForce {
					resp.Licensed = true
					resp.Edition = string(snap.Payload.Edition)
				}
				if snap.Payload.Features != nil {
					resp.Features = snap.Payload.Features
				}
				if snap.Payload.Limits != nil {
					resp.Limits = snap.Payload.Limits
				}
			}
		}

		// A feature is usable when the license carries it AND, for enterprise
		// features, the ee code is present. Derived per request rather than
		// cached, so a lapsed or hot-reloaded license shows up immediately.
		for _, f := range resp.Features {
			if extension.Usable(extension.Feature(f)) {
				resp.Capabilities = append(resp.Capabilities, f)
			}
		}

		c.JSON(http.StatusOK, resp)
	})
}

// licenseStateOrUnlicensed reports the license state, treating a disabled
// license hub as unlicensed — which is the truth from a capability standpoint
// and keeps the response shape stable.
func licenseStateOrUnlicensed(svc *license.Service) string {
	if svc == nil {
		return "unlicensed"
	}
	return string(svc.State().State)
}
