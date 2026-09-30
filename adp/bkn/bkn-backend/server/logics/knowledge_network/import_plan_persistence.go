// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.

package knowledge_network

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	attr "go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	bknsdk "bkn-backend/bkn-specification/bkn"
	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
	"bkn-backend/logics"
	"bkn-backend/logics/permission"
)

const importPlanLookupBatchSize = 500

func (kns *knowledgeNetworkService) persistNormalizedImportPlan(ctx context.Context, tx *sql.Tx,
	plan *NormalizedImportPlan, mode string, strictMode bool) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Persist normalized knowledge network import")
	defer span.End()
	span.SetAttributes(
		attr.Int("concept_group_count", len(plan.ConceptGroups)),
		attr.Int("object_type_count", len(plan.ObjectTypes)),
		attr.Int("relation_type_count", len(plan.RelationTypes)),
		attr.Int("action_type_count", len(plan.ActionTypes)),
		attr.Int("risk_type_count", len(plan.RiskTypes)),
		attr.Int("metric_count", len(plan.Metrics)),
	)
	if len(plan.ConceptGroups) > 0 {
		if _, err := kns.cgs.CreateConceptGroups(ctx, tx, plan.ConceptGroups, mode, strictMode); err != nil {
			logger.Errorf("CreateConceptGroups error: %s", err.Error())
			return logics.PreserveHTTPError(ctx, err,
				berrors.BknBackend_KnowledgeNetwork_InternalError_CreateObjectTypesFailed)
		}
	}

	validGroups, err := kns.filterValidImportGroupReferences(ctx, tx, plan)
	if err != nil {
		return err
	}
	if len(plan.ObjectTypes) > 0 {
		if _, err = kns.ots.CreateObjectTypes(ctx, tx, plan.ObjectTypes, mode, false, strictMode); err != nil {
			logger.Errorf("CreateObjectTypes error: %s", err.Error())
			return logics.PreserveHTTPError(ctx, err,
				berrors.BknBackend_KnowledgeNetwork_InternalError_CreateObjectTypesFailed)
		}
	}
	if len(plan.RelationTypes) > 0 {
		if _, err = kns.rts.CreateRelationTypes(ctx, tx, plan.RelationTypes, mode, strictMode); err != nil {
			logger.Errorf("CreateRelationTypes error: %s", err.Error())
			return logics.PreserveHTTPError(ctx, err,
				berrors.BknBackend_KnowledgeNetwork_InternalError_CreateRelationTypesFailed)
		}
	}
	if len(plan.ActionTypes) > 0 {
		if _, err = kns.ats.CreateActionTypes(ctx, tx, plan.ActionTypes, mode, strictMode); err != nil {
			logger.Errorf("CreateActionTypes error: %s", err.Error())
			return logics.PreserveHTTPError(ctx, err,
				berrors.BknBackend_KnowledgeNetwork_InternalError_CreateActionTypesFailed)
		}
	}
	if len(plan.RiskTypes) > 0 {
		if _, err = kns.riskTypeS.CreateRiskTypes(ctx, tx, plan.RiskTypes, mode); err != nil {
			logger.Errorf("CreateRiskTypes error: %s", err.Error())
			return rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_RiskType_InternalError).WithErrorDetails(err.Error())
		}
	}
	if len(plan.Metrics) > 0 {
		if _, err = kns.ms.CreateMetrics(ctx, tx, plan.Metrics, strictMode, mode); err != nil {
			logger.Errorf("CreateMetrics error: %s", err.Error())
			return err
		}
	}
	if err = kns.restoreImportGroupMembers(ctx, tx, plan, validGroups, plan.validObjects); err != nil {
		return err
	}
	span.SetAttributes(
		attr.Int("restored_member_count", plan.RestoredMemberCount),
		attr.Int("invalid_member_count", plan.InvalidMemberCount),
	)
	span.SetStatus(codes.Ok, "")
	return nil
}

