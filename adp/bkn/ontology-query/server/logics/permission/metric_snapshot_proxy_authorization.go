// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package permission

import (
	"context"
	"fmt"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common"
	permissionaccess "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/drivenadapters/permission"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
)

type metricSnapshotProxyAuthorizationService struct {
	access interfaces.MetricSnapshotProxyAccess
}

func NewMetricSnapshotProxyAuthorizationService(setting *common.AppSetting) interfaces.MetricSnapshotProxyAuthorizationService {
	return &metricSnapshotProxyAuthorizationService{access: permissionaccess.NewMetricSnapshotProxyAccess(setting)}
}

func (service *metricSnapshotProxyAuthorizationService) AuthorizeMetricSnapshotProxies(ctx context.Context,
	bindings []*interfaces.TrustedProxyContext) error {
	if service == nil || service.access == nil {
		return permissionUnavailable(ctx, fmt.Errorf("metric proxy authorization is unavailable"))
	}
	states := make(map[string]*interfaces.ManagedProxyAccountState)
	checks := make(map[string][]interfaces.PermissionCheck)
	type proxyCheckKey struct{ proxy, resourceType, resourceID, operation string }
	seen := make(map[proxyCheckKey]bool)
	for _, binding := range bindings {
		if binding == nil || binding.Proxy.ID == "" || binding.Proxy.Type != interfaces.ProxyAccountTypeApp ||
			binding.Binding.KNID == "" || binding.Binding.TargetType != interfaces.ProxyTargetTypeResource ||
			binding.Binding.TargetID == "" || binding.Binding.Operation != interfaces.PermissionOperationQueryData {
			return permissionDenied(ctx, "metric proxy binding is invalid")
		}
		id := binding.Proxy.ID
		state, loaded := states[id]
		if !loaded {
			var err error
			state, err = service.access.GetManagedProxy(ctx, id)
			if err != nil {
				return permissionUnavailable(ctx, err)
			}
			states[id] = state
		}
		if state == nil || state.ProxyAccountID != id || state.AccountType != interfaces.ProxyAccountTypeApp ||
			state.ManagedBy != "bkn" || state.ManagedResourceType != interfaces.PermissionResourceTypeKnowledgeNetwork ||
			state.ManagedResourceID != binding.Binding.KNID || state.LifecycleStatus != interfaces.ProxyLifecycleActive || !state.Enabled {
			return permissionDenied(ctx, "metric proxy authorization was not granted")
		}
		key := proxyCheckKey{id, binding.Binding.TargetType, binding.Binding.TargetID, binding.Binding.Operation}
		if !seen[key] {
			checks[id] = append(checks[id], interfaces.PermissionCheck{Resource: interfaces.PermissionResource{
				Type: binding.Binding.TargetType, ID: binding.Binding.TargetID}, Operation: binding.Binding.Operation})
			seen[key] = true
		}
	}
	for id, requested := range checks {
		for start := 0; start < len(requested); start += 100 {
			batch := requested[start:min(start+100, len(requested))]
			// bkn-safe's effective checks include catalog inheritance and live
			// delegator/source validity, the same policy evaluated by Vega's PEP.
			response, err := service.access.CheckPermissions(ctx, interfaces.PermissionChecksRequest{AccessorID: id, Checks: batch})
			if err != nil {
				return permissionUnavailable(ctx, err)
			}
			if len(response.Results) != len(batch) {
				return permissionUnavailable(ctx, fmt.Errorf("metric proxy decision count mismatch"))
			}
			allowed := true
			for index, expected := range batch {
				decision := response.Results[index]
				if decision.ResourceType != expected.Resource.Type || decision.ResourceID != expected.Resource.ID || decision.Operation != expected.Operation {
					return permissionUnavailable(ctx, fmt.Errorf("metric proxy decision does not match the requested resource"))
				}
				allowed = allowed && decision.Allowed
			}
			if allowed != response.Allowed {
				return permissionUnavailable(ctx, fmt.Errorf("inconsistent metric proxy authorization decision"))
			}
			if !allowed {
				return permissionDenied(ctx, "metric proxy resource permission was not granted")
			}
		}
	}
	return nil
}
