// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package concept_group

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bytedance/sonic"
	"github.com/google/uuid"
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
	"bkn-backend/logics/action_type"
	"bkn-backend/logics/batchindex"
	"bkn-backend/logics/model_factory"
	"bkn-backend/logics/object_type"
	"bkn-backend/logics/permission"
	"bkn-backend/logics/relation_type"
	"bkn-backend/logics/user_mgmt"
	"bkn-backend/logics/vega_backend"
)

var (
	cgServiceOnce sync.Once
	cgService     interfaces.ConceptGroupService
)

type conceptGroupService struct {
	appSetting *common.AppSetting
	db         *sql.DB
	ata        interfaces.ActionTypeAccess
	ats        interfaces.ActionTypeService
	cga        interfaces.ConceptGroupAccess
	kna        interfaces.KNAccess
	mfs        interfaces.ModelFactoryService
	ota        interfaces.ObjectTypeAccess
	ots        interfaces.ObjectTypeService
	rta        interfaces.RelationTypeAccess
	ps         interfaces.PermissionService
	rts        interfaces.RelationTypeService
	ums        interfaces.UserMgmtService
	vbs        interfaces.VegaBackendService
}

func NewConceptGroupService(appSetting *common.AppSetting) interfaces.ConceptGroupService {
	cgServiceOnce.Do(func() {
		cgService = &conceptGroupService{
			appSetting: appSetting,
			ata:        logics.ATA,
			ats:        action_type.NewActionTypeService(appSetting),
			db:         logics.DB,
			cga:        logics.CGA,
			kna:        logics.KNA,
			mfs:        model_factory.NewModelFactoryService(appSetting, logics.MFA),
			ota:        logics.OTA,
			ots:        object_type.NewObjectTypeService(appSetting),
			ps:         permission.NewPermissionService(appSetting),
			rta:        logics.RTA,
			rts:        relation_type.NewRelationTypeService(appSetting),
			ums:        user_mgmt.NewUserMgmtService(appSetting),
			vbs:        vega_backend.NewVegaBackendService(appSetting, logics.VBA),
		}
	})
	return cgService
}

func (cgs *conceptGroupService) CheckConceptGroupExistByID(ctx context.Context, knID string, branch string,
	cgID string) (string, bool, error) {

	ctx, span := oteltrace.StartNamedInternalSpan(ctx, fmt.Sprintf("校验概念分组[%v]的存在性", cgID))
	defer span.End()

	cgName, exist, err := cgs.cga.CheckConceptGroupExistByID(ctx, knID, branch, cgID)
	if err != nil {
		otellog.LogError(ctx, fmt.Sprintf("按ID[%v]获取概念分组失败", knID), err)
		return "", exist, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ConceptGroup_InternalError_CheckConceptGroupIfExistFailed).WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return cgName, exist, nil
}

func (cgs *conceptGroupService) CheckConceptGroupExistByName(ctx context.Context, knID string, branch string, cgName string) (string, bool, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, fmt.Sprintf("校验概念分组[%v]的存在性", cgName))
	defer span.End()

	cgID, exist, err := cgs.cga.CheckConceptGroupExistByName(ctx, knID, branch, cgName)
	if err != nil {
		otellog.LogError(ctx, fmt.Sprintf("按名称[%v]获取概念分组失败", cgName), err)
		return cgID, exist, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ConceptGroup_InternalError_CheckConceptGroupIfExistFailed).WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return cgID, exist, nil
}

