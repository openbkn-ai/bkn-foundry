package operator

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	icommon "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	oerrors "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/telemetry"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/metric"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/utils"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
)

// EditOperator editing operator (only supports editing the current version)
func (m *operatorManager) EditOperator(ctx context.Context, req *interfaces.OperatorEditReq) (resp *interfaces.OperatorEditResp, err error) {
	// Record observability.
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	telemetry.SetSpanAttributes(ctx, map[string]interface{}{
		"operator_id": req.OperatorID,
		"user_id":     req.UserID,
	})
	// Verify the legality of data.
	operator, metadataDB, accessor, needUpdateMetadata, err := m.preCheckEdit(ctx, req, false)
	if err != nil {
		m.Logger.WithContext(ctx).Warnf("pre check edit failed, err: %v", err)
		return
	}
	var isDataSource bool
	if req.OperatorInfoEdit != nil {
		isDataSource, err = checkIsDataSource(ctx, req.OperatorInfoEdit.ExecutionMode, req.OperatorInfoEdit.IsDataSource)
		if err != nil {
			m.Logger.WithContext(ctx).Warnf("check is data source failed, err: %v", err)
			return
		}
	}
	resp, err = m.editOperator(ctx, req, operator, metadataDB, needUpdateMetadata, false, isDataSource)
	if err != nil {
		m.Logger.WithContext(ctx).Errorf("edit operator failed, err: %v", err)
		return
	}
	// Asynchronous recording of audit logs.
	go func() {
		accountAuthCtx, ok := icommon.GetAccountAuthContextFromCtx(ctx)
		if !ok {
			m.Logger.WithContext(ctx).Errorf("account auth context not found")
			return
		}
		m.AuditLog.Logger(ctx, &metric.AuditLogBuilderParams{
			TokenInfo: accountAuthCtx.TokenInfo,
			Accessor:  accessor,
			Operation: metric.AuditLogOperationEdit,
			Object: &metric.AuditLogObject{
				Type: metric.AuditLogObjectOperator,
				ID:   operator.OperatorID,
				Name: operator.Name,
			},
		})
	}()
	return resp, nil
}

// editOperator
func (m *operatorManager) editOperator(ctx context.Context, req *interfaces.OperatorEditReq, operator *model.OperatorRegisterDB,
	metadataDB interfaces.IMetadataDB, needUpdateMetadata, directPublish, isDataSource bool) (resp *interfaces.OperatorEditResp, err error) {
	// Determine whether the name has changed.
	var nameChanged bool
	if req.Name != "" && req.Name != operator.Name {
		// TODO: Check if the name is the same.
		nameChanged = true
	}
	tx, err := m.DBTx.GetTx(ctx)
	if err != nil {
		m.Logger.WithContext(ctx).Warnf("get tx failed, OperatorID: %s, Version: %s, err: %v", operator.OperatorID, operator.MetadataVersion, err)
		err = oerrors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return nil, err
	}
	defer func() {
		finishErr := finishTx(tx, err != nil)
		if finishErr != nil && err == nil {
			err = finishErr
		}
	}()
	switch interfaces.BizStatus(operator.Status) {
	case interfaces.BizStatusUnpublish, interfaces.BizStatusEditing:
		if directPublish {
			operator.Status = string(interfaces.BizStatusPublished)
		}
		err = m.modifyOperatorInfo(ctx, tx, req, operator, metadataDB, needUpdateMetadata, isDataSource)
	case interfaces.BizStatusPublished:
		operator.Status = string(interfaces.BizStatusEditing)
		if directPublish {
			operator.Status = string(interfaces.BizStatusPublished)
		}
		err = m.upgradeOperatorInfo(ctx, tx, req, operator, metadataDB, needUpdateMetadata, isDataSource)
	case interfaces.BizStatusOffline:
		operator.Status = string(interfaces.BizStatusUnpublish)
		if directPublish {
			operator.Status = string(interfaces.BizStatusPublished)
		}
		err = m.upgradeOperatorInfo(ctx, tx, req, operator, metadataDB, needUpdateMetadata, isDataSource)
	default: // Invalid status.
		err = oerrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.ErrExtOperatorUnSupportEdit, "invalid operator status")
	}
	if err != nil {
		return
	}
	if operator.Status == interfaces.BizStatusPublished.String() {
		err = m.publishRelease(ctx, tx, operator, req.UserID)
		if err != nil {
			return
		}
	}
	if nameChanged {
		// Name change, notify all subscribers.
		err = m.AuthService.NotifyResourceChange(ctx, &interfaces.AuthResource{
			Type: interfaces.AuthResourceTypeOperator.String(),
			ID:   operator.OperatorID,
			Name: operator.Name,
		})
		if err != nil {
			return
		}
	}
	// Check whether the name has changed. If it changes, check whether the name is the same.
	resp = &interfaces.OperatorEditResp{
		Status:     interfaces.BizStatus(operator.Status),
		OperatorID: operator.OperatorID,
		Version:    operator.MetadataVersion,
	}
	return
}

