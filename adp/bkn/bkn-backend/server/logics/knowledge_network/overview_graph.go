// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package knowledge_network

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
	"bkn-backend/logics/permission"
)

type overviewGraphCursor struct {
	Offset   int    `json:"offset"`
	Snapshot string `json:"snapshot"`
}

func overviewVisibleChildIDs(knID string, scope interfaces.PermissionResourceScope) []string {
	if scope.Unrestricted {
		return nil
	}
	ids := make([]string, 0, len(scope.ResourceIDs))
	seen := make(map[string]struct{}, len(scope.ResourceIDs))
	for _, resourceID := range scope.ResourceIDs {
		childID, ok := interfaces.KNChildIDFromResourceID(knID, resourceID)
		if !ok {
			continue
		}
		if _, exists := seen[childID]; exists {
			continue
		}
		seen[childID] = struct{}{}
		ids = append(ids, childID)
	}
	return ids
}

func (kns *knowledgeNetworkService) resolveOverviewObjectScope(ctx context.Context, knID, branch string,
	scope interfaces.PermissionResourceScope) (interfaces.PermissionResourceScope, error) {
	if !scope.RequiresCandidateFilter {
		return scope, nil
	}
	items, err := kns.ota.ListObjectTypeSummaries(ctx, nil, interfaces.ObjectTypesQueryParams{
		KNID: knID, Branch: branch,
		PaginationQueryParameters: interfaces.PaginationQueryParameters{Limit: -1},
	})
	if err != nil {
		return scope, err
	}
	childIDs := make([]string, 0, len(items))
	for _, item := range items {
		childIDs = append(childIDs, item.OTID)
	}
	operations, err := permission.FilterKNChildResourceIDsWithOperations(ctx, kns.ps,
		interfaces.RESOURCE_TYPE_OBJECT_TYPE, interfaces.KNChildResourceIDs(knID, childIDs),
		interfaces.OPERATION_TYPE_VIEW_DETAIL)
	if err != nil {
		return scope, err
	}
	visible := make([]string, 0, len(operations))
	for _, childID := range childIDs {
		resourceID := interfaces.KNChildResourceID(knID, childID)
		if _, ok := operations[resourceID]; ok {
			visible = append(visible, resourceID)
		}
	}
	return interfaces.PermissionResourceScope{ResourceIDs: visible}, nil
}

func (kns *knowledgeNetworkService) resolveOverviewRelationScope(ctx context.Context, knID, branch string,
	scope interfaces.PermissionResourceScope) (interfaces.PermissionResourceScope, error) {
	if !scope.RequiresCandidateFilter {
		return scope, nil
	}
	items, err := kns.rta.ListRelationTypeSummaries(ctx, interfaces.RelationTypesQueryParams{
		KNID: knID, Branch: branch,
		PaginationQueryParameters: interfaces.PaginationQueryParameters{Limit: -1},
	})
	if err != nil {
		return scope, err
	}
	childIDs := make([]string, 0, len(items))
	for _, item := range items {
		childIDs = append(childIDs, item.RTID)
	}
	operations, err := permission.FilterKNChildResourceIDsWithOperations(ctx, kns.ps,
		interfaces.RESOURCE_TYPE_RELATION_TYPE, interfaces.KNChildResourceIDs(knID, childIDs),
		interfaces.OPERATION_TYPE_VIEW_DETAIL)
	if err != nil {
		return scope, err
	}
	visible := make([]string, 0, len(operations))
	for _, childID := range childIDs {
		resourceID := interfaces.KNChildResourceID(knID, childID)
		if _, ok := operations[resourceID]; ok {
			visible = append(visible, resourceID)
		}
	}
	return interfaces.PermissionResourceScope{ResourceIDs: visible}, nil
}

func overviewSnapshot(knID, branch string, updateTime int64, objectTotal, relationTotal int) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%d:%d:%d", knID, branch, updateTime,
		objectTotal, relationTotal)))
	return base64.RawURLEncoding.EncodeToString(digest[:12])
}

func encodeOverviewCursor(cursor overviewGraphCursor) string {
	payload, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(payload)
}