// Create concept groups.
func (cgs *conceptGroupService) CreateConceptGroup(ctx context.Context, tx *sql.Tx,
	conceptGroup *interfaces.ConceptGroup, mode string, strictMode bool) (id string, err error) {

	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Create concept group")
	defer span.End()
	ctx, parentTracker, trackerOwner := permission.WithResourceParentTracker(ctx)
	defer func() {
		if trackerOwner && err != nil {
			_ = parentTracker.Cleanup(ctx, cgs.ps)
		}
	}()
	ctx, policyTracker, policyTrackerOwner := permission.WithCreatedPolicyTracker(ctx)
	defer func() {
		if policyTrackerOwner && err != nil {
			_ = policyTracker.Cleanup(ctx, cgs.ps)
		}
	}()

	if !permission.KNImportPermissionPrechecked(ctx) {
		err = cgs.ps.CheckPermission(ctx, interfaces.PermissionResource{
			Type: interfaces.RESOURCE_TYPE_KN,
			ID:   conceptGroup.KNID,
		}, []string{interfaces.OPERATION_TYPE_MODIFY})
		if err != nil {
			return "", err
		}
	}

	currentTime := time.Now().UnixMilli()
	conceptGroup.CGID, err = permission.PrepareKNChildResourceID(ctx, conceptGroup.CGID)
	if err != nil {
		return "", err
	}
	if err = permission.ValidateKNChildAuthorizationIDs(ctx, conceptGroup.KNID, []string{conceptGroup.CGID}); err != nil {
		return "", err
	}
	otIDs := []interfaces.ID{}
	bknOtMap := map[string]*bknsdk.BknObjectType{}
	for _, objectType := range conceptGroup.ObjectTypes {
		objectType.KNID = conceptGroup.KNID
		objectType.Branch = conceptGroup.Branch

		otIDs = append(otIDs, interfaces.ID{ID: objectType.OTID})
		bknOtMap[objectType.OTID] = logics.ToBKNObjectType(objectType)
	}
	for _, relationType := range conceptGroup.RelationTypes {
		relationType.KNID = conceptGroup.KNID
		relationType.Branch = conceptGroup.Branch
	}
	for _, actionType := range conceptGroup.ActionTypes {
		actionType.KNID = conceptGroup.KNID
		actionType.Branch = conceptGroup.Branch
	}

	accountInfo := interfaces.AccountInfo{}
	if ctx.Value(interfaces.ACCOUNT_INFO_KEY) != nil {
		accountInfo = ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	}
	conceptGroup.Creator = accountInfo
	conceptGroup.Updater = accountInfo

	conceptGroup.CreateTime = currentTime
	conceptGroup.UpdateTime = currentTime

	bknCG := logics.ToBKNConceptGroup(conceptGroup)
	conceptGroup.BKNRawContent = bknsdk.SerializeConceptGroup(bknCG, bknOtMap)

	if tx == nil {
		// 0. Begin the transaction.
		tx, err = cgs.db.Begin()
		if err != nil {
			otellog.LogError(ctx, "Begin transaction error", err)
			return "", rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_ConceptGroup_InternalError_BeginTransactionFailed).
				WithErrorDetails(err.Error())
		}

		// 0.1 On failure.
		defer func() {
			switch err {
			case nil:
				// Commit the transaction.
				err = tx.Commit()
				if err != nil {
					otellog.LogError(ctx, "CreateConceptGroup Transaction Commit Failed", err)
					return
				}
				otellog.LogDebug(ctx, "CreateConceptGroup Transaction Commit Success")
			default:
				rollbackErr := tx.Rollback()
				if rollbackErr != nil {
					otellog.LogError(ctx, "CreateConceptGroup Transaction Rollback Error", err)
				}
			}
		}()
	}

	// Process import mode.
	isCreate, isUpdate, err := cgs.handleConceptGroupImportMode(ctx, mode, conceptGroup)
	if err != nil {
		return "", err
	}

	// Process creation.
	if isCreate {
		err = cgs.cga.CreateConceptGroup(ctx, tx, conceptGroup)
		if err != nil {
			logger.Errorf("CreateConceptGroup error: %s", err.Error())
			span.SetStatus(codes.Error, "创建概念分组失败")

			return "", rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_ConceptGroup_InternalError_CreateConceptGroupFailed).
				WithErrorDetails(err.Error())
		}
		parentItems := interfaces.KNChildResourceParents(conceptGroup.KNID, []string{conceptGroup.CGID})
		err = cgs.ps.UpsertResourceParents(ctx, interfaces.RESOURCE_TYPE_CONCEPT_GROUP,
			interfaces.RESOURCE_TYPE_KN, parentItems)
		if err != nil {
			return "", err
		}
		permission.TrackResourceParents(ctx, interfaces.RESOURCE_TYPE_CONCEPT_GROUP,
			interfaces.RESOURCE_TYPE_KN, parentItems)

		if len(conceptGroup.ObjectTypes) > 0 {
			_, err = cgs.ots.CreateObjectTypes(ctx, tx, conceptGroup.ObjectTypes, mode, false, strictMode)
			if err != nil {
				logger.Errorf("CreateObjectTypes error: %s", err.Error())
				span.SetStatus(codes.Error, "创建对象类失败")
				return "", rest.NewHTTPError(ctx, http.StatusInternalServerError,
					berrors.BknBackend_ConceptGroup_InternalError_CreateObjectTypesFailed).
					WithErrorDetails(err.Error())
			}

			// Import path: process group-to-concept relationships.
			_, err = cgs.AddObjectTypesToConceptGroup(ctx, tx, conceptGroup.KNID, conceptGroup.Branch, conceptGroup.CGID, otIDs, mode, strictMode)
			if err != nil {
				logger.Errorf("AddObjectTypesToConceptGroup error: %s", err.Error())
				span.SetStatus(codes.Error, "创建概念分组与对象类的关系失败")
				return "", rest.NewHTTPError(ctx, http.StatusInternalServerError,
					berrors.BknBackend_ConceptGroup_InternalError_AddObjectTypesToConceptGroupFailed).
					WithErrorDetails(err.Error())
			}
		}

		if len(conceptGroup.RelationTypes) > 0 {
			_, err = cgs.rts.CreateRelationTypes(ctx, tx, conceptGroup.RelationTypes, mode, strictMode)
			if err != nil {
				logger.Errorf("CreateRelationTypes error: %s", err.Error())
				span.SetStatus(codes.Error, "创建关系类失败")
				return "", rest.NewHTTPError(ctx, http.StatusInternalServerError,
					berrors.BknBackend_ConceptGroup_InternalError_CreateRelationTypesFailed).
					WithErrorDetails(err.Error())
			}
		}

		if len(conceptGroup.ActionTypes) > 0 {
			_, err = cgs.ats.CreateActionTypes(ctx, tx, conceptGroup.ActionTypes, mode, strictMode)
			if err != nil {
				logger.Errorf("CreateActionTypes error: %s", err.Error())
				span.SetStatus(codes.Error, "创建概念分组动作类失败")
				return "", rest.NewHTTPError(ctx, http.StatusInternalServerError,
					berrors.BknBackend_ConceptGroup_InternalError_CreateActionTypesFailed).
					WithErrorDetails(err.Error())
			}
		}
	}

	// Process updates.
	if isUpdate {
		err = cgs.UpdateConceptGroup(ctx, tx, conceptGroup, strictMode)
		if err != nil {
			logger.Errorf("UpdateConceptGroup error: %s", err.Error())
			span.SetStatus(codes.Error, "修改概念分组失败")
			return "", rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_ConceptGroup_InternalError_UpdateConceptGroupFailed).
				WithErrorDetails(err.Error())
		}

		if len(conceptGroup.ObjectTypes) > 0 {
			// Persist object types.
			_, err = cgs.ots.CreateObjectTypes(ctx, tx, conceptGroup.ObjectTypes, mode, false, strictMode)
			if err != nil {
				logger.Errorf("CreateObjectTypes error: %s", err.Error())
				span.SetStatus(codes.Error, "创建对象类失败")
				return "", rest.NewHTTPError(ctx, http.StatusInternalServerError,
					berrors.BknBackend_ConceptGroup_InternalError_CreateObjectTypesFailed).
					WithErrorDetails(err.Error())
			}
			// Import path: create only relationships between this group and current object types.
			// Updating groups requires full synchronization.
			_, err = cgs.AddObjectTypesToConceptGroup(ctx, tx, conceptGroup.KNID, conceptGroup.Branch, conceptGroup.CGID, otIDs, mode, strictMode)
			if err != nil {
				logger.Errorf("AddObjectTypesToConceptGroup error: %s", err.Error())
				span.SetStatus(codes.Error, "创建概念分组与对象类的关系失败")
				return "", rest.NewHTTPError(ctx, http.StatusInternalServerError,
					berrors.BknBackend_ConceptGroup_InternalError_AddObjectTypesToConceptGroupFailed).
					WithErrorDetails(err.Error())
			}
		}

		if len(conceptGroup.RelationTypes) > 0 {
			_, err = cgs.rts.CreateRelationTypes(ctx, tx, conceptGroup.RelationTypes, mode, strictMode)
			if err != nil {
				logger.Errorf("CreateRelationTypes error: %s", err.Error())
				span.SetStatus(codes.Error, "创建关系类失败")
				return "", rest.NewHTTPError(ctx, http.StatusInternalServerError,
					berrors.BknBackend_ConceptGroup_InternalError_CreateRelationTypesFailed).
					WithErrorDetails(err.Error())
			}
		}

		if len(conceptGroup.ActionTypes) > 0 {
			_, err = cgs.ats.CreateActionTypes(ctx, tx, conceptGroup.ActionTypes, mode, strictMode)
			if err != nil {
				logger.Errorf("CreateActionTypes error: %s", err.Error())
				span.SetStatus(codes.Error, "创建动作类失败")
				return "", rest.NewHTTPError(ctx, http.StatusInternalServerError,
					berrors.BknBackend_ConceptGroup_InternalError_CreateActionTypesFailed).
					WithErrorDetails(err.Error())
			}
		}
	}

	if isCreate || isUpdate {
		err = cgs.InsertDatasetData(ctx, conceptGroup)
		if err != nil {
			logger.Errorf("InsertDatasetData error: %s", err.Error())
			span.SetStatus(codes.Error, "概念分组概念索引写入失败")

			return "", rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_ConceptGroup_InternalError_InsertOpenSearchDataFailed).
				WithErrorDetails(err.Error())
		}
	}

	span.SetStatus(codes.Ok, "")
	return conceptGroup.CGID, nil
}

