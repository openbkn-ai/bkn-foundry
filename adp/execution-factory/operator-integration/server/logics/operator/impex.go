package operator

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"time"

	"github.com/creasty/defaults"
	"github.com/google/uuid"
	icommon "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/metadata"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/metric"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/utils"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
)

// Export export operator.
func (m *operatorManager) Export(ctx context.Context, req *interfaces.ExportReq) (data *interfaces.ComponentImpexConfigModel, err error) {
	// record observable.
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	// Export pre-check.
	operatorList, err := m.exportPreCheck(ctx, req)
	if err != nil {
		return
	}
	data = &interfaces.ComponentImpexConfigModel{
		Operator: &interfaces.OperatorImpexConfig{},
	}
	// Export dependencies and append dependency operators.
	allOperatorDBs, compositeConfigs, err := m.getCompositeOperatorDependencies(ctx, operatorList, req.UserID)
	if err != nil {
		return
	}
	data.Operator.CompositeConfigs = compositeConfigs

	// Obtain operator metadata in batches.
	items, err := m.batchGetOperatorInfo(ctx, allOperatorDBs)
	if err != nil {
		return
	}
	data.Operator.Configs = items
	return
}

// Import import operator.
func (m *operatorManager) Import(ctx context.Context, tx *sql.Tx, mode interfaces.ImportType, data *interfaces.OperatorImpexConfig, userID string) (err error) {
	// record observable.
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	if data == nil || len(data.Configs) == 0 {
		err = errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtCommonImportDataEmpty, "operator configs is empty")
		return
	}
	// Import preliminary checks.
	operatorList, err := m.importPreCheck(ctx, mode, data.Configs)
	if err != nil {
		return
	}
	var accessor *interfaces.AuthAccessor
	if icommon.IsPublicAPIFromCtx(ctx) {
		accessor, err = m.AuthService.GetAccessor(ctx, userID)
		if err != nil {
			return
		}
	}
	// Import operator metadata.
	createMap, updateMap, err := m.batchImportOperatorMetadata(ctx, tx, data.Configs, operatorList, accessor, userID)
	if err != nil {
		return
	}

	err = m.importPostProcess(ctx, createMap, updateMap, accessor)
	return
}

// Post-operation: Add permission configuration and audit logging.
func (m *operatorManager) importPostProcess(ctx context.Context, createMap, updateMap map[string]*model.OperatorRegisterDB, accessor *interfaces.AuthAccessor) (err error) {
	for _, operatorDB := range createMap {
		// Triggering a new policy, the creator has all operating permissions on the current resources by default (internal calls will not create)
		if accessor != nil {
			err := m.AuthService.CreateOwnerPolicy(ctx, accessor, &interfaces.AuthResource{
				ID:   operatorDB.OperatorID,
				Type: interfaces.AuthResourceTypeOperator.String(),
				Name: operatorDB.Name,
			})
			if err != nil {
				m.Logger.WithContext(ctx).Warnf("[importPostProcess] CreateOwnerPolicy err :%v", err)
			}
		}
		// Record design logs and subsequent notifications (internal calls are not recorded)
		if accessor != nil {
			go func() {
				accountAuthContext, ok := icommon.GetAccountAuthContextFromCtx(ctx)
				if !ok {
					m.Logger.WithContext(ctx).Warnf("[importPostProcess] GetAccountAuthContextFromCtx err :%v", err)
					return
				}
				m.AuditLog.Logger(ctx, &metric.AuditLogBuilderParams{
					TokenInfo: accountAuthContext.TokenInfo,
					Accessor:  accessor,
					Operation: metric.AuditLogOperationCreate,
					Object: &metric.AuditLogObject{
						Type: metric.AuditLogObjectOperator,
						ID:   operatorDB.OperatorID,
						Name: operatorDB.Name,
					},
				})
			}()
		}
		// Built-in component: Create a full authorization policy (public_access + execute)
		if operatorDB.IsInternal {
			err = m.AuthService.CreateIntCompPolicyForAllUsers(ctx, &interfaces.AuthResource{
				ID:   operatorDB.OperatorID,
				Type: interfaces.AuthResourceTypeOperator.String(),
				Name: operatorDB.Name,
			})
			if err != nil {
				m.Logger.WithContext(ctx).Warnf("[importPostProcess] CreateIntCompPolicyForAllUsers err:%v", err)
				return
			}
		}
	}
	// update operator.
	for _, operatorDB := range updateMap {
		// Notify resource changes.
		authResource := &interfaces.AuthResource{
			ID:   operatorDB.OperatorID,
			Name: operatorDB.Name,
			Type: interfaces.AuthResourceTypeOperator.String(),
		}
		err := m.AuthService.NotifyResourceChange(ctx, authResource)
		if err != nil {
			m.Logger.WithContext(ctx).Warnf("[importPostProcess] NotifyResourceChange err :%v", err)
		}
		// Built-in component: Create a full authorization policy (public_access + execute)
		if operatorDB.IsInternal {
			policyErr := m.AuthService.CreateIntCompPolicyForAllUsers(ctx, &interfaces.AuthResource{
				ID:   operatorDB.OperatorID,
				Type: interfaces.AuthResourceTypeOperator.String(),
				Name: operatorDB.Name,
			})
			if policyErr != nil {
				m.Logger.WithContext(ctx).Warnf("[importPostProcess] CreateIntCompPolicyForAllUsers err:%v", policyErr)
			}
		}
		// Record design logs and subsequent notifications (internal calls are not recorded)
		if accessor != nil {
			go func() {
				accountAuthContext, ok := icommon.GetAccountAuthContextFromCtx(ctx)
				if !ok {
					m.Logger.WithContext(ctx).Warnf("[importPostProcess] GetAccountAuthContextFromCtx err :%v", err)
					return
				}
				m.AuditLog.Logger(ctx, &metric.AuditLogBuilderParams{
					TokenInfo: accountAuthContext.TokenInfo,
					Accessor:  accessor,
					Operation: metric.AuditLogOperationEdit,
					Object: &metric.AuditLogObject{
						Type: metric.AuditLogObjectOperator,
						ID:   operatorDB.OperatorID,
						Name: operatorDB.Name,
					},
				})
			}()
		}
	}
	return nil
}

