// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package relation_type

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bytedance/sonic"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"github.com/rs/xid"
	"go.opentelemetry.io/otel/codes"

	bknsdk "bkn-backend/bkn-specification/bkn"
	"bkn-backend/common"
	cond "bkn-backend/common/condition"
	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
	"bkn-backend/logics"
	"bkn-backend/logics/batchindex"
	"bkn-backend/logics/model_factory"
	"bkn-backend/logics/object_type"
	"bkn-backend/logics/permission"
	"bkn-backend/logics/user_mgmt"
)

var (
	rtServiceOnce sync.Once
	rtService     interfaces.RelationTypeService
)

type relationTypeService struct {
	appSetting *common.AppSetting
	db         *sql.DB
	cga        interfaces.ConceptGroupAccess
	mfs        interfaces.ModelFactoryService
	ots        interfaces.ObjectTypeService
	ps         interfaces.PermissionService
	rta        interfaces.RelationTypeAccess
	ums        interfaces.UserMgmtService
	vba        interfaces.VegaBackendAccess
}

func invalidParameterDetail(ctx context.Context, name string, templateData map[string]any) string {
	return i18n.Translate(rest.GetLanguageByCtx(ctx), "BknBackend.RelationType.InvalidParameter.Detail."+name, templateData)
}

func NewRelationTypeService(appSetting *common.AppSetting) interfaces.RelationTypeService {
	rtServiceOnce.Do(func() {
		rtService = &relationTypeService{
			appSetting: appSetting,
			db:         logics.DB,
			cga:        logics.CGA,
			mfs:        model_factory.NewModelFactoryService(appSetting, logics.MFA),
			ots:        object_type.NewObjectTypeService(appSetting),
			ps:         permission.NewPermissionService(appSetting),
			rta:        logics.RTA,
			ums:        user_mgmt.NewUserMgmtService(appSetting),
			vba:        logics.VBA,
		}
	})
	return rtService
}

func (rts *relationTypeService) CheckRelationTypeExistByID(ctx context.Context, knID string, branch string, rtID string) (string, bool, error) {

	ctx, span := oteltrace.StartNamedInternalSpan(ctx, fmt.Sprintf("校验关系类[%s]的存在性", rtID))
	defer span.End()

	rtName, exist, err := rts.rta.CheckRelationTypeExistByID(ctx, knID, branch, rtID)
	if err != nil {
		otellog.LogError(ctx, fmt.Sprintf("按ID[%s]获取关系类失败", rtID), err)
		return "", exist, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_RelationType_InternalError_CheckRelationTypeIfExistFailed).WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return rtName, exist, nil
}

func (rts *relationTypeService) CreateRelationTypes(ctx context.Context, tx *sql.Tx,
	relationTypes []*interfaces.RelationType, mode string, strictMode bool) ([]string, error) {

	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Create relation type")
	defer span.End()

	// Check whether the user ID can modify the business knowledge network.
	err := rts.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: interfaces.RESOURCE_TYPE_KN,
		ID:   relationTypes[0].KNID,
	}, []string{interfaces.OPERATION_TYPE_MODIFY})
	if err != nil {
		return []string{}, err
	}

	// 0. Begin the transaction.
	if tx == nil {
		tx, err = rts.db.Begin()
		if err != nil {
			otellog.LogError(ctx, "Begin transaction error", err)
			return []string{}, rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_RelationType_InternalError_BeginTransactionFailed).
				WithErrorDetails(err.Error())
		}
		// 0.1 On failure.
		defer func() {
			switch err {
			case nil:
				// Commit the transaction.
				err = tx.Commit()
				if err != nil {
					otellog.LogError(ctx, "CreateRelationType Transaction Commit Failed", err)
					return
				}
				otellog.LogDebug(ctx, "CreateRelationType Transaction Commit Success")
			default:
				rollbackErr := tx.Rollback()
				if rollbackErr != nil {
					otellog.LogError(ctx, "CreateRelationType Transaction Rollback Error", err)
				}
			}
		}()
	}

	currentTime := time.Now().UnixMilli()
	for _, relationType := range relationTypes {
		// Generate a distributed ID when the submitted model ID is empty.
		if relationType.RTID == "" {
			relationType.RTID = xid.New().String()
		}

		accountInfo := interfaces.AccountInfo{}
		if ctx.Value(interfaces.ACCOUNT_INFO_KEY) != nil {
			accountInfo = ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
		}
		relationType.Creator = accountInfo
		relationType.Updater = accountInfo

		relationType.CreateTime = currentTime
		relationType.UpdateTime = currentTime

		// Validate existence when source or target object type IDs are provided.
		err = rts.validateDependency(ctx, tx, relationType, strictMode, nil)
		if err != nil {
			return []string{}, err
		}

		bknRel := logics.ToBKNRelationType(relationType)
		relationType.BKNRawContent = bknsdk.SerializeRelationType(bknRel)
	}

	createRelationTypes, updateRelationTypes, err := rts.handleRelationTypeImportMode(ctx, mode, relationTypes)
	if err != nil {
		return []string{}, err
	}

	// 1. Create the model.
	rtIDs := []string{}
	for _, relationType := range createRelationTypes {
		rtIDs = append(rtIDs, relationType.RTID)
		err = rts.rta.CreateRelationType(ctx, tx, relationType)
		if err != nil {
			logger.Errorf("CreateRelationType error: %s", err.Error())
			span.SetStatus(codes.Error, "创建关系类失败")
			return []string{}, rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_RelationType_InternalError).
				WithErrorDetails(err.Error())
		}
	}

	// Update.
	for _, relationType := range updateRelationTypes {
		err = rts.UpdateRelationType(ctx, tx, relationType, strictMode)
		if err != nil {
			return []string{}, err
		}
	}

	insetRelationTypes := createRelationTypes
	insetRelationTypes = append(insetRelationTypes, updateRelationTypes...)
	err = rts.InsertDatasetData(ctx, insetRelationTypes)
	if err != nil {
		logger.Errorf("InsertDatasetData error: %s", err.Error())
		span.SetStatus(codes.Error, "关系类索引写入失败")
		return []string{}, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_RelationType_InternalError_InsertOpenSearchDataFailed).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return rtIDs, nil
}

