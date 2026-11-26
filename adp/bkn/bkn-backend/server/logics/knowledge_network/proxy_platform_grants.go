// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knowledge_network

import (
	"context"
	"fmt"

	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// A resource the platform keeps for a kind of object type, such as an event class's dataset, is
// one the editors of the network hold no permission on. The built-in administrator grants it to
// the network's proxy instead, and bkn-safe then resolves the source to the administrator for
// whichever editor publishes: a source a delegator still vouches for keeps that delegator.

// grantPlatformKept has the built-in administrator grant the proxy the sources a preflight
// denied whose resources the platform keeps, and reports whether it granted any: the caller then
// checks again. A grant that fails is logged, and the second check denies the source as before.
func (kns *knowledgeNetworkService) grantPlatformKept(ctx context.Context, proxyID string,
	checked, denied []interfaces.ProxyGrantSourceSpec) bool {
	kept := map[string]interfaces.ProxyGrantSourceSpec{}
	for _, source := range checked {
		if source.PlatformKept {
			kept[proxySourceResolutionKey(source)] = source
		}
	}
	if len(kept) == 0 {
		return false
	}
	granted := false
	for _, deniedSource := range denied {
		source, ok := kept[proxySourceResolutionKey(deniedSource)]
		if !ok {
			continue
		}
		if kns.grantAsPlatform(ctx, proxyID, source) {
			granted = true
		}
	}
	return granted
}

// grantUncheckedPlatformKept has the built-in administrator grant the platform-kept sources of
// the latest model that the preflight never checked, before they are synchronized under the
// delegator, who could not vouch for them.
func (kns *knowledgeNetworkService) grantUncheckedPlatformKept(ctx context.Context, proxyID string,
	selection *proxyGrantSelection, sources []interfaces.ProxyGrantSourceSpec) {
	for _, source := range sources {
		if !source.PlatformKept {
			continue
		}
		if selection != nil {
			if _, checked := selection.checked[proxySourceResolutionKey(source)]; checked {
				continue
			}
		}
		kns.grantAsPlatform(ctx, proxyID, source)
	}
}

func (kns *knowledgeNetworkService) grantAsPlatform(ctx context.Context, proxyID string,
	source interfaces.ProxyGrantSourceSpec) bool {
	if err := kns.mpa.Grant(ctx, proxyID, interfaces.ADMIN_ACCOUNT_ID, source); err != nil {
		otellog.LogError(ctx, fmt.Sprintf("Grant platform-kept resource %s of %s %s to proxy %s failed",
			source.ResourceID, source.BindingType, source.BindingID, proxyID), err)
		return false
	}
	return true
}
