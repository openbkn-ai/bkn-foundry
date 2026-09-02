// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package action_type

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
	"bkn-backend/logics/vega_backend"
)

var (
	atServiceOnce sync.Once
	atService     interfaces.ActionTypeService
)

type actionTypeService struct {
	appSetting *common.AppSetting
	db         *sql.DB
	aoa        interfaces.AgentOperatorAccess
	ata        interfaces.ActionTypeAccess
	cga        interfaces.ConceptGroupAccess
	mfs        interfaces.ModelFactoryService
	ots        interfaces.ObjectTypeService
	ps         interfaces.PermissionService
	ums        interfaces.UserMgmtService
	vbs        interfaces.VegaBackendService
}

func invalidParameterDetail(ctx context.Context, name string, templateData map[string]any) string {
	return i18n.Translate(rest.GetLanguageByCtx(ctx), "BknBackend.ActionType.InvalidParameter.Detail."+name, templateData)
}

func NewActionTypeService(appSetting *common.AppSetting) interfaces.ActionTypeService {
	atServiceOnce.Do(func() {
		atService = &actionTypeService{
			appSetting: appSetting,
			db:         logics.DB,
			ata:        logics.ATA,
			aoa:        logics.AOA,
			cga:        logics.CGA,
			mfs:        model_factory.NewModelFactoryService(appSetting, logics.MFA),
			ots:        object_type.NewObjectTypeService(appSetting),
			ps:         permission.NewPermissionService(appSetting),
			ums:        user_mgmt.NewUserMgmtService(appSetting),
			vbs:        vega_backend.NewVegaBackendService(appSetting, logics.VBA),
		}
	})
	return atService
}

func (ats *actionTypeService) CheckActionTypeExistByID(ctx context.Context, knID string, branch string, atID string) (string, bool, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "CheckActionTypeExistByID")
	defer span.End()

	atName, exist, err := ats.ata.CheckActionTypeExistByID(ctx, knID, branch, atID)
	if err != nil {
		otellog.LogError(ctx, fmt.Sprintf("按ID[%v]获取行动类失败", atID), err)
		return "", exist, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ActionType_InternalError_CheckActionTypeIfExistFailed).WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return atName, exist, nil
}

func (ats *actionTypeService) CheckActionTypeExistByName(ctx context.Context, knID string, branch string, atName string) (string, bool, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "CheckActionTypeExistByName")
	defer span.End()

	actionTypeID, exist, err := ats.ata.CheckActionTypeExistByName(ctx, knID, branch, atName)
	if err != nil {
		otellog.LogError(ctx, fmt.Sprintf("按名称[%s]获取行动类失败", atName), err)
		return actionTypeID, exist, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ActionType_InternalError_CheckActionTypeIfExistFailed).WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return actionTypeID, exist, nil
}

// checkImpactContractObjectTypes validates every impact-contract object type in strict mode.
func (ats *actionTypeService) checkImpactContractObjectTypes(ctx context.Context, tx *sql.Tx,
	knID, branch string, contracts []interfaces.ImpactContractItem, batch *interfaces.BatchIDIndex) error {

	for i := range contracts {
		otID := strings.TrimSpace(contracts[i].ObjectTypeID)
		if otID == "" {
			continue
		}
		if batch != nil && batchindex.HasObjectTypeID(otID, batch) {
			continue
		}
		if _, err := ats.ots.GetObjectTypeByID(ctx, tx, knID, branch, otID); err != nil {
			return err
		}
	}
	return nil
}

// validateActionSourceStrict checks tool-box / MCP tool existence when strict_mode applies (via agent-operator-integration internal APIs).
func (ats *actionTypeService) validateActionSourceStrict(ctx context.Context, at *interfaces.ActionType) error {
	if at == nil {
		return nil
	}
	src := at.ActionSource
	switch src.Type {
	case interfaces.ACTION_SOURCE_TYPE_TOOL:
		if src.BoxID == "" || src.ToolID == "" {
			return nil
		}
		if err := ats.aoa.GetToolByID(ctx, src.BoxID, src.ToolID); err != nil {
			logger.Errorf("validate action type tool binding failed: action_type=%s box_id=%s tool_id=%s err=%v",
				at.ATName, src.BoxID, src.ToolID, err)
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
				WithErrorDetails(invalidParameterDetail(ctx, "ToolBindingInvalid", map[string]any{
					"actionType": at.ATName,
					"boxID":      src.BoxID,
					"toolID":     src.ToolID,
				}))
		}
	case interfaces.ACTION_SOURCE_TYPE_MCP:
		if src.McpID == "" || src.ToolName == "" {
			return nil
		}
		if err := ats.aoa.GetMcpToolByName(ctx, src.McpID, src.ToolName); err != nil {
			logger.Errorf("validate action type MCP tool binding failed: action_type=%s mcp_id=%s tool_name=%s err=%v",
				at.ATName, src.McpID, src.ToolName, err)
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
				WithErrorDetails(invalidParameterDetail(ctx, "MCPBindingInvalid", map[string]any{
					"actionType": at.ATName,
					"mcpID":      src.McpID,
					"toolName":   src.ToolName,
				}))
		}
	}
	return nil
}

