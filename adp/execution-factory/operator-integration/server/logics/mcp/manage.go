package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	icommon "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	infracommon "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common/ormhelper"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	oerrors "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/telemetry"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/auth"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/metric"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/utils"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
)

// Sorting field and database field mapping.
var sortFieldMap = map[string]string{
	"create_time": "f_create_time",
	"update_time": "f_update_time",
	"name":        "f_name",
}

const (
	mcpToolMaxCount = 30
)

// ParseSSE parses SSE MCPServer.
func (s *mcpServiceImpl) ParseSSE(ctx context.Context, req *interfaces.MCPParseSSERequest) (resp *interfaces.MCPParseSSEResponse, err error) {
	// record observable.
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	// The parsing action will drive the server to initiate an outbound request to the URL given by the caller. It is a prerequisite step for creating a new MCP Server.
	// Therefore, new permission decisions are created at the same type level as AddMCPServer; internal aspects are not affected.
	if icommon.IsPublicAPIFromCtx(ctx) {
		var accessor *interfaces.AuthAccessor
		accessor, err = s.AuthService.GetAccessor(ctx, "")
		if err != nil {
			return
		}
		if err = s.AuthService.CheckCreatePermission(ctx, accessor, interfaces.AuthResourceTypeMCP); err != nil {
			return
		}
	}
	mcpCoreInfo := interfaces.MCPCoreConfigInfo{
		Mode:    req.Mode,
		URL:     req.URL,
		Headers: req.Headers,
	}

	listToolsReq := ListToolsRequest{
		MCPCoreInfo: &mcpCoreInfo,
	}

	toolsResponse, err := s.listTools(ctx, &listToolsReq)
	if err != nil {
		s.logger.WithContext(ctx).Errorf("list tools error: %v", err)
		return
	}
	resp = &interfaces.MCPParseSSEResponse{
		Tools:          toolsResponse.Tools,
		ServerInitInfo: toolsResponse.ServerInitInfo,
	}
	return
}

// AddMCPServer Add MCP Server.
func (s *mcpServiceImpl) AddMCPServer(ctx context.Context, req *interfaces.MCPServerAddRequest) (resp *interfaces.MCPServerAddResponse, err error) {
	// record observable.
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	telemetry.SetSpanAttributes(ctx, map[string]interface{}{
		"user_id": req.UserID,
	})
	// Check if there is new permission.
	accessor, err := s.AuthService.GetAccessor(ctx, req.UserID)
	if err != nil {
		return
	}
	err = s.AuthService.CheckCreatePermission(ctx, accessor, interfaces.AuthResourceTypeMCP)
	if err != nil {
		return
	}

	tx, err := s.DBTx.GetTx(ctx)
	if err != nil {
		s.logger.WithContext(ctx).Errorf("get tx failed, err: %v", err)
		err = oerrors.DefaultHTTPError(ctx, http.StatusInternalServerError, fmt.Sprintf("get tx failed, err: %v", err))
		return
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		} else {
			_ = tx.Commit()
		}
	}()

	// It is not built-in by default. Built-in tools call built-in interfaces.
	req.IsInternal = false
	mcpserverConfig, err := s.registerReqToModel(req)
	if err != nil {
		return nil, err
	}

	MCPID, err := s.addMCPConfig(ctx, tx, mcpserverConfig)
	if err != nil {
		return
	}

	// Add mcp tool configuration information.
	if req.CreationType == interfaces.MCPCreationTypeToolImported {
		var mcpTools []*model.MCPToolDB
		mcpTools, err = s.syncMCPTools(ctx, tx, req.UserID, MCPID, mcpserverConfig.Version, req.ToolConfigs)
		if err != nil {
			return nil, err
		}

		// Create mcp Server instance.
		err = s.createMCPServerInstance(ctx, mcpserverConfig, mcpTools)
		if err != nil {
			return nil, err
		}
	}

	// Triggering a new policy, the creator has all operating permissions on the current resources by default.
	err = s.AuthService.CreateOwnerPolicy(ctx, accessor, &interfaces.AuthResource{
		ID:   MCPID,
		Type: string(interfaces.AuthResourceTypeMCP),
		Name: mcpserverConfig.Name,
	})
	if err != nil {
		return
	}
	// Record audit log.
	go func() {
		accountAuthContext, ok := icommon.GetAccountAuthContextFromCtx(ctx)
		if !ok {
			s.logger.WithContext(ctx).Errorf("get account auth context from ctx error")
			return
		}
		s.AuditLog.Logger(ctx, &metric.AuditLogBuilderParams{
			TokenInfo: accountAuthContext.TokenInfo,
			Accessor:  accessor,
			Operation: metric.AuditLogOperationCreate,
			Object: &metric.AuditLogObject{
				Type: metric.AuditLogObjectMCP,
				Name: mcpserverConfig.Name,
				ID:   MCPID,
			},
		})
	}()

	resp = &interfaces.MCPServerAddResponse{
		MCPID:  MCPID,
		Status: string(interfaces.BizStatusUnpublish),
	}
	return
}

func (s *mcpServiceImpl) addMCPConfig(ctx context.Context, tx *sql.Tx, mcpConfig *model.MCPServerConfigDB) (string, error) {
	// Parameter verification.
	err := s.Validator.ValidatorMCPName(ctx, mcpConfig.Name)
	if err != nil {
		return "", err
	}
	err = s.Validator.ValidatorMCPDesc(ctx, mcpConfig.Description)
	if err != nil {
		return "", err
	}

	// Check classification.
	if !s.CategoryManager.CheckCategory(interfaces.BizCategory(mcpConfig.Category)) {
		return "", oerrors.DefaultHTTPError(ctx, http.StatusBadRequest, "invalid category")
	}

	// Verify based on name, name cannot be repeated.
	err = s.checkDuplicateName(ctx, mcpConfig.Name, "")
	if err != nil {
		return "", err
	}

	MCPID, err := s.DBMCPServerConfig.Insert(ctx, tx, mcpConfig)
	if err != nil {
		s.logger.WithContext(ctx).Errorf("addMCPConfig Insert failed, err: %v", err)
		err = oerrors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return "", err
	}

	return MCPID, nil
}