func decodeOverviewCursor(value string) (overviewGraphCursor, error) {
	var cursor overviewGraphCursor
	payload, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return cursor, err
	}
	if err := json.Unmarshal(payload, &cursor); err != nil || cursor.Offset < 0 || cursor.Snapshot == "" {
		return overviewGraphCursor{}, fmt.Errorf("invalid overview graph cursor")
	}
	return cursor, nil
}

func overviewIDSet(ids []string) map[string]struct{} {
	set := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	return set
}

func intersectOverviewIDs(ids []string, allowed []string, unrestricted bool) []string {
	if unrestricted {
		return ids
	}
	allowedSet := overviewIDSet(allowed)
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, ok := allowedSet[id]; ok {
			result = append(result, id)
		}
	}
	return result
}

func overviewNodes(items []*interfaces.ObjectType, edges []interfaces.OverviewGraphEdge) []interfaces.OverviewGraphNode {
	degree := make(map[string]int, len(items))
	for _, edge := range edges {
		degree[edge.SourceID]++
		degree[edge.TargetID]++
	}
	nodes := make([]interfaces.OverviewGraphNode, 0, len(items))
	for _, item := range items {
		indexed := item.Status != nil && item.Status.IndexAvailable
		nodes = append(nodes, interfaces.OverviewGraphNode{
			ID: item.OTID, Name: item.OTName, Icon: item.Icon, Color: item.Color,
			Indexed: indexed, Degree: degree[item.OTID],
		})
	}
	return nodes
}

func overviewEdges(items []*interfaces.RelationType, nodeIDs map[string]struct{}) []interfaces.OverviewGraphEdge {
	edges := make([]interfaces.OverviewGraphEdge, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		if _, ok := nodeIDs[item.SourceObjectTypeID]; !ok {
			continue
		}
		if _, ok := nodeIDs[item.TargetObjectTypeID]; !ok {
			continue
		}
		if _, ok := seen[item.RTID]; ok {
			continue
		}
		seen[item.RTID] = struct{}{}
		edges = append(edges, interfaces.OverviewGraphEdge{
			ID: item.RTID, Name: item.RTName, SourceID: item.SourceObjectTypeID,
			TargetID: item.TargetObjectTypeID, MappingMode: item.Type,
		})
	}
	return edges
}

func (kns *knowledgeNetworkService) overviewRelationQuery(knID string, query interfaces.OverviewGraphQuery,
	visibleRelationIDs, visibleObjectIDs []string, objectScopeUnrestricted bool) interfaces.RelationTypesQueryParams {
	relationQuery := interfaces.RelationTypesQueryParams{
		KNID: knID, Branch: query.Branch, RTIDS: visibleRelationIDs,
		PaginationQueryParameters: interfaces.PaginationQueryParameters{
			Limit: query.EdgeLimit, Sort: "f_name", Direction: interfaces.ASC_DIRECTION,
		},
	}
	if objectScopeUnrestricted {
		return relationQuery
	}
	relationQuery.SourceObjectTypeIDs = visibleObjectIDs
	relationQuery.TargetObjectTypeIDs = visibleObjectIDs
	return relationQuery
}

func (kns *knowledgeNetworkService) listOverviewEdges(ctx context.Context,
	base interfaces.RelationTypesQueryParams, nodeIDs []string) ([]*interfaces.RelationType, error) {
	if len(nodeIDs) == 0 {
		return []*interfaces.RelationType{}, nil
	}
	base.SourceObjectTypeIDs = nodeIDs
	base.TargetObjectTypeIDs = nodeIDs
	return kns.rta.ListRelationTypeSummaries(ctx, base)
}

