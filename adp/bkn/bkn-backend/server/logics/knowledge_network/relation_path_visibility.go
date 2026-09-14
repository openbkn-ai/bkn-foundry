// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knowledge_network

import (
	"context"
	"errors"
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	"bkn-backend/common"
	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
	"bkn-backend/logics/permission"
	"bkn-backend/logics/relation_type"
)

// relationPathScope is the part of a network's model that relation type paths may go through for
// a caller without view_detail on the network (#1553): the relation types the caller may read,
// and the source object type together with their endpoints -- all of which the caller holds at
// least one effective operation on.
//
// Paths are searched over this part alone, as if nothing else were in the network. A path is
// neither cut short at nor dropped because of a type the caller cannot see, so no id, name, count
// or path length of a hidden type reaches the caller.
type relationPathScope struct {
	relationTypes map[string]struct{}
	objectTypes   map[string]struct{}
}

// resolveRelationPathScope returns the scope of a caller whose network check was refused with
// denied. The caller stays refused with denied unless it can see the source object type and, when
// the query is limited to concept groups, read every one of them. An authorization failure is
// returned rather than read as "nothing is visible".
func (kns *knowledgeNetworkService) resolveRelationPathScope(ctx context.Context,
	query interfaces.RelationTypePathsBaseOnSource, denied error) (*relationPathScope, error) {

	visible, err := permission.VisibleReferencedObjectTypes(ctx, kns.ps, query.KNID,
		[]string{query.SourceObjecTypeId})
	if err != nil {
		return nil, err
	}
	if _, ok := visible[query.SourceObjecTypeId]; !ok {
		return nil, denied
	}

	// A concept group limits which relation types a path may use. Limiting by a group the caller
	// cannot read would tell which visible object types are its members, so, as when listing a
	// group's members, every group named must be readable.
	if len(query.ConceptGroups) > 0 {
		groupIDs := common.DuplicateSlice(query.ConceptGroups)
		readableGroups, err := permission.FilterKNChildIDs(ctx, kns.ps, interfaces.RESOURCE_TYPE_CONCEPT_GROUP,
			query.KNID, groupIDs, interfaces.OPERATION_TYPE_VIEW_DETAIL)
		if err != nil {
			return nil, err
		}
		if len(readableGroups) != len(groupIDs) {
			return nil, denied
		}
	}

	branch := query.Branch
	if branch == "" {
		branch = interfaces.MAIN_BRANCH
	}
	relationTypes, err := kns.rta.ListRelationTypes(ctx, interfaces.RelationTypesQueryParams{
		PaginationQueryParameters: interfaces.PaginationQueryParameters{Limit: -1},
		KNID:                      query.KNID,
		Branch:                    branch,
	})
	if err != nil {
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_KnowledgeNetwork_InternalError).WithErrorDetails(err.Error())
	}
	readable, err := relation_type.ReadableRelationTypes(ctx, kns.ps, query.KNID, relationTypes)
	if err != nil {
		return nil, err
	}

	scope := &relationPathScope{
		relationTypes: make(map[string]struct{}, len(readable)),
		objectTypes:   map[string]struct{}{query.SourceObjecTypeId: {}},
	}
	for _, relationType := range readable {
		scope.relationTypes[relationType.RTID] = struct{}{}
		// A readable relation type's endpoints passed the reference rule on the way. An empty one
		// is no reference and names nothing.
		for _, endpointID := range []string{relationType.SourceObjectTypeID, relationType.TargetObjectTypeID} {
			if endpointID != "" {
				scope.objectTypes[endpointID] = struct{}{}
			}
		}
	}
	return scope, nil
}

// keep narrows the one-hop paths found from each node to those inside the scope. A node left with
// none ends its path there, as a node without neighbors does.
func (s *relationPathScope) keep(
	neighborPathsMap map[string][]interfaces.RelationTypePath) map[string][]interfaces.RelationTypePath {

	kept := make(map[string][]interfaces.RelationTypePath, len(neighborPathsMap))
	for nodeID, neighborPaths := range neighborPathsMap {
		for _, neighborPath := range neighborPaths {
			if s.contains(neighborPath) {
				kept[nodeID] = append(kept[nodeID], neighborPath)
			}
		}
	}
	return kept
}

// contains reports whether every relation type and object type a path names is in the scope. The
// ids are checked as the path carries them, so a row that differs from the definition authorized
// -- another branch's relation type of the same id, say -- cannot bring in an unchecked end.
func (s *relationPathScope) contains(path interfaces.RelationTypePath) bool {
	for _, edge := range path.TypeEdges {
		if !inScope(s.relationTypes, edge.RelationTypeId, edge.RelationType.RTID) ||
			!inScope(s.objectTypes, edge.SourceObjectTypeId, edge.Target_ObjectTypeId,
				edge.RelationType.SourceObjectTypeID, edge.RelationType.TargetObjectTypeID) {
			return false
		}
	}
	for _, objectType := range path.ObjectTypes {
		if !inScope(s.objectTypes, objectType.OTID) {
			return false
		}
	}
	return true
}

// inScope reports whether every id is in set. Unlike a reference, an empty id on a path is not
// skipped: it is not in the scope.
func inScope(set map[string]struct{}, ids ...string) bool {
	for _, id := range ids {
		if _, ok := set[id]; !ok {
			return false
		}
	}
	return true
}

// isForbidden reports whether err refuses the caller, as opposed to failing to decide.
func isForbidden(err error) bool {
	var httpErr *rest.HTTPError
	return errors.As(err, &httpErr) && httpErr.HTTPCode == http.StatusForbidden
}
