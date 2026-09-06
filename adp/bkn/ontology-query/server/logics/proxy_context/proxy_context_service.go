// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package proxy_context

import (
	"context"
	"errors"
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

const (
	bknProxyBindingInvalidCode  = "BknBackend.KnowledgeNetwork.Proxy.BindingInvalid"
	bknProxyDisabledCode        = "BknBackend.KnowledgeNetwork.Proxy.Disabled"
	bknProxyMappingNotFoundCode = "BknBackend.KnowledgeNetwork.Proxy.MappingNotFound"
	bknProxySyncFailedCode      = "BknBackend.KnowledgeNetwork.Proxy.SyncFailed"
	bknProxySyncPendingCode     = "BknBackend.KnowledgeNetwork.Proxy.SyncPending"
)

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
		return nil, proxyError(ctx, http.StatusForbidden, oerrors.OntologyQuery_Proxy_BindingInvalid,
			"trusted proxy binding is invalid")
	}
	if r == nil || r.access == nil {
		return nil, proxyUnavailable(ctx, "knowledge network proxy resolver is not configured")
	}

	mapping, err := r.access.ResolveKnowledgeNetworkProxy(ctx, binding)
	if err != nil {
		return nil, mapProxyResolutionError(ctx, err)
	}
	if mapping == nil {
		return nil, proxyUnavailable(ctx, "knowledge network proxy mapping is unavailable")
	}
	if mapping.LifecycleStatus != interfaces.ProxyLifecycleActive {
		return nil, proxyError(ctx, http.StatusServiceUnavailable, oerrors.OntologyQuery_Proxy_Disabled,
			"knowledge network proxy is not active")
	}
	if mapping.SyncStatus == interfaces.ProxySyncFailed {
		return nil, proxyError(ctx, http.StatusServiceUnavailable, oerrors.OntologyQuery_Proxy_SyncFailed,
			"knowledge network proxy synchronization failed")
	}
	if mapping.SyncStatus != interfaces.ProxySyncReady ||
		strings.TrimSpace(mapping.PublishedModelVersion) == "" ||
		mapping.PublishedModelVersion != mapping.SyncedModelVersion {
		return nil, proxyError(ctx, http.StatusServiceUnavailable, oerrors.OntologyQuery_Proxy_SyncPending,
			"knowledge network proxy is not synchronized with the current published model")
	}
	if mapping.KNID != binding.KNID ||
		strings.TrimSpace(mapping.ProxyAccountID) == "" ||
		strings.TrimSpace(mapping.ProxyAccountID) != mapping.ProxyAccountID ||
		mapping.ProxyAccountType != interfaces.ProxyAccountTypeApp ||
		mapping.Version <= 0 ||
		strings.TrimSpace(mapping.PublishedModelVersion) != mapping.PublishedModelVersion {
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
	return proxyError(ctx, http.StatusServiceUnavailable, oerrors.OntologyQuery_Proxy_Unavailable, detail)
}

func proxyError(ctx context.Context, status int, code, detail string) *rest.HTTPError {
	return rest.NewHTTPError(ctx, status, code).WithErrorDetails(detail)
}

func mapProxyResolutionError(ctx context.Context, err error) *rest.HTTPError {
	var resolutionErr *interfaces.KnowledgeNetworkProxyResolveError
	if !errors.As(err, &resolutionErr) {
		return proxyUnavailable(ctx, "knowledge network proxy mapping is unavailable")
	}
	switch resolutionErr.Code {
	case bknProxyBindingInvalidCode:
		return proxyError(ctx, http.StatusForbidden, oerrors.OntologyQuery_Proxy_BindingInvalid,
			"target is not a current published binding")
	case bknProxyDisabledCode:
		return proxyError(ctx, http.StatusServiceUnavailable, oerrors.OntologyQuery_Proxy_Disabled,
			"knowledge network proxy is not active")
	case bknProxyMappingNotFoundCode:
		return proxyError(ctx, http.StatusNotFound, oerrors.OntologyQuery_Proxy_MappingNotFound,
			"knowledge network proxy mapping does not exist")
	case bknProxySyncFailedCode:
		return proxyError(ctx, http.StatusServiceUnavailable, oerrors.OntologyQuery_Proxy_SyncFailed,
			"knowledge network proxy synchronization failed")
	case bknProxySyncPendingCode:
		return proxyError(ctx, http.StatusServiceUnavailable, oerrors.OntologyQuery_Proxy_SyncPending,
			"knowledge network proxy synchronization is pending")
	default:
		return proxyUnavailable(ctx, "knowledge network proxy mapping is unavailable")
	}
}
