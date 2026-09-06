// Copyright openbkn.ai
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package proxy_authorization

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/interfaces"
)

type fakeProxyAuthorizationAccess struct {
	account          *interfaces.ManagedProxyAccount
	accountErr       error
	allowed          bool
	permissionErr    error
	permissionChecks int
}

func (f *fakeProxyAuthorizationAccess) GetManagedProxy(context.Context, string) (*interfaces.ManagedProxyAccount, error) {
	return f.account, f.accountErr
}

func (f *fakeProxyAuthorizationAccess) CheckPermission(context.Context, string, string, string, string) (bool, error) {
	f.permissionChecks++
	return f.allowed, f.permissionErr
}

func TestProxyAuthorizationServiceAuthorize(t *testing.T) {
	request := interfaces.ProxyReadContext{
		KnowledgeID: "kn-1", ProxyID: "proxy-1", ProxyVersion: 3,
		TargetType: interfaces.ProxyTargetTypeResource, TargetID: "resource-1",
		Operation: interfaces.OPERATION_TYPE_QUERY_DATA,
	}
	active := &interfaces.ManagedProxyAccount{
		ProxyAccountID: "proxy-1", AccountType: interfaces.ProxyAccountTypeApp,
		ManagedBy: interfaces.ProxyManagerBKN, ManagedResourceType: interfaces.ProxyManagedResourceTypeKN,
		ManagedResourceID: "kn-1", LifecycleStatus: interfaces.ProxyLifecycleActive,
		Enabled: true, Version: 1,
	}

	t.Run("allows independent BKN mapping and bkn-safe lifecycle versions", func(t *testing.T) {
		access := &fakeProxyAuthorizationAccess{account: active, allowed: true}
		err := NewProxyAuthorizationService(access).Authorize(context.Background(), request)

		require.NoError(t, err)
		assert.Equal(t, 1, access.permissionChecks)
	})

	for _, test := range []struct {
		name   string
		mutate func(*interfaces.ManagedProxyAccount)
	}{
		{name: "missing proxy", mutate: func(account *interfaces.ManagedProxyAccount) { *account = interfaces.ManagedProxyAccount{} }},
		{name: "disabled proxy", mutate: func(account *interfaces.ManagedProxyAccount) {
			account.Enabled = false
			account.LifecycleStatus = "disabling"
		}},
		{name: "different knowledge network", mutate: func(account *interfaces.ManagedProxyAccount) { account.ManagedResourceID = "kn-2" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			account := *active
			test.mutate(&account)
			access := &fakeProxyAuthorizationAccess{account: &account, allowed: true}
			err := NewProxyAuthorizationService(access).Authorize(context.Background(), request)

			require.ErrorIs(t, err, interfaces.ErrProxyAuthorizationDenied)
			assert.Zero(t, access.permissionChecks, "state rejection must happen before the policy check")
		})
	}

	t.Run("denies missing resource policy", func(t *testing.T) {
		access := &fakeProxyAuthorizationAccess{account: active, allowed: false}
		err := NewProxyAuthorizationService(access).Authorize(context.Background(), request)

		require.ErrorIs(t, err, interfaces.ErrProxyAuthorizationDenied)
		assert.Equal(t, 1, access.permissionChecks)
	})

	t.Run("fails closed when bkn-safe state is unavailable", func(t *testing.T) {
		access := &fakeProxyAuthorizationAccess{accountErr: errors.New("safe unavailable")}
		err := NewProxyAuthorizationService(access).Authorize(context.Background(), request)

		require.ErrorIs(t, err, interfaces.ErrProxyAuthorizationUnavailable)
		assert.Zero(t, access.permissionChecks)
	})

	t.Run("fails closed when policy check is unavailable", func(t *testing.T) {
		access := &fakeProxyAuthorizationAccess{account: active, permissionErr: errors.New("safe unavailable")}
		err := NewProxyAuthorizationService(access).Authorize(context.Background(), request)

		require.ErrorIs(t, err, interfaces.ErrProxyAuthorizationUnavailable)
		assert.Equal(t, 1, access.permissionChecks)
	})

	t.Run("observes proxy disable immediately", func(t *testing.T) {
		account := *active
		access := &fakeProxyAuthorizationAccess{account: &account, allowed: true}
		service := NewProxyAuthorizationService(access)

		require.NoError(t, service.Authorize(context.Background(), request))
		account.Enabled = false
		account.LifecycleStatus = "disabling"
		err := service.Authorize(context.Background(), request)

		require.ErrorIs(t, err, interfaces.ErrProxyAuthorizationDenied)
		assert.Equal(t, 1, access.permissionChecks, "the disabled proxy must be rejected before another policy check")
	})

	t.Run("observes resource policy revocation immediately", func(t *testing.T) {
		access := &fakeProxyAuthorizationAccess{account: active, allowed: true}
		service := NewProxyAuthorizationService(access)

		require.NoError(t, service.Authorize(context.Background(), request))
		access.allowed = false
		err := service.Authorize(context.Background(), request)

		require.ErrorIs(t, err, interfaces.ErrProxyAuthorizationDenied)
		assert.Equal(t, 2, access.permissionChecks, "each read must re-check the resource policy")
	})
}
