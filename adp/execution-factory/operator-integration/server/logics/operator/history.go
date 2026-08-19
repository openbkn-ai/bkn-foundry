package operator

import (
	"context"
	"fmt"
	"net/http"

	jsoniter "github.com/json-iterator/go"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/metadata"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/utils"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
)

// QueryOperatorHistoryDetail Query operation history details.
func (m *operatorManager) QueryOperatorHistoryDetail(ctx context.Context, req *interfaces.OperatorHistoryDetailReq) (result *interfaces.OperatorDataInfo, err error) {
	// record observable.
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	// Check if the operator exists.
	has, historyDB, err := m.OpReleaseHistoryDB.SelectByOpIDAndMetdata(ctx, req.OperatorID, req.Version)
	if err != nil {
		m.Logger.WithContext(ctx).Errorf("select operator history failed, err: %v", err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, "select operator history failed")
		return
	}
	if !has {
		err = errors.DefaultHTTPError(ctx, http.StatusNotFound, "operator versioin not found")
		return
	}
	// Check viewing permissions or public access permissions.
	if common.IsPublicAPIFromCtx(ctx) {
		var accessor *interfaces.AuthAccessor
		accessor, err = m.AuthService.GetAccessor(ctx, req.UserID)
		if err != nil {
			return
		}
		var authorized bool
		authorized, err = m.AuthService.OperationCheckAny(ctx, accessor, req.OperatorID, interfaces.AuthResourceTypeOperator,
			interfaces.AuthOperationTypeView, interfaces.AuthOperationTypePublicAccess)
		if err != nil {
			return
		}
		if !authorized {
			err = errors.NewHTTPError(ctx, http.StatusForbidden, errors.ErrExtCommonOperationForbidden, nil)
			return
		}
	}
	// Analyze operator data.
	releaseDB := &model.OperatorReleaseDB{}
	err = jsoniter.Unmarshal([]byte(historyDB.OpRelease), releaseDB)
	if err != nil {
		m.Logger.WithContext(ctx).Errorf("unmarshal operator failed, err: %v", err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, fmt.Sprintf("unmarshal operator failed, err: %v", err))
		return
	}
	// Get operator metadata.
	exists, metadataDB, err := m.MetadataService.CheckMetadataExists(ctx, interfaces.MetadataType(releaseDB.MetadataType), releaseDB.MetadataVersion)
	if err != nil {
		m.Logger.WithContext(ctx).Errorf("select operator metadata failed, err: %v", err)
		return
	}
	if !exists {
		err = errors.NewHTTPError(ctx, http.StatusNotFound, errors.ErrExtMetadataNotFound, nil)
		return
	}
	// Assembly operator information results.
	userIDs, result, err := m.assembleReleaseResult(ctx, releaseDB, metadataDB)
	if err != nil {
		m.Logger.WithContext(ctx).Errorf("assemble release result failed, err: %v", err)
		return
	}
	userMap, err := m.UserMgnt.GetUsersName(ctx, userIDs)
	if err != nil {
		m.Logger.WithContext(ctx).Warnf("get users info failed, err: %v", err)
		return
	}
	result.CreateUser = utils.GetValueOrDefault(userMap, result.CreateUser, interfaces.UnknownUser)
	result.UpdateUser = utils.GetValueOrDefault(userMap, result.UpdateUser, interfaces.UnknownUser)
	return
}