// ValidateRelationTypes checks dependency existence only; does not write to the database.
func (rts *relationTypeService) ValidateRelationTypes(ctx context.Context, knID string, branch string,
	relationTypes []*interfaces.RelationType, strictMode bool, batch *interfaces.BatchIDIndex, mode string) error {

	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "ValidateRelationTypes")
	defer span.End()

	if len(relationTypes) == 0 {
		span.SetStatus(codes.Ok, "")
		return nil
	}

	err := rts.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: interfaces.RESOURCE_TYPE_KN,
		ID:   knID,
	}, []string{interfaces.OPERATION_TYPE_MODIFY})
	if err != nil {
		return err
	}
	_, _, err = rts.handleRelationTypeImportMode(ctx, mode, relationTypes)
	if err != nil {
		return err
	}

	for _, relationType := range relationTypes {
		relationType.KNID = knID
		relationType.Branch = branch
		err = rts.validateDependency(ctx, nil, relationType, strictMode, batch)
		if err != nil {
			return err
		}
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

func (rts *relationTypeService) ListRelationTypes(ctx context.Context,
	query interfaces.RelationTypesQueryParams) ([]*interfaces.RelationType, int, error) {

	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "查询关系类列表")
	defer span.End()

	// Fall back to the main branch when branch is empty. Relation type lookup does this already, but source and target name lookup does not.
	// An empty branch would match f_branch = empty and return no rows, leaving SourceObjectType and TargetObjectType empty.
	if query.Branch == "" {
		query.Branch = interfaces.MAIN_BRANCH
	}

	// Check whether the user ID can view the business knowledge network.
	err := rts.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: interfaces.RESOURCE_TYPE_KN,
		ID:   query.KNID,
	}, []string{interfaces.OPERATION_TYPE_VIEW_DETAIL})
	if err != nil {
		return []*interfaces.RelationType{}, 0, err
	}

	// Get the relation type list.
	relationTypes, err := rts.rta.ListRelationTypes(ctx, query)
	if err != nil {
		logger.Errorf("ListRelationTypes error: %s", err.Error())
		span.SetStatus(codes.Error, "List relation types error")

		return []*interfaces.RelationType{}, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_RelationType_InternalError).WithErrorDetails(err.Error())
	}

	total, err := rts.rta.GetRelationTypesTotal(ctx, query)
	if err != nil {
		logger.Errorf("GetRelationTypesTotal error: %s", err.Error())
		span.SetStatus(codes.Error, "Get relation types total error")

		return []*interfaces.RelationType{}, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_RelationType_InternalError).WithErrorDetails(err.Error())
	}
	if len(relationTypes) == 0 {
		span.SetStatus(codes.Ok, "")
		return relationTypes, total, nil
	}

	objectTypeIDs := make([]string, 0, len(relationTypes)*2)
	for _, relationType := range relationTypes {
		objectTypeIDs = append(objectTypeIDs, relationType.SourceObjectTypeID, relationType.TargetObjectTypeID)
	}

	objectTypeMap, err := rts.ots.GetObjectTypesMapByIDs(ctx, query.KNID, query.Branch,
		common.DuplicateSlice(objectTypeIDs), false)
	if err != nil {
		return []*interfaces.RelationType{}, 0, err
	}

	// Populate source and target object type names for the current relation type page.
	for _, relationType := range relationTypes {
		sourceObj := objectTypeMap[relationType.SourceObjectTypeID]
		targetObj := objectTypeMap[relationType.TargetObjectTypeID]

		if sourceObj != nil {
			relationType.SourceObjectType = interfaces.SimpleObjectType{
				OTID:   relationType.SourceObjectTypeID,
				OTName: sourceObj.OTName,
				Icon:   sourceObj.Icon,
				Color:  sourceObj.Color,
			}
		}
		if targetObj != nil {
			relationType.TargetObjectType = interfaces.SimpleObjectType{
				OTID:   relationType.TargetObjectTypeID,
				OTName: targetObj.OTName,
				Icon:   targetObj.Icon,
				Color:  targetObj.Color,
			}
		}
	}

	accountInfos := make([]*interfaces.AccountInfo, 0, len(relationTypes)*2)
	for _, relationType := range relationTypes {
		accountInfos = append(accountInfos, &relationType.Creator, &relationType.Updater)
	}

	err = rts.ums.GetAccountNames(ctx, accountInfos)
	if err != nil {
		span.SetStatus(codes.Error, "GetAccountNames error")

		return []*interfaces.RelationType{}, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_RelationType_InternalError).WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return relationTypes, total, nil
}

