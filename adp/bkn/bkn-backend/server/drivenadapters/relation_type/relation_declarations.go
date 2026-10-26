// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package relation_type

import (
	"context"

	"github.com/bytedance/sonic"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// declarationColumns are the columns of the BKN spec 3.0 relation declarations (migration 0.2.4).
// They are read after the 2.x columns. A row without declarations is NULL in each of them.
var declarationColumns = []string{
	"f_reverse_name",
	"f_aliases",
	"f_cardinality",
	"f_constraints",
	"f_lifecycle_status",
	"f_deprecation",
	"f_importance",
	"f_fallback_for",
	"f_verification",
}

// scannedDeclarations receives the declaration columns of one row.
type scannedDeclarations struct {
	reverseName, aliases, cardinality, constraints, lifecycleStatus, deprecation,
	importance, fallbackFor, verification []byte
}

// apply decodes the declaration columns into relationType. A constraints column that does not
// decode is read as none, and logged, as for aliases: one bad column must not make the relation
// type, and the network read with it, unopenable.
func (d *scannedDeclarations) apply(ctx context.Context, relationType *interfaces.RelationType) {
	decl := interfaces.RelationDeclarations{
		ReverseName:  string(d.reverseName),
		Aliases:      common.DecodeStoredAliases(ctx, d.aliases),
		Cardinality:  string(d.cardinality),
		Importance:   string(d.importance),
		FallbackFor:  string(d.fallbackFor),
		Verification: string(d.verification),
	}
	var constraints interfaces.RelationConstraints
	if err := common.UnmarshalStoredJSON(d.constraints, &constraints); err != nil {
		common.LogSafeError(ctx, "Failed to unmarshal stored relation constraints", err)
	} else if !constraints.IsEmpty() {
		decl.Constraints = &constraints
	}
	if status, reason, replacedBy := common.DecodeStoredLifecycle(ctx, d.lifecycleStatus, d.deprecation); status != "" {
		decl.Lifecycle = &interfaces.Lifecycle{Status: status, Reason: reason, ReplacedBy: replacedBy}
	}
	relationType.RelationDeclarations = decl
}

// declarationValues are the declaration column values of relationType, in declarationColumns order.
func declarationValues(relationType *interfaces.RelationType) ([]any, error) {
	aliases, err := common.EncodeAliases(relationType.Aliases)
	if err != nil {
		return nil, err
	}
	var constraints any
	if !relationType.Constraints.IsEmpty() {
		b, err := sonic.Marshal(relationType.Constraints)
		if err != nil {
			return nil, err
		}
		constraints = string(b)
	}
	var status, reason, replacedBy string
	if lc := relationType.Lifecycle; lc != nil {
		status, reason, replacedBy = lc.Status, lc.Reason, lc.ReplacedBy
	}
	lifecycleStatus, deprecation, err := common.EncodeLifecycle(status, reason, replacedBy)
	if err != nil {
		return nil, err
	}
	return []any{
		common.NullableString(relationType.ReverseName),
		aliases,
		common.NullableString(relationType.Cardinality),
		constraints,
		lifecycleStatus,
		deprecation,
		common.NullableString(relationType.Importance),
		common.NullableString(relationType.FallbackFor),
		common.NullableString(relationType.Verification),
	}, nil
}