// syncMCPTools adds MCP tool configuration information.
func (s *mcpServiceImpl) syncMCPTools(ctx context.Context, tx *sql.Tx, userID, mcpID string, mcpVersion int, toolConfigs []*interfaces.MCPToolConfigInfo) (mcpTools []*model.MCPToolDB, err error) {
	// todo: The number of verification tools cannot exceed 30.
	if len(toolConfigs) > mcpToolMaxCount {
		return nil, oerrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.ErrExtMCPToolMaxCount, fmt.Sprintf("mcp tool count must be less than %d", mcpToolMaxCount), mcpToolMaxCount)
	}

	toolNames := make(map[string]bool)
	mcpTools = make([]*model.MCPToolDB, len(toolConfigs))
	for i, toolConfig := range toolConfigs {
		if toolConfig.ToolName != "" {
			// Check whether the tool name is duplicated.
			if toolNames[toolConfig.ToolName] {
				return nil, oerrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.ErrExtMCPToolNameDuplicate, fmt.Sprintf("mcp tool name %s is duplicate", toolConfig.ToolName), toolConfig.ToolName)
			}
			toolNames[toolConfig.ToolName] = true
			// Verify whether the tool name is legal.
			err = s.Validator.ValidatorToolName(ctx, toolConfig.ToolName)
			if err != nil {
				return nil, err
			}
		}
		// Verify whether the tool description is legal.
		if toolConfig.ToolDescription != "" {
			err = s.Validator.ValidatorToolDesc(ctx, toolConfig.ToolDescription)
			if err != nil {
				return nil, err
			}
		}
		mcpTools[i] = &model.MCPToolDB{
			MCPID:       mcpID,
			MCPVersion:  mcpVersion,
			BoxID:       toolConfig.BoxID,
			BoxName:     toolConfig.BoxName,
			ToolID:      toolConfig.ToolID,
			Name:        toolConfig.ToolName,
			Description: toolConfig.ToolDescription,
			UseRule:     toolConfig.UseRule,
			CreateUser:  userID,
			UpdateUser:  userID,
		}
	}

	// First delete data based on mcpID and mcpVersion.
	err = s.DBMCPTool.DeleteByMCPIDAndVersion(ctx, tx, mcpID, mcpVersion)
	if err != nil {
		s.logger.WithContext(ctx).Errorf("syncMCPTools DeleteByMCPIDAndVersion failed, err: %v", err)
		err = oerrors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return nil, err
	}

	if len(mcpTools) > 0 {
		// Batch data insertion.
		err = s.DBMCPTool.BatchInsert(ctx, tx, mcpTools)
		if err != nil {
			s.logger.WithContext(ctx).Errorf("syncMCPTools BatchInsert failed, err: %v", err)
			err = oerrors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
			return nil, err
		}
	}
	return mcpTools, nil
}

// DeleteMCPServer Delete MCP Server.
func (s *mcpServiceImpl) DeleteMCPServer(ctx context.Context, req *interfaces.MCPServerDeleteRequest) (err error) {
	// record observable.
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	telemetry.SetSpanAttributes(ctx, map[string]interface{}{
		"mcp_id":  req.MCPID,
		"user_id": req.UserID,
	})
	// Check delete permissions.
	accessor, err := s.AuthService.GetAccessor(ctx, req.UserID)
	if err != nil {
		return
	}
	err = s.AuthService.CheckDeletePermission(ctx, accessor, req.MCPID, interfaces.AuthResourceTypeMCP)
	if err != nil {
		return
	}

	tx, err := s.DBTx.GetTx(ctx)
	if err != nil {
		s.logger.WithContext(ctx).Errorf("get tx failed, err: %v", err)
		err = oerrors.DefaultHTTPError(ctx, http.StatusInternalServerError, fmt.Sprintf("get tx failed, err: %v", err))
		return
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		} else {
			_ = tx.Commit()
		}
	}()

	// Delete MCP Server configuration.
	configDB, err := s.removeMCPConfig(ctx, tx, req.MCPID)
	if err != nil {
		return
	}

	// Delete MCP tool configuration information.
	if configDB.CreationType == interfaces.MCPCreationTypeToolImported.String() {
		err = s.removeMCPTools(ctx, tx, req.MCPID, configDB.Version)
		if err != nil {
			return err
		}

		// Delete mcp Server instance.
		err = s.MCPInstanceService.DeleteAllMCPInstances(ctx, req.MCPID)
		if err != nil {
			return err
		}
	}

	// Trigger permission policy deletion.
	err = s.AuthService.DeletePolicy(ctx, []string{req.MCPID}, interfaces.AuthResourceTypeMCP)
	if err != nil {
		return
	}
	// Record audit log.
	go func() {
		accountAuthContext, ok := icommon.GetAccountAuthContextFromCtx(ctx)
		if !ok {
			s.logger.WithContext(ctx).Errorf("get account auth context from ctx error")
			return
		}
		s.AuditLog.Logger(ctx, &metric.AuditLogBuilderParams{
			TokenInfo: accountAuthContext.TokenInfo,
			Accessor:  accessor,
			Operation: metric.AuditLogOperationDelete,
			Object: &metric.AuditLogObject{
				Type: metric.AuditLogObjectMCP,
				ID:   req.MCPID,
				Name: configDB.Name,
			},
		})
	}()
	return nil
}

