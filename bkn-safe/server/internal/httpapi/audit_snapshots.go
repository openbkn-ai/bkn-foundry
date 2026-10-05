// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/directory"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

var errAuditSnapshotUnavailable = errors.New("audit target snapshot is unavailable")

const auditSnapshotResolveTimeout = 2 * time.Second

func resolveObjectGrantAuditName(
	ctx context.Context,
	catalog AuthorizationResourceCatalog,
	db *gorm.DB,
	dir *directory.Service,
	accessorID, resourceType, resourceID string,
) (string, error) {
	resolveCtx, cancel := context.WithTimeout(ctx, auditSnapshotResolveTimeout)
	defer cancel()
	if catalog == nil {
		return "", fmt.Errorf("%w: authorization resource catalog is unavailable", errAuditSnapshotUnavailable)
	}
	resource, err := catalog.Resolve(resolveCtx, resourceType, resourceID)
	if err != nil {
		return "", fmt.Errorf("%w: resolve %s %s: %v", errAuditSnapshotUnavailable, resourceType, resourceID, err)
	}
	name := auditObjectGrantName(resolveCtx, db, dir, accessorID, resource.Name)
	if name == "" {
		return "", fmt.Errorf("%w: accessor %s has no display name", errAuditSnapshotUnavailable, accessorID)
	}
	return name, nil
}

func setObjectGrantAuditOperation(
	c *gin.Context,
	catalog AuthorizationResourceCatalog,
	db *gorm.DB,
	dir *directory.Service,
	action, accessorID, resourceType, resourceID string,
) {
	targetID := objectGrantScopeTargetID(accessorID, resourceType, resourceID)
	name, err := resolveObjectGrantAuditName(c.Request.Context(), catalog, db, dir, accessorID, resourceType, resourceID)
	if err != nil {
		setAuditSnapshotFailure(c, action, targetID, err)
		return
	}
	setAuditOperation(c, action, targetID, name)
}

func auditSubjectName(ctx context.Context, db *gorm.DB, dir *directory.Service, subjectType, subjectID string) string {
	switch subjectType {
	case "user":
		if name := accessorNameByID(ctx, dir, subjectID); name != "" {
			return name
		}
	case "role":
		return roleNameByID(ctx, db, subjectID)
	case "department":
		if dir != nil {
			if names, err := dir.ResolveDepartmentNames(ctx, []string{subjectID}); err == nil && len(names) > 0 {
				return strings.TrimSpace(names[0].Name)
			}
		}
	case "group":
		// Groups have no directory batch resolver; use the authoritative local
		// identity projection below.
	}
	if db == nil || subjectID == "" {
		return ""
	}
	var target any
	switch subjectType {
	case "user":
		var user model.User
		if err := db.WithContext(ctx).Select("name", "account").First(&user, "id = ?", subjectID).Error; err != nil {
			return ""
		}
		if name := strings.TrimSpace(user.Name); name != "" {
			return name
		}
		return strings.TrimSpace(user.Account)
	case "role":
		target = &model.Role{}
	case "department":
		target = &model.Department{}
	case "group":
		target = &model.Group{}
	default:
		return ""
	}
	var row struct{ Name string }
	if err := db.WithContext(ctx).Model(target).Select("name").Where("id = ?", subjectID).Scan(&row).Error; err != nil {
		return ""
	}
	return strings.TrimSpace(row.Name)
}

func resolveScopedAuditTarget(
	ctx context.Context,
	catalog AuthorizationResourceCatalog,
	db *gorm.DB,
	dir *directory.Service,
	targetKind, targetLabel, subjectType, subjectID, resourceType, resourceID string,
) (string, string, error) {
	resolveCtx, cancel := context.WithTimeout(ctx, auditSnapshotResolveTimeout)
	defer cancel()
	subjectName := auditSubjectName(resolveCtx, db, dir, subjectType, subjectID)
	if subjectName == "" {
		return "", "", fmt.Errorf("%w: %s %s has no display name", errAuditSnapshotUnavailable, subjectType, subjectID)
	}
	if catalog == nil {
		return "", "", fmt.Errorf("%w: authorization resource catalog is unavailable", errAuditSnapshotUnavailable)
	}
	resource, err := catalog.Resolve(resolveCtx, resourceType, resourceID)
	if err != nil {
		return "", "", fmt.Errorf("%w: resolve %s %s: %v", errAuditSnapshotUnavailable, resourceType, resourceID, err)
	}
	targetID := fmt.Sprintf("%s:%s:%s:%s", targetKind, subjectType, subjectID, resourceID)
	return targetID, fmt.Sprintf("%s · %s for %s", subjectName, targetLabel, resource.Name), nil
}

func setScopedManagementAuditOperation(
	c *gin.Context,
	catalog AuthorizationResourceCatalog,
	db *gorm.DB,
	dir *directory.Service,
	action, targetKind, targetLabel, subjectType, subjectID, objectTypeRef string,
) {
	targetID, targetName, err := resolveScopedAuditTarget(c.Request.Context(), catalog, db, dir,
		targetKind, targetLabel, subjectType, subjectID, objectTypeResourceType, objectTypeRef)
	if err != nil {
		if targetID == "" {
			targetID = fmt.Sprintf("%s:%s:%s:%s", targetKind, subjectType, subjectID, objectTypeRef)
		}
		setAuditSnapshotFailure(c, action, targetID, err)
		return
	}
	setAuditOperation(c, action, targetID, targetName)
}

func roleBindingTargetID(accessorID, roleID string) string {
	return "role-binding:" + accessorID + ":" + roleID
}

func objectGrantScopeTargetID(accessorID, resourceType, resourceID string) string {
	return "object-grant:" + accessorID + ":" + resourceType + ":" + resourceID
}

func objectGrantBatchTargetID(grantIDs []string) string {
	normalized := append([]string(nil), grantIDs...)
	// Callers already reject duplicates and preserve request order. Sorting
	// keeps retries of the same logical batch correlated even if UI order moves.
	sort.Strings(normalized)
	digest := sha256.Sum256([]byte(strings.Join(normalized, "\x1f")))
	return "object-grant-batch:" + hex.EncodeToString(digest[:12])
}

func oauthRedirectTargetID(clientID, redirectURI string) string {
	digest := sha256.Sum256([]byte(redirectURI))
	return "oauth-client:" + clientID + ":redirect-uri:" + hex.EncodeToString(digest[:12])
}
