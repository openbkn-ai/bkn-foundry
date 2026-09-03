package toolbox

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/metric"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
)

// CreateToolBox toolbox management.
func (s *ToolServiceImpl) CreateToolBox(ctx context.Context, req *interfaces.CreateToolBoxReq) (resp *interfaces.CreateToolBoxResp, err error) {
	// record observable.
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	// Check new permissions.
	var accessor *interfaces.AuthAccessor
	accessor, err = s.AuthService.GetAccessor(ctx, req.UserID)
	if err != nil {
		return
	}
	err = s.AuthService.CheckCreatePermission(ctx, accessor, interfaces.AuthResourceTypeToolBox)
	if err != nil {
		return
	}
	// 1. Parameter analysis and verification.
	metadatas, err := s.parseAndInitDefaultValues(ctx, req)
	if err != nil {
		return
	}
	// 2. Verify whether the toolbox name exists.
	err = s.checkBoxDuplicateName(ctx, req.BoxName, "")
	if err != nil {
		return
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

	// Add toolbox.
	toolBox := &model.ToolboxDB{
		Name:         req.BoxName,
		Description:  req.BoxDesc,
		Status:       interfaces.BizStatusUnpublish.String(),
		Source:       req.Source,
		Category:     string(req.Category),
		ServerURL:    req.BoxSvcURL,
		CreateUser:   req.UserID,
		CreateTime:   time.Now().UnixNano(),
		UpdateUser:   req.UserID,
		UpdateTime:   time.Now().UnixNano(),
		MetadataType: string(req.MetadataType),
	}
	var boxID string
	boxID, err = s.ToolBoxDB.InsertToolBox(ctx, tx, toolBox)
	if err != nil {
		s.Logger.WithContext(ctx).Errorf("insert toolbox failed, err: %v", err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	// Check for metadata changes.
	var detils []metric.AuditLogToolDetil
	if len(metadatas) > 0 {
		var tools []*model.ToolDB
		tools, _, _, err = s.parseOpenAPIToMetadata(ctx, boxID, req.UserID, metadatas, false)
		if err != nil {
			return
		}
		for i, tool := range tools {
			// Add metadata.
			tool.SourceID, err = s.MetadataService.RegisterMetadata(ctx, tx, metadatas[i])
			if err != nil {
				s.Logger.WithContext(ctx).Errorf("register metadata failed, err: %v", err)
				err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
				return
			}
			// Add tool.
			var toolID string
			toolID, err = s.ToolDB.InsertTool(ctx, tx, tool)
			if err != nil {
				s.Logger.WithContext(ctx).Errorf("insert tool failed, err: %v", err)
				err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
				return
			}
			detils = append(detils, metric.AuditLogToolDetil{
				ToolID:   toolID,
				ToolName: tool.Name,
			})
		}
	}
	// Triggering a new policy, the creator has all operating permissions on the current resources by default.
	err = s.AuthService.CreateOwnerPolicy(ctx, accessor, &interfaces.AuthResource{
		ID:   boxID,
		Type: string(interfaces.AuthResourceTypeToolBox),
		Name: req.BoxName,
	})
	if err != nil {
		return
	}
	// Record audit log.
	go func() {
		accountAuthContext, ok := common.GetAccountAuthContextFromCtx(ctx)
		if !ok {
			s.Logger.WithContext(ctx).Warnf("[CreateToolBox] GetAccountAuthContextFromCtx err :%v", err)
			return
		}
		s.AuditLog.Logger(ctx, &metric.AuditLogBuilderParams{
			TokenInfo: accountAuthContext.TokenInfo,
			Accessor:  accessor,
			Operation: metric.AuditLogOperationCreate,
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
	resp = &interfaces.CreateToolBoxResp{
		BoxID: boxID,
	}
	return
}

// Parse and initialize default values.
func (s *ToolServiceImpl) parseAndInitDefaultValues(ctx context.Context, req *interfaces.CreateToolBoxReq) (metadatas []interfaces.IMetadataDB, err error) {
	switch req.MetadataType {
	case interfaces.MetadataTypeAPI:
		if req.OpenAPIInput != nil && req.OpenAPIInput.Data != nil {
			// Parse API data.
			var rawContent any
			rawContent, err = s.MetadataService.ParseRawContent(ctx, req.MetadataType, req.OpenAPIInput)
			if err != nil {
				s.Logger.WithContext(ctx).Infof("parse openapi failed, err: %v", err)
				return
			}
			var content *interfaces.OpenAPIContent
			content, ok := rawContent.(*interfaces.OpenAPIContent)
			if !ok {
				err = errors.DefaultHTTPError(ctx, http.StatusBadRequest, "openapi content type error")
				s.Logger.WithContext(ctx).Infof("parse openapi failed, err: %v", err)
				return
			}
			if req.BoxName == "" {
				req.BoxName = content.Info.Title
			}
			if req.BoxDesc == "" {
				req.BoxDesc = content.Info.Description
			}
			if req.BoxSvcURL == "" {
				req.BoxSvcURL = content.SererURL
			}
			// Parse metadata.
			metadatas, err = s.MetadataService.ParseMetadata(ctx, req.MetadataType, req.OpenAPIInput)
			if err != nil {
				s.Logger.WithContext(ctx).Infof("parse openapi failed, err: %v", err)
				return
			}
		}
		err = s.Validator.ValidatorURL(ctx, req.BoxSvcURL)
		if err != nil {
			return
		}
	case interfaces.MetadataTypeFunc:
		req.BoxSvcURL = interfaces.AOIServerURL
	default:
		err = errors.DefaultHTTPError(ctx, http.StatusBadRequest, fmt.Sprintf("unsupported metadata type: %s", req.MetadataType))
		return
	}
	// When description is empty, name is used by default.
	if req.BoxDesc == "" {
		req.BoxDesc = req.BoxName
	}
	err = s.Validator.ValidatorToolBoxName(ctx, req.BoxName)
	if err != nil {
		return
	}
	err = s.Validator.ValidatorToolBoxDesc(ctx, req.BoxDesc)
	return
}

// Extract tool information from metadata.
func (s *ToolServiceImpl) parseOpenAPIToMetadata(ctx context.Context, boxID, userID string,
	metadatas []interfaces.IMetadataDB, isInternalTool bool) (tools []*model.ToolDB, validatorNameMap, validatorMethodPathMap map[string]bool, err error) {
	// Check if the tool has the same name.
	validatorMethodPathMap = make(map[string]bool)
	validatorNameMap = make(map[string]bool)
	for _, metadata := range metadatas {
		// Check tool name.
		err = s.Validator.ValidatorToolName(ctx, metadata.GetSummary())
		if err != nil {
			return
		}
		var useRule string
		description := metadata.GetDescription()
		// If it is a built-in tool, desc exceeds the limit and injects content into useRule, and desc is filled with summary.
		err = s.Validator.ValidatorToolDesc(ctx, description)
		if err != nil {
			if !isInternalTool {
				return
			}
			err = nil
			useRule = description
			description = metadata.GetSummary()
		}
		// Are the tool names duplicated?.
		if validatorNameMap[metadata.GetSummary()] {
			err = errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtToolExists,
				fmt.Sprintf("tool name %s duplicate", metadata.GetSummary()), metadata.GetSummary())
			return
		}
		validatorNameMap[metadata.GetSummary()] = true
		// Check if toolpath duplicates exist.
		if metadata.GetType() == string(interfaces.MetadataTypeAPI) {
			val := validatorMethodPath(metadata.GetMethod(), metadata.GetPath())
			if validatorMethodPathMap[val] { // Repeat.
				err = errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtToolExists,
					fmt.Sprintf("tool info %s duplicate", val), val)
				return
			}
			validatorMethodPathMap[val] = true
		}
		// Add basic information.
		metadata.SetCreateInfo(userID)
		metadata.SetUpdateInfo(userID)
		if metadata.GetVersion() == "" {
			metadataVersion, generateErr := uuid.NewV7()
			if generateErr != nil {
				return nil, nil, nil, generateErr
			}
			metadata.SetVersion(metadataVersion.String())
		}
		tools = append(tools, &model.ToolDB{
			BoxID:       boxID,
			Name:        metadata.GetSummary(),
			Description: description,
			UseRule:     useRule,
			SourceID:    metadata.GetVersion(),
			SourceType:  model.SourceType(metadata.GetType()),
			Status:      string(interfaces.ToolStatusTypeDisabled),
			CreateUser:  userID,
			UpdateUser:  userID,
		})
	}
	return
}