func (s *mcpServiceImpl) removeMCPConfig(ctx context.Context, tx *sql.Tx, mcpID string) (config *model.MCPServerConfigDB, err error) {
	// Check if MCP Server configuration exists.
	config, err = s.DBMCPServerConfig.SelectByID(ctx, tx, mcpID)
	if err != nil {
		s.logger.WithContext(ctx).Errorf("removeMCPConfig SelectByID failed, err: %v", err)
		err = oerrors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	if config == nil {
		err = oerrors.NewHTTPError(ctx, http.StatusNotFound, oerrors.ErrExtMCPNotFound, "mcp not found")
		return
	}
	if config.Status != string(interfaces.BizStatusUnpublish) && config.Status != string(interfaces.BizStatusOffline) {
		err = oerrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.ErrExtMCPUnSupportDelete,
			fmt.Sprintf("current mcp status %s, can not be deleted", config.Status))
		return
	}
	// Delete MCP Server configuration.
	err = s.DBMCPServerConfig.DeleteByID(ctx, tx, mcpID)
	if err != nil {
		s.logger.WithContext(ctx).Errorf("delete mcp config failed, err: %v", err)
		err = oerrors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	// Delete MCP Server release history.
	err = s.DBMCPServerReleaseHistory.DeleteByMCPID(ctx, tx, mcpID)
	if err != nil {
		s.logger.WithContext(ctx).Errorf("delete mcp release history failed, err: %v", err)
		err = oerrors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
	}
	return
}

func (s *mcpServiceImpl) removeMCPTools(ctx context.Context, tx *sql.Tx, mcpID string, mcpVersion int) (err error) {
	err = s.DBMCPTool.DeleteByMCPIDAndVersion(ctx, tx, mcpID, mcpVersion)
	if err != nil {
		s.logger.WithContext(ctx).Errorf("removeMCPTools DeleteByMCPIDAndVersion failed, err: %v", err)
		err = oerrors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
	}
	return
}

// QueryPage Query MCP Server list by page.
func (s *mcpServiceImpl) QueryPage(ctx context.Context, req *interfaces.MCPServerListRequest) (result *interfaces.MCPServerListResponse, err error) {
	// record observable.
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	filter := make(map[string]interface{})
	filter["all"] = req.All
	if req.Name != "" {
		filter["name"] = req.Name
	}
	if req.Source != "" {
		filter["source"] = req.Source
	}
	if req.Category != "" {
		filter["category"] = req.Category
	}
	if req.Status != "" {
		filter["status"] = req.Status
	}
	if req.CreateUser != "" {
		filter["create_user"] = req.CreateUser
	}
	if req.Mode != "" {
		filter["mode"] = req.Mode
	}

	// sort field.
	sortField := "f_update_time"
	if req.SortBy != "" {
		sortField = sortFieldMap[req.SortBy]
		if sortField == "" {
			err = fmt.Errorf("invalid sort field: %s", req.SortBy)
			return
		}
	}
	// Query MCP Server configuration list.
	sort := &ormhelper.SortParams{
		Fields: []ormhelper.SortField{
			{Field: sortField, Order: ormhelper.SortOrder(req.SortOrder)},
		},
	}
	queryTotalFunc := func(newCtx context.Context) (int64, error) {
		var total int64
		total, err = s.DBMCPServerConfig.CountByWhereClause(newCtx, nil, filter)
		if err != nil {
			return 0, err
		}
		return total, nil
	}
	queryBatchFunc := func(newCtx context.Context, pageSize, offset int, cursorValue *model.MCPServerConfigDB) (
		[]*model.MCPServerConfigDB, error) {
		var configList []*model.MCPServerConfigDB
		var cursor *ormhelper.CursorParams
		if cursorValue != nil {
			cursor = &ormhelper.CursorParams{
				Field:     sortField,
				Direction: ormhelper.SortOrder(req.SortOrder),
			}
			switch sortField {
			case "f_update_time":
				cursor.Value = cursorValue.UpdateTime
			case "f_create_time":
				cursor.Value = cursorValue.CreateTime
			case "f_name":
				cursor.Value = cursorValue.Name
			}
			// If using a cursor, offset is not required.
			offset = 0
		}
		filter["limit"] = pageSize
		filter["offset"] = offset
		configList, err = s.DBMCPServerConfig.SelectListPage(newCtx, nil, filter, sort, cursor)
		if err != nil {
			return nil, err
		}
		return configList, nil
	}

	queryBuilder := auth.NewQueryBuilder[model.MCPServerConfigDB]().
		SetPage(req.Page, req.PageSize).SetAll(req.All).
		SetQueryFunctions(queryTotalFunc, queryBatchFunc).
		SetFilteredQueryFunctions(func(newCtx context.Context, ids []string) (int64, error) {
			filter["in"] = ids
			return queryTotalFunc(newCtx)
		}, func(newCtx context.Context, pageSize, offset int, ids []string, cursorValue *model.MCPServerConfigDB) ([]*model.MCPServerConfigDB, error) {
			filter["in"] = ids
			return queryBatchFunc(newCtx, pageSize, offset, cursorValue)
		}).
		SetAuthFilter(func(newCtx context.Context) ([]string, error) {
			var accessor *interfaces.AuthAccessor
			accessor, err = s.AuthService.GetAccessor(newCtx, req.UserID)
			if err != nil {
				return nil, err
			}
			return s.AuthService.ResourceListIDs(newCtx, accessor, interfaces.AuthResourceTypeMCP, interfaces.AuthOperationTypeView)
		})
	resp, err := queryBuilder.Execute(ctx)
	if err != nil {
		return
	}
	configList := resp.Data
	userIDs := []string{}
	data := make([]*interfaces.MCPServerConfigInfo, 0, len(configList))
	for _, config := range configList {
		userIDs = append(userIDs, config.CreateUser, config.UpdateUser)
		data = append(data, s.modelToResponse(config))
	}

	// Get tool configuration information.
	toolConfigMap, err := s.getMCPToolConfigs(ctx, configList)
	if err != nil {
		return nil, err
	}

	// Render user name.
	userMap, err := s.UserMgnt.GetUsersName(ctx, userIDs)
	if err != nil {
		return
	}
	for _, config := range data {
		config.CreateUser = utils.GetValueOrDefault(userMap, config.CreateUser, interfaces.UnknownUser)
		config.UpdateUser = utils.GetValueOrDefault(userMap, config.UpdateUser, interfaces.UnknownUser)
		config.ToolConfigs = toolConfigMap[s.genToolConfigMapKey(config.MCPID, config.Version)]
	}

	queryResult := &ormhelper.QueryResult{
		Total:      int64(resp.TotalCount),
		Page:       req.Page,
		PageSize:   req.PageSize,
		TotalPages: resp.TotalPage,
		HasNext:    resp.HasNext,
		HasPrev:    resp.HasPrev,
	}
	result = &interfaces.MCPServerListResponse{
		QueryResult: queryResult,
		Data:        data,
	}
	return
}

// GetDetail Gets MCP Server details.
func (s *mcpServiceImpl) GetDetail(ctx context.Context, req *interfaces.MCPServerDetailRequest) (resp *interfaces.MCPServerDetailResponse, err error) {
	// record observable.
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	// Check viewing permissions.
	accessor, err := s.AuthService.GetAccessor(ctx, req.UserID)
	if err != nil {
		return
	}
	err = s.AuthService.CheckViewPermission(ctx, accessor, req.ID, interfaces.AuthResourceTypeMCP)
	if err != nil {
		return
	}

	mcpConfigDB, err := s.DBMCPServerConfig.SelectByID(ctx, nil, req.ID)
	if err != nil {
		s.logger.WithContext(ctx).Errorf("select mcp config by id failed, err: %v", err)
		err = oerrors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}

	if mcpConfigDB == nil {
		err = oerrors.NewHTTPError(ctx, http.StatusNotFound, oerrors.ErrExtMCPNotFound, "mcp not found")
		return
	}

	mcpConfig := s.modelToResponse(mcpConfigDB)

	// Render user name.
	userIDs := []string{mcpConfigDB.CreateUser, mcpConfigDB.UpdateUser}
	userMap, err := s.UserMgnt.GetUsersName(ctx, userIDs)
	if err != nil {
		return
	}
	mcpConfig.CreateUser = utils.GetValueOrDefault(userMap, mcpConfigDB.CreateUser, interfaces.UnknownUser)
	mcpConfig.UpdateUser = utils.GetValueOrDefault(userMap, mcpConfigDB.UpdateUser, interfaces.UnknownUser)

	// Assemble response results.
	response := &interfaces.MCPServerDetailResponse{
		BaseInfo: mcpConfig,
	}

	// When the current status is the publishing status, MCP Server connection information is generated.
	if mcpConfigDB.Status == string(interfaces.BizStatusPublished) {
		response.ConnectionInfo = s.generateExternalConnectionInfo(mcpConfigDB.MCPID, mcpConfig.CreationType)
	}

	// Assemble MCP tool configuration information.
	if mcpConfig.CreationType == interfaces.MCPCreationTypeToolImported {
		toolConfigs, err := s.getMCPToolConfig(ctx, mcpConfig.MCPID, mcpConfig.Version)
		if err != nil {
			return nil, err
		}
		response.BaseInfo.ToolConfigs = toolConfigs
	}
	return response, nil
}

// UpdateMCPServer Update MCP Server.
func (s *mcpServiceImpl) UpdateMCPServer(ctx context.Context, req *interfaces.MCPServerUpdateRequest) (resp *interfaces.MCPServerUpdateResponse, err error) {
	// record observable.
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	telemetry.SetSpanAttributes(ctx, map[string]interface{}{
		"mcp_id":  req.MCPID,
		"user_id": req.UserID,
	})
	// Check editing permissions.
	accessor, err := s.AuthService.GetAccessor(ctx, req.UserID)
	if err != nil {
		return
	}
	err = s.AuthService.CheckModifyPermission(ctx, accessor, req.MCPID, interfaces.AuthResourceTypeMCP)
	if err != nil {
		return
	}

	tx, err := s.DBTx.GetTx(ctx)
	if err != nil {
		s.logger.WithContext(ctx).Errorf("get db tx failed, err: %v", err)
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

	// Custom MCP Server updates.
	newMCPConfig := s.mcpUpdateReqToModel(req)
	config, oldVersion, currentVersion, err := s.updateMCPConfig(ctx, tx, newMCPConfig)
	if err != nil {
		return
	}

	// Synchronize MCP tool configuration information.
	if config.CreationType == interfaces.MCPCreationTypeToolImported.String() {
		mcpTools, err := s.syncMCPTools(ctx, tx, req.UserID, req.MCPID, config.Version, req.ToolConfigs)
		if err != nil {
			return nil, err
		}

		// Update mcp Server instance.
		err = s.refreshMCPServerInstance(ctx, oldVersion, currentVersion, config, mcpTools)
		if err != nil {
			return nil, err
		}
	}

	// Record audit log.
	go func() {
		accountAuthContext, ok := infracommon.GetAccountAuthContextFromCtx(ctx)
		if !ok {
			s.logger.WithContext(ctx).Errorf("get account auth context from ctx failed")
			return
		}
		s.AuditLog.Logger(ctx, &metric.AuditLogBuilderParams{
			TokenInfo: accountAuthContext.TokenInfo,
			Accessor:  accessor,
			Operation: metric.AuditLogOperationEdit,
			Object: &metric.AuditLogObject{
				Type: metric.AuditLogObjectMCP,
				ID:   config.MCPID,
				Name: config.Name,
			},
		})
	}()
	resp = &interfaces.MCPServerUpdateResponse{
		MCPID:  config.MCPID,
		Status: interfaces.BizStatus(config.Status),
	}
	return
}

// updateMCPConfig updates MCP Server configuration.
func (s *mcpServiceImpl) updateMCPConfig(ctx context.Context, tx *sql.Tx, newMCPConfig *model.MCPServerConfigDB) (config *model.MCPServerConfigDB, oldVersion, currentVersion int, err error) {
	// Parameter verification.
	err = s.Validator.ValidatorMCPName(ctx, newMCPConfig.Name)
	if err != nil {
		return nil, 0, 0, err
	}
	err = s.Validator.ValidatorMCPDesc(ctx, newMCPConfig.Description)
	if err != nil {
		return nil, 0, 0, err
	}
	// Check classification.
	if !s.CategoryManager.CheckCategory(interfaces.BizCategory(newMCPConfig.Category)) {
		return nil, 0, 0, oerrors.DefaultHTTPError(ctx, http.StatusBadRequest, "invalid category")
	}

	// Get MCP Server configuration based on ID.
	config, err = s.DBMCPServerConfig.SelectByID(ctx, nil, newMCPConfig.MCPID)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("select mcp server config failed: %w", err)
	}

	if config == nil {
		// Configuration does not exist.
		return nil, 0, 0, oerrors.NewHTTPError(ctx, http.StatusNotFound, oerrors.ErrExtMCPNotFound, "mcp not found")
	}

	// Added new fields, compatible with older versions.
	if config.CreationType == "" {
		config.CreationType = interfaces.MCPCreationTypeCustom.String()
	}

	// Verify whether the state transition is legal.
	targetState, err := common.GetEditStatusTrans(ctx, interfaces.BizStatus(config.Status))
	if err != nil {
		return nil, 0, 0, err
	}

	// Has the name changed?.
	isNameChange := config.Name != newMCPConfig.Name
	if isNameChange {
		// When the name changes, verify whether the name is duplicated.
		err = s.checkDuplicateName(ctx, newMCPConfig.Name, config.MCPID)
		if err != nil {
			return nil, 0, 0, err
		}
	}

	// Update version number.
	oldVersion = config.Version
	currentVersion, err = s.updateMCPConfigVersion(ctx, tx, config)
	if err != nil {
		return nil, 0, 0, err
	}

	// Update the MCP Server configuration to define which fields can be updated.
	config.Name = newMCPConfig.Name
	config.Description = newMCPConfig.Description
	config.Source = newMCPConfig.Source
	config.Category = newMCPConfig.Category
	config.UpdateUser = newMCPConfig.UpdateUser
	config.UpdateTime = time.Now().UnixNano()

	config.Mode = newMCPConfig.Mode
	config.Command = newMCPConfig.Command
	config.Args = newMCPConfig.Args
	config.URL = newMCPConfig.URL
	config.Headers = newMCPConfig.Headers
	config.Env = newMCPConfig.Env
	config.Status = string(targetState)

	if config.CreationType == interfaces.MCPCreationTypeToolImported.String() {
		config.URL = s.generateInternalMCPURL(config.MCPID, config.Version, interfaces.MCPMode(config.Mode))
	}

	// status update.
	err = s.DBMCPServerConfig.UpdateByID(ctx, tx, config)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("update mcp server config failed: %w", err)
	}

	// If the name changes, trigger permission resource change notification.
	if isNameChange {
		authResource := &interfaces.AuthResource{
			ID:   config.MCPID,
			Name: config.Name,
			Type: string(interfaces.AuthResourceTypeMCP),
		}
		err = s.AuthService.NotifyResourceChange(ctx, authResource)
		if err != nil {
			return nil, 0, 0, err
		}
	}
	return config, oldVersion, currentVersion, nil
}

