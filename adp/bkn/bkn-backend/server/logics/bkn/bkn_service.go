// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"bytes"
	"context"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	bknsdk "bkn-backend/bkn-specification/bkn"
	"bkn-backend/common"
	"bkn-backend/interfaces"
	"bkn-backend/logics"
	"bkn-backend/logics/knowledge_network"
)

var (
	bServiceOnce sync.Once
	bService     interfaces.BKNService
)

type bknService struct {
	appSetting *common.AppSetting
	kns        interfaces.KNService
}

// NewBKNService creates the BKN service.
func NewBKNService(appSetting *common.AppSetting) interfaces.BKNService {
	bServiceOnce.Do(func() {
		bService = &bknService{
			appSetting: appSetting,
			kns:        knowledge_network.NewKNService(appSetting),
		}
	})
	return bService
}

// ExportToTar exports a knowledge network as a tar archive.
func (bs *bknService) ExportToTar(ctx context.Context, knID string, branch string) ([]byte, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "BKN导出为Tar")
	defer span.End()

	logger.Debugf("BKN ExportToTar Start: kn_id=%s", knID)

	bknNetwork, err := bs.buildNetwork(ctx, knID, branch)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	err = bknsdk.WriteNetworkToTar(bknNetwork, &buf)
	if err != nil {
		otellog.LogError(ctx, "BKN ExportToTar failed", err)
		return nil, err
	}
	tarData := buf.Bytes()

	logger.Debugf("BKN ExportToTar Completed: size=%d", len(tarData))
	span.SetStatus(codes.Ok, "")
	return tarData, nil
}

// buildNetwork assembles the complete in-memory model of one knowledge network branch.
//
// Export and comparison must see the same thing. Assembling the model separately for each caller
// is how the two drift: a section added to the export quietly stops being compared.
func (bs *bknService) buildNetwork(ctx context.Context, knID string, branch string) (*bknsdk.BknNetwork, error) {
	kn, err := bs.kns.GetKNByID(ctx, knID, branch, interfaces.Mode_Export)
	if err != nil {
		otellog.LogError(ctx, "BKN GetKNByID failed", err)
		return nil, err
	}

	bknNetwork := logics.ToBKNNetWork(kn)
	for _, ot := range kn.ObjectTypes {
		bknNetwork.ObjectTypes = append(bknNetwork.ObjectTypes, logics.ToBKNObjectType(ot))
	}
	for _, rt := range kn.RelationTypes {
		bknNetwork.RelationTypes = append(bknNetwork.RelationTypes, logics.ToBKNRelationType(rt))
	}
	for _, act := range kn.ActionTypes {
		bknNetwork.ActionTypes = append(bknNetwork.ActionTypes, logics.ToBKNActionType(act))
	}
	for _, risk := range kn.RiskTypes {
		bknNetwork.RiskTypes = append(bknNetwork.RiskTypes, logics.ToBKNRiskType(risk))
	}
	for _, cg := range kn.ConceptGroups {
		bknNetwork.ConceptGroups = append(bknNetwork.ConceptGroups, logics.ToBKNConceptGroup(cg))
	}
	for _, m := range kn.Metrics {
		bknNetwork.Metrics = append(bknNetwork.Metrics, logics.ToBKNMetricDefinition(m))
	}

	return bknNetwork, nil
}

// DiffNetworks compares two knowledge network branches and reports what differs.
//
// Both sides are loaded through GetKNByID, so each is authorized on its own: a caller who may read
// one network and not the other is refused exactly as they would be asking for that network
// directly, and a comparison cannot be used to read a network sideways.
func (bs *bknService) DiffNetworks(ctx context.Context, req interfaces.KNDiffRequest) (*interfaces.KNDiffResult, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "BKN网络对比")
	defer span.End()

	base, err := bs.buildNetwork(ctx, req.Base.KNID, req.Base.Branch)
	if err != nil {
		return nil, err
	}
	target, err := bs.buildNetwork(ctx, req.Target.KNID, req.Target.Branch)
	if err != nil {
		return nil, err
	}

	diff := bknsdk.DiffNetworkModels(base, target, bknsdk.DiffOptions{FallbackByName: req.FallbackByName})

	logger.Debugf("BKN DiffNetworks Completed: created=%d updated=%d deleted=%d unchanged=%d common_ids=%d",
		diff.Summary.Created, diff.Summary.Updated, diff.Summary.Deleted, diff.Summary.Unchanged,
		diff.Lineage.CommonIDs)
	span.SetStatus(codes.Ok, "")
	return &interfaces.KNDiffResult{
		Base:        interfaces.KNDiffSide{KNID: req.Base.KNID, Branch: req.Base.Branch, Name: base.Name},
		Target:      interfaces.KNDiffSide{KNID: req.Target.KNID, Branch: req.Target.Branch, Name: target.Name},
		NetworkDiff: diff,
	}, nil
}