func (ats *actionTypeService) CreateActionTypes(ctx context.Context, tx *sql.Tx, actionTypes []*interfaces.ActionType, mode string, strictMode bool) (ids []string, err error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "CreateActionTypes")
	defer span.End()
	ctx, parentTracker, trackerOwner := permission.WithResourceParentTracker(ctx)
	defer func() {
		if trackerOwner && err != nil {
			_ = parentTracker.Cleanup(ctx, ats.ps)
		}
	}()

	if !permission.KNImportPermissionPrechecked(ctx) && !permission.KNChildResourcePEPEnabled() {
		err = ats.ps.CheckPermission(ctx, interfaces.PermissionResource{
			Type: interfaces.RESOURCE_TYPE_KN,
			ID:   actionTypes[0].KNID,
		}, []string{interfaces.OPERATION_TYPE_MODIFY})
		if err != nil {
			return []string{}, err
		}
	}

	// 0. Begin the transaction.
	if tx == nil {
		tx, err = ats.db.Begin()
		if err != nil {
			otellog.LogError(ctx, "Begin transaction error", err)
			return []string{}, rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_ActionType_InternalError_BeginTransactionFailed).
				WithErrorDetails(err.Error())
		}
		// 0.1 On failure.
		defer func() {
			switch err {
			case nil:
				// Commit the transaction.
				err = tx.Commit()
				if err != nil {
					otellog.LogError(ctx, "CreateActionType Transaction Commit Failed", err)
					return
				}
				otellog.LogDebug(ctx, "CreateActionType Transaction Commit Success")
			default:
				rollbackErr := tx.Rollback()
				if rollbackErr != nil {
					otellog.LogError(ctx, "CreateActionType Transaction Rollback Error", rollbackErr)
				}
			}
		}()
	}

	currentTime := time.Now().UnixMilli()
	for _, actionType := range actionTypes {
		actionType.ATID, err = permission.PrepareKNChildResourceID(ctx, actionType.ATID)
		if err != nil {
			return nil, err
		}
		if err = permission.ValidateKNChildAuthorizationIDs(ctx, actionType.KNID, []string{actionType.ATID}); err != nil {
			return nil, err
		}

		accountInfo := interfaces.AccountInfo{}
		if ctx.Value(interfaces.ACCOUNT_INFO_KEY) != nil {
			accountInfo = ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
		}

		actionType.Creator = accountInfo
		actionType.Updater = accountInfo

		actionType.CreateTime = currentTime
		actionType.UpdateTime = currentTime

		// When strictMode is true, validate bound object type and affect object types exist
		if strictMode {
			if actionType.ObjectTypeID != "" {
				_, err = ats.ots.GetObjectTypeByID(ctx, tx, actionType.KNID, actionType.Branch, actionType.ObjectTypeID)
				if err != nil {
					return []string{}, err
				}
			}
			if actionType.Affect != nil && actionType.Affect.ObjectTypeID != "" {
				_, err = ats.ots.GetObjectTypeByID(ctx, tx, actionType.KNID, actionType.Branch, actionType.Affect.ObjectTypeID)
				if err != nil {
					return []string{}, err
				}
			}
			err = ats.checkImpactContractObjectTypes(ctx, tx, actionType.KNID,
				actionType.Branch, actionType.ImpactContracts, nil)
			if err != nil {
				return []string{}, err
			}
			err = ats.validateActionSourceStrict(ctx, actionType)
			if err != nil {
				return []string{}, err
			}
		}

		bknAction := logics.ToBKNActionType(actionType)
		actionType.BKNRawContent = bknsdk.SerializeActionType(bknAction)
	}

	createActionTypes, updateActionTypes, err := ats.handleActionTypeImportMode(ctx, mode, actionTypes)
	if err != nil {
		return []string{}, err
	}
	if !permission.KNImportPermissionPrechecked(ctx) && permission.KNChildResourcePEPEnabled() {
		if len(createActionTypes) > 0 {
			if err = ats.ps.CheckPermission(ctx, interfaces.PermissionResource{
				Type: interfaces.RESOURCE_TYPE_KN,
				ID:   actionTypes[0].KNID,
			}, []string{interfaces.OPERATION_TYPE_MODIFY}); err != nil {
				return []string{}, err
			}
		}
		updateIDs := make([]string, 0, len(updateActionTypes))
		for _, actionType := range updateActionTypes {
			updateIDs = append(updateIDs, actionType.ATID)
		}
		if err = permission.CheckKNChildBatchPermission(ctx, ats.ps,
			interfaces.RESOURCE_TYPE_ACTION_TYPE, actionTypes[0].KNID, updateIDs,
			interfaces.OPERATION_TYPE_MODIFY, interfaces.OPERATION_TYPE_MODIFY); err != nil {
			return []string{}, err
		}
	}

	// Create.
	atIDs := []string{}
	for _, actionType := range createActionTypes {
		atIDs = append(atIDs, actionType.ATID)
		err = ats.ata.CreateActionType(ctx, tx, actionType)
		if err != nil {
			logger.Errorf("CreateActionType error: %s", err.Error())
			span.SetStatus(codes.Error, "创建行动类失败")
			return []string{}, rest.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_ActionType_InternalError).
				WithErrorDetails(err.Error())
		}
	}
	parentItems := interfaces.KNChildResourceParents(actionTypes[0].KNID, atIDs)
	if err = ats.ps.UpsertResourceParents(ctx, interfaces.RESOURCE_TYPE_ACTION_TYPE,
		interfaces.RESOURCE_TYPE_KN, parentItems); err != nil {
		return []string{}, err
	}
	permission.TrackResourceParents(ctx, interfaces.RESOURCE_TYPE_ACTION_TYPE,
		interfaces.RESOURCE_TYPE_KN, parentItems)

	// Update.
	for _, actionType := range updateActionTypes {
		// Update an existing submitted item.
		err = ats.UpdateActionType(ctx, tx, actionType, strictMode)
		if err != nil {
			return []string{}, err
		}
	}

	insetActionTypes := createActionTypes
	insetActionTypes = append(insetActionTypes, updateActionTypes...)
	err = ats.InsertDatasetData(ctx, insetActionTypes)
	if err != nil {
		logger.Errorf("InsertDatasetData error: %s", err.Error())
		span.SetStatus(codes.Error, "行动类索引写入失败")
		return []string{}, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ActionType_InternalError_InsertOpenSearchDataFailed).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return atIDs, nil
}

