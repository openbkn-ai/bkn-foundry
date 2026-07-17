// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/licverify"

	"bkn-safe/internal/auth"
	"bkn-safe/internal/authz"
	"bkn-safe/internal/license"
)

// registerLicenseAdmin mounts the license management surface on the admin
// group (RequireAdmin + audit middleware are inherited from the group).
// bkn-safe is the cluster's license hub — import/activation/removal happen
// here and nowhere else; modules only ever read.
func registerLicenseAdmin(g *gin.RouterGroup, svc *license.Service, e *authz.Enforcer) {
	// POST /license/import — verify and store a .lic; on online deployments an
	// unbound license is auto-activated. { license } -> license detail.
	// 409 with stored:true = stored fine but the issuer refused activation.
	g.POST("/license/import", RequirePermission(e, "admin-license", "manage"), func(c *gin.Context) {
		importLicense(c, svc)
	})

	// POST /license/receipt — import the offline activation receipt (a signed,
	// fingerprint-bound .lic from the customer portal). Same verification as
	// import; a separate route so the UI flow and the audit trail read right.
	g.POST("/license/receipt", RequirePermission(e, "admin-license", "manage"), func(c *gin.Context) {
		importLicense(c, svc)
	})

	// GET /license — current license detail (weak judgement; modules gate by
	// verifying the signature themselves).
	g.GET("/license", RequirePermission(e, "admin-license", "view"), func(c *gin.Context) {
		c.JSON(http.StatusOK, licenseDetail(svc))
	})

	// POST /license/activate — report the installed license to the issuer and
	// store the reissued, fingerprint-bound text.
	g.POST("/license/activate", RequirePermission(e, "admin-license", "manage"), func(c *gin.Context) {
		snap, err := svc.Activate(c.Request.Context())
		if err != nil {
			status := http.StatusBadGateway
			switch {
			case errors.Is(err, license.ErrOfflineDeployment), errors.Is(err, license.ErrNoLicense):
				status = http.StatusBadRequest
			case errors.Is(err, license.ErrActivatedElsewhere):
				status = http.StatusConflict
			}
			c.JSON(status, gin.H{"error": err.Error()})
			return
		}
		_ = snap
		c.JSON(http.StatusOK, licenseDetail(svc))
	})

	// GET /license/fingerprint — this cluster's machine code. Works with no
	// license installed: the activation guide shows it for portal registration,
	// and admins quote it for unbind support.
	g.GET("/license/fingerprint", RequirePermission(e, "admin-license", "view"), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"instance_fp": svc.Fingerprint()})
	})

	// GET /license/activation-code — the offline activation request code the
	// customer pastes into the license portal (shown next to the fingerprint,
	// both copyable).
	g.GET("/license/activation-code", RequirePermission(e, "admin-license", "view"), func(c *gin.Context) {
		fp, code, licID := svc.ActivationCode()
		c.JSON(http.StatusOK, gin.H{"instance_fp": fp, "activation_code": code, "lic_id": licID})
	})

	// DELETE /license — drop the installed license (back to unactivated).
	g.DELETE("/license", RequirePermission(e, "admin-license", "manage"), func(c *gin.Context) {
		if err := svc.Remove(c.Request.Context()); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.Status(http.StatusNoContent)
	})
}

func importLicense(c *gin.Context, svc *license.Service) {
	var req struct {
		License string `json:"license" binding:"required"`
	}
	if !bind(c, &req) {
		return
	}
	_, actErr, err := svc.Import(c.Request.Context(), req.License)
	if err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, license.ErrBadLicense):
			status = http.StatusBadRequest
		case errors.Is(err, license.ErrBoundElsewhere):
			status = http.StatusConflict
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	if actErr != nil {
		// Stored, but the issuer refused/failed activation — the admin should
		// see both facts, not lose the import.
		status := http.StatusBadGateway
		if errors.Is(actErr, license.ErrActivatedElsewhere) {
			status = http.StatusConflict
		}
		c.JSON(status, gin.H{"stored": true, "error": actErr.Error(), "license": licenseDetail(svc)})
		return
	}
	c.JSON(http.StatusOK, licenseDetail(svc))
}

