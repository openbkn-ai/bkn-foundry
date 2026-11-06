// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package docdraft

import (
	"context"
	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/projecthooks"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbconcept"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"net/http"
	"time"
)

func (s service) verifyBusinessIdentity(ctx context.Context, draft *interfaces.Draft, relation *interfaces.DraftRelationType, caller interfaces.AccountInfo) (*interfaces.DraftBusinessIdentityEvidence, error) {
	invalid := func() error {
		return httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Draft_BusinessIdentityNotUnique)
	}
	unavailable := func() error {
		return httperrors.NewHTTPError(ctx, http.StatusServiceUnavailable, perrors.BknProject_Draft_BusinessIdentityUnavailable)
	}
	target := kbconcept.VerifiedReferenceTarget(draft.Items, relation)
	if target == nil {
		return nil, invalid()
	}
	field := projecthooks.FindProperty(target, relation.DocumentMapping.IdentityProperty)
	count := s.CountBusinessKey(ctx, caller, target.Table.ResourceID, field.MappedField, field.Type)
	switch count.Refusal {
	case projecthooks.KeyInvalid:
		return nil, invalid()
	case projecthooks.KeyUnavailable:
		return nil, unavailable()
	}
	rows, distinct, nulls, blanks := count.Rows, count.Distinct, count.Nulls, count.Blanks
	if rows != distinct || nulls != 0 || blanks != 0 {
		return nil, invalid()
	}
	return &interfaces.DraftBusinessIdentityEvidence{TargetObjectTypeID: target.ID, ResourceID: target.Table.ResourceID, Property: field.Name, MappedField: field.MappedField, Type: field.Type, Rows: rows, Distinct: distinct, Nulls: nulls, Blanks: blanks, ConfirmedBy: caller, CheckedAt: time.Now().UnixMilli()}, nil
}

// A saved count is an audit snapshot, not permanent uniqueness certification.
// Recheck adopted alternate keys before preparing any remote Dataset.
func (s service) checkBusinessIdentities(ctx context.Context, draft *interfaces.Draft, caller interfaces.AccountInfo) error {
	for _, relation := range draft.Items.RelationTypes {
		if relation == nil || !relation.Adopted || relation.DocumentMapping == nil || relation.DocumentMapping.BusinessIdentity == nil {
			continue
		}
		if !kbconcept.LinkIdentityReady(draft.Items, relation) {
			return httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Draft_BusinessIdentityNotUnique)
		}
		if _, err := s.verifyBusinessIdentity(ctx, draft, relation, caller); err != nil {
			return err
		}
	}
	return nil
}