func (kns *knowledgeNetworkService) listOverviewFocus(ctx context.Context, knID string,
	query interfaces.OverviewGraphQuery, baseRelationQuery interfaces.RelationTypesQueryParams,
	visibleObjectIDs []string, objectScopeUnrestricted bool) ([]*interfaces.ObjectType, []*interfaces.RelationType, error) {
	allowed := overviewIDSet(visibleObjectIDs)
	if !objectScopeUnrestricted {
		if _, ok := allowed[query.FocusObjectTypeID]; !ok {
			return nil, nil, rest.NewHTTPError(ctx, http.StatusNotFound,
				berrors.BknBackend_ObjectType_ObjectTypeNotFound)
		}
	}
	selected := map[string]struct{}{query.FocusObjectTypeID: {}}
	frontier := []string{query.FocusObjectTypeID}
	edgeByID := map[string]*interfaces.RelationType{}
	for depth := 0; depth < query.ExpandDepth && len(frontier) > 0 && len(selected) < query.NodeLimit; depth++ {
		relationQuery := baseRelationQuery
		relationQuery.BoundObjectTypeIDs = frontier
		relations, err := kns.rta.ListRelationTypeSummaries(ctx, relationQuery)
		if err != nil {
			return nil, nil, err
		}
		next := make([]string, 0)
		for _, relation := range relations {
			if len(edgeByID) >= query.EdgeLimit {
				break
			}
			for _, id := range []string{relation.SourceObjectTypeID, relation.TargetObjectTypeID} {
				if !objectScopeUnrestricted {
					if _, ok := allowed[id]; !ok {
						continue
					}
				}
				if _, exists := selected[id]; exists || len(selected) >= query.NodeLimit {
					continue
				}
				selected[id] = struct{}{}
				next = append(next, id)
			}
			edgeByID[relation.RTID] = relation
		}
		if len(edgeByID) >= query.EdgeLimit {
			break
		}
		frontier = next
	}
	ids := make([]string, 0, len(selected))
	for id := range selected {
		ids = append(ids, id)
	}
	nodes, err := kns.ota.ListObjectTypeSummaries(ctx, nil, interfaces.ObjectTypesQueryParams{
		KNID: knID, Branch: query.Branch, OTIDS: ids,
		PaginationQueryParameters: interfaces.PaginationQueryParameters{Limit: -1, Sort: "f_name", Direction: interfaces.ASC_DIRECTION},
	})
	if err != nil {
		return nil, nil, err
	}
	edges := make([]*interfaces.RelationType, 0, len(edgeByID))
	for _, edge := range edgeByID {
		if _, sourceOK := selected[edge.SourceObjectTypeID]; !sourceOK {
			continue
		}
		if _, targetOK := selected[edge.TargetObjectTypeID]; targetOK {
			edges = append(edges, edge)
		}
	}
	return nodes, edges, nil
}

