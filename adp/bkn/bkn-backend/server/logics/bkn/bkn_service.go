// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"bytes"
	"context"
	"net/http"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"go.opentelemetry.io/otel/codes"

	bknsdk "bkn-backend/bkn-specification/bkn"
	"bkn-backend/common"
	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
	"bkn-backend/logics"
	"bkn-backend/logics/capability_binding"
	"bkn-backend/logics/knowledge_network"
)

var (
	bServiceOnce sync.Once
	bService     interfaces.BKNService
)

type bknService struct {
	appSetting *common.AppSetting
	kns        interfaces.KNService
	cbs        interfaces.CapabilityBindingService
}

// NewBKNService creates the BKN service.
func NewBKNService(appSetting *common.AppSetting) interfaces.BKNService {
	bServiceOnce.Do(func() {
		bService = &bknService{
			appSetting: appSetting,
			kns:        knowledge_network.NewKNService(appSetting),
			cbs:        capability_binding.NewCapabilityBindingService(appSetting),
		}
	})
	return bService
}

// ExportToTar exports a knowledge network as a tar archive.
func (bs *bknService) ExportToTar(ctx context.Context, knID string, branch string) ([]byte, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "BKN导出为Tar")
	defer span.End()

	logger.Debugf("BKN ExportToTar Start: kn_id=%s", knID)

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

	// Capability bindings ride along as a dependency declaration. An export that dropped them
	// would move a knowledge network to another environment with its Skills and functions
	// silently unbound, and nothing in the file to say they were ever there.
	capabilities, err := bs.exportCapabilities(ctx, knID, branch)
	if err != nil {
		return nil, err
	}
	bknNetwork.Capabilities = capabilities

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

// exportCapabilities reads the bindings of a branch and turns them into a dependency declaration.
//
// Both the ids and the names are written. Ids are generated per environment and match only at
// home; names are the sole thing an import elsewhere can resolve against, and a section carrying
// ids alone would be decoration everywhere but the environment that produced it.
func (bs *bknService) exportCapabilities(ctx context.Context, knID, branch string) (*bknsdk.BknCapabilities, error) {
	// No paging: the export is the whole model, and a page of the bindings would quietly ship a
	// subset of what the network depends on.
	list, err := bs.cbs.ListCapabilities(ctx, interfaces.CapabilityBindingsQueryParams{
		KNID:   knID,
		Branch: branch,
	})
	if err != nil {
		return nil, err
	}
	if list == nil || len(list.Entries) == 0 {
		return nil, nil
	}
	// Names are half the identity and the only half another environment can resolve. When the
	// execution factory could not be reached they are all empty, and the section would still look
	// well-formed while resolving to nothing on import. Failing the export says so instead.
	if !list.MetadataAvailable {
		return nil, rest.NewHTTPError(ctx, http.StatusServiceUnavailable,
			berrors.BknBackend_CapabilityBinding_ExportMetadataUnavailable).
			WithErrorDetails("capability names are unavailable, so the exported dependency section would not resolve elsewhere")
	}

	capabilities := &bknsdk.BknCapabilities{}
	for _, binding := range list.Entries {
		// A binding whose target is gone has no name to carry, and writing its id alone would
		// only reappear as a not_found skip wherever the file is imported.
		if binding.Status == interfaces.CAPABILITY_STATUS_MISSING {
			continue
		}
		switch binding.CapabilityType {
		case interfaces.CAPABILITY_TYPE_SKILL:
			capabilities.Skills = append(capabilities.Skills, &bknsdk.BknCapabilitySkill{
				ID:   binding.CapabilityID,
				Name: binding.Name,
			})
		case interfaces.CAPABILITY_TYPE_FUNCTION:
			capabilities.Functions = append(capabilities.Functions, &bknsdk.BknCapabilityFunction{
				BoxID:    binding.OwnerID,
				ToolID:   binding.CapabilityID,
				BoxName:  binding.OwnerName,
				ToolName: binding.Name,
			})
		}
	}
	if len(capabilities.Skills) == 0 && len(capabilities.Functions) == 0 {
		return nil, nil
	}
	return capabilities, nil
}