func (s *mcpServiceImpl) UpdateMCPStatus(ctx context.Context, req *interfaces.UpdateMCPStatusRequest) (resp *interfaces.UpdateMCPStatusResponse, err error) {
	// record observable.
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	telemetry.SetSpanAttributes(ctx, map[string]interface{}{
		"mcp_id":  req.MCPID,
		"user_id": req.UserID,
	})
	// Check publishing or removal permissions.
	accessor, err := s.AuthService.GetAccessor(ctx, req.UserID)
	if err != nil {
		return
	}
	var operation metric.AuditLogOperationType
	if req.Status == interfaces.BizStatusPublished {
		operation = metric.AuditLogOperationPublish
		err = s.AuthService.CheckPublishPermission(ctx, accessor, req.MCPID, interfaces.AuthResourceTypeMCP)
	} else if req.Status == interfaces.BizStatusOffline {
		operation = metric.AuditLogOperationUnpublish
		err = s.AuthService.CheckUnpublishPermission(ctx, accessor, req.MCPID, interfaces.AuthResourceTypeMCP)
	}
	if err != nil {
		return
	}

	var tx *sql.Tx
	tx, err = s.DBTx.GetTx(ctx)
	if err != nil {
		s.logger.WithContext(ctx).Errorf("get db tx failed, err: %v", err)
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
	mcpConfigDB, resp, err := s.modifyMCPStatus(ctx, tx, req)
	if err != nil {
		return
	}
	if operation == "" {
		return
	}
	// Record audit log.
	go func() {
		accountAuthContext, ok := infracommon.GetAccountAuthContextFromCtx(ctx)
		if !ok {
			s.logger.WithContext(ctx).Errorf("get account auth context from ctx error")
			return
		}
		s.AuditLog.Logger(ctx, &metric.AuditLogBuilderParams{
			TokenInfo: accountAuthContext.TokenInfo,
			Accessor:  accessor,
			Operation: operation,
			Object: &metric.AuditLogObject{
				Type: metric.AuditLogObjectMCP,
				ID:   req.MCPID,
				Name: mcpConfigDB.Name,
			},
		})
	}()
	return
}

func (s *mcpServiceImpl) modifyMCPStatus(ctx context.Context, tx *sql.Tx, req *interfaces.UpdateMCPStatusRequest) (mcpConfigDB *model.MCPServerConfigDB,
	resp *interfaces.UpdateMCPStatusResponse, err error) {
	// Check whether MCP configuration information exists.
	mcpConfigDB, err = s.DBMCPServerConfig.SelectByID(ctx, tx, req.MCPID)
	if err != nil {
		s.logger.WithContext(ctx).Errorf("select mcp server config failed, err: %v", err)
		err = oerrors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	if mcpConfigDB == nil {
		err = oerrors.NewHTTPError(ctx, http.StatusNotFound, oerrors.ErrExtMCPNotFound, "mcp not found")
		return
	}

	// Verify whether the state transition is legal.
	if !common.CheckStatusTransition(interfaces.BizStatus(mcpConfigDB.Status), req.Status) {
		err = oerrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.ErrExtMCPStatusInvalid,
			fmt.Sprintf("current mcp status %s, can not be transition to %s", mcpConfigDB.Status, req.Status))
		return
	}

	mcpConfigDB.UpdateUser = req.UserID
	mcpConfigDB.UpdateTime = time.Now().UnixNano()

	switch req.Status {
	case interfaces.BizStatusPublished:
		// Check if there is a duplicate name.
		err = s.checkDuplicateName(ctx, mcpConfigDB.Name, mcpConfigDB.MCPID)
		if err != nil {
			return
		}
		mcpConfigDB.Status = string(req.Status)
		// Release MCP.
		var mcpReleaseDB *model.MCPServerReleaseDB
		mcpReleaseDB, err = s.publishMCP(ctx, tx, mcpConfigDB, req.UserID)
		if err != nil {
			return
		}
		// Add new release history.
		err = s.addMCPHistory(ctx, tx, mcpReleaseDB, req.UserID)
		if err != nil {
			return
		}
	case interfaces.BizStatusOffline:
		// Removal operation.
		err = s.unpublishMCP(ctx, tx, mcpConfigDB)
		if err != nil {
			return
		}
	case interfaces.BizStatusUnpublish, interfaces.BizStatusEditing:
		// In editing or unpublished status, update version number.
		_, err = s.updateMCPConfigVersion(ctx, tx, mcpConfigDB)
		if err != nil {
			return
		}
	}

	// Update MCP configuration table status.
	err = s.DBMCPServerConfig.UpdateStatus(ctx, tx, req.MCPID, string(req.Status), req.UserID, mcpConfigDB.Version)
	if err != nil {
		s.logger.WithContext(ctx).Errorf("update mcp server config status failed, err: %v", err)
		err = oerrors.DefaultHTTPError(ctx, http.StatusInternalServerError, fmt.Sprintf("update mcp server config status failed, err: %v", err))
		return
	}

	resp = &interfaces.UpdateMCPStatusResponse{
		MCPID:  req.MCPID,
		Status: req.Status,
	}
	return
}

// DebugTool debugging tool.
func (s *mcpServiceImpl) DebugTool(ctx context.Context, req *interfaces.MCPToolDebugRequest) (resp *interfaces.MCPToolDebugResponse, err error) {
	// record observable.
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	telemetry.SetSpanAttributes(ctx, map[string]interface{}{
		"mcp_id":  req.MCPID,
		"user_id": req.UserID,
	})
	// Verify usage rights.
	accessor, err := s.AuthService.GetAccessor(ctx, req.UserID)
	if err != nil {
		return
	}
	err = s.AuthService.CheckExecutePermission(ctx, accessor, req.MCPID, interfaces.AuthResourceTypeMCP)
	if err != nil {
		return
	}

	// 1. Obtain MCP Server configuration.
	mcpConfigDB, err := s.DBMCPServerConfig.SelectByID(ctx, nil, req.MCPID)
	if err != nil {
		s.logger.WithContext(ctx).Errorf("select mcp server config failed, err: %v", err)
		err = oerrors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}

	if mcpConfigDB == nil {
		err = oerrors.NewHTTPError(ctx, http.StatusNotFound, oerrors.ErrExtMCPNotFound, "mcp not found")
		return
	}

	mcpConfig := s.modelToResponse(mcpConfigDB)

	// 2. Call the tool.
	callToolReq := &CallToolRequest{
		ListToolsRequest: &ListToolsRequest{
			CreationType: mcpConfig.CreationType,
			MCPID:        mcpConfig.MCPID,
			Version:      mcpConfig.Version,
			MCPCoreInfo: &interfaces.MCPCoreConfigInfo{
				Mode:    mcpConfig.Mode,
				URL:     mcpConfig.URL,
				Headers: mcpConfig.Headers,
			},
		},
		ToolName: req.ToolName,
		Params:   req.Parameters,
	}
	callToolResp, err := s.callTool(ctx, callToolReq)
	if err != nil {
		return
	}

	// Record audit log.
	go func() {
		accountAuthContext, ok := infracommon.GetAccountAuthContextFromCtx(ctx)
		if !ok {
			s.logger.WithContext(ctx).Errorf("get account auth context from ctx error")
			return
		}

		s.AuditLog.Logger(ctx, &metric.AuditLogBuilderParams{
			TokenInfo: accountAuthContext.TokenInfo,
			Accessor:  accessor,
			Operation: metric.AuditLogOperationExecute,
			Object: &metric.AuditLogObject{
				Type: metric.AuditLogObjectMCP,
				ID:   req.MCPID,
				Name: mcpConfig.Name,
			},
		})
	}()
	resp = &interfaces.MCPToolDebugResponse{
		Content: callToolResp.Content,
		IsError: callToolResp.IsError,
	}
	return
}

// registerReqToModel converts registration request to model.
func (s *mcpServiceImpl) registerReqToModel(req *interfaces.MCPServerAddRequest) (config *model.MCPServerConfigDB, err error) {
	id, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	config = &model.MCPServerConfigDB{
		MCPID:        id.String(),
		Version:      1,
		Name:         req.Name,
		Description:  req.Description,
		CreationType: req.CreationType.String(),
		Mode:         string(req.Mode),
		URL:          req.URL,
		Headers:      utils.ObjectToJSON(req.Headers),
		Command:      req.Command,
		Env:          utils.ObjectToJSON(req.Env),
		Args:         utils.ObjectToJSON(req.Args),
		Status:       string(interfaces.BizStatusUnpublish),
		Category:     req.Category,
		Source:       req.Source,
		IsInternal:   req.IsInternal,
		CreateUser:   req.UserID,
		UpdateUser:   req.UserID,
	}

	if req.CreationType == interfaces.MCPCreationTypeToolImported {
		config.Mode = interfaces.MCPModeStream.String()
		config.URL = s.generateInternalMCPURL(config.MCPID, config.Version, interfaces.MCPMode(config.Mode))
	}
	return config, nil
}

// mcpUpdateReqToModel Convert update request to model.
func (s *mcpServiceImpl) mcpUpdateReqToModel(req *interfaces.MCPServerUpdateRequest) *model.MCPServerConfigDB {
	return &model.MCPServerConfigDB{
		MCPID:        req.MCPID,
		CreationType: req.CreationType.String(),
		Name:         req.Name,
		Description:  req.Description,
		Source:       req.Source,
		IsInternal:   false,
		Category:     req.Category,
		UpdateUser:   req.UserID,
		UpdateTime:   time.Now().UnixNano(),
		Mode:         string(req.Mode),
		Command:      req.Command,
		Args:         utils.ObjectToJSON(req.Args),
		URL:          req.URL,
		Headers:      utils.ObjectToJSON(req.Headers),
		Env:          utils.ObjectToJSON(req.Env),
	}
}

// modelToResponse Convert model to response.
func (s *mcpServiceImpl) modelToResponse(config *model.MCPServerConfigDB) *interfaces.MCPServerConfigInfo {
	return &interfaces.MCPServerConfigInfo{
		MCPCoreConfigInfo: interfaces.MCPCoreConfigInfo{
			Mode:    interfaces.MCPMode(config.Mode),
			Command: config.Command,
			Args:    utils.JSONToObject[[]string](config.Args),
			URL:     config.URL,
			Headers: utils.JSONToObject[map[string]string](config.Headers),
			Env:     utils.JSONToObject[map[string]string](config.Env),
		},
		MCPID:        config.MCPID,
		Version:      config.Version,
		CreationType: interfaces.MCPCreationType(config.CreationType),
		Name:         config.Name,
		Description:  config.Description,
		Status:       config.Status,
		Source:       config.Source,
		IsInternal:   config.IsInternal,
		Category:     config.Category,
		CreateUser:   config.CreateUser,
		CreateTime:   config.CreateTime,
		UpdateUser:   config.UpdateUser,
		UpdateTime:   config.UpdateTime,
	}
}

// checkDuplicateName checks whether there is a duplicate name.
func (s *mcpServiceImpl) checkDuplicateName(ctx context.Context, name, mcpID string) (err error) {
	// Verify based on name, name cannot be repeated.
	configDB, err := s.DBMCPServerConfig.SelectByName(ctx, nil, name, []string{interfaces.BizStatusPublished.String()})
	if err != nil {
		s.logger.WithContext(ctx).Errorf("checkDuplicateName count by name failed, name: %s, err: %v", name, err)
		err = oerrors.DefaultHTTPError(ctx, http.StatusInternalServerError, fmt.Sprintf("check duplicate name, err: %s", err.Error()))
		return
	}
	if configDB == nil || (mcpID != "" && configDB.MCPID == mcpID) {
		return
	}
	err = oerrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.ErrExtMCPExists,
		fmt.Sprintf("mcp server name %s already exists, please use another name", name),
		name)
	return
}