// ValidateActionTypes checks dependency existence only; does not write to the database.
func (ats *actionTypeService) ValidateActionTypes(ctx context.Context, knID string, branch string,
	actionTypes []*interfaces.ActionType, strictMode bool, batch *interfaces.BatchIDIndex, mode string) error {

	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "ValidateActionTypes")
	defer span.End()

	if len(actionTypes) == 0 {
		span.SetStatus(codes.Ok, "")
		return nil
	}

	err := ats.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: interfaces.RESOURCE_TYPE_KN,
		ID:   knID,
	}, []string{interfaces.OPERATION_TYPE_MODIFY})
	if err != nil {
		return err
	}
	_, _, err = ats.handleActionTypeImportMode(ctx, mode, actionTypes)
	if err != nil {
		return err
	}

	for _, actionType := range actionTypes {
		actionType.KNID = knID
		actionType.Branch = branch
		if strictMode {
			if actionType.ObjectTypeID != "" {
				if batch == nil || !batchindex.HasObjectTypeID(actionType.ObjectTypeID, batch) {
					_, err = ats.ots.GetObjectTypeByID(ctx, nil, knID, branch, actionType.ObjectTypeID)
					if err != nil {
						return err
					}
				}
			}
			if actionType.Affect != nil && actionType.Affect.ObjectTypeID != "" {
				if batch == nil || !batchindex.HasObjectTypeID(actionType.Affect.ObjectTypeID, batch) {
					_, err = ats.ots.GetObjectTypeByID(ctx, nil, knID, branch, actionType.Affect.ObjectTypeID)
					if err != nil {
						return err
					}
				}
			}
			err = ats.checkImpactContractObjectTypes(ctx, nil, knID, branch, actionType.ImpactContracts, batch)
			if err != nil {
				return err
			}
			err = ats.validateActionSourceStrict(ctx, actionType)
			if err != nil {
				return err
			}
		}
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

func (ats *actionTypeService) ListActionTypes(ctx context.Context, query interfaces.ActionTypesQueryParams) ([]*interfaces.ActionType, int, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "ListActionTypes")
	defer span.End()
	if !permission.KNChildResourcePEPEnabled() {
		if err := ats.ps.CheckPermission(ctx, interfaces.PermissionResource{
			Type: interfaces.RESOURCE_TYPE_KN,
			ID:   query.KNID,
		}, []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}); err != nil {
			return []*interfaces.ActionType{}, 0, err
		}
	}

	candidateQuery := query
	candidateQuery.Offset = 0
	candidateQuery.Limit = -1
	actionTypes, err := ats.ata.ListActionTypes(ctx, candidateQuery)
	if err != nil {
		logger.Errorf("ListActionTypes error: %s", err.Error())
		span.SetStatus(codes.Error, "List action types error")
		return []*interfaces.ActionType{}, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ActionType_InternalError).WithErrorDetails(err.Error())
	}

	total := len(actionTypes)
	if permission.KNChildResourcePEPEnabled() {
		actionTypes, total, err = permission.FilterAndPaginateKNChildren(ctx, ats.ps,
			interfaces.RESOURCE_TYPE_ACTION_TYPE, query.KNID, actionTypes,
			func(actionType *interfaces.ActionType) string { return actionType.ATID }, query.Offset, query.Limit)
		if err != nil {
			return []*interfaces.ActionType{}, 0, err
		}
	} else {
		actionTypes = permission.PaginateKNChildCandidates(actionTypes, query.Offset, query.Limit)
	}
	if len(actionTypes) == 0 {
		span.SetStatus(codes.Ok, "")
		return actionTypes, total, nil
	}

	objectTypeIDs := make([]string, 0, len(actionTypes))
	for _, actionType := range actionTypes {
		objectTypeIDs = append(objectTypeIDs, actionType.ObjectTypeID)
	}

	objectTypeMap, err := ats.ots.GetObjectTypesMapByIDs(ctx, query.KNID,
		query.Branch, common.DuplicateSlice(objectTypeIDs), false)
	if err != nil {
		return []*interfaces.ActionType{}, 0, err
	}

	// Populate bound object type names for the current action type page.
	for _, actionType := range actionTypes {
		if objectTypeMap[actionType.ObjectTypeID] != nil {
			actionType.ObjectType = interfaces.SimpleObjectType{
				OTID:   objectTypeMap[actionType.ObjectTypeID].OTID,
				OTName: objectTypeMap[actionType.ObjectTypeID].OTName,
				Icon:   objectTypeMap[actionType.ObjectTypeID].Icon,
				Color:  objectTypeMap[actionType.ObjectTypeID].Color,
			}
		}
	}

	accountInfos := make([]*interfaces.AccountInfo, 0, len(actionTypes)*2)
	for _, at := range actionTypes {
		accountInfos = append(accountInfos, &at.Creator, &at.Updater)
	}

	err = ats.ums.GetAccountNames(ctx, accountInfos)
	if err != nil {
		span.SetStatus(codes.Error, "GetAccountNames error")
		return []*interfaces.ActionType{}, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ActionType_InternalError).WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return actionTypes, total, nil
}

func (ats *actionTypeService) GetActionTypesByIDs(ctx context.Context, knID string, branch string, atIDs []string) ([]*interfaces.ActionType, error) {
	// Get action types.
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "GetActionTypesByIDs")
	defer span.End()

	atIDs = common.DuplicateSlice(atIDs)
	if err := permission.ValidateKNChildPEPAuthorizationIDs(ctx, knID, atIDs); err != nil {
		return nil, err
	}
	resource := interfaces.PermissionResource{Type: interfaces.RESOURCE_TYPE_KN, ID: knID}
	operation := interfaces.OPERATION_TYPE_VIEW_DETAIL
	if len(atIDs) == 1 {
		resource, operation = permission.ResolveKNChildPermissionTarget(interfaces.RESOURCE_TYPE_ACTION_TYPE,
			knID, atIDs[0], interfaces.OPERATION_TYPE_VIEW_DETAIL, interfaces.OPERATION_TYPE_VIEW_DETAIL)
	}
	var err error

	// De-duplicate IDs before querying.
	atIDs = common.DuplicateSlice(atIDs)

	// Get basic model information.
	actionTypes, err := ats.ata.GetActionTypesByIDs(ctx, knID, branch, atIDs)
	if err != nil {
		logger.Errorf("GetActionTypesByATIDs error: %s", err.Error())
		span.SetStatus(codes.Error, fmt.Sprintf("Get action type[%v] error: %v", atIDs, err))
		return []*interfaces.ActionType{}, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ActionType_InternalError_GetActionTypesByIDsFailed).
			WithErrorDetails(err.Error())
	}

	if len(actionTypes) != len(atIDs) {
		errStr := fmt.Sprintf("Exists any action types not found, expect action types nums is [%d], actual action types num is [%d]", len(atIDs), len(actionTypes))
		logger.Errorf(errStr)
		span.SetStatus(codes.Error, errStr)
		return []*interfaces.ActionType{}, rest.NewHTTPError(ctx, http.StatusNotFound,
			berrors.BknBackend_ActionType_ActionTypeNotFound).WithErrorDetails(errStr)
	}
	if err = ats.ps.CheckPermission(ctx, resource, []string{operation}); err != nil {
		return nil, err
	}

	// TODO: localize bound and impacted object types and their API documents.
	// Retrieve bound and impacted object type names.
	for _, actionType := range actionTypes {
		affectObjectTypeID := ""
		if actionType.Affect != nil && actionType.Affect.ObjectTypeID != "" {
			affectObjectTypeID = actionType.Affect.ObjectTypeID
		}

		objectTypeMap, err := ats.ots.GetObjectTypesMapByIDs(ctx, knID, branch,
			[]string{actionType.ObjectTypeID, affectObjectTypeID}, false)
		if err != nil {
			return []*interfaces.ActionType{}, err
		}

		if objectTypeMap[actionType.ObjectTypeID] != nil {
			actionType.ObjectType = interfaces.SimpleObjectType{
				OTID:   objectTypeMap[actionType.ObjectTypeID].OTID,
				OTName: objectTypeMap[actionType.ObjectTypeID].OTName,
				Icon:   objectTypeMap[actionType.ObjectTypeID].Icon,
				Color:  objectTypeMap[actionType.ObjectTypeID].Color,
			}
		}

		if objectTypeMap[affectObjectTypeID] != nil {
			actionType.Affect.ObjectType = interfaces.SimpleObjectType{
				OTID:   objectTypeMap[affectObjectTypeID].OTID,
				OTName: objectTypeMap[affectObjectTypeID].OTName,
				Icon:   objectTypeMap[affectObjectTypeID].Icon,
				Color:  objectTypeMap[affectObjectTypeID].Color,
			}
		}
	}

	span.SetStatus(codes.Ok, "")
	return actionTypes, nil
}

