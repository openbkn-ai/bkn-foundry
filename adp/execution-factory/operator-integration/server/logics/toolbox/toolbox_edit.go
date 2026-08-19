package toolbox

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/telemetry"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/metric"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
)

// UpdateToolBox update toolbox.
func (s *ToolServiceImpl) UpdateToolBox(ctx context.Context, req *interfaces.UpdateToolBoxReq) (resp *interfaces.UpdateToolBoxResp, err error) {
	// Record observability.
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	telemetry.SetSpanAttributes(ctx, map[string]interface{}{
		"user_id":  req.UserID,
		"box_id":   req.BoxID,
		"box_name": req.BoxName,
	})

	// Verify editing permissions.
	var accessor *interfaces.AuthAccessor
	accessor, err = s.AuthService.GetAccessor(ctx, req.UserID)
	if err != nil {
		return
	}
	err = s.AuthService.CheckModifyPermission(ctx, accessor, req.BoxID, interfaces.AuthResourceTypeToolBox)
	if err != nil {
		return
	}
	// Check if the category exists.
	if !s.CategoryManager.CheckCategory(req.Category) {
		err = errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtToolBoxCategoryTypeInvalid,
			fmt.Sprintf(" %s category not found", req.Category))
		return
	}
	// Check if the tool exists.
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
	// The metadata type will not change after it is created, and the edit request does not need to be included. If not provided, the stored value will be used——.
	// The following branches by type. If left blank, neither branch will be hit, and the metadata will be silently skipped.
	if req.MetadataType == "" {
		req.MetadataType = interfaces.MetadataType(toolBox.MetadataType)
	}
	// Check whether the openapi type toolbox has filled in the service address.
	if req.MetadataType == interfaces.MetadataTypeAPI {
		err = s.Validator.ValidatorURL(ctx, req.BoxSvcURL)
		if err != nil {
			return
		}
	}
	// Check if toolbox name exists.
	isNameChanged := toolBox.Name != req.BoxName
	if isNameChanged {
		err = s.checkBoxDuplicateName(ctx, req.BoxName, toolBox.BoxID)
		if err != nil {
			return
		}
	}

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
	resp = &interfaces.UpdateToolBoxResp{}
	// Update metadata.
	switch req.MetadataType {
	case interfaces.MetadataTypeAPI:
		var metadatas []interfaces.IMetadataDB
		if req.OpenAPIInput != nil && req.OpenAPIInput.Data != nil {
			metadatas, err = s.MetadataService.ParseMetadata(ctx, req.MetadataType, req.OpenAPIInput.Data)
		}
		if len(metadatas) > 0 {
			resp.EditTools, err = s.batchUpdateOpenAPIToolMetadata(ctx, tx, toolBox.BoxID, req.UserID, metadatas)
			if err != nil {
				return
			}
		}
		toolBox.ServerURL = req.BoxSvcURL
	case interfaces.MetadataTypeFunc:
	}
	// Update toolbox.
	toolBox.Name = req.BoxName
	toolBox.Description = req.BoxDesc
	toolBox.UpdateUser = req.UserID
	toolBox.Category = string(req.Category)
	err = s.ToolBoxDB.UpdateToolBox(ctx, tx, toolBox)
	if err != nil {
		s.Logger.WithContext(ctx).Errorf("update toolbox failed, err: %v", err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	// If the name changes, trigger permission resource change notification.
	if isNameChanged {
		authResource := &interfaces.AuthResource{
			ID:   toolBox.BoxID,
			Name: toolBox.Name,
			Type: string(interfaces.AuthResourceTypeToolBox),
		}
		err = s.AuthService.NotifyResourceChange(ctx, authResource)
	}
	// Record audit log.
	go func() {
		accountAuthContext, ok := common.GetAccountAuthContextFromCtx(ctx)
		if !ok {
			s.Logger.WithContext(ctx).Warnf("[UpdateToolBox] GetAccountAuthContextFromCtx err :%v", err)
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
		})
	}()
	resp.BoxID = req.BoxID
	return
}

