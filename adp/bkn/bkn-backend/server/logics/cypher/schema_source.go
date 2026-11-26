// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"context"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// serviceSchemaSource reads modelling metadata through the object type and
// relation type services, scoped to one network branch and unfiltered by
// permission: LoadSchema applies the query_data check itself.
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
	// Read the whole set unfiltered, as for object types. The public list keeps
	// only relation types the caller may view_detail on, and LoadSchema then
	// filters by query_data, so a caller holding query_data alone would find a
	// relation type it may query missing and get "unknown relationship type"
	// (#3410). The query_data filter in LoadSchema is the check that applies.
	return s.rts.GetAllRelationTypesByKnID(ctx, knID, branch)
}
