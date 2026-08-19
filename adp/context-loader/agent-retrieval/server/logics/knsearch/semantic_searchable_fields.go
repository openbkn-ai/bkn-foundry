// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package knsearch (semantically searchable field filtering)
// file: semantic_searchable_fields.go
package knsearch

import (
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// searchableField Field information that can participate in semantic retrieval.
type searchableField struct {
	Name          string
	HasKnn        bool
	HasExactMatch bool
	HasMatch      bool
}

var semanticOps = map[interfaces.KnOperationType]struct{}{
	interfaces.KnOperationTypeEqual: {},
	interfaces.KnOperationTypeMatch: {},
	interfaces.KnOperationTypeKnn:   {},
}

// isTextField determines whether the attribute is text type.
func isTextField(prop *interfaces.KnSearchDataProperty) bool {
	if prop == nil {
		return false
	}
	t := strings.TrimSpace(strings.ToLower(prop.Type))
	if t == "" {
		return false
	}
	textTypes := []string{"text", "string", "varchar", "char"}
	for _, tt := range textTypes {
		if t == tt || strings.HasPrefix(t, tt+"[") {
			return true
		}
	}
	return false
}

// findSemanticSearchableFields Filters semantically searchable fields from an object type.
//
// The basis is the condition_operations of the attribute: whether the field can be matched / knn by bkn-backend by resource.
// The actually built index is derived and published here. The retrieval layer only consumes it and does not ask the physical layer itself.
func findSemanticSearchableFields(objType *interfaces.KnSearchObjectType) []searchableField {
	if objType == nil || len(objType.DataProperties) == 0 {
		return nil
	}
	var out []searchableField
	for _, p := range objType.DataProperties {
		if p == nil {
			continue
		}
		name := strings.TrimSpace(p.Name)
		if name == "" {
			continue
		}
		if !isTextField(p) {
			continue
		}
		ops := p.ConditionOperations
		if len(ops) == 0 {
			continue
		}
		var hasExact, hasMatch, hasKnn bool
		for _, op := range ops {
			if _, ok := semanticOps[op]; !ok {
				continue
			}
			switch op {
			case interfaces.KnOperationTypeEqual:
				hasExact = true
			case interfaces.KnOperationTypeMatch:
				hasMatch = true
			case interfaces.KnOperationTypeKnn:
				hasKnn = true
			case interfaces.KnOperationTypeAnd, interfaces.KnOperationTypeOr,
				interfaces.KnOperationTypeNotEqual, interfaces.KnOperationTypeGreater, interfaces.KnOperationTypeLess,
				interfaces.KnOperationTypeGreaterOrEqual, interfaces.KnOperationTypeLessOrEqual,
				interfaces.KnOperationTypeIn, interfaces.KnOperationTypeNotIn,
				interfaces.KnOperationTypeLike, interfaces.KnOperationTypeNotLike,
				interfaces.KnOperationTypeRange, interfaces.KnOperationTypeOutRange,
				interfaces.KnOperationTypeExist, interfaces.KnOperationTypeNotExist,
				interfaces.KnOperationTypeRegex:
				// Non-semantic retrieval related operation types, do not set hasExact/hasMatch/hasKnn.
			}
		}
		if !hasExact && !hasMatch && !hasKnn {
			continue
		}
		out = append(out, searchableField{
			Name:          name,
			HasKnn:        hasKnn,
			HasExactMatch: hasExact,
			HasMatch:      hasMatch,
		})
	}
	return out
}