func (rts *relationTypeService) GetRelationTypesByIDs(ctx context.Context, knID string, branch string, rtIDs []string) ([]*interfaces.RelationType, error) {
	// Get relation types.
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, fmt.Sprintf("查询关系类[%v]信息", rtIDs))
	defer span.End()

	// Check whether the user ID can view the business knowledge network.
	err := rts.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: interfaces.RESOURCE_TYPE_KN,
		ID:   knID,
	}, []string{interfaces.OPERATION_TYPE_VIEW_DETAIL})
	if err != nil {
		return []*interfaces.RelationType{}, err
	}

	// De-duplicate IDs before querying.
	rtIDs = common.DuplicateSlice(rtIDs)

	// Get basic model information.
	relationTypes, err := rts.rta.GetRelationTypesByIDs(ctx, knID, branch, rtIDs)
	if err != nil {
		logger.Errorf("GetRelationTypesByRTIDs error: %s", err.Error())
		span.SetStatus(codes.Error, fmt.Sprintf("Get relation types[%v] error: %v", rtIDs, err))

		return []*interfaces.RelationType{}, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_RelationType_InternalError_GetRelationTypesByIDsFailed).
			WithErrorDetails(err.Error())
	}

	if len(relationTypes) != len(rtIDs) {
		errStr := fmt.Sprintf("Exists any relation types not found, expect relation type nums is [%d], actual relation types num is [%d]", len(rtIDs), len(relationTypes))
		logger.Errorf(errStr)
		span.SetStatus(codes.Error, errStr)

		return []*interfaces.RelationType{}, rest.NewHTTPError(ctx, http.StatusNotFound,
			berrors.BknBackend_RelationType_RelationTypeNotFound).WithErrorDetails(errStr)
	}

	// Retrieve source and target object type names.
	for _, relationType := range relationTypes {
		// Retrieve source and target object type names.
		objectTypeMap, err := rts.ots.GetObjectTypesMapByIDs(ctx, knID, branch,
			[]string{relationType.SourceObjectTypeID, relationType.TargetObjectTypeID}, true)
		if err != nil {
			return []*interfaces.RelationType{}, err
		}

		sourceObj := objectTypeMap[relationType.SourceObjectTypeID]
		targetObj := objectTypeMap[relationType.TargetObjectTypeID]

		// Resolve mapped field display names.
		switch relationType.Type {
		case interfaces.RELATION_TYPE_DIRECT:
			// Continue when neither is present.
			if sourceObj == nil && targetObj == nil {
				continue
			}

			// Source properties come from the source object type. Only data properties are bound, so build a data property map.
			// Add display names to source fields in mappings.
			for k, m := range relationType.MappingRules.([]interfaces.Mapping) {
				if sourceObj != nil {
					relationType.SourceObjectType = interfaces.SimpleObjectType{
						OTID:   relationType.SourceObjectTypeID,
						OTName: sourceObj.OTName,
						Icon:   sourceObj.Icon,
						Color:  sourceObj.Color,
					}
					// Add display names to source fields in mappings.
					relationType.MappingRules.([]interfaces.Mapping)[k].SourceProp.DisplayName = sourceObj.PropertyMap[m.SourceProp.Name]
				}
				if targetObj != nil {
					relationType.TargetObjectType = interfaces.SimpleObjectType{
						OTID:   relationType.TargetObjectTypeID,
						OTName: targetObj.OTName,
						Icon:   targetObj.Icon,
						Color:  targetObj.Color,
					}
					// Add display names to target fields in mappings.
					relationType.MappingRules.([]interfaces.Mapping)[k].TargetProp.DisplayName = targetObj.PropertyMap[m.TargetProp.Name]
				}
			}

		case interfaces.RELATION_TYPE_FILTERED_CROSS_JOIN:
			if sourceObj != nil {
				relationType.SourceObjectType = interfaces.SimpleObjectType{
					OTID:   relationType.SourceObjectTypeID,
					OTName: sourceObj.OTName,
					Icon:   sourceObj.Icon,
					Color:  sourceObj.Color,
				}
			}
			if targetObj != nil {
				relationType.TargetObjectType = interfaces.SimpleObjectType{
					OTID:   relationType.TargetObjectTypeID,
					OTName: targetObj.OTName,
					Icon:   targetObj.Icon,
					Color:  targetObj.Color,
				}
			}
		}
	}

	accountInfos := make([]*interfaces.AccountInfo, 0, len(relationTypes)*2)
	for _, relationType := range relationTypes {
		accountInfos = append(accountInfos, &relationType.Creator, &relationType.Updater)
	}

	err = rts.ums.GetAccountNames(ctx, accountInfos)
	if err != nil {
		span.SetStatus(codes.Error, "GetAccountNames error")

		return []*interfaces.RelationType{}, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_RelationType_InternalError).WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return relationTypes, nil
}

