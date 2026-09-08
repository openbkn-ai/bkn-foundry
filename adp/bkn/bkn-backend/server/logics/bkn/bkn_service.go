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

	bknNetwork, err := bs.buildNetwork(ctx, knID, branch)
	if err != nil {
		return nil, err
	}

	// Capability bindings ride along as a dependency declaration. An export that dropped them
	// would move a knowledge network to another environment with its Skills and functions
	// silently unbound, and nothing in the file to say they were ever there.
	//
	// They are added here rather than in buildNetwork: the model differ does not compare them,
	// and reading them fails when the execution factory is unreachable, so assembling them for a
	// comparison would only give it a way to fail that has nothing to do with the two models.
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

	diff := bknsdk.DiffNetworkModels(base, target, bknsdk.DiffOptions{
		FallbackByName:   req.FallbackByName,
		IncludeUnchanged: req.IncludeUnchanged,
	})

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
		// Only what someone mounted. A capability that is in the list because an object type or
		// an action type uses it has no binding of its own, and the model file already carries
		// that object type or action type — exporting it here as a dependency would turn a
		// reference into an explicit mount on the next import, which is not what the source
		// network had.
		if binding.ID == "" {
			continue
		}
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
		case interfaces.CAPABILITY_TYPE_MCP_TOOL:
			// The tool travels by name only: MCP addresses tools by name, so there is no id to
			// fall back from. The server carries both halves, like a tool box does.
			capabilities.MCPTools = append(capabilities.MCPTools, &bknsdk.BknCapabilityMCPTool{
				MCPID:    binding.OwnerID,
				MCPName:  binding.OwnerName,
				ToolName: binding.CapabilityID,
			})
		}
	}
	if len(capabilities.Skills) == 0 && len(capabilities.Functions) == 0 &&
		len(capabilities.MCPTools) == 0 {
		return nil, nil
	}
	return capabilities, nil
}
