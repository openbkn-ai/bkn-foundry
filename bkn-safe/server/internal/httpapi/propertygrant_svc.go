// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"context"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/permdata"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/comm-go/propertyaccess"
	"gorm.io/gorm"
)

type propertyGrantManagementServices struct {
	enforcer *authz.Enforcer
	db       *gorm.DB
}

func newPropertyGrantManagementServices(enforcer *authz.Enforcer, db *gorm.DB) permdata.ManagementServices {
	return &propertyGrantManagementServices{enforcer: enforcer, db: db}
}

// AuthorizeUserGrants gives platform authorization administrators an
// unrestricted path and resource-root authorizers a delegated, ceiling-limited
// path. The EE manager applies that ceiling before changing any row.
func (services *propertyGrantManagementServices) AuthorizeUserGrants(
	ctx context.Context,
	operatorID, objectTypeRef string,
) (permdata.UserGrantAuthority, error) {
	canGrant, err := services.enforcer.CheckContext(ctx, operatorID, "admin-authz", "*", "grant")
	if err != nil {
		return permdata.UserGrantAuthority{}, err
	}
	canRevoke, err := services.enforcer.CheckContext(ctx, operatorID, "admin-authz", "*", "revoke")
	if err != nil {
		return permdata.UserGrantAuthority{}, err
	}
	if canGrant && canRevoke {
		return permdata.UserGrantAuthority{Allowed: true, Unrestricted: true}, nil
	}
	_, allowed, err := authorizeDelegatedResourceManagement(ctx, services.enforcer, services.db, operatorID,
		resourceRef{Type: "object_type", ID: objectTypeRef})
	if err != nil {
		return permdata.UserGrantAuthority{}, err
	}
	return permdata.UserGrantAuthority{Allowed: allowed}, nil
}

// AuthorizeRoleGrants applies the same authorization-root boundary and property-level
// ceiling as user grants. It authorizes a role as the subject of this one
// object-type policy; it does not expose or modify role membership or the
// role's platform-wide permission set.
func (services *propertyGrantManagementServices) AuthorizeRoleGrants(
	ctx context.Context,
	operatorID, objectTypeRef string,
) (permdata.UserGrantAuthority, error) {
	platformRoleAdmin, err := services.enforcer.CheckContext(ctx, operatorID, "admin-role", "*", "permissions")
	if err != nil {
		return permdata.UserGrantAuthority{}, err
	}
	if platformRoleAdmin {
		return permdata.UserGrantAuthority{Allowed: true, Unrestricted: true}, nil
	}
	return services.AuthorizeUserGrants(ctx, operatorID, objectTypeRef)
}

func (services *propertyGrantManagementServices) GrantSubjectExists(
	ctx context.Context,
	subjectType, subjectID string,
) (bool, error) {
	return grantSubjectExists(ctx, services.db, subjectType, subjectID)
}

// EffectivePropertyLevels resolves the operator through the same base-level
// clamp and Enterprise property resolver used by runtime data exits.
func (services *propertyGrantManagementServices) EffectivePropertyLevels(
	ctx context.Context,
	operatorID, objectTypeRef string,
	propertyNames []string,
) (map[string]propertyaccess.Level, error) {
	allowed, err := services.enforcer.FilterResourceOpsScoped(ctx, operatorID,
		[]authz.ResourceRef{{Type: "object_type", ID: objectTypeRef}}, nil,
		[]string{"view_detail", "query_data"}, authz.VisibilityMatchAll, authz.ScopeEffective)
	if err != nil {
		return nil, err
	}
	var operations []string
	if len(allowed) == 1 {
		operations = allowed[0].Operations
	}
	response, err := permdata.Resolve(ctx, permdata.Request{
		AccessorID: operatorID,
		Items: []permdata.RequestItem{{
			ObjectTypeRef: objectTypeRef,
			Properties:    uniqueStrings(propertyNames),
			BaseLevel:     basePropertyLevel(operations),
		}},
	})
	if err != nil {
		return nil, err
	}
	levels := make(map[string]propertyaccess.Level, len(propertyNames))
	for _, property := range response.Entries[0].Properties {
		levels[property.Name] = property.Level
	}
	return levels, nil
}
