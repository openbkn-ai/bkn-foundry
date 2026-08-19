package toolbox

import (
	"context"
	"fmt"
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/telemetry"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/metric"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/utils"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
)

// CreateTool tool management.
func (s *ToolServiceImpl) CreateTool(ctx context.Context, req *interfaces.CreateToolReq) (resp *interfaces.CreateToolResp, err error) {
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
		err = errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtToolBoxNotFound,
			fmt.Sprintf("toolbox %s not found", req.BoxID))
		return
	}
	// The built-in toolbox does not allow adding tools.
	if toolBox.IsInternal {
		err = errors.DefaultHTTPError(ctx, http.StatusForbidden, "internal toolbox cannot add tools")
		return
	}
	// Parse imported data.
	var metadataList []interfaces.IMetadataDB
	switch req.MetadataType {
	case interfaces.MetadataTypeFunc:
		metadataList, err = s.MetadataService.ParseMetadata(ctx, req.MetadataType, req.FunctionInput)
	case interfaces.MetadataTypeAPI:
		metadataList, err = s.MetadataService.ParseMetadata(ctx, req.MetadataType, req.OpenAPIInput)
	default:
		err = errors.DefaultHTTPError(ctx, http.StatusBadRequest, fmt.Sprintf("metadata type %s not found", req.MetadataType))
	}
	if err != nil {
		return
	}
	// Check imported tools for duplicate tools.
	tools, validatorNameMap, validatorMethodPathMap, err := s.parseOpenAPIToMetadata(ctx, req.BoxID, req.UserID, metadataList, false)
	if err != nil {
		return
	}
	// Remove duplicates.
	failuresVailMap, err := s.checkToolConflict(ctx, req.BoxID, validatorNameMap, validatorMethodPathMap)
	if err != nil {
		return
	}
	resp = &interfaces.CreateToolResp{
		BoxID:      req.BoxID,
		SuccessIDs: []string{},
		Failures:   []interfaces.CreateToolFailureResult{},
	}
	// Assemble information and save tools.
	extendInfo := utils.ObjectToJSON(req.ExtendInfo)
	globalParameters := utils.ObjectToJSON(req.GlobalParameters)
	useRule := req.UseRule
	var detils []metric.AuditLogToolDetil
	for i, tool := range tools {
		// Record failure information.
		if failuresVailMap[tool.Name] != nil {
			resp.FailureCount++
			resp.Failures = append(resp.Failures, interfaces.CreateToolFailureResult{Error: failuresVailMap[tool.Name], ToolName: tool.Name})
			continue
		}
		// Save tool.
		tool.ExtendInfo = extendInfo
		tool.UseRule = useRule
		tool.Parameters = globalParameters
		toolID, err := s.saveToolToBox(ctx, tool, metadataList[i])
		if err != nil {
			resp.FailureCount++
			resp.Failures = append(resp.Failures, interfaces.CreateToolFailureResult{Error: err, ToolName: tool.Name})
			continue
		}
		// Record success information.
		resp.SuccessCount++
		resp.SuccessIDs = append(resp.SuccessIDs, toolID)
		detils = append(detils, metric.AuditLogToolDetil{ToolID: toolID, ToolName: tool.Name})
	}
	// Record audit log.
	go func() {
		accountAuthContext, ok := common.GetAccountAuthContextFromCtx(ctx)
		if !ok {
			s.Logger.WithContext(ctx).Warnf("[CreateTool] GetAccountAuthContextFromCtx err :%v", err)
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
				OperationCode: metric.AddTool,
			},
		})
	}()
	return resp, nil
}

// Check whether the new tool conflicts with existing tools.
func (s *ToolServiceImpl) checkToolConflict(ctx context.Context, boxID string, validatorNameMap, validatorMethodPathMap map[string]bool) (
	failuresVailMap map[string]error, err error) {
	// Check if the tool exists.
	toolList, err := s.ToolDB.SelectToolByBoxID(ctx, boxID)
	if err != nil {
		s.Logger.WithContext(ctx).Errorf("select tool failed, err: %v", err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	// Remove duplicates.
	failuresVailMap = map[string]error{}
	for _, tool := range toolList {
		if validatorNameMap[tool.Name] {
			failuresVailMap[tool.Name] = errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtToolExists,
				fmt.Sprintf("tool name %s exist", tool.Name), tool.Name)
			continue
		}
		if tool.SourceType == model.SourceTypeFunction {
			continue
		}
		// Get metadata.
		var has bool
		var metadata interfaces.IMetadataDB
		has, metadata, err = s.MetadataService.GetMetadataBySource(ctx, tool.SourceID, tool.SourceType)
		if err != nil {
			failuresVailMap[tool.Name] = err
			continue
		}
		if !has {
			continue
		}
		val := validatorMethodPath(metadata.GetMethod(), metadata.GetPath())
		if validatorMethodPathMap[val] {
			failuresVailMap[tool.Name] = errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtToolExists,
				fmt.Sprintf("tool %s exist", val), val)
			continue
		}
	}
	return
}

// saveToolToBox adds a tool to the toolbox.
func (s *ToolServiceImpl) saveToolToBox(ctx context.Context, tool *model.ToolDB, metadata interfaces.IMetadataDB) (toolID string, err error) {
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
	var sourceID string
	sourceID, err = s.MetadataService.RegisterMetadata(ctx, tx, metadata)
	if err != nil {
		s.Logger.WithContext(ctx).Errorf("insert metadata failed, err: %v", err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	tool.SourceID = sourceID
	toolID, err = s.ToolDB.InsertTool(ctx, tx, tool)
	if err != nil {
		s.Logger.WithContext(ctx).Errorf("insert tool failed, err: %v", err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
	}
	return
}