// Import preliminary checks.
func (m *operatorManager) importPreCheck(ctx context.Context, mode interfaces.ImportType, items []*interfaces.OperatorImpexItem) (operatorList []*model.OperatorRegisterDB, err error) {
	// Get the operator ID list and name list to be imported.
	operatorIDs := make([]string, 0)
	for _, operatorItem := range items {
		operatorIDs = append(operatorIDs, operatorItem.OperatorID)
		// Built-in operators are not allowed to be imported.
		if icommon.IsPublicAPIFromCtx(ctx) && operatorItem.IsInternal {
			err = errors.NewHTTPError(ctx, http.StatusForbidden, errors.ErrExtCommonInternalComponentNotAllowed,
				fmt.Sprintf("internal operator %v not allowed to import", operatorItem.OperatorID), operatorItem.OperatorName)
			return
		}
		// Operator duplicate name verification.
		err = m.checkDuplicateName(ctx, operatorItem.OperatorName, operatorItem.OperatorID)
		if err != nil {
			return
		}
	}
	operatorIDs = utils.UniqueStrings(operatorIDs)
	// Check if ID resources conflict.
	operatorList, err = m.DBOperatorManager.SelectByOperatorIDs(ctx, operatorIDs)
	if err != nil {
		m.Logger.WithContext(ctx).Errorf("select operator list err: %v", err.Error())
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	// Creation mode: If the operator already exists, a conflict error is returned.
	if mode == interfaces.ImportTypeCreate && len(operatorList) > 0 {
		err = errors.NewHTTPError(ctx, http.StatusConflict, errors.ErrExtCommonResourceIDConflict, "operator id already exists")
	}
	return
}

// Import operator metadata in batches.
func (m *operatorManager) batchImportOperatorMetadata(ctx context.Context, tx *sql.Tx, items []*interfaces.OperatorImpexItem, needUpdateOperatorList []*model.OperatorRegisterDB,
	accessor *interfaces.AuthAccessor, userID string) (createMap, updateMap map[string]*model.OperatorRegisterDB, err error) {
	// List of operators that need to be added.
	createMap = map[string]*model.OperatorRegisterDB{}
	// List of operators that need to be updated.
	updateMap = map[string]*model.OperatorRegisterDB{}
	for _, operatorDB := range needUpdateOperatorList {
		// Check operator editing permissions (internal calls are not authenticated)
		if icommon.IsPublicAPIFromCtx(ctx) {
			err = m.AuthService.CheckModifyPermission(ctx, accessor, operatorDB.OperatorID, interfaces.AuthResourceTypeOperator)
			if err != nil {
				return
			}
			// Built-in operators are not allowed to be updated.
			if operatorDB.IsInternal {
				err = errors.NewHTTPError(ctx, http.StatusForbidden, errors.ErrExtCommonInternalComponentNotAllowed,
					fmt.Sprintf("internal operator %v not allowed to update", operatorDB.OperatorID), operatorDB.Name)
				return
			}
		}
		updateMap[operatorDB.OperatorID] = operatorDB
	}
	for _, operatorItem := range items {
		// Parameter preparation check.
		var newOperatorDB *model.OperatorRegisterDB
		var newMetadataDB interfaces.IMetadataDB
		uid := userID
		if accessor != nil {
			uid = accessor.ID
		}
		newOperatorDB, newMetadataDB, err = m.importCheck(ctx, operatorItem, uid)
		if err != nil {
			return
		}
		operatorDB, ok := updateMap[newOperatorDB.OperatorID]
		if ok { // update operator.
			err = m.updateOperatorConfig(ctx, tx, operatorDB, newOperatorDB, newMetadataDB)
			if err != nil {
				return
			}
			updateMap[operatorDB.OperatorID] = operatorDB
			if operatorDB.Status == interfaces.BizStatusPublished.String() {
				err = m.publishRelease(ctx, tx, operatorDB, operatorDB.UpdateUser)
			}
		} else { // New operator.
			err = m.addOperatorConfig(ctx, tx, newOperatorDB, newMetadataDB) // New operator.
			if err != nil {
				return
			}
			createMap[newOperatorDB.OperatorID] = newOperatorDB
			// Release operator.
			if newOperatorDB.Status == interfaces.BizStatusPublished.String() {
				err = m.publishRelease(ctx, tx, newOperatorDB, newOperatorDB.CreateUser)
			}
		}
		if err != nil {
			return
		}
	}
	return
}

// Add operator configuration.
func (m *operatorManager) addOperatorConfig(ctx context.Context, tx *sql.Tx, operatorDB *model.OperatorRegisterDB, metadataDB interfaces.IMetadataDB) (err error) {
	// Check whether the version metadata exists and report an error if there is a conflict.
	exists, _, err := m.MetadataService.CheckMetadataExists(ctx, interfaces.MetadataType(metadataDB.GetType()), metadataDB.GetVersion())
	if err != nil {
		m.Logger.WithContext(ctx).Errorf("check metadata version exists failed, err: %v", err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	if exists {
		err = errors.NewHTTPError(ctx, http.StatusConflict, errors.ErrExtCommonResourceIDConflict,
			fmt.Sprintf("metadata version %s already exists", metadataDB.GetVersion()))
		return
	}
	version, err := m.MetadataService.RegisterMetadata(ctx, tx, metadataDB)
	if err != nil {
		m.Logger.WithContext(ctx).Errorf("register metadata failed, err: %v", err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	operatorDB.MetadataVersion = version
	_, err = m.DBOperatorManager.InsertOperator(ctx, tx, operatorDB)
	if err != nil {
		m.Logger.WithContext(ctx).Errorf("insert operator failed, err: %v", err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	return
}

// Update (upgrade) operator configuration.
func (m *operatorManager) updateOperatorConfig(ctx context.Context, tx *sql.Tx, operatorDB,
	newOperatorDB *model.OperatorRegisterDB, newMetadataDB interfaces.IMetadataDB) (err error) {
	// Check whether metadata types are consistent.
	if operatorDB.MetadataType != newOperatorDB.MetadataType {
		err = errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtCommonMetadataTypeConflict,
			fmt.Sprintf("operator %s metadata type conflict, expect %v, got %v", operatorDB.OperatorID, operatorDB.MetadataType, newOperatorDB.MetadataType))
		return
	}
	operatorDB.Category = newOperatorDB.Category
	operatorDB.Name = newOperatorDB.Name
	operatorDB.OperatorType = newOperatorDB.OperatorType
	operatorDB.ExecutionMode = newOperatorDB.ExecutionMode
	operatorDB.Category = newOperatorDB.Category
	operatorDB.Source = newOperatorDB.Source
	operatorDB.ExecuteControl = newOperatorDB.ExecuteControl
	operatorDB.ExtendInfo = newOperatorDB.ExtendInfo
	operatorDB.IsInternal = newOperatorDB.IsInternal
	operatorDB.UpdateUser = newOperatorDB.CreateUser
	operatorDB.UpdateTime = time.Now().UnixNano()
	operatorDB.MetadataType = newOperatorDB.MetadataType
	switch interfaces.BizStatus(operatorDB.Status) {
	case interfaces.BizStatusPublished, interfaces.BizStatusOffline:
		newMetadataDB.SetVersion(uuid.New().String())
		operatorDB.MetadataVersion, err = m.MetadataService.RegisterMetadata(ctx, tx, newMetadataDB)
	case interfaces.BizStatusUnpublish, interfaces.BizStatusEditing:
		// Check if metadata exists.
		var metadataDB interfaces.IMetadataDB
		var has bool
		has, metadataDB, err = m.MetadataService.CheckMetadataExists(ctx, interfaces.MetadataType(newOperatorDB.MetadataType), operatorDB.MetadataVersion)
		if err != nil {
			m.Logger.WithContext(ctx).Errorf("select api metadata failed, err: %v", err)
			err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
			return
		}
		if has {
			metadataDB.SetSummary(newMetadataDB.GetSummary())
			metadataDB.SetDescription(newMetadataDB.GetDescription())
			metadataDB.SetScriptType(newMetadataDB.GetScriptType())
			metadataDB.SetServerURL(newMetadataDB.GetServerURL())
			metadataDB.SetPath(newMetadataDB.GetPath())
			metadataDB.SetMethod(newMetadataDB.GetMethod())
			metadataDB.SetCode(newMetadataDB.GetCode())
			metadataDB.SetScriptType(newMetadataDB.GetScriptType())
			metadataDB.SetDependencies(newMetadataDB.GetDependencies())
			metadataDB.SetDependenciesURL(newMetadataDB.GetDependenciesURL())
			metadataDB.SetUpdateInfo(newOperatorDB.CreateUser)
			err = m.MetadataService.UpdateMetadata(ctx, tx, metadataDB)
		} else {
			operatorDB.MetadataVersion, err = m.MetadataService.RegisterMetadata(ctx, tx, newMetadataDB)
		}
	}
	if err != nil {
		m.Logger.WithContext(ctx).Errorf("upsert api metadata failed, err: %v", err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	operatorDB.Status = newOperatorDB.Status
	err = m.DBOperatorManager.UpdateByOperatorID(ctx, tx, operatorDB)
	if err != nil {
		m.Logger.WithContext(ctx).Errorf("update operator failed, err: %v", err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	return
}

func (m *operatorManager) importCheck(ctx context.Context, item *interfaces.OperatorImpexItem, userID string) (operatorDB *model.OperatorRegisterDB,
	metadataDB interfaces.IMetadataDB, err error) {
	// Check operator information.
	err = m.Validator.ValidateOperatorName(ctx, item.OperatorName)
	if err != nil {
		return
	}
	if item.OperatorInfo == nil {
		item.OperatorInfo = &interfaces.OperatorInfo{}
		err = defaults.Set(item.OperatorInfo)
		if err != nil {
			err = errors.DefaultHTTPError(ctx, http.StatusBadRequest, err.Error())
			return
		}
	}
	if item.OperatorExecuteControl == nil {
		item.OperatorExecuteControl = &interfaces.OperatorExecuteControl{}
		err = defaults.Set(item.OperatorExecuteControl)
		if err != nil {
			err = errors.DefaultHTTPError(ctx, http.StatusBadRequest, err.Error())
			return
		}
	}
	// If it is a data source operator, it can only be a synchronization operator.
	isDataSource, err := checkIsDataSource(ctx, item.OperatorInfo.ExecutionMode, item.OperatorInfo.IsDataSource)
	if err != nil {
		return
	}
	// Check whether the category exists. If it does not exist, set it as the default category.
	if !m.CategoryManager.CheckCategory(item.OperatorInfo.Category) {
		item.OperatorInfo.Category = interfaces.CategoryTypeOther
	}
	// Check metadata.
	if item.Metadata == nil {
		err = errors.DefaultHTTPError(ctx, http.StatusBadRequest, fmt.Sprintf("operator %v metadata is nil", item.OperatorName))
		return
	}
	switch item.MetadataType {
	case interfaces.MetadataTypeAPI:
		metadata := &interfaces.MetadataInfo{
			APISpec: &interfaces.APISpec{},
		}
		err = utils.AnyToObject(item.Metadata, metadata)
		if err != nil {
			err = errors.DefaultHTTPError(ctx, http.StatusBadRequest, err.Error())
			return
		}
		err = m.Validator.ValidatorStruct(ctx, metadata)
		if err != nil {
			return
		}
		metadataDB = &model.APIMetadataDB{
			Version:     metadata.Version,
			CreateUser:  userID,
			CreateTime:  time.Now().UnixNano(),
			UpdateUser:  userID,
			UpdateTime:  time.Now().UnixNano(),
			Summary:     metadata.Summary,
			Description: metadata.Description,
			Path:        metadata.Path,
			ServerURL:   metadata.ServerURL,
			Method:      metadata.Method,
			APISpec:     utils.ObjectToJSON(metadata.APISpec),
		}
	case interfaces.MetadataTypeFunc:
		err = m.Validator.ValidatorStruct(ctx, item.Metadata)
		if err != nil {
			return
		}
		metadataDB = &model.FunctionMetadataDB{
			Version:         item.Metadata.Version,
			CreateUser:      userID,
			CreateTime:      time.Now().UnixNano(),
			UpdateUser:      userID,
			UpdateTime:      time.Now().UnixNano(),
			Summary:         item.Metadata.Summary,
			Description:     item.Metadata.Description,
			Path:            item.Metadata.Path,
			ServerURL:       item.Metadata.ServerURL,
			Method:          item.Metadata.Method,
			APISpec:         utils.ObjectToJSON(item.Metadata.APISpec),
			ScriptType:      string(item.Metadata.FunctionContent.ScriptType),
			Dependencies:    utils.ObjectToJSON(item.Metadata.FunctionContent.Dependencies),
			DependenciesURL: item.Metadata.FunctionContent.DependenciesURL,
			Code:            item.Metadata.FunctionContent.Code,
		}
	default:
		err = errors.DefaultHTTPError(ctx, http.StatusBadRequest, fmt.Sprintf("operator %v metadata type %v is not supported", item.OperatorName, item.MetadataType))
		return
	}
	// If the operator description is empty, the operator name is used by default.
	if metadataDB.GetDescription() == "" {
		metadataDB.SetDescription(metadataDB.GetSummary())
	}
	err = m.Validator.ValidateOperatorDesc(ctx, metadataDB.GetDescription())
	if err != nil {
		return
	}
	operatorDB = &model.OperatorRegisterDB{
		OperatorID:      item.OperatorID,
		Name:            item.OperatorName,
		MetadataType:    string(item.MetadataType),
		MetadataVersion: metadataDB.GetVersion(),
		Status:          item.Status.String(),
		OperatorType:    string(item.OperatorInfo.Type),
		ExecutionMode:   string(item.OperatorInfo.ExecutionMode),
		Category:        string(item.OperatorInfo.Category),
		Source:          item.OperatorInfo.Source,
		ExecuteControl:  utils.ObjectToJSON(item.OperatorExecuteControl),
		ExtendInfo:      utils.ObjectToJSON(item.ExtendInfo),
		CreateUser:      userID,
		CreateTime:      time.Now().UnixNano(),
		UpdateUser:      userID,
		UpdateTime:      time.Now().UnixNano(),
		IsDataSource:    isDataSource,
		IsInternal:      item.IsInternal,
	}
	metadataDB.SetCreateInfo(userID)
	metadataDB.SetUpdateInfo(userID)
	return
}

// Export pre-check.
func (m *operatorManager) exportPreCheck(ctx context.Context, req *interfaces.ExportReq) (operatorList []*model.OperatorRegisterDB, err error) {
	// Batch authentication.
	var accessor *interfaces.AuthAccessor
	accessor, err = m.AuthService.GetAccessor(ctx, req.UserID)
	if err != nil {
		return
	}
	// Check viewing permissions.
	checkOperatorIDs, err := m.AuthService.ResourceFilterIDs(ctx, accessor, req.IDs,
		interfaces.AuthResourceTypeOperator, interfaces.AuthOperationTypeView)
	if err != nil {
		return
	}
	if len(checkOperatorIDs) != len(req.IDs) {
		clist := utils.FindMissingElements(req.IDs, checkOperatorIDs)
		err = errors.NewHTTPError(ctx, http.StatusForbidden, errors.ErrExtCommonOperationForbidden,
			fmt.Sprintf("operator %v not access", clist))
		return
	}
	// Check if the operator exists.
	operatorList, err = m.DBOperatorManager.SelectByOperatorIDs(ctx, req.IDs)
	if err != nil {
		m.Logger.WithContext(ctx).Errorf("select operator list err: %s", err.Error())
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	if len(operatorList) != len(req.IDs) {
		checkOperatorIDs := []string{}
		for _, v := range operatorList {
			checkOperatorIDs = append(checkOperatorIDs, v.OperatorID)
		}
		clist := utils.FindMissingElements(req.IDs, checkOperatorIDs)
		err = errors.NewHTTPError(ctx, http.StatusNotFound, errors.ErrExtOperatorNotFound,
			fmt.Sprintf("operator %v not found", clist))
		return
	}
	return
}

// Pull the combination operator dependencies and remove duplicates.
// getCompositeOperatorDependencies returns the operators to export. The dataflow
// product was removed, so composite operators no longer pull in DAG-derived
// configs/dependency operators (that path went through flow-automation); the
// export now contains just the requested operators and compositeConfigs is empty.
func (m *operatorManager) getCompositeOperatorDependencies(_ context.Context, operatorDBs []*model.OperatorRegisterDB, _ string) (allOperatorDBs []*model.OperatorRegisterDB,
	compositeConfigs []any, err error) {
	allOperatorDBs = append(allOperatorDBs, operatorDBs...)
	return
}

// batchGetOperatorInfo obtains operator information in batches.
func (m *operatorManager) batchGetOperatorInfo(ctx context.Context, operatorDBs []*model.OperatorRegisterDB) (items []*interfaces.OperatorImpexItem, err error) {
	items = []*interfaces.OperatorImpexItem{}
	// Collect combination operator process ID.
	sourceMap := map[model.SourceType][]string{}
	for _, v := range operatorDBs {
		if v.IsInternal {
			err = errors.NewHTTPError(ctx, http.StatusForbidden, errors.ErrExtCommonInternalComponentNotAllowed,
				fmt.Sprintf("operator %v not allowed to export", v.OperatorID), v.Name)
			return
		}
		extendInfo := map[string]interface{}{}
		err = utils.StringToObject(v.ExtendInfo, &extendInfo)
		if err != nil {
			m.Logger.WithContext(ctx).Errorf("string to object err: %s", err.Error())
			err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
			return
		}
		executeControl := &interfaces.OperatorExecuteControl{}
		err = utils.StringToObject(v.ExecuteControl, &executeControl)
		if err != nil {
			m.Logger.WithContext(ctx).Errorf("string to object err: %s", err.Error())
			err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
			return
		}
		items = append(items, &interfaces.OperatorImpexItem{
			OperatorID:   v.OperatorID,
			OperatorName: v.Name,
			Version:      v.MetadataVersion,
			Status:       interfaces.BizStatus(v.Status),
			MetadataType: interfaces.MetadataType(v.MetadataType),
			ExtendInfo:   extendInfo,
			OperatorInfo: &interfaces.OperatorInfo{
				Type:          interfaces.OperatorType(v.OperatorType),
				ExecutionMode: interfaces.ExecutionMode(v.ExecutionMode),
				Category:      interfaces.BizCategory(v.Category),
				Source:        v.Source,
				IsDataSource:  &v.IsDataSource,
			},
			OperatorExecuteControl: executeControl,
			CreateUser:             v.CreateUser,
			CreateTime:             v.CreateTime,
			UpdateUser:             v.UpdateUser,
			UpdateTime:             v.UpdateTime,
			IsInternal:             v.IsInternal,
		})
		switch v.MetadataType {
		case string(interfaces.MetadataTypeAPI):
			sourceMap[model.SourceTypeOpenAPI] = append(sourceMap[model.SourceTypeOpenAPI], v.MetadataVersion)
		case string(interfaces.MetadataTypeFunc):
			sourceMap[model.SourceTypeFunction] = append(sourceMap[model.SourceTypeFunction], v.MetadataVersion)
		}
	}
	// Collect metadata information.
	sourceIDToMetadataMap, err := m.MetadataService.BatchGetMetadataBySourceIDs(ctx, sourceMap)
	if err != nil {
		m.Logger.WithContext(ctx).Errorf("batch get metadata err: %s", err.Error())
		return
	}
	// Fill in metadata information.
	for _, item := range items {
		item.Metadata = metadata.MetadataDBToStruct(sourceIDToMetadataMap[item.Version])
	}
	return
}