// Update action types.
func (ats *actionTypeService) UpdateActionType(ctx context.Context, tx *sql.Tx, actionType *interfaces.ActionType, strictMode bool) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "UpdateActionType")
	defer span.End()

	if err := permission.ValidateKNChildPEPAuthorizationIDs(ctx, actionType.KNID, []string{actionType.ATID}); err != nil {
		return err
	}
	_, exists, err := ats.CheckActionTypeExistByID(ctx, actionType.KNID, actionType.Branch, actionType.ATID)
	if err != nil {
		return err
	}
	if !exists {
		return rest.NewHTTPError(ctx, http.StatusNotFound, berrors.BknBackend_ActionType_ActionTypeNotFound)
	}
	resource, operation := permission.ResolveKNChildPermissionTarget(interfaces.RESOURCE_TYPE_ACTION_TYPE,
		actionType.KNID, actionType.ATID, interfaces.OPERATION_TYPE_MODIFY, interfaces.OPERATION_TYPE_MODIFY)
	err = ats.ps.CheckPermission(ctx, resource, []string{operation})
	if err != nil {
		return err
	}

	if strictMode {
		if actionType.ObjectTypeID != "" {
			_, err = ats.ots.GetObjectTypeByID(ctx, tx, actionType.KNID, actionType.Branch, actionType.ObjectTypeID)
			if err != nil {
				return err
			}
		}
		if actionType.Affect != nil && actionType.Affect.ObjectTypeID != "" {
			_, err = ats.ots.GetObjectTypeByID(ctx, tx, actionType.KNID, actionType.Branch, actionType.Affect.ObjectTypeID)
			if err != nil {
				return err
			}
		}
		err = ats.checkImpactContractObjectTypes(ctx, tx, actionType.KNID,
			actionType.Branch, actionType.ImpactContracts, nil)
		if err != nil {
			return err
		}
		err = ats.validateActionSourceStrict(ctx, actionType)
		if err != nil {
			return err
		}
	}

	accountInfo := interfaces.AccountInfo{}
	if ctx.Value(interfaces.ACCOUNT_INFO_KEY) != nil {
		accountInfo = ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	}
	actionType.Updater = accountInfo

	currentTime := time.Now().UnixMilli() // Action type update_time uses an integer type.
	actionType.UpdateTime = currentTime

	bknAction := logics.ToBKNActionType(actionType)
	actionType.BKNRawContent = bknsdk.SerializeActionType(bknAction)

	if tx == nil {
		// 0. Begin the transaction.
		tx, err = ats.db.Begin()
		if err != nil {
			otellog.LogError(ctx, "Begin transaction error", err)
			return rest.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_ActionType_InternalError_BeginTransactionFailed).
				WithErrorDetails(err.Error())
		}
		// 0.1 On failure.
		defer func() {
			switch err {
			case nil:
				// Commit the transaction.
				err = tx.Commit()
				if err != nil {
					otellog.LogError(ctx, "UpdateActionType Transaction Commit Failed", err)
					return
				}
				otellog.LogDebug(ctx, fmt.Sprintf("UpdateActionType Transaction Commit Success: %s", actionType.ATName))
			default:
				rollbackErr := tx.Rollback()
				if rollbackErr != nil {
					otellog.LogError(ctx, "UpdateActionType Transaction Rollback Error", rollbackErr)
				}
			}
		}()
	}

	// Update model information.
	err = ats.ata.UpdateActionType(ctx, tx, actionType)
	if err != nil {
		logger.Errorf("UpdateActionType error: %s", err.Error())
		span.SetStatus(codes.Error, "修改行动类失败")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ActionType_InternalError).
			WithErrorDetails(err.Error())
	}

	err = ats.InsertDatasetData(ctx, []*interfaces.ActionType{actionType})
	if err != nil {
		logger.Errorf("InsertDatasetData error: %s", err.Error())
		span.SetStatus(codes.Error, "行动类索引写入失败")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ActionType_InternalError_InsertOpenSearchDataFailed).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