// Update relation types.
func (rts *relationTypeService) UpdateRelationType(ctx context.Context, tx *sql.Tx, relationType *interfaces.RelationType, strictMode bool) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Update relation type")
	defer span.End()

	// Check whether the user ID can modify the business knowledge network.
	err := rts.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: interfaces.RESOURCE_TYPE_KN,
		ID:   relationType.KNID,
	}, []string{interfaces.OPERATION_TYPE_MODIFY})
	if err != nil {
		return err
	}

	accountInfo := interfaces.AccountInfo{}
	if ctx.Value(interfaces.ACCOUNT_INFO_KEY) != nil {
		accountInfo = ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	}
	relationType.Updater = accountInfo

	currentTime := time.Now().UnixMilli() // Relation type update_time uses an integer type.
	relationType.UpdateTime = currentTime

	bknRel := logics.ToBKNRelationType(relationType)
	relationType.BKNRawContent = bknsdk.SerializeRelationType(bknRel)

	if tx == nil {
		// 0. Begin the transaction.
		tx, err = rts.db.Begin()
		if err != nil {
			otellog.LogError(ctx, "Begin transaction error", err)

			return rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_RelationType_InternalError_BeginTransactionFailed).
				WithErrorDetails(err.Error())
		}
		// 0.1 On failure.
		defer func() {
			switch err {
			case nil:
				// Commit the transaction.
				err = tx.Commit()
				if err != nil {
					otellog.LogError(ctx, "UpdateRelationType Transaction Commit Failed", err)
					return
				}
				otellog.LogDebug(ctx, fmt.Sprintf("UpdateRelationType Transaction Commit Success: %s", relationType.RTName))
			default:
				rollbackErr := tx.Rollback()
				if rollbackErr != nil {
					otellog.LogError(ctx, "UpdateRelationType Transaction Rollback Error", err)
				}
			}
		}()
	}

	// Validate source and target object type existence when present, controlled by strict_mode.
	err = rts.validateDependency(ctx, tx, relationType, strictMode, nil)
	if err != nil {
		return err
	}

	// Update model information.
	err = rts.rta.UpdateRelationType(ctx, tx, relationType)
	if err != nil {
		logger.Errorf("relationType error: %s", err.Error())
		span.SetStatus(codes.Error, "修改关系类失败")

		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_RelationType_InternalError).
			WithErrorDetails(err.Error())
	}

	err = rts.InsertDatasetData(ctx, []*interfaces.RelationType{relationType})
	if err != nil {
		logger.Errorf("InsertDatasetData error: %s", err.Error())
		span.SetStatus(codes.Error, "关系类索引写入失败")

		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_RelationType_InternalError_InsertOpenSearchDataFailed).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

func (rts *relationTypeService) DeleteRelationTypesByIDs(ctx context.Context, tx *sql.Tx, knID string, branch string, rtIDs []string) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Delete relation types")
	defer span.End()

	// Check whether the user ID can modify the business knowledge network.
	err := rts.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: interfaces.RESOURCE_TYPE_KN,
		ID:   knID,
	}, []string{interfaces.OPERATION_TYPE_MODIFY})
	if err != nil {
		return err
	}

	if tx == nil {
		// 0. Begin the transaction.
		tx, err = rts.db.Begin()
		if err != nil {
			otellog.LogError(ctx, "Begin transaction error", err)

			return rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_RelationType_InternalError_BeginTransactionFailed).
				WithErrorDetails(err.Error())
		}
		// 0.1 On failure.
		defer func() {
			switch err {
			case nil:
				// Commit the transaction.
				err = tx.Commit()
				if err != nil {
					otellog.LogError(ctx, "DeleteRelationTypes Transaction Commit Failed", err)
					return
				}
				otellog.LogDebug(ctx, fmt.Sprintf("DeleteRelationTypes Transaction Commit Success: kn_id:%s,ot_ids:%v", knID, rtIDs))
			default:
				rollbackErr := tx.Rollback()
				if rollbackErr != nil {
					otellog.LogError(ctx, "DeleteRelationTypes Transaction Rollback Error", rollbackErr)
				}
			}
		}()
	}

	// Delete metric models.
	rowsAffect, err := rts.rta.DeleteRelationTypesByIDs(ctx, tx, knID, branch, rtIDs)
	if err != nil {
		logger.Errorf("DeleteRelationTypes error: %s", err.Error())
		span.SetStatus(codes.Error, "删除关系类失败")

		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_RelationType_InternalError).WithErrorDetails(err.Error())
	}

	logger.Infof("DeleteRelationTypes: Rows affected is %v, request delete RTIDs is %v!", rowsAffect, len(rtIDs))
	if rowsAffect != int64(len(rtIDs)) {
		otellog.LogWarn(ctx, fmt.Sprintf("Delete relation types number %v not equal requerst relation types number %v!", rowsAffect, len(rtIDs)))
	}

	for _, rtID := range rtIDs {
		docid := interfaces.GenerateConceptDocuemtnID(knID, interfaces.MODULE_TYPE_RELATION_TYPE, rtID, branch)
		err = rts.vba.DeleteDatasetDocumentByID(ctx, interfaces.BKN_DATASET_ID, docid)
		if err != nil {
			logger.Errorf("DeleteDatasetDocumentByID error: %s", err.Error())
			span.SetStatus(codes.Error, "删除关系类概念索引失败")
			return err
		}
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

// Internal API. Deletes all relation types by business knowledge network ID without permission checks; tx is required.
func (rts *relationTypeService) DeleteRelationTypesByKnID(ctx context.Context, tx *sql.Tx, knID string, branch string) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Delete relation types by kn_id")
	defer span.End()

	if tx == nil {
		otellog.LogError(ctx, "missing transaction", nil)
		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_RelationType_InternalError_MissingTransaction).
			WithErrorDetails("missing transaction")
	}

	// Delete metric models.
	rowsAffect, err := rts.rta.DeleteRelationTypesByKnID(ctx, tx, knID, branch)
	if err != nil {
		logger.Errorf("DeleteRelationTypesByKnID error: %s", err.Error())
		span.SetStatus(codes.Error, "删除关系类失败")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_RelationType_InternalError).WithErrorDetails(err.Error())
	}

	logger.Infof("DeleteRelationTypesByKnID success, the kn_id is [%s], branch is [%s], rowsAffect is [%d]",
		knID, branch, rowsAffect)
	span.SetStatus(codes.Ok, "")
	return nil
}