func (kns *knowledgeNetworkService) prepareNormalizedImportMembers(ctx context.Context,
	plan *NormalizedImportPlan, preserveExistingMembers bool) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Prepare knowledge network import members")
	defer span.End()
	valid := make(map[string]struct{}, len(plan.ObjectTypes))
	objectTypesByID := make(map[string]*interfaces.ObjectType, len(plan.ObjectTypes))
	for _, objectType := range plan.ObjectTypes {
		if objectType != nil && objectType.OTID != "" {
			// These definitions are persisted before membership restoration. Any failure to create or
			// update one aborts the transaction, so it is safe to treat it as a planned valid member.
			valid[objectType.OTID] = struct{}{}
			objectTypesByID[objectType.OTID] = objectType
		}
	}

	existingMembers := make(map[string][]string)
	if preserveExistingMembers && len(plan.ConceptGroups) > 0 {
		groupIDs := make([]string, 0, len(plan.ConceptGroups))
		for _, conceptGroup := range plan.ConceptGroups {
			groupIDs = append(groupIDs, conceptGroup.CGID)
		}
		var err error
		existingMembers, err = kns.cga.GetConceptIDsGroupedByConceptGroupIDs(ctx,
			plan.KNID, plan.Branch, groupIDs, interfaces.MODULE_TYPE_OBJECT_TYPE)
		if err != nil {
			return rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_ConceptGroup_InternalError_GetConceptIDsByConceptGroupIDsFailed).
				WithErrorDetails(err.Error())
		}
	}

	existingCandidates := make([]string, 0)
	seen := make(map[string]struct{})
	collectExistingCandidate := func(objectID string) {
		if objectID == "" {
			return
		}
		if _, planned := valid[objectID]; planned {
			return
		}
		if _, duplicate := seen[objectID]; duplicate {
			return
		}
		seen[objectID] = struct{}{}
		existingCandidates = append(existingCandidates, objectID)
	}
	for _, groupID := range plan.groupOrder {
		for _, objectID := range plan.GroupMembers[groupID] {
			collectExistingCandidate(objectID)
		}
	}
	for _, conceptGroup := range plan.ConceptGroups {
		for _, objectID := range existingMembers[conceptGroup.CGID] {
			collectExistingCandidate(objectID)
		}
	}
	for start := 0; start < len(existingCandidates); start += importPlanLookupBatchSize {
		end := min(start+importPlanLookupBatchSize, len(existingCandidates))
		objects, err := kns.ota.GetObjectTypesByIDs(ctx, nil,
			plan.KNID, plan.Branch, existingCandidates[start:end])
		if err != nil {
			return rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_ObjectType_InternalError_GetObjectTypesByIDsFailed).
				WithErrorDetails(err.Error())
		}
		for _, objectType := range objects {
			if objectType != nil {
				valid[objectType.OTID] = struct{}{}
				objectTypesByID[objectType.OTID] = objectType
			}
		}
	}

	for _, conceptGroup := range plan.ConceptGroups {
		requestedMembers, invalid := validImportMembers(plan.GroupMembers[conceptGroup.CGID], valid)
		conceptGroup.ObjectTypeIDs = mergeImportIDs(existingMembers[conceptGroup.CGID], requestedMembers)
		bknObjectTypes := make(map[string]*bknsdk.BknObjectType, len(conceptGroup.ObjectTypeIDs))
		for _, objectID := range conceptGroup.ObjectTypeIDs {
			if objectType := objectTypesByID[objectID]; objectType != nil {
				bknObjectTypes[objectID] = logics.ToBKNObjectType(objectType)
			}
		}
		conceptGroup.BKNRawContent = bknsdk.SerializeConceptGroup(
			logics.ToBKNConceptGroup(conceptGroup), bknObjectTypes)
		plan.InvalidMemberCount += len(invalid)
		logInvalidImportMembers(plan, conceptGroup.CGID, invalid)
	}
	plan.validObjects = valid
	span.SetAttributes(
		attr.Int("valid_object_count", len(valid)),
		attr.Int("invalid_member_count", plan.InvalidMemberCount),
	)
	span.SetStatus(codes.Ok, "")
	return nil
}

func (kns *knowledgeNetworkService) filterValidImportGroupReferences(ctx context.Context, tx *sql.Tx,
	plan *NormalizedImportPlan) (map[string]struct{}, error) {
	candidateIDs := make([]string, 0)
	seen := make(map[string]struct{})
	addCandidate := func(id string) {
		if id == "" {
			return
		}
		if _, duplicate := seen[id]; duplicate {
			return
		}
		seen[id] = struct{}{}
		candidateIDs = append(candidateIDs, id)
	}
	for _, groupID := range plan.groupOrder {
		members := plan.GroupMembers[groupID]
		if len(members) > 0 {
			addCandidate(groupID)
		}
	}
	for _, objectType := range plan.ObjectTypes {
		for _, group := range objectType.ConceptGroups {
			if group != nil {
				addCandidate(group.CGID)
			}
		}
	}

	valid := make(map[string]struct{}, len(candidateIDs))
	for start := 0; start < len(candidateIDs); start += importPlanLookupBatchSize {
		end := min(start+importPlanLookupBatchSize, len(candidateIDs))
		groups, err := kns.cga.GetConceptGroupsByIDs(ctx, tx,
			plan.KNID, plan.Branch, candidateIDs[start:end])
		if err != nil {
			return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_ConceptGroup_InternalError).WithErrorDetails(err.Error())
		}
		for _, group := range groups {
			valid[group.CGID] = struct{}{}
		}
	}

	for _, objectType := range plan.ObjectTypes {
		filtered := objectType.ConceptGroups[:0]
		for _, group := range objectType.ConceptGroups {
			if group == nil {
				continue
			}
			if _, exists := valid[group.CGID]; !exists {
				continue
			}
			filtered = append(filtered, group)
		}
		objectType.ConceptGroups = filtered
	}
	for _, groupID := range plan.groupOrder {
		if _, exists := valid[groupID]; !exists {
			members := plan.GroupMembers[groupID]
			logInvalidImportMembers(plan, groupID, members)
			for _, objectID := range members {
				if _, objectExists := plan.validObjects[objectID]; objectExists {
					plan.InvalidMemberCount++
				}
			}
		}
	}
	return valid, nil
}