// ValidateConceptGroups checks concept-group dependency existence only; does not write to the database.
func (cgs *conceptGroupService) ValidateConceptGroups(ctx context.Context, knID string, branch string,
	conceptGroups []*interfaces.ConceptGroup, strictMode bool, parentBatch *interfaces.BatchIDIndex, mode string) error {

	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "ValidateConceptGroups")
	defer span.End()

	if len(conceptGroups) == 0 {
		span.SetStatus(codes.Ok, "")
		return nil
	}

	err := cgs.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: interfaces.RESOURCE_TYPE_KN,
		ID:   knID,
	}, []string{interfaces.OPERATION_TYPE_MODIFY})
	if err != nil {
		return err
	}

	effectiveBatch := parentBatch
	if effectiveBatch == nil {
		effectiveBatch, err = batchindex.CollectFromConceptGroups(knID, branch, conceptGroups)
		if err != nil {
			return rest.NewHTTPError(ctx, http.StatusBadRequest,
				berrors.BknBackend_ConceptGroup_InvalidParameter).
				WithErrorDetails(err.Error())
		}
	}

	for _, cg := range conceptGroups {
		cg.KNID = knID
		cg.Branch = branch
		if _, _, err := cgs.handleConceptGroupImportMode(ctx, mode, cg); err != nil {
			return err
		}
		if strictMode {
			// Align with CreateConceptGroup persistence: preflight dependencies for nested concept synchronization.
			if len(cg.ObjectTypes) > 0 {
				// Align with CreateObjectTypes strict validation: validate data views, logical properties, and bound concept groups rather than only persisted IDs.
				if err := cgs.ots.ValidateObjectTypes(ctx, knID, branch, cg.ObjectTypes, strictMode, effectiveBatch, mode); err != nil {
					return err
				}
			}
			if len(cg.RelationTypes) > 0 {
				if err := cgs.rts.ValidateRelationTypes(ctx, knID, branch, cg.RelationTypes, strictMode, effectiveBatch, mode); err != nil {
					return err
				}
			}
			if len(cg.ActionTypes) > 0 {
				if err := cgs.ats.ValidateActionTypes(ctx, knID, branch, cg.ActionTypes, strictMode, effectiveBatch, mode); err != nil {
					return err
				}
			}
		}
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

func (cgs *conceptGroupService) ListConceptGroups(ctx context.Context,
	query interfaces.ConceptGroupsQueryParams) ([]*interfaces.ConceptGroup, int, error) {

	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "查询概念分组列表")
	defer span.End()
	pepEnabled := permission.KNChildResourcePEPEnabled()
	if !pepEnabled {
		if err := cgs.ps.CheckPermission(ctx, interfaces.PermissionResource{
			Type: interfaces.RESOURCE_TYPE_KN,
			ID:   query.KNID,
		}, []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}); err != nil {
			return []*interfaces.ConceptGroup{}, 0, err
		}
	}

	listQuery := query
	if pepEnabled {
		listQuery.Offset = 0
		listQuery.Limit = -1
	}
	conceptGroups, err := cgs.cga.ListConceptGroups(ctx, listQuery)
	if err != nil {
		logger.Errorf("ListConceptGroups error: %s", err.Error())
		span.SetStatus(codes.Error, "List concept groups error")

		return []*interfaces.ConceptGroup{}, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ConceptGroup_InternalError).WithErrorDetails(err.Error())
	}

	var total int
	if pepEnabled {
		conceptGroups, total, err = permission.FilterAndPaginateKNChildren(ctx, cgs.ps,
			interfaces.RESOURCE_TYPE_CONCEPT_GROUP, query.KNID, conceptGroups,
			func(group *interfaces.ConceptGroup) string { return group.CGID }, query.Offset, query.Limit)
		if err != nil {
			return []*interfaces.ConceptGroup{}, 0, err
		}
	} else {
		total, err = cgs.cga.GetConceptGroupsTotal(ctx, query)
		if err != nil {
			logger.Errorf("GetConceptGroupsTotal error: %s", err.Error())
			span.SetStatus(codes.Error, "Get concept groups total error")
			return []*interfaces.ConceptGroup{}, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_ConceptGroup_InternalError).WithErrorDetails(err.Error())
		}
	}
	if len(conceptGroups) == 0 {
		span.SetStatus(codes.Ok, "")
		return []*interfaces.ConceptGroup{}, total, nil
	}

	accountInfos := make([]*interfaces.AccountInfo, 0, len(conceptGroups)*2)
	for _, cg := range conceptGroups {
		accountInfos = append(accountInfos, &cg.Creator, &cg.Updater)
	}

	err = cgs.ums.GetAccountNames(ctx, accountInfos)
	if err != nil {
		span.SetStatus(codes.Error, "GetAccountNames error")

		return []*interfaces.ConceptGroup{}, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ConceptGroup_InternalError).WithErrorDetails(err.Error())
	}

	// Generate concept statistics for every group in the list.
	for _, conceptGroup := range conceptGroups {
		otIDs, err := cgs.cga.GetConceptIDsByConceptGroupIDs(ctx, conceptGroup.KNID,
			conceptGroup.Branch, []string{conceptGroup.CGID}, interfaces.MODULE_TYPE_OBJECT_TYPE)
		if err != nil {
			errStr := fmt.Sprintf("GetConceptIDsByConceptGroupIDs failed, kn_id:[%s],branch:[%s],cg_ids:[%s], error: %v",
				conceptGroup.KNID, conceptGroup.Branch, conceptGroup.CGID, err)
			logger.Errorf(errStr)
			span.SetStatus(codes.Error, errStr)

			return []*interfaces.ConceptGroup{}, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_ConceptGroup_InternalError).WithErrorDetails(err.Error())
		}
		conceptGroup.ObjectTypeIDs = otIDs

		stats, err := cgs.getStatByObjectTypeIDs(ctx, conceptGroup, otIDs)
		if err != nil {
			return []*interfaces.ConceptGroup{}, 0, err
		}
		conceptGroup.Statistics = stats
	}

	span.SetStatus(codes.Ok, "")
	return conceptGroups, total, nil
}

func (cgs *conceptGroupService) GetConceptGroupByID(ctx context.Context, knID string, branch string,
	cgID string, mode string) (*interfaces.ConceptGroup, error) {

	// Get concept groups.
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, fmt.Sprintf("查询概念分组[%s]信息", knID))
	defer span.End()

	if err := permission.ValidateKNChildPEPAuthorizationIDs(ctx, knID, []string{cgID}); err != nil {
		return nil, err
	}

	// Get basic model information.
	conceptGroup, err := cgs.cga.GetConceptGroupByID(ctx, knID, branch, cgID)
	if err != nil {
		logger.Errorf("GetConceptGroupByID error: %s", err.Error())
		span.SetStatus(codes.Error, fmt.Sprintf("Get concept group[%s] error: %v", knID, err))
		span.End()

		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ConceptGroup_InternalError_GetConceptGroupByIDFailed).WithErrorDetails(err.Error())
	}

	if conceptGroup == nil {
		errStr := fmt.Sprintf("Concept group[%s] not found in knowledge network [%s] branch [%s]", cgID, knID, branch)
		logger.Errorf(errStr)
		span.SetStatus(codes.Error, errStr)
		span.End()

		return nil, rest.NewHTTPError(ctx, http.StatusNotFound, berrors.BknBackend_ConceptGroup_ConceptGroupNotFound).
			WithErrorDetails(errStr)
	}
	resource, operation := permission.ResolveKNChildPermissionTarget(interfaces.RESOURCE_TYPE_CONCEPT_GROUP,
		knID, cgID, interfaces.OPERATION_TYPE_VIEW_DETAIL, interfaces.OPERATION_TYPE_VIEW_DETAIL)
	if err = cgs.ps.CheckPermission(ctx, resource, []string{operation}); err != nil {
		return nil, err
	}

	otIDs, err := cgs.cga.GetConceptIDsByConceptGroupIDs(ctx, conceptGroup.KNID,
		conceptGroup.Branch, []string{conceptGroup.CGID}, interfaces.MODULE_TYPE_OBJECT_TYPE)
	if err != nil {
		errStr := fmt.Sprintf("GetConceptIDsByConceptGroupIDs failed, kn_id:[%s],branch:[%s],cg_ids:[%s], error: %v",
			conceptGroup.KNID, conceptGroup.Branch, conceptGroup.CGID, err)
		logger.Errorf(errStr)
		span.SetStatus(codes.Error, errStr)
		span.End()

		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ConceptGroup_InternalError_GetConceptIDsByConceptGroupIDsFailed).WithErrorDetails(err.Error())
	}

	// Find related relation types only when object types are present.
	if len(otIDs) > 0 {
		objectTypes, _, err := cgs.ots.ListObjectTypes(ctx, nil, interfaces.ObjectTypesQueryParams{
			PaginationQueryParameters: interfaces.PaginationQueryParameters{
				Limit: -1, // -1 returns every entry found in storage.
			},
			KNID:   conceptGroup.KNID,
			Branch: conceptGroup.Branch,
			OTIDS:  otIDs,
		})
		if err != nil {
			return nil, err
		}
		conceptGroup.ObjectTypes = objectTypes

		relationTypes, _, err := cgs.rts.ListRelationTypes(ctx, interfaces.RelationTypesQueryParams{
			PaginationQueryParameters: interfaces.PaginationQueryParameters{
				Limit: -1,
			},
			KNID:                conceptGroup.KNID,
			Branch:              conceptGroup.Branch,
			SourceObjectTypeIDs: otIDs,
			TargetObjectTypeIDs: otIDs,
		})
		if err != nil {
			return nil, err
		}
		conceptGroup.RelationTypes = relationTypes

		actionTypes, _, err := cgs.ats.ListActionTypes(ctx, interfaces.ActionTypesQueryParams{
			PaginationQueryParameters: interfaces.PaginationQueryParameters{
				Limit: -1,
			},
			KNID:          conceptGroup.KNID,
			Branch:        conceptGroup.Branch,
			ObjectTypeIDs: otIDs,
		})
		if err != nil {
			return nil, err
		}
		conceptGroup.ActionTypes = actionTypes
	}

	span.SetStatus(codes.Ok, "")
	return conceptGroup, nil
}