func (rts *relationTypeService) handleRelationTypeImportMode(ctx context.Context, mode string,
	relationTypes []*interfaces.RelationType) ([]*interfaces.RelationType, []*interfaces.RelationType, error) {

	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "relation type import mode logic")
	defer span.End()

	creates := []*interfaces.RelationType{}
	updates := []*interfaces.RelationType{}

	// 3. When the submitted model ID is not empty, validate conflicts with existing model IDs.
	for _, relationType := range relationTypes {
		creates = append(creates, relationType)
		_, idExist, err := rts.CheckRelationTypeExistByID(ctx, relationType.KNID, relationType.Branch, relationType.RTID)
		if err != nil {
			return creates, updates, err
		}

		// Handle mode: ignore removes it from results, overwrite updates it, and normal returns an error.
		if idExist {
			switch mode {
			case interfaces.ImportMode_Normal:
				errDetails := fmt.Sprintf("The relation type with id [%s] already exists!", relationType.RTID)
				logger.Error(errDetails)
				span.SetStatus(codes.Error, errDetails)
				return creates, updates, rest.NewHTTPError(ctx, http.StatusBadRequest,
					berrors.BknBackend_RelationType_RelationTypeIDExisted).
					WithErrorDetails(errDetails)

			case interfaces.ImportMode_Ignore:
				// Skip when the ID exists and remove it from the create array.
				creates = creates[:len(creates)-1]

			case interfaces.ImportMode_Overwrite:
				// Overwrite when the ID exists, remove it from create, and add it to update.
				creates = creates[:len(creates)-1]
				updates = append(updates, relationType)
			}
		}
	}
	span.SetStatus(codes.Ok, "")
	return creates, updates, nil
}

func (rts *relationTypeService) InsertDatasetData(ctx context.Context, relationTypes []*interfaces.RelationType) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "关系类索引写入")
	defer span.End()

	// Write the relation type index.
	if len(relationTypes) == 0 {
		return nil
	}

	if rts.appSetting.ServerSetting.DefaultSmallModelEnabled {
		words := []string{}
		for _, relationType := range relationTypes {
			arr := []string{relationType.RTName}
			arr = append(arr, relationType.Tags...)
			arr = append(arr, relationType.Comment, relationType.BKNRawContent)
			word := strings.Join(arr, "\n")
			words = append(words, word)
		}

		dftModel, err := rts.mfs.GetDefaultModel(ctx)
		if err != nil {
			logger.Errorf("GetDefaultModel error: %s", err.Error())
			span.SetStatus(codes.Error, "获取默认模型失败")
			return err
		}
		vectors, err := rts.mfs.GetVector(ctx, dftModel, words)
		if err != nil {
			logger.Errorf("GetVector error: %s", err.Error())
			span.SetStatus(codes.Error, "获取关系类向量失败")
			return err
		}

		if len(vectors) != len(relationTypes) {
			logger.Errorf("GetVector error: expect vectors num is [%d], actual vectors num is [%d]", len(relationTypes), len(vectors))
			span.SetStatus(codes.Error, "获取关系类向量失败")
			return fmt.Errorf("GetVector error: expect vectors num is [%d], actual vectors num is [%d]", len(relationTypes), len(vectors))
		}

		for i, relationType := range relationTypes {
			relationType.Vector = vectors[i].Vector
		}
	}

	documents := []map[string]any{}
	for _, relationType := range relationTypes {
		docid := interfaces.GenerateConceptDocuemtnID(relationType.KNID, interfaces.MODULE_TYPE_RELATION_TYPE,
			relationType.RTID, relationType.Branch)
		relationType.ModuleType = interfaces.MODULE_TYPE_RELATION_TYPE

		// Convert to map for dataset
		docBytes, err := sonic.Marshal(relationType)
		if err != nil {
			logger.Errorf("Failed to marshal RelationType: %s", err.Error())
			span.SetStatus(codes.Error, "序列化关系类失败")
			return err
		}

		var doc map[string]any
		if err := sonic.Unmarshal(docBytes, &doc); err != nil {
			logger.Errorf("Failed to unmarshal RelationType: %s", err.Error())
			span.SetStatus(codes.Error, "反序列化关系类失败")
			return err
		}

		// Set document ID
		doc["_id"] = docid
		documents = append(documents, doc)
	}

	err := rts.vba.WriteDatasetDocuments(ctx, interfaces.BKN_DATASET_ID, documents)
	if err != nil {
		logger.Errorf("WriteDatasetDocuments error: %s", err.Error())
		span.SetStatus(codes.Error, "关系类概念索引写入失败")
		return err
	}

	return nil
}

