// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.

package object_type

import (
	"context"
	"database/sql"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics"
)

func replaceObjectTypeMetricReferences(ctx context.Context, tx *sql.Tx, objectTypes ...*interfaces.ObjectType) error {
	// A whole-network import persists object types before metric definitions.
	// The archive adapter rebuilds both reference kinds after all metric versions
	// have been inserted, avoiding a temporary foreign-key violation here.
	if interfaces.ObjectMetricReferenceSyncDeferred(ctx) {
		return nil
	}
	access, ok := logics.OMA.(interfaces.ObjectMetricLogicPropertyReferenceAccessV1)
	if !ok {
		return nil
	}
	for _, objectType := range objectTypes {
		if objectType == nil {
			continue
		}
		if err := access.ReplaceObjectTypeMetricReferences(ctx, tx, objectType); err != nil {
			return err
		}
	}
	return nil
}

func deleteObjectTypeMetricReferences(ctx context.Context, tx *sql.Tx, knID, branch string, objectTypeIDs []string) error {
	access, ok := logics.OMA.(interfaces.ObjectMetricLogicPropertyReferenceAccessV1)
	if !ok || len(objectTypeIDs) == 0 {
		return nil
	}
	return access.DeleteObjectTypeMetricReferences(ctx, tx, knID, branch, objectTypeIDs)
}
