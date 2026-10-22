// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"go.opentelemetry.io/otel/codes"

	bknsdk "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/bkn-specification/bkn"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/capability_binding"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/knowledge_network"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/permission"
)

var (
	bServiceOnce sync.Once
	bService     interfaces.BKNService
)

type bknService struct {
	appSetting *common.AppSetting
	kns        interfaces.KNService
	cbs        interfaces.CapabilityBindingService
	archive    interfaces.ObjectMetricArchiveAccessV1
	ps         interfaces.PermissionService
}

// NewBKNService creates the BKN service.
func NewBKNService(appSetting *common.AppSetting) interfaces.BKNService {
	bServiceOnce.Do(func() {
		bService = &bknService{
			appSetting: appSetting,
			kns:        knowledge_network.NewKNService(appSetting),
			cbs:        capability_binding.NewCapabilityBindingService(appSetting),
			ps:         permission.NewPermissionService(appSetting),
		}
	})
	return bService
}

// ExportToTar exports a knowledge network as a tar archive.
//
// A network without a 3.0 declaration is written as 2.x, byte for byte as before, unless 3.0 is asked
// for. A network with one is written as 3.0, because a 2.x package cannot hold it, unless 2.0 is
// asked for, which leaves the declarations out. A 3.0 export first runs the network check: a
// misplaced parameter would be refused when the package is imported again, so the export is refused
// with the parameters to fix.
func (bs *bknService) ExportToTar(ctx context.Context, knID string, branch string, spec string) ([]byte, error) {
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

	v3 := spec == interfaces.BKN_EXPORT_SPEC_3 || (spec == interfaces.BKN_EXPORT_SPEC_AUTO && bknsdk.HasV3Declarations(bknNetwork))
	if v3 {
		if found := checkResult(bknNetwork); found.NeedsFix() {
			return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_KnowledgeNetwork_ExportNeedsNetworkCheck).
				WithErrorDetails(found)
		}
		bknNetwork.BknSpec = bknsdk.BknSpecV3
	}

	var buf bytes.Buffer
	err = bknsdk.WriteNetworkToTar(bknNetwork, &buf)
	if err != nil {
		otellog.LogError(ctx, "BKN ExportToTar failed", err)
		return nil, err
	}
	tarData := buf.Bytes()

	// A 3.0 package is read back by the 3.0 rules before it leaves. The check above covers the
	// parameters; this covers everything else stored data might hold that 3.0 refuses, so the
	// refusal comes now, with its reason, instead of at the next import.
	if v3 {
		if _, err := bknsdk.LoadNetworkFromTar(bytes.NewReader(tarData)); err != nil {
			return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_KnowledgeNetwork_ExportNotReadableAsV3).
				WithErrorDetails(err.Error())
		}
	}

	logger.Debugf("BKN ExportToTar Completed: size=%d", len(tarData))
	span.SetStatus(codes.Ok, "")
	return tarData, nil
}

// CheckNetwork checks the definitions of a knowledge network branch without changing anything. It
// works on any network: the parameters it finds are as misplaced in a 2.x network, where execution
// drops them.
func (bs *bknService) CheckNetwork(ctx context.Context, knID string, branch string) (*interfaces.NetworkCheckResult, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "BKN network check")
	defer span.End()

	bknNetwork, err := bs.buildNetwork(ctx, knID, branch)
	if err != nil {
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return checkResult(bknNetwork), nil
}

