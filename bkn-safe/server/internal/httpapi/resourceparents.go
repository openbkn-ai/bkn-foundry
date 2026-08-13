// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

// maxResourceParentBatch caps one PUT/DELETE. A synchroniser pushing a full
// snapshot chunks; the cap keeps a single request's transaction and memory
// bounded rather than scaling with a module's whole inventory.
const maxResourceParentBatch = 1000

// defaultResourceParentPageSize bounds the GET when the caller states no limit.
// The read exists for reconciliation (compare the owning module's inventory
// against what bkn-safe stored), which pages; an unbounded default would make
// the first honest reconciliation the largest response the service ever sends.
const defaultResourceParentPageSize = 500

// maxPreviewFlips caps the sample a dry run returns. The TOTAL is always exact,
// so a reviewer reads "500 shown of 4128" rather than a list that quietly
// implies it is the whole story — a truncated report that looks complete is
// worse than no report, because it invites approving a widening nobody saw.
const maxPreviewFlips = 500

// registerResourceParents mounts the instance-level hierarchy writes under
// /api/safe/v1/authz. bkn-safe cannot derive which catalog a table belongs to —
// it only ever saw opaque "type:id" object keys — so the owning module pushes
// the fact here and the enforce-time fallback reads it (#800).
//
// Same tokenless service face as the rest of /authz: the guard is not a
// credential but the shape check below, which refuses any pair the seeded
// catalog did not declare as a parent/child pair. That matters because the
// hierarchy is a privilege path: a caller free to claim "safe_admin:console
// hangs under a catalog I own" would inherit the admin console from a catalog
// grant. Declaring the hierarchy in the seed and the membership over HTTP keeps
// the shape trusted while the data stays owned by the module that knows it.
func registerResourceParents(g *gin.RouterGroup, e *authz.Enforcer, db *gorm.DB) {
	// PUT /resource-parents — upsert a batch of (resource -> parent) rows.
	//
	//	{ resource_type, parent_type, items:[{resource_id, parent_id}] }
	//
	// Idempotent: re-pushing an unchanged snapshot rewrites the same rows. A
	// resource whose parent changed is re-pointed by the same call, because the
	// row is keyed by the child, not by the pair.
	g.PUT("/resource-parents", func(c *gin.Context) {
		var req struct {
			ResourceType string `json:"resource_type" binding:"required"`
			ParentType   string `json:"parent_type" binding:"required"`
			Items        []struct {
				ResourceID string `json:"resource_id" binding:"required"`
				ParentID   string `json:"parent_id" binding:"required"`
			} `json:"items" binding:"required"`
		}
		if !bind(c, &req) {
			return
		}
		if err := checkDeclaredHierarchy(c.Request.Context(), db, req.ResourceType, req.ParentType); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if len(req.Items) > maxResourceParentBatch {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "items exceeds the per-request cap of " + strconv.Itoa(maxResourceParentBatch),
			})
			return
		}
		rows := make([]model.ResourceParent, 0, len(req.Items))
		for _, it := range req.Items {
			// A wildcard id would make EVERY instance of the type inherit from one
			// parent (or one instance inherit from every parent) — the hierarchy is
			// a membership fact about concrete instances only.
			if it.ResourceID == "*" || it.ParentID == "*" {
				c.JSON(http.StatusBadRequest, gin.H{"error": `resource_id and parent_id must be concrete ids (not "*")`})
				return
			}
			if req.ResourceType == req.ParentType && it.ResourceID == it.ParentID {
				c.JSON(http.StatusBadRequest, gin.H{"error": "a resource cannot be its own parent: " + it.ResourceID})
				return
			}
			rows = append(rows, model.ResourceParent{
				ResourceTypeID: req.ResourceType, ResourceID: it.ResourceID,
				ParentTypeID: req.ParentType, ParentID: it.ParentID,
			})
		}
		if len(rows) == 0 {
			c.Status(http.StatusNoContent)
			return
		}
		// dry_run answers "who would gain what" and writes nothing. Recording
		// ownership is the moment inheritance starts applying, and that is a
		// widening — whoever holds a grant on a catalog begins to reach the tables
		// inside it. An allow-only engine has nothing to stage, so the review has
		// to happen BEFORE the write; there is no flag afterwards to hold it back.
		if c.Query("dry_run") == "true" {
			links := make(map[string]string, len(rows))
			for _, r := range rows {
				links[r.ResourceID] = r.ParentID
			}
			flips, total, err := e.PreviewOwnership(req.ResourceType, req.ParentType, links, maxPreviewFlips)
			if err != nil {
				serverError(c, err)
				return
			}
			changes := make([]gin.H, 0, len(flips))
			for _, f := range flips {
				changes = append(changes, gin.H{
					"accessor_id": f.AccessorID,
					"resource_id": f.ResourceID,
					"operation":   f.Operation,
					// "grant" or "revoke": re-pointing a resource at another parent
					// takes access away from the old parent's holders, and a caller
					// that keys "safe to push" on the total must see those too.
					"direction": f.Direction,
				})
			}
			c.JSON(http.StatusOK, gin.H{
				"changes": changes, "total": total, "truncated": total > len(changes),
			})
			return
		}
		if err := db.WithContext(c.Request.Context()).Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "resource_type_id"}, {Name: "resource_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"parent_type_id", "parent_id", "updated_at"}),
		}).Create(&rows).Error; err != nil {
			serverError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})

	// DELETE /resource-parents — drop the rows for the named resources.
	// { resource_type, resource_ids:[...] }. Idempotent; unknown ids are a no-op.
	g.DELETE("/resource-parents", func(c *gin.Context) {
		var req struct {
			ResourceType string   `json:"resource_type" binding:"required"`
			ResourceIDs  []string `json:"resource_ids" binding:"required"`
		}
		if !bind(c, &req) {
			return
		}
		if len(req.ResourceIDs) > maxResourceParentBatch {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "resource_ids exceeds the per-request cap of " + strconv.Itoa(maxResourceParentBatch),
			})
			return
		}
		if len(req.ResourceIDs) == 0 {
			c.Status(http.StatusNoContent)
			return
		}
		if err := db.WithContext(c.Request.Context()).
			Where("resource_type_id = ? AND resource_id IN ?", req.ResourceType, req.ResourceIDs).
			Delete(&model.ResourceParent{}).Error; err != nil {
			serverError(c, err)
			return
		}
		c.Status(http.StatusNoContent)
	})

	// GET /resource-parents?resource_type=&parent_id=&limit=&offset= — read back
	// what was stored, so the owning module can reconcile its inventory against
	// it (rows for resources it no longer has are orphans to delete; resources
	// with no row are the gap that still judges the pre-#800 way).
	// -> { items:[{resource_id, parent_type, parent_id}], total }
	g.GET("/resource-parents", func(c *gin.Context) {
		rtype := c.Query("resource_type")
		if rtype == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "resource_type required"})
			return
		}
		q := db.WithContext(c.Request.Context()).Model(&model.ResourceParent{}).
			Where("resource_type_id = ?", rtype)
		if pid := c.Query("parent_id"); pid != "" {
			q = q.Where("parent_id = ?", pid)
		}
		if rid := c.Query("resource_id"); rid != "" {
			q = q.Where("resource_id = ?", rid)
		}
		var total int64
		if err := q.Count(&total).Error; err != nil {
			serverError(c, err)
			return
		}
		limit := defaultResourceParentPageSize
		if v := c.Query("limit"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n <= 0 || n > maxResourceParentBatch {
				c.JSON(http.StatusBadRequest, gin.H{
					"error": "limit must be an integer in 1.." + strconv.Itoa(maxResourceParentBatch),
				})
				return
			}
			limit = n
		}
		offset := 0
		if v := c.Query("offset"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "offset must be a non-negative integer"})
				return
			}
			offset = n
		}
		var rows []model.ResourceParent
		// Ordered so paging is stable across requests; the table has no natural
		// sequence and an unordered LIMIT/OFFSET can repeat or skip rows.
		if err := q.Order("resource_id").Limit(limit).Offset(offset).Find(&rows).Error; err != nil {
			serverError(c, err)
			return
		}
		items := make([]gin.H, 0, len(rows))
		for _, r := range rows {
			items = append(items, gin.H{
				"resource_id": r.ResourceID,
				"parent_type": r.ParentTypeID,
				"parent_id":   r.ParentID,
			})
		}
		c.JSON(http.StatusOK, gin.H{"items": items, "total": total})
	})
}