// Batch update tool metadata of OpenAPI type.
func (s *ToolServiceImpl) batchUpdateOpenAPIToolMetadata(ctx context.Context, tx *sql.Tx, boxID, userID string, updateMetadatas []interfaces.IMetadataDB) (resp []*interfaces.EditToolInfo, err error) {
	resp = []*interfaces.EditToolInfo{}
	// Get all tools in the current toolbox.
	var tools []*model.ToolDB
	tools, err = s.ToolDB.SelectToolByBoxID(ctx, boxID)
	if err != nil {
		s.Logger.WithContext(ctx).Errorf("select toolbox tools failed, err: %v", err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	// Tools to collect metadata that needs to be changed.
	metadataVersions := []string{}
	toolMap := make(map[string]*model.ToolDB)
	for _, tool := range tools {
		if tool.SourceType != model.SourceTypeOpenAPI {
			continue
		}
		metadataVersions = append(metadataVersions, tool.SourceID)
		toolMap[tool.SourceID] = tool
	}
	// Get all metadata.
	currentMetadatas, err := s.MetadataService.BatchGetMetadata(ctx, metadataVersions, []string{})
	if err != nil {
		s.Logger.WithContext(ctx).Errorf("select metadata failed, err: %v", err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	// Build a mapping table for updating metadata.
	updateMetadataMap := map[string]interfaces.IMetadataDB{}
	for _, metadata := range updateMetadatas {
		updateMetadataMap[validatorMethodPath(metadata.GetMethod(), metadata.GetPath())] = metadata
	}
	// Iterate through all metadata and check if there are any changes.
	var changed bool
	for _, metadata := range currentMetadatas {
		// Check for changes.
		_, changed = updateMetadataMap[validatorMethodPath(metadata.GetMethod(), metadata.GetPath())]
		if changed {
			break
		}
	}
	if !changed {
		// Interaction design requires returning specified error information: https://confluence.aishu.cn/pages/viewpage.action?pageId=280780968.
		err = errors.NewHTTPError(ctx, http.StatusNotFound, errors.ErrExtCommonNoMatchedMethodPath,
			"no matched method path found").WithDescription(errors.ErrExtToolNotExistInFile)
		return
	}
	// Update metadata and tools.
	for _, metadata := range currentMetadatas {
		key := validatorMethodPath(metadata.GetMethod(), metadata.GetPath())
		waitUpdateMetadata, ok := updateMetadataMap[key]
		if !ok {
			continue
		}
		// Update metadata.
		metadata.SetSummary(waitUpdateMetadata.GetSummary())
		metadata.SetDescription(waitUpdateMetadata.GetDescription())
		metadata.SetPath(waitUpdateMetadata.GetPath())
		metadata.SetMethod(waitUpdateMetadata.GetMethod())
		metadata.SetServerURL(waitUpdateMetadata.GetServerURL())
		metadata.SetAPISpec(waitUpdateMetadata.GetAPISpec())
		metadata.SetUpdateInfo(userID)
		err = s.MetadataService.UpdateMetadata(ctx, tx, metadata)
		if err != nil {
			s.Logger.WithContext(ctx).Errorf("update metadata failed, err: %v", err)
			err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
			return
		}
		// Update tool.
		toolDB, ok := toolMap[metadata.GetVersion()]
		if !ok {
			continue
		}
		toolDB.UpdateTime = time.Now().UnixNano()
		toolDB.UpdateUser = userID
		err = s.ToolDB.UpdateTool(ctx, tx, toolDB)
		if err != nil {
			s.Logger.WithContext(ctx).Errorf("update tool failed, err: %v", err)
			err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
			return
		}
		// Tools for collecting changes.
		resp = append(resp, &interfaces.EditToolInfo{
			ToolID: toolDB.ToolID,
			Status: interfaces.ToolStatusType(toolDB.Status),
			Name:   toolDB.Name,
			Desc:   toolDB.Description,
		})
	}
	return
}