func checkResult(network *bknsdk.BknNetwork) *interfaces.NetworkCheckResult {
	result := &interfaces.NetworkCheckResult{
		MisplacedParameters: []interfaces.NetworkCheckParameter{},
		InvalidParameters:   []interfaces.NetworkCheckInvalidParameter{},
	}
	for _, m := range bknsdk.FindMisplacedParameters(network) {
		result.MisplacedParameters = append(result.MisplacedParameters, interfaces.NetworkCheckParameter{
			ActionTypeID:  m.ActionTypeID,
			ObjectTypeID:  m.ObjectTypeID,
			LogicProperty: m.LogicProperty,
			Parameter:     m.Parameter,
			Current:       interfaces.NetworkCheckParameterValue{Source: m.Current.Source, ValueFrom: m.Current.ValueFrom, Value: m.Current.Value},
			Suggested:     interfaces.NetworkCheckParameterValue{Source: m.Suggested.Source, ValueFrom: m.Suggested.ValueFrom, Value: m.Suggested.Value},
			Conflict:      m.Conflict,
		})
	}
	for _, p := range bknsdk.FindInvalidParameters(network) {
		result.InvalidParameters = append(result.InvalidParameters, interfaces.NetworkCheckInvalidParameter{
			ActionTypeID:  p.ActionTypeID,
			ObjectTypeID:  p.ObjectTypeID,
			LogicProperty: p.LogicProperty,
			Parameter:     p.Parameter,
			Current:       interfaces.NetworkCheckParameterValue{Source: p.Current.Source, ValueFrom: p.Current.ValueFrom, Value: p.Current.Value},
		})
	}
	return result
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
	archive := bs.archive
	if archive == nil {
		archive, _ = logics.OMA.(interfaces.ObjectMetricArchiveAccessV1)
	}
	if archive != nil {
		records, err := archive.ListObjectMetricArchive(ctx, knID, branch)
		if err != nil {
			return nil, err
		}
		records, err = bs.filterVisibleObjectMetricArchive(ctx, knID, bknNetwork.ObjectTypes, records)
		if err != nil {
			return nil, err
		}
		for _, record := range records {
			data, err := json.Marshal(record)
			if err != nil {
				return nil, fmt.Errorf("marshal object metric archive: %w", err)
			}
			bknNetwork.ObjectMetrics = append(bknNetwork.ObjectMetrics, data)
		}
	}

	return bknNetwork, nil
}

func (bs *bknService) filterVisibleObjectMetricArchive(ctx context.Context, knID string,
	objectTypes []*bknsdk.BknObjectType, records []*interfaces.ObjectMetricRecordV1) ([]*interfaces.ObjectMetricRecordV1, error) {

	if len(records) == 0 {
		return records, nil
	}
	metricIDs := make([]string, 0, len(records))
	seenMetricIDs := make(map[string]struct{}, len(records))
	for _, record := range records {
		if record == nil {
			continue
		}
		metricID := record.Definition.ID
		if _, seen := seenMetricIDs[metricID]; !seen {
			seenMetricIDs[metricID] = struct{}{}
			metricIDs = append(metricIDs, metricID)
		}
	}
	visibleMetricIDs, err := permission.FilterKNChildIDs(ctx, bs.ps, interfaces.RESOURCE_TYPE_METRIC,
		knID, metricIDs, interfaces.OPERATION_TYPE_VIEW_DETAIL)
	if err != nil {
		return nil, err
	}
	visibleMetrics := make(map[string]struct{}, len(visibleMetricIDs))
	for _, metricID := range visibleMetricIDs {
		visibleMetrics[metricID] = struct{}{}
	}
	visibleOwners := make(map[string]struct{}, len(objectTypes))
	for _, objectType := range objectTypes {
		if objectType != nil {
			visibleOwners[objectType.ID] = struct{}{}
		}
	}
	filtered := make([]*interfaces.ObjectMetricRecordV1, 0, len(records))
	for _, record := range records {
		if record == nil {
			continue
		}
		if _, visible := visibleMetrics[record.Definition.ID]; !visible {
			continue
		}
		if _, ownerVisible := visibleOwners[record.Definition.OwnerObjectTypeID]; !ownerVisible {
			continue
		}
		filtered = append(filtered, record)
	}
	return filtered, nil
}

// DiffNetworks compares two knowledge network branches and reports what differs.
//
// Both sides are loaded through GetKNByID, so each is authorized on its own: a caller who may read
// one network and not the other is refused exactly as they would be asking for that network
// directly, and a comparison cannot be used to read a network sideways.
func (bs *bknService) DiffNetworks(ctx context.Context, req interfaces.KNDiffRequest) (*interfaces.KNDiffResult, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Compare knowledge networks")
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