func (kns *knowledgeNetworkService) restoreImportGroupMembers(ctx context.Context, tx *sql.Tx,
	plan *NormalizedImportPlan, validGroups, validObjects map[string]struct{}) error {
	targetGroupIDs := make([]string, 0, len(plan.groupOrder))
	validMembersByGroup := make(map[string][]string, len(plan.groupOrder))
	for _, groupID := range plan.groupOrder {
		members := plan.GroupMembers[groupID]
		if _, exists := validGroups[groupID]; !exists {
			continue
		}
		validMembers := make([]string, 0, len(members))
		for _, objectID := range members {
			if _, exists := validObjects[objectID]; !exists {
				continue
			}
			validMembers = append(validMembers, objectID)
		}
		if len(validMembers) == 0 {
			continue
		}
		targetGroupIDs = append(targetGroupIDs, groupID)
		validMembersByGroup[groupID] = validMembers
	}
	if len(targetGroupIDs) == 0 {
		return nil
	}
	if err := permission.ValidateKNChildAuthorizationIDs(ctx, plan.KNID, targetGroupIDs); err != nil {
		return err
	}
	if !permission.KNImportPermissionPrechecked(ctx) {
		if err := permission.CheckKNChildBatchPermission(ctx, kns.ps,
			interfaces.RESOURCE_TYPE_CONCEPT_GROUP, plan.KNID, targetGroupIDs,
			interfaces.OPERATION_TYPE_MODIFY); err != nil {
			return err
		}
	}
	existingMembers, err := kns.cga.GetConceptIDsGroupedByConceptGroupIDs(ctx,
		plan.KNID, plan.Branch, targetGroupIDs, interfaces.MODULE_TYPE_OBJECT_TYPE)
	if err != nil {
		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ConceptGroup_InternalError_GetConceptIDsByConceptGroupIDsFailed).
			WithErrorDetails(err.Error())
	}

	currentTime := time.Now().UnixMilli()
	relations := make([]*interfaces.ConceptGroupRelation, 0)
	for _, groupID := range targetGroupIDs {
		existing := make(map[string]struct{}, len(existingMembers[groupID]))
		for _, objectID := range existingMembers[groupID] {
			existing[objectID] = struct{}{}
		}
		for _, objectID := range validMembersByGroup[groupID] {
			if _, exists := existing[objectID]; exists {
				continue
			}
			relationID, generateErr := uuid.NewV7()
			if generateErr != nil {
				return fmt.Errorf("generate concept group relation UUIDv7: %w", generateErr)
			}
			relations = append(relations, &interfaces.ConceptGroupRelation{
				ID: relationID.String(), KNID: plan.KNID, Branch: plan.Branch, CGID: groupID,
				ConceptType: interfaces.MODULE_TYPE_OBJECT_TYPE, ConceptID: objectID,
				CreateTime: currentTime,
			})
		}
	}
	if len(relations) == 1 {
		err = kns.cga.CreateConceptGroupRelation(ctx, tx, relations[0])
	} else if len(relations) > 1 {
		err = kns.cga.CreateConceptGroupRelations(ctx, tx, relations)
	}
	if err != nil {
		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ConceptGroup_InternalError_CreateConceptGroupRelationFailed).
			WithErrorDetails(err.Error())
	}
	plan.RestoredMemberCount += len(relations)
	return nil
}

func validImportMembers(members []string, validObjects map[string]struct{}) ([]string, []string) {
	var valid []string
	var invalid []string
	for _, objectID := range members {
		if _, exists := validObjects[objectID]; exists {
			valid = append(valid, objectID)
		} else {
			invalid = append(invalid, objectID)
		}
	}
	return valid, invalid
}

func mergeImportIDs(existing, requested []string) []string {
	if len(existing) == 0 && len(requested) == 0 {
		return nil
	}
	merged := make([]string, 0, len(existing)+len(requested))
	seen := make(map[string]struct{}, cap(merged))
	for _, objectID := range append(append([]string(nil), existing...), requested...) {
		if objectID == "" {
			continue
		}
		if _, duplicate := seen[objectID]; duplicate {
			continue
		}
		seen[objectID] = struct{}{}
		merged = append(merged, objectID)
	}
	return merged
}

func logInvalidImportMembers(plan *NormalizedImportPlan, groupID string, objectIDs []string) {
	if len(objectIDs) == 0 {
		return
	}
	const maxLoggedIDs = 10
	limit := min(len(objectIDs), maxLoggedIDs)
	summary := strings.Join(objectIDs[:limit], ",")
	if len(objectIDs) > limit {
		summary = fmt.Sprintf("%s,...(+%d)", summary, len(objectIDs)-limit)
	}
	logger.Warnf("Skip invalid import concept-group members: kn_id=%s branch=%s cg_id=%s invalid_count=%d object_ids=%s",
		plan.KNID, plan.Branch, groupID, len(objectIDs), summary)
}
