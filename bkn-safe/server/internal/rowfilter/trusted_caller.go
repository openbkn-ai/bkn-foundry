// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package rowfilter resolves the server-trusted caller context that the
// row-filter extension socket accepts. It is intentionally internal to
// bkn-safe: public callers may name an identity for authentication, but may
// never provide roles or department ranges themselves.
package rowfilter

import (
	"context"
	"errors"
	"fmt"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/rowfilter"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/authz"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/directory"
)

// ErrCallerUnavailable covers a missing, disabled, or non-user caller. It is
// deliberately a single opaque error because the query layer must fail closed
// rather than downgrade an unresolved identity to the TRUE compatibility plan.
var ErrCallerUnavailable = errors.New("row filter caller is unavailable")

// TrustedCallerResolver loads one caller's complete policy-subject context.
// Both dependencies are bkn-safe-owned services backed by the authoritative
// directory and Casbin role graph.
type TrustedCallerResolver struct {
	directory                *directory.Service
	authz                    *authz.Enforcer
	maxDepartmentScopeValues int
}

func NewTrustedCallerResolver(directoryService *directory.Service, enforcer *authz.Enforcer) *TrustedCallerResolver {
	return NewTrustedCallerResolverWithDepartmentLimit(directoryService, enforcer, directory.DefaultRowFilterDepartmentScopeLimit)
}

// NewTrustedCallerResolverWithDepartmentLimit uses the deployment's measured
// minimum safe value for every target query backend. A non-positive limit is a
// configuration error that resolves callers as unavailable rather than
// yielding an unbounded department predicate.
func NewTrustedCallerResolverWithDepartmentLimit(directoryService *directory.Service, enforcer *authz.Enforcer, maxDepartmentScopeValues int) *TrustedCallerResolver {
	return &TrustedCallerResolver{
		directory:                directoryService,
		authz:                    enforcer,
		maxDepartmentScopeValues: maxDepartmentScopeValues,
	}
}

// Resolve returns only data loaded from trusted local services. It accepts a
// user id after authentication has established the actual caller (or the
// Caller in TrustedProxyContext); it must not be fed a proxy principal or a
// caller-selected role/department list.
func (resolver *TrustedCallerResolver) Resolve(ctx context.Context, userID string) (rowfilter.Caller, error) {
	if resolver == nil || resolver.directory == nil || resolver.authz == nil || resolver.maxDepartmentScopeValues <= 0 || userID == "" {
		return rowfilter.Caller{}, ErrCallerUnavailable
	}
	user, err := resolver.directory.GetUser(ctx, userID)
	if err != nil {
		return rowfilter.Caller{}, fmt.Errorf("%w: resolve user", ErrCallerUnavailable)
	}
	if !user.Enabled || user.AccountType == "app" {
		return rowfilter.Caller{}, ErrCallerUnavailable
	}
	roles, err := resolver.authz.ImplicitRolesForAccessor(user.ID)
	if err != nil {
		return rowfilter.Caller{}, fmt.Errorf("%w: resolve roles", ErrCallerUnavailable)
	}
	departments, err := resolver.directory.UserRowFilterDepartmentScopeWithLimit(ctx, user.ID, resolver.maxDepartmentScopeValues)
	if err != nil {
		return rowfilter.Caller{}, fmt.Errorf("%w: resolve departments", ErrCallerUnavailable)
	}
	return rowfilter.Caller{
		UserID:              user.ID,
		RoleIDs:             roles,
		DirectDepartmentIDs: departments.DirectDepartmentIDs,
		DepartmentTreeIDs:   departments.DepartmentTreeIDs,
	}, nil
}