func (cgs *conceptGroupService) GetConceptGroupIDsByKnID(ctx context.Context, knID string, branch string) ([]string, error) {
	// Get concept groups.
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, fmt.Sprintf("查询概念分组[%s]信息", knID))
	defer span.End()

	// Get basic model information.
	cgIDs, err := cgs.cga.GetConceptGroupIDsByKnID(ctx, knID, branch)
	if err != nil {
		logger.Errorf("GetConceptGroupIDsByKnID error: %s", err.Error())
		span.SetStatus(codes.Error, fmt.Sprintf("Get concept group ids by kn_id[%s] error: %v", knID, err))
		span.End()

		return []string{}, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ConceptGroup_InternalError).WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return cgIDs, nil
}

// Get concept group statistics.
func (cgs *conceptGroupService) GetStatByConceptGroup(ctx context.Context, conceptGroup *interfaces.ConceptGroup) (*interfaces.Statistics, error) {
	// Get concept groups.
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, fmt.Sprintf("查询概念分组[%s]信息", conceptGroup.KNID))
	defer span.End()

	// Counts are obtained by joining object types, concept-object relationships, and concept groups.
	// Get counts of object, relation, and action types in a concept group.

	otIDs, err := cgs.cga.GetConceptIDsByConceptGroupIDs(ctx, conceptGroup.KNID,
		conceptGroup.Branch, []string{conceptGroup.CGID}, interfaces.MODULE_TYPE_OBJECT_TYPE)
	if err != nil {
		errStr := fmt.Sprintf("GetConceptIDsByConceptGroupIDs failed, kn_id:[%s],branch:[%s],cg_ids:[%s], error: %v",
			conceptGroup.KNID, conceptGroup.Branch, conceptGroup.CGID, err)
		logger.Errorf(errStr)
		span.SetStatus(codes.Error, errStr)
		span.End()

		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ConceptGroup_InternalError_GetConceptIDsByConceptGroupIDsFailed).WithErrorDetails(err.Error())
	}

	return cgs.getStatByObjectTypeIDs(ctx, conceptGroup, otIDs)
}

func (cgs *conceptGroupService) getStatByObjectTypeIDs(ctx context.Context,
	conceptGroup *interfaces.ConceptGroup, otIDs []string) (*interfaces.Statistics, error) {

	ctx, span := oteltrace.StartNamedInternalSpan(ctx, fmt.Sprintf("查询概念分组[%s]统计信息", conceptGroup.KNID))
	defer span.End()

	if len(otIDs) == 0 {
		return &interfaces.Statistics{
			OtTotal: 0,
			RtTotal: 0,
			AtTotal: 0,
		}, nil
	}

	// Relation type count.
	rtCnt, err := cgs.rta.GetRelationTypesTotal(ctx, interfaces.RelationTypesQueryParams{
		KNID:                conceptGroup.KNID,
		Branch:              conceptGroup.Branch,
		SourceObjectTypeIDs: otIDs,
		TargetObjectTypeIDs: otIDs,
	})
	if err != nil {
		logger.Errorf("GetRelationTypesTotal in concept group[%s] error: %s", conceptGroup.KNID, err.Error())
		span.SetStatus(codes.Error, fmt.Sprintf("GetRelationTypesTotal in concept group[%s], error: %v", conceptGroup.KNID, err))
		span.End()

		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ConceptGroup_InternalError_GetRelationTypesTotalFailed).WithErrorDetails(err.Error())
	}

	// Action type count.
	atCnt, err := cgs.ata.GetActionTypesTotal(ctx, interfaces.ActionTypesQueryParams{
		KNID:          conceptGroup.KNID,
		Branch:        conceptGroup.Branch,
		ObjectTypeIDs: otIDs,
	})
	if err != nil {
		logger.Errorf("GetActionTypesTotal in concept group[%s] error: %s", conceptGroup.KNID, err.Error())
		span.SetStatus(codes.Error, fmt.Sprintf("GetActionTypesTotal in concept group[%s], error: %v", conceptGroup.KNID, err))
		span.End()

		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ConceptGroup_InternalError_GetRelationTypesTotalFailed).WithErrorDetails(err.Error())
	}

	statistics := &interfaces.Statistics{
		OtTotal: len(otIDs),
		RtTotal: rtCnt,
		AtTotal: atCnt,
	}

	span.SetStatus(codes.Ok, "")
	return statistics, nil
}