func (ats *actionTypeService) DeleteActionTypesByIDs(ctx context.Context, tx *sql.Tx, knID string, branch string, atIDs []string) (err error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "DeleteActionTypesByIDs")
	defer span.End()
	if tx == nil {
		var cleanupTracker *permission.AuthorizationCleanupTracker
		var trackerOwner bool
		ctx, cleanupTracker, trackerOwner = permission.WithAuthorizationCleanupTracker(ctx)
		defer func() {
			if trackerOwner && err == nil {
				_ = cleanupTracker.Cleanup(ctx, ats.ps)
			}
		}()
	}

	atIDs = common.DuplicateSlice(atIDs)
	if err := permission.ValidateKNChildPEPAuthorizationIDs(ctx, knID, atIDs); err != nil {
		return err
	}
	if len(atIDs) == 1 {
		_, exists, err := ats.CheckActionTypeExistByID(ctx, knID, branch, atIDs[0])
		if err != nil {
			return err
		}
		if !exists {
			return rest.NewHTTPError(ctx, http.StatusNotFound, berrors.BknBackend_ActionType_ActionTypeNotFound)
		}
		resource, operation := permission.ResolveKNChildPermissionTarget(interfaces.RESOURCE_TYPE_ACTION_TYPE,
			knID, atIDs[0], interfaces.OPERATION_TYPE_MODIFY, interfaces.OPERATION_TYPE_DELETE)
		if err := ats.ps.CheckPermission(ctx, resource, []string{operation}); err != nil {
			return err
		}
	} else {
		if permission.KNChildResourcePEPEnabled() {
			for _, atID := range atIDs {
				_, exists, err := ats.CheckActionTypeExistByID(ctx, knID, branch, atID)
				if err != nil {
					return err
				}
				if !exists {
					return rest.NewHTTPError(ctx, http.StatusNotFound, berrors.BknBackend_ActionType_ActionTypeNotFound)
				}
			}
		}
		if err := permission.CheckKNChildBatchPermission(ctx, ats.ps,
			interfaces.RESOURCE_TYPE_ACTION_TYPE, knID, atIDs,
			interfaces.OPERATION_TYPE_MODIFY, interfaces.OPERATION_TYPE_DELETE); err != nil {
			return err
		}
	}
	if tx == nil {
		// 0. Begin the transaction.
		tx, err = ats.db.Begin()
		if err != nil {
			otellog.LogError(ctx, "Begin transaction error", err)
			return rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_ActionType_InternalError_BeginTransactionFailed).
				WithErrorDetails(err.Error())
		}
		// 0.1 On failure.
		defer func() {
			switch err {
			case nil:
				// Commit the transaction.
				err = tx.Commit()
				if err != nil {
					otellog.LogError(ctx, "DeleteActionTypes Transaction Commit Failed", err)
					return
				}
				otellog.LogDebug(ctx, fmt.Sprintf("DeleteActionTypes Transaction Commit Success: kn_id:%s,ot_ids:%v", knID, atIDs))
			default:
				rollbackErr := tx.Rollback()
				if rollbackErr != nil {
					otellog.LogError(ctx, "DeleteActionTypes Transaction Rollback Error", rollbackErr)
				}
			}
		}()
	}

	// Delete action types.
	rowsAffect, err := ats.ata.DeleteActionTypesByIDs(ctx, tx, knID, branch, atIDs)
	if err != nil {
		logger.Errorf("DeleteActionTypes error: %s", err.Error())
		span.SetStatus(codes.Error, "删除行动类失败")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ActionType_InternalError).WithErrorDetails(err.Error())
	}

	logger.Infof("DeleteActionTypes: Rows affected is %v, request delete ATIDs is %v!", rowsAffect, len(atIDs))
	if rowsAffect != int64(len(atIDs)) {
		otellog.LogWarn(ctx, fmt.Sprintf("Delete action types number %v not equal requerst action types number %v!", rowsAffect, len(atIDs)))
	}

	for _, atID := range atIDs {
		docid := interfaces.GenerateConceptDocuemtnID(knID, interfaces.MODULE_TYPE_ACTION_TYPE, atID, branch)
		err = ats.vbs.DeleteDatasetDocumentByID(ctx, interfaces.BKN_DATASET_ID, docid)
		if err != nil {
			logger.Errorf("DeleteDatasetDocumentByID error: %s", err.Error())
			span.SetStatus(codes.Error, "删除行动类概念索引失败")
			return err
		}
	}
	permission.TrackKNChildAuthorizationCleanup(ctx,
		interfaces.RESOURCE_TYPE_ACTION_TYPE, knID, atIDs)

	span.SetStatus(codes.Ok, "")
	return nil
}

// Internal API. It does not check permissions and requires tx.
func (ats *actionTypeService) DeleteActionTypesByKnID(ctx context.Context, tx *sql.Tx, knID string, branch string) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "DeleteActionTypesByKnID")
	defer span.End()

	if tx == nil {
		otellog.LogError(ctx, "missing transaction", nil)
		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ActionType_InternalError_MissingTransaction).
			WithErrorDetails("missing transaction")
	}
	atIDs, err := ats.ata.GetActionTypeIDsByKnID(ctx, knID, branch)
	if err != nil {
		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ActionType_InternalError).WithErrorDetails(err.Error())
	}

	// Delete action types.
	rowsAffect, err := ats.ata.DeleteActionTypesByKnID(ctx, tx, knID, branch)
	if err != nil {
		logger.Errorf("DeleteActionTypes error: %s", err.Error())
		span.SetStatus(codes.Error, "删除行动类失败")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ActionType_InternalError).WithErrorDetails(err.Error())
	}

	logger.Infof("DeleteActionTypesByKnID success, the kn_id is [%s], branch is [%s], rowsAffect is [%d]",
		knID, branch, rowsAffect)
	permission.TrackKNChildAuthorizationCleanup(ctx,
		interfaces.RESOURCE_TYPE_ACTION_TYPE, knID, atIDs)
	span.SetStatus(codes.Ok, "")
	return nil
}

func (ats *actionTypeService) handleActionTypeImportMode(ctx context.Context, mode string,
	actionTypes []*interfaces.ActionType) ([]*interfaces.ActionType, []*interfaces.ActionType, error) {

	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "handleActionTypeImportMode")
	defer span.End()

	creates := []*interfaces.ActionType{}
	updates := []*interfaces.ActionType{}

	// 3. When the submitted model ID is not empty, validate conflicts with existing model IDs.
	for _, actionType := range actionTypes {
		creates = append(creates, actionType)
		idExist := false
		_, idExist, err := ats.CheckActionTypeExistByID(ctx, actionType.KNID, actionType.Branch, actionType.ATID)
		if err != nil {
			return creates, updates, err
		}

		// Validate conflicts between the request and existing model names.
		existID, nameExist, err := ats.CheckActionTypeExistByName(ctx, actionType.KNID, actionType.Branch, actionType.ATName)
		if err != nil {
			return creates, updates, err
		}

		// Handle mode: ignore removes it from results, overwrite updates it, and normal returns an error.
		if idExist || nameExist {
			switch mode {
			case interfaces.ImportMode_Normal:
				if idExist {
					errDetails := fmt.Sprintf("The action type with id [%s] already exists!", actionType.ATID)
					logger.Error(errDetails)
					span.SetStatus(codes.Error, errDetails)
					return creates, updates, rest.NewHTTPError(ctx, http.StatusBadRequest,
						berrors.BknBackend_ActionType_ActionTypeIDExisted).
						WithErrorDetails(errDetails)
				}

				if nameExist {
					errDetails := fmt.Sprintf("action type name '%s' already exists", actionType.ATName)
					logger.Error(errDetails)
					span.SetStatus(codes.Error, errDetails)
					return creates, updates, rest.NewHTTPError(ctx, http.StatusForbidden,
						berrors.BknBackend_ActionType_ActionTypeNameExisted).
						WithDescription(map[string]any{"name": actionType.ATName}).
						WithErrorDetails(errDetails)
				}

			case interfaces.ImportMode_Ignore:
				// Skip duplicates.
				// Remove from the create array.
				creates = creates[:len(creates)-1]
			case interfaces.ImportMode_Overwrite:
				if idExist && nameExist {
					// Return an error when both ID and name exist but the named action type has a different ID.
					if existID != actionType.ATID {
						errDetails := fmt.Sprintf("ActionType ID '%s' and name '%s' already exist, but the exist action type id is '%s'",
							actionType.ATID, actionType.ATName, existID)
						logger.Error(errDetails)
						span.SetStatus(codes.Error, errDetails)
						return creates, updates, rest.NewHTTPError(ctx, http.StatusForbidden,
							berrors.BknBackend_ActionType_ActionTypeNameExisted).
							WithErrorDetails(errDetails)
					} else {
						// Overwrite when ID, name, and metric name exist and the named model ID matches the current model ID.
						// Remove from the create array and add to the update array.
						creates = creates[:len(creates)-1]
						updates = append(updates, actionType)
					}
				}

				// Overwrite when the ID exists and the name does not.
				if idExist && !nameExist {
					// Remove from the create array and add to the update array.
					creates = creates[:len(creates)-1]
					updates = append(updates, actionType)
				}

				// Return an error when the ID does not exist but the name exists.
				if !idExist && nameExist {
					errDetails := fmt.Sprintf("ActionType ID '%s' does not exist, but name '%s' already exists",
						actionType.ATID, actionType.ATName)
					logger.Error(errDetails)
					span.SetStatus(codes.Error, errDetails)
					return creates, updates, rest.NewHTTPError(ctx, http.StatusForbidden,
						berrors.BknBackend_ActionType_ActionTypeNameExisted).
						WithErrorDetails(errDetails)
				}

				// Create when ID, name, and metric name do not exist.
				// if !idExist && !nameExist {}
			}
		}
	}

	span.SetStatus(codes.Ok, "")
	return creates, updates, nil
}

