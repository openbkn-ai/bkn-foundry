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
)

type RowFilterPublishedObjectTypeResolver interface {
	ResolvePublishedObjectType(ctx context.Context, operatorID, objectTypeRef string) (rowfiltersocket.PublishedObjectType, error)
}

type rowFilterManagementServices struct {
	enforcer  *authz.Enforcer
	directory *directory.Service
	published RowFilterPublishedObjectTypeResolver
}

func newRowFilterManagementServices(enforcer *authz.Enforcer, directoryService *directory.Service,
	published RowFilterPublishedObjectTypeResolver) rowfiltersocket.ManagementServices {
	return &rowFilterManagementServices{enforcer: enforcer, directory: directoryService, published: published}
}

func (services *rowFilterManagementServices) AuthorizeUserRead(ctx context.Context, operatorID string) (bool, error) {
	return services.enforcer.CheckContext(ctx, operatorID, "admin-authz", "*", "view")
}

func (services *rowFilterManagementServices) AuthorizeUserWrite(ctx context.Context, operatorID string) (bool, error) {
	grant, err := services.enforcer.CheckContext(ctx, operatorID, "admin-authz", "*", "grant")
	if err != nil || !grant {
		return grant, err
	}
	return services.enforcer.CheckContext(ctx, operatorID, "admin-authz", "*", "revoke")
}

func (services *rowFilterManagementServices) AuthorizeRoleRead(ctx context.Context, operatorID string) (bool, error) {
	return services.enforcer.CheckContext(ctx, operatorID, "admin-role", "*", "view")
}

func (services *rowFilterManagementServices) AuthorizeRoleWrite(ctx context.Context, operatorID string) (bool, error) {
	return services.enforcer.CheckContext(ctx, operatorID, "admin-role", "*", "permissions")
}

func (services *rowFilterManagementServices) ResolveCaller(ctx context.Context, userID string) (rowfiltersocket.Caller, error) {
	return internalrowfilter.NewTrustedCallerResolver(services.directory, services.enforcer).Resolve(ctx, userID)
}

func (services *rowFilterManagementServices) ResolvePublishedObjectType(ctx context.Context, operatorID, objectTypeRef string) (rowfiltersocket.PublishedObjectType, error) {
	return services.published.ResolvePublishedObjectType(ctx, operatorID, objectTypeRef)
}
