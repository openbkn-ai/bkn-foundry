package toolbox

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"time"

	"github.com/creasty/defaults"
	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	icommon "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/metadata"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/metric"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/utils"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
)

// Import Import.
func (s *ToolServiceImpl) Import(ctx context.Context, tx *sql.Tx, mode interfaces.ImportType, data *interfaces.ComponentImpexConfigModel, userID string) (err error) {
	// record observable.
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	if data == nil || data.Toolbox == nil || len(data.Toolbox.Configs) == 0 {
		err = errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtCommonImportDataEmpty, "toolbox configs is empty")
		return
	}
	// Import pre-check.
	waitUpdataBoxList, err := s.importPreCheck(ctx, mode, data.Toolbox.Configs)
	if err != nil {
		return
	}
	var accessor *interfaces.AuthAccessor
	if icommon.IsPublicAPIFromCtx(ctx) {
		accessor, err = s.AuthService.GetAccessor(ctx, userID)
		if err != nil {
			s.Logger.WithContext(ctx).Warnf("[Import] GetAccessor err:%v", err)
			return
		}
	}
	// Import toolbox and tool information.
	createMap, updateMap, err := s.batchImportToolBoxMetadata(ctx, tx, data.Toolbox.Configs, waitUpdataBoxList, accessor, userID)
	if err != nil {
		s.Logger.WithContext(ctx).Warnf("[Import] batchImportToolBoxMetadata err:%v", err)
		return
	}
	// Import dependencies.
	if data.Operator != nil && len(data.Operator.Configs) > 0 {
		err = s.OperatorMgnt.Import(ctx, tx, mode, data.Operator, userID)
		if err != nil {
			s.Logger.WithContext(ctx).Warnf("[Import] OperatorMgnt.Import err:%v", err)
			return
		}
	}
	// Import post-processing.
	err = s.importPostProcess(ctx, createMap, updateMap, accessor)
	if err != nil {
		s.Logger.WithContext(ctx).Warnf("[Import] importPostProcess err:%v", err)
	}
	return
}

// Post-operation: Add permission configuration and audit logging.
func (s *ToolServiceImpl) importPostProcess(ctx context.Context, createBoxMap, updateBoxMap map[string]*model.ToolboxDB, accessor *interfaces.AuthAccessor) (err error) {
	for _, boxDB := range createBoxMap {
		// Triggering a new policy, the creator has all operating permissions on the current resources by default (internal calls will not create)
		if accessor != nil {
			err := s.AuthService.CreateOwnerPolicy(ctx, accessor, &interfaces.AuthResource{
				ID:   boxDB.BoxID,
				Type: interfaces.AuthResourceTypeToolBox.String(),
				Name: boxDB.Name,
			})
			if err != nil {
				s.Logger.WithContext(ctx).Errorf("[importPostProcess] CreateOwnerPolicy err:%v", err)
			}
		}
		// Record design logs and subsequent notifications (internal calls are not recorded)
		if accessor != nil {
			go func() {
				accountAuthContext, ok := common.GetAccountAuthContextFromCtx(ctx)
				if !ok {
					s.Logger.WithContext(ctx).Warnf("[importPostProcess] GetAccountAuthContextFromCtx err :%v", err)
					return
				}
				s.AuditLog.Logger(ctx, &metric.AuditLogBuilderParams{
					TokenInfo: accountAuthContext.TokenInfo,
					Accessor:  accessor,
					Operation: metric.AuditLogOperationCreate,
					Object: &metric.AuditLogObject{
						Type: metric.AuditLogObjectTool,
						ID:   boxDB.BoxID,
						Name: boxDB.Name,
					},
				})
			}()
		}
		// Built-in component: Create a full authorization policy (public_access + execute)
		if boxDB.IsInternal {
			err = s.AuthService.CreateIntCompPolicyForAllUsers(ctx, &interfaces.AuthResource{
				ID:   boxDB.BoxID,
				Type: interfaces.AuthResourceTypeToolBox.String(),
				Name: boxDB.Name,
			})
			if err != nil {
				s.Logger.WithContext(ctx).Errorf("[importPostProcess] CreateIntCompPolicyForAllUsers err:%v", err)
				return
			}
		}
	}
	// Update toolbox.
	for _, boxDB := range updateBoxMap {
		// Notify resource changes.
		authResource := &interfaces.AuthResource{
			ID:   boxDB.BoxID,
			Name: boxDB.Name,
			Type: interfaces.AuthResourceTypeToolBox.String(),
		}
		err := s.AuthService.NotifyResourceChange(ctx, authResource)
		if err != nil {
			s.Logger.WithContext(ctx).Errorf("[importPostProcess] NotifyResourceChange err:%v", err)
		}
		// Built-in component: Create a full authorization policy (public_access + execute)
		if boxDB.IsInternal {
			policyErr := s.AuthService.CreateIntCompPolicyForAllUsers(ctx, &interfaces.AuthResource{
				ID:   boxDB.BoxID,
				Type: interfaces.AuthResourceTypeToolBox.String(),
				Name: boxDB.Name,
			})
			if policyErr != nil {
				s.Logger.WithContext(ctx).Errorf("[importPostProcess] CreateIntCompPolicyForAllUsers err:%v", policyErr)
			}
		}
		// Record design logs and subsequent notifications (internal calls are not recorded)
		if accessor != nil {
			go func() {
				accountAuthContext, ok := common.GetAccountAuthContextFromCtx(ctx)
				if !ok {
					s.Logger.WithContext(ctx).Warnf("[importPostProcess] GetAccountAuthContextFromCtx err :%v", err)
					return
				}
				s.AuditLog.Logger(ctx, &metric.AuditLogBuilderParams{
					TokenInfo: accountAuthContext.TokenInfo,
					Accessor:  accessor,
					Operation: metric.AuditLogOperationEdit,
					Object: &metric.AuditLogObject{
						Type: metric.AuditLogObjectTool,
						ID:   boxDB.BoxID,
						Name: boxDB.Name,
					},
				})
			}()
		}
	}
	return nil
}