func (rts *relationTypeService) SearchRelationTypes(ctx context.Context,
	query *interfaces.ConceptsQuery) (interfaces.RelationTypes, error) {

	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "业务知识网络关系类检索")
	defer span.End()

	response := interfaces.RelationTypes{}
	var err error

	// Check whether the user ID can view the business knowledge network.
	err = rts.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: interfaces.RESOURCE_TYPE_KN,
		ID:   query.KNID,
	}, []string{interfaces.OPERATION_TYPE_VIEW_DETAIL})
	if err != nil {
		return response, err
	}

	// Convert conditions to dataset filter conditions.
	var filterCondition map[string]any
	if query.ActualCondition != nil {
		filterCondition, err = cond.ConvertCondCfgToFilterCondition(ctx, query.ActualCondition,
			interfaces.CONCPET_QUERY_FIELD,
			func(ctx context.Context, word string) ([]*cond.VectorResp, error) {
				if !rts.appSetting.ServerSetting.DefaultSmallModelEnabled {
					err = errors.New(cond.DEFAULT_SMALL_MODEL_ENABLED_FALSE_ERROR)
					span.SetStatus(codes.Error, err.Error())
					return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
						berrors.BknBackend_RelationType_InternalError).
						WithErrorDetails(err.Error())
				}
				dftModel, err := rts.mfs.GetDefaultModel(ctx)
				if err != nil {
					logger.Errorf("GetDefaultModel error: %s", err.Error())
					span.SetStatus(codes.Error, "获取默认模型失败")
					return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
						berrors.BknBackend_RelationType_InternalError).
						WithErrorDetails(err.Error())
				}
				result, err := rts.mfs.GetVector(ctx, dftModel, []string{word})
				if err != nil {
					logger.Errorf("GetVector error: %s", err.Error())
					span.SetStatus(codes.Error, "获取业务知识网络向量失败")
					return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
						berrors.BknBackend_RelationType_InternalError).
						WithErrorDetails(err.Error())
				}
				return result, nil
			})
		if err != nil {
			logger.Errorf("convert relation type condition to filter condition failed: %v", err)
			return response, rest.NewHTTPError(ctx, http.StatusBadRequest,
				berrors.BknBackend_RelationType_InvalidParameter_ConceptCondition).
				WithErrorDetails(i18n.Translate(rest.GetLanguageByCtx(ctx), "BknBackend.Validation.Detail.ConditionDecodeFailed", nil))
		}
	}

	// 1. Get relation types in the groups.
	rtIDMap := map[string]bool{} // Object type IDs in the groups
	rtIDs := []string{}          // Object types can overlap between groups, so de-duplicate object type IDs.
	if len(query.ConceptGroups) > 0 {
		// Validate groups by retrieving them by ID.
		cgCnt, err := rts.cga.GetConceptGroupsTotal(ctx, interfaces.ConceptGroupsQueryParams{
			KNID:   query.KNID,
			Branch: query.Branch,
			CGIDs:  query.ConceptGroups,
		})
		if err != nil {
			logger.Errorf("GetConceptGroupsTotal in knowledge network[%s] error: %s", query.KNID, err.Error())
			span.SetStatus(codes.Error, fmt.Sprintf("GetConceptGroupsTotal in knowledge network[%s], error: %v", query.KNID, err))

			return response, rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_RelationType_InternalError).WithErrorDetails(err.Error())
		}
		if cgCnt == 0 {
			errStr := fmt.Sprintf("all concept group not found, expect concept group nums is [%d], actual concept group num is [%d]",
				cgCnt, len(query.ConceptGroups))
			logger.Errorf(errStr)

			// Return 404 when all requested concept groups are missing.
			return response, rest.NewHTTPError(ctx, http.StatusNotFound,
				berrors.BknBackend_ConceptGroup_ConceptGroupNotFound).
				WithErrorDetails(errStr)
		}
		// Find relation type IDs in requested groups within the current business knowledge network.
		rtIDArr, err := rts.cga.GetRelationTypeIDsFromConceptGroupRelation(ctx, interfaces.ConceptGroupRelationsQueryParams{
			KNID:        query.KNID,
			Branch:      query.Branch,
			ConceptType: interfaces.MODULE_TYPE_OBJECT_TYPE, // Concept type in the concept-to-group relation
			CGIDs:       query.ConceptGroups,
		})
		if err != nil {
			errStr := fmt.Sprintf("GetRelationTypeIDsFromConceptGroupRelation failed, kn_id:[%s],branch:[%s],cg_ids:[%v], error: %v",
				query.KNID, query.Branch, query.ConceptGroups, err)
			logger.Errorf(errStr)
			span.SetStatus(codes.Error, errStr)
			span.End()

			return response, rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_RelationType_InternalError).WithErrorDetails(errStr)
		}
		// Return empty when the concept groups contain no relation types.
		if len(rtIDArr) == 0 {
			return response, nil
		}

		for _, rtID := range rtIDArr {
			if !rtIDMap[rtID] {
				rtIDMap[rtID] = true
				rtIDs = append(rtIDs, rtID)
			}
		}
	}

	// Decide whether to query the total based on NeedTotal.
	if query.NeedTotal {
		if len(rtIDMap) == 0 {
			// Search directly when no group is specified and read the total from dataset results.
			params := &interfaces.ResourceDataQueryParams{
				FilterCondition: filterCondition,
				Paging: interfaces.ResourceDataPagingRequest{
					Mode:  "single",
					Limit: 1, // Query one entry to obtain the total count.
				},
				NeedTotal: true,
			}
			datasetResp, err := rts.vba.QueryResourceData(ctx, interfaces.BKN_DATASET_ID, params)
			if err != nil {
				logger.Errorf("QueryDatasetData error: %s", err.Error())
				span.SetStatus(codes.Error, "业务知识网络关系类检索查询总数失败")
				return response, rest.NewHTTPError(ctx, http.StatusInternalServerError,
					berrors.BknBackend_RelationType_InternalError).
					WithErrorDetails(err.Error())
			}
			response.TotalCount = datasetResp.TotalCount
		} else {
			// Query the matching total within specified groups.
			total, err := rts.GetTotalWithLargeRTIDs(ctx, filterCondition, rtIDs)
			if err != nil {
				return response, err
			}
			response.TotalCount = total
		}
	}

	// 4. Iterate until enough entries are collected or no more data exists.
	relationTypes := []*interfaces.RelationType{}
	var totalFilteredCount int64 = 0
	sort := query.Sort
	if len(sort) == 0 {
		sort = []*interfaces.SortParams{{Field: "id", Direction: "asc"}}
	}
	cursor := query.Cursor
	var nextCursor *string
	limit := query.Limit
	if limit == 0 {
		limit = interfaces.ConceptQueryLimit
	}

	for {
		paging := interfaces.ResourceDataPagingRequest{Mode: "cursor", Limit: limit}
		if cursor != "" {
			paging = interfaces.ResourceDataPagingRequest{Cursor: cursor}
		}
		// Call the dataset query.
		params := &interfaces.ResourceDataQueryParams{
			FilterCondition: filterCondition,
			Paging:          paging,
			NeedTotal:       true,
			Sort:            sort,
		}
		datasetResp, err := rts.vba.QueryResourceData(ctx, interfaces.BKN_DATASET_ID, params)
		if err != nil {
			logger.Errorf("QueryResourceData error: %s", err.Error())
			span.SetStatus(codes.Error, "业务知识网络关系类检索查询失败")
			return response, rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_RelationType_InternalError).
				WithErrorDetails(err.Error())
		}

		// Stop when no data remains.
		if len(datasetResp.Entries) == 0 {
			break
		}

		// 5. Process query results.
		for _, entry := range datasetResp.Entries {
			// Convert to a relation type struct.
			jsonByte, err := json.Marshal(entry)
			if err != nil {
				return response, rest.NewHTTPError(ctx, http.StatusBadRequest,
					berrors.BknBackend_InternalError_MarshalDataFailed).
					WithErrorDetails(fmt.Sprintf("failed to Marshal dataset entry, %s", err.Error()))
			}
			var relationType interfaces.RelationType
			err = json.Unmarshal(jsonByte, &relationType)
			if err != nil {
				return response, rest.NewHTTPError(ctx, http.StatusBadRequest,
					berrors.BknBackend_InternalError_UnMarshalDataFailed).
					WithErrorDetails(fmt.Sprintf("failed to Unmarshal dataset entry to Relation Type, %s", err.Error()))
			}

			// Add the relation type when no group is specified or it belongs to the group.
			if len(rtIDMap) == 0 || rtIDMap[relationType.RTID] {
				// Extract _score when present.
				if scoreVal, ok := entry["_score"]; ok {
					if score, err := common.AnyToFloat64(scoreVal); err == nil {
						relationType.Score = &score
					}
				}
				relationType.Vector = nil
				relationTypes = append(relationTypes, &relationType)
				totalFilteredCount++

				// Stop when enough entries have been collected.
				if len(relationTypes) >= query.Limit && query.Limit > 0 {
					break
				}
			}
		}
		nextCursor = nil
		if datasetResp.Paging != nil {
			nextCursor = datasetResp.Paging.NextCursor
		}

		if query.Limit > 0 && len(relationTypes) >= query.Limit {
			break
		}
		if nextCursor == nil {
			break
		}
		cursor = *nextCursor
	}

	response.Entries = relationTypes
	response.NextCursor = nextCursor
	return response, nil
}

