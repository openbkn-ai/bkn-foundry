package toolbox

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	oerrors "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/telemetry"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/metric"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/parsers"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/utils"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
)

// UpdateTool update tool.
func (s *ToolServiceImpl) UpdateTool(ctx context.Context, req *interfaces.UpdateToolReq) (resp *interfaces.UpdateToolResp, err error) {
	// record observable.
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	telemetry.SetSpanAttributes(ctx, map[string]interface{}{
		"box_id":  req.BoxID,
		"user_id": req.UserID,
		"tool_id": req.ToolID,
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
		err = oerrors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	if !exist {
		err = oerrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.ErrExtToolBoxNotFound, "toolbox not found")
		return
	}
	// Check whether the tool metadata type and requested update are consistent.
	if toolBox.MetadataType != string(req.MetadataType) {
		err = oerrors.DefaultHTTPError(ctx, http.StatusBadRequest, fmt.Sprintf("metadata type %s not match", toolBox.MetadataType))
		return
	}
	// Check if the tool exists.
	exist, tool, err := s.ToolDB.SelectTool(ctx, req.ToolID)
	if err != nil {
		s.Logger.WithContext(ctx).Errorf("select tool failed, err: %v", err)
		err = oerrors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	if !exist {
		err = oerrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.ErrExtToolNotFound,
			fmt.Sprintf("tool %s not found", req.ToolID))
		return
	}
	// Check if the tool name has the same name.
	if tool.Name != req.ToolName {
		err = s.checkToolNameExist(ctx, req.BoxID, req.ToolName)
		if err != nil {
			// Interaction design requires returning specified error information: https://confluence.aishu.cn/pages/viewpage.action?pageId=280780968.
			httErr := &oerrors.HTTPError{}
			if errors.As(err, &httErr) && httErr.HTTPCode == http.StatusConflict {
				err = httErr.WithDescription(oerrors.ErrExtCommonNameExists)
			}
			return
		}
		tool.Name = req.ToolName
	}
	tool.Description = req.ToolDesc
	tool.UpdateUser = req.UserID
	tool.UseRule = req.UseRule
	if req.ExtendInfo != nil {
		tool.ExtendInfo = utils.ObjectToJSON(req.ExtendInfo)
	}
	if req.GlobalParameters != nil {
		tool.Parameters = utils.ObjectToJSON(req.GlobalParameters)
	}
	// Update metadata.
	err = s.updateToolMetadata(ctx, req, tool)
	if err != nil {
		return
	}
	// Record audit log.
	go func() {
		accountAuthContext, ok := common.GetAccountAuthContextFromCtx(ctx)
		if !ok {
			s.Logger.WithContext(ctx).Warnf("[UpdateTool] GetAccountAuthContextFromCtx err :%v", err)
			return
		}
		s.AuditLog.Logger(ctx, &metric.AuditLogBuilderParams{
			TokenInfo: accountAuthContext.TokenInfo,
			Accessor:  accessor,
			Operation: metric.AuditLogOperationEdit,
			Object:    metric.NewAuditLogObject(metric.AuditLogObjectTool, toolBox.Name, toolBox.BoxID),
			Detils: metric.NewAuditLogToolDetils(metric.EditTool, []metric.AuditLogToolDetil{
				{ToolID: tool.ToolID, ToolName: tool.Name},
			}),
		})
	}()
	resp = &interfaces.UpdateToolResp{
		BoxID:  req.BoxID,
		ToolID: req.ToolID,
	}
	return
}

// Check if the tool has the same name.
func (s *ToolServiceImpl) checkToolNameExist(ctx context.Context, boxID, toolName string) (err error) {
	exist, _, err := s.ToolDB.SelectBoxToolByName(ctx, boxID, toolName)
	if err != nil {
		s.Logger.WithContext(ctx).Errorf("select tool by name failed, err: %v", err)
		err = oerrors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	if exist {
		err = oerrors.NewHTTPError(ctx, http.StatusConflict, oerrors.ErrExtToolExists,
			"tool name already exists", toolName)
	}
	return
}

// resolveFunctionCode takes the function code to be added to the library for this edit.
// When the request does not include code, it means that the user only changes the dependencies or parameter definitions and uses the existing code to prevent the entire metadata update from being skipped.
func (s *ToolServiceImpl) buildFunctionInput(ctx context.Context, req *interfaces.UpdateToolReq,
	toolDB *model.ToolDB) (*interfaces.FunctionInput, error) {
	edit := req.FunctionInputEdit
	input := &interfaces.FunctionInput{
		Name:            req.ToolName,
		Description:     req.ToolDesc,
		Inputs:          edit.Inputs,
		Outputs:         edit.Outputs,
		ScriptType:      edit.ScriptType,
		Code:            edit.Code,
		Dependencies:    edit.Dependencies,
		DependenciesURL: edit.DependenciesURL,
	}
	// The metadata is reconstructed as a whole, and fields not included in the request will be written as null values. When the editor only wants to change one of the items.
	// (For example, only changing dependencies), the remaining fields must use the existing values, otherwise the parameter definitions and dependencies will be silently cleared.
	if input.Code != "" && input.Inputs != nil && input.Outputs != nil &&
		input.Dependencies != nil && input.ScriptType != "" && input.DependenciesURL != "" {
		return input, nil
	}
	has, current, err := s.MetadataService.GetMetadataBySource(ctx, toolDB.SourceID, toolDB.SourceType)
	if err != nil {
		s.Logger.WithContext(ctx).Errorf("select metadata failed, err: %v", err)
		return nil, oerrors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
	}
	if !has {
		return nil, oerrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.ErrExtMetadataNotFound,
			fmt.Sprintf("metadata %s not found", toolDB.SourceID))
	}
	if input.Code == "" {
		input.Code = current.GetCode()
	}
	if input.ScriptType == "" {
		input.ScriptType = interfaces.ScriptType(current.GetScriptType())
	}
	if input.Dependencies == nil && current.GetDependencies() != "" {
		input.Dependencies = utils.JSONToObject[[]*interfaces.DependencyInfo](current.GetDependencies())
	}
	if input.DependenciesURL == "" {
		input.DependenciesURL = current.GetDependenciesURL()
	}
	// The parameter definition is expanded into the API specification when it is stored, and is decoded back when it is used.
	if input.Inputs == nil || input.Outputs == nil {
		storedInputs, storedOutputs := parsers.FunctionParamsFromAPISpec(current.GetAPISpec())
		if input.Inputs == nil {
			input.Inputs = storedInputs
		}
		if input.Outputs == nil {
			input.Outputs = storedOutputs
		}
	}
	return input, nil
}

// Verify and update tool metadata.
func (s *ToolServiceImpl) updateToolMetadata(ctx context.Context, req *interfaces.UpdateToolReq, toolDB *model.ToolDB) (err error) {
	var needUpdate bool
	switch req.MetadataType {
	case interfaces.MetadataTypeAPI:
		needUpdate = req.OpenAPIInput != nil && req.OpenAPIInput.Data != nil
	case interfaces.MetadataTypeFunc:
		// Only changing dependencies and parameter definitions without changing the code is also legal editing, so it is no longer required to include code.
		// When code is empty, the stored code will be used, see resolveFunctionCode below.
		needUpdate = req.FunctionInputEdit != nil
	}
	var metadatas []interfaces.IMetadataDB
	if needUpdate {
		switch toolDB.SourceType {
		case model.SourceTypeOpenAPI:
			metadatas, err = s.MetadataService.ParseMetadata(ctx, req.MetadataType, req.OpenAPIInput)
		case model.SourceTypeFunction:
			var functionInput *interfaces.FunctionInput
			functionInput, err = s.buildFunctionInput(ctx, req, toolDB)
			if err != nil {
				return
			}
			metadatas, err = s.MetadataService.ParseMetadata(ctx, req.MetadataType, functionInput)
		case model.SourceTypeOperator:
			// The tool converted by the operator does not allow direct editing of metadata.
			err = oerrors.NewHTTPError(ctx, http.StatusMethodNotAllowed, oerrors.ErrExtToolOperatorNotAllowEdit,
				"operator tool not allow edit metadata")
		}
		if err != nil {
			return
		}
	}
	// No need to update metadata.
	if len(metadatas) == 0 {
		err = s.ToolDB.UpdateTool(ctx, nil, toolDB)
		if err != nil {
			s.Logger.WithContext(ctx).Errorf("update tool failed, err: %v", err)
			err = oerrors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		}
		return
	}
	tx, err := s.DBTx.GetTx(ctx)
	if err != nil {
		err = oerrors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		} else {
			_ = tx.Commit()
		}
	}()
	// Get current metadata information.
	has, currentMetadataDB, err := s.MetadataService.GetMetadataBySource(ctx, toolDB.SourceID, toolDB.SourceType)
	if err != nil {
		s.Logger.WithContext(ctx).Errorf("select metadata failed, err: %v", err)
		err = oerrors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return err
	}
	if !has {
		err = oerrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.ErrExtMetadataNotFound,
			fmt.Sprintf("metadata %s not found", toolDB.SourceID))
		return err
	}

	// Parse and inspect metadata.
	switch toolDB.SourceType {
	case model.SourceTypeOpenAPI:
		// Parse and inspect OpenAPI metadata.
		var metadata interfaces.IMetadataDB
		for _, value := range metadatas {
			if value.GetPath() == currentMetadataDB.GetPath() && value.GetMethod() == currentMetadataDB.GetMethod() {
				metadata = value
				break
			}
		}
		if metadata == nil {
			err = oerrors.NewHTTPError(ctx, http.StatusNotFound, oerrors.ErrExtToolNotExistInFile,
				fmt.Sprintf("no matched method path found, path: %s, method: %s",
					currentMetadataDB.GetPath(), currentMetadataDB.GetMethod()))
			return
		}
		// Assembling metadata.
		currentMetadataDB.SetSummary(metadata.GetSummary())
		currentMetadataDB.SetDescription(metadata.GetDescription())
		currentMetadataDB.SetPath(metadata.GetPath())
		currentMetadataDB.SetMethod(metadata.GetMethod())
		currentMetadataDB.SetServerURL(metadata.GetServerURL())
		currentMetadataDB.SetAPISpec(metadata.GetAPISpec())
	case model.SourceTypeFunction:
		// The function does not support batch updates.
		metadata := metadatas[0]
		currentMetadataDB.SetSummary(metadata.GetSummary())
		currentMetadataDB.SetDescription(metadata.GetDescription())
		currentMetadataDB.SetPath(metadata.GetPath())
		currentMetadataDB.SetMethod(metadata.GetMethod())
		currentMetadataDB.SetServerURL(metadata.GetServerURL())
		currentMetadataDB.SetAPISpec(metadata.GetAPISpec())
		currentMetadataDB.SetCode(metadata.GetCode())
		currentMetadataDB.SetScriptType(metadata.GetScriptType())
		currentMetadataDB.SetDependencies(metadata.GetDependencies())
		currentMetadataDB.SetDependenciesURL(metadata.GetDependenciesURL())
	case model.SourceTypeOperator:
		// The tool converted by the operator does not allow direct editing of metadata.
		err = oerrors.NewHTTPError(ctx, http.StatusMethodNotAllowed, oerrors.ErrExtToolOperatorNotAllowEdit,
			"operator tool not allow edit metadata")
		return
	}
	// Update metadata.
	currentMetadataDB.SetUpdateInfo(toolDB.UpdateUser)
	err = s.MetadataService.UpdateMetadata(ctx, tx, currentMetadataDB)
	if err != nil {
		s.Logger.WithContext(ctx).Errorf("update metadata failed, err: %v", err)
		err = oerrors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	// Update tool.
	err = s.ToolDB.UpdateTool(ctx, tx, toolDB)
	if err != nil {
		s.Logger.WithContext(ctx).Errorf("update tool failed, err: %v", err)
		err = oerrors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
	}
	return
}