// updateMCPConfigVersion updates the MCP configuration table version number.
func (s *mcpServiceImpl) updateMCPConfigVersion(ctx context.Context, tx *sql.Tx, mcpConfigDB *model.MCPServerConfigDB) (version int, err error) {
	if mcpConfigDB.Status == string(interfaces.BizStatusPublished) || mcpConfigDB.Status == string(interfaces.BizStatusOffline) {
		// For backward compatibility, the version number is taken +1 from the release history.
		releaseHistorys, err := s.DBMCPServerReleaseHistory.SelectByMCPID(ctx, tx, mcpConfigDB.MCPID)
		if err != nil {
			return 0, err
		}
		if len(releaseHistorys) > 0 {
			mcpConfigDB.Version = releaseHistorys[0].Version + 1
		}
	}
	version = mcpConfigDB.Version
	return version, nil
}

// createMCPServerInstance creates an MCP Server instance.
func (s *mcpServiceImpl) createMCPServerInstance(ctx context.Context, mcpConfigDB *model.MCPServerConfigDB, tools []*model.MCPToolDB) (err error) {
	// Create mcp Server instance.
	req := &interfaces.MCPInstanceCreateRequest{
		MCPID:        mcpConfigDB.MCPID,
		Version:      mcpConfigDB.Version,
		Name:         mcpConfigDB.Name,
		Instructions: mcpConfigDB.Description,
	}
	toolConfigs, err := s.getMCPToolDeployConfigs(ctx, tools)
	if err != nil {
		s.logger.WithContext(ctx).Warnf("createMCPServerInstance getMCPToolDeployConfigs failed, err: %v", err)
		return err
	}
	req.ToolConfigs = toolConfigs
	_, err = s.MCPInstanceService.CreateMCPInstance(ctx, req)
	if err != nil {
		return err
	}
	// todo: choose to update mcp url.
	return nil
}