// Update concept groups.
func (cgs *conceptGroupService) UpdateConceptGroup(ctx context.Context, tx *sql.Tx, conceptGroup *interfaces.ConceptGroup, strictMode bool) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Update concept group")
	defer span.End()

	if err := permission.ValidateKNChildPEPAuthorizationIDs(ctx, conceptGroup.KNID, []string{conceptGroup.CGID}); err != nil {
		return err
	}
	_, exists, err := cgs.cga.CheckConceptGroupExistByID(ctx, conceptGroup.KNID, conceptGroup.Branch, conceptGroup.CGID)
	if err != nil {
		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ConceptGroup_InternalError_CheckConceptGroupIfExistFailed).WithErrorDetails(err.Error())
	}
	if !exists {
		return rest.NewHTTPError(ctx, http.StatusNotFound, berrors.BknBackend_ConceptGroup_ConceptGroupNotFound)
	}
	resource, operation := permission.ResolveKNChildPermissionTarget(interfaces.RESOURCE_TYPE_CONCEPT_GROUP,
		conceptGroup.KNID, conceptGroup.CGID, interfaces.OPERATION_TYPE_MODIFY, interfaces.OPERATION_TYPE_MODIFY)
	err = cgs.ps.CheckPermission(ctx, resource, []string{operation})
	if err != nil {
		return err
	}

	if strictMode {
		if err := cgs.ValidateConceptGroups(ctx, conceptGroup.KNID, conceptGroup.Branch,
			[]*interfaces.ConceptGroup{conceptGroup}, strictMode, nil, interfaces.ImportMode_Overwrite); err != nil {
			return err
		}
	}

	accountInfo := interfaces.AccountInfo{}
	if ctx.Value(interfaces.ACCOUNT_INFO_KEY) != nil {
		accountInfo = ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	}
	conceptGroup.Updater = accountInfo

	currentTime := time.Now().UnixMilli() // Concept group update_time uses an integer type.
	conceptGroup.UpdateTime = currentTime

	otIDs, err := cgs.cga.GetConceptIDsByConceptGroupIDs(ctx, conceptGroup.KNID,
		conceptGroup.Branch, []string{conceptGroup.CGID}, interfaces.MODULE_TYPE_OBJECT_TYPE)
	if err != nil {
		errStr := fmt.Sprintf("GetConceptIDsByConceptGroupIDs failed, kn_id:[%s],branch:[%s],cg_ids:[%s], error: %v",
			conceptGroup.KNID, conceptGroup.Branch, conceptGroup.CGID, err)
		logger.Errorf(errStr)
		span.SetStatus(codes.Error, errStr)

		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ConceptGroup_InternalError_GetConceptIDsByConceptGroupIDsFailed).
			WithErrorDetails(err.Error())
	}

	// Find related relation types only when object types are present.
	if len(otIDs) > 0 {
		objectTypes, _, err := cgs.ots.ListObjectTypes(ctx, nil, interfaces.ObjectTypesQueryParams{
			PaginationQueryParameters: interfaces.PaginationQueryParameters{
				Limit: -1, // -1 returns every entry found in storage.
			},
			KNID:   conceptGroup.KNID,
			Branch: conceptGroup.Branch,
			OTIDS:  otIDs,
		})
		if err != nil {
			return err
		}
		conceptGroup.ObjectTypes = objectTypes
	}

	bknOtMaps := map[string]*bknsdk.BknObjectType{}
	for _, objectType := range conceptGroup.ObjectTypes {
		bknOtMaps[objectType.OTID] = logics.ToBKNObjectType(objectType)
	}

	bknCG := logics.ToBKNConceptGroup(conceptGroup)
	conceptGroup.BKNRawContent = bknsdk.SerializeConceptGroup(bknCG, bknOtMaps)

	if tx == nil {
		// 0. Begin the transaction.
		tx, err = cgs.db.Begin()
		if err != nil {
			otellog.LogError(ctx, "Begin transaction error", err)
			return rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_ConceptGroup_InternalError_BeginTransactionFailed).
				WithErrorDetails(err.Error())
		}
		// 0.1 On failure.
		defer func() {
			switch err {
			case nil:
				// Commit the transaction.
				err = tx.Commit()
				if err != nil {
					otellog.LogError(ctx, "UpdateConceptGroup Transaction Commit Failed", err)
					return
				}
				otellog.LogDebug(ctx, fmt.Sprintf("UpdateConceptGroup Transaction Commit Success: %s", conceptGroup.CGName))
			default:
				rollbackErr := tx.Rollback()
				if rollbackErr != nil {
					otellog.LogError(ctx, "UpdateConceptGroup Transaction Rollback Error", rollbackErr)
				}
			}
		}()
	}

	// Update model information.
	err = cgs.cga.UpdateConceptGroup(ctx, tx, conceptGroup)
	if err != nil {
		logger.Errorf("UpdateConceptGroup error: %s", err.Error())
		span.SetStatus(codes.Error, "修改概念分组失败")

		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ConceptGroup_InternalError).
			WithErrorDetails(err.Error())
	}

	err = cgs.InsertDatasetData(ctx, conceptGroup)
	if err != nil {
		logger.Errorf("InsertDatasetData error: %s", err.Error())
		span.SetStatus(codes.Error, "概念分组概念索引写入失败")

		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ConceptGroup_InternalError_InsertOpenSearchDataFailed).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

func (cgs *conceptGroupService) DeleteConceptGroupByID(ctx context.Context, tx *sql.Tx, knID string, branch string, cgID string) (err error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Delete concept group by id")
	defer span.End()
	if tx == nil {
		var cleanupTracker *permission.AuthorizationCleanupTracker
		var trackerOwner bool
		ctx, cleanupTracker, trackerOwner = permission.WithAuthorizationCleanupTracker(ctx)
		defer func() {
			if trackerOwner && err == nil {
				_ = cleanupTracker.Cleanup(ctx, cgs.ps)
			}
		}()
	}

	if err := permission.ValidateKNChildPEPAuthorizationIDs(ctx, knID, []string{cgID}); err != nil {
		return err
	}
	_, exists, err := cgs.cga.CheckConceptGroupExistByID(ctx, knID, branch, cgID)
	if err != nil {
		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ConceptGroup_InternalError_CheckConceptGroupIfExistFailed).WithErrorDetails(err.Error())
	}
	if !exists {
		return rest.NewHTTPError(ctx, http.StatusNotFound, berrors.BknBackend_ConceptGroup_ConceptGroupNotFound)
	}
	resource, operation := permission.ResolveKNChildPermissionTarget(interfaces.RESOURCE_TYPE_CONCEPT_GROUP,
		knID, cgID, interfaces.OPERATION_TYPE_MODIFY, interfaces.OPERATION_TYPE_DELETE)
	err = cgs.ps.CheckPermission(ctx, resource, []string{operation})
	if err != nil {
		return err
	}

	if tx == nil {
		// 0. Begin the transaction.
		tx, err = cgs.db.Begin()
		if err != nil {
			otellog.LogError(ctx, "Begin transaction error", err)
			return rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_ConceptGroup_InternalError_BeginTransactionFailed).
				WithErrorDetails(err.Error())
		}
	}

	// 0.1 On failure.
	defer func() {
		switch err {
		case nil:
			// Commit the transaction.
			err = tx.Commit()
			if err != nil {
				otellog.LogError(ctx, "DeleteConceptGroup Transaction Commit Failed", err)
				return
			}
			otellog.LogDebug(ctx, "DeleteConceptGroup Transaction Commit Success")
		default:
			rollbackErr := tx.Rollback()
			if rollbackErr != nil {
				otellog.LogError(ctx, "DeleteConceptGroup Transaction Rollback Error", err)
			}
		}
	}()

	// Delete concept groups.
	rowsAffect, err := cgs.cga.DeleteConceptGroupByID(ctx, tx, knID, branch, cgID)
	if err != nil {
		logger.Errorf("DeleteConceptGroupsByIDs error: %s", err.Error())
		span.SetStatus(codes.Error, "删除概念分组失败")

		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ConceptGroup_InternalError).WithErrorDetails(err.Error())
	}
	logger.Infof("DeleteConceptGroupByID: Rows affected is %v, request delete CGID is %s in knowledge network [%s] branch [%s]!",
		rowsAffect, cgID, knID, branch)
	if rowsAffect != int64(1) {
		otellog.LogWarn(ctx, fmt.Sprintf("DeleteConceptGroupByID number %v not equal %v!", rowsAffect, 1))
	}

	// Delete all bindings under the group.
	cgrsRowsAffect, err := cgs.cga.DeleteObjectTypesFromGroup(ctx, tx, interfaces.ConceptGroupRelationsQueryParams{
		KNID:        knID,
		Branch:      branch,
		ConceptType: interfaces.MODULE_TYPE_OBJECT_TYPE,
		CGIDs:       []string{cgID},
	})
	if err != nil {
		logger.Errorf("DeleteObjectTypesFromGroup error: %s", err.Error())
		span.SetStatus(codes.Error, "删除概念与分组的关系失败")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ConceptGroup_InternalError).WithErrorDetails(err.Error())
	}
	logger.Infof("DeleteObjectTypesFromGroup: Rows affected is %v, request delete cgID is %s!", cgrsRowsAffect, cgID)

	docid := interfaces.GenerateConceptDocuemtnID(knID,
		interfaces.MODULE_TYPE_CONCEPT_GROUP, cgID, branch)
	err = cgs.vbs.DeleteDatasetDocumentByID(ctx, interfaces.BKN_DATASET_ID, docid)
	if err != nil {
		logger.Errorf("DeleteDatasetDocumentByID error: %s", err.Error())
		span.SetStatus(codes.Error, "删除概念分组概念索引失败")
		return err
	}
	permission.TrackKNChildAuthorizationCleanup(ctx,
		interfaces.RESOURCE_TYPE_CONCEPT_GROUP, knID, []string{cgID})

	span.SetStatus(codes.Ok, "")
	return nil
}