func (rts *relationTypeService) GetTotal(ctx context.Context, filterCondition map[string]any) (total int64, err error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "logic layer: search relation type total ")
	defer span.End()

	params := &interfaces.ResourceDataQueryParams{
		FilterCondition: filterCondition,
		Paging: interfaces.ResourceDataPagingRequest{
			Mode:  "single",
			Limit: 1, // Query one entry to obtain the total count.
		},
		NeedTotal: true,
	}
	datasetResp, err := rts.vba.QueryResourceData(ctx, interfaces.BKN_DATASET_ID, params)
	if err != nil {
		span.SetStatus(codes.Error, "Search total documents count failed")
		return total, rest.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_RelationType_InternalError).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")

	if datasetResp == nil {
		return 0, nil
	}
	return datasetResp.TotalCount, nil
}

// Internal call without permission checks.
func (rts *relationTypeService) GetRelationTypeIDsByKnID(ctx context.Context, knID string, branch string) ([]string, error) {
	// Get relation types.
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, fmt.Sprintf("按kn_id[%s]获取关系类IDs", knID))
	defer span.End()

	// Get basic object type information.
	rtIDs, err := rts.rta.GetRelationTypeIDsByKnID(ctx, knID, branch)
	if err != nil {
		logger.Errorf("GetRelationTypeIDsByKnID error: %s", err.Error())
		span.SetStatus(codes.Error, fmt.Sprintf("Get relation type ids by kn_id[%s] error: %v", knID, err))

		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_RelationType_InternalError_GetRelationTypesByIDsFailed).WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return rtIDs, nil
}

// Query in batches.
func (rts *relationTypeService) GetTotalWithLargeRTIDs(ctx context.Context,
	filterCondition map[string]any,
	rtIDs []string) (int64, error) {

	total := int64(0)
	for i := 0; i < len(rtIDs); i += interfaces.GET_TOTAL_CONCEPTID_BATCH_SIZE {
		end := i + interfaces.GET_TOTAL_CONCEPTID_BATCH_SIZE
		if end > len(rtIDs) {
			end = len(rtIDs)
		}

		batchIDs := rtIDs[i:end]
		batchTotal, err := rts.GetTotalWithRTIDs(ctx, filterCondition, batchIDs)
		if err != nil {
			return 0, err
		}

		total += batchTotal
	}

	return total, nil
}

