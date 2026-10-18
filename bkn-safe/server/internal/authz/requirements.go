// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package authz

import (
	"context"
	"sort"
	"strings"
)

// DirectRequirements returns the catalog-declared, same-resource prerequisites.
// Use Requirements when validating or normalizing a complete dependency chain.
func (en *Enforcer) DirectRequirements(ctx context.Context, resourceType string,
	operations []string) (map[string][]string, error) {
	if en.db == nil || len(operations) == 0 {
		return make(map[string][]string, len(operations)), nil
	}
	return en.Registry().requirements(ctx, resourceType, operations, false, false)
}

// DirectRequirements uses the same immutable registry as the live enforcer.
func (tx *PolicyTransaction) DirectRequirements(ctx context.Context, resourceType string,
	operations []string) (map[string][]string, error) {
	return tx.Registry().requirements(ctx, resourceType, operations, false, false)
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
// transitive dependencies. Startup precomputes the closure once.
func (en *Enforcer) Requirements(ctx context.Context, resourceType string, operations []string) (map[string][]string, error) {
	if en.db == nil || len(operations) == 0 {
		return make(map[string][]string), nil
	}
	return en.Registry().requirements(ctx, resourceType, operations, true, false)
}

func (tx *PolicyTransaction) Requirements(ctx context.Context, resourceType string, operations []string) (map[string][]string, error) {
	return tx.Registry().requirements(ctx, resourceType, operations, true, false)
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
// for every requested prerequisite from the shared registry snapshot.
func (en *Enforcer) RequiringOperationsByRequirement(ctx context.Context, resourceType string,
	operations []string) (map[string][]string, error) {
	if en.db == nil || len(operations) == 0 {
		return make(map[string][]string, len(operations)), nil
	}
	return en.Registry().requirements(ctx, resourceType, operations, true, true)
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