// Internal method. Deletes concept groups without permission checks; tx is required.
func (cgs *conceptGroupService) DeleteConceptGroupsByKnID(ctx context.Context, tx *sql.Tx, knID string, branch string) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Delete concept group by knID")
	defer span.End()

	if tx == nil {
		otellog.LogError(ctx, "missing transaction", nil)
		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ConceptGroup_InternalError_MissingTransaction).
			WithErrorDetails("missing transaction")
	}
	cgIDs, err := cgs.cga.GetConceptGroupIDsByKnID(ctx, knID, branch)
	if err != nil {
		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ConceptGroup_InternalError).WithErrorDetails(err.Error())
	}

	// Delete concept groups.
	rowsAffect, err := cgs.cga.DeleteConceptGroupsByKnID(ctx, tx, knID, branch)
	if err != nil {
		logger.Errorf("DeleteConceptGroupsByKnID error: %s", err.Error())
		span.SetStatus(codes.Error, "删除概念分组失败")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ConceptGroup_InternalError).WithErrorDetails(err.Error())
	}
	logger.Infof("DeleteConceptGroupsByKnID: Rows affected is %v, request delete knID is %s in knowledge network [%s] branch [%s]!",
		rowsAffect, knID, knID, branch)

	// Delete all bindings under the group.
	rowsAffect, err = cgs.cga.DeleteConceptGroupRelationsByKnID(ctx, tx, knID, branch)
	if err != nil {
		logger.Errorf("DeleteConceptGroupRelationsByKnID error: %s", err.Error())
		span.SetStatus(codes.Error, "删除概念与分组的关系失败")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ConceptGroup_InternalError).WithErrorDetails(err.Error())
	}
	logger.Infof("DeleteConceptGroupRelationsByKnID: Rows affected is %v, request delete knID is %s in knowledge network [%s] branch [%s]!",
		rowsAffect, knID, knID, branch)
	permission.TrackKNChildAuthorizationCleanup(ctx,
		interfaces.RESOURCE_TYPE_CONCEPT_GROUP, knID, cgIDs)

	span.SetStatus(codes.Ok, "")
	return nil
}

// Update knowledge network details.
func (cgs *conceptGroupService) UpdateConceptGroupDetail(ctx context.Context, knID string, branch string, cgID string, detail string) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, fmt.Sprintf("Update concept group detail[%s]", knID))
	defer span.End()

	// Update knowledge network details.
	err := cgs.cga.UpdateConceptGroupDetail(ctx, knID, branch, cgID, detail)
	if err != nil {
		logger.Errorf("UpdateConceptGroupDetail error: %s", err.Error())
		span.SetStatus(codes.Error, "修改知识网络详情失败")

		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ConceptGroup_InternalError).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

func (cgs *conceptGroupService) handleConceptGroupImportMode(ctx context.Context, mode string,
	conceptGroup *interfaces.ConceptGroup) (isCreate bool, isUpdate bool, err error) {

	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "concept group import mode logic")
	defer span.End()

	isCreate = false
	isUpdate = false

	// Validate import mode for a single ConceptGroup.
	idExist := false
	_, idExist, err = cgs.CheckConceptGroupExistByID(ctx, conceptGroup.KNID, conceptGroup.Branch, conceptGroup.CGID)
	if err != nil {
		return false, false, err
	}

	// Validate conflicts between the request and existing model names.
	existID, nameExist, err := cgs.CheckConceptGroupExistByName(ctx, conceptGroup.KNID, conceptGroup.Branch, conceptGroup.CGName)
	if err != nil {
		return false, false, err
	}

	// Handle mode: ignore removes it from results, overwrite updates it, and normal returns an error.
	if idExist || nameExist {
		switch mode {
		case interfaces.ImportMode_Normal:
			if idExist {
				errDetails := fmt.Sprintf("The concept group with id [%s] already exists in knowledge network [%s] branch [%s]!",
					conceptGroup.CGID, conceptGroup.KNID, conceptGroup.Branch)
				logger.Error(errDetails)
				span.SetStatus(codes.Error, errDetails)
				return false, false, rest.NewHTTPError(ctx, http.StatusBadRequest,
					berrors.BknBackend_ConceptGroup_ConceptGroupIDExisted).
					WithErrorDetails(errDetails)
			}

			if nameExist {
				errDetails := fmt.Sprintf("concept group name '%s' already exists in knowledge network [%s] branch [%s]",
					conceptGroup.CGName, conceptGroup.KNID, conceptGroup.Branch)
				logger.Error(errDetails)
				span.SetStatus(codes.Error, errDetails)
				return false, false, rest.NewHTTPError(ctx, http.StatusForbidden,
					berrors.BknBackend_ConceptGroup_ConceptGroupNameExisted).
					WithDescription(map[string]any{"cg_name": conceptGroup.CGName}).
					WithErrorDetails(errDetails)
			}

		case interfaces.ImportMode_Ignore:
			// Skip duplicates without creating or updating.
			return false, false, nil
		case interfaces.ImportMode_Overwrite:
			if idExist && nameExist {
				// Return an error when both ID and name exist but the named view has a different ID.
				if existID != conceptGroup.CGID {
					errDetails := fmt.Sprintf("Concept group ID '%s' and name '%s' already exist in knowledge network [%s] branch [%s], but the exist concept group id is '%s'",
						conceptGroup.CGID, conceptGroup.CGName, conceptGroup.KNID, conceptGroup.Branch, existID)
					logger.Error(errDetails)
					span.SetStatus(codes.Error, errDetails)
					return false, false, rest.NewHTTPError(ctx, http.StatusForbidden,
						berrors.BknBackend_ConceptGroup_ConceptGroupNameExisted).
						WithErrorDetails(errDetails)
				} else {
					// Overwrite when ID, name, and metric name exist and the named model ID matches the current model ID.
					isUpdate = true
					return isCreate, isUpdate, nil
				}
			}

			// Overwrite when the ID exists and the name does not.
			if idExist && !nameExist {
				isUpdate = true
				return isCreate, isUpdate, nil
			}

			// Return an error when the ID does not exist but the name exists.
			if !idExist && nameExist {
				errDetails := fmt.Sprintf("Concept Group ID '%s' does not exist, but name '%s' already exists in knowledge network [%s] branch [%s]",
					conceptGroup.CGID, conceptGroup.CGName, conceptGroup.KNID, conceptGroup.Branch)
				logger.Error(errDetails)
				span.SetStatus(codes.Error, errDetails)
				return false, false, rest.NewHTTPError(ctx, http.StatusForbidden,
					berrors.BknBackend_ConceptGroup_ConceptGroupNameExisted).
					WithErrorDetails(errDetails)
			}

			// Create when ID, name, and metric name do not exist.
			// if !idExist && !nameExist {}
		}
	}

	// Default behavior is creation.
	isCreate = true
	return isCreate, isUpdate, nil
}

