// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package knsearch (attribute operator completion)
// file: condition_operations_backfill.go
package knsearch

import (
	"context"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// backfillConditionOperations completes attribute operators for the object type to be retrieved.
//
// The Schema obtained by concept recall comes from the knowledge network export view (GET /knowledge-networks/{kn}?mode=export),
// That path only lists object types and does not enrich data sources, so there is no condition_operations in the attributes.
// And "Whether this attribute can match" is derived by bkn-backend according to the resource reality in the object type details.
// Fill in the details as needed before retrieval, so that the instance recall can have a basis for judgment; otherwise, each object type will have "no retrieval attributes".
// is skipped, semantic instance recall always returns null.
//
// Only the object types with missing operators are supplemented and merged into one batch request.
//
// It also settles SearchCapabilitiesUnknown for every object type it looks up: set when the detail
// request fails or bkn-backend reports the bound resource unreadable, cleared when the detail was
// derived from the resource, so an object type still without operators afterwards genuinely has
// nothing searchable.
func (s *localSearchImpl) backfillConditionOperations(
	ctx context.Context,
	knID string,
	objectTypes []*interfaces.KnSearchObjectType,
	indexOpsOnly bool,
) {
	if knID == "" || len(objectTypes) == 0 {
		return
	}

	pending := map[string][]*interfaces.KnSearchObjectType{}
	ids := make([]string, 0, len(objectTypes))
	for _, objType := range objectTypes {
		if objType == nil || conditionOperationsPresent(objType) {
			continue
		}
		id := strings.TrimSpace(objType.ConceptID)
		if id == "" {
			continue
		}
		if _, seen := pending[id]; !seen {
			ids = append(ids, id)
		}
		// The same id may appear multiple times (combined from different recall paths), and the completion must cover every copy.
		pending[id] = append(pending[id], objType)
	}
	if len(ids) == 0 {
		return
	}

	details, err := s.bknBackend.GetObjectTypeDetail(ctx, knID, ids, true)
	if err != nil {
		// If it cannot be filled, return to the original state: the recall may become empty, but the Schema result is still valid, and the entire schema should not fail.
		// What these object types can be searched by is now unknown, which instance recall must report rather than read as "no match".
		s.logger.WithContext(ctx).Warnf("[SemanticInstanceRetrieval] Backfill condition_operations failed for %d object types: %v", len(ids), err)
		for _, targets := range pending {
			for _, objType := range targets {
				objType.SearchCapabilitiesUnknown = true
			}
		}
		return
	}

	filled := 0
	var unavailable []string
	for _, detail := range details {
		if detail == nil {
			continue
		}
		targets := pending[strings.TrimSpace(detail.ID)]
		if len(targets) == 0 {
			continue
		}
		for _, objType := range targets {
			objType.SearchCapabilitiesUnknown = detail.DataSourceMetadataUnavailable
		}
		if detail.DataSourceMetadataUnavailable {
			unavailable = append(unavailable, detail.ID)
		}
		ops := map[string][]interfaces.KnOperationType{}
		for _, p := range detail.DataProperties {
			if p == nil || len(p.ConditionOperations) == 0 {
				continue
			}
			selected := p.ConditionOperations
			if indexOpsOnly {
				selected = indexBackedOperations(selected)
			}
			if len(selected) == 0 {
				continue
			}
			ops[p.Name] = selected
		}
		if len(ops) == 0 {
			continue
		}
		for _, objType := range targets {
			for _, p := range objType.DataProperties {
				if p == nil || len(p.ConditionOperations) > 0 {
					continue
				}
				if objType.EffectivePermissions != nil &&
					objType.EffectivePermissions[p.Name] != interfaces.PropertyAccessFull {
					continue
				}
				if o, ok := ops[p.Name]; ok {
					p.ConditionOperations = o
					filled++
				}
			}
		}
	}

	if len(unavailable) > 0 {
		s.logger.WithContext(ctx).Warnf("[SemanticInstanceRetrieval] bkn-backend could not read the data source metadata of %d object types, their search capabilities are unknown: %v",
			len(unavailable), unavailable)
	}
	s.logger.WithContext(ctx).Infof("[SemanticInstanceRetrieval] Backfilled condition_operations: object_types=%d properties=%d", len(ids), filled)
}

// conditionOperationsPresent determines whether the attributes of the object type have operators.
func conditionOperationsPresent(objType *interfaces.KnSearchObjectType) bool {
	for _, p := range objType.DataProperties {
		if p != nil && len(p.ConditionOperations) > 0 {
			return true
		}
	}
	return false
}

// trimToIndexBackedOperations converges the operators to the ones brought by the index before sending the response.
//
// Actual measurement of a retrieval of 10 object types and 172 attributes: 10,453 more bytes (+27%) for all operators, leaving only the index class.
// 151 bytes more (+0.4%), a 69x difference. Almost all the extra ones are repeated for each string attribute.
// ==/in/like and the like, those attributes type can be deduced, and the server will inform one by one that there is no information; and streamline.
// Schema is designed to save space.
//
// It is only done when a response is issued and does not affect retrieval: instance recall relies on operators to select searchable fields. Cutting them in advance will only support equivalent fields.
// The field disappears entirely. It only takes effect on the MCP side, and the REST caller still gets the full amount.
func trimToIndexBackedOperations(objectTypes []*interfaces.KnSearchObjectType, indexOpsOnly bool) {
	if !indexOpsOnly {
		return
	}
	for _, objType := range objectTypes {
		if objType == nil {
			continue
		}
		for _, p := range objType.DataProperties {
			if p == nil {
				continue
			}
			p.ConditionOperations = indexBackedOperations(p.ConditionOperations)
		}
		hoistSharedIndexOperations(objType)
	}
}

// hoistSharedIndexOperations lifts the operator list that an object type's indexed properties
// share up to the object type, instead of repeating it on each of them.
//
// After the trim above almost every indexed property of an object type carries the same short
// list, because the list now says only "this field has a full text / vector index" and a
// resource usually indexes its text columns the same way. Measured on 14.103.77.23,
// kn=supply_942_verify, query "supply order": ["match","multi_match","knn"] repeated on 52
// properties, ~2.7KB, about 15% of the whole search_schema response.
//
// A property that shares the list carries indexed=true in its place; one whose capability
// differs keeps its own condition_operations, so field level differences survive; one with no
// index carries neither key, as before. A list carried by a single property is left inline,
// since lifting it would only move the bytes.
func hoistSharedIndexOperations(objType *interfaces.KnSearchObjectType) {
	if objType == nil {
		return
	}

	counts := map[string]int{}
	sets := map[string][]interfaces.KnOperationType{}
	for _, p := range objType.DataProperties {
		if p == nil || len(p.ConditionOperations) == 0 {
			continue
		}
		key := operationsKey(p.ConditionOperations)
		counts[key]++
		sets[key] = p.ConditionOperations
	}

	shared := ""
	most := 1
	for key, n := range counts {
		// The tie break keeps one input producing one response, whatever order the map yields.
		if n > most || (n == most && shared != "" && key < shared) {
			shared, most = key, n
		}
	}
	if shared == "" {
		return
	}

	objType.IndexOperations = sets[shared]
	for _, p := range objType.DataProperties {
		if p == nil || len(p.ConditionOperations) == 0 {
			continue
		}
		if operationsKey(p.ConditionOperations) != shared {
			continue
		}
		p.ConditionOperations = nil
		p.Indexed = true
	}
}

// operationsKey identifies an operator list. indexBackedOperations emits a fixed order, so
// two properties with the same capability always produce the same key.
func operationsKey(ops []interfaces.KnOperationType) string {
	parts := make([]string, 0, len(ops))
	for _, op := range ops {
		parts = append(parts, string(op))
	}
	return strings.Join(parts, ",")
}

// indexBackedOperations selects those operators that only the server knows - they depend on whether the underlying index is built or not.
// It cannot be inferred from the attribute type. The caller of the remaining comparison operators can make their own judgment based on type.
//
// The result keeps a fixed order rather than the order bkn-backend happened to send, so that two
// properties with the same capability are recognisably the same list.
func indexBackedOperations(ops []interfaces.KnOperationType) []interfaces.KnOperationType {
	present := make(map[interfaces.KnOperationType]bool, len(ops))
	for _, op := range ops {
		present[op] = true
	}
	var out []interfaces.KnOperationType
	for _, op := range indexBackedOperationOrder {
		if present[op] {
			out = append(out, op)
		}
	}
	return out
}

// indexBackedOperationOrder is the published order of the index-derived operators.
var indexBackedOperationOrder = []interfaces.KnOperationType{
	interfaces.KnOperationTypeMatch,
	interfaces.KnOperationTypeMultiMatch,
	interfaces.KnOperationTypeKnn,
}