func (ats *actionTypeService) InsertDatasetData(ctx context.Context, actionTypes []*interfaces.ActionType) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "行动类索引写入")
	defer span.End()

	if len(actionTypes) == 0 {
		return nil
	}

	if ats.appSetting.ServerSetting.DefaultSmallModelEnabled {
		words := []string{}
		for _, actionType := range actionTypes {
			arr := []string{actionType.ATName}
			arr = append(arr, actionType.Tags...)
			arr = append(arr, actionType.Comment, actionType.BKNRawContent)
			word := strings.Join(arr, "\n")
			words = append(words, word)
		}

		dftModel, err := ats.mfs.GetDefaultModel(ctx)
		if err != nil {
			logger.Errorf("GetDefaultModel error: %s", err.Error())
			span.SetStatus(codes.Error, "获取默认模型失败")
			return err
		}
		vectors, err := ats.mfs.GetVector(ctx, dftModel, words)
		if err != nil {
			logger.Errorf("GetVector error: %s", err.Error())
			span.SetStatus(codes.Error, "获取行动类向量失败")
			return err
		}

		if len(vectors) != len(actionTypes) {
			logger.Errorf("GetVector error: expect vectors num is [%d], actual vectors num is [%d]", len(actionTypes), len(vectors))
			span.SetStatus(codes.Error, "获取行动类向量失败")
			return fmt.Errorf("GetVector error: expect vectors num is [%d], actual vectors num is [%d]", len(actionTypes), len(vectors))
		}

		for i, actionType := range actionTypes {
			actionType.Vector = vectors[i].Vector
		}
	}

	documents := []map[string]any{}
	for _, actionType := range actionTypes {
		docid := interfaces.GenerateConceptDocuemtnID(actionType.KNID, interfaces.MODULE_TYPE_ACTION_TYPE,
			actionType.ATID, actionType.Branch)
		actionType.ModuleType = interfaces.MODULE_TYPE_ACTION_TYPE

		// Convert to map for dataset
		docBytes, err := sonic.Marshal(actionType)
		if err != nil {
			logger.Errorf("Failed to marshal ActionType: %s", err.Error())
			span.SetStatus(codes.Error, "序列化行动类失败")
			return err
		}

		var doc map[string]any
		if err := sonic.Unmarshal(docBytes, &doc); err != nil {
			logger.Errorf("Failed to unmarshal ActionType: %s", err.Error())
			span.SetStatus(codes.Error, "反序列化行动类失败")
			return err
		}

		// Serialize parameters to JSON string
		if params, exists := doc["parameters"]; exists {
			paramsBytes, err := sonic.Marshal(params)
			if err != nil {
				logger.Errorf("Failed to marshal action_type parameters: %s", err.Error())
				span.SetStatus(codes.Error, "序列化行动类参数失败")
				return err
			}
			doc["parameters"] = string(paramsBytes)
		}

		// Serialize condition to JSON string
		if cond, exists := doc["condition"]; exists && cond != nil {
			condBytes, err := sonic.Marshal(cond)
			if err != nil {
				logger.Errorf("Failed to marshal action_type condition: %s", err.Error())
				span.SetStatus(codes.Error, "序列化行动类条件失败")
				return err
			}
			doc["condition"] = string(condBytes)
		}

		// Set document ID
		doc["_id"] = docid
		documents = append(documents, doc)
	}

	err := ats.vbs.WriteDatasetDocuments(ctx, interfaces.BKN_DATASET_ID, documents)
	if err != nil {
		logger.Errorf("WriteDatasetDocuments error: %s", err.Error())
		span.SetStatus(codes.Error, "行动类概念索引写入失败")
		return err
	}

	return nil
}