// Import preliminary checks.
func (s *ToolServiceImpl) importPreCheck(ctx context.Context, mode interfaces.ImportType, items []*interfaces.ToolBoxImpexItem) (boxList []*model.ToolboxDB, err error) {
	// Collect toolbox ID, and name.
	boxIDs := []string{}
	for _, item := range items {
		boxIDs = append(boxIDs, item.BoxID)
		if icommon.IsPublicAPIFromCtx(ctx) && item.IsInternal {
			err = errors.NewHTTPError(ctx, http.StatusForbidden, errors.ErrExtCommonInternalComponentNotAllowed,
				fmt.Sprintf("internal toolbox %v not allowed to import", item.BoxID), item.BoxName)
			return
		}
		// Toolbox duplicate name verification.
		err = s.checkBoxDuplicateName(ctx, item.BoxName, item.BoxID)
		if err != nil {
			return
		}
	}
	// Check if ID resources conflict.
	boxIDs = utils.UniqueStrings(boxIDs)
	boxList, err = s.ToolBoxDB.SelectListByBoxIDs(ctx, boxIDs)
	if err != nil {
		s.Logger.WithContext(ctx).Errorf("select toolbox by ids failed, err: %v", err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	// Create mode: Return conflict error if toolbox already exists.
	if mode == interfaces.ImportTypeCreate && len(boxList) > 0 {
		err = errors.NewHTTPError(ctx, http.StatusConflict, errors.ErrExtCommonResourceIDConflict, "toolbox id already exists")
	}
	return
}

// Batch import of toolboxes and tool metadata.
func (s *ToolServiceImpl) batchImportToolBoxMetadata(ctx context.Context, tx *sql.Tx, items []*interfaces.ToolBoxImpexItem, waitUpdataBoxList []*model.ToolboxDB,
	accessor *interfaces.AuthAccessor, userID string) (createBoxMap, updateBoxMap map[string]*model.ToolboxDB, err error) {
	// Collect the ToolBoxes that need to be added.
	createBoxMap = map[string]*model.ToolboxDB{}
	// Collect tools ToolBox that need to be updated.
	updateBoxMap = map[string]*model.ToolboxDB{}
	// Get user ID (internal call uses incoming userID)
	uid := userID
	if accessor != nil {
		uid = accessor.ID
	}
	// Check for update permissions and collect toolboxes that need to be updated.
	for _, boxDB := range waitUpdataBoxList {
		// Check toolbox editing permissions (internal calls are not authenticated)
		if icommon.IsPublicAPIFromCtx(ctx) {
			err = s.AuthService.CheckModifyPermission(ctx, accessor, boxDB.BoxID, interfaces.AuthResourceTypeToolBox)
			if err != nil {
				return
			}
			// Built-in toolbox cannot be edited.
			if boxDB.IsInternal {
				err = errors.NewHTTPError(ctx, http.StatusForbidden, errors.ErrExtCommonInternalComponentNotAllowed,
					fmt.Sprintf("internal toolbox %v not allowed to update", boxDB.BoxID), boxDB.Name)
				return
			}
		}
		updateBoxMap[boxDB.BoxID] = boxDB
	}
	// Traverse the imported items and determine whether they are new or updated based on whether the toolbox ID exists.
	for _, item := range items {
		if boxDB, ok := updateBoxMap[item.BoxID]; ok {
			err = s.importByUpsert(ctx, tx, boxDB, item, uid)
			if err != nil {
				return
			}
		} else {
			boxDB, err = s.importByCreate(ctx, tx, item, uid)
			if err != nil {
				return
			}
			createBoxMap[boxDB.BoxID] = boxDB
		}
	}
	return
}

// importByCreate import toolbox.
func (s *ToolServiceImpl) importByCreate(ctx context.Context, tx *sql.Tx, item *interfaces.ToolBoxImpexItem, userID string) (boxDB *model.ToolboxDB, err error) {
	// Verify imported toolbox information.
	toolDBs, metadataDBs, err := s.importCheck(ctx, item, userID)
	if err != nil {
		return
	}
	// Add toolbox.
	boxDB = &model.ToolboxDB{
		BoxID:        item.BoxID,
		Name:         item.BoxName,
		Description:  item.BoxDesc,
		Source:       item.Source,
		ServerURL:    item.BoxSvcURL,
		Category:     item.CategoryType,
		Status:       item.Status.String(),
		IsInternal:   item.IsInternal,
		CreateTime:   time.Now().UnixNano(),
		CreateUser:   userID,
		UpdateUser:   userID,
		UpdateTime:   time.Now().UnixNano(),
		MetadataType: string(item.MetadataType),
	}
	if item.Status == interfaces.BizStatusPublished {
		boxDB.ReleaseUser = userID
		boxDB.ReleaseTime = time.Now().UnixNano()
	}
	_, err = s.ToolBoxDB.InsertToolBox(ctx, tx, boxDB)
	if err != nil {
		s.Logger.WithContext(ctx).Errorf("insert toolbox failed, err: %v", err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	// Process metadata.
	metadataMap := map[string]interfaces.IMetadataDB{}
	for _, metadataDB := range metadataDBs {
		version := metadataDB.GetVersion()
		metadataVersion, generateErr := uuid.NewV7()
		if generateErr != nil {
			return nil, generateErr
		}
		metadataDB.SetVersion(metadataVersion.String())
		metadataMap[version] = metadataDB
	}
	newMetadataDBs := []interfaces.IMetadataDB{}
	toolIDs := []string{}
	for _, toolDB := range toolDBs {
		if metadataDB, ok := metadataMap[toolDB.SourceID]; ok {
			toolDB.SourceID = metadataDB.GetVersion()
			newMetadataDBs = append(newMetadataDBs, metadataDB)
		}
		toolIDs = append(toolIDs, toolDB.ToolID)
	}
	// Check if tools are duplicated.
	duplicateTools, err := s.ToolDB.SelectToolBoxByToolIDs(ctx, toolIDs)
	if err != nil {
		s.Logger.WithContext(ctx).Errorf("select tool by source ids failed, err: %v", err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	if len(duplicateTools) > 0 {
		err = errors.NewHTTPError(ctx, http.StatusConflict, errors.ErrExtCommonResourceIDConflict, fmt.Sprintf("tool resource conflict, tool ids: %v", toolIDs))
		return
	}
	// Add metadata.
	if len(newMetadataDBs) > 0 {
		_, err = s.MetadataService.BatchRegisterMetadata(ctx, tx, newMetadataDBs)
		if err != nil {
			s.Logger.WithContext(ctx).Errorf("insert metadata failed, err: %v", err)
			err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
			return
		}
	}
	// Add tool.
	if len(toolDBs) > 0 {
		_, err = s.ToolDB.InsertTools(ctx, tx, toolDBs)
		if err != nil {
			s.Logger.WithContext(ctx).Errorf("insert tool failed, err: %v", err)
			err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
			return
		}
	}
	return
}

// importByUpsert update or create.
func (s *ToolServiceImpl) importByUpsert(ctx context.Context, tx *sql.Tx, toolBoxDB *model.ToolboxDB, item *interfaces.ToolBoxImpexItem, userID string) (err error) {
	// Verify imported toolbox information.
	toolDBs, metadataDBs, err := s.importCheck(ctx, item, userID)
	if err != nil {
		return
	}
	// Check toolbox metadata for consistency.
	if toolBoxDB.MetadataType != string(item.MetadataType) {
		err = errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtCommonMetadataTypeConflict,
			fmt.Sprintf("toolbox %s metadata type conflict, expect %v, got %v", toolBoxDB.BoxID, toolBoxDB.MetadataType, item.MetadataType))
		return
	}
	toolBoxDB.Name = item.BoxName
	toolBoxDB.Description = item.BoxDesc
	toolBoxDB.ServerURL = item.BoxSvcURL
	toolBoxDB.Category = item.CategoryType
	toolBoxDB.IsInternal = item.IsInternal
	toolBoxDB.UpdateTime = time.Now().UnixNano()
	toolBoxDB.UpdateUser = userID
	toolBoxDB.Status = item.Status.String()
	if item.Status == interfaces.BizStatusPublished {
		toolBoxDB.ReleaseUser = userID
		toolBoxDB.ReleaseTime = time.Now().UnixNano()
	}
	err = s.ToolBoxDB.UpdateToolBox(ctx, tx, toolBoxDB)
	if err != nil {
		s.Logger.WithContext(ctx).Errorf("update toolbox failed, err: %v", err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	// Get the tools in the toolbox.
	tools, err := s.ToolDB.SelectToolByBoxID(ctx, toolBoxDB.BoxID)
	if err != nil {
		s.Logger.WithContext(ctx).Errorf("select tools failed, err: %v", err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	// Delete a tool from the toolbox.
	err = s.deleteTools(ctx, tx, toolBoxDB.BoxID, tools)
	if err != nil {
		return
	}
	// Add metadata.
	if len(metadataDBs) > 0 {
		_, err = s.MetadataService.BatchRegisterMetadata(ctx, tx, metadataDBs)
		if err != nil {
			s.Logger.WithContext(ctx).Errorf("insert metadata failed, err: %v", err)
			err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
			return
		}
	}
	// Add tool.
	if len(toolDBs) > 0 {
		_, err = s.ToolDB.InsertTools(ctx, tx, toolDBs)
		if err != nil {
			s.Logger.WithContext(ctx).Errorf("insert tool failed, err: %v", err)
			err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
			return
		}
	}
	return
}

// importCheck verifies imported toolbox information.
func (s *ToolServiceImpl) importCheck(ctx context.Context, item *interfaces.ToolBoxImpexItem, userID string) (toolDBs []*model.ToolDB,
	metadataList []interfaces.IMetadataDB, err error) {
	// Inject default value and verify.
	err = defaults.Set(item)
	if err != nil {
		s.Logger.WithContext(ctx).Errorf("set default value failed, err: %v", err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	err = s.Validator.ValidatorStruct(ctx, item)
	if err != nil {
		return
	}
	// Verification toolbox information.
	err = s.Validator.ValidatorToolBoxName(ctx, item.BoxName)
	if err != nil {
		return
	}
	// check desc.
	err = s.Validator.ValidatorToolBoxDesc(ctx, item.BoxDesc)
	if err != nil {
		return
	}
	// Check if the category exists.
	if !s.CategoryManager.CheckCategory(interfaces.BizCategory(item.CategoryType)) {
		// Set as default category.
		item.CategoryType = interfaces.CategoryTypeOther.String()
	}
	// Check if it is built-in.
	toolDBs = []*model.ToolDB{}
	toolNames := make(map[string]bool)
	for _, toolImpexItem := range item.Tools {
		if _, ok := toolNames[toolImpexItem.Name]; ok {
			err = errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtToolNameDuplicate,
				fmt.Sprintf("tool name %v duplicate", toolImpexItem.Name), toolImpexItem.Name)
			return
		}
		// Verification tool information.
		err = s.Validator.ValidatorToolName(ctx, toolImpexItem.Name)
		if err != nil {
			return
		}
		if toolImpexItem.Description == "" {
			toolImpexItem.Description = toolImpexItem.Name
		}
		err = s.Validator.ValidatorToolDesc(ctx, toolImpexItem.Description)
		if err != nil {
			return
		}
		toolNames[toolImpexItem.Name] = true
		toolDBs = append(toolDBs, &model.ToolDB{
			ToolID:      toolImpexItem.ToolID,
			BoxID:       item.BoxID,
			Name:        toolImpexItem.Name,
			Description: toolImpexItem.Description,
			SourceID:    toolImpexItem.SourceID,
			SourceType:  toolImpexItem.SourceType,
			Status:      toolImpexItem.Status.String(),
			UseRule:     toolImpexItem.UseRule,
			Parameters:  utils.ObjectToJSON(toolImpexItem.GlobalParameters),
			CreateUser:  userID,
			CreateTime:  time.Now().UnixNano(),
			UpdateUser:  userID,
			UpdateTime:  time.Now().UnixNano(),
			ExtendInfo:  utils.ObjectToJSON(toolImpexItem.ExtendInfo),
		})
		switch toolImpexItem.SourceType {
		case model.SourceTypeOpenAPI:
			if toolImpexItem.Metadata == nil {
				err = errors.DefaultHTTPError(ctx, http.StatusBadRequest, "tool metadata is nil")
				return
			}
			err = s.Validator.ValidatorStruct(ctx, toolImpexItem.Metadata)
			if err != nil {
				return
			}
			if toolImpexItem.MetadataType != "" && toolImpexItem.MetadataType != item.MetadataType {
				err = errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtToolTypeMismatch,
					fmt.Sprintf("tool type %v mismatch", toolImpexItem.MetadataType))
				return
			}
			metadataDB := &model.APIMetadataDB{
				Version:     toolImpexItem.Metadata.Version,
				CreateUser:  userID,
				CreateTime:  time.Now().UnixNano(),
				UpdateUser:  userID,
				UpdateTime:  time.Now().UnixNano(),
				Summary:     toolImpexItem.Metadata.Summary,
				Description: toolImpexItem.Metadata.Description,
				Path:        toolImpexItem.Metadata.Path,
				ServerURL:   toolImpexItem.Metadata.ServerURL,
				Method:      toolImpexItem.Metadata.Method,
				APISpec:     utils.ObjectToJSON(toolImpexItem.Metadata.APISpec),
			}
			metadataList = append(metadataList, metadataDB)
		case model.SourceTypeFunction:
			if toolImpexItem.Metadata == nil {
				err = errors.DefaultHTTPError(ctx, http.StatusBadRequest, "tool metadata is nil")
				return
			}
			err = s.Validator.ValidatorStruct(ctx, toolImpexItem.Metadata)
			if err != nil {
				return
			}
			if toolImpexItem.MetadataType != "" && toolImpexItem.MetadataType != item.MetadataType {
				err = errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtToolTypeMismatch,
					fmt.Sprintf("tool type %v mismatch", toolImpexItem.MetadataType))
				return
			}
			metadataDB := &model.FunctionMetadataDB{
				Version:      toolImpexItem.Metadata.Version,
				CreateUser:   userID,
				CreateTime:   time.Now().UnixNano(),
				UpdateUser:   userID,
				UpdateTime:   time.Now().UnixNano(),
				Summary:      toolImpexItem.Metadata.Summary,
				Description:  toolImpexItem.Metadata.Description,
				Path:         toolImpexItem.Metadata.Path,
				ServerURL:    toolImpexItem.Metadata.ServerURL,
				Method:       toolImpexItem.Metadata.Method,
				APISpec:      utils.ObjectToJSON(toolImpexItem.Metadata.APISpec),
				ScriptType:   string(toolImpexItem.ScriptType),
				Dependencies: utils.ObjectToJSON(toolImpexItem.Dependencies),
				Code:         toolImpexItem.Code,
			}
			metadataList = append(metadataList, metadataDB)
		case model.SourceTypeOperator:
		}
	}
	return
}

// Export pre-check.
func (s *ToolServiceImpl) exportPreCheck(ctx context.Context, req *interfaces.ExportReq) (boxDBs []*model.ToolboxDB, err error) {
	// Batch authentication.
	var accessor *interfaces.AuthAccessor
	accessor, err = s.AuthService.GetAccessor(ctx, req.UserID)
	if err != nil {
		return
	}
	// Check view permissions permissions.
	checkBoxIDs, err := s.AuthService.ResourceFilterIDs(ctx, accessor, req.IDs,
		interfaces.AuthResourceTypeToolBox, interfaces.AuthOperationTypeView)
	if err != nil {
		return
	}
	if len(checkBoxIDs) != len(req.IDs) {
		clist := utils.FindMissingElements(req.IDs, checkBoxIDs)
		err = errors.NewHTTPError(ctx, http.StatusForbidden, errors.ErrExtCommonOperationForbidden,
			fmt.Sprintf("toolbox %v not access", clist))
		return
	}
	// Check if the data exists.
	boxDBs, err = s.ToolBoxDB.SelectListByBoxIDs(ctx, req.IDs)
	if err != nil {
		s.Logger.WithContext(ctx).Errorf("select toolbox list err: %s", err.Error())
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	if len(boxDBs) != len(req.IDs) {
		checkBoxes := []string{}
		for _, v := range boxDBs {
			checkBoxes = append(checkBoxes, v.BoxID)
		}
		clist := utils.FindMissingElements(req.IDs, checkBoxes)
		err = errors.NewHTTPError(ctx, http.StatusNotFound, errors.ErrExtToolNotFound,
			fmt.Sprintf("toolbox %v not found", clist))
		return
	}
	return
}

// Export export.
func (s *ToolServiceImpl) Export(ctx context.Context, req *interfaces.ExportReq) (data *interfaces.ComponentImpexConfigModel, err error) {
	// record observable.
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)

	boxDBs, err := s.exportPreCheck(ctx, req)
	if err != nil {
		return
	}
	// Get tool information in the toolbox in batches.
	toolBoxConfig, depOperatorIDs, err := s.batchGetToolBoxInfo(ctx, boxDBs)
	if err != nil {
		return
	}
	data = &interfaces.ComponentImpexConfigModel{
		Toolbox: toolBoxConfig,
	}
	// Obtain operator dependency information in batches.
	depOperatorIDs = utils.UniqueStrings(depOperatorIDs)
	if len(depOperatorIDs) == 0 {
		return
	}
	operatorImpexConfig, err := s.OperatorMgnt.Export(ctx, &interfaces.ExportReq{
		UserID: req.UserID,
		IDs:    depOperatorIDs,
	})
	if err != nil {
		return
	}
	data.Operator = operatorImpexConfig.Operator
	return
}

// Get tool information in the toolbox in batches.
func (s *ToolServiceImpl) batchGetToolBoxInfo(ctx context.Context, boxDBs []*model.ToolboxDB) (toolBoxInfo *interfaces.ToolBoxImpexConfig,
	depOperatorIDs []string, err error) {
	toolsMap := map[string][]*interfaces.ToolImpexItem{} // Export information of tools under the toolbox.
	toolBoxInfo = &interfaces.ToolBoxImpexConfig{
		Configs: []*interfaces.ToolBoxImpexItem{},
	}
	// Assembly toolbox information.
	boxIDs := []string{}
	for _, boxDB := range boxDBs {
		if boxDB.IsInternal {
			err = errors.NewHTTPError(ctx, http.StatusForbidden, errors.ErrExtCommonInternalComponentNotAllowed,
				fmt.Sprintf("internal toolbox %v not allowed to export", boxDB.BoxID), boxDB.Name)
			return
		}
		toolBoxInfo.Configs = append(toolBoxInfo.Configs, &interfaces.ToolBoxImpexItem{
			BoxID:        boxDB.BoxID,
			BoxName:      boxDB.Name,
			BoxDesc:      boxDB.Description,
			BoxSvcURL:    boxDB.ServerURL,
			Status:       interfaces.BizStatus(boxDB.Status),
			CategoryType: boxDB.Category,
			CategoryName: s.CategoryManager.GetCategoryName(ctx, interfaces.BizCategory(boxDB.Category)),
			IsInternal:   boxDB.IsInternal,
			Source:       boxDB.Source,
			Tools:        []*interfaces.ToolImpexItem{},
			CreateTime:   boxDB.CreateTime,
			UpdateTime:   boxDB.UpdateTime,
			CreateUser:   boxDB.CreateUser,
			UpdateUser:   boxDB.UpdateUser,
			MetadataType: interfaces.MetadataType(boxDB.MetadataType),
		})
		// Collect toolbox IDs and initialize tool mapping.
		boxIDs = append(boxIDs, boxDB.BoxID)
		toolsMap[boxDB.BoxID] = []*interfaces.ToolImpexItem{}
	}
	// Get all the tools in your toolbox.
	tools, err := s.ToolDB.SelectToolBoxByIDs(ctx, boxIDs)
	if err != nil {
		s.Logger.WithContext(ctx).Errorf("select toolbox by ids:%v, err:%v", boxIDs, err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	// Assemble tool information and collect metadata information with queries.
	sourceMap := map[model.SourceType][]string{} // Metadata ID mapping.
	for _, toolDB := range tools {
		var toolInfo *interfaces.ToolInfo
		toolInfo, err = s.toolDBToToolInfo(ctx, toolDB)
		if err != nil {
			return
		}
		toolImpexItem := &interfaces.ToolImpexItem{
			ToolInfo:   *toolInfo,
			SourceID:   toolDB.SourceID,
			SourceType: toolDB.SourceType,
		}
		switch toolDB.SourceType {
		case model.SourceTypeOpenAPI:
			sourceMap[model.SourceTypeOpenAPI] = append(sourceMap[model.SourceTypeOpenAPI], toolDB.SourceID)
		case model.SourceTypeFunction:
			sourceMap[model.SourceTypeFunction] = append(sourceMap[model.SourceTypeFunction], toolDB.SourceID)
		case model.SourceTypeOperator:
			sourceMap[model.SourceTypeOperator] = append(sourceMap[model.SourceTypeOperator], toolDB.SourceID)
			depOperatorIDs = append(depOperatorIDs, toolDB.SourceID)
		}
		toolsMap[toolDB.BoxID] = append(toolsMap[toolDB.BoxID], toolImpexItem)
	}

	// Get metadata in batches.
	sourceIDToMetadataMap, err := s.MetadataService.BatchGetMetadataBySourceIDs(ctx, sourceMap)
	if err != nil {
		return
	}
	// Assembly tool metadata information.
	for _, toolBox := range toolBoxInfo.Configs {
		// Get the tools in the toolbox.
		for _, toolInfo := range toolsMap[toolBox.BoxID] {
			metadataDB, ok := sourceIDToMetadataMap[toolInfo.SourceID]
			if !ok {
				continue
			}
			toolInfo.MetadataType = interfaces.MetadataType(metadataDB.GetType())
			if toolInfo.SourceType != model.SourceTypeOperator {
				// The operator tool does not directly export metadata, but exports it through operator dependencies.
				toolInfo.Metadata = metadata.MetadataDBToStruct(metadataDB)
				dependencies := []interfaces.DependencyInfo{}
				if metadataDB.GetDependencies() != "" {
					dependencies = utils.JSONToObject[[]interfaces.DependencyInfo](metadataDB.GetDependencies())
				}
				toolInfo.FunctionContent = interfaces.FunctionContent{
					ScriptType:      interfaces.ScriptType(metadataDB.GetScriptType()),
					Code:            metadataDB.GetCode(),
					Dependencies:    dependencies,
					DependenciesURL: metadataDB.GetDependenciesURL(),
				}
			}
			toolBox.Tools = append(toolBox.Tools, toolInfo)
		}
	}
	return
}
