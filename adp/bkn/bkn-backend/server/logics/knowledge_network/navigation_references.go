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

// dropUnreadableReferenceRows removes, from the child rows that make a network navigable, relation
// types with an endpoint the caller holds nothing on. Relation types name both endpoints and are
// therefore unreadable in that case (#1532). Action types are independently authorized and remain
// navigable when the caller can read the action itself.
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
		if candidate.Type != interfaces.RESOURCE_TYPE_RELATION_TYPE {
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

	// Definitions are read per network, where they live; the object types they reference are
	// then checked for every network in one authorization call, so the cost of listing networks
	// does not grow with how many of them the caller reaches only through their children.
	definitions := make(map[string]*referenceDefinitions, len(knIDs))
	referencedByKN := make(map[string][]string, len(knIDs))
	for _, knID := range knIDs {
		defs, err := kns.readReferenceDefinitions(ctx, knID, branch, idsByKN[knID])
		if err != nil {
			return err
		}
		definitions[knID] = defs
		referencedByKN[knID] = defs.referencedObjectTypes()
	}
	visibleByKN, err := permission.VisibleReferencedObjectTypesByKN(ctx, kns.ps, referencedByKN)
	if err != nil {
		return err
	}

	for _, knID := range knIDs {
		readable := definitions[knID].readable(visibleByKN[knID])
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

// referenceDefinitions are the relation type rows of one network whose endpoints a navigation
// check has to resolve.
type referenceDefinitions struct {
	relationTypes []*interfaces.RelationType
}

// readReferenceDefinitions loads the given relation types of one network.
func (kns *knowledgeNetworkService) readReferenceDefinitions(ctx context.Context, knID, branch string,
	idsByType map[string][]string) (*referenceDefinitions, error) {

	defs := &referenceDefinitions{}
	var err error
	if ids := idsByType[interfaces.RESOURCE_TYPE_RELATION_TYPE]; len(ids) > 0 {
		defs.relationTypes, err = kns.rta.GetRelationTypesByIDs(ctx, knID, branch, ids)
		if err != nil {
			return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_KnowledgeNetwork_InternalError).WithErrorDetails(err.Error())
		}
	}
	return defs, nil
}

// referencedObjectTypes lists the object types the relation definitions point at.
func (defs *referenceDefinitions) referencedObjectTypes() []string {
	referenced := make([]string, 0, len(defs.relationTypes)*2)
	for _, relationType := range defs.relationTypes {
		referenced = append(referenced, relationType.SourceObjectTypeID, relationType.TargetObjectTypeID)
	}
	return referenced
}

// readable returns the relation type ids whose endpoint object types are all in visible.
func (defs *referenceDefinitions) readable(visible map[string]struct{}) map[string]map[string]struct{} {
	readable := map[string]map[string]struct{}{
		interfaces.RESOURCE_TYPE_RELATION_TYPE: {},
	}
	for _, relationType := range defs.relationTypes {
		if permission.ReferencesVisible(visible, relationType.SourceObjectTypeID, relationType.TargetObjectTypeID) {
			readable[interfaces.RESOURCE_TYPE_RELATION_TYPE][relationType.RTID] = struct{}{}
		}
	}
	return readable
}
