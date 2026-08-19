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
		s.logger.WithContext(ctx).Warnf("[SemanticInstanceRetrieval] Backfill condition_operations failed for %d object types: %v", len(ids), err)
		return
	}

	filled := 0
	for _, detail := range details {
		if detail == nil {
			continue
		}
		targets := pending[strings.TrimSpace(detail.ID)]
		if len(targets) == 0 {
			continue
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
				if o, ok := ops[p.Name]; ok {
					p.ConditionOperations = o
					filled++
				}
			}
		}
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
	}
}

// indexBackedOperations selects those operators that only the server knows - they depend on whether the underlying index is built or not.
// It cannot be inferred from the attribute type. The caller of the remaining comparison operators can make their own judgment based on type.
func indexBackedOperations(ops []interfaces.KnOperationType) []interfaces.KnOperationType {
	var out []interfaces.KnOperationType
	for _, op := range ops {
		switch op {
		case interfaces.KnOperationTypeMatch, interfaces.KnOperationTypeMultiMatch, interfaces.KnOperationTypeKnn:
			out = append(out, op)
		}
	}
	return out
}
