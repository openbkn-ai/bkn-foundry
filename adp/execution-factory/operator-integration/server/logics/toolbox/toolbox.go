// Package toolbox toolbox, tool management.
// @file internal_tool.go
// @description: management implementation.
package toolbox

import (
	"context"
	"fmt"
	"net/http"

	infracommon "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/telemetry"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/auth"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/metadata"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/metric"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/utils"
)

// GetToolBox Get toolbox information.
func (s *ToolServiceImpl) GetToolBox(ctx context.Context, req *interfaces.GetToolBoxReq, isMarket bool) (resp *interfaces.ToolBoxToolInfo, err error) {
	// record observable.
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	// If it is a public interface, check the viewing permissions.
	if infracommon.IsPublicAPIFromCtx(ctx) {
		var accessor *interfaces.AuthAccessor
		accessor, err = s.AuthService.GetAccessor(ctx, req.UserID)
		if err != nil {
			return
		}
		if isMarket {
			err = s.AuthService.CheckPublicAccessPermission(ctx, accessor, req.BoxID, interfaces.AuthResourceTypeToolBox)
		} else {
			err = s.AuthService.CheckViewPermission(ctx, accessor, req.BoxID, interfaces.AuthResourceTypeToolBox)
		}
		if err != nil {
			return
		}
	}

	// Check if the toolbox exists.
	exist, toolBox, err := s.ToolBoxDB.SelectToolBox(ctx, req.BoxID)
	if err != nil {
		s.Logger.WithContext(ctx).Errorf("select toolbox failed, err: %v", err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	if !exist {
		err = errors.NewHTTPError(ctx, http.StatusNotFound, errors.ErrExtToolBoxNotFound,
			fmt.Sprintf("toolbox %s not found", req.BoxID))
		return
	}
	// If the market interface is used, only the details of published tools can be obtained.
	if isMarket && toolBox.Status != interfaces.BizStatusPublished.String() {
		err = errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtToolBoxNotFound,
			fmt.Sprintf("toolbox %s is not published", req.BoxID))
		return
	}

	// Convert toolbox database model to toolbox information.
	resp = s.toolBoxDBToToolBoxToolInfo(ctx, toolBox)
	userIDs := []string{toolBox.CreateUser, toolBox.UpdateUser, toolBox.ReleaseUser}

	// Get the tools under the toolbox.
	tools, err := s.ToolDB.SelectToolByBoxID(ctx, req.BoxID)
	if err != nil {
		s.Logger.WithContext(ctx).Errorf("select tool failed, err: %v", err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	toolInfos, userMap, err := s.batchGetToolInfoAndUserInfo(ctx, tools, userIDs, toolBox.ServerURL, interfaces.MetadataType(toolBox.MetadataType))
	if err != nil {
		return
	}
	resp.Tools = append(resp.Tools, toolInfos...)
	resp.CreateUser = utils.GetValueOrDefault(userMap, toolBox.CreateUser, interfaces.UnknownUser)
	resp.UpdateUser = utils.GetValueOrDefault(userMap, toolBox.UpdateUser, interfaces.UnknownUser)
	resp.ReleaseUser = utils.GetValueOrDefault(userMap, toolBox.ReleaseUser, interfaces.UnknownUser)
	return
}

// DeleteBoxByID delete toolbox.
func (s *ToolServiceImpl) DeleteBoxByID(ctx context.Context, req *interfaces.DeleteBoxReq) (resp *interfaces.DeleteBoxResp, err error) {
	// record observable.
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	telemetry.SetSpanAttributes(ctx, map[string]interface{}{
		"box_id":  req.BoxID,
		"user_id": req.UserID,
	})
	// Verify delete permission.
	var accessor *interfaces.AuthAccessor
	accessor, err = s.AuthService.GetAccessor(ctx, req.UserID)
	if err != nil {
		return
	}
	err = s.AuthService.CheckDeletePermission(ctx, accessor, req.BoxID, interfaces.AuthResourceTypeToolBox)
	if err != nil {
		return
	}
	// Check if the toolbox exists.
	exist, toolBox, err := s.ToolBoxDB.SelectToolBox(ctx, req.BoxID)
	if err != nil {
		s.Logger.WithContext(ctx).Errorf("select toolbox failed, err: %v", err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	if !exist {
		err = errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtToolBoxNotFound, "toolbox not found")
		return
	}
	// Delete toolbox.
	tx, err := s.DBTx.GetTx(ctx)
	if err != nil {
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		} else {
			_ = tx.Commit()
		}
	}()
	err = s.deleteToolBox(ctx, tx, req.BoxID)
	if err != nil {
		return
	}

	// Delete resource permissions policy.
	err = s.AuthService.DeletePolicy(ctx, []string{req.BoxID}, interfaces.AuthResourceTypeToolBox)
	if err != nil {
		return
	}

	// Record audit log.
	go func() {
		accountAuthContext, ok := infracommon.GetAccountAuthContextFromCtx(ctx)
		if !ok {
			s.Logger.WithContext(ctx).Warnf("[DeleteToolBox] GetAccountAuthContextFromCtx err :%v", err)
			return
		}
		s.AuditLog.Logger(ctx, &metric.AuditLogBuilderParams{
			TokenInfo: accountAuthContext.TokenInfo,
			Accessor:  accessor,
			Operation: metric.AuditLogOperationDelete,
			Object: &metric.AuditLogObject{
				Type: metric.AuditLogObjectTool,
				Name: toolBox.Name,
				ID:   toolBox.BoxID,
			},
		})
	}()
	return
}

// QueryToolBoxList toolbox management.
func (s *ToolServiceImpl) QueryToolBoxList(ctx context.Context, req *interfaces.QueryToolBoxListReq) (resp *interfaces.QueryToolBoxListResp, err error) {
	// record observable.
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	// Construct query conditions.
	filter := make(map[string]interface{})
	filter["all"] = req.All
	if req.MetadataType != "" {
		filter["metadata_type"] = req.MetadataType
	}
	if req.BoxName != "" {
		filter["name"] = req.BoxName
	}
	if req.BoxCategory != "" {
		// Check whether the classification is legal.
		if !s.CategoryManager.CheckCategory(req.BoxCategory) {
			err = errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtToolBoxCategoryTypeInvalid,
				fmt.Sprintf(" %s category not found", req.BoxCategory))
			return
		}
		filter["category"] = req.BoxCategory
	}
	if req.CreateUser != "" {
		filter["create_user"] = req.CreateUser
	}
	if req.ReleaseUser != "" {
		filter["release_user"] = req.ReleaseUser
	}
	if req.Status != "" {
		filter["status"] = req.Status
	}
	operations := interfaces.AuthOperationTypeView
	resp = &interfaces.QueryToolBoxListResp{
		Data: []*interfaces.ToolBoxInfo{},
	}
	authResp, err := s.getToolBoxListPage(ctx, filter, req.CommonPageParams, req.UserID, operations)
	if err != nil {
		return
	}
	resp.CommonPageResult = authResp.CommonPageResult
	toolBoxList := authResp.Data
	if len(toolBoxList) == 0 {
		return
	}
	// Assembly toolbox information results.
	toolBoxInfoList, err := s.getToolBoxList(ctx, toolBoxList)
	if err != nil {
		return
	}
	if err = projectToolBoxAuthorizeOperations(ctx, s.AuthService, req.UserID, toolBoxInfoList); err != nil {
		return
	}
	resp.Data = toolBoxInfoList
	return
}

func projectToolBoxAuthorizeOperations(ctx context.Context, authorization interfaces.IAuthorizationService, userID string, toolBoxes []*interfaces.ToolBoxInfo) error {
	toolBoxIDs := make([]string, 0, len(toolBoxes))
	for _, toolBox := range toolBoxes {
		toolBoxIDs = append(toolBoxIDs, toolBox.BoxID)
	}
	operationsByID, err := auth.ProjectAuthorizeOperations(ctx, authorization, userID, toolBoxIDs, interfaces.AuthResourceTypeToolBox)
	if err != nil {
		return err
	}
	for _, toolBox := range toolBoxes {
		toolBox.Operations = operationsByID[toolBox.BoxID]
	}
	return nil
}

// UpdateToolBoxStatus Modifies toolbox status.
func (s *ToolServiceImpl) UpdateToolBoxStatus(ctx context.Context, req *interfaces.UpdateToolBoxStatusReq) (resp *interfaces.UpdateToolBoxStatusResp, err error) {
	// record observable.
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	telemetry.SetSpanAttributes(ctx, map[string]interface{}{
		"box_id":  req.BoxID,
		"user_id": req.UserID,
	})
	// Check if the toolbox exists.
	exist, toolBox, err := s.ToolBoxDB.SelectToolBox(ctx, req.BoxID)
	if err != nil {
		s.Logger.WithContext(ctx).Errorf("select toolbox failed, err: %v", err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	if !exist {
		err = errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtToolBoxNotFound,
			fmt.Sprintf("toolbox %s not found", req.BoxID))
		return
	}
	// Check whether the request conversion parameters are legal.
	if !common.CheckStatusTransition(interfaces.BizStatus(toolBox.Status), req.Status) {
		err = errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtToolBoxStatusInvalid,
			fmt.Sprintf("toolbox %s status can not be transition to %s", req.BoxID, req.Status))
		return
	}
	var accessor *interfaces.AuthAccessor
	accessor, err = s.AuthService.GetAccessor(ctx, req.UserID)
	if err != nil {
		return
	}
	var operation metric.AuditLogOperationType
	switch req.Status {
	case interfaces.BizStatusPublished:
		operation = metric.AuditLogOperationPublish
		// Verify publishing permissions.
		err = s.AuthService.CheckPublishPermission(ctx, accessor, req.BoxID, interfaces.AuthResourceTypeToolBox)
		if err != nil {
			return
		}
		// Check if there is a duplicate name.
		err = s.checkBoxDuplicateName(ctx, toolBox.Name, toolBox.BoxID)
	case interfaces.BizStatusUnpublish, interfaces.BizStatusEditing:
	case interfaces.BizStatusOffline:
		operation = metric.AuditLogOperationUnpublish
		// Verify delisting permissions, verify editing permissions.
		err = s.AuthService.CheckUnpublishPermission(ctx, accessor, req.BoxID, interfaces.AuthResourceTypeToolBox)
	default:
		err = errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtToolBoxStatusInvalid,
			fmt.Sprintf("invalid toolbox status: %s", req.Status))
	}
	if err != nil {
		return
	}
	err = s.ToolBoxDB.UpdateToolBoxStatus(ctx, nil, req.BoxID, string(req.Status), req.UserID)
	if err != nil {
		s.Logger.WithContext(ctx).Errorf("update toolbox status failed, err: %v", err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, "update toolbox status failed")
		return
	}
	// Record audit log.
	if operation != "" {
		go func() {
			accountAuthContext, ok := infracommon.GetAccountAuthContextFromCtx(ctx)
			if !ok {
				s.Logger.WithContext(ctx).Warnf("[UpdateToolBoxStatus] GetAccountAuthContextFromCtx err :%v", err)
				return
			}
			s.AuditLog.Logger(ctx, &metric.AuditLogBuilderParams{
				TokenInfo: accountAuthContext.TokenInfo,
				Accessor:  accessor,
				Operation: operation,
				Object: &metric.AuditLogObject{
					Type: metric.AuditLogObjectTool,
					Name: toolBox.Name,
					ID:   toolBox.BoxID,
				},
			})
		}()
	}
	resp = &interfaces.UpdateToolBoxStatusResp{
		BoxID:  req.BoxID,
		Status: req.Status,
	}
	return
}

// GetBoxTool Get tool information.
func (s *ToolServiceImpl) GetBoxTool(ctx context.Context, req *interfaces.GetToolReq) (resp *interfaces.ToolInfo, err error) {
	// record observable.
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	telemetry.SetSpanAttributes(ctx, map[string]interface{}{
		"box_id":  req.BoxID,
		"tool_id": req.ToolID,
	})
	// If it is an external interface, verify whether it has the viewing and public access rights of the tool it belongs to.
	if infracommon.IsPublicAPIFromCtx(ctx) {
		var accessor *interfaces.AuthAccessor
		accessor, err = s.AuthService.GetAccessor(ctx, req.UserID)
		if err != nil {
			return
		}
		var authorized bool
		authorized, err = s.AuthService.OperationCheckAny(ctx, accessor, req.BoxID, interfaces.AuthResourceTypeToolBox, interfaces.AuthOperationTypeView, interfaces.AuthOperationTypePublicAccess)
		if err != nil {
			return
		}
		if !authorized {
			err = errors.NewHTTPError(ctx, http.StatusForbidden, errors.ErrExtCommonOperationForbidden, nil)
			return
		}
	}
	// Check if the toolbox exists.
	exist, boxDB, err := s.ToolBoxDB.SelectToolBox(ctx, req.BoxID)
	if err != nil {
		s.Logger.WithContext(ctx).Errorf("select toolbox failed, err: %v", err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	if !exist {
		err = errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtToolBoxNotFound, fmt.Sprintf("toolbox %s not found", req.BoxID))
		return
	}
	exist, tool, err := s.ToolDB.SelectTool(ctx, req.ToolID)
	if err != nil {
		s.Logger.WithContext(ctx).Errorf("select tool failed, err: %v", err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	if !exist {
		err = errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtToolNotFound,
			fmt.Sprintf("tool %s not found", req.ToolID))
		return
	}
	resp, err = s.getToolInfo(ctx, tool, boxDB.ServerURL, interfaces.MetadataType(boxDB.MetadataType))
	return
}

// DeleteBoxTool Batch delete tools in the toolbox.
func (s *ToolServiceImpl) DeleteBoxTool(ctx context.Context, req *interfaces.BatchDeleteToolReq) (resp *interfaces.BatchDeleteToolResp, err error) {
	// record observable.
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	// Permission verification.
	var accessor *interfaces.AuthAccessor
	accessor, err = s.AuthService.GetAccessor(ctx, req.UserID)
	if err != nil {
		return
	}
	err = s.AuthService.CheckModifyPermission(ctx, accessor, req.BoxID, interfaces.AuthResourceTypeToolBox)
	if err != nil {
		return
	}
	// Check if the toolbox exists.
	exist, toolBox, err := s.ToolBoxDB.SelectToolBox(ctx, req.BoxID)
	if err != nil {
		s.Logger.WithContext(ctx).Errorf("select toolbox failed, err: %v", err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	if !exist {
		err = errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtToolBoxNotFound, "toolbox not found")
		return
	}
	// Built-in tools do not allow deletion of tools.
	if toolBox.IsInternal {
		err = errors.DefaultHTTPError(ctx, http.StatusForbidden, "internal toolbox cannot delete tools")
		return
	}
	// Check if the tool exists.
	tools, err := s.ToolDB.SelectToolBoxByID(ctx, req.BoxID, req.ToolIDs)
	if err != nil {
		s.Logger.WithContext(ctx).Errorf("select tool failed, err: %v", err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	if len(tools) != len(req.ToolIDs) {
		checkTools := []string{}
		for _, v := range tools {
			checkTools = append(checkTools, v.ToolID)
		}
		clist := utils.FindMissingElements(req.ToolIDs, checkTools)
		err = errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtToolNotFound,
			fmt.Sprintf("tools %v not found", clist))
		return
	}
	tx, err := s.DBTx.GetTx(ctx)
	if err != nil {
		s.Logger.WithContext(ctx).Errorf("get tx failed, err: %v", err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		} else {
			_ = tx.Commit()
		}
	}()
	err = s.deleteTools(ctx, tx, req.BoxID, tools)
	if err != nil {
		return
	}
	// Record audit log.
	go func() {
		var detils []metric.AuditLogToolDetil
		for _, tool := range tools {
			detils = append(detils, metric.AuditLogToolDetil{
				ToolID:   tool.ToolID,
				ToolName: tool.Name,
			})
		}
		accountAuthContext, ok := infracommon.GetAccountAuthContextFromCtx(ctx)
		if !ok {
			s.Logger.WithContext(ctx).Warnf("[DeleteBoxTool] GetAccountAuthContextFromCtx err :%v", err)
			return
		}
		s.AuditLog.Logger(ctx, &metric.AuditLogBuilderParams{
			TokenInfo: accountAuthContext.TokenInfo,
			Accessor:  accessor,
			Operation: metric.AuditLogOperationEdit,
			Object: &metric.AuditLogObject{
				Type: metric.AuditLogObjectTool,
				Name: toolBox.Name,
				ID:   toolBox.BoxID,
			},
			Detils: &metric.AuditLogToolDetils{
				Infos:         detils,
				OperationCode: metric.DeleteTool,
			},
		})
	}()
	return
}

// QueryToolList Query tool list (get the tool list in the toolbox)
func (s *ToolServiceImpl) QueryToolList(ctx context.Context, req *interfaces.QueryToolListReq) (resp *interfaces.QueryToolListResp, err error) {
	// record observable.
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	// If it is an external interface, verify whether it has the viewing and public access rights to the toolbox it belongs to.
	if infracommon.IsPublicAPIFromCtx(ctx) {
		var accessor *interfaces.AuthAccessor
		accessor, err = s.AuthService.GetAccessor(ctx, req.UserID)
		if err != nil {
			return
		}
		var authorized bool
		authorized, err = s.AuthService.OperationCheckAny(ctx, accessor, req.BoxID, interfaces.AuthResourceTypeToolBox, interfaces.AuthOperationTypeView, interfaces.AuthOperationTypePublicAccess)
		if err != nil {
			return
		}
		if !authorized {
			err = errors.NewHTTPError(ctx, http.StatusForbidden, errors.ErrExtCommonOperationForbidden, nil)
			return
		}
	}
	// Check if the toolbox exists.
	exist, boxDB, err := s.ToolBoxDB.SelectToolBox(ctx, req.BoxID)
	if err != nil {
		s.Logger.WithContext(ctx).Errorf("select toolbox failed, err: %v", err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	if !exist {
		err = errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtToolBoxNotFound, "toolbox not found")
		return
	}
	// Construct query conditions.
	filter := make(map[string]interface{})
	filter["all"] = req.All
	if req.ToolName != "" {
		filter["name"] = req.ToolName
	}
	if req.Status != "" {
		filter["status"] = req.Status
	}
	if req.QueryUserID != "" {
		filter["user_id"] = req.QueryUserID
	}
	// Query the total number of toolboxes.
	total, err := s.ToolDB.CountToolByBoxID(ctx, req.BoxID, filter)
	if err != nil {
		s.Logger.WithContext(ctx).Errorf("count tool failed by id: %s, err: %v", req.BoxID, err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	resp = &interfaces.QueryToolListResp{
		BoxID: req.BoxID,
		CommonPageResult: interfaces.CommonPageResult{
			Page:       req.Page,
			PageSize:   req.PageSize,
			TotalCount: int(total),
		},
		Tools: []*interfaces.ToolInfo{},
	}
	if total == 0 {
		return
	}
	// Calculate offset.
	var offset int
	if req.PageSize > 0 {
		offset = (req.Page - 1) * req.PageSize
		resp.TotalPage = int(total) / req.PageSize
		if int(total)%req.PageSize > 0 {
			resp.TotalPage++
		}
		resp.HasNext = req.Page < resp.TotalPage
		resp.HasPrev = req.Page > 1
	} else {
		resp.TotalPage = 1
		resp.PageSize = int(total)
	}
	// Construct sorting conditions.
	filter["sort_by"] = req.SortBy
	filter["sort_order"] = req.SortOrder
	filter["limit"] = req.PageSize
	filter["offset"] = offset
	// Query toolbox list.
	tools, err := s.ToolDB.SelectToolLisByBoxID(ctx, req.BoxID, filter)
	if err != nil {
		s.Logger.WithContext(ctx).Errorf("select tool list failed, err: %v", err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	// Collect information about tools.
	userIDs := []string{}
	toolInfos, _, err := s.batchGetToolInfoAndUserInfo(ctx, tools, userIDs, boxDB.ServerURL, interfaces.MetadataType(boxDB.MetadataType))
	if err != nil {
		return
	}
	resp.Tools = append(resp.Tools, toolInfos...)
	return
}

// UpdateToolStatus update tool status.
func (s *ToolServiceImpl) UpdateToolStatus(ctx context.Context, req *interfaces.UpdateToolStatusReq) (resp []*interfaces.ToolStatus, err error) {
	// record observable.
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	telemetry.SetSpanAttributes(ctx, map[string]interface{}{
		"box_id":  req.BoxID,
		"user_id": req.UserID,
	})
	// Permission verification.
	var accessor *interfaces.AuthAccessor
	accessor, err = s.AuthService.GetAccessor(ctx, req.UserID)
	if err != nil {
		return
	}
	err = s.AuthService.CheckModifyPermission(ctx, accessor, req.BoxID, interfaces.AuthResourceTypeToolBox)
	if err != nil {
		return
	}
	// Check if the toolbox exists.
	exist, toolBox, err := s.ToolBoxDB.SelectToolBox(ctx, req.BoxID)
	if err != nil {
		s.Logger.WithContext(ctx).Errorf("select toolbox failed, err: %v", err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	if !exist {
		err = errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtToolBoxNotFound, "toolbox not found")
		return
	}
	// Check if the tool exists.
	var toolIDs []string
	for _, v := range req.ToolStatusList {
		toolIDs = append(toolIDs, v.ToolID)
	}
	tools, err := s.ToolDB.SelectToolBoxByID(ctx, req.BoxID, toolIDs)
	if err != nil {
		s.Logger.WithContext(ctx).Errorf("select tool failed, err: %v", err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	checkTools := []string{}
	sourceMap := map[model.SourceType][]string{}
	sourceMap[model.SourceTypeOperator] = []string{}
	for _, v := range tools {
		checkTools = append(checkTools, v.ToolID)
		if v.SourceType == model.SourceTypeOperator {
			sourceMap[v.SourceType] = append(sourceMap[v.SourceType], v.SourceID)
		}
	}
	// Compare tool ID exists.
	clist := utils.FindMissingElements(toolIDs, checkTools)
	if len(clist) > 0 {
		err = errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtToolNotFound,
			fmt.Sprintf("tools %v not found", clist))
		return
	}
	if len(sourceMap[model.SourceTypeOperator]) > 0 {
		// Check whether dependent resources exist.
		var sourceIDToMetadataMap map[string]interfaces.IMetadataDB
		sourceIDToMetadataMap, err = s.MetadataService.BatchGetMetadataBySourceIDs(ctx, sourceMap)
		if err != nil {
			s.Logger.WithContext(ctx).Errorf("batch get metadata failed, err: %v", err)
			err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
			return
		}
		for _, v := range tools {
			if v.SourceType == model.SourceTypeOperator {
				if _, ok := sourceIDToMetadataMap[v.SourceID]; !ok {
					err = errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtToolRefOperatorNotFound,
						fmt.Sprintf("tool %s ref operator %s not found", v.ToolID, v.SourceID), v.Name)
					return
				}
			}
		}
	}

	// Update tool status.
	tx, err := s.DBTx.GetTx(ctx)
	if err != nil {
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		} else {
			_ = tx.Commit()
		}
	}()
	resp = []*interfaces.ToolStatus{}
	for _, tool := range req.ToolStatusList {
		err = s.ToolDB.UpdateToolStatus(ctx, tx, tool.ToolID, string(tool.Status), req.UserID)
		if err != nil {
			s.Logger.WithContext(ctx).Errorf("update tool status failed, err: %v", err)
			err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
			return
		}
		resp = append(resp, &interfaces.ToolStatus{
			ToolID: tool.ToolID,
			Status: tool.Status,
		})
	}
	// Record audit log.
	go func() {
		var detils []metric.AuditLogToolDetil
		for _, tool := range tools {
			detils = append(detils, metric.AuditLogToolDetil{
				ToolID:   tool.ToolID,
				ToolName: tool.Name,
			})
		}
		accountAuthContext, ok := infracommon.GetAccountAuthContextFromCtx(ctx)
		if !ok {
			s.Logger.WithContext(ctx).Warnf("[UpdateToolStatus] GetAccountAuthContextFromCtx err :%v", err)
			return
		}
		s.AuditLog.Logger(ctx, &metric.AuditLogBuilderParams{
			TokenInfo: accountAuthContext.TokenInfo,
			Accessor:  accessor,
			Operation: metric.AuditLogOperationEdit,
			Object: &metric.AuditLogObject{
				Type: metric.AuditLogObjectTool,
				Name: toolBox.Name,
				ID:   toolBox.BoxID,
			},
			Detils: &metric.AuditLogToolDetils{
				Infos:         detils,
				OperationCode: metric.UpdateToolStatus,
			},
		})
	}()
	return resp, nil
}

// getToolInfo Get tool information.
func (s *ToolServiceImpl) getToolInfo(ctx context.Context, tool *model.ToolDB, boxSvcURL string, boxMetadataType interfaces.MetadataType) (toolInfo *interfaces.ToolInfo, err error) {
	toolInfo, err = s.toolDBToToolInfo(ctx, tool)
	if err != nil {
		return
	}
	// Get metadata p.
	has, metadataDB, err := s.MetadataService.GetMetadataBySource(ctx, tool.SourceID, tool.SourceType)
	if err != nil {
		s.Logger.WithContext(ctx).Errorf("get metadata failed, err: %v", err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	if !has {
		s.Logger.WithContext(ctx).Errorf("metadata type: %s source_id: %s not found", tool.SourceType, tool.SourceID)
		toolInfo.MetadataType = boxMetadataType
		toolInfo.Metadata = metadata.DefaultMetadataInfo(boxMetadataType)
		return
	}
	// If it is an OpenAPI type, the ServerURL must be consistent with the boxSvcURL configured in the toolbox.
	metadataDB.SetServerURL(boxSvcURL)
	// Convert to structure.
	toolInfo.MetadataType = interfaces.MetadataType(metadataDB.GetType())
	toolInfo.Metadata = metadata.MetadataDBToStruct(metadataDB)
	return
}

// batchGetToolInfoAndUserInfo Get tools and user information in batches.
func (s *ToolServiceImpl) batchGetToolInfoAndUserInfo(ctx context.Context, tools []*model.ToolDB, userIDs []string,
	boxSvcURL string, boxMetadataType interfaces.MetadataType) (toolInfos []*interfaces.ToolInfo, userMap map[string]string, err error) {
	toolInfos = []*interfaces.ToolInfo{}
	sourceMap := map[model.SourceType][]string{}
	toolIDSourceMap := map[string]string{}
	// Assembly tool information.
	for _, toolDB := range tools {
		toolIDSourceMap[toolDB.ToolID] = toolDB.SourceID
		sourceMap[toolDB.SourceType] = append(sourceMap[toolDB.SourceType], toolDB.SourceID)
		userIDs = append(userIDs, toolDB.CreateUser, toolDB.UpdateUser)
		var toolInfo *interfaces.ToolInfo
		toolInfo, err = s.toolDBToToolInfo(ctx, toolDB)
		if err != nil {
			return
		}
		toolInfos = append(toolInfos, toolInfo)
	}
	// Get user name.
	userMap, err = s.UserMgnt.GetUsersName(ctx, userIDs)
	if err != nil {
		return
	}
	// Get tool metadata in batches.
	sourceIDToMetadataMap, err := s.MetadataService.BatchGetMetadataBySourceIDs(ctx, sourceMap)
	if err != nil {
		return
	}
	// Populate metadata information.
	for _, toolInfo := range toolInfos {
		toolInfo.CreateUser = utils.GetValueOrDefault(userMap, toolInfo.CreateUser, interfaces.UnknownUser)
		toolInfo.UpdateUser = utils.GetValueOrDefault(userMap, toolInfo.UpdateUser, interfaces.UnknownUser)
		metadataDB, ok := sourceIDToMetadataMap[toolIDSourceMap[toolInfo.ToolID]]
		if !ok {
			s.Logger.WithContext(ctx).Errorf("metadata not found, toolID: %s", toolInfo.ToolID)
			toolInfo.MetadataType = boxMetadataType
			toolInfo.Metadata = metadata.DefaultMetadataInfo(boxMetadataType)
			continue
		}
		metadataDB.SetServerURL(boxSvcURL)
		toolInfo.MetadataType = interfaces.MetadataType(metadataDB.GetType())
		toolInfo.Metadata = metadata.MetadataDBToStruct(metadataDB)
	}
	return
}

// checkBoxDuplicateName checks whether the toolbox name is duplicated.
func (s *ToolServiceImpl) checkBoxDuplicateName(ctx context.Context, name, boxID string) (err error) {
	has, boxDB, err := s.ToolBoxDB.SelectToolBoxByName(ctx, name, []string{string(interfaces.BizStatusPublished)})
	if err != nil {
		s.Logger.WithContext(ctx).Errorf("select toolbox by name failed, err: %v", err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, "select toolbox by name failed")
		return
	}
	if !has || (boxID != "" && boxDB.BoxID == boxID) {
		return
	}
	err = errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtToolBoxNameExists,
		fmt.Sprintf("toolbox name %s already exists", name), name)
	return
}
