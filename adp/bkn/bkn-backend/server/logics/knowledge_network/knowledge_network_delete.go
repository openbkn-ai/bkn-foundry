// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knowledge_network

import (
	"context"

	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/permission"
)

// cleanupKNChildren reads existing IDs and cleans external authorization before
// opening the business write transaction. A failure leaves the source records
// available for the next authorized DELETE, without holding database row locks.
func (kns *knowledgeNetworkService) cleanupKNChildren(ctx context.Context, kn *interfaces.KN) error {
	steps := []struct {
		resourceType string
		loadIDs      func() ([]string, error)
	}{
		{interfaces.RESOURCE_TYPE_METRIC, func() ([]string, error) {
			if logics.OMA == nil {
				return nil, nil
			}
			return logics.OMA.ListObjectMetricIDs(ctx, interfaces.ObjectMetricListQueryV1{
				KNID: kn.KNID, Branch: kn.Branch,
			})
		}},
		{interfaces.RESOURCE_TYPE_ACTION_TYPE, func() ([]string, error) {
			return kns.ats.GetActionTypeIDsByKnID(ctx, kn.KNID, kn.Branch)
		}},
		{interfaces.RESOURCE_TYPE_RELATION_TYPE, func() ([]string, error) {
			return kns.rts.GetRelationTypeIDsByKnID(ctx, kn.KNID, kn.Branch)
		}},
		{interfaces.RESOURCE_TYPE_OBJECT_TYPE, func() ([]string, error) {
			return kns.ots.GetObjectTypeIDsByKnID(ctx, kn.KNID, kn.Branch)
		}},
		{interfaces.RESOURCE_TYPE_RISK_TYPE, func() ([]string, error) {
			risks, err := kns.riskTypeS.GetAllRiskTypesByKnID(ctx, kn.KNID, kn.Branch)
			if err != nil {
				return nil, err
			}
			ids := make([]string, 0, len(risks))
			for _, risk := range risks {
				ids = append(ids, risk.RTID)
			}
			return ids, nil
		}},
		{interfaces.RESOURCE_TYPE_CONCEPT_GROUP, func() ([]string, error) {
			return kns.cgs.GetConceptGroupIDsByKnID(ctx, kn.KNID, kn.Branch)
		}},
	}
	for _, step := range steps {
		if err := ctx.Err(); err != nil {
			return logics.PreserveHTTPError(ctx, err, berrors.BknBackend_InternalError_DeleteResourcesFailed)
		}
		ids, err := step.loadIDs()
		if err == nil {
			err = permission.CleanupKNChildAuthorization(ctx, kns.ps, step.resourceType, kn.KNID, ids)
		}
		if err != nil {
			return logics.PreserveHTTPError(ctx, err, berrors.BknBackend_InternalError_DeleteResourcesFailed)
		}
	}
	return nil
}
