// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package proxy_context

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	oerrors "ontology-query/errors"
	"ontology-query/interfaces"
)

type proxyContextResolver struct {
	access interfaces.KnowledgeNetworkProxyAccess
}

func NewProxyContextResolver(access interfaces.KnowledgeNetworkProxyAccess) interfaces.ProxyContextResolver {
	return &proxyContextResolver{access: access}
}

func (r *proxyContextResolver) Resolve(
	ctx context.Context, binding interfaces.TrustedProxyBinding,
) (*interfaces.TrustedProxyContext, error) {
	caller, ok := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	if !ok || strings.TrimSpace(caller.ID) == "" || strings.TrimSpace(caller.ID) != caller.ID ||
		strings.TrimSpace(caller.Type) == "" || strings.TrimSpace(caller.Type) != caller.Type {
		return nil, proxyUnavailable(ctx, "request caller is unavailable")
	}
	if err := validateBinding(binding); err != nil {
		return nil, proxyUnavailable(ctx, "trusted proxy binding is invalid")
	}
	if r == nil || r.access == nil {
		return nil, proxyUnavailable(ctx, "knowledge network proxy resolver is not configured")
	}

	mapping, err := r.access.ResolveKnowledgeNetworkProxy(ctx, binding)
	if err != nil || mapping == nil {
		return nil, proxyUnavailable(ctx, "knowledge network proxy mapping is unavailable")
	}
	if mapping.KNID != binding.KNID ||
		strings.TrimSpace(mapping.ProxyAccountID) == "" ||
		strings.TrimSpace(mapping.ProxyAccountID) != mapping.ProxyAccountID ||
		mapping.ProxyAccountType != interfaces.ProxyAccountTypeApp ||
		mapping.LifecycleStatus != interfaces.ProxyLifecycleActive ||
		mapping.SyncStatus != interfaces.ProxySyncReady ||
		mapping.Version <= 0 ||
		strings.TrimSpace(mapping.PublishedModelVersion) == "" ||
		strings.TrimSpace(mapping.PublishedModelVersion) != mapping.PublishedModelVersion ||
		mapping.PublishedModelVersion != mapping.SyncedModelVersion {
		return nil, proxyUnavailable(ctx, "knowledge network proxy is not ready")
	}

	return &interfaces.TrustedProxyContext{
		Caller: caller,
		Proxy: interfaces.AccountInfo{
			ID:   mapping.ProxyAccountID,
			Type: mapping.ProxyAccountType,
		},
		ProxyVersion:          mapping.Version,
		PublishedModelVersion: mapping.PublishedModelVersion,
		Binding:               binding,
	}, nil
}

func validateBinding(binding interfaces.TrustedProxyBinding) error {
	values := []string{
		binding.KNID,
		binding.ChildType,
		binding.ChildID,
		binding.TargetType,
		binding.TargetID,
		binding.Operation,
	}
	for _, value := range values {
		if strings.TrimSpace(value) == "" || strings.TrimSpace(value) != value {
			return fmt.Errorf("binding fields are required")
		}
		if strings.ContainsAny(value, "*\r\n") {
			return fmt.Errorf("binding fields contain forbidden characters")
		}
	}
	if strings.Contains(binding.KNID, "/") || strings.Contains(binding.ChildID, "/") ||
		strings.Contains(binding.TargetID, "/") {
		return fmt.Errorf("binding resource ids cannot contain slash")
	}

	switch binding.ChildType {
	case interfaces.PermissionResourceTypeObjectType:
		if binding.TargetType != interfaces.ProxyTargetTypeResource ||
			(binding.Operation != interfaces.PermissionOperationViewDetail &&
				binding.Operation != interfaces.PermissionOperationQueryData) {
			return fmt.Errorf("data binding target or operation is invalid")
		}
	case interfaces.PermissionResourceTypeRelationType,
		interfaces.PermissionResourceTypeMetric:
		if binding.TargetType != interfaces.ProxyTargetTypeResource ||
			binding.Operation != interfaces.PermissionOperationQueryData {
			return fmt.Errorf("data binding target or operation is invalid")
		}
	case interfaces.PermissionResourceTypeActionType:
		if (binding.TargetType != interfaces.ProxyTargetTypeToolBox &&
			binding.TargetType != interfaces.ProxyTargetTypeMCP) ||
			binding.Operation != interfaces.PermissionOperationExecute {
			return fmt.Errorf("action binding target or operation is invalid")
		}
	case interfaces.PermissionResourceTypeLogicProperty:
		if binding.TargetType != interfaces.ProxyTargetTypeToolBox ||
			binding.Operation != interfaces.PermissionOperationExecute {
			return fmt.Errorf("logic property binding target or operation is invalid")
		}
	default:
		return fmt.Errorf("binding child type is invalid")
	}
	return nil
}

func proxyUnavailable(ctx context.Context, detail string) *rest.HTTPError {
	return rest.NewHTTPError(ctx, http.StatusServiceUnavailable,
		oerrors.OntologyQuery_InternalError_CheckPermissionFailed).WithErrorDetails(detail)
}