// Query the total count for specified relation type IDs.
func (rts *relationTypeService) GetTotalWithRTIDs(ctx context.Context,
	filterCondition map[string]any,
	rtIDs []string) (int64, error) {

	// Build a filter condition containing the relation type ID filter.
	rtIDCondition := map[string]any{
		"field":      "id",
		"operation":  "in",
		"value":      rtIDs,
		"value_from": "const",
	}

	var combinedCondition map[string]any
	if filterCondition == nil {
		combinedCondition = rtIDCondition
	} else {
		combinedCondition = map[string]any{
			"operation": "and",
			"sub_conditions": []map[string]any{
				filterCondition,
				rtIDCondition,
			},
		}
	}

	// Execute the count query.
	total, err := rts.GetTotal(ctx, combinedCondition)
	if err != nil {
		return total, err
	}

	return total, nil
}

// Validate object types and data views referenced by relation types.
func (rts *relationTypeService) validateDependency(ctx context.Context, tx *sql.Tx, relationType *interfaces.RelationType,
	strictMode bool, batch *interfaces.BatchIDIndex) error {

	if !strictMode {
		return nil
	}
	resolveOT := func(otID string) (*interfaces.ObjectType, error) {
		if otID == "" {
			return nil, nil
		}
		if batch != nil && batchindex.HasObjectTypeID(otID, batch) {
			// Read from the batch when BatchIDIndex is provided and the object type ID exists in that batch.
			ot := batch.ObjectTypes[otID]
			if ot == nil {
				return nil, nil
			}
			// Ensure the object type has data properties and build propertyMap.
			batchindex.EnsureObjectTypePropertyMap(ot)
			if len(ot.PropertyMap) == 0 {
				// Empty means the batch contains only an ID without data properties. Skip storage lookup and reduce mapping-rule validation.
				return nil, nil
			}
			return ot, nil
		}
		// GetObjectTypeByID does not populate PropertyMap (json:"-"); build from DataProperties for mapping checks.
		ot, err := rts.ots.GetObjectTypeByID(ctx, tx, relationType.KNID, relationType.Branch, otID)
		if err != nil {
			return nil, err
		}
		if ot != nil {
			batchindex.EnsureObjectTypePropertyMap(ot)
		}
		return ot, nil
	}

	var sourceObjectType *interfaces.ObjectType
	var targetObjectType *interfaces.ObjectType
	var err error
	if relationType.SourceObjectTypeID != "" {
		sourceObjectType, err = resolveOT(relationType.SourceObjectTypeID)
		if err != nil {
			return err
		}
	}
	if relationType.TargetObjectTypeID != "" {
		targetObjectType, err = resolveOT(relationType.TargetObjectTypeID)
		if err != nil {
			return err
		}
	}
	// Validate source and target object type properties when mapping rules are present.
	if relationType.MappingRules != nil {
		switch relationType.Type {
		case interfaces.RELATION_TYPE_DIRECT:
			directMappingRules := relationType.MappingRules.([]interfaces.Mapping)
			for _, mapping := range directMappingRules {
				if sourceObjectType != nil {
					// Check that source properties exist in source object type data properties.
					if _, exist := sourceObjectType.PropertyMap[mapping.SourceProp.Name]; !exist {
						return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_RelationType_InvalidParameter).
							WithErrorDetails(invalidParameterDetail(ctx, "SourcePropertyNotFound", map[string]any{"property": mapping.SourceProp.Name, "objectType": sourceObjectType.OTName}))
					}
				}

				if targetObjectType != nil {
					// Check that target properties exist in target object type data properties.
					if _, exist := targetObjectType.PropertyMap[mapping.TargetProp.Name]; !exist {
						return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_RelationType_InvalidParameter).
							WithErrorDetails(invalidParameterDetail(ctx, "TargetPropertyNotFound", map[string]any{"property": mapping.TargetProp.Name, "objectType": targetObjectType.OTName}))
					}
				}
			}
		case interfaces.RELATION_TYPE_FILTERED_CROSS_JOIN:
			rules := relationType.MappingRules.(*interfaces.FilteredCrossJoinMapping)
			if sourceObjectType != nil && rules.SourceCondition != nil {
				if _, err := cond.NewCondition(ctx, rules.SourceCondition, cond.CUSTOM, objectTypeToCondFieldsMap(sourceObjectType)); err != nil {
					return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_RelationType_InvalidParameter).
						WithErrorDetails(invalidParameterDetail(ctx, "SourceConditionInvalid", nil))
				}
			}
			if targetObjectType != nil && rules.TargetCondition != nil {
				if _, err := cond.NewCondition(ctx, rules.TargetCondition, cond.CUSTOM, objectTypeToCondFieldsMap(targetObjectType)); err != nil {
					return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_RelationType_InvalidParameter).
						WithErrorDetails(invalidParameterDetail(ctx, "TargetConditionInvalid", nil))
				}
			}
		}
	}
	return nil
}

func objectTypeToCondFieldsMap(ot *interfaces.ObjectType) map[string]*cond.ViewField {
	m := make(map[string]*cond.ViewField)
	if ot == nil {
		return m
	}
	for _, dp := range ot.DataProperties {
		if dp == nil {
			continue
		}
		m[dp.Name] = &cond.ViewField{
			Name:         dp.Name,
			Type:         dp.Type,
			DisplayName:  dp.DisplayName,
			OriginalName: dp.Name,
		}
	}
	return m
}
