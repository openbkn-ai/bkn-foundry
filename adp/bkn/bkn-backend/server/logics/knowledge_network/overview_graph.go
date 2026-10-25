// Copyright openbkn.ai
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
	"sort"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"

	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/permission"
)

type overviewGraphCursor struct {
	Offset     int    `json:"offset,omitempty"`
	NodeOffset int    `json:"node_offset,omitempty"`
	EdgeOffset int    `json:"edge_offset,omitempty"`
	Scope      string `json:"scope,omitempty"`
	Snapshot   string `json:"snapshot"`
}

func overviewCursorScope(conceptGroupID string) string {
	if conceptGroupID == "" {
		return ""
	}
	return "concept-group:" + conceptGroupID
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

func overviewSnapshot(knID, branch, scope string, updateTime int64, objectTotal, relationTotal int) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%s:%d:%d:%d", knID, branch, scope, updateTime,
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
	if err := json.Unmarshal(payload, &cursor); err != nil || cursor.Offset < 0 || cursor.NodeOffset < 0 ||
		cursor.EdgeOffset < 0 || cursor.Snapshot == "" {
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

func overviewNodes(items []*interfaces.ObjectType, edges []interfaces.OverviewGraphEdge,
	groupsByObjectType map[string][]interfaces.OverviewGraphConceptGroup) []interfaces.OverviewGraphNode {
	degree := make(map[string]int, len(items))
	for _, edge := range edges {
		degree[edge.SourceID]++
		degree[edge.TargetID]++
	}
	nodes := make([]interfaces.OverviewGraphNode, 0, len(items))
	for _, item := range items {
		nodes = append(nodes, interfaces.OverviewGraphNode{
			ID: item.OTID, Name: item.OTName, Icon: item.Icon, Color: item.Color,
			IndexStatus: item.IndexStatus, Degree: degree[item.OTID],
			ConceptGroups: groupsByObjectType[item.OTID],
		})
	}
	return nodes
}

// overviewConceptGroups returns only concept groups the caller may read. Object-type enrichment
// already loads memberships in one batch, avoiding extra database reads and per-group requests.
func (kns *knowledgeNetworkService) overviewConceptGroups(ctx context.Context, knID string,
	items []*interfaces.ObjectType) (map[string][]interfaces.OverviewGraphConceptGroup, error) {
	groupIDs := make([]string, 0)
	seen := make(map[string]struct{})
	for _, item := range items {
		if item == nil {
			continue
		}
		for _, group := range item.ConceptGroups {
			if group == nil || group.CGID == "" {
				continue
			}
			if _, ok := seen[group.CGID]; ok {
				continue
			}
			seen[group.CGID] = struct{}{}
			groupIDs = append(groupIDs, group.CGID)
		}
	}
	sort.Strings(groupIDs)
	if len(groupIDs) == 0 {
		return map[string][]interfaces.OverviewGraphConceptGroup{}, nil
	}
	visibleIDs, err := permission.FilterKNChildIDs(ctx, kns.ps,
		interfaces.RESOURCE_TYPE_CONCEPT_GROUP, knID, groupIDs, interfaces.OPERATION_TYPE_VIEW_DETAIL)
	if err != nil {
		return nil, err
	}
	visible := overviewIDSet(visibleIDs)
	result := make(map[string][]interfaces.OverviewGraphConceptGroup, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		projected := make([]interfaces.OverviewGraphConceptGroup, 0, len(item.ConceptGroups))
		for _, group := range item.ConceptGroups {
			if group == nil {
				continue
			}
			if _, ok := visible[group.CGID]; !ok {
				continue
			}
			projected = append(projected, interfaces.OverviewGraphConceptGroup{
				ID: group.CGID, Name: group.CGName,
			})
		}
		sort.Slice(projected, func(left, right int) bool {
			if projected[left].Name == projected[right].Name {
				return projected[left].ID < projected[right].ID
			}
			return projected[left].Name < projected[right].Name
		})
		if len(projected) > 0 {
			result[item.OTID] = projected
		}
	}
	return result, nil
}

// enrichOverviewIndexStatuses reuses the normal object-type detail path so overview nodes see the
// same Vega local_status, batching, and knowledge-network proxy authorization as list and detail.
func (kns *knowledgeNetworkService) enrichOverviewIndexStatuses(ctx context.Context, knID, branch string,
	items []*interfaces.ObjectType) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		if item != nil && item.OTID != "" {
			ids = append(ids, item.OTID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	details, err := kns.ots.GetObjectTypesByIDs(ctx, nil, knID, branch, ids)
	if err != nil {
		return err
	}
	byID := make(map[string]*interfaces.ObjectType, len(details))
	for _, detail := range details {
		if detail != nil {
			byID[detail.OTID] = detail
		}
	}
	for _, item := range items {
		if item != nil {
			if detail, ok := byID[item.OTID]; ok {
				item.IndexStatus = detail.IndexStatus
				item.ConceptGroups = detail.ConceptGroups
			}
		}
	}
	return nil
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
			return nil, nil, httperrors.NewHTTPError(ctx, http.StatusNotFound,
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
		return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, berrors.BknBackend_KnowledgeNetwork_NotFound)
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
	var conceptGroupObjectIDs []string
	if query.ConceptGroupID != "" {
		conceptGroupObjectIDs, err = kns.cga.GetConceptIDsByConceptGroupIDs(ctx, knID, query.Branch,
			[]string{query.ConceptGroupID}, interfaces.MODULE_TYPE_OBJECT_TYPE)
		if err != nil {
			return nil, err
		}
		conceptGroupObjectIDs = intersectOverviewIDs(conceptGroupObjectIDs, visibleObjectIDs,
			objectScope.Unrestricted)
		if len(conceptGroupObjectIDs) == 0 {
			snapshot := overviewSnapshot(knID, query.Branch, overviewCursorScope(query.ConceptGroupID),
				kn.UpdateTime, 0, 0)
			return &interfaces.OverviewGraph{Nodes: []interfaces.OverviewGraphNode{},
				Edges: []interfaces.OverviewGraphEdge{}, Snapshot: snapshot}, nil
		}
		objectQuery.OTIDS = conceptGroupObjectIDs
	}
	objectTotal, err := kns.ota.GetObjectTypesTotal(ctx, objectQuery)
	if err != nil {
		return nil, err
	}
	if objectTotal == 0 {
		snapshot := overviewSnapshot(knID, query.Branch, overviewCursorScope(query.ConceptGroupID),
			kn.UpdateTime, 0, 0)
		return &interfaces.OverviewGraph{Nodes: []interfaces.OverviewGraphNode{}, Edges: []interfaces.OverviewGraphEdge{}, Snapshot: snapshot}, nil
	}

	relationQuery := kns.overviewRelationQuery(knID, query, visibleRelationIDs, visibleObjectIDs,
		objectScope.Unrestricted)
	if query.ConceptGroupID != "" {
		relationQuery.SourceObjectTypeIDs = conceptGroupObjectIDs
		relationQuery.TargetObjectTypeIDs = conceptGroupObjectIDs
	}
	relationTotal, err := kns.rta.GetRelationTypesTotal(ctx, relationQuery)
	if err != nil {
		return nil, err
	}
	scope := overviewCursorScope(query.ConceptGroupID)
	snapshot := overviewSnapshot(knID, query.Branch, scope, kn.UpdateTime, objectTotal, relationTotal)
	cursor := overviewGraphCursor{Scope: scope, Snapshot: snapshot}
	if query.Cursor != "" {
		decoded, cursorErr := decodeOverviewCursor(query.Cursor)
		if cursorErr != nil || decoded.Snapshot != snapshot || decoded.Scope != scope {
			return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest,
				berrors.BknBackend_KnowledgeNetwork_InvalidParameter).WithErrorDetails("overview graph cursor is invalid or stale")
		}
		cursor = decoded
	}

	var objectTypes []*interfaces.ObjectType
	var relationTypes []*interfaces.RelationType
	switch {
	case query.FocusObjectTypeID != "":
		objectTypes, relationTypes, err = kns.listOverviewFocus(ctx, knID, query, relationQuery,
			visibleObjectIDs, objectScope.Unrestricted)
	case query.ConceptGroupID != "":
		if cursor.NodeOffset < objectTotal {
			objectQuery.Offset = cursor.NodeOffset
			objectTypes, err = kns.ota.ListObjectTypeSummaries(ctx, nil, objectQuery)
		} else {
			objectTypes = []*interfaces.ObjectType{}
		}
		if err == nil {
			if cursor.EdgeOffset < relationTotal {
				relationQuery.Offset = cursor.EdgeOffset
				relationTypes, err = kns.rta.ListRelationTypeSummaries(ctx, relationQuery)
			} else {
				relationTypes = []*interfaces.RelationType{}
			}
		}
	default:
		objectQuery.Offset = cursor.Offset
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
	if err := kns.enrichOverviewIndexStatuses(ctx, knID, query.Branch, objectTypes); err != nil {
		return nil, err
	}
	groupsByObjectType, err := kns.overviewConceptGroups(ctx, knID, objectTypes)
	if err != nil {
		return nil, err
	}
	nodeIDSet := make(map[string]struct{}, len(objectTypes))
	for _, item := range objectTypes {
		nodeIDSet[item.OTID] = struct{}{}
	}
	edgeNodeIDs := nodeIDSet
	if query.ConceptGroupID != "" {
		edgeNodeIDs = overviewIDSet(conceptGroupObjectIDs)
	}
	edges := overviewEdges(relationTypes, edgeNodeIDs)
	result := &interfaces.OverviewGraph{
		Edges: edges, ObjectTypeTotal: objectTotal, RelationTypeTotal: relationTotal,
		ReturnedEdges: len(edges), Snapshot: snapshot,
	}
	result.Nodes = overviewNodes(objectTypes, edges, groupsByObjectType)
	result.ReturnedNodes = len(result.Nodes)
	if query.ConceptGroupID != "" {
		nextNodeOffset := cursor.NodeOffset + len(objectTypes)
		if len(objectTypes) == 0 {
			nextNodeOffset = objectTotal
		}
		nextEdgeOffset := cursor.EdgeOffset + len(relationTypes)
		if len(relationTypes) == 0 {
			nextEdgeOffset = relationTotal
		}
		if nextNodeOffset < objectTotal || nextEdgeOffset < relationTotal {
			result.NextCursor = encodeOverviewCursor(overviewGraphCursor{
				NodeOffset: nextNodeOffset, EdgeOffset: nextEdgeOffset, Scope: scope, Snapshot: snapshot,
			})
		}
		result.Truncated = result.ReturnedNodes < objectTotal || result.ReturnedEdges < relationTotal
	} else {
		if query.FocusObjectTypeID == "" && len(objectTypes) > 0 &&
			cursor.Offset+len(objectTypes) < objectTotal {
			result.NextCursor = encodeOverviewCursor(overviewGraphCursor{
				Offset: cursor.Offset + len(objectTypes), Snapshot: snapshot,
			})
		}
		result.Truncated = result.NextCursor != "" || len(relationTypes) >= query.EdgeLimit || len(edges) < relationTotal
	}
	return result, nil
}