func (cgs *conceptGroupService) InsertDatasetData(ctx context.Context, origConceptGroup *interfaces.ConceptGroup) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "概念分组概念索引写入")
	defer span.End()

	conceptGroup := &interfaces.ConceptGroup{
		CGID:       origConceptGroup.CGID,
		CGName:     origConceptGroup.CGName,
		CommonInfo: origConceptGroup.CommonInfo,
		KNID:       origConceptGroup.KNID,
		Branch:     origConceptGroup.Branch,
		Creator:    origConceptGroup.Creator,
		CreateTime: origConceptGroup.CreateTime,
		Updater:    origConceptGroup.Updater,
		UpdateTime: origConceptGroup.UpdateTime,
		ModuleType: interfaces.MODULE_TYPE_CONCEPT_GROUP,
	}

	if cgs.appSetting.ServerSetting.DefaultSmallModelEnabled {
		words := []string{conceptGroup.CGName}
		words = append(words, conceptGroup.Tags...)
		words = append(words, conceptGroup.Comment, conceptGroup.BKNRawContent)
		word := strings.Join(words, "\n")

		defaultModel, err := cgs.mfs.GetDefaultModel(ctx)
		if err != nil {
			logger.Errorf("GetDefaultModel error: %s", err.Error())
			span.SetStatus(codes.Error, "获取默认模型失败")
			return err
		}
		vectors, err := cgs.mfs.GetVector(ctx, defaultModel, []string{word})
		if err != nil {
			logger.Errorf("GetVector error: %s", err.Error())
			span.SetStatus(codes.Error, "获取概念分组向量失败")
			return err
		}

		conceptGroup.Vector = vectors[0].Vector
	}

	docid := interfaces.GenerateConceptDocuemtnID(conceptGroup.KNID, interfaces.MODULE_TYPE_CONCEPT_GROUP, conceptGroup.CGID, conceptGroup.Branch)

	// Convert to map for dataset
	docBytes, err := sonic.Marshal(conceptGroup)
	if err != nil {
		logger.Errorf("Failed to marshal ConceptGroup: %s", err.Error())
		span.SetStatus(codes.Error, "序列化概念分组失败")
		return err
	}

	var doc map[string]any
	if err := sonic.Unmarshal(docBytes, &doc); err != nil {
		logger.Errorf("Failed to unmarshal ConceptGroup: %s", err.Error())
		span.SetStatus(codes.Error, "反序列化概念分组失败")
		return err
	}

	// Set document ID
	doc["_id"] = docid

	err = cgs.vbs.WriteDatasetDocuments(ctx, interfaces.BKN_DATASET_ID, []map[string]any{doc})
	if err != nil {
		logger.Errorf("WriteDatasetDocuments error: %s", err.Error())
		span.SetStatus(codes.Error, "概念分组概念索引写入失败")
		return err
	}

	return nil
}

// Add object types to the specified concept group.
func (cgs *conceptGroupService) AddObjectTypesToConceptGroup(ctx context.Context, tx *sql.Tx, knID string, branch string,
	cgID string, otIDs []interfaces.ID, importMode string, strictMode bool) ([]string, error) {

	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "添加对象类到概念分组中")
	defer span.End()

	var err error
	if tx == nil {
		// 0. Begin the transaction.
		tx, err = cgs.db.Begin()
		if err != nil {
			otellog.LogError(ctx, "Begin transaction error", err)
			return []string{}, rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_ConceptGroup_InternalError_BeginTransactionFailed).
				WithErrorDetails(err.Error())
		}
		// 0.1 On failure.
		defer func() {
			switch err {
			case nil:
				// Commit the transaction.
				err = tx.Commit()
				if err != nil {
					otellog.LogError(ctx, "AddObjectTypesToConceptGroup Transaction Commit Failed", err)
					return
				}
				otellog.LogDebug(ctx, fmt.Sprintf("AddObjectTypesToConceptGroup Transaction Commit Success:kn_id:%s,branch:%s,cg_id:%s,ot_ids:%v", knID, branch, cgID, otIDs))
			default:
				rollbackErr := tx.Rollback()
				if rollbackErr != nil {
					otellog.LogError(ctx, "AddObjectTypesToConceptGroup Transaction Rollback Error", rollbackErr)
				}
			}
		}()
	}

	// De-duplicate IDs before querying.
	otIDArr := interfaces.GetUniqueIDs(otIDs)

	// 1. When strictMode is true, validate all object type IDs exist in the KN/branch
	if strictMode {
		objectTypes, _, err := cgs.ots.ListObjectTypes(ctx, tx, interfaces.ObjectTypesQueryParams{
			PaginationQueryParameters: interfaces.PaginationQueryParameters{
				Limit: -1,
			},
			KNID:   knID,
			Branch: branch,
			OTIDS:  otIDArr,
		})
		if err != nil {
			return nil, err
		}
		if len(objectTypes) != len(otIDArr) {
			errStr := fmt.Sprintf("Exists any object types not found, expect object types nums is [%d], actual object types num is [%d]", len(otIDs), len(objectTypes))
			logger.Errorf(errStr)
			span.SetStatus(codes.Error, errStr)

			return []string{}, rest.NewHTTPError(ctx, http.StatusNotFound,
				berrors.BknBackend_ConceptGroup_ObjectTypeNotFound).WithErrorDetails(errStr)
		}
	}

	currentTime := time.Now().UnixMilli()

	// 2. Return an error when an object type is already in the group.
	cgRelations, err := cgs.cga.ListConceptGroupRelations(ctx, tx, interfaces.ConceptGroupRelationsQueryParams{
		PaginationQueryParameters: interfaces.PaginationQueryParameters{
			Limit: -1,
		},
		KNID:        knID,
		Branch:      branch,
		CGIDs:       []string{cgID},
		ConceptType: interfaces.MODULE_TYPE_OBJECT_TYPE,
		OTIDs:       otIDArr,
	})
	if err != nil {
		errStr := fmt.Sprintf("ListConceptGroupRelations failed, the concept group is [%s], knowledge network is [%s], branch is [%s], object types is [%v]",
			cgID, knID, branch, otIDArr)
		logger.Errorf(errStr)
		span.SetStatus(codes.Error, errStr)

		return []string{}, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ConceptGroup_InternalError).
			WithErrorDetails(err.Error())
	}

	groupsToAdd := make([]string, 0)
	if len(cgRelations) > 0 {
		switch importMode {
		case interfaces.ImportMode_Normal:
			// In normal mode, return an error when the relation exists.
			errStr := fmt.Sprintf("Exists some object types in the concept group [%s] knowledge network [%s] branch [%s], expect relations num is [%d], actual relations num is [%d]",
				cgID, knID, branch, len(otIDs), len(cgRelations))
			logger.Errorf(errStr)
			span.SetStatus(codes.Error, errStr)

			return []string{}, rest.NewHTTPError(ctx, http.StatusNotFound,
				berrors.BknBackend_ConceptGroup_ConceptGroupRelationExisted).WithErrorDetails(errStr)

		case interfaces.ImportMode_Ignore, interfaces.ImportMode_Overwrite:
			// In ignore and override modes, skip duplicate relations and add new ones.
			// 2. Calculate non-conflicting groups to add.
			existingGroupIDs := make(map[string]bool)

			// Object types with existing relationships.
			for _, rel := range cgRelations {
				existingGroupIDs[rel.ConceptID] = true
			}

			// Object types requested to establish relationships.
			newGroupIDs := make(map[string]bool)
			for _, otID := range otIDArr {
				newGroupIDs[otID] = true
			}

			// Calculate differences.
			for groupID := range newGroupIDs {
				if !existingGroupIDs[groupID] {
					groupsToAdd = append(groupsToAdd, groupID)
				}
			}
		}
	} else {
		groupsToAdd = otIDArr
	}

	// 3. Build and persist relationship records.
	otCGIDs := []string{}
	for _, otID := range groupsToAdd {
		generatedID, generateErr := uuid.NewV7()
		if generateErr != nil {
			return nil, fmt.Errorf("generate concept group relation UUIDv7: %w", generateErr)
		}
		cgRelationID := generatedID.String()

		err = cgs.cga.CreateConceptGroupRelation(ctx, tx, &interfaces.ConceptGroupRelation{
			ID:          cgRelationID,
			KNID:        knID,
			Branch:      branch,
			CGID:        cgID,
			ConceptType: interfaces.MODULE_TYPE_OBJECT_TYPE,
			ConceptID:   otID,
			CreateTime:  currentTime,
		})
		if err != nil {
			errStr := fmt.Sprintf("CreateConceptGroupRelation failed, the concept group is [%s], knowledge network is [%s], branch is [%s], object type is [%s]",
				cgID, knID, branch, otID)
			logger.Errorf(errStr)
			span.SetStatus(codes.Error, errStr)

			return []string{}, rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_ConceptGroup_InternalError_CreateConceptGroupRelationFailed).
				WithErrorDetails(err.Error())
		}
		otCGIDs = append(otCGIDs, cgRelationID)
	}

	return otCGIDs, nil
}

