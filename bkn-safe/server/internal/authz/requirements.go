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

// DirectRequirements returns the catalog-declared, same-resource prerequisites.
// Use Requirements when validating or normalizing a complete dependency chain.
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

// ExpandRequirements follows the requested operation's dependencies in a registry
// already validated by seed. Seen operations are processed once, including shared dependencies.
func ExpandRequirements(requirements map[string][]string, operation string) []string {
	seen := map[string]bool{operation: true}
	queue := []string{operation}
	for i := 0; i < len(queue); i++ {
		for _, required := range requirements[queue[i]] {
			if !seen[required] {
				seen[required] = true
				queue = append(queue, required)
			}
		}
	}
	return queue[1:]
}

// Requirements returns all prerequisites for each requested operation, including
// transitive dependencies. Only the requested operations and their dependencies are read.
func (en *Enforcer) Requirements(ctx context.Context, resourceType string, operations []string) (map[string][]string, error) {
	if en.db == nil || len(operations) == 0 {
		return make(map[string][]string), nil
	}
	return requirementsFor(en.db.WithContext(ctx), resourceType, operations)
}

func (tx *PolicyTransaction) Requirements(ctx context.Context, resourceType string, operations []string) (map[string][]string, error) {
	return requirementsFor(tx.db.WithContext(ctx), resourceType, operations)
}

func requirementsFor(db *gorm.DB, resourceType string, operations []string) (map[string][]string, error) {
	dependencies := make(map[string][]string)
	pending := distinctOperations(operations)
	for len(pending) > 0 {
		direct, err := directRequirements(db, resourceType, pending)
		if err != nil {
			return nil, err
		}
		for _, operation := range pending {
			dependencies[operation] = direct[operation]
		}
		next := make(map[string]bool)
		for _, operation := range pending {
			for _, required := range dependencies[operation] {
				if _, loaded := dependencies[required]; !loaded {
					next[required] = true
				}
			}
		}
		pending = pending[:0]
		for required := range next {
			pending = append(pending, required)
		}
		sort.Strings(pending)
	}
	result := make(map[string][]string, len(operations))
	for _, operation := range operations {
		result[operation] = ExpandRequirements(dependencies, operation)
	}
	return result, nil
}

// NormalizeOperations adds every transitive requirement while preserving the
// caller's first-seen order. Callers use the result to persist an authorization
// set that satisfies the declared write-time constraint.
func (en *Enforcer) NormalizeOperations(ctx context.Context, resourceType string,
	operations []string) ([]string, error) {
	requires, err := en.Requirements(ctx, resourceType, operations)
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
// that transitively requires one of them. Role revocation uses this reverse view to
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

// RequiringOperationsByRequirement returns the transitive reverse dependency list
// for every requested prerequisite. It loads the catalog once so callers that
// revoke a set do not repeat the same resource-type query per operation.
func (en *Enforcer) RequiringOperationsByRequirement(ctx context.Context, resourceType string,
	operations []string) (map[string][]string, error) {
	if en.db == nil || len(operations) == 0 {
		return make(map[string][]string, len(operations)), nil
	}
	return requiringOperationsByRequirement(en.db.WithContext(ctx), resourceType, operations)
}

func requiringOperationsByRequirement(db *gorm.DB, resourceType string,
	operations []string) (map[string][]string, error) {
	result := make(map[string][]string, len(operations))
	var rows []model.Operation
	if err := db.Where("resource_type_id = ? AND implied_operation_ids <> ''", resourceType).Order("id").Find(&rows).Error; err != nil {
		return nil, err
	}
	reverse := make(map[string][]string)
	for _, row := range rows {
		for _, required := range splitOperationIDs(row.RequiredOperationIDs) {
			reverse[required] = append(reverse[required], row.ID)
		}
	}
	for _, operation := range operations {
		if requiring := ExpandRequirements(reverse, operation); len(requiring) > 0 {
			sort.Strings(requiring)
			result[operation] = requiring
		}
	}
	return result, nil
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