// UpdateOperatorStatus updates operator status.
func (m *operatorManager) UpdateOperatorStatus(ctx context.Context, req *interfaces.OperatorStatusUpdateReq, userID string) (err error) {
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	// Get transaction.
	tx, err := m.DBTx.GetTx(ctx)
	if err != nil {
		m.Logger.WithContext(ctx).Warnf("get tx failed, err: %v", err)
		err = oerrors.DefaultHTTPError(ctx, http.StatusInternalServerError, "get tx failed")
		return
	}
	defer func() {
		finishErr := finishTx(tx, err != nil)
		if finishErr != nil {
			if err != nil {
				m.Logger.Errorf("rollback failed, err: %v", finishErr)
			} else {
				m.Logger.Errorf("commit failed, err: %v", finishErr)
				err = finishErr
			}
		}
	}()
	// Update operator status.
	for _, item := range req.StatusItems {
		err = m.updateSinglOperatorStatus(ctx, tx, item, userID)
		if err != nil {
			return
		}
	}
	return
}

// updateSinglOperatorStatus updates the status of a single operator.
func (m *operatorManager) updateSinglOperatorStatus(ctx context.Context, tx *sql.Tx, itemReq *interfaces.OperatorStatusItem, userID string) (err error) {
	var has bool
	var operator *model.OperatorRegisterDB
	// Get operator.
	has, operator, err = m.DBOperatorManager.SelectByOperatorID(ctx, tx, itemReq.OperatorID)
	if err != nil {
		m.Logger.WithContext(ctx).Warnf("select operator failed, OperatorID: %s, err: %v", itemReq.OperatorID, err)
		err = oerrors.DefaultHTTPError(ctx, http.StatusInternalServerError, "select operator failed")
		return err
	}
	if !has {
		// operator does not exist.
		err = oerrors.DefaultHTTPError(ctx, http.StatusNotFound, "operator not found")
		return err
	}
	// Verify and execute state transitions.
	if !common.CheckStatusTransition(interfaces.BizStatus(operator.Status), itemReq.Status) {
		err = oerrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.ErrExtOperatorStatusInvalid,
			fmt.Sprintf("invalid status transition from %s to %s", operator.Status, itemReq.Status.String()))
		return
	}
	operator.Status = itemReq.Status.String()
	accessor, err := m.AuthService.GetAccessor(ctx, userID)
	if err != nil {
		return
	}
	// Handle change operations based on status.
	var operation metric.AuditLogOperationType
	switch interfaces.BizStatus(operator.Status) {
	case interfaces.BizStatusPublished:
		operation = metric.AuditLogOperationPublish
		// Check publishing permissions.
		err = m.AuthService.CheckPublishPermission(ctx, accessor, operator.OperatorID, interfaces.AuthResourceTypeOperator)
		if err != nil {
			return
		}
		// Check if there is a duplicate name.
		err = m.checkDuplicateName(ctx, operator.Name, operator.OperatorID)
		if err != nil {
			return
		}
		// Update configuration.
		err = m.DBOperatorManager.UpdateOperatorStatus(ctx, tx, operator, userID)
		if err != nil {
			m.Logger.WithContext(ctx).Errorf("update operator status failed, err: %v")
			return oerrors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		}
		err = m.publishRelease(ctx, tx, operator, userID)
	case interfaces.BizStatusUnpublish, interfaces.BizStatusEditing:
		// Check editing permissions.
		err = m.AuthService.CheckModifyPermission(ctx, accessor, operator.OperatorID, interfaces.AuthResourceTypeOperator)
		if err != nil {
			return
		}
		// Update status only.
		err = m.DBOperatorManager.UpdateOperatorStatus(ctx, tx, operator, userID)
		if err != nil {
			m.Logger.WithContext(ctx).Errorf("update operator status failed, err: %v")
			err = oerrors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		}
	case interfaces.BizStatusOffline:
		operation = metric.AuditLogOperationUnpublish
		// Check removal permissions.
		err = m.AuthService.CheckUnpublishPermission(ctx, accessor, operator.OperatorID, interfaces.AuthResourceTypeOperator)
		if err != nil {
			return
		}
		// Update configuration.
		err = m.DBOperatorManager.UpdateOperatorStatus(ctx, tx, operator, userID)
		if err != nil {
			m.Logger.WithContext(ctx).Errorf("update operator status failed, err: %v")
			return oerrors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		}
		// Removed from shelves.
		err = m.unpublishRelease(ctx, tx, operator, userID)
	default:
		err = oerrors.NewHTTPError(ctx, http.StatusBadRequest, oerrors.ErrExtOperatorStatusInvalid, "invalid operator status")
	}
	if err != nil {
		return
	}
	if operation == "" {
		return
	}
	// Asynchronous recording of audit logs.
	go func() {
		accountAuthContext, ok := icommon.GetAccountAuthContextFromCtx(ctx)
		if !ok {
			m.Logger.WithContext(ctx).Errorf("get account auth context failed")
			return
		}
		m.AuditLog.Logger(ctx, &metric.AuditLogBuilderParams{
			TokenInfo: accountAuthContext.TokenInfo,
			Accessor:  accessor,
			Operation: operation,
			Object: &metric.AuditLogObject{
				Type: metric.AuditLogObjectOperator,
				ID:   operator.OperatorID,
				Name: operator.Name,
			},
		})
	}()
	return
}

