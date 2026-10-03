// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

import (
	"context"

	rowfiltersocket "github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/rowfilter"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/directory"
	internalrowfilter "github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/rowfilter"
	"gorm.io/gorm"
)

type RowFilterPublishedObjectTypeResolver interface {
	ResolvePublishedObjectType(ctx context.Context, operatorID, objectTypeRef string) (rowfiltersocket.PublishedObjectType, error)
}

type rowFilterManagementServices struct {
	enforcer  *authz.Enforcer
	db        *gorm.DB
	directory *directory.Service
	published RowFilterPublishedObjectTypeResolver
}

func newRowFilterManagementServices(enforcer *authz.Enforcer, db *gorm.DB, directoryService *directory.Service,
	published RowFilterPublishedObjectTypeResolver) rowfiltersocket.ManagementServices {
	return &rowFilterManagementServices{enforcer: enforcer, db: db, directory: directoryService, published: published}
}

func (services *rowFilterManagementServices) authorizeObjectType(ctx context.Context, operatorID, objectTypeRef string) (bool, error) {
	_, allowed, err := authorizeDelegatedResourceManagement(ctx, services.enforcer, services.db, operatorID,
		resourceRef{Type: "object_type", ID: objectTypeRef})
	return allowed, err
}

func (services *rowFilterManagementServices) AuthorizeUserRead(ctx context.Context, operatorID, objectTypeRef string) (bool, error) {
	admin, err := services.enforcer.CheckContext(ctx, operatorID, "admin-authz", "*", "view")
	if err != nil || admin {
		return admin, err
	}
	return services.authorizeObjectType(ctx, operatorID, objectTypeRef)
}

func (services *rowFilterManagementServices) AuthorizeUserWrite(ctx context.Context, operatorID, objectTypeRef string) (bool, error) {
	grant, err := services.enforcer.CheckContext(ctx, operatorID, "admin-authz", "*", "grant")
	if err != nil {
		return false, err
	}
	if grant {
		revoke, revokeErr := services.enforcer.CheckContext(ctx, operatorID, "admin-authz", "*", "revoke")
		if revokeErr != nil || revoke {
			return revoke, revokeErr
		}
	}
	return services.authorizeObjectType(ctx, operatorID, objectTypeRef)
}

func (services *rowFilterManagementServices) AuthorizeRoleRead(ctx context.Context, operatorID, objectTypeRef string) (bool, error) {
	admin, err := services.enforcer.CheckContext(ctx, operatorID, "admin-role", "*", "view")
	if err != nil || admin {
		return admin, err
	}
	return services.authorizeObjectType(ctx, operatorID, objectTypeRef)
}

func (services *rowFilterManagementServices) AuthorizeRoleWrite(ctx context.Context, operatorID, objectTypeRef string) (bool, error) {
	admin, err := services.enforcer.CheckContext(ctx, operatorID, "admin-role", "*", "permissions")
	if err != nil || admin {
		return admin, err
	}
	return services.authorizeObjectType(ctx, operatorID, objectTypeRef)
}

func (services *rowFilterManagementServices) CanInspectRoleMembership(ctx context.Context, operatorID string) (bool, error) {
	return services.enforcer.CheckContext(ctx, operatorID, "admin-role", "*", "view")
}

func (services *rowFilterManagementServices) GrantSubjectExists(ctx context.Context, subjectType, subjectID string) (bool, error) {
	return grantSubjectExists(ctx, services.db, subjectType, subjectID)
}

func (services *rowFilterManagementServices) ResolveCaller(ctx context.Context, userID string) (rowfiltersocket.Caller, error) {
	return internalrowfilter.NewTrustedCallerResolver(services.directory, services.enforcer).Resolve(ctx, userID)
}

func (services *rowFilterManagementServices) ResolvePublishedObjectType(ctx context.Context, operatorID, objectTypeRef string) (rowfiltersocket.PublishedObjectType, error) {
	return services.published.ResolvePublishedObjectType(ctx, operatorID, objectTypeRef)
}
