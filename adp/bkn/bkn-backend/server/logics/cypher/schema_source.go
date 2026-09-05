// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"context"

	"bkn-backend/interfaces"
)

// serviceSchemaSource reads modelling metadata through the ordinary object
// type and relation type services, so the compiler sees exactly what the rest
// of the service sees, including branch scoping.
type serviceSchemaSource struct {
	ots interfaces.ObjectTypeService
	rts interfaces.RelationTypeService
}

// NewSchemaSource wires the compiler to the modelling services.
func NewSchemaSource(ots interfaces.ObjectTypeService, rts interfaces.RelationTypeService) KNSchemaSource {
	return &serviceSchemaSource{ots: ots, rts: rts}
}

func (s *serviceSchemaSource) AllObjectTypes(ctx context.Context, knID, branch string) ([]*interfaces.ObjectType, error) {
	byID, err := s.ots.GetAllObjectTypesByKnID(ctx, knID, branch)
	if err != nil {
		return nil, err
	}
	objectTypes := make([]*interfaces.ObjectType, 0, len(byID))
	for _, ot := range byID {
		objectTypes = append(objectTypes, ot)
	}
	return objectTypes, nil
}

func (s *serviceSchemaSource) AllRelationTypes(ctx context.Context, knID, branch string) ([]*interfaces.RelationType, error) {
	// Limit -1 asks for the whole set; a Cypher pattern may name any relation
	// type in the network, so paging here would only hide some of them.
	relationTypes, _, err := s.rts.ListRelationTypes(ctx, interfaces.RelationTypesQueryParams{
		PaginationQueryParameters: interfaces.PaginationQueryParameters{Limit: -1},
		KNID:                      knID,
		Branch:                    branch,
	})
	if err != nil {
		return nil, err
	}
	return relationTypes, nil
}