// checkDuplicateName checks whether there is a duplicate name.
func (m *operatorManager) checkDuplicateName(ctx context.Context, name, operatorID string) (err error) {
	has, operatorDB, err := m.DBOperatorManager.SelectByNameAndStatus(ctx, nil, name, interfaces.BizStatusPublished.String())
	if err != nil {
		m.Logger.WithContext(ctx).Warnf("select operator by name failed, err: %v", err)
		err = oerrors.DefaultHTTPError(ctx, http.StatusInternalServerError, "select operator by name failed")
		return
	}
	if !has || (operatorID != "" && operatorDB.OperatorID == operatorID) {
		return
	}
	err = oerrors.NewHTTPError(ctx, http.StatusConflict, oerrors.ErrExtOperatorExistsSameName,
		"operator name already exists, please use a different name", name)
	return
}

// Pre-edit check: Verify the legality of the edit request: check whether the data exists, whether it is legal, whether there is permission to modify it, and return the query information.
func (m *operatorManager) preCheckEdit(ctx context.Context, req *interfaces.OperatorEditReq, directPublish bool) (operatorDB *model.OperatorRegisterDB,
	metadataDB interfaces.IMetadataDB, accessor *interfaces.AuthAccessor, needUpdateMetadata bool, err error) {
	// Get operator.
	var has bool
	has, operatorDB, err = m.DBOperatorManager.SelectByOperatorID(ctx, nil, req.OperatorID)
	if err != nil {
		m.Logger.WithContext(ctx).Errorf("select operator failed, OperatorID: %s, err: %v", req.OperatorID, err)
		err = oerrors.DefaultHTTPError(ctx, http.StatusInternalServerError, "select operator failed")
		return
	}
	if !has {
		// operator does not exist.
		err = oerrors.DefaultHTTPError(ctx, http.StatusNotFound, "operator not found")
		return
	}
	// Check parameter validity.
	if req.Name != "" {
		err = m.Validator.ValidateOperatorName(ctx, req.Name)
		if err != nil {
			return
		}
	}
	if req.Description != "" {
		err = m.Validator.ValidateOperatorDesc(ctx, req.Description)
		if err != nil {
			return
		}
	}
	// TODO: In theory, system operators need to be verified, and system operators are not allowed to be edited after they are released (for example, only system administrators can edit system operators)
	accessor, err = m.AuthService.GetAccessor(ctx, req.UserID)
	if err != nil {
		return
	}
	if directPublish {
		err = m.AuthService.MultiCheckOperationPermission(ctx, accessor, req.OperatorID, interfaces.AuthResourceTypeOperator,
			interfaces.AuthOperationTypeModify, interfaces.AuthOperationTypePublish)
	} else {
		// Check if you have edit permissions.
		err = m.AuthService.CheckModifyPermission(ctx, accessor, req.OperatorID, interfaces.AuthResourceTypeOperator)
	}
	if err != nil {
		return
	}
	// Get metadata based on version.
	exists, metadataDB, err := m.MetadataService.CheckMetadataExists(ctx, interfaces.MetadataType(operatorDB.MetadataType), operatorDB.MetadataVersion)
	if err != nil {
		m.Logger.WithContext(ctx).Warnf("select api metadata failed, OperatorID: %s, Version: %s, err: %v", operatorDB.OperatorID, operatorDB.MetadataVersion, err)
		return
	}
	if !exists {
		// If metadata does not exist.
		err = oerrors.NewHTTPError(ctx, http.StatusNotFound, oerrors.ErrExtMetadataNotFound, map[string]any{
			"operator_id":      req.OperatorID,
			"metadata_type":    req.MetadataType,
			"metadata_version": operatorDB.MetadataVersion,
			"error":            "metadata not found",
		})
		return
	}
	var updateMetadataDB interfaces.IMetadataDB
	updateMetadataDB, err = m.getUpdateMetadataDB(ctx, req, operatorDB, metadataDB)
	if err != nil { // No need to update metadata.
		return
	}
	var desc string
	if updateMetadataDB != nil {
		if req.MetadataType == interfaces.MetadataTypeFunc {
			// Is there any change in the updated function content?.
			if updateMetadataDB.GetScriptType() != metadataDB.GetScriptType() {
				needUpdateMetadata = true
				metadataDB.SetScriptType(updateMetadataDB.GetScriptType())
			}
			if updateMetadataDB.GetCode() != metadataDB.GetCode() {
				needUpdateMetadata = true
				metadataDB.SetCode(updateMetadataDB.GetCode())
			}
			if len(updateMetadataDB.GetDependencies()) > 0 || len(updateMetadataDB.GetDependencies()) != len(metadataDB.GetDependencies()) {
				needUpdateMetadata = true
				metadataDB.SetDependencies(updateMetadataDB.GetDependencies())
			}
			if updateMetadataDB.GetDependenciesURL() != metadataDB.GetDependenciesURL() {
				needUpdateMetadata = true
				metadataDB.SetDependenciesURL(updateMetadataDB.GetDependenciesURL())
			}
		}
		if metadataDB.GetServerURL() != updateMetadataDB.GetServerURL() {
			metadataDB.SetServerURL(updateMetadataDB.GetServerURL())
			needUpdateMetadata = true
		}
		if metadataDB.GetSummary() != updateMetadataDB.GetSummary() {
			err = m.Validator.ValidateOperatorName(ctx, updateMetadataDB.GetSummary())
			if err != nil {
				return
			}
			metadataDB.SetSummary(updateMetadataDB.GetSummary())
			needUpdateMetadata = true
		}
		if updateMetadataDB.GetAPISpec() != "" {
			metadataDB.SetAPISpec(updateMetadataDB.GetAPISpec())
			needUpdateMetadata = true
		}
		desc = updateMetadataDB.GetDescription()
	}
	if req.Description != "" {
		desc = req.Description
	}
	if metadataDB.GetDescription() != desc {
		err = m.Validator.ValidateOperatorDesc(ctx, desc)
		if err != nil {
			return
		}
		metadataDB.SetDescription(desc)
		needUpdateMetadata = true
	}
	return
}