func (ats *actionTypeService) SearchActionTypes(ctx context.Context, query *interfaces.ConceptsQuery) (interfaces.ActionTypes, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "SearchActionTypes")
	defer span.End()

	response := interfaces.ActionTypes{}
	var err error

	var visibleIDs []string
	if !permission.KNChildResourcePEPEnabled() {
		err = ats.ps.CheckPermission(ctx, interfaces.PermissionResource{
			Type: interfaces.RESOURCE_TYPE_KN,
			ID:   query.KNID,
		}, []string{interfaces.OPERATION_TYPE_VIEW_DETAIL})
		if err != nil {
			return response, err
		}
	} else {
		candidateIDs, err := ats.GetActionTypeIDsByKnID(ctx, query.KNID, query.Branch)
		if err != nil {
			return response, err
		}
		visibleIDs, err = permission.FilterKNChildIDs(ctx, ats.ps,
			interfaces.RESOURCE_TYPE_ACTION_TYPE, query.KNID, candidateIDs,
			interfaces.OPERATION_TYPE_VIEW_DETAIL)
		if err != nil {
			return response, err
		}
		if len(visibleIDs) == 0 {
			return response, nil
		}
	}

	// Convert conditions to dataset filter conditions.
	var filterCondition map[string]any
	if query.ActualCondition != nil {
		filterCondition, err = cond.ConvertCondCfgToFilterCondition(ctx, query.ActualCondition,
			interfaces.CONCPET_QUERY_FIELD,
			func(ctx context.Context, word string) ([]*cond.VectorResp, error) {
				if !ats.appSetting.ServerSetting.DefaultSmallModelEnabled {
					err = errors.New(cond.DEFAULT_SMALL_MODEL_ENABLED_FALSE_ERROR)
					span.SetStatus(codes.Error, err.Error())
					return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
						berrors.BknBackend_ActionType_InternalError).
						WithErrorDetails(err.Error())
				}
				dftModel, err := ats.mfs.GetDefaultModel(ctx)
				if err != nil {
					logger.Errorf("GetDefaultModel error: %s", err.Error())
					span.SetStatus(codes.Error, "获取默认模型失败")
					return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
						berrors.BknBackend_ActionType_InternalError).
						WithErrorDetails(err.Error())
				}
				result, err := ats.mfs.GetVector(ctx, dftModel, []string{word})
				if err != nil {
					logger.Errorf("GetVector error: %s", err.Error())
					span.SetStatus(codes.Error, "获取业务知识网络向量失败")
					return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
						berrors.BknBackend_ActionType_InternalError).
						WithErrorDetails(err.Error())
				}
				return result, nil
			})
		if err != nil {
			logger.Errorf("convert action type condition to filter condition failed: %v", err)
			return response, rest.NewHTTPError(ctx, http.StatusBadRequest,
				berrors.BknBackend_ActionType_InvalidParameter_ConceptCondition).
				WithErrorDetails(i18n.Translate(rest.GetLanguageByCtx(ctx), "BknBackend.Validation.Detail.ConditionDecodeFailed", nil))
		}
	}
	if permission.KNChildResourcePEPEnabled() {
		filterCondition = permission.RestrictDatasetFilterToIDs(filterCondition, visibleIDs)
	}

	// 1. Get relation types in the groups.
	atIDMap := map[string]bool{} // Object type IDs in the groups
	atIDs := []string{}          // Object types can overlap between groups, so de-duplicate object type IDs.
	if len(query.ConceptGroups) > 0 {
		// Validate groups by retrieving them by ID.
		cgCnt, err := ats.cga.GetConceptGroupsTotal(ctx, interfaces.ConceptGroupsQueryParams{
			KNID:   query.KNID,
			Branch: query.Branch,
			CGIDs:  query.ConceptGroups,
		})
		if err != nil {
			logger.Errorf("GetConceptGroupsTotal in knowledge network[%s] error: %s", query.KNID, err.Error())
			span.SetStatus(codes.Error, fmt.Sprintf("GetConceptGroupsTotal in knowledge network[%s], error: %v", query.KNID, err))

			return response, rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_ActionType_InternalError).WithErrorDetails(err.Error())
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

		// Find action type IDs in requested groups within the current business knowledge network.
		atIDArr, err := ats.cga.GetActionTypeIDsFromConceptGroupRelation(ctx, interfaces.ConceptGroupRelationsQueryParams{
			KNID:        query.KNID,
			Branch:      query.Branch,
			ConceptType: interfaces.MODULE_TYPE_OBJECT_TYPE, // Concept type in the concept-to-group relation
			CGIDs:       query.ConceptGroups,
		})
		if err != nil {
			errStr := fmt.Sprintf("GetActionTypeIDsFromConceptGroupRelation failed, kn_id:[%s],branch:[%s],cg_ids:[%v], error: %v",
				query.KNID, query.Branch, query.ConceptGroups, err)
			logger.Errorf(errStr)
			span.SetStatus(codes.Error, errStr)
			span.End()

			return response, rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_ActionType_InternalError).WithErrorDetails(errStr)
		}

		// Return empty when the concept groups contain no action types.
		if len(atIDArr) == 0 {
			return response, nil
		}

		for _, atID := range atIDArr {
			if !atIDMap[atID] {
				atIDMap[atID] = true
				atIDs = append(atIDs, atID)
			}
		}
	}

	// Decide whether to query the total based on NeedTotal.
	if query.NeedTotal {
		if len(atIDMap) == 0 {
			// Query the total count.
			params := &interfaces.ResourceDataQueryParams{
				FilterCondition: filterCondition,
				Paging: interfaces.ResourceDataPagingRequest{
					Mode:  "single",
					Limit: 1, // Query one entry to obtain the total count.
				},
				NeedTotal: true,
			}
			datasetResp, err := ats.vbs.QueryResourceData(ctx, interfaces.BKN_DATASET_ID, params)
			if err != nil {
				logger.Errorf("QueryResourceData error: %s", err.Error())
				span.SetStatus(codes.Error, "业务知识网络行动类检索查询总数失败")
				return response, rest.NewHTTPError(ctx, http.StatusInternalServerError,
					berrors.BknBackend_ActionType_InternalError).
					WithErrorDetails(err.Error())
			}
			response.TotalCount = datasetResp.TotalCount
		} else {
			// Query the matching total within specified groups.
			total, err := ats.GetTotalWithLargeATIDs(ctx, filterCondition, atIDs)
			if err != nil {
				return response, err
			}
			response.TotalCount = total
		}
	}

	// 4. Iterate until enough entries are collected or no more data exists.
	actionTypes := []*interfaces.ActionType{}
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
			NeedTotal:       false,
			Sort:            sort,
		}
		datasetResp, err := ats.vbs.QueryResourceData(ctx, interfaces.BKN_DATASET_ID, params)
		if err != nil {
			logger.Errorf("QueryResourceData error: %s", err.Error())
			span.SetStatus(codes.Error, "业务知识网络行动类检索查询失败")
			return response, rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_ActionType_InternalError).
				WithErrorDetails(err.Error())
		}

		// Stop when no data remains.
		if len(datasetResp.Entries) == 0 {
			break
		}

		// 5. Process query results.
		for _, entry := range datasetResp.Entries {
			// Deserialize condition from JSON string
			if condStr, exists := entry["condition"]; exists {
				if condStrStr, ok := condStr.(string); ok && condStrStr != "" {
					var condCfg interfaces.ActionCondCfg
					if err := sonic.Unmarshal([]byte(condStrStr), &condCfg); err != nil {
						logger.Errorf("Failed to unmarshal action_type condition: %s", err.Error())
						return response, rest.NewHTTPError(ctx, http.StatusBadRequest,
							berrors.BknBackend_InternalError_UnMarshalDataFailed).
							WithErrorDetails(fmt.Sprintf("failed to Unmarshal condition, %s", err.Error()))
					}
					entry["condition"] = &condCfg
				} else if condStr == nil {
					entry["condition"] = nil
				}
			}

			// Deserialize parameters from JSON string
			if paramsStr, exists := entry["parameters"]; exists {
				if paramsStrStr, ok := paramsStr.(string); ok && paramsStrStr != "" {
					var params []interfaces.Parameter
					if err := sonic.Unmarshal([]byte(paramsStrStr), &params); err != nil {
						logger.Errorf("Failed to unmarshal action_type parameters: %s", err.Error())
						return response, rest.NewHTTPError(ctx, http.StatusBadRequest,
							berrors.BknBackend_InternalError_UnMarshalDataFailed).
							WithErrorDetails(fmt.Sprintf("failed to Unmarshal parameters, %s", err.Error()))
					}
					entry["parameters"] = params
				}
			}

			// Convert to an action type struct.
			jsonByte, err := json.Marshal(entry)
			if err != nil {
				return response, rest.NewHTTPError(ctx, http.StatusBadRequest,
					berrors.BknBackend_InternalError_MarshalDataFailed).
					WithErrorDetails(fmt.Sprintf("failed to Marshal dataset entry, %s", err.Error()))
			}
			var actionType interfaces.ActionType
			err = json.Unmarshal(jsonByte, &actionType)
			if err != nil {
				return response, rest.NewHTTPError(ctx, http.StatusBadRequest,
					berrors.BknBackend_InternalError_UnMarshalDataFailed).
					WithErrorDetails(fmt.Sprintf("failed to Unmarshal dataset entry to Action Type, %s", err.Error()))
			}

			// Add the action type when no group is specified or it belongs to the group.
			if len(atIDMap) == 0 || atIDMap[actionType.ATID] {
				// Extract _score when present.
				if scoreVal, ok := entry["_score"]; ok {
					if score, err := common.AnyToFloat64(scoreVal); err == nil {
						actionType.Score = &score
					}
				}
				actionType.Vector = nil
				actionTypes = append(actionTypes, &actionType)
				totalFilteredCount++

				// Stop when enough entries have been collected.
				if len(actionTypes) >= query.Limit && query.Limit > 0 {
					break
				}
			}
		}

		nextCursor = nil
		if datasetResp.Paging != nil {
			nextCursor = datasetResp.Paging.NextCursor
		}

		if query.Limit > 0 && len(actionTypes) >= query.Limit {
			break
		}
		if nextCursor == nil {
			break
		}
		cursor = *nextCursor
	}

	response.Entries = actionTypes
	response.NextCursor = nextCursor
	return response, nil
}

