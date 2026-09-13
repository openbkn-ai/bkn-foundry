// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package logics

import (
	"context"
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	oerrors "ontology-query/errors"
	"ontology-query/interfaces"
	"ontology-query/locale"
)

// ResolveTypeEdgeDirection returns the direction in which edge, the index-th
// (0-based) hop of a requested path, walks relationType.
//
// Query authorization and path execution both call it, so a path that one of them
// accepts the other cannot reject. When the edge cannot walk the relation the error
// names the edge, the relation type, the endpoints the path asks for and the ones
// the relation is defined with: that is what a caller needs to rewrite the request.
func ResolveTypeEdgeDirection(ctx context.Context, index int, edge interfaces.TypeEdge,
	relationType interfaces.RelationType) (string, error) {
	if direction, ok := edge.TraversalDirection(relationType); ok {
		return direction, nil
	}

	params := map[string]any{
		"index":    index + 1,
		"relation": edge.RelationTypeId,
		"from":     edge.SourceObjectTypeId,
		"to":       edge.TargetObjectTypeId,
		"source":   relationType.SourceObjectTypeID,
		"target":   relationType.TargetObjectTypeID,
	}
	detailKey := "EdgeRelationMismatch"
	undirected := edge
	undirected.Direction = ""
	if _, connected := undirected.TraversalDirection(relationType); connected {
		// The endpoints are the relation's two ends; only the declared direction is wrong.
		detailKey = "EdgeDirectionConflict"
		params["direction"] = edge.Direction
		params["expectedFrom"], params["expectedTo"] = relationType.SourceObjectTypeID, relationType.TargetObjectTypeID
		if edge.Direction == interfaces.DIRECTION_BACKWARD {
			params["expectedFrom"], params["expectedTo"] = relationType.TargetObjectTypeID, relationType.SourceObjectTypeID
		}
	}
	return "", rest.NewHTTPError(ctx, http.StatusBadRequest,
		oerrors.OntologyQuery_KnowledgeNetwork_InvalidParameter_TypePath).
		WithErrorDetails(locale.ValidationDetail(ctx, detailKey, params))
}