// Get metadata to be updated.
func (m *operatorManager) getUpdateMetadataDB(ctx context.Context, req *interfaces.OperatorEditReq, operatorDB *model.OperatorRegisterDB,
	metadataDB interfaces.IMetadataDB) (updateMetadataDB interfaces.IMetadataDB, err error) {
	// Parse incoming data.
	switch req.MetadataType {
	case interfaces.MetadataTypeAPI:
		if req.OpenAPIInput == nil || req.Data == nil {
			return
		}
		var updateMetadataDBs []interfaces.IMetadataDB
		updateMetadataDBs, err = m.MetadataService.ParseMetadata(ctx, req.MetadataType, req.OpenAPIInput)
		if err != nil {
			return
		}
		switch interfaces.OperatorType(operatorDB.OperatorType) {
		case interfaces.OperatorTypeBase:
			for _, md := range updateMetadataDBs {
				// If it is a basic operator, match metadata based on path and method.
				if metadataDB.GetPath() == md.GetPath() && metadataDB.GetMethod() == md.GetMethod() {
					updateMetadataDB = md
					break
				}
			}
			// Check for updates.
			if updateMetadataDB == nil {
				// Interaction design requires returning specified error information: https://confluence.aishu.cn/pages/viewpage.action?pageId=280780968.
				err = oerrors.NewHTTPError(ctx, http.StatusNotFound, oerrors.ErrExtCommonNoMatchedMethodPath,
					"no matched method path found or metadata data not exist").WithDescription(oerrors.ErrExtToolNotExistInFile)
				return
			}
		case interfaces.OperatorTypeComposite:
			// If it is a composite operator, only the first metadata is updated.
			updateMetadataDB = updateMetadataDBs[0]
		}
	case interfaces.MetadataTypeFunc:
		if req.FunctionInputEdit == nil {
			return
		}
		funcInput := &interfaces.FunctionInput{
			Name:            req.Name,
			Description:     req.Description,
			Inputs:          req.FunctionInputEdit.Inputs,
			Outputs:         req.FunctionInputEdit.Outputs,
			ScriptType:      req.FunctionInputEdit.ScriptType,
			Code:            req.FunctionInputEdit.Code,
			Dependencies:    req.FunctionInputEdit.Dependencies,
			DependenciesURL: req.FunctionInputEdit.DependenciesURL,
		}
		var updateMetadataDBs []interfaces.IMetadataDB
		updateMetadataDBs, err = m.MetadataService.ParseMetadata(ctx, req.MetadataType, funcInput)
		if err != nil {
			return
		}
		updateMetadataDB = updateMetadataDBs[0]
	default:
		err = oerrors.DefaultHTTPError(ctx, http.StatusBadRequest, "unsupported metadata type")
		return
	}
	return
}

