// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package objectpermission applies ontology-query's effective property plan to
// Context Loader schema models. Physical resource tools intentionally do not
// use this package: their authorization boundary is Vega resource permission.
package objectpermission

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// schemaFanOut bounds how many object-type schema reads run at once.
//
// The property plan is per object type and ontology-query publishes no batch
// route, so a network's worth of them is a fan-out either way; the only question
// was whether it ran serially. It did, at roughly 28ms a read, which a
// 1000-object network paid one read at a time -- 28 seconds, past every caller's
// timeout (#1877). Eight at a time keeps the wall clock proportional to the
// network without turning one get_kn_detail into a thousand simultaneous
// requests against the service that has to answer them.
const schemaFanOut = 8

// FilterObjectTypes clones and filters object types using the authorization-safe
// schema result from ontology-query. A missing or unknown decision is denied.
//
// The reads run concurrently but the answer does not depend on how they
// interleave: each object type is written back to its own slot, so the result
// keeps the caller's order, and the failure reported is the first one in that
// same order rather than the first to arrive.
func FilterObjectTypes(ctx context.Context, access interfaces.ObjectSchemaAccess, knID string,
	objectTypes []*interfaces.ObjectType) ([]*interfaces.ObjectType, error) {
	if access == nil {
		return objectTypes, nil
	}

	// Cancelled as soon as one read fails: the answer is already lost, so the
	// reads still in flight are load on a dependency for a result nobody reads.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	filtered := make([]*interfaces.ObjectType, len(objectTypes))
	var mu sync.Mutex
	failure := struct {
		index int
		err   error
	}{index: -1}
	record := func(i int, err error) {
		mu.Lock()
		defer mu.Unlock()
		// Once one read has failed the rest are cancelled, and that cancellation is
		// our own doing -- recording it would bury the failure that caused it.
		if failure.err != nil && errors.Is(err, context.Canceled) {
			return
		}
		if failure.err == nil || i < failure.index {
			failure.index, failure.err = i, err
		}
	}

	var wg sync.WaitGroup
	tokens := make(chan struct{}, schemaFanOut)
queue:
	for i, objectType := range objectTypes {
		if objectType == nil {
			continue
		}
		// ontology-query can only build a property plan for a published resource
		// binding. Keep an unbound object discoverable, but expose no properties.
		if objectType.DataSource == nil || strings.TrimSpace(objectType.DataSource.ID) == "" ||
			(strings.TrimSpace(objectType.DataSource.Type) != "" && objectType.DataSource.Type != "resource") {
			filtered[i] = filterObjectType(objectType, nil)
			continue
		}
		select {
		case tokens <- struct{}{}:
		case <-ctx.Done():
			// A read has already failed, or the caller left. Either way this answer
			// is lost, so stop queueing reads someone would still have to serve.
			break queue
		}
		wg.Add(1)
		go func(i int, objectType *interfaces.ObjectType) {
			defer wg.Done()
			defer func() { <-tokens }()
			schema, err := access.GetObjectTypeSchema(ctx, knID, objectType.ID)
			switch {
			case err != nil:
				record(i, err)
			case schema == nil:
				record(i, fmt.Errorf("ontology-query returned an empty schema for object type %q", objectType.ID))
			default:
				filtered[i] = filterObjectType(objectType, schema.EffectivePermissions)
				return
			}
			cancel()
		}(i, objectType)
	}
	wg.Wait()

	if failure.err != nil {
		return nil, failure.err
	}
	// No read failed, so an object type still missing means the caller's context
	// ended mid-fan-out. Saying so beats handing back a network whose model is
	// silently short a few concepts.
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	result := make([]*interfaces.ObjectType, 0, len(objectTypes))
	for _, objectType := range filtered {
		if objectType != nil {
			result = append(result, objectType)
		}
	}
	return result, nil
}

func filterObjectType(source *interfaces.ObjectType,
	permissions map[string]interfaces.PropertyAccessLevel) *interfaces.ObjectType {
	filtered := *source
	filtered.EffectivePermissions = clonePermissions(permissions)
	filtered.DataProperties = make([]*interfaces.DataProperty, 0, len(source.DataProperties))
	for _, property := range source.DataProperties {
		if property == nil || !schemaVisible(permissions[property.Name]) {
			continue
		}
		copy := *property
		if permissions[property.Name] != interfaces.PropertyAccessFull {
			copy.ConditionOperations = nil
		}
		filtered.DataProperties = append(filtered.DataProperties, &copy)
	}
	filtered.PrimaryKeys = make([]string, 0, len(source.PrimaryKeys))
	for _, name := range source.PrimaryKeys {
		if schemaVisible(permissions[name]) {
			filtered.PrimaryKeys = append(filtered.PrimaryKeys, name)
		}
	}
	filtered.LogicProperties = make([]*interfaces.LogicPropertyDef, 0, len(source.LogicProperties))
	for _, property := range source.LogicProperties {
		if property != nil && logicPropertyVisible(property.Parameters, permissions) {
			copy := *property
			filtered.LogicProperties = append(filtered.LogicProperties, &copy)
		}
	}
	return &filtered
}

func schemaVisible(level interfaces.PropertyAccessLevel) bool {
	switch level {
	case interfaces.PropertyAccessSchema, interfaces.PropertyAccessMasked, interfaces.PropertyAccessFull:
		return true
	default:
		return false
	}
}

func logicPropertyVisible(parameters []interfaces.PropertyParameter,
	permissions map[string]interfaces.PropertyAccessLevel) bool {
	for _, parameter := range parameters {
		if parameter.ValueFrom != "property" {
			continue
		}
		name, ok := parameter.Value.(string)
		if !ok || name == "" || permissions[name] != interfaces.PropertyAccessFull {
			return false
		}
	}
	return true
}

func clonePermissions(source map[string]interfaces.PropertyAccessLevel) map[string]interfaces.PropertyAccessLevel {
	if len(source) == 0 {
		return nil
	}
	result := make(map[string]interfaces.PropertyAccessLevel, len(source))
	for name, level := range source {
		if schemaVisible(level) {
			result[name] = level
		}
	}
	return result
}

// TrimObjectTypesToIndexBackedOps keeps only operators whose availability
// cannot be inferred from the property type. REST and MCP both call this helper
// so their advertised schema stays byte-for-byte equivalent.
func TrimObjectTypesToIndexBackedOps(objectTypes []*interfaces.ObjectType) {
	for _, objectType := range objectTypes {
		if objectType == nil {
			continue
		}
		for _, property := range objectType.DataProperties {
			if property == nil {
				continue
			}
			var filtered []interfaces.KnOperationType
			for _, operation := range property.ConditionOperations {
				switch operation {
				case interfaces.KnOperationTypeMatch, interfaces.KnOperationTypeMultiMatch, interfaces.KnOperationTypeKnn:
					filtered = append(filtered, operation)
				}
			}
			property.ConditionOperations = filtered
		}
	}
}
