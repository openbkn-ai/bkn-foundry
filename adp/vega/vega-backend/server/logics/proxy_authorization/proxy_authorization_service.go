// Copyright openbkn.ai
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package proxy_authorization

import (
	"context"
	"fmt"

	"vega-backend/interfaces"
)

type proxyAuthorizationService struct {
	access interfaces.ProxyAuthorizationAccess
}

func NewProxyAuthorizationService(access interfaces.ProxyAuthorizationAccess) interfaces.ProxyAuthorizationService {
	return &proxyAuthorizationService{access: access}
}

// Authorize rechecks proxy ownership, current state, and exact resource policy for every physical read.
func (s *proxyAuthorizationService) Authorize(ctx context.Context, request interfaces.ProxyReadContext) error {
	if s.access == nil {
		return fmt.Errorf("%w: bkn-safe access is not configured", interfaces.ErrProxyAuthorizationUnavailable)
	}

	account, err := s.access.GetManagedProxy(ctx, request.ProxyID)
	if err != nil {
		return fmt.Errorf("%w: load managed proxy: %v", interfaces.ErrProxyAuthorizationUnavailable, err)
	}
	if !matchesCurrentProxy(account, request) {
		return interfaces.ErrProxyAuthorizationDenied
	}

	allowed, err := s.access.CheckPermission(ctx, request.ProxyID, request.TargetType, request.TargetID, request.Operation)
	if err != nil {
		return fmt.Errorf("%w: check resource policy: %v", interfaces.ErrProxyAuthorizationUnavailable, err)
	}
	if !allowed {
		return interfaces.ErrProxyAuthorizationDenied
	}
	return nil
}

func matchesCurrentProxy(account *interfaces.ManagedProxyAccount, request interfaces.ProxyReadContext) bool {
	// request.ProxyVersion is BKN's KN-to-proxy mapping version and is retained
	// for trusted-context validation and audit. bkn-safe's account Version is an
	// independent lifecycle counter, so comparing them would reject valid reads.
	return account != nil &&
		account.ProxyAccountID == request.ProxyID &&
		account.AccountType == interfaces.ProxyAccountTypeApp &&
		account.ManagedBy == interfaces.ProxyManagerBKN &&
		account.ManagedResourceType == interfaces.ProxyManagedResourceTypeKN &&
		account.ManagedResourceID == request.KnowledgeID &&
		account.LifecycleStatus == interfaces.ProxyLifecycleActive &&
		account.Enabled
}