// ListOverviewGraph returns a bounded, authorization-safe graph projection.
func (kns *knowledgeNetworkService) ListOverviewGraph(ctx context.Context, knID string,
	query interfaces.OverviewGraphQuery) (*interfaces.OverviewGraph, error) {
	kn, err := kns.kna.GetKNByID(ctx, knID, query.Branch)
	if err != nil || kn == nil {
		return nil, rest.NewHTTPError(ctx, http.StatusNotFound, berrors.BknBackend_KnowledgeNetwork_NotFound)
	}
	objectScope, err := kns.ps.ListAccessibleResources(ctx, interfaces.RESOURCE_TYPE_OBJECT_TYPE,
		interfaces.OPERATION_TYPE_VIEW_DETAIL)
	if err != nil {
		return nil, err
	}
	relationScope, err := kns.ps.ListAccessibleResources(ctx, interfaces.RESOURCE_TYPE_RELATION_TYPE,
		interfaces.OPERATION_TYPE_VIEW_DETAIL)
	if err != nil {
		return nil, err
	}
	objectScope, err = kns.resolveOverviewObjectScope(ctx, knID, query.Branch, objectScope)
	if err != nil {
		return nil, err
	}
	relationScope, err = kns.resolveOverviewRelationScope(ctx, knID, query.Branch, relationScope)
	if err != nil {
		return nil, err
	}
	visibleObjectIDs := overviewVisibleChildIDs(knID, objectScope)
	visibleRelationIDs := overviewVisibleChildIDs(knID, relationScope)
	objectQuery := interfaces.ObjectTypesQueryParams{
		KNID: knID, Branch: query.Branch, OTIDS: visibleObjectIDs,
		PaginationQueryParameters: interfaces.PaginationQueryParameters{
			Limit: query.NodeLimit, Sort: "f_name", Direction: interfaces.ASC_DIRECTION,
		},
	}
	objectTotal, err := kns.ota.GetObjectTypesTotal(ctx, objectQuery)
	if err != nil {
		return nil, err
	}
	if objectTotal == 0 {
		snapshot := overviewSnapshot(knID, query.Branch, kn.UpdateTime, 0, 0)
		return &interfaces.OverviewGraph{Nodes: []interfaces.OverviewGraphNode{}, Edges: []interfaces.OverviewGraphEdge{}, Snapshot: snapshot}, nil
	}

	relationQuery := kns.overviewRelationQuery(knID, query, visibleRelationIDs, visibleObjectIDs,
		objectScope.Unrestricted)
	relationTotal, err := kns.rta.GetRelationTypesTotal(ctx, relationQuery)
	if err != nil {
		return nil, err
	}
	snapshot := overviewSnapshot(knID, query.Branch, kn.UpdateTime, objectTotal, relationTotal)
	offset := 0
	if query.Cursor != "" {
		cursor, cursorErr := decodeOverviewCursor(query.Cursor)
		if cursorErr != nil || cursor.Snapshot != snapshot {
			return nil, rest.NewHTTPError(ctx, http.StatusBadRequest,
				berrors.BknBackend_KnowledgeNetwork_InvalidParameter).WithErrorDetails("overview graph cursor is invalid or stale")
		}
		offset = cursor.Offset
	}

	var objectTypes []*interfaces.ObjectType
	var relationTypes []*interfaces.RelationType
	switch {
	case query.FocusObjectTypeID != "":
		objectTypes, relationTypes, err = kns.listOverviewFocus(ctx, knID, query, relationQuery,
			visibleObjectIDs, objectScope.Unrestricted)
	case query.ConceptGroupID != "":
		var groupIDs []string
		groupIDs, err = kns.cga.GetConceptIDsByConceptGroupIDs(ctx, knID, query.Branch,
			[]string{query.ConceptGroupID}, interfaces.MODULE_TYPE_OBJECT_TYPE)
		if err == nil {
			groupIDs = intersectOverviewIDs(groupIDs, visibleObjectIDs, objectScope.Unrestricted)
			if len(groupIDs) > query.NodeLimit {
				groupIDs = groupIDs[:query.NodeLimit]
			}
			objectQuery.OTIDS = groupIDs
			objectQuery.Offset = 0
			objectQuery.Limit = query.NodeLimit
			objectTypes, err = kns.ota.ListObjectTypeSummaries(ctx, nil, objectQuery)
			if err == nil {
				relationTypes, err = kns.listOverviewEdges(ctx, relationQuery, groupIDs)
			}
		}
	default:
		objectQuery.Offset = offset
		objectTypes, err = kns.ota.ListObjectTypeSummaries(ctx, nil, objectQuery)
		if err == nil {
			pageIDs := make([]string, 0, len(objectTypes))
			for _, item := range objectTypes {
				pageIDs = append(pageIDs, item.OTID)
			}
			relationTypes, err = kns.listOverviewEdges(ctx, relationQuery, pageIDs)
		}
	}
	if err != nil {
		return nil, err
	}
	nodeIDSet := make(map[string]struct{}, len(objectTypes))
	for _, item := range objectTypes {
		nodeIDSet[item.OTID] = struct{}{}
	}
	edges := overviewEdges(relationTypes, nodeIDSet)
	result := &interfaces.OverviewGraph{
		Edges: edges, ObjectTypeTotal: objectTotal, RelationTypeTotal: relationTotal,
		ReturnedEdges: len(edges), Snapshot: snapshot,
	}
	result.Nodes = overviewNodes(objectTypes, edges)
	result.ReturnedNodes = len(result.Nodes)
	if query.FocusObjectTypeID == "" && query.ConceptGroupID == "" && len(objectTypes) > 0 &&
		offset+len(objectTypes) < objectTotal {
		result.NextCursor = encodeOverviewCursor(overviewGraphCursor{Offset: offset + len(objectTypes), Snapshot: snapshot})
	}
	result.Truncated = result.NextCursor != "" || len(relationTypes) >= query.EdgeLimit || len(edges) < relationTotal
	return result, nil
}
