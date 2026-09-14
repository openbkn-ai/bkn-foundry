// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knowledge_network

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
)

const (
	// proxySkipReasonDelegatorDenied: the delegator does not hold execute on the Skill.
	proxySkipReasonDelegatorDenied = "delegator_lacks_permission"
	// proxySkipReasonUnsupported: bkn-safe refused Skill sources as an invalid request,
	// which is what a release that predates them answers.
	proxySkipReasonUnsupported = "authorization_service_rejected_skill_sources"
)

// proxyGrantSelection records what a preflight decided about one desired source
// set: every source it checked, and the best-effort sources it left out.
type proxyGrantSelection struct {
	resolved []interfaces.ProxyGrantResolvedSource
	checked  map[string]struct{}
	skips    map[string]skippedProxyGrant
}

type skippedProxyGrant struct {
	source interfaces.ProxyGrantSourceSpec
	reason string
}

func newProxyGrantSelection(sources []interfaces.ProxyGrantSourceSpec) *proxyGrantSelection {
	selection := &proxyGrantSelection{
		checked: make(map[string]struct{}, len(sources)),
		skips:   map[string]skippedProxyGrant{},
	}
	for _, source := range sources {
		selection.checked[proxySourceResolutionKey(source)] = struct{}{}
	}
	return selection
}

func (s *proxyGrantSelection) skip(source interfaces.ProxyGrantSourceSpec, reason string) {
	s.skips[proxySourceResolutionKey(source)] = skippedProxyGrant{source: source, reason: reason}
}

func (s *proxyGrantSelection) skipped(source interfaces.ProxyGrantSourceSpec) bool {
	if s == nil {
		return false
	}
	_, ok := s.skips[proxySourceResolutionKey(source)]
	return ok
}

// needsRecheck reports whether sources hold a best-effort source this selection
// never checked. Only those need a second preflight: an unchecked data or
// execution source keeps today's behavior and is decided by Sync itself.
func (s *proxyGrantSelection) needsRecheck(sources []interfaces.ProxyGrantSourceSpec) bool {
	for _, source := range sources {
		if !interfaces.IsBestEffortProxyGrantSource(source) {
			continue
		}
		if s == nil {
			return true
		}
		if _, ok := s.checked[proxySourceResolutionKey(source)]; !ok {
			return true
		}
	}
	return false
}

// materialized returns sources without the ones this selection skipped. A
// skipped Skill that already had a grant is revoked by the full-set Sync, which
// is right: its delegator can no longer vouch for it.
func (s *proxyGrantSelection) materialized(sources []interfaces.ProxyGrantSourceSpec) []interfaces.ProxyGrantSourceSpec {
	if s == nil || len(s.skips) == 0 {
		return sources
	}
	kept := make([]interfaces.ProxyGrantSourceSpec, 0, len(sources))
	for _, source := range sources {
		if !s.skipped(source) {
			kept = append(kept, source)
		}
	}
	return kept
}

// skippedGrants lists the skipped sources in a stable order.
func (s *proxyGrantSelection) skippedGrants() []skippedProxyGrant {
	if s == nil || len(s.skips) == 0 {
		return nil
	}
	out := make([]skippedProxyGrant, 0, len(s.skips))
	for _, skipped := range s.skips {
		out = append(out, skipped)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].source.ResourceID != out[j].source.ResourceID {
			return out[i].source.ResourceID < out[j].source.ResourceID
		}
		return out[i].source.BindingID < out[j].source.BindingID
	})
	return out
}

// logSkipped writes one line per network naming every Skill grant left out, so
// an operator can see why a mounted Skill is not readable through the proxy.
func (s *proxyGrantSelection) logSkipped(ctx context.Context, knID string) {
	skipped := s.skippedGrants()
	if len(skipped) == 0 {
		return
	}
	parts := make([]string, 0, len(skipped))
	for _, grant := range skipped {
		parts = append(parts, grant.source.ResourceType+":"+grant.source.ResourceID+
			" binding="+grant.source.BindingID+" reason="+grant.reason)
	}
	logger.Warnf("[KNProxy] kn_id=%s skipped %d best-effort skill grant(s); the network stays ready "+
		"and these skills are not readable through its proxy: %s", knID, len(skipped), strings.Join(parts, "; "))
}

// splitBestEffortProxySources separates the sources a publication requires
// from the ones it may leave out.
func splitBestEffortProxySources(sources []interfaces.ProxyGrantSourceSpec) (required,
	optional []interfaces.ProxyGrantSourceSpec) {
	for _, source := range sources {
		if interfaces.IsBestEffortProxyGrantSource(source) {
			optional = append(optional, source)
			continue
		}
		required = append(required, source)
	}
	return required, optional
}

// isManagedProxyRequestRejected reports whether bkn-safe refused the request as
// invalid (400), as opposed to being unavailable.
func isManagedProxyRequestRejected(err error) bool {
	var statusErr *interfaces.ManagedProxyStatusError
	return errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusBadRequest
}

// refuseNewlyMountedSkillsWithoutGrant refuses a mount whose new Skill the
// mounter cannot vouch for. Only Skills added by this request count: a Skill
// already mounted keeps the best-effort rule, and a Skill skipped because
// bkn-safe predates Skill sources is not the mounter's to fix.
func refuseNewlyMountedSkillsWithoutGrant(ctx context.Context, selection *proxyGrantSelection,
	current, attached []*interfaces.CapabilityBinding) error {
	existing := make(map[string]struct{}, len(current))
	for _, binding := range current {
		if binding != nil {
			existing[binding.ID] = struct{}{}
		}
	}
	added := make(map[string]struct{}, len(attached))
	for _, binding := range attached {
		if binding == nil || binding.CapabilityType != interfaces.CAPABILITY_TYPE_SKILL {
			continue
		}
		if _, ok := existing[binding.ID]; !ok {
			added[binding.ID] = struct{}{}
		}
	}
	missing := make([]missingProxyPermission, 0)
	for _, skipped := range selection.skippedGrants() {
		if skipped.reason != proxySkipReasonDelegatorDenied {
			continue
		}
		if _, ok := added[skipped.source.BindingID]; !ok {
			continue
		}
		missing = append(missing, missingProxyPermission{
			ResourceType: skipped.source.ResourceType,
			ResourceID:   skipped.source.ResourceID,
			Operation:    skipped.source.Operation,
			BindingType:  skipped.source.BindingType,
			BindingID:    skipped.source.BindingID,
		})
	}
	if len(missing) == 0 {
		return nil
	}
	return rest.NewHTTPError(ctx, http.StatusForbidden, berrors.BknBackend_KnowledgeNetwork_ProxyPermissionMissing).
		WithErrorDetails(missingProxyPermissionDetails{MissingPermissions: missing})
}