// Get group-to-object type relationships.
func (cgs *conceptGroupService) ListConceptGroupRelations(ctx context.Context,
	query interfaces.ConceptGroupRelationsQueryParams) ([]interfaces.ConceptGroupRelation, error) {

	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "查询概念与分组的关系列表")
	defer span.End()

	// Check whether the user ID can view the business knowledge network.
	err := cgs.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: interfaces.RESOURCE_TYPE_KN,
		ID:   query.KNID,
	}, []string{interfaces.OPERATION_TYPE_VIEW_DETAIL})
	if err != nil {
		return []interfaces.ConceptGroupRelation{}, err
	}

	// 0. Begin the transaction.
	tx, err := cgs.db.Begin()
	if err != nil {
		otellog.LogError(ctx, "Begin transaction error", err)
		return []interfaces.ConceptGroupRelation{}, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ConceptGroup_InternalError_BeginTransactionFailed).
			WithErrorDetails(err.Error())
	}
	// 0.1 On failure.
	defer func() {
		switch err {
		case nil:
			// Commit the transaction.
			err = tx.Commit()
			if err != nil {
				otellog.LogError(ctx, "ListConceptGroupRelations Transaction Commit Failed", err)
				return
			}
			otellog.LogDebug(ctx, fmt.Sprintf("ListConceptGroupRelations Transaction Commit Success: %v", query))
		default:
			rollbackErr := tx.Rollback()
			if rollbackErr != nil {
				otellog.LogError(ctx, "ListConceptGroupRelations Transaction Rollback Error", rollbackErr)
			}
		}
	}()

	// Get the concept group list.
	cgrArr, err := cgs.cga.ListConceptGroupRelations(ctx, tx, query)
	if err != nil {
		logger.Errorf("ListConceptGroupRelations error: %s", err.Error())
		span.SetStatus(codes.Error, "List concept group relations error")

		return []interfaces.ConceptGroupRelation{}, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ConceptGroup_InternalError).WithErrorDetails(err.Error())
	}
	if len(cgrArr) == 0 {
		span.SetStatus(codes.Ok, "")
		return []interfaces.ConceptGroupRelation{}, nil
	}

	// Return all entries when limit is -1.
	if query.Limit == -1 {
		span.SetStatus(codes.Ok, "")
		return cgrArr, nil
	}
	// Paginate results.
	// Check whether the start offset is out of range.
	if query.Offset < 0 || query.Offset >= len(cgrArr) {
		span.SetStatus(codes.Ok, "")
		return []interfaces.ConceptGroupRelation{}, nil
	}
	// Calculate the end offset.
	end := query.Offset + query.Limit
	if end > len(cgrArr) {
		end = len(cgrArr)
	}

	cgrArr = cgrArr[query.Offset:end]

	span.SetStatus(codes.Ok, "")
	return cgrArr, nil

}

// Remove object types from the concept group.
func (cgs *conceptGroupService) DeleteObjectTypesFromGroup(ctx context.Context, tx *sql.Tx, knID string, branch string, cgID string, otIDs []string) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Delete concept group relations")
	defer span.End()

	// Check whether the user ID can modify the business knowledge network.
	err := cgs.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: interfaces.RESOURCE_TYPE_KN,
		ID:   knID,
	}, []string{interfaces.OPERATION_TYPE_MODIFY})
	if err != nil {
		return err
	}

	if tx == nil {
		// 0. Begin the transaction.
		tx, err = cgs.db.Begin()
		if err != nil {
			otellog.LogError(ctx, "Begin transaction error", err)
			return rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_ConceptGroup_InternalError_BeginTransactionFailed).
				WithErrorDetails(err.Error())
		}
		// 0.1 On failure.
		defer func() {
			switch err {
			case nil:
				// Commit the transaction.
				err = tx.Commit()
				if err != nil {
					otellog.LogError(ctx, "DeleteObjectTypesFromGroup Transaction Commit Failed", err)
					return
				}
				otellog.LogDebug(ctx, fmt.Sprintf("DeleteObjectTypesFromGroup Transaction Commit Success: kn_id:%s,branch:%s,cg_id:%s,ot_ids:%v", knID, branch, cgID, otIDs))
			default:
				rollbackErr := tx.Rollback()
				if rollbackErr != nil {
					otellog.LogError(ctx, "DeleteObjectTypesFromGroup Transaction Rollback Error", rollbackErr)
				}
			}
		}()
	}

	// Delete object type-to-group bindings.
	rowsAffect, err := cgs.cga.DeleteObjectTypesFromGroup(ctx, tx, interfaces.ConceptGroupRelationsQueryParams{
		KNID:        knID,
		Branch:      branch,
		CGIDs:       []string{cgID},
		ConceptType: interfaces.MODULE_TYPE_OBJECT_TYPE,
		OTIDs:       otIDs,
	})
	if err != nil {
		logger.Errorf("DeleteObjectTypesFromGroup error: %s", err.Error())
		span.SetStatus(codes.Error, "删除概念与分组的关系失败")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ConceptGroup_InternalError).WithErrorDetails(err.Error())
	}

	logger.Infof("DeleteObjectTypesFromGroup: Rows affected is %v, request delete ATIDs is %v!", rowsAffect, len(otIDs))
	if rowsAffect != int64(len(otIDs)) {
		otellog.LogWarn(ctx, fmt.Sprintf("Delete action types number %v not equal requerst action types number %v!", rowsAffect, len(otIDs)))
	}

	span.SetStatus(codes.Ok, "")
	return nil
}