// modifyOperatorInfo Modifies operator registration configuration.
func (m *operatorManager) modifyOperatorInfo(ctx context.Context, tx *sql.Tx, req *interfaces.OperatorEditReq, operator *model.OperatorRegisterDB,
	metdataDB interfaces.IMetadataDB, needUpdateMetadata, isDataSource bool) (err error) {
	err = m.modifyOperator(ctx, tx, req, operator, isDataSource)
	if err != nil {
		return
	}
	if !needUpdateMetadata {
		return
	}
	// Update operator metadata.
	metdataDB.SetUpdateInfo(req.UserID)
	err = m.MetadataService.UpdateMetadata(ctx, tx, metdataDB)
	if err != nil {
		m.Logger.WithContext(ctx).Warnf("modify api metadata failed, OperatorID: %s, Version: %s, err: %v", operator.OperatorID, operator.MetadataVersion, err)
	}
	return
}

// modifyOperator edit operator.
func (m *operatorManager) modifyOperator(ctx context.Context, tx *sql.Tx, req *interfaces.OperatorEditReq,
	operator *model.OperatorRegisterDB, isDataSource bool) (err error) {
	// record observable.
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	// Update parameters.
	operator.UpdateUser = req.UserID
	if req.OperatorInfoEdit != nil {
		operator.OperatorType = string(req.OperatorInfoEdit.Type)
		operator.ExecutionMode = string(req.OperatorInfoEdit.ExecutionMode)
		operator.Category = string(req.OperatorInfoEdit.Category)
		operator.Source = req.OperatorInfoEdit.Source
		operator.IsDataSource = isDataSource
	}
	if req.OperatorExecuteControl != nil {
		operator.ExecuteControl = utils.ObjectToJSON(req.OperatorExecuteControl)
	}
	if req.ExtendInfo != nil {
		operator.ExtendInfo = utils.ObjectToJSON(req.ExtendInfo)
	}
	// If name changes, update name based on operatorID.
	if req.Name != "" && req.Name != operator.Name { // Check if there is a duplicate name.
		err = m.checkDuplicateName(ctx, req.Name, operator.OperatorID)
		if err != nil {
			// Interaction design requires returning specified error information: https://confluence.aishu.cn/pages/viewpage.action?pageId=280780968.
			httErr := &oerrors.HTTPError{}
			if errors.As(err, &httErr) && httErr.HTTPCode == http.StatusConflict {
				err = httErr.WithDescription(oerrors.ErrExtCommonNameExists)
			}
			return
		}
		operator.Name = req.Name
	}
	// Update operator information.
	err = m.DBOperatorManager.UpdateByOperatorID(ctx, tx, operator)
	if err != nil {
		m.Logger.WithContext(ctx).Warnf("update operator failed, OperatorID: %s, Version: %s, err: %v", operator.OperatorID, operator.MetadataVersion, err)
		err = oerrors.DefaultHTTPError(ctx, http.StatusInternalServerError, "update operator failed, err")
	}
	return
}

