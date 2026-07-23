package toolbox

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/infra/common"
	oerrors "github.com/openbkn-ai/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/infra/telemetry"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/logics/metric"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/logics/parsers"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/utils"
	"github.com/openbkn-ai/bkn-comm-go/otel/oteltrace"
)

// UpdateTool 更新工具
func (s *ToolServiceImpl) UpdateTool(ctx context.Context, req *interfaces.UpdateToolReq) (resp *interfaces.UpdateToolResp, err error) {
	// 记录可观测
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	telemetry.SetSpanAttributes(ctx, map[string]interface{}{
		"box_id":  req.BoxID,
		"user_id": req.UserID,
		"tool_id": req.ToolID,
	})
	// 权限校验
	var accessor *interfaces.AuthAccessor
	accessor, err = s.AuthService.GetAccessor(ctx, req.UserID)
	if err != nil {
		return
	}
	err = s.AuthService.CheckModifyPermission(ctx, accessor, req.BoxID, interfaces.AuthResourceTypeToolBox)
	if err != nil {
		return
	}
	// 检查工具箱是否存在
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
	// 检查工具元数据类型和请求更新是否一致
	if toolBox.MetadataType != string(req.MetadataType) {
		err = oerrors.DefaultHTTPError(ctx, http.StatusBadRequest, fmt.Sprintf("metadata type %s not match", toolBox.MetadataType))
		return
	}
	// 检查工具是否存在
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
	// 检查工具名称是否重名
	if tool.Name != req.ToolName {
		err = s.checkToolNameExist(ctx, req.BoxID, req.ToolName)
		if err != nil {
			// 交互设计要求返回指定错误信息：https://confluence.aishu.cn/pages/viewpage.action?pageId=280780968
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
	// 更新元数据
	err = s.updateToolMetadata(ctx, req, tool)
	if err != nil {
		return
	}
	// 记录审计日志
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

// 检查工具是否重名
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

// resolveFunctionCode 取本次编辑要落库的函数代码。
// 请求没带 code 时说明用户只改依赖或参数定义,沿用已存代码,避免整段元数据更新被跳过。
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
	// 元数据是整体重建的,请求没带的字段会被写成空值。编辑只想改其中一项时
	// （比如只换依赖）,其余字段必须沿用已存值,否则参数定义和依赖会被静默清空。
	if input.Code != "" && input.Inputs != nil && input.Outputs != nil &&
		input.Dependencies != nil && input.ScriptType != "" {
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
	// 参数定义落库时展开进了 API 规格,沿用时反解回来
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

// 校验并更新工具元数据
func (s *ToolServiceImpl) updateToolMetadata(ctx context.Context, req *interfaces.UpdateToolReq, toolDB *model.ToolDB) (err error) {
	var needUpdate bool
	switch req.MetadataType {
	case interfaces.MetadataTypeAPI:
		needUpdate = req.OpenAPIInput != nil && req.OpenAPIInput.Data != nil
	case interfaces.MetadataTypeFunc:
		// 只改依赖、参数定义而不改代码也是合法编辑,因此不再要求必须带 code。
		// code 为空时沿用已存代码,见下方 resolveFunctionCode。
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
			// 算子转换成的工具不允许直接编辑元数据
			err = oerrors.NewHTTPError(ctx, http.StatusMethodNotAllowed, oerrors.ErrExtToolOperatorNotAllowEdit,
				"operator tool not allow edit metadata")
		}
		if err != nil {
			return
		}
	}
	// 不需要更新元数据
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
	// 获取当前元数据信息
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

	// 解析并检查元数据
	switch toolDB.SourceType {
	case model.SourceTypeOpenAPI:
		// 解析并检查OpenAPI元数据
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
		// 组装元数据
		currentMetadataDB.SetSummary(metadata.GetSummary())
		currentMetadataDB.SetDescription(metadata.GetDescription())
		currentMetadataDB.SetPath(metadata.GetPath())
		currentMetadataDB.SetMethod(metadata.GetMethod())
		currentMetadataDB.SetServerURL(metadata.GetServerURL())
		currentMetadataDB.SetAPISpec(metadata.GetAPISpec())
	case model.SourceTypeFunction:
		// 函数不支持批量更新
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
		// 算子转换成的工具不允许直接编辑元数据
		err = oerrors.NewHTTPError(ctx, http.StatusMethodNotAllowed, oerrors.ErrExtToolOperatorNotAllowEdit,
			"operator tool not allow edit metadata")
		return
	}
	// 更新元数据
	currentMetadataDB.SetUpdateInfo(toolDB.UpdateUser)
	err = s.MetadataService.UpdateMetadata(ctx, tx, currentMetadataDB)
	if err != nil {
		s.Logger.WithContext(ctx).Errorf("update metadata failed, err: %v", err)
		err = oerrors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	// 更新工具
	err = s.ToolDB.UpdateTool(ctx, tx, toolDB)
	if err != nil {
		s.Logger.WithContext(ctx).Errorf("update tool failed, err: %v", err)
		err = oerrors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
	}
	return
}
