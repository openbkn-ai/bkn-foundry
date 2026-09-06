// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package proxy_context

import (
	"context"
	"strings"

	"ontology-query/interfaces"
)

const (
	ProxyRolloutModeOff       = "off"
	ProxyRolloutModeAllowlist = "allowlist"
	ProxyRolloutModeAll       = "all"
)

type rolloutProxyContextResolver struct {
	next      interfaces.ProxyContextResolver
	mode      string
	allowlist map[string]struct{}
}

// NewRolloutProxyContextResolver applies the deployment rollout policy before
// resolving a managed proxy. Unknown modes fail closed to the direct-caller
// path so a configuration typo cannot enable proxies globally.
func NewRolloutProxyContextResolver(
	next interfaces.ProxyContextResolver, mode string, knowledgeNetworks []string,
) interfaces.ProxyContextResolver {
	normalizedMode := strings.ToLower(strings.TrimSpace(mode))
	switch normalizedMode {
	case ProxyRolloutModeAllowlist, ProxyRolloutModeAll:
	default:
		normalizedMode = ProxyRolloutModeOff
	}
	allowlist := make(map[string]struct{}, len(knowledgeNetworks))
	for _, knID := range knowledgeNetworks {
		if normalized := strings.TrimSpace(knID); normalized != "" {
			allowlist[normalized] = struct{}{}
		}
	}
	return &rolloutProxyContextResolver{next: next, mode: normalizedMode, allowlist: allowlist}
}

func (r *rolloutProxyContextResolver) Resolve(
	ctx context.Context, binding interfaces.TrustedProxyBinding,
) (*interfaces.TrustedProxyContext, error) {
	if !r.proxyEnabled(binding.KNID) {
		caller, _ := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
		return &interfaces.TrustedProxyContext{
			Caller:          caller,
			Binding:         binding,
			UseDirectCaller: true,
		}, nil
	}
	return r.next.Resolve(ctx, binding)
}

func (r *rolloutProxyContextResolver) proxyEnabled(knID string) bool {
	if r == nil {
		return false
	}
	switch r.mode {
	case ProxyRolloutModeAll:
		return true
	case ProxyRolloutModeAllowlist:
		_, ok := r.allowlist[knID]
		return ok
	default:
		return false
	}
}
