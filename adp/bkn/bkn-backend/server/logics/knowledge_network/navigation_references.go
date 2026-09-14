// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knowledge_network

import (
	"context"
	"net/http"
	"sort"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
	"bkn-backend/logics/permission"
)

// dropUnreadableReferenceRows removes, from the child rows that make a network navigable, the
// relation types with an endpoint and the action types with a bound object type the caller holds
// nothing on. Those types are not readable (#1532), so they must neither open a navigation shell
// nor count in its statistics.
//
// A row whose definition can no longer be found is dropped too: without it the references cannot
// be checked.
func (kns *knowledgeNetworkService) dropUnreadableReferenceRows(ctx context.Context, branch string,
	candidates []interfaces.KNChildResourceCandidate,
	visibleChildRows map[string]map[string]interfaces.PermissionResourceOps) error {

	if branch == "" {
		branch = interfaces.MAIN_BRANCH
	}
	idsByKN := map[string]map[string][]string{}
	for _, candidate := range candidates {
		if candidate.Type != interfaces.RESOURCE_TYPE_RELATION_TYPE && candidate.Type != interfaces.RESOURCE_TYPE_ACTION_TYPE {
			continue
		}
		if _, visible := visibleChildRows[candidate.Type][interfaces.KNChildResourceID(candidate.KNID, candidate.ResourceID)]; !visible {
			continue
		}
		if idsByKN[candidate.KNID] == nil {
			idsByKN[candidate.KNID] = map[string][]string{}
		}
		idsByKN[candidate.KNID][candidate.Type] = append(idsByKN[candidate.KNID][candidate.Type], candidate.ResourceID)
	}

	knIDs := make([]string, 0, len(idsByKN))
	for knID := range idsByKN {
		knIDs = append(knIDs, knID)
	}
	sort.Strings(knIDs)
	for _, knID := range knIDs {
		readable, err := kns.readableReferenceRows(ctx, knID, branch, idsByKN[knID])
		if err != nil {
			return err
		}
		for resourceType, childIDs := range idsByKN[knID] {
			for _, childID := range childIDs {
				if _, ok := readable[resourceType][childID]; !ok {
					delete(visibleChildRows[resourceType], interfaces.KNChildResourceID(knID, childID))
				}
			}
		}
	}
	return nil
}

// readableReferenceRows returns, per resource type, the relation and action type ids of one
// network whose referenced object types the caller holds at least one effective operation on.
func (kns *knowledgeNetworkService) readableReferenceRows(ctx context.Context, knID, branch string,
	idsByType map[string][]string) (map[string]map[string]struct{}, error) {

	var relationTypes []*interfaces.RelationType
	var actionTypes []*interfaces.ActionType
	var err error
	referenced := make([]string, 0)
	if ids := idsByType[interfaces.RESOURCE_TYPE_RELATION_TYPE]; len(ids) > 0 {
		relationTypes, err = kns.rta.GetRelationTypesByIDs(ctx, knID, branch, ids)
		if err != nil {
			return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_KnowledgeNetwork_InternalError).WithErrorDetails(err.Error())
		}
		for _, relationType := range relationTypes {
			referenced = append(referenced, relationType.SourceObjectTypeID, relationType.TargetObjectTypeID)
		}
	}
	if ids := idsByType[interfaces.RESOURCE_TYPE_ACTION_TYPE]; len(ids) > 0 {
		actionTypes, err = kns.ata.GetActionTypesByIDs(ctx, knID, branch, ids)
		if err != nil {
			return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_KnowledgeNetwork_InternalError).WithErrorDetails(err.Error())
		}
		for _, actionType := range actionTypes {
			referenced = append(referenced, actionType.ObjectTypeID)
		}
	}

	visible, err := permission.VisibleReferencedObjectTypes(ctx, kns.ps, knID, referenced)
	if err != nil {
		return nil, err
	}
	readable := map[string]map[string]struct{}{
		interfaces.RESOURCE_TYPE_RELATION_TYPE: {},
		interfaces.RESOURCE_TYPE_ACTION_TYPE:   {},
	}
	for _, relationType := range relationTypes {
		if permission.ReferencesVisible(visible, relationType.SourceObjectTypeID, relationType.TargetObjectTypeID) {
			readable[interfaces.RESOURCE_TYPE_RELATION_TYPE][relationType.RTID] = struct{}{}
		}
	}
	for _, actionType := range actionTypes {
		if permission.ReferencesVisible(visible, actionType.ObjectTypeID) {
			readable[interfaces.RESOURCE_TYPE_ACTION_TYPE][actionType.ATID] = struct{}{}
		}
	}
	return readable, nil
}
