// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package authz

import (
	"context"
	"sort"
	"strings"

	safemodel "github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

type proxyPermission struct {
	ResourceType string
	ResourceID   string
	Operation    string
}

// currentProxyPermissions resolves all currently effective source-backed
// permissions for one managed proxy with a bounded number of database reads.
// Delegator policy and hierarchy checks are grouped by delegator instead of by
// source, which keeps list filtering proportional to the number of delegators.
func (en *Enforcer) currentProxyPermissions(ctx context.Context, proxyID string) (map[proxyPermission]bool, error) {
	sources, valid, err := en.currentProxySourceIDs(ctx, proxyID)
	if err != nil {
		return nil, err
	}
	permissions := make(map[proxyPermission]bool, len(valid))
	for _, source := range sources {
		if !valid[source.ID] {
			continue
		}
		permissions[proxyPermission{
			ResourceType: source.ResourceType,
			ResourceID:   source.ResourceID,
			Operation:    source.Operation,
		}] = true
	}
	return permissions, nil
}

// currentProxySourceIDs returns the active source rows and the subset whose
// lifecycle and delegator authority are still valid.
func (en *Enforcer) currentProxySourceIDs(ctx context.Context, proxyID string) ([]safemodel.ProxyGrantSource, map[string]bool, error) {
	var sources []safemodel.ProxyGrantSource
	if err := en.db.WithContext(ctx).Where("proxy_account_id = ? AND lifecycle_status = ?",
		proxyID, safemodel.ProxyGrantSourceStatusActive).Find(&sources).Error; err != nil {
		return nil, nil, err
	}
	valid, err := en.validProxySourceIDs(ctx, sources)
	return sources, valid, err
}

// CurrentProxySourceIDs exposes the batched validity projection to provenance
// services already running inside the same authorization transaction.
func (tx *PolicyTransaction) CurrentProxySourceIDs(proxyID string) (map[string]bool, error) {
	_, valid, err := tx.enforcer.currentProxySourceIDs(context.Background(), proxyID)
	return valid, err
}

// FilterResourceOpsRaw evaluates a non-managed delegator without applying
// managed-proxy provenance. Callers must validate that the accessor is an
// enabled, non-managed identity before using it.
func (tx *PolicyTransaction) FilterResourceOpsRaw(accessorID string, resources []ResourceRef,
	candidates []string) ([]FilteredResource, error) {
	return tx.enforcer.filterResourceOps(context.Background(), accessorID, resources, nil, candidates, ScopeEffective, false)
}

func (en *Enforcer) validProxySourceIDs(ctx context.Context, sources []safemodel.ProxyGrantSource) (map[string]bool, error) {
	valid := make(map[string]bool, len(sources))
	byDelegator := map[string][]safemodel.ProxyGrantSource{}
	resourceTypes := map[string]bool{}
	operations := map[string]bool{}
	delegators := map[string]bool{}
	for _, source := range sources {
		switch source.SourceType {
		case safemodel.ProxyGrantSourceTypeManual, safemodel.ProxyGrantSourceTypeAdmin:
			valid[source.ID] = true
		case safemodel.ProxyGrantSourceTypeKNBinding:
			delegator := strings.TrimSpace(source.GrantedBy)
			if delegator == "" {
				continue
			}
			byDelegator[delegator] = append(byDelegator[delegator], source)
			resourceTypes[source.ResourceType] = true
			operations[source.Operation] = true
			delegators[delegator] = true
		}
	}
	if len(byDelegator) == 0 {
		return valid, nil
	}

	type operationKey struct {
		ResourceType string
		Operation    string
	}
	var registeredRows []safemodel.Operation
	if err := en.db.WithContext(ctx).Where("resource_type_id IN ? AND id IN ?", sortedKeys(resourceTypes), sortedKeys(operations)).
		Find(&registeredRows).Error; err != nil {
		return nil, err
	}
	registered := make(map[operationKey]bool, len(registeredRows))
	for _, operation := range registeredRows {
		registered[operationKey{ResourceType: operation.ResourceTypeID, Operation: operation.ID}] = true
	}

	delegatorIDs := sortedKeys(delegators)
	var users []safemodel.User
	if err := en.db.WithContext(ctx).Where("id IN ? AND enabled = ?", delegatorIDs, true).Find(&users).Error; err != nil {
		return nil, err
	}
	enabled := make(map[string]bool, len(users))
	for _, user := range users {
		enabled[user.ID] = true
	}
	var managed []safemodel.ManagedProxyAccount
	if err := en.db.WithContext(ctx).Where("proxy_account_id IN ?", delegatorIDs).Find(&managed).Error; err != nil {
		return nil, err
	}
	for _, account := range managed {
		delete(enabled, account.ProxyAccountID)
	}

	for delegator, delegatedSources := range byDelegator {
		if !enabled[delegator] {
			continue
		}
		resourceSet := map[ResourceRef]bool{}
		candidateSet := map[string]bool{}
		for _, source := range delegatedSources {
			if !registered[operationKey{ResourceType: source.ResourceType, Operation: source.Operation}] {
				continue
			}
			resourceSet[ResourceRef{Type: source.ResourceType, ID: source.ResourceID}] = true
			candidateSet[source.Operation] = true
		}
		resources := sortedResourceRefs(resourceSet)
		if len(resources) == 0 {
			continue
		}
		allowed, err := en.filterResourceOps(ctx, delegator, resources, nil,
			sortedKeys(candidateSet), ScopeEffective, false)
		if err != nil {
			return nil, err
		}
		allowedSet := map[proxyPermission]bool{}
		for _, resource := range allowed {
			for _, operation := range resource.Operations {
				allowedSet[proxyPermission{
					ResourceType: resource.Type,
					ResourceID:   resource.ID,
					Operation:    operation,
				}] = true
			}
		}
		for _, source := range delegatedSources {
			if allowedSet[proxyPermission{source.ResourceType, source.ResourceID, source.Operation}] {
				valid[source.ID] = true
			}
		}
	}
	return valid, nil
}

func sortedKeys(values map[string]bool) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func sortedResourceRefs(values map[ResourceRef]bool) []ResourceRef {
	out := make([]ResourceRef, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		return out[i].ID < out[j].ID
	})
	return out
}
