// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package authz

import (
	"context"
	"sort"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
	"gorm.io/gorm"
)

// DirectRequirements returns the catalog-declared, same-resource prerequisites
// for each requested operation. Requirements are one layer only; seed rejects
// a requirement that itself declares requirements.
func (en *Enforcer) DirectRequirements(ctx context.Context, resourceType string,
	operations []string) (map[string][]string, error) {
	if en.db == nil || len(operations) == 0 {
		return make(map[string][]string, len(operations)), nil
	}
	return directRequirements(en.db.WithContext(ctx), resourceType, operations)
}

// DirectRequirements reads prerequisites through the transaction connection.
// Mutation workflows use this form so the catalog snapshot and policy changes
// belong to the same database transaction.
func (tx *PolicyTransaction) DirectRequirements(ctx context.Context, resourceType string,
	operations []string) (map[string][]string, error) {
	return directRequirements(tx.db.WithContext(ctx), resourceType, operations)
}

func directRequirements(db *gorm.DB, resourceType string,
	operations []string) (map[string][]string, error) {
	result := make(map[string][]string, len(operations))
	var rows []model.Operation
	if err := db.
		Where("resource_type_id = ? AND id IN ?", resourceType, distinctOperations(operations)).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		result[row.ID] = splitOperationIDs(row.RequiredOperationIDs)
	}
	return result, nil
}

// NormalizeOperations adds every direct requirement while preserving the
// caller's first-seen order. It is the write-side counterpart of the mandatory
// runtime check; callers receive the exact set they should persist or return.
func (en *Enforcer) NormalizeOperations(ctx context.Context, resourceType string,
	operations []string) ([]string, error) {
	requires, err := en.DirectRequirements(ctx, resourceType, operations)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(operations)+len(requires))
	seen := make(map[string]bool, len(operations)+len(requires))
	for _, operation := range operations {
		if operation == "" || seen[operation] {
			continue
		}
		seen[operation] = true
		out = append(out, operation)
	}
	for _, operation := range operations {
		for _, required := range requires[operation] {
			if required == "" || seen[required] {
				continue
			}
			seen[required] = true
			out = append(out, required)
		}
	}
	return out, nil
}

// RequiringOperations returns the requested operations plus every operation
// that directly requires one of them. Role revocation uses this reverse view to
// keep a retained operation and its prerequisite together.
func (en *Enforcer) RequiringOperations(ctx context.Context, resourceType string,
	operations []string) ([]string, error) {
	byRequirement, err := en.RequiringOperationsByRequirement(ctx, resourceType, operations)
	if err != nil {
		return nil, err
	}
	out := distinctOperations(operations)
	seen := make(map[string]bool, len(out))
	for _, operation := range out {
		seen[operation] = true
	}
	for _, operation := range operations {
		for _, requiring := range byRequirement[operation] {
			if !seen[requiring] {
				seen[requiring] = true
				out = append(out, requiring)
			}
		}
	}
	return out, nil
}

// RequiringOperationsByRequirement returns the direct reverse dependency list
// for every requested prerequisite. It loads the catalog once so callers that
// revoke a set do not repeat the same resource-type query per operation.
func (en *Enforcer) RequiringOperationsByRequirement(ctx context.Context, resourceType string,
	operations []string) (map[string][]string, error) {
	result := make(map[string][]string, len(operations))
	if en.db == nil || len(operations) == 0 {
		return result, nil
	}
	var rows []model.Operation
	if err := en.db.WithContext(ctx).
		Where("resource_type_id = ? AND implied_operation_ids <> ''", resourceType).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	wanted := make(map[string]bool, len(operations))
	for _, operation := range operations {
		wanted[operation] = true
	}
	// Seed rejects multi-level declarations, so one pass is the complete reverse
	// set and intentionally does not turn this into a recursive dependency graph.
	for _, row := range rows {
		for _, required := range splitOperationIDs(row.RequiredOperationIDs) {
			if wanted[required] {
				result[required] = append(result[required], row.ID)
			}
		}
	}
	return result, nil
}

func (en *Enforcer) requirementsFor(ctx context.Context,
	want map[ResourceRef][]string) (map[string]map[string][]string, error) {
	byType := make(map[string][]string)
	for resource, operations := range want {
		byType[resource.Type] = append(byType[resource.Type], operations...)
	}
	result := make(map[string]map[string][]string, len(byType))
	for resourceType, operations := range byType {
		requires, err := en.DirectRequirements(ctx, resourceType, operations)
		if err != nil {
			return nil, err
		}
		result[resourceType] = requires
	}
	return result, nil
}

func expandWithRequirements(want map[ResourceRef][]string,
	requires map[string]map[string][]string) map[ResourceRef][]string {
	expanded := make(map[ResourceRef][]string, len(want))
	for resource, operations := range want {
		ops := append([]string(nil), operations...)
		seen := make(map[string]bool, len(ops))
		for _, operation := range ops {
			seen[operation] = true
		}
		for _, operation := range operations {
			for _, required := range requires[resource.Type][operation] {
				if !seen[required] {
					seen[required] = true
					ops = append(ops, required)
				}
			}
		}
		expanded[resource] = ops
	}
	return expanded
}

func splitOperationIDs(value string) []string {
	if value == "" {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, operation := range strings.Split(value, ",") {
		operation = strings.TrimSpace(operation)
		if operation == "" || seen[operation] {
			continue
		}
		seen[operation] = true
		out = append(out, operation)
	}
	return out
}

func distinctOperations(operations []string) []string {
	seen := make(map[string]bool, len(operations))
	out := make([]string, 0, len(operations))
	for _, operation := range operations {
		if operation == "" || seen[operation] {
			continue
		}
		seen[operation] = true
		out = append(out, operation)
	}
	sort.Strings(out)
	return out
}