// upgradeOperatorInfo upgrade operator information.
/*
	The released version metadata has changed, so a new metadata record must be generated:
	1. Create a new record in the metadata table.
	2. Update the registry configuration with the version and change information.
	3. If direct_publish is true, publish directly and add a record to release/release_history.
*/

func (m *operatorManager) upgradeOperatorInfo(ctx context.Context, tx *sql.Tx, req *interfaces.OperatorEditReq, operator *model.OperatorRegisterDB,
	metadataDB interfaces.IMetadataDB, needUpdateMetadata, isDataSource bool) (err error) {
	// record observable.
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	// Upgrade metadata.
	if needUpdateMetadata {
		metadataDB.SetVersion(uuid.New().String())
		metadataDB.SetUpdateInfo(req.UserID)
		_, err = m.MetadataService.RegisterMetadata(ctx, tx, metadataDB)
		if err != nil {
			m.Logger.WithContext(ctx).Errorf("register metadata failed, err: %v", err)
			err = oerrors.DefaultHTTPError(ctx, http.StatusInternalServerError, "register metadata failed")
			return
		}
	}
	// 3. Assemble operator registration information and add it to the operator registration table.
	operator.MetadataVersion = metadataDB.GetVersion()
	err = m.modifyOperator(ctx, tx, req, operator, isDataSource)
	return
}
