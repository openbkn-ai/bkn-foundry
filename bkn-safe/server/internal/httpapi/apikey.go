package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"bkn-safe/internal/auth"
	"bkn-safe/internal/model"
)

// defaultKeyTTL is the validity granted when an issuer specifies neither an
// explicit expires_at nor never_expire. Long-lived by design (the whole point of
// an AppKey), but not unbounded — never-expire must be opted into explicitly.
const defaultKeyTTL = 365 * 24 * time.Hour

// registerMeAPIKeys mounts self-service AppKey management under /api/safe/v1/me/
// api-keys (RequireUser): the caller issues/lists/revokes keys for ITSELF. The
// owner is the verified token subject — never taken from the request body — so a
// key can never be minted for, or read across, another identity.
func registerMeAPIKeys(g *gin.RouterGroup, keys *auth.APIKeyStore) {
	// POST /me/api-keys — issue a key. { name, expires_at?, never_expire? }
	// expires_at: RFC3339; omitted -> default 1y; never_expire:true -> no expiry.
	// Returns the plaintext key ONCE: { id, key_id, name, key, expires_at, created_at }.
	g.POST("/api-keys", func(c *gin.Context) {
		owner := c.GetString(ctxAccessorID)
		var req struct {
			Name        string  `json:"name" binding:"required"`
			ExpiresAt   *string `json:"expires_at"`
			NeverExpire bool    `json:"never_expire"`
		}
		if !bind(c, &req) {
			return
		}
		exp, err := resolveExpiry(req.ExpiresAt, req.NeverExpire)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		plaintext, rec, err := keys.Issue(c.Request.Context(), owner, req.Name, exp)
		if err != nil {
			serverError(c, err)
			return
		}
		body := apiKeyJSON(*rec, false)
		body["key"] = plaintext // shown exactly once
		c.JSON(http.StatusCreated, body)
	})

	// GET /me/api-keys — list the caller's keys (no secret). -> { keys:[...] }
	g.GET("/api-keys", func(c *gin.Context) {
		owner := c.GetString(ctxAccessorID)
		list, err := keys.ListByOwner(c.Request.Context(), owner)
		if err != nil {
			serverError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"keys": apiKeysJSON(list, false)})
	})

	// DELETE /me/api-keys/:id — revoke one of the caller's own keys.
	g.DELETE("/api-keys/:id", func(c *gin.Context) {
		owner := c.GetString(ctxAccessorID)
		err := keys.DeleteOwned(c.Request.Context(), owner, c.Param("id"))
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "api key not found"})
			return
		}
		if err != nil {
			serverError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})

	// POST /me/api-keys/:id/regenerate — rotate the secret of the caller's own
	// key (lost it / suspected leak). Old plaintext stops working immediately;
	// the new plaintext is returned ONCE. Same id/name/expiry.
	g.POST("/api-keys/:id/regenerate", func(c *gin.Context) {
		owner := c.GetString(ctxAccessorID)
		plaintext, rec, err := keys.Regenerate(c.Request.Context(), owner, c.Param("id"))
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "api key not found"})
			return
		}
		if err != nil {
			serverError(c, err)
			return
		}
		body := apiKeyJSON(*rec, false)
		body["key"] = plaintext // shown exactly once
		c.JSON(http.StatusOK, body)
	})
}

// registerAdminAPIKeys mounts global AppKey oversight under /api/safe/v1/admin/
// api-keys (RequireAdmin): list/revoke ANY user's keys for audit and incident
// response.
func registerAdminAPIKeys(g *gin.RouterGroup, keys *auth.APIKeyStore) {
	// GET /admin/api-keys?owner_id= — list all keys (optionally one owner's).
	g.GET("/api-keys", func(c *gin.Context) {
		list, err := keys.ListAll(c.Request.Context(), c.Query("owner_id"))
		if err != nil {
			serverError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"keys": apiKeysJSON(list, true)})
	})

	// DELETE /admin/api-keys/:id — revoke any key by id.
	g.DELETE("/api-keys/:id", func(c *gin.Context) {
		err := keys.Delete(c.Request.Context(), c.Param("id"))
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "api key not found"})
			return
		}
		if err != nil {
			serverError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})
}

// registerAPIKeyVerify mounts the internal AppKey verification endpoint —
// tokenless, ClusterIP-internal (same trust face as /authz and /directory). The
// MCP/REST gateway calls it to resolve an AppKey to its owner identity. Response
// mirrors OAuth2 introspection: 200 { active:false } on any failure (no leak of
// why), 200 { active:true, sub, account_type, key_id } on success.
func registerAPIKeyVerify(r gin.IRoutes, keys *auth.APIKeyStore) {
	r.POST("/api/safe/v1/api-keys/introspect", func(c *gin.Context) {
		var req struct {
			Token string `json:"token" binding:"required"`
		}
		if !bind(c, &req) {
			return
		}
		v, err := keys.Verify(c.Request.Context(), req.Token)
		if err != nil {
			// Includes ErrAPIKeyInvalid (expected) and DB errors; both => not active.
			c.JSON(http.StatusOK, gin.H{"active": false})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"active":       true,
			"sub":          v.OwnerID,
			"account_type": v.AccountType,
			"key_id":       v.KeyID,
		})
	})
}

// resolveExpiry turns the issue request's expiry inputs into a concrete pointer:
// never_expire -> nil; explicit RFC3339 expires_at -> that instant (must be in
// the future); neither -> now + defaultKeyTTL.
func resolveExpiry(expiresAt *string, neverExpire bool) (*time.Time, error) {
	if neverExpire {
		return nil, nil
	}
	if expiresAt != nil && *expiresAt != "" {
		t, err := time.Parse(time.RFC3339, *expiresAt)
		if err != nil {
			return nil, errors.New("expires_at must be RFC3339")
		}
		if !t.After(time.Now()) {
			return nil, errors.New("expires_at must be in the future")
		}
		return &t, nil
	}
	t := time.Now().Add(defaultKeyTTL)
	return &t, nil
}

// apiKeyJSON renders a key for API responses WITHOUT any secret. includeOwner
// adds owner_user_id (admin views).
func apiKeyJSON(k model.APIKey, includeOwner bool) gin.H {
	h := gin.H{
		"id":           k.ID,
		"key_id":       k.KeyID,
		"name":         k.Name,
		"enabled":      k.Enabled,
		"expires_at":   k.ExpiresAt,  // null = never expires
		"last_used_at": k.LastUsedAt, // null = never used
		"created_at":   k.CreatedAt,
	}
	if includeOwner {
		h["owner_user_id"] = k.OwnerUserID
	}
	return h
}

func apiKeysJSON(list []model.APIKey, includeOwner bool) []gin.H {
	out := make([]gin.H, 0, len(list))
	for _, k := range list {
		out = append(out, apiKeyJSON(k, includeOwner))
	}
	return out
}
