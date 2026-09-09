// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"context"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/permdata"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/comm-go/propertyaccess"
)

type propertyGrantManagementServices struct {
	enforcer *authz.Enforcer
}

func newPropertyGrantManagementServices(enforcer *authz.Enforcer) permdata.ManagementServices {
	return &propertyGrantManagementServices{enforcer: enforcer}
}

// AuthorizeUserGrants gives platform authorization administrators an
// unrestricted path and object-type authorizers a delegated, ceiling-limited
// path. The EE manager applies that ceiling before changing any row.
func (services *propertyGrantManagementServices) AuthorizeUserGrants(
	ctx context.Context,
	operatorID, objectTypeRef string,
) (permdata.UserGrantAuthority, error) {
	canGrant, err := services.enforcer.Check(operatorID, "admin-authz", "*", "grant")
	if err != nil {
		return permdata.UserGrantAuthority{}, err
	}
	canRevoke, err := services.enforcer.Check(operatorID, "admin-authz", "*", "revoke")
	if err != nil {
		return permdata.UserGrantAuthority{}, err
	}
	if canGrant && canRevoke {
		return permdata.UserGrantAuthority{Allowed: true, Unrestricted: true}, nil
	}
	allowed, err := services.enforcer.Check(operatorID, "object_type", objectTypeRef, opAuthorize)
	if err != nil {
		return permdata.UserGrantAuthority{}, err
	}
	return permdata.UserGrantAuthority{Allowed: allowed}, nil
}

// AuthorizePlatformRoleGrants requires the platform role-permission point.
// Object-level authorize is intentionally insufficient because changing a role
// can affect users outside the current object owner's delegation scope.
func (services *propertyGrantManagementServices) AuthorizePlatformRoleGrants(
	ctx context.Context,
	operatorID string,
) (bool, error) {
	return services.enforcer.Check(operatorID, "admin-role", "*", "permissions")
}

// EffectivePropertyLevels resolves the operator through the same base-level
// clamp and Enterprise property resolver used by runtime data exits.
func (services *propertyGrantManagementServices) EffectivePropertyLevels(
	ctx context.Context,
	operatorID, objectTypeRef string,
	propertyNames []string,
) (map[string]propertyaccess.Level, error) {
	allowed, err := services.enforcer.FilterResourceOps(operatorID,
		[]authz.ResourceRef{{Type: "object_type", ID: objectTypeRef}}, nil,
		[]string{"view_detail", "query_data"})
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