func (s *mcpServiceImpl) UpgradeMCPInstance(ctx context.Context, mcpID string) (err error) {
	// Get MCP configuration information.
	mcpConfigDB, err := s.DBMCPServerConfig.SelectByID(ctx, nil, mcpID)
	if err != nil {
		return err
	}
	if mcpConfigDB == nil {
		return oerrors.DefaultHTTPError(ctx, http.StatusNotFound, fmt.Sprintf("mcp server %s not found", mcpID))
	}
	// Get tool information.
	tools, err := s.DBMCPTool.SelectListByMCPIDAndVersion(ctx, nil, mcpID, mcpConfigDB.Version)
	if err != nil {
		return err
	}
	toolConfigs, err := s.getMCPToolDeployConfigs(ctx, tools)
	if err != nil {
		s.logger.WithContext(ctx).Warnf("createMCPServerInstance getMCPToolDeployConfigs failed, err: %v", err)
		return err
	}
	req := &interfaces.MCPInstanceCreateRequest{
		MCPID:        mcpConfigDB.MCPID,
		Version:      mcpConfigDB.Version,
		Name:         mcpConfigDB.Name,
		Instructions: mcpConfigDB.Description,
		ToolConfigs:  toolConfigs,
	}
	_, err = s.MCPInstanceService.UpgradeMCPInstance(ctx, req)
	if err != nil {
		return err
	}
	return
}