// QueryOperatorHistoryList Gets a list of historical versions.
func (m *operatorManager) QueryOperatorHistoryList(ctx context.Context, req *interfaces.OperatorHistoryListReq) (result []*interfaces.OperatorDataInfo, err error) {
	// record observable.
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	// Check if the operator exists.
	var has bool
	has, _, err = m.DBOperatorManager.SelectByOperatorID(ctx, nil, req.OperatorID)
	if err != nil {
		m.Logger.WithContext(ctx).Errorf("select operator failed, OperatorID: %s, err: %v", req.OperatorID, err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, "select operator failed")
		return
	}
	if !has {
		// operator does not exist.
		err = errors.DefaultHTTPError(ctx, http.StatusNotFound, "operator not found")
		return
	}
	if common.IsPublicAPIFromCtx(ctx) {
		// Check viewing permissions or public access permissions.
		var accessor *interfaces.AuthAccessor
		accessor, err = m.AuthService.GetAccessor(ctx, req.UserID)
		if err != nil {
			return
		}
		var authorized bool
		authorized, err = m.AuthService.OperationCheckAny(ctx, accessor, req.OperatorID, interfaces.AuthResourceTypeOperator,
			interfaces.AuthOperationTypeView, interfaces.AuthOperationTypePublicAccess)
		if err != nil {
			return
		}
		if !authorized {
			err = errors.NewHTTPError(ctx, http.StatusForbidden, errors.ErrExtCommonOperationForbidden, nil)
			return
		}
	}

	// Query historical data.
	result = []*interfaces.OperatorDataInfo{}
	histories, err := m.OpReleaseHistoryDB.SelectByOpID(ctx, req.OperatorID)
	if err != nil {
		m.Logger.WithContext(ctx).Errorf("select operator history failed, err: %v", err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, "select operator history failed")
		return
	}
	if histories == nil {
		return
	}
	// Get metadata information.
	sourceMap := map[model.SourceType][]string{}
	for _, history := range histories {
		switch interfaces.MetadataType(history.MetadataType) {
		case interfaces.MetadataTypeAPI:
			sourceMap[model.SourceTypeOpenAPI] = append(sourceMap[model.SourceTypeOpenAPI], history.MetadataVersion)
		case interfaces.MetadataTypeFunc:
			sourceMap[model.SourceTypeFunction] = append(sourceMap[model.SourceTypeFunction], history.MetadataVersion)
		}
	}
	sourceIDToMetadataMap, err := m.MetadataService.BatchGetMetadataBySourceIDs(ctx, sourceMap)
	if err != nil {
		return
	}
	var userList []string
	for _, historyDB := range histories {
		metadataDB, ok := sourceIDToMetadataMap[historyDB.MetadataVersion]
		if !ok || metadataDB == nil {
			m.Logger.WithContext(ctx).Errorf("select operator metadata failed, err: %v", err)
			continue
		}
		releaseDB := &model.OperatorReleaseDB{}
		err = jsoniter.Unmarshal([]byte(historyDB.OpRelease), releaseDB)
		if err != nil {
			m.Logger.WithContext(ctx).Errorf("unmarshal op release error: %v", err)
			continue
		}
		var userIDs []string
		var info *interfaces.OperatorDataInfo
		userIDs, info, err = m.assembleReleaseResult(ctx, releaseDB, metadataDB)
		if err != nil {
			m.Logger.WithContext(ctx).Errorf("assemble release result failed, err: %v", err)
			continue
		}
		result = append(result, info)
		userList = append(userList, userIDs...)
	}
	userMap, err := m.UserMgnt.GetUsersName(ctx, userList)
	if err != nil {
		return
	}
	for i := range result {
		result[i].CreateUser = utils.GetValueOrDefault(userMap, result[i].CreateUser, interfaces.UnknownUser)
		result[i].UpdateUser = utils.GetValueOrDefault(userMap, result[i].UpdateUser, interfaces.UnknownUser)
		result[i].ReleaseUser = utils.GetValueOrDefault(userMap, result[i].ReleaseUser, interfaces.UnknownUser)
	}
	return
}

// Assembly operator information results.
func (m *operatorManager) assembleReleaseResult(ctx context.Context, releaseDB *model.OperatorReleaseDB, metadataDB interfaces.IMetadataDB) (
	userIDs []string, info *interfaces.OperatorDataInfo, err error) {
	executeControl := &interfaces.OperatorExecuteControl{}
	err = jsoniter.Unmarshal([]byte(releaseDB.ExecuteControl), executeControl)
	if err != nil {
		m.Logger.WithContext(ctx).Warnf("unmarshal execute control failed, err: %v", err)
		return
	}
	var extendInfo map[string]interface{}
	err = jsoniter.Unmarshal([]byte(releaseDB.ExtendInfo), &extendInfo)
	if err != nil {
		m.Logger.WithContext(ctx).Warnf("unmarshal extend info failed, err: %v", err)
		return
	}
	info = &interfaces.OperatorDataInfo{
		Name:         releaseDB.Name,
		OperatorID:   releaseDB.OpID,
		Version:      releaseDB.MetadataVersion,
		Status:       interfaces.BizStatus(releaseDB.Status),
		MetadataType: interfaces.MetadataType(releaseDB.MetadataType),
		Metadata:     metadata.MetadataDBToStruct(metadataDB),
		ExtendInfo:   extendInfo,
		OperatorInfo: &interfaces.OperatorInfo{
			Type:          interfaces.OperatorType(releaseDB.OperatorType),
			ExecutionMode: interfaces.ExecutionMode(releaseDB.ExecutionMode),
			Category:      interfaces.BizCategory(releaseDB.Category),
			CategoryName:  m.CategoryManager.GetCategoryName(ctx, interfaces.BizCategory(releaseDB.Category)),
			Source:        releaseDB.Source,
			IsDataSource:  &releaseDB.IsDataSource,
		},
		OperatorExecuteControl: executeControl,
		CreateTime:             releaseDB.CreateTime,
		UpdateTime:             releaseDB.UpdateTime,
		UpdateUser:             releaseDB.UpdateUser,
		CreateUser:             releaseDB.CreateUser,
		ReleaseUser:            releaseDB.ReleaseUser,
		ReleaseTime:            releaseDB.ReleaseTime,
		Tag:                    releaseDB.Tag,
		IsInternal:             releaseDB.IsInternal,
	}
	userIDs = []string{releaseDB.CreateUser, releaseDB.UpdateUser, releaseDB.ReleaseUser, metadataDB.GetCreateUser(), metadataDB.GetUpdateUser()}
	return
}