func (ats *actionTypeService) GetTotal(ctx context.Context, filterCondition map[string]any) (total int64, err error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "GetTotal")
	defer span.End()

	// Add a module_type filter condition.
	if filterCondition == nil {
		filterCondition = map[string]any{
			"field":      "module_type",
			"operation":  "==",
			"value":      interfaces.MODULE_TYPE_ACTION_TYPE,
			"value_from": "const",
		}
	} else {
		filterCondition = map[string]any{
			"operation": "and",
			"sub_conditions": []map[string]any{
				filterCondition,
				{
					"field":      "module_type",
					"operation":  "==",
					"value":      interfaces.MODULE_TYPE_ACTION_TYPE,
					"value_from": "const",
				},
			},
		}
	}

	params := &interfaces.ResourceDataQueryParams{
		FilterCondition: filterCondition,
		Paging: interfaces.ResourceDataPagingRequest{
			Mode:  "single",
			Limit: 1, // Query one entry to obtain the total count.
		},
		NeedTotal: true,
	}
	datasetResp, err := ats.vbs.QueryResourceData(ctx, interfaces.BKN_DATASET_ID, params)
	if err != nil {
		span.SetStatus(codes.Error, "Search total documents count failed")
		return total, rest.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_ActionType_InternalError).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	if datasetResp == nil {
		return 0, nil
	}
	return datasetResp.TotalCount, nil
}

// Internal call without permission checks.
func (ats *actionTypeService) GetActionTypeIDsByKnID(ctx context.Context, knID string, branch string) ([]string, error) {
	// Get action types.
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "GetActionTypeIDsByKnID")
	defer span.End()

	// Get basic model information.
	atIDs, err := ats.ata.GetActionTypeIDsByKnID(ctx, knID, branch)
	if err != nil {
		logger.Errorf("GetActionTypeIDsByKnID error: %s", err.Error())
		span.SetStatus(codes.Error, fmt.Sprintf("Get action type[%v] error: %v", atIDs, err))
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ActionType_InternalError_GetActionTypesByIDsFailed).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return atIDs, nil
}

// Query in batches.
func (ats *actionTypeService) GetTotalWithLargeATIDs(ctx context.Context,
	filterCondition map[string]any,
	atIDs []string) (int64, error) {

	total := int64(0)
	for i := 0; i < len(atIDs); i += interfaces.GET_TOTAL_CONCEPTID_BATCH_SIZE {
		end := i + interfaces.GET_TOTAL_CONCEPTID_BATCH_SIZE
		if end > len(atIDs) {
			end = len(atIDs)
		}

		batchIDs := atIDs[i:end]
		batchTotal, err := ats.GetTotalWithATIDs(ctx, filterCondition, batchIDs)
		if err != nil {
			return 0, err
		}

		total += batchTotal
	}

	return total, nil
}

// Query the total count for specified action type IDs.
func (ats *actionTypeService) GetTotalWithATIDs(ctx context.Context,
	filterCondition map[string]any,
	atIDs []string) (int64, error) {

	// Build a filter condition containing the action type ID filter.
	atIDCondition := map[string]any{
		"field":      "id",
		"operation":  "in",
		"value":      atIDs,
		"value_from": "const",
	}

	var combinedCondition map[string]any
	if filterCondition == nil {
		combinedCondition = atIDCondition
	} else {
		combinedCondition = map[string]any{
			"operation": "and",
			"sub_conditions": []map[string]any{
				filterCondition,
				atIDCondition,
			},
		}
	}

	// Execute the count query.
	total, err := ats.GetTotal(ctx, combinedCondition)
	if err != nil {
		return total, err
	}

	return total, nil
}
