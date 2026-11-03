// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
package kbinstance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbdataset"
	"time"
)

// PublishedFusionPlan is private execution input, not a DTO or permission grant.
// It reuses the same linking, reconciliation, fact-state and entity-fusion plan
// as construction, while keeping staged claims distinct from acknowledged rows.
type PublishedFusionPlan = preparedSystemLinks

func PreparePublishedFusion(ctx context.Context, data systemLinkDatasets, vega interfaces.VegaAccess, build *interfaces.KBBuild, draft *interfaces.Draft, now int64, guard func(context.Context) error) (*PublishedFusionPlan, error) {
	if data == nil || build == nil || draft == nil || build.Mode != "full" || now <= 0 || guard == nil {
		return nil, ErrInvalidBuild
	}
	if err := guard(ctx); err != nil {
		return nil, err
	}
	fingerprint, err := kbdataset.ModelFingerprint(draft)
	if err != nil || fingerprint != build.ModelFingerprint {
		return nil, ErrBuildChanged
	}
	if len(build.Files) == 0 {
		// A complete authorized empty manifest has no invented first file or task.
		// File-scoped rows are retired by refresh; canonical entities retire here.
		result := &PublishedFusionPlan{Writes: []kb.DatasetWrite{}, Preserved: []kb.DatasetWrite{}, FusionWrites: []kb.DatasetWrite{}, Scopes: []systemLinkScope{}, Entities: []entityFusionScope{}, Preview: SystemLinkPreview{Relations: []SystemLinkSummary{}, Fusion: []EntityFusionSummary{}, Facts: []FactStateSummary{}, FactRulesPending: []string{}}}
		found := false
		for _, plan := range draft.Items.DatasetPlans {
			if plan == nil || plan.ID != build.GenerationID {
				continue
			}
			if plan.State != "ready" || plan.ModelFingerprint != fingerprint || kbdataset.VerifyBindings(draft, plan) != nil {
				return nil, ErrBuildChanged
			}
			found = true
			for _, spec := range plan.Datasets {
				if !spec.Ready {
					return nil, ErrBuildChanged
				}
				object := instanceObject(draft.Items, spec.ObjectTypeID)
				if object != nil && object.Document != nil && object.Document.Role == "entity" {
					result.Entities = append(result.Entities, entityFusionScope{Spec: spec, Model: fingerprint, Keep: []string{}})
					result.Preview.Fusion = append(result.Preview.Fusion, EntityFusionSummary{ObjectTypeID: object.ID, Name: object.Name})
				}
			}
		}
		if !found {
			return nil, ErrBuildChanged
		}
		raw, err := json.Marshal(struct {
			Model, Build string
			Entities     []entityFusionScope
		}{fingerprint, build.ID, result.Entities})
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(raw)
		result.Preview.Signature = hex.EncodeToString(sum[:])
		return result, guard(ctx)
	}
	service := &SystemLinkService{Datasets: data, Vega: vega, Now: func() time.Time { return time.UnixMilli(now) }, staged: true}
	return service.prepare(ctx, &interfaces.KBBuildRecord{Build: build}, draft, guard)
}
