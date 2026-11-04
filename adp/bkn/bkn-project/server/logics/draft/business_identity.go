// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbconcept"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"net/http"
	"time"
)

func (s *draftService) verifyBusinessIdentity(ctx context.Context, draft *interfaces.Draft, relation *interfaces.DraftRelationType, caller interfaces.AccountInfo) (*interfaces.DraftBusinessIdentityEvidence, error) {
	invalid := func() error {
		return httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Draft_BusinessIdentityNotUnique)
	}
	unavailable := func() error {
		return httperrors.NewHTTPError(ctx, http.StatusServiceUnavailable, perrors.BknProject_Draft_BusinessIdentityUnavailable)
	}
	target := kbconcept.VerifiedReferenceTarget(draft.Items, relation)
	if target == nil || s.va == nil {
		return nil, invalid()
	}
	field := findProperty(target, relation.DocumentMapping.IdentityProperty)
	countCtx, cancel := context.WithTimeout(ctx, identityCountTimeout)
	defer cancel()
	tables, err := s.va.GetTables(countCtx, caller, []string{target.Table.ResourceID})
	if err != nil {
		return nil, unavailable()
	}
	if len(tables) != 1 || tables[0] == nil || tables[0].ID != target.Table.ResourceID {
		return nil, unavailable()
	}
	physical, found := column(tables[0], field.MappedField)
	if !found || bknTypes[physical.Type] != field.Type {
		return nil, invalid()
	}
	timeout := s.setting.QueryTimeout
	if timeout <= 0 || timeout > int(identityCountTimeout.Seconds()) {
		timeout = int(identityCountTimeout.Seconds())
	}
	counter := &vegaCounter{va: s.va, account: caller, timeoutSeconds: timeout}
	rows, distinct, nulls, blanks, err := counter.businessKeyStats(countCtx, tables[0], field.MappedField, field.Type)
	if err != nil {
		return nil, unavailable()
	}
	if rows != distinct || nulls != 0 || blanks != 0 {
		return nil, invalid()
	}
	return &interfaces.DraftBusinessIdentityEvidence{TargetObjectTypeID: target.ID, ResourceID: target.Table.ResourceID, Property: field.Name, MappedField: field.MappedField, Type: field.Type, Rows: rows, Distinct: distinct, Nulls: nulls, Blanks: blanks, ConfirmedBy: caller, CheckedAt: time.Now().UnixMilli()}, nil
}

// A saved count is an audit snapshot, not permanent uniqueness certification.
// Recheck adopted alternate keys before preparing any remote Dataset.
func (s *draftService) checkBusinessIdentities(ctx context.Context, draft *interfaces.Draft, caller interfaces.AccountInfo) error {
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