// refreshMCPServerInstance refresh MCP Server instance.
func (s *mcpServiceImpl) refreshMCPServerInstance(ctx context.Context, oldVersion, currentVersion int, mcpConfigDB *model.MCPServerConfigDB, tools []*model.MCPToolDB) (err error) {
	if currentVersion > oldVersion {
		// Create a new mcp Server instance.
		return s.createMCPServerInstance(ctx, mcpConfigDB, tools)
	}
	// Update mcp Server instance.
	return s.updateMCPServerInstance(ctx, mcpConfigDB, tools)
}

func (s *mcpServiceImpl) updateMCPServerInstance(ctx context.Context, mcpConfigDB *model.MCPServerConfigDB, tools []*model.MCPToolDB) (err error) {
	// Update mcp Server instance.
	req := &interfaces.MCPInstanceUpdateRequest{
		MCPServerName: mcpConfigDB.Name,
		Instructions:  mcpConfigDB.Description,
	}
	toolConfigs, err := s.getMCPToolDeployConfigs(ctx, tools)
	if err != nil {
		return err
	}
	req.ToolConfigs = toolConfigs
	_, err = s.MCPInstanceService.UpdateMCPInstance(ctx, mcpConfigDB.MCPID, mcpConfigDB.Version, req)
	if err != nil {
		return err
	}
	return nil
}

