// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/permissionmatrix"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
	"gorm.io/gorm"
)

// Domain services own resource enumeration. This admin-only projection accepts
// one visible page of concrete candidates, never enumerates platform policies,
// and evaluates all operations in one shared grant-index pass.
func registerPermissionProjection(g *gin.RouterGroup, e *authz.Enforcer, db *gorm.DB) {
	g.POST("/permission-projection", RequirePermission(e, "admin-authz", "view"), func(c *gin.Context) {
		var req struct {
			Subject struct {
				ID   string `json:"id"`
				Type string `json:"type"`
			} `json:"subject"`
			NetworkID      string              `json:"network_id"`
			Resources      []authz.ResourceRef `json:"resources"`
			IncludeSources bool                `json:"include_sources"`
		}
		if !bind(c, &req) {
			return
		}
		if req.Subject.ID == "" || !isConcreteResourceID(req.NetworkID) || (req.Subject.Type != "user" && req.Subject.Type != "role") || len(req.Resources) > 100 {
			replyPublicError(c, http.StatusBadRequest)
			return
		}
		// Full provenance is an on-demand detail query for one resource. Listing
		// a page never performs per-operation explanation walks.
		if req.IncludeSources && len(req.Resources) != 1 {
			replyPublicError(c, http.StatusBadRequest)
			return
		}
		if req.Subject.Type == "user" {
			active, err := activeAccount(c, db, req.Subject.ID)
			if err != nil {
				serverError(c, err)
				return
			}
			if !active {
				replyPublicError(c, http.StatusNotFound)
				return
			}
		} else {
			var role model.Role
			if err := db.WithContext(c.Request.Context()).First(&role, "id = ?", req.Subject.ID).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					replyPublicError(c, http.StatusNotFound)
				} else {
					serverError(c, err)
				}
				return
			}
		}
		operations := map[string][]string{}
		checks := make([]authz.ResourceOperationCheck, 0, len(req.Resources))
		for _, ref := range req.Resources {
			valid := ref.Type == "knowledge_network" && ref.ID == req.NetworkID
			switch ref.Type {
			case "object_type", "relation_type", "action_type", "metric":
				valid = strings.HasPrefix(ref.ID, req.NetworkID+"/")
			}
			if !valid || !isConcreteResourceID(ref.ID) {
				replyPublicError(c, http.StatusBadRequest)
				return
			}
			ops, exists := operations[ref.Type]
			if !exists {
				catalog, err := e.Registry().Operations(c.Request.Context(), ref.Type)
				if err != nil {
					serverError(c, err)
					return
				}
				for _, op := range catalog {
					if op.ID == "create" {
						continue
					} // Creation is type-scoped, never an existing instance operation.
					ops = append(ops, op.ID)
				}
				operations[ref.Type] = ops
			}
			checks = append(checks, authz.ResourceOperationCheck{Resource: ref, Operations: ops})
		}
		results, err := e.CheckResourceOpsScoped(c.Request.Context(), req.Subject.ID, checks, authz.ScopeEffective)
		if err != nil {
			serverError(c, err)
			return
		}
		objectRefs := []string{}
		for _, ref := range req.Resources {
			if ref.Type == "object_type" {
				objectRefs = append(objectRefs, ref.ID)
			}
		}
		var summaries map[string]permissionmatrix.Summary
		if permissionmatrix.Available() && len(objectRefs) > 0 {
			roles, err := e.ImplicitRolesForAccessor(req.Subject.ID)
			if err != nil {
				serverError(c, err)
				return
			}
			summaries, err = permissionmatrix.Summarize(c.Request.Context(), permissionmatrix.Request{SubjectType: req.Subject.Type, SubjectID: req.Subject.ID, RoleIDs: roles, ObjectTypeRefs: uniqueStrings(objectRefs)})
			if err != nil {
				serverError(c, err)
				return
			}
		}
		entries := make([]gin.H, 0, len(results))
		sourceIDs := []string{}
		sourceTargets := map[string]gin.H{}
		canManageObjects, canManageRoles := false, false
		if req.IncludeSources {
			canManageObjects, err = e.CheckContext(c.Request.Context(), c.GetString(ctxAccessorID), "admin-authz", "*", "grant")
			if err != nil {
				serverError(c, err)
				return
			}
			canManageRoles, err = e.CheckContext(c.Request.Context(), c.GetString(ctxAccessorID), "admin-role", "*", "permissions")
			if err != nil {
				serverError(c, err)
				return
			}
			canManageRoles = canManageRoles || canManageObjects
			if !canManageRoles {
				canManageRoles, err = e.CheckContext(c.Request.Context(), c.GetString(ctxAccessorID), "admin-authz", "*", "revoke")
				if err != nil {
					serverError(c, err)
					return
				}
			}
		}
		for _, result := range results {
			explanations := map[string]authz.OperationExplanation{}
			if req.IncludeSources {
				ops := make([]string, 0, len(result.Decisions))
				for _, decision := range result.Decisions {
					ops = append(ops, decision.Operation)
				}
				explain := e.ExplainResourceOperations
				if req.Subject.Type == "role" {
					explain = e.ExplainRoleResourceOperations
				}
				traces, err := explain(c.Request.Context(), req.Subject.ID, authz.ResourceRef{Type: result.Type, ID: result.ID}, ops)
				if err != nil {
					serverError(c, err)
					return
				}
				for _, trace := range traces {
					explanations[trace.Operation] = trace
				}
			}
			allowed := make([]string, 0)
			decisions := make([]gin.H, 0, len(result.Decisions))
			for _, decision := range result.Decisions {
				if decision.Decision == authz.DecisionAllow {
					allowed = append(allowed, decision.Operation)
				}
				item := gin.H{"operation": decision.Operation, "decision": decision.Decision, "basis": decision.Basis, "requires": decision.Requirements, "denied_requirement": decision.DeniedRequirement, "requirement_basis": decision.RequirementBasis}
				if req.IncludeSources {
					explanation := explanations[decision.Operation]
					item["steps"] = explanation.Steps
					for _, step := range explanation.Steps {
						for _, grant := range step.MatchedGrants {
							sourceIDs = append(sourceIDs, grant.SubjectID)
							resourceType, resourceID, valid := strings.Cut(grant.Object, ":")
							kind, allowed := "read_only", false
							if valid && isConcreteResourceID(resourceID) && (grant.SubjectKind == "user" || grant.SubjectKind == "role") {
								kind, allowed = "object_grants", canManageObjects
							}
							if valid && strings.Contains(resourceID, "*") && grant.SubjectKind == "role" {
								kind, allowed = "role_permissions", canManageRoles
							}
							sourceTargets[grant.GrantID] = gin.H{"kind": kind, "can_manage": allowed, "subject_id": grant.SubjectID, "subject_type": grant.SubjectKind, "resource_type": resourceType, "resource_id": resourceID}
						}
					}
					item["requirements"] = explanation.Requirements
				}
				decisions = append(decisions, item)
			}
			entry := gin.H{"accessor_id": req.Subject.ID, "accessor_type": req.Subject.Type, "resource": gin.H{"type": result.Type, "id": result.ID}, "operations": allowed, "effective_decisions": decisions}
			if result.Type == "object_type" {
				if summary, ok := summaries[result.ID]; ok {
					entry["configuration_summary"] = summary
				}
			}
			entries = append(entries, entry)
		}
		subjects := map[string]string{}
		if len(sourceIDs) > 0 {
			var users []model.User
			var roles []model.Role
			if err := db.WithContext(c.Request.Context()).Select("id", "name", "account").Where("id IN ?", uniqueStrings(sourceIDs)).Find(&users).Error; err != nil {
				serverError(c, err)
				return
			}
			if err := db.WithContext(c.Request.Context()).Select("id", "name", "source").Where("id IN ?", uniqueStrings(sourceIDs)).Find(&roles).Error; err != nil {
				serverError(c, err)
				return
			}
			for _, user := range users {
				name := user.Name
				if name == "" {
					name = user.Account
				}
				subjects[user.ID] = name
			}
			for _, role := range roles {
				subjects[role.ID] = role.Name
				if role.BuiltIn() {
					for _, target := range sourceTargets {
						if target["subject_id"] == role.ID && target["kind"] == "role_permissions" {
							target["can_manage"] = false
						}
					}
				}
			}
		}
		c.JSON(http.StatusOK, gin.H{"entries": entries, "subjects": subjects, "source_targets": sourceTargets})
	})
}
