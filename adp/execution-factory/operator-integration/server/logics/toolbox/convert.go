// Package toolbox toolbox, tool management.
// @file convert.go
// @description: Convert operator to tool.
package toolbox

import (
	"context"
	"fmt"
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/metric"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/utils"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
)

// ConvertOperatorToTool Convert operator to tool.
func (s *ToolServiceImpl) ConvertOperatorToTool(ctx context.Context, req *interfaces.ConvertOperatorToToolReq) (resp *interfaces.ConvertOperatorToToolResp, err error) {
	// record observable.
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	// Verify whether you have editing permissions for the toolbox it belongs to.
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
		err = errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtToolBoxNotFound,
			fmt.Sprintf("toolbox %s not found", req.BoxID))
		return
	}
	// TODO: Built-in tools do not allow adding tools.
	if toolBox.IsInternal {
		err = errors.DefaultHTTPError(ctx, http.StatusForbidden, "internal toolbox cannot add tools")
		return
	}
	operatorCheckInfo, err := s.OperatorMgnt.CheckAddAsTool(ctx, req.OperatorID, req.UserID)
	if err != nil {
		return
	}
	// Check whether the operator metadata type and tool are consistent.
	if toolBox.MetadataType != operatorCheckInfo.Metadata.GetType() {
		err = errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtToolConvertMetadataTypeNotMatch,
			fmt.Sprintf("operator %s metadata type %s not match toolbox metadata type %s", operatorCheckInfo.OperatorID, operatorCheckInfo.Metadata.GetType(), toolBox.MetadataType))
		return
	}

	resp = &interfaces.ConvertOperatorToToolResp{
		BoxID: req.BoxID,
	}
	switch interfaces.MetadataType(operatorCheckInfo.Metadata.GetType()) {
	case interfaces.MetadataTypeAPI, interfaces.MetadataTypeFunc:
		metadataDB := operatorCheckInfo.Metadata
		err = s.checkBoxToolSame(ctx, req.BoxID, operatorCheckInfo.Name, metadataDB.GetMethod(), metadataDB.GetPath())
		if err != nil {
			return
		}
		// Convert operator to tool.
		tool := &model.ToolDB{
			BoxID:       req.BoxID,
			Name:        operatorCheckInfo.Name,
			Description: metadataDB.GetDescription(),
			SourceID:    operatorCheckInfo.OperatorID,
			SourceType:  model.SourceTypeOperator,
			Status:      string(interfaces.ToolStatusTypeDisabled),
			UseRule:     req.UseRule,
			ExtendInfo:  utils.ObjectToJSON(req.ExtendInfo),
			Parameters:  utils.ObjectToJSON(req.GlobalParameters),
			CreateUser:  req.UserID,
			UpdateUser:  req.UserID,
		}
		// insert tool.
		resp.ToolID, err = s.ToolDB.InsertTool(ctx, nil, tool)
		if err != nil {
			s.Logger.WithContext(ctx).Warnf("insert tool failed, err: %v", err)
			err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
			return
		}
	default:
		err = errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtToolConvertOnlySupportAPI,
			"only api operators can be published as tools")
		return
	}
	// Record audit log.
	go func() {
		accountAuthContext, ok := common.GetAccountAuthContextFromCtx(ctx)
		if !ok {
			s.Logger.WithContext(ctx).Warnf("[ConvertOperatorToTool] GetAccountAuthContextFromCtx err :%v", err)
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
				Infos: []metric.AuditLogToolDetil{
					{
						ToolID:   resp.ToolID,
						ToolName: operatorCheckInfo.Name,
					},
				},
				OperationCode: metric.ImportToolFromOperator,
			},
		})
	}()
	return
}

// checkBoxToolSame checks whether a tool with the same name exists in the toolbox.
func (s *ToolServiceImpl) checkBoxToolSame(ctx context.Context, boxID, name, method, path string) (err error) {
	// Check if the tool exists.
	toolList, err := s.ToolDB.SelectToolByBoxID(ctx, boxID)
	if err != nil {
		s.Logger.WithContext(ctx).Errorf("select tool failed, err: %v", err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	for _, tool := range toolList {
		if tool.Name == name {
			err = errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtToolExists,
				fmt.Sprintf("tool name %s exist", tool.Name), tool.Name)
			return
		}
		var toolInfo *interfaces.ToolInfo
		toolInfo, err = s.getToolInfo(ctx, tool, "", "")
		if err != nil {
			return
		}
		if toolInfo.Metadata == nil {
			s.Logger.WithContext(ctx).Warnf("toolbox %s tool %s:%s metadata is nil", boxID, tool.Name, tool.ToolID)
			continue
		}
		val := validatorMethodPath(toolInfo.Metadata.Method, toolInfo.Metadata.Path)
		if val == validatorMethodPath(method, path) {
			err = errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtToolExists,
				fmt.Sprintf("tool %s exist", val), val)
			return
		}
	}
	return
}