// registerLicenseInternal mounts the in-cluster distribution surface. It is
// NOT anonymous (upstream hard rule): callers authenticate with an AppKey —
// the existing service-identity mechanism. What is distributed is the signed
// license text; modules verify it locally and never trust these answers for
// gating.
func registerLicenseInternal(r *gin.Engine, svc *license.Service, keysStore *auth.APIKeyStore) {
	g := r.Group("/api/safe/v1/internal/license", RequireAppKey(keysStore))

	// GET /current — the raw .lic + ETag. Modules poll with If-None-Match
	// (≤5 min upstream contract); 304 keeps steady-state traffic body-free.
	g.GET("/current", func(c *gin.Context) {
		text, etag, err := svc.Current()
		if err != nil {
			if errors.Is(err, license.ErrNoLicense) {
				c.JSON(http.StatusNotFound, gin.H{"error": "no license installed"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		quoted := `"` + etag + `"`
		if match := c.GetHeader("If-None-Match"); match != "" && strings.Contains(match, etag) {
			c.Header("ETag", quoted)
			c.Status(http.StatusNotModified)
			return
		}
		c.Header("ETag", quoted)
		c.JSON(http.StatusOK, gin.H{"license": text, "etag": etag})
	})

	// GET /status — bkn-safe's pre-computed judgement (state + validity times).
	// Weak: for dashboards and ops, never for gating.
	g.GET("/status", func(c *gin.Context) {
		c.JSON(http.StatusOK, licenseStatus(svc))
	})

	// GET /capabilities — the features/limits the license carries, plus the
	// state needed to interpret them (fallback = community set applies). The
	// frontend shows/hides entries by this; enforcement stays server-side.
	g.GET("/capabilities", func(c *gin.Context) {
		snap := svc.State()
		resp := gin.H{
			"state":    snap.State,
			"features": []string{},
			"limits":   map[string]int64{},
		}
		if snap.Payload != nil {
			if snap.Payload.Features != nil {
				resp["features"] = snap.Payload.Features
			}
			if snap.Payload.Limits != nil {
				resp["limits"] = snap.Payload.Limits
			}
			resp["edition"] = snap.Payload.Edition
		}
		c.JSON(http.StatusOK, resp)
	})
}

// RequireAppKey authenticates service callers by AppKey (Authorization:
// Bearer bak_...). Any active key passes — this fences the surface off from
// anonymous traffic, it is not a per-caller authorization scheme.
func RequireAppKey(keysStore *auth.APIKeyStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		tok := bearerToken(c)
		if tok == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing bearer AppKey"})
			return
		}
		v, err := keysStore.Verify(c.Request.Context(), tok)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid AppKey"})
			return
		}
		c.Set(ctxAccessorID, v.OwnerID)
		c.Next()
	}
}

// licenseStatus is the weak-judgement view shared by the internal status
// endpoint and the admin detail.
func licenseStatus(svc *license.Service) gin.H {
	snap := svc.State()
	h := gin.H{
		"state":     snap.State,
		"activated": svc.Activated(),
	}
	if snap.Err != nil {
		h["error"] = snap.Err.Error()
	}
	if snap.RenewErr != nil {
		h["renew_error"] = snap.RenewErr.Error()
	}
	if p := snap.Payload; p != nil {
		h["edition"] = p.Edition
		h["expires_at"] = p.ExpiresAt
		h["contract_expires_at"] = p.ContractExpiresAt
		if snap.State == licverify.StateGrace && p.ExpiresAt != 0 {
			graceEnd := time.Unix(p.ExpiresAt, 0).Add(licverify.GracePeriod)
			days := int(time.Until(graceEnd).Hours() / 24)
			if days < 0 {
				days = 0
			}
			h["grace_remaining_days"] = days
		}
	}
	return h
}

// licenseDetail is the admin view: status plus identity/customer/features and
// the instance fingerprint (the activation guide needs fingerprint + code
// visible side by side).
func licenseDetail(svc *license.Service) gin.H {
	h := licenseStatus(svc)
	h["instance_fp"] = svc.Fingerprint()
	if p := svc.State().Payload; p != nil {
		h["lic_id"] = p.LicID
		h["customer"] = p.Customer
		h["issued_at"] = p.IssuedAt
		h["features"] = p.Features
		h["limits"] = p.Limits
	}
	return h
}