// getMCPToolDeployConfigs Gets MCP tool deployment configuration.
func (s *mcpServiceImpl) getMCPToolDeployConfigs(ctx context.Context, tools []*model.MCPToolDB) ([]*interfaces.MCPToolConfig, error) {
	toolConfigs := make([]*interfaces.MCPToolConfig, len(tools))
	for i, tool := range tools {
		mcpToolConfig, err := s.generateMCPToolConfig(ctx, tool)
		if err != nil {
			return nil, err
		}
		toolConfigs[i] = mcpToolConfig
	}
	return toolConfigs, nil
}

func (s *mcpServiceImpl) generateMCPToolConfig(ctx context.Context, tool *model.MCPToolDB) (*interfaces.MCPToolConfig, error) {
	toolConfig := &interfaces.MCPToolConfig{
		ToolID:      tool.MCPToolID,
		Description: tool.Description,
	}
	// Get tool information under the toolbox.
	toolInfo, err := s.ToolService.GetBoxTool(ctx, &interfaces.GetToolReq{
		BoxID:  tool.BoxID,
		ToolID: tool.ToolID,
	})
	if err != nil {
		return nil, err
	}

	if tool.Name != "" {
		toolConfig.Name = tool.Name
	} else {
		toolConfig.Name = toolInfo.Name
	}

	if tool.Description != "" {
		toolConfig.Description = tool.Description
	} else {
		toolConfig.Description = toolInfo.Description
	}
	if tool.UseRule != "" {
		toolConfig.Description += "\n use rule:" + tool.UseRule
	}

	// Convert metadata information to json schema.
	toolConfig.InputSchema, err = s.convertInputSchema(ctx, toolInfo)
	if err != nil {
		return nil, err
	}
	return toolConfig, nil
}

func (s *mcpServiceImpl) convertInputSchema(ctx context.Context, toolInfo *interfaces.ToolInfo) (json.RawMessage, error) {
	if toolInfo.MetadataType != interfaces.MetadataTypeAPI && toolInfo.MetadataType != interfaces.MetadataTypeFunc {
		s.logger.WithContext(ctx).Warnf("unsupported metadata type: %s", toolInfo.MetadataType)
		err := errors.DefaultHTTPError(ctx, http.StatusBadRequest, fmt.Sprintf("unsupported metadata type: %s", toolInfo.MetadataType))
		return nil, err
	}
	if toolInfo.Metadata == nil {
		s.logger.WithContext(ctx).Warnf("tool metadata is nil")
		return nil, fmt.Errorf("tool metadata is nil")
	}
	metadata := toolInfo.Metadata
	if metadata.APISpec == nil {
		s.logger.WithContext(ctx).Warnf("tool apispec is nil")
		return nil, fmt.Errorf("tool apispec is nil")
	}
	converter := NewSimpleConverter()
	result := converter.ConvertFromBytes(utils.ObjectToByte(metadata.APISpec))
	if !result.Success {
		s.logger.WithContext(ctx).Warnf("convert metadata failed: %s", result.Error)
		return nil, fmt.Errorf("convert metadata failed: %s", result.Error)
	}
	return json.RawMessage(utils.ObjectToJSON(result.Data)), nil
}