// checkDeclaredHierarchy refuses a (child type, parent type) pair the seeded
// catalog did not declare. It is the whole trust story of this tokenless write:
// membership rows are only as safe as the shape they may claim.
//
// A type the seed never registered is refused too, and that is on purpose rather
// than an oversight: vega owns two local-only types (internal_catalog,
// internal_resource) that reach nobody but the super-admin wildcard, so
// inheritance would buy them nothing. A synchroniser must therefore SKIP the
// types it finds undeclared instead of treating the 400 as an outage.
func checkDeclaredHierarchy(ctx context.Context, db *gorm.DB, resourceType, parentType string) error {
	var rt model.ResourceType
	err := db.WithContext(ctx).First(&rt, "id = ?", resourceType).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return errors.New("resource_type is not a registered resource type: " + resourceType)
	}
	if err != nil {
		return err
	}
	if rt.ParentTypeID == "" {
		return errors.New("resource type declares no parent_type in the catalog: " + resourceType)
	}
	if rt.ParentTypeID != parentType {
		return errors.New("resource type " + resourceType + " hangs under " + rt.ParentTypeID + ", not " + parentType)
	}
	return nil
}

// dropResourceParent removes the hierarchy row for one instance. Called on the
// resource-delete path alongside the policy teardown, so a deleted resource does
// not leave a row that would later make a recycled id inherit from a stale
// parent. Best-effort by design: the caller is deleting policies, and a failure
// to tidy the hierarchy must not fail that.
func dropResourceParent(ctx context.Context, db *gorm.DB, resourceType, resourceID string) error {
	if db == nil || resourceType == "" || resourceID == "" {
		return nil
	}
	return db.WithContext(ctx).
		Where("resource_type_id = ? AND resource_id = ?", resourceType, resourceID).
		Delete(&model.ResourceParent{}).Error
}
