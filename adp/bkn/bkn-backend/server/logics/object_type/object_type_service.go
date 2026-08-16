// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package object_type

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bytedance/sonic"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"github.com/rs/xid"
	"go.opentelemetry.io/otel/codes"

	bknsdk "bkn-backend/bkn-specification/bkn"
	"bkn-backend/common"
	cond "bkn-backend/common/condition"
	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
	"bkn-backend/logics"
	"bkn-backend/logics/batchindex"
	"bkn-backend/logics/model_factory"
	"bkn-backend/logics/permission"
	"bkn-backend/logics/user_mgmt"
)

var (
	otServiceOnce sync.Once
	otService     interfaces.ObjectTypeService
)

type objectTypeService struct {
	appSetting *common.AppSetting
	db         *sql.DB
	aoa        interfaces.AgentOperatorAccess
	cga        interfaces.ConceptGroupAccess
	ma         interfaces.MetricAccess
	mfs        interfaces.ModelFactoryService
	ota        interfaces.ObjectTypeAccess
	ps         interfaces.PermissionService
	ums        interfaces.UserMgmtService
	vba        interfaces.VegaBackendAccess
}

func invalidParameterDetail(ctx context.Context, name string, templateData map[string]any) string {
	return i18n.Translate(rest.GetLanguageByCtx(ctx), "BknBackend.ObjectType.InvalidParameter.Detail."+name, templateData)
}

func NewObjectTypeService(appSetting *common.AppSetting) interfaces.ObjectTypeService {
	otServiceOnce.Do(func() {
		otService = &objectTypeService{
			appSetting: appSetting,
			db:         logics.DB,
			aoa:        logics.AOA,
			cga:        logics.CGA,
			ma:         logics.MA,
			mfs:        model_factory.NewModelFactoryService(appSetting, logics.MFA),
			ota:        logics.OTA,
			ps:         permission.NewPermissionService(appSetting),
			ums:        user_mgmt.NewUserMgmtService(appSetting),
			vba:        logics.VBA,
		}
	})
	return otService
}

// validateObjectTypeStrictExternalDeps checks backing data view or vega resource, vector embedding models, and logic property references.
func (ots *objectTypeService) validateObjectTypeStrictExternalDeps(ctx context.Context, objectType *interfaces.ObjectType) error {
	if objectType.DataSource != nil && objectType.DataSource.ID != "" {
		switch objectType.DataSource.Type {
		case interfaces.DATA_SOURCE_TYPE_RESOURCE:
			res, err := ots.vba.GetResourceByID(ctx, objectType.DataSource.ID)
			if err != nil {
				return rest.NewHTTPError(ctx, http.StatusBadRequest,
					berrors.BknBackend_ObjectType_InvalidParameter).
					WithErrorDetails(invalidParameterDetail(ctx, "ResourceLookupFailed", map[string]any{"objectType": objectType.OTName, "resource": objectType.DataSource.ID}))
			}
			if res == nil {
				return rest.NewHTTPError(ctx, http.StatusBadRequest,
					berrors.BknBackend_ObjectType_InvalidParameter).
					WithErrorDetails(invalidParameterDetail(ctx, "ResourceNotFound", map[string]any{"objectType": objectType.OTName, "resource": objectType.DataSource.ID}))
			}
		default:
			return logics.UnsupportedObjectTypeDataSourceError(ctx, objectType.OTID, objectType.DataSource.Type)
		}
	}
	// Schema for logic properties (type, data_source) is validated in driveradapters.ValidateObjectType.
	for _, lp := range objectType.LogicProperties {
		switch lp.Type {
		case interfaces.LOGIC_PROPERTY_TYPE_METRIC:
			if err := ots.validateLogicMetricProperty(ctx, objectType, lp); err != nil {
				return err
			}
		case interfaces.LOGIC_PROPERTY_TYPE_TOOL:
			if err := ots.aoa.GetToolByID(ctx, lp.DataSource.BoxID, lp.DataSource.ToolID); err != nil {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
					WithErrorDetails(invalidParameterDetail(ctx, "ToolLookupFailed", map[string]any{"objectType": objectType.OTName, "property": lp.Name, "box": lp.DataSource.BoxID, "tool": lp.DataSource.ToolID}))
			}
		}
	}
	return nil
}

func (ots *objectTypeService) CheckObjectTypeExistByID(ctx context.Context,
	knID string, branch string, otID string) (string, bool, error) {

	ctx, span := oteltrace.StartNamedInternalSpan(ctx, fmt.Sprintf("校验对象类[%s]的存在性", otID))
	defer span.End()

	otName, exist, err := ots.ota.CheckObjectTypeExistByID(ctx, knID, branch, otID)
	if err != nil {
		otellog.LogError(ctx, fmt.Sprintf("在业务知识网络[%s]下按ID[%s]获取对象类失败", knID, otID), err)
		return "", exist, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ObjectType_InternalError_CheckObjectTypeIfExistFailed).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return otName, exist, nil
}

func (ots *objectTypeService) CheckObjectTypeExistByName(ctx context.Context,
	knID string, branch string, otName string) (string, bool, error) {

	ctx, span := oteltrace.StartNamedInternalSpan(ctx, fmt.Sprintf("校验对象类[%s]的存在性", otName))
	defer span.End()

	otID, exist, err := ots.ota.CheckObjectTypeExistByName(ctx, knID, branch, otName)
	if err != nil {
		otellog.LogError(ctx, fmt.Sprintf("在业务知识网络[%s]下按名称[%s]获取对象类失败", knID, otName), err)
		return "", exist, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ObjectType_InternalError_CheckObjectTypeIfExistFailed).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return otID, exist, nil
}

func (ots *objectTypeService) CreateObjectTypes(ctx context.Context, tx *sql.Tx,
	objectTypes []*interfaces.ObjectType, mode string, needCreateConceptGroupRelation bool, strictMode bool) ([]string, error) {

	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Create object type")
	defer span.End()

	// Check whether the user ID can modify the business knowledge network.
	err := ots.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: interfaces.RESOURCE_TYPE_KN,
		ID:   objectTypes[0].KNID,
	}, []string{interfaces.OPERATION_TYPE_MODIFY})
	if err != nil {
		return []string{}, err
	}

	currentTime := time.Now().UnixMilli()
	for _, objectType := range objectTypes {
		// Generate a distributed ID when the submitted model ID is empty.
		if objectType.OTID == "" {
			objectType.OTID = xid.New().String()
		}

		accountInfo := interfaces.AccountInfo{}
		if ctx.Value(interfaces.ACCOUNT_INFO_KEY) != nil {
			accountInfo = ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
		}
		objectType.Creator = accountInfo
		objectType.Updater = accountInfo

		objectType.CreateTime = currentTime
		objectType.UpdateTime = currentTime

		if strictMode {
			if err := ots.validateObjectTypeStrictExternalDeps(ctx, objectType); err != nil {
				return []string{}, err
			}
		}

		bknObj := logics.ToBKNObjectType(objectType)
		objectType.BKNRawContent = bknsdk.SerializeObjectType(bknObj)
	}

	// 0. Begin the transaction.
	if tx == nil {
		tx, err = ots.db.Begin()
		if err != nil {
			otellog.LogError(ctx, "Begin transaction error", err)
			return []string{}, rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_ObjectType_InternalError_BeginTransactionFailed).
				WithErrorDetails(err.Error())
		}
		// 0.1 On failure.
		defer func() {
			switch err {
			case nil:
				// Commit the transaction.
				err = tx.Commit()
				if err != nil {
					otellog.LogError(ctx, "CreateObjectType Transaction Commit Failed", err)
					return
				}
				otellog.LogDebug(ctx, "CreateObjectType Transaction Commit Success")
			default:
				rollbackErr := tx.Rollback()
				if rollbackErr != nil {
					otellog.LogError(ctx, "CreateObjectType Transaction Rollback Error", err)
				}
			}
		}()
	}

	createObjectTypes, updateObjectTypes, err := ots.handleObjectTypeImportMode(ctx, mode, objectTypes)
	if err != nil {
		return []string{}, err
	}

	// Create.
	otIDs := []string{}
	for _, objectType := range createObjectTypes {
		otIDs = append(otIDs, objectType.OTID)
		err = ots.ota.CreateObjectType(ctx, tx, objectType)
		if err != nil {
			logger.Errorf("CreateObjectType error: %s", err.Error())
			span.SetStatus(codes.Error, "创建对象类失败")

			return []string{}, rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_ObjectType_InternalError).
				WithErrorDetails(err.Error())
		}

		err = ots.ota.CreateObjectTypeStatus(ctx, tx, objectType)
		if err != nil {
			logger.Errorf("CreateObjectTypeStatus error: %s", err.Error())
			span.SetStatus(codes.Error, "创建对象类状态失败")

			return []string{}, rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_ObjectType_InternalError).
				WithErrorDetails(err.Error())
		}

		// Create object type-to-group relationships as needed.
		if needCreateConceptGroupRelation {
			// Create missing object type-to-group relationships after retrieving existing bindings.
			if len(objectType.ConceptGroups) > 0 {
				err = ots.handleGroupRelations(ctx, tx, objectType, currentTime, strictMode)
				if err != nil {
					span.SetStatus(codes.Error, "处理对象类与分组的关系失败")
					return []string{}, err
				}
			}
		}
	}

	// Update.
	for _, objectType := range updateObjectTypes {
		err = ots.UpdateObjectType(ctx, tx, objectType, strictMode)
		if err != nil {
			return []string{}, err
		}
	}

	insetObjectTypes := createObjectTypes
	insetObjectTypes = append(insetObjectTypes, updateObjectTypes...)
	err = ots.InsertDatasetData(ctx, insetObjectTypes)
	if err != nil {
		logger.Errorf("InsertDatasetData error: %s", err.Error())
		span.SetStatus(codes.Error, "对象类索引写入失败")

		return []string{}, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ObjectType_InternalError_InsertOpenSearchDataFailed).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return otIDs, nil
}

// ValidateObjectTypes checks dependency existence only; does not write to the database.
func (ots *objectTypeService) ValidateObjectTypes(ctx context.Context, knID string, branch string,
	objectTypes []*interfaces.ObjectType, strictMode bool, batch *interfaces.BatchIDIndex, mode string) error {

	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "ValidateObjectTypes")
	defer span.End()

	if len(objectTypes) == 0 {
		return nil
	}

	err := ots.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: interfaces.RESOURCE_TYPE_KN,
		ID:   knID,
	}, []string{interfaces.OPERATION_TYPE_MODIFY})
	if err != nil {
		return err
	}

	_, _, err = ots.handleObjectTypeImportMode(ctx, mode, objectTypes)
	if err != nil {
		return err
	}

	for _, objectType := range objectTypes {
		objectType.KNID = knID
		objectType.Branch = branch
		if strictMode {
			if err := ots.validateObjectTypeStrictExternalDeps(ctx, objectType); err != nil {
				return err
			}

			// Validate concept groups. Same-batch group IDs are treated as pending creation and skip storage lookup.
			if len(objectType.ConceptGroups) > 0 {
				cgIDs := []string{}
				for _, cg := range objectType.ConceptGroups {
					cgIDs = append(cgIDs, cg.CGID)
				}
				cgIDs = common.DuplicateSlice(cgIDs)

				var needDBLookup []string
				for _, id := range cgIDs {
					if batch != nil && batchindex.HasConceptGroupID(id, batch) {
						continue
					}
					needDBLookup = append(needDBLookup, id)
				}
				if len(needDBLookup) == 0 {
					continue
				}

				tx, err := ots.db.Begin()
				if err != nil {
					return rest.NewHTTPError(ctx, http.StatusInternalServerError,
						berrors.BknBackend_ObjectType_InternalError_BeginTransactionFailed).
						WithErrorDetails(err.Error())
				}
				defer func() { _ = tx.Rollback() }()

				conceptGroups, err := ots.cga.GetConceptGroupsByIDs(ctx, tx, knID, branch, needDBLookup)
				if err != nil {
					return rest.NewHTTPError(ctx, http.StatusInternalServerError,
						berrors.BknBackend_ObjectType_InternalError).
						WithErrorDetails(fmt.Sprintf("GetConceptGroupsByIDs failed: %s", err.Error()))
				}
				if len(conceptGroups) != len(needDBLookup) {
					return rest.NewHTTPError(ctx, http.StatusBadRequest,
						berrors.BknBackend_ObjectType_InvalidParameter).
						WithErrorDetails(invalidParameterDetail(ctx, "ConceptGroupsNotFound", map[string]any{"expected": len(needDBLookup), "actual": len(conceptGroups)}))
				}
			}
		}
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

func (ots *objectTypeService) ListObjectTypes(ctx context.Context, tx *sql.Tx,
	query interfaces.ObjectTypesQueryParams) ([]*interfaces.ObjectType, int, error) {

	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "查询对象类列表")
	defer span.End()

	// Check whether the user ID can view the business knowledge network.
	err := ots.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: interfaces.RESOURCE_TYPE_KN,
		ID:   query.KNID,
	}, []string{interfaces.OPERATION_TYPE_VIEW_DETAIL})
	if err != nil {
		return []*interfaces.ObjectType{}, 0, err
	}

	// 0. Begin the transaction.
	if tx == nil {
		tx, err = ots.db.Begin()
		if err != nil {
			otellog.LogError(ctx, "Begin transaction error", err)
			return []*interfaces.ObjectType{}, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_ObjectType_InternalError_BeginTransactionFailed).
				WithErrorDetails(err.Error())
		}
		// 0.1 On failure.
		defer func() {
			switch err {
			case nil:
				// Commit the transaction.
				err = tx.Commit()
				if err != nil {
					otellog.LogError(ctx, "ListObjectTypes Transaction Commit Failed", err)
					return
				}
				otellog.LogDebug(ctx, "ListObjectTypes Transaction Commit Success")
			default:
				rollbackErr := tx.Rollback()
				if rollbackErr != nil {
					otellog.LogError(ctx, "ListObjectTypes Transaction Rollback Error", err)
				}
			}
		}()
	}

	// Get the object type list.
	objectTypes, err := ots.ota.ListObjectTypes(ctx, tx, query)
	if err != nil {
		logger.Errorf("ListObjectTypes error: %s", err.Error())
		span.SetStatus(codes.Error, "List object types error")

		return []*interfaces.ObjectType{}, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ObjectType_InternalError).WithErrorDetails(err.Error())
	}

	total, err := ots.ota.GetObjectTypesTotal(ctx, query)
	if err != nil {
		logger.Errorf("GetObjectTypesTotal error: %s", err.Error())
		span.SetStatus(codes.Error, "Get object types total error")

		return []*interfaces.ObjectType{}, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ObjectType_InternalError).WithErrorDetails(err.Error())
	}
	if len(objectTypes) == 0 {
		span.SetStatus(codes.Ok, "")
		return objectTypes, total, nil
	}

	accountInfos := make([]*interfaces.AccountInfo, 0, len(objectTypes)*2)
	for _, objectType := range objectTypes {
		accountInfos = append(accountInfos, &objectType.Creator, &objectType.Updater)
	}

	err = ots.ums.GetAccountNames(ctx, accountInfos)
	if err != nil {
		span.SetStatus(codes.Error, "GetAccountNames error")

		return []*interfaces.ObjectType{}, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ObjectType_InternalError).WithErrorDetails(err.Error())
	}

	// Object type groups are intentionally omitted from this response.
	// otGroups, err := ots.cga.GetConceptGroupsByOTIDs(ctx, tx, interfaces.ConceptGroupRelationsQueryParams{
	// 	KNID:   query.KNID,
	// 	Branch: query.Branch,
	// 	OTIDs:  otIDs,
	// })
	// if err != nil {
	// 	span.SetStatus(codes.Error, "GetConceptGroupsByOTIDs error")

	// 	return []*interfaces.ObjectType{}, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError,
	// 		berrors.BknBackend_ObjectType_InternalError).WithErrorDetails(err.Error())
	// }

	// View information and mapped field display names are intentionally omitted.
	// for _, objectType := range objectTypes {
	// 	// Get view field display names.
	// 	if objectType.DataSource != nil && objectType.DataSource.ID != "" {
	// 		dataView, err := ots.dva.GetDataViewByID(ctx, objectType.DataSource.ID)
	// 		if err != nil {
	// 			return []*interfaces.ObjectType{}, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError,
	// 				berrors.BknBackend_ObjectType_InternalError_GetDataViewByIDFailed).
	// 				WithErrorDetails(err.Error())
	// 		}
	// 		if dataView == nil {
	// 			otellog.LogWarn(ctx, fmt.Sprintf("Object type [%s]'s Data view %s not found", objectType.OTID, objectType.DataSource.ID))
	// 		} else {
	// 			objectType.DataSource.Name = dataView.ViewName
	// 			// Resolve data-property mapped field display names.
	// 			for j, prop := range objectType.DataProperties {
	// 				// Resolve the field display name only when it is present.
	// 				if prop.MappedField != nil {
	// 					if field, exists := dataView.FieldsMap[prop.MappedField.Name]; exists {
	// 						objectType.DataProperties[j].MappedField.DisplayName = field.DisplayName
	// 						objectType.DataProperties[j].MappedField.Type = field.Type
	// 					}
	// 				}
	// 				// Return supported operators for string properties.
	// 				objectType.DataProperties[j].ConditionOperations = ots.processConditionOperations(objectType, prop, dataView)
	// 			}
	// 		}
	// 	}

	// 	// Add group information to object types.
	// 	objectType.ConceptGroups = otGroups[objectType.OTID]
	// }

	span.SetStatus(codes.Ok, "")
	return objectTypes, total, nil
}

func (ots *objectTypeService) GetObjectTypesByIDs(ctx context.Context, tx *sql.Tx,
	knID string, branch string, otIDs []string) ([]*interfaces.ObjectType, error) {
	// Get object types.
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, fmt.Sprintf("查询对象类[%s]信息", otIDs))
	defer span.End()

	// Check whether the user ID can view the business knowledge network.
	err := ots.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: interfaces.RESOURCE_TYPE_KN,
		ID:   knID,
	}, []string{interfaces.OPERATION_TYPE_VIEW_DETAIL})
	if err != nil {
		return []*interfaces.ObjectType{}, err
	}

	// 0. Begin the transaction.
	if tx == nil {
		tx, err = ots.db.Begin()
		if err != nil {
			otellog.LogError(ctx, "Begin transaction error", err)
			return []*interfaces.ObjectType{}, rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_ObjectType_InternalError_BeginTransactionFailed).
				WithErrorDetails(err.Error())
		}
		// 0.1 On failure.
		defer func() {
			switch err {
			case nil:
				// Commit the transaction.
				err = tx.Commit()
				if err != nil {
					otellog.LogError(ctx, "GetObjectTypes Transaction Commit Failed", err)
					return
				}
				otellog.LogDebug(ctx, "GetObjectTypes Transaction Commit Success")
			default:
				rollbackErr := tx.Rollback()
				if rollbackErr != nil {
					otellog.LogError(ctx, "GetObjectTypes Transaction Rollback Error", err)
				}
			}
		}()
	}

	// De-duplicate IDs before querying.
	otIDs = common.DuplicateSlice(otIDs)

	// Get basic object type information.
	objectTypes, err := ots.ota.GetObjectTypesByIDs(ctx, tx, knID, branch, otIDs)
	if err != nil {
		logger.Errorf("GetObjectTypesByObjectTypeIDs error: %s", err.Error())
		span.SetStatus(codes.Error, fmt.Sprintf("Get object types[%s] error: %v", otIDs, err))

		return []*interfaces.ObjectType{}, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ObjectType_InternalError_GetObjectTypesByIDsFailed).WithErrorDetails(err.Error())
	}

	if len(objectTypes) != len(otIDs) {
		errStr := fmt.Sprintf("Exists any object types not found, expect object types nums is [%d], actual object types num is [%d]", len(otIDs), len(objectTypes))
		logger.Errorf(errStr)
		span.SetStatus(codes.Error, errStr)

		return []*interfaces.ObjectType{}, rest.NewHTTPError(ctx, http.StatusNotFound,
			berrors.BknBackend_ObjectType_ObjectTypeNotFound).WithErrorDetails(errStr)
	}

	// Get object type groups.
	otGroups, err := ots.cga.GetConceptGroupsByOTIDs(ctx, tx, interfaces.ConceptGroupRelationsQueryParams{
		KNID:   knID,
		Branch: branch,
		OTIDs:  otIDs,
	})
	if err != nil {
		span.SetStatus(codes.Error, "GetConceptGroupsByOTIDs error")

		return []*interfaces.ObjectType{}, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ObjectType_InternalError).WithErrorDetails(err.Error())
	}

	// Resolve a non-empty data view ID to its name.
	// Request the view.
	for _, objectType := range objectTypes {
		// Process the data source and operators.
		err = ots.processObjectTypeDetails(ctx, objectType)
		if err != nil {
			return []*interfaces.ObjectType{}, err
		}
		// Add group information to object types.
		objectType.ConceptGroups = otGroups[objectType.OTID]
	}

	accountInfos := make([]*interfaces.AccountInfo, 0, len(objectTypes)*2)
	for _, objectType := range objectTypes {
		accountInfos = append(accountInfos, &objectType.Creator, &objectType.Updater)
	}

	err = ots.ums.GetAccountNames(ctx, accountInfos)
	if err != nil {
		span.SetStatus(codes.Error, "GetAccountNames error")

		return []*interfaces.ObjectType{}, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ObjectType_InternalError).WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return objectTypes, nil
}

func (ots *objectTypeService) GetObjectTypeSampleData(ctx context.Context,
	knID string, branch string, otID string, query interfaces.ObjectTypeSampleDataQueryParams) (*interfaces.ObjectTypeSampleData, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "query object type sample data")
	defer span.End()

	if query.Limit <= 0 {
		query.Limit = 20
	}
	if query.Limit > 100 {
		query.Limit = 100
	}
	if query.Offset < 0 {
		query.Offset = 0
	}

	objectTypes, err := ots.GetObjectTypesByIDs(ctx, nil, knID, branch, []string{otID})
	if err != nil {
		return nil, err
	}
	if len(objectTypes) == 0 || objectTypes[0] == nil {
		return nil, rest.NewHTTPError(ctx, http.StatusNotFound,
			berrors.BknBackend_ObjectType_ObjectTypeNotFound).WithErrorDetails(invalidParameterDetail(ctx, "ObjectTypeNotFound", map[string]any{"objectTypeID": otID}))
	}

	objectType := objectTypes[0]
	result := &interfaces.ObjectTypeSampleData{
		Columns: []*interfaces.ObjectTypeSampleDataColumn{},
		Entries: []map[string]any{},
		Name:    objectType.OTName,
	}
	if objectType.DataSource == nil || strings.TrimSpace(objectType.DataSource.ID) == "" {
		return result, nil
	}

	fieldMappings := []struct {
		sourceField  string
		propertyName string
	}{}
	outputFields := []string{}
	for _, prop := range objectType.DataProperties {
		if prop == nil || strings.TrimSpace(prop.Name) == "" {
			continue
		}

		title := prop.DisplayName
		if title == "" {
			title = prop.Name
		}
		result.Columns = append(result.Columns, &interfaces.ObjectTypeSampleDataColumn{
			DataIndex: prop.Name,
			Title:     title,
		})

		sourceField := prop.Name
		if prop.MappedField != nil && strings.TrimSpace(prop.MappedField.Name) != "" {
			sourceField = prop.MappedField.Name
		}
		fieldMappings = append(fieldMappings, struct {
			sourceField  string
			propertyName string
		}{
			sourceField:  sourceField,
			propertyName: prop.Name,
		})
		outputFields = append(outputFields, sourceField)
	}

	dsType := objectType.DataSource.Type

	var datasetResp *interfaces.DatasetQueryResponse
	switch dsType {
	case interfaces.DATA_SOURCE_TYPE_RESOURCE:
		datasetResp, err = ots.vba.QueryResourceData(ctx, objectType.DataSource.ID, &interfaces.ResourceDataQueryParams{
			Paging: interfaces.ResourceDataPagingRequest{
				Mode:   "single",
				Limit:  query.Limit,
				Offset: query.Offset,
			},
			NeedTotal:    query.NeedTotal,
			OutputFields: outputFields,
		})
	default:
		return nil, logics.UnsupportedObjectTypeDataSourceError(ctx, objectType.OTID, dsType)
	}
	if err != nil {
		logger.Errorf("Query object type sample data error: %s", err.Error())
		span.SetStatus(codes.Error, "query object type sample data failed")
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ObjectType_InternalError).WithErrorDetails(err.Error())
	}
	if datasetResp == nil {
		return result, nil
	}

	for _, entry := range datasetResp.Entries {
		row := map[string]any{}
		for _, mapping := range fieldMappings {
			row[mapping.propertyName] = entry[mapping.sourceField]
		}
		result.Entries = append(result.Entries, row)
	}
	result.TotalCount = datasetResp.TotalCount
	span.SetStatus(codes.Ok, "")
	return result, nil
}

// hasDataPropertyIndexAffectingChanges detects changes to index-affecting data property fields.
// Index-affecting fields include Name, Type, IndexConfig, MappedField.Name, and MappedField.Type.
func hasDataPropertyIndexAffectingChanges(oldProp, newProp *interfaces.DataProperty) bool {
	if oldProp == nil || newProp == nil {
		return oldProp != newProp
	}

	// Compare property names.
	if oldProp.Name != newProp.Name {
		return true
	}

	// Compare property types.
	if oldProp.Type != newProp.Type {
		return true
	}

	// Compare index configuration.
	if !compareIndexConfig(oldProp.IndexConfig, newProp.IndexConfig) {
		return true // Return true when configuration differs.
	}

	// Compare mapped field names and types.
	if !compareMappedField(oldProp.MappedField, newProp.MappedField) {
		return true
	}

	return false
}

// compareIndexConfig compares two index configurations.
func compareIndexConfig(oldConfig, newConfig *interfaces.IndexConfig) bool {
	if oldConfig == nil && newConfig == nil {
		return true // Both are empty, so their states are equal.
	}
	if oldConfig == nil || newConfig == nil {
		return false // One is empty and the other is not, so their states differ.
	}

	// Compare JSON serialization to ensure accuracy.
	oldBytes, err := sonic.Marshal(oldConfig)
	if err != nil {
		return false
	}
	newBytes, err := sonic.Marshal(newConfig)
	if err != nil {
		return false
	}

	return string(oldBytes) == string(newBytes)
}

// compareMappedField compares mapped fields by Name and Type only.
func compareMappedField(oldField, newField *interfaces.Field) bool {
	if oldField == nil && newField == nil {
		return true
	}
	if oldField == nil || newField == nil {
		return false
	}

	// Compare field names.
	if oldField.Name != newField.Name {
		return false
	}

	// Compare field types.
	if oldField.Type != newField.Type {
		return false
	}

	return true
}

// hasAnyDataPropertyIndexAffectingChanges detects index-affecting changes in data property lists.
func hasAnyDataPropertyIndexAffectingChanges(oldProps, newProps []*interfaces.DataProperty) bool {
	// Convert old properties to a map keyed by Name.
	oldPropMap := make(map[string]*interfaces.DataProperty)
	for _, prop := range oldProps {
		if prop != nil {
			oldPropMap[prop.Name] = prop
		}
	}

	// Traverse new properties and compare corresponding old properties.
	for _, newProp := range newProps {
		if newProp == nil {
			continue
		}

		oldProp, exists := oldPropMap[newProp.Name]
		if !exists {
			// Added properties can affect indexes.
			return true
		}

		// Check whether property changes affect indexes.
		if hasDataPropertyIndexAffectingChanges(oldProp, newProp) {
			return true
		}

		// Delete compared properties from the map.
		delete(oldPropMap, newProp.Name)
	}

	// Removed old properties can also affect indexes.
	if len(oldPropMap) > 0 {
		return true
	}

	return false
}

// Update object types.
func (ots *objectTypeService) UpdateObjectType(ctx context.Context, tx *sql.Tx, objectType *interfaces.ObjectType, strictMode bool) error {

	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Update object type")
	defer span.End()

	// Check whether the user ID can modify the business knowledge network.
	err := ots.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: interfaces.RESOURCE_TYPE_KN,
		ID:   objectType.KNID,
	}, []string{interfaces.OPERATION_TYPE_MODIFY})
	if err != nil {
		return err
	}

	if strictMode {
		if err := ots.validateObjectTypeStrictExternalDeps(ctx, objectType); err != nil {
			return err
		}
	}

	accountInfo := interfaces.AccountInfo{}
	if ctx.Value(interfaces.ACCOUNT_INFO_KEY) != nil {
		accountInfo = ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	}
	objectType.Updater = accountInfo

	currentTime := time.Now().UnixMilli() // Object type update_time uses an integer type.
	objectType.UpdateTime = currentTime

	bknObj := logics.ToBKNObjectType(objectType)
	objectType.BKNRawContent = bknsdk.SerializeObjectType(bknObj)

	if tx == nil {
		// 0. Begin the transaction.
		tx, err = ots.db.Begin()
		if err != nil {
			otellog.LogError(ctx, "Begin transaction error", err)

			return rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_ObjectType_InternalError_BeginTransactionFailed).
				WithErrorDetails(err.Error())
		}
		// 0.1 On failure.
		defer func() {
			switch err {
			case nil:
				// Commit the transaction.
				err = tx.Commit()
				if err != nil {
					otellog.LogError(ctx, "UpdateObjectType Transaction Commit Failed", err)
					return
				}
				otellog.LogDebug(ctx, fmt.Sprintf("UpdateObjectType Transaction Commit Success: %s", objectType.OTName))
			default:
				rollbackErr := tx.Rollback()
				if rollbackErr != nil {
					otellog.LogError(ctx, "UpdateObjectType Transaction Rollback Error", rollbackErr)
				}
			}
		}()
	}

	// Get old object type data to compare data property changes.
	oldObjectType, err := ots.ota.GetObjectTypeByID(ctx, tx, objectType.KNID, objectType.Branch, objectType.OTID)
	if err != nil {
		otellog.LogError(ctx, "GetObjectTypeByID error", err)

		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ObjectType_InternalError_GetObjectTypeByIDFailed).
			WithErrorDetails(err.Error())
	}

	// Detect whether data property changes affect indexes.
	if oldObjectType != nil && hasAnyDataPropertyIndexAffectingChanges(oldObjectType.DataProperties, objectType.DataProperties) {
		// Mark index status unavailable.
		otStatus := *oldObjectType.Status
		otStatus.IndexAvailable = false
		otStatus.UpdateTime = currentTime
		err = ots.ota.UpdateObjectTypeStatus(ctx, tx, objectType.KNID, objectType.Branch, objectType.OTID, otStatus)
		if err != nil {
			otellog.LogError(ctx, "UpdateObjectTypeStatus error", err)

			return rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_ObjectType_InternalError).
				WithErrorDetails(invalidParameterDetail(ctx, "IndexStatusUpdateFailed", nil))
		}

		otellog.LogInfo(ctx, fmt.Sprintf("数据属性变化影响索引，已将对象类[%s]的索引状态设置为不可用", objectType.OTID))
	}

	// Update model information.
	err = ots.ota.UpdateObjectType(ctx, tx, objectType)
	if err != nil {
		logger.Errorf("UpdateObjectType error: %s", err.Error())
		span.SetStatus(codes.Error, "修改对象类失败")

		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ObjectType_InternalError).
			WithErrorDetails(err.Error())
	}

	// 4. Synchronize group relationships by full replacement.
	if err := ots.syncObjectGroups(ctx, tx, *objectType, currentTime, strictMode); err != nil {
		return err
	}

	err = ots.InsertDatasetData(ctx, []*interfaces.ObjectType{objectType})
	if err != nil {
		logger.Errorf("InsertDatasetData error: %s", err.Error())
		span.SetStatus(codes.Error, "对象类索引写入失败")

		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ObjectType_InternalError_InsertOpenSearchDataFailed).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

// Update object type data properties.
func (ots *objectTypeService) UpdateDataProperties(ctx context.Context,
	objectType *interfaces.ObjectType, dataProperties []*interfaces.DataProperty, strictMode bool) error {

	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Update object type")
	defer span.End()

	// Check whether the user ID can modify the business knowledge network.
	err := ots.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: interfaces.RESOURCE_TYPE_KN,
		ID:   objectType.KNID,
	}, []string{interfaces.OPERATION_TYPE_MODIFY})
	if err != nil {
		return err
	}

	// When strictMode is true, validate embedding small model for any submitted property with vector index enabled.
	if strictMode {
		for _, prop := range dataProperties {
			if prop.IndexConfig != nil && prop.IndexConfig.VectorConfig.Enabled {
				model, err := ots.mfs.GetModelByID(ctx, prop.IndexConfig.VectorConfig.ModelID)
				if err != nil {
					return rest.NewHTTPError(ctx, http.StatusInternalServerError,
						berrors.BknBackend_ObjectType_InternalError_GetSmallModelByIDFailed).
						WithErrorDetails(err.Error())
				}
				if model == nil {
					return rest.NewHTTPError(ctx, http.StatusNotFound,
						berrors.BknBackend_ObjectType_SmallModelNotFound).
						WithErrorDetails(invalidParameterDetail(ctx, "SmallModelNotFound", map[string]any{"modelID": prop.IndexConfig.VectorConfig.ModelID}))
				}
				if model.ModelType != interfaces.SMALL_MODEL_TYPE_EMBEDDING {
					return rest.NewHTTPError(ctx, http.StatusBadRequest,
						berrors.BknBackend_ObjectType_InvalidParameter_SmallModel).
						WithErrorDetails(invalidParameterDetail(ctx, "SmallModelTypeInvalid", map[string]any{"modelType": model.ModelType, "expectedType": interfaces.SMALL_MODEL_TYPE_EMBEDDING}))
				}
				if model.EmbeddingDim == 0 || model.BatchSize == 0 || model.MaxTokens == 0 {
					return rest.NewHTTPError(ctx, http.StatusBadRequest,
						berrors.BknBackend_ObjectType_InvalidParameter_SmallModel).
						WithErrorDetails(invalidParameterDetail(ctx, "SmallModelConfigInvalid", map[string]any{"modelID": model.ModelID}))
				}
			}
		}
	}

	accountInfo := interfaces.AccountInfo{}
	if ctx.Value(interfaces.ACCOUNT_INFO_KEY) != nil {
		accountInfo = ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	}
	objectType.Updater = accountInfo
	currentTime := time.Now().UnixMilli() // Object type update_time uses an integer type.
	objectType.UpdateTime = currentTime

	// Deep-copy old data properties for later comparison.
	oldDataPropertiesBytes, err := sonic.Marshal(objectType.DataProperties)
	if err != nil {
		otellog.LogError(ctx, "Failed to marshal old DataProperties, err", err)

		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ObjectType_InternalError).
			WithErrorDetails(invalidParameterDetail(ctx, "OldDataPropertiesMarshalFailed", nil))
	}

	var oldDataProperties []*interfaces.DataProperty
	err = sonic.Unmarshal(oldDataPropertiesBytes, &oldDataProperties)
	if err != nil {
		otellog.LogError(ctx, "Failed to unmarshal old DataProperties, err", err)

		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ObjectType_InternalError).
			WithErrorDetails(invalidParameterDetail(ctx, "OldDataPropertiesUnmarshalFailed", nil))
	}

	propMap := map[string]int{}
	for idx, prop := range objectType.DataProperties {
		propMap[prop.Name] = idx
	}
	for _, prop := range dataProperties {
		if idx, ok := propMap[prop.Name]; ok {
			objectType.DataProperties[idx] = prop // Update an existing data property.
		} else {
			objectType.DataProperties = append(objectType.DataProperties, prop) // Add a new data property.
		}
	}

	bknObj := logics.ToBKNObjectType(objectType)
	objectType.BKNRawContent = bknsdk.SerializeObjectType(bknObj)

	// 0. Begin the transaction.
	var tx *sql.Tx
	tx, err = ots.db.Begin()
	if err != nil {
		otellog.LogError(ctx, "Begin transaction error", err)

		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ObjectType_InternalError_BeginTransactionFailed).
			WithErrorDetails(err.Error())
	}
	// 0.1 On failure.
	defer func() {
		switch err {
		case nil:
			// Commit the transaction.
			err = tx.Commit()
			if err != nil {
				otellog.LogError(ctx, "UpdateObjectType Transaction Commit Failed", err)
				return
			}
			otellog.LogDebug(ctx, fmt.Sprintf("UpdateObjectType Transaction Commit Success: %s", objectType.OTName))
		default:
			rollbackErr := tx.Rollback()
			if rollbackErr != nil {
				otellog.LogError(ctx, "UpdateObjectType Transaction Rollback Error", rollbackErr)
			}
		}
	}()

	// Detect whether data property changes affect indexes.
	if hasAnyDataPropertyIndexAffectingChanges(oldDataProperties, objectType.DataProperties) {
		// Mark index status unavailable.
		if objectType.Status != nil {
			otStatus := *objectType.Status
			otStatus.IndexAvailable = false
			otStatus.UpdateTime = currentTime
			// UpdateDataProperties has no tx parameter and manages its transaction internally.
			// Use db.Exec directly to preserve consistency.
			err = ots.ota.UpdateObjectTypeStatus(ctx, tx, objectType.KNID, objectType.Branch, objectType.OTID, otStatus)
			if err != nil {
				otellog.LogError(ctx, "UpdateObjectTypeStatus error", err)

				return rest.NewHTTPError(ctx, http.StatusInternalServerError,
					berrors.BknBackend_ObjectType_InternalError).
					WithErrorDetails(invalidParameterDetail(ctx, "IndexStatusUpdateFailed", nil))
			}

			otellog.LogInfo(ctx, fmt.Sprintf("数据属性变化影响索引，已将对象类[%s]的索引状态设置为不可用", objectType.OTID))
		}
	}

	// Update model information.
	err = ots.ota.UpdateDataProperties(ctx, tx, objectType)
	if err != nil {
		logger.Errorf("UpdateObjectType error: %s", err.Error())
		span.SetStatus(codes.Error, "修改对象类失败")

		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ObjectType_InternalError).
			WithErrorDetails(err.Error())
	}

	err = ots.InsertDatasetData(ctx, []*interfaces.ObjectType{objectType})
	if err != nil {
		logger.Errorf("InsertDatasetData error: %s", err.Error())
		span.SetStatus(codes.Error, "对象类索引写入失败")

		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ObjectType_InternalError_InsertOpenSearchDataFailed).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

func (ots *objectTypeService) DeleteObjectTypesByIDs(ctx context.Context, tx *sql.Tx, knID string, branch string, otIDs []string) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Delete object types")
	defer span.End()

	// Check whether the user ID can modify the business knowledge network.
	err := ots.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: interfaces.RESOURCE_TYPE_KN,
		ID:   knID,
	}, []string{interfaces.OPERATION_TYPE_MODIFY})
	if err != nil {
		return err
	}

	if tx == nil {
		// 0. Begin the transaction.
		tx, err = ots.db.Begin()
		if err != nil {
			otellog.LogError(ctx, "Begin transaction error", err)

			return rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_ObjectType_InternalError_BeginTransactionFailed).
				WithErrorDetails(err.Error())
		}
		// 0.1 On failure.
		defer func() {
			switch err {
			case nil:
				// Commit the transaction.
				err = tx.Commit()
				if err != nil {
					otellog.LogError(ctx, "DeleteObjectTypes Transaction Commit Failed", err)
				}
				otellog.LogDebug(ctx, fmt.Sprintf("DeleteObjectTypes Transaction Commit Success: kn_id:%s,ot_ids:%v", knID, otIDs))
			default:
				rollbackErr := tx.Rollback()
				if rollbackErr != nil {
					otellog.LogError(ctx, "DeleteObjectTypes Transaction Rollback Error", rollbackErr)
				}
			}
		}()
	}

	// Delete object types.
	rowsAffect, err := ots.ota.DeleteObjectTypesByIDs(ctx, tx, knID, branch, otIDs)
	if err != nil {
		logger.Errorf("DeleteObjectTypes error: %s", err.Error())
		span.SetStatus(codes.Error, "删除对象类失败")

		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ObjectType_InternalError).WithErrorDetails(err.Error())
	}

	logger.Infof("DeleteObjectTypes: Rows affected is %v, request delete ObjectTypeIDs is %v!", rowsAffect, len(otIDs))
	if rowsAffect != int64(len(otIDs)) {
		otellog.LogWarn(ctx, fmt.Sprintf("Delete object types number %v not equal requerst object types number %v!", rowsAffect, len(otIDs)))
	}

	rowsAffect, err = ots.ota.DeleteObjectTypeStatusByIDs(ctx, tx, knID, branch, otIDs)
	if err != nil {
		logger.Errorf("DeleteObjectTypeStatusByIDs error: %s", err.Error())
		span.SetStatus(codes.Error, "删除对象类状态失败")

		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ObjectType_InternalError).WithErrorDetails(err.Error())
	}

	// Record the deleted count in an info log.
	logger.Infof("DeleteObjectTypeStatusByIDs success, the kn_id is [%s], branch is [%s], ot_ids is [%v], rowsAffect is [%d]",
		knID, branch, otIDs, rowsAffect)

	for _, otID := range otIDs {
		docid := interfaces.GenerateConceptDocuemtnID(knID, interfaces.MODULE_TYPE_OBJECT_TYPE, otID, branch)
		err = ots.vba.DeleteDatasetDocumentByID(ctx, interfaces.BKN_DATASET_ID, docid)
		if err != nil {
			logger.Errorf("DeleteDatasetDocumentByID error: %s", err.Error())
			span.SetStatus(codes.Error, "删除对象类概念索引失败")

			// Vega returns ordinary errors. Normalize them to HTTPError before propagating.
			// Otherwise handler type assertions panic before headers are written and the gateway reports 502.
			var httpErr *rest.HTTPError
			if errors.As(err, &httpErr) {
				return httpErr
			}
			return rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_ObjectType_InternalError).WithErrorDetails(err.Error())
		}
	}

	// Delete this object relations from the concept-to-group relation table.
	// Delete object type-to-group bindings.
	rowsAffect, err = ots.cga.DeleteObjectTypesFromGroup(ctx, tx, interfaces.ConceptGroupRelationsQueryParams{
		KNID:        knID,
		Branch:      branch,
		ConceptType: interfaces.MODULE_TYPE_OBJECT_TYPE,
		OTIDs:       otIDs,
	})
	if err != nil {
		errStr := fmt.Sprintf("DeleteObjectTypesFromGroup failed, the kn_id is [%s], branch is [%s], ot_ids is [%v], error is [%s]",
			knID, "branch", otIDs, err.Error())
		logger.Errorf(errStr)

		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ObjectType_InternalError).
			WithErrorDetails(errStr)
	}
	// Record the deleted count in an info log.
	logger.Infof("DeleteObjectTypesFromGroup success, the kn_id is [%s], branch is [%s], ot_ids is [%v], rowsAffect is [%d]",
		knID, branch, otIDs, rowsAffect)

	span.SetStatus(codes.Ok, "")
	return nil
}

// Internal method. Deletes object types and status without permission checks; tx is required.
func (ots *objectTypeService) DeleteObjectTypesByKnID(ctx context.Context, tx *sql.Tx, knID string, branch string) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Delete object types")
	defer span.End()

	if tx == nil {
		otellog.LogError(ctx, "missing transaction", nil)
		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ObjectType_InternalError_BeginTransactionFailed).
			WithErrorDetails("missing transaction")
	}

	// Delete object types.
	rowsAffect, err := ots.ota.DeleteObjectTypesByKnID(ctx, tx, knID, branch)
	if err != nil {
		logger.Errorf("DeleteObjectTypes error: %s", err.Error())
		span.SetStatus(codes.Error, "删除对象类失败")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ObjectType_InternalError).WithErrorDetails(err.Error())
	}

	logger.Infof("DeleteObjectTypes: Rows affected is %v!", rowsAffect)
	rowsAffect, err = ots.ota.DeleteObjectTypeStatusByKnID(ctx, tx, knID, branch)
	if err != nil {
		logger.Errorf("DeleteObjectTypeStatusByIDs error: %s", err.Error())
		span.SetStatus(codes.Error, "删除对象类状态失败")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ObjectType_InternalError).WithErrorDetails(err.Error())
	}

	// Record the deleted count in an info log.
	logger.Infof("DeleteObjectTypesByKnID success, the kn_id is [%s], branch is [%s], rowsAffect is [%d]",
		knID, branch, rowsAffect)
	span.SetStatus(codes.Ok, "")
	return nil
}

func (ots *objectTypeService) handleObjectTypeImportMode(ctx context.Context, mode string,
	objectTypes []*interfaces.ObjectType) ([]*interfaces.ObjectType, []*interfaces.ObjectType, error) {

	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "object type import mode logic")
	defer span.End()

	creates := []*interfaces.ObjectType{}
	updates := []*interfaces.ObjectType{}

	// 3. When the submitted model ID is not empty, validate conflicts with existing model IDs.
	for _, objectType := range objectTypes {
		creates = append(creates, objectType)
		idExist := false
		_, idExist, err := ots.CheckObjectTypeExistByID(ctx, objectType.KNID, objectType.Branch, objectType.OTID)
		if err != nil {
			return creates, updates, err
		}

		// Validate conflicts between the request and existing model names.
		existID, nameExist, err := ots.CheckObjectTypeExistByName(ctx, objectType.KNID, objectType.Branch, objectType.OTName)
		if err != nil {
			return creates, updates, err
		}

		// Handle mode: ignore removes it from results, overwrite updates it, and normal returns an error.
		if idExist || nameExist {
			switch mode {
			case interfaces.ImportMode_Normal:
				if idExist {
					errDetails := fmt.Sprintf("The object type with id [%s] already exists!", objectType.OTID)
					logger.Error(errDetails)
					span.SetStatus(codes.Error, errDetails)
					return creates, updates, rest.NewHTTPError(ctx, http.StatusBadRequest,
						berrors.BknBackend_ObjectType_ObjectTypeIDExisted).
						WithErrorDetails(errDetails)
				}

				if nameExist {
					errDetails := fmt.Sprintf("object type name '%s' already exists", objectType.OTName)
					logger.Error(errDetails)
					span.SetStatus(codes.Error, errDetails)
					return creates, updates, rest.NewHTTPError(ctx, http.StatusForbidden,
						berrors.BknBackend_ObjectType_ObjectTypeNameExisted).
						WithDescription(map[string]any{"name": objectType.OTName}).
						WithErrorDetails(errDetails)
				}

			case interfaces.ImportMode_Ignore:
				// Skip duplicates.
				// Remove from the create array.
				creates = creates[:len(creates)-1]
			case interfaces.ImportMode_Overwrite:
				if idExist && nameExist {
					// Return an error when both ID and name exist but the named view has a different ID.
					if existID != objectType.OTID {
						errDetails := fmt.Sprintf("ObjectType ID '%s' and name '%s' already exist, but the exist object type id is '%s'",
							objectType.OTID, objectType.OTName, existID)
						logger.Error(errDetails)
						span.SetStatus(codes.Error, errDetails)
						return creates, updates, rest.NewHTTPError(ctx, http.StatusForbidden,
							berrors.BknBackend_ObjectType_ObjectTypeNameExisted).
							WithErrorDetails(errDetails)
					} else {
						// Overwrite when ID, name, and metric name exist and the named model ID matches the current model ID.
						// Remove from the create array and add to the update array.
						creates = creates[:len(creates)-1]
						updates = append(updates, objectType)
					}
				}

				// Overwrite when the ID exists and the name does not.
				if idExist && !nameExist {
					// Remove from the create array and add to the update array.
					creates = creates[:len(creates)-1]
					updates = append(updates, objectType)
				}

				// Return an error when the ID does not exist but the name exists.
				if !idExist && nameExist {
					errDetails := fmt.Sprintf("ObjectType ID '%s' does not exist, but name '%s' already exists",
						objectType.OTID, objectType.OTName)
					logger.Error(errDetails)
					span.SetStatus(codes.Error, errDetails)
					return creates, updates, rest.NewHTTPError(ctx, http.StatusForbidden,
						berrors.BknBackend_ObjectType_ObjectTypeNameExisted).
						WithErrorDetails(errDetails)
				}

				// Create when ID, name, and metric name do not exist.
				// if !idExist && !nameExist {}
			}
		}
	}
	span.SetStatus(codes.Ok, "")
	return creates, updates, nil
}

// Internal use without permission checks.
func (ots *objectTypeService) GetObjectTypesMapByIDs(ctx context.Context, knID string,
	branch string, otIDs []string, needPropMap bool) (map[string]*interfaces.ObjectType, error) {
	// Get object types.
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, fmt.Sprintf("查询对象类[%v]信息", otIDs))
	defer span.End()

	// Check whether the user ID can modify the business knowledge network.
	err := ots.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: interfaces.RESOURCE_TYPE_KN,
		ID:   knID,
	}, []string{interfaces.OPERATION_TYPE_VIEW_DETAIL})
	if err != nil {
		return map[string]*interfaces.ObjectType{}, err
	}

	// De-duplicate IDs before querying.
	otIDs = common.DuplicateSlice(otIDs)

	// Get basic model information.
	objectTypeArr, err := ots.ota.GetObjectTypesByIDs(ctx, nil, knID, branch, otIDs)
	if err != nil {
		logger.Errorf("GetObjectTypesByObjectTypeIDs error: %s", err.Error())
		span.SetStatus(codes.Error, fmt.Sprintf("Get object type[%v] error: %v", otIDs, err))
		return map[string]*interfaces.ObjectType{}, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ObjectType_InternalError_GetObjectTypesByIDsFailed).
			WithErrorDetails(err.Error())
	}

	objectTypeMap := map[string]*interfaces.ObjectType{}
	for _, object := range objectTypeArr {
		if needPropMap {
			propMap := map[string]string{}
			for _, prop := range object.DataProperties {
				propMap[prop.Name] = prop.DisplayName
			}
			object.PropertyMap = propMap
		}
		objectTypeMap[object.OTID] = object
	}

	span.SetStatus(codes.Ok, "")
	return objectTypeMap, nil
}

func (ots *objectTypeService) InsertDatasetData(ctx context.Context, objectTypes []*interfaces.ObjectType) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "对象类索引写入")
	defer span.End()

	if len(objectTypes) == 0 {
		return nil
	}

	if ots.appSetting.ServerSetting.DefaultSmallModelEnabled {
		words := []string{}
		for _, objectType := range objectTypes {
			arr := []string{objectType.OTName}
			arr = append(arr, objectType.Tags...)
			arr = append(arr, objectType.Comment, objectType.BKNRawContent)
			word := strings.Join(arr, "\n")
			words = append(words, word)
		}

		dftModel, err := ots.mfs.GetDefaultModel(ctx)
		if err != nil {
			logger.Errorf("GetDefaultModel error: %s", err.Error())
			span.SetStatus(codes.Error, "获取默认模型失败")
			return err
		}
		vectors, err := ots.mfs.GetVector(ctx, dftModel, words)
		if err != nil {
			logger.Errorf("GetVector error: %s", err.Error())
			span.SetStatus(codes.Error, "获取业务知识网络向量失败")
			return err
		}

		if len(vectors) != len(objectTypes) {
			logger.Errorf("GetVector error: expect vectors num is [%d], actual vectors num is [%d]", len(objectTypes), len(vectors))
			span.SetStatus(codes.Error, "获取业务知识网络向量失败")
			return fmt.Errorf("GetVector error: expect vectors num is [%d], actual vectors num is [%d]", len(objectTypes), len(vectors))
		}

		for i, objectType := range objectTypes {
			objectType.Vector = vectors[i].Vector
		}
	}

	documents := []map[string]any{}
	for _, objectType := range objectTypes {
		docid := interfaces.GenerateConceptDocuemtnID(objectType.KNID, interfaces.MODULE_TYPE_OBJECT_TYPE,
			objectType.OTID, objectType.Branch)
		objectType.ModuleType = interfaces.MODULE_TYPE_OBJECT_TYPE

		// Convert to map for dataset
		docBytes, err := sonic.Marshal(objectType)
		if err != nil {
			logger.Errorf("Failed to marshal ObjectType: %s", err.Error())
			span.SetStatus(codes.Error, "序列化对象类失败")
			return err
		}

		var doc map[string]any
		if err := sonic.Unmarshal(docBytes, &doc); err != nil {
			logger.Errorf("Failed to unmarshal ObjectType: %s", err.Error())
			span.SetStatus(codes.Error, "反序列化对象类失败")
			return err
		}

		// Serialize logic_properties[].parameters to JSON string
		if logicProps, ok := doc["logic_properties"].([]any); ok {
			for _, lp := range logicProps {
				if lpMap, ok := lp.(map[string]any); ok {
					if params, exists := lpMap["parameters"]; exists {
						paramsBytes, err := sonic.Marshal(params)
						if err != nil {
							logger.Errorf("Failed to marshal logic_properties parameters: %s", err.Error())
							span.SetStatus(codes.Error, "序列化逻辑属性参数失败")
							return err
						}
						lpMap["parameters"] = string(paramsBytes)
					}
				}
			}
		}

		// Set document ID
		doc["_id"] = docid
		documents = append(documents, doc)
	}

	err := ots.vba.WriteDatasetDocuments(ctx, interfaces.BKN_DATASET_ID, documents)
	if err != nil {
		logger.Errorf("WriteDatasetDocuments error: %s", err.Error())
		span.SetStatus(codes.Error, "对象类概念索引写入失败")
		return err
	}

	return nil
}

// type vectorFunc func(ctx context.Context, words []string) ([]cond.VectorResp, error)

func (ots *objectTypeService) SearchObjectTypes(ctx context.Context,
	query *interfaces.ConceptsQuery) (interfaces.ObjectTypes, error) {

	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "业务知识网络对象类检索")
	defer span.End()

	response := interfaces.ObjectTypes{}
	var err error

	// Check whether the user ID can view the business knowledge network.
	err = ots.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: interfaces.RESOURCE_TYPE_KN,
		ID:   query.KNID,
	}, []string{interfaces.OPERATION_TYPE_VIEW_DETAIL})
	if err != nil {
		return response, err
	}

	// Convert conditions to dataset filter conditions.
	var filterCondition map[string]any
	if query.ActualCondition != nil {
		filterCondition, err = cond.ConvertCondCfgToFilterCondition(ctx, query.ActualCondition,
			interfaces.CONCPET_QUERY_FIELD,
			func(ctx context.Context, word string) ([]*cond.VectorResp, error) {
				if !ots.appSetting.ServerSetting.DefaultSmallModelEnabled {
					err = errors.New(cond.DEFAULT_SMALL_MODEL_ENABLED_FALSE_ERROR)
					span.SetStatus(codes.Error, err.Error())
					return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
						berrors.BknBackend_ObjectType_InternalError).
						WithErrorDetails(err.Error())
				}
				dftModel, err := ots.mfs.GetDefaultModel(ctx)
				if err != nil {
					logger.Errorf("GetDefaultModel error: %s", err.Error())
					span.SetStatus(codes.Error, "获取默认模型失败")
					return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
						berrors.BknBackend_ObjectType_InternalError).
						WithErrorDetails(err.Error())
				}
				result, err := ots.mfs.GetVector(ctx, dftModel, []string{word})
				if err != nil {
					logger.Errorf("GetVector error: %s", err.Error())
					span.SetStatus(codes.Error, "获取业务知识网络向量失败")
					return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
						berrors.BknBackend_ObjectType_InternalError).
						WithErrorDetails(err.Error())
				}
				return result, nil
			})
		if err != nil {
			logger.Errorf("convert object type condition to filter condition failed: %v", err)
			return response, rest.NewHTTPError(ctx, http.StatusBadRequest,
				berrors.BknBackend_ObjectType_InvalidParameter_ConceptCondition).
				WithErrorDetails(i18n.Translate(rest.GetLanguageByCtx(ctx), "BknBackend.Validation.Detail.ConditionDecodeFailed", nil))
		}
	}

	// 1. Get object types in the groups.
	otIDMap := map[string]bool{} // Object type IDs in the groups
	otIDs := []string{}          // Object types can overlap between groups, so de-duplicate object type IDs.
	if len(query.ConceptGroups) > 0 {
		// Validate groups by retrieving them by ID.
		cgCnt, err := ots.cga.GetConceptGroupsTotal(ctx, interfaces.ConceptGroupsQueryParams{
			KNID:   query.KNID,
			Branch: query.Branch,
			CGIDs:  query.ConceptGroups,
		})
		if err != nil {
			logger.Errorf("GetConceptGroupsTotal in knowledge network[%s] error: %s", query.KNID, err.Error())
			span.SetStatus(codes.Error, fmt.Sprintf("GetConceptGroupsTotal in knowledge network[%s], error: %v", query.KNID, err))

			return response, rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_ObjectType_InternalError).WithErrorDetails(err.Error())
		}
		if cgCnt == 0 {
			errStr := fmt.Sprintf("all concept group not found, expect concept group nums is [%d], actual concept group num is [%d]",
				cgCnt, len(query.ConceptGroups))
			logger.Errorf(errStr)

			// Return 404 when all requested concept groups are missing.
			return response, rest.NewHTTPError(ctx, http.StatusNotFound,
				berrors.BknBackend_ConceptGroup_ConceptGroupNotFound).
				WithErrorDetails(errStr)
		}

		// Find object type IDs in requested groups within the current business knowledge network.
		otIDArr, err := ots.cga.GetConceptIDsByConceptGroupIDs(ctx, query.KNID,
			query.Branch, query.ConceptGroups, interfaces.MODULE_TYPE_OBJECT_TYPE)
		if err != nil {
			errStr := fmt.Sprintf("GetConceptIDsByConceptGroupIDs failed, kn_id:[%s],branch:[%s],cg_ids:[%v], error: %v",
				query.KNID, query.Branch, query.ConceptGroups, err)
			logger.Errorf(errStr)
			span.SetStatus(codes.Error, errStr)
			span.End()

			return response, rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_ObjectType_InternalError).WithErrorDetails(err.Error())
		}

		// Return empty when the concept groups contain no object types.
		if len(otIDArr) == 0 {
			return response, nil
		}

		for _, otID := range otIDArr {
			if !otIDMap[otID] {
				otIDMap[otID] = true
				otIDs = append(otIDs, otID)
			}
		}
	}

	// Decide whether to query the total based on NeedTotal.
	if query.NeedTotal {
		if len(otIDMap) == 0 {
			// Query the total count.
			params := &interfaces.ResourceDataQueryParams{
				FilterCondition: filterCondition,
				Paging: interfaces.ResourceDataPagingRequest{
					Mode:  "single",
					Limit: 1, // Query one entry to obtain the total count.
				},
				NeedTotal: true,
			}
			datasetResp, err := ots.vba.QueryResourceData(ctx, interfaces.BKN_DATASET_ID, params)
			if err != nil {
				logger.Errorf("QueryDatasetData error: %s", err.Error())
				span.SetStatus(codes.Error, "业务知识网络对象类检索查询总数失败")
				return response, rest.NewHTTPError(ctx, http.StatusInternalServerError,
					berrors.BknBackend_ObjectType_InternalError).
					WithErrorDetails(err.Error())
			}
			response.TotalCount = datasetResp.TotalCount
		} else {
			// Query the matching total within specified groups.
			total, err := ots.GetTotalWithLargeOTIDs(ctx, filterCondition, otIDs)
			if err != nil {
				return response, err
			}
			response.TotalCount = total
		}
	}

	// 4. Iterate until enough entries are collected or no more data exists.
	objectTypes := []*interfaces.ObjectType{}
	var totalFilteredCount int64 = 0
	sort := query.Sort
	if len(sort) == 0 {
		sort = []*interfaces.SortParams{{Field: "id", Direction: "asc"}}
	}
	cursor := query.Cursor
	var nextCursor *string
	limit := query.Limit
	if limit == 0 {
		limit = interfaces.ConceptQueryLimit
	}

	for {
		paging := interfaces.ResourceDataPagingRequest{Mode: "cursor", Limit: limit}
		if cursor != "" {
			paging = interfaces.ResourceDataPagingRequest{Cursor: cursor}
		}
		// Call the dataset query.
		params := &interfaces.ResourceDataQueryParams{
			FilterCondition: filterCondition,
			Paging:          paging,
			NeedTotal:       false,
			Sort:            sort,
		}
		datasetResp, err := ots.vba.QueryResourceData(ctx, interfaces.BKN_DATASET_ID, params)
		if err != nil {
			logger.Errorf("QueryResourceData error: %s", err.Error())
			span.SetStatus(codes.Error, "业务知识网络对象类检索查询失败")
			return response, rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_ObjectType_InternalError).
				WithErrorDetails(err.Error())
		}

		// Stop when no data remains.
		if len(datasetResp.Entries) == 0 {
			break
		}

		// 5. Process query results.
		for _, entry := range datasetResp.Entries {
			// Deserialize logic_properties[].parameters from JSON string
			if logicProps, exists := entry["logic_properties"]; exists {
				if logicPropsArr, ok := logicProps.([]any); ok {
					for _, lp := range logicPropsArr {
						if lpMap, ok := lp.(map[string]any); ok {
							if paramsStr, exists := lpMap["parameters"]; exists {
								if paramsStrStr, ok := paramsStr.(string); ok && paramsStrStr != "" {
									var params []interfaces.Parameter
									if err := sonic.Unmarshal([]byte(paramsStrStr), &params); err != nil {
										logger.Errorf("Failed to unmarshal object_type logic_properties parameters: %s", err.Error())
										return response, rest.NewHTTPError(ctx, http.StatusBadRequest,
											berrors.BknBackend_InternalError_UnMarshalDataFailed).
											WithErrorDetails(fmt.Sprintf("failed to Unmarshal logic_properties parameters, %s", err.Error()))
									}
									lpMap["parameters"] = params
								}
							}
						}
					}
				}
			}

			// Convert to an object type struct.
			jsonByte, err := json.Marshal(entry)
			if err != nil {
				return response, rest.NewHTTPError(ctx, http.StatusBadRequest,
					berrors.BknBackend_InternalError_MarshalDataFailed).
					WithErrorDetails(fmt.Sprintf("failed to Marshal dataset entry, %s", err.Error()))
			}
			var objectType interfaces.ObjectType
			err = json.Unmarshal(jsonByte, &objectType)
			if err != nil {
				return response, rest.NewHTTPError(ctx, http.StatusBadRequest,
					berrors.BknBackend_InternalError_UnMarshalDataFailed).
					WithErrorDetails(fmt.Sprintf("failed to Unmarshal dataset entry to Object Type, %s", err.Error()))
			}

			// Add the object type when no group is specified or it belongs to the group.
			if len(otIDMap) == 0 || otIDMap[objectType.OTID] {
				// Process the data source and operators.
				err = ots.processObjectTypeDetails(ctx, &objectType)
				if err != nil {
					return response, err
				}
				// Extract _score when present.
				if scoreVal, ok := entry["_score"]; ok {
					if scoreFloat, ok := scoreVal.(float64); ok {
						score := float64(scoreFloat)
						objectType.Score = &score
					}
				}
				objectType.Vector = nil

				objectTypes = append(objectTypes, &objectType)
				totalFilteredCount++

				// Stop when enough entries have been collected.
				if len(objectTypes) >= query.Limit && query.Limit > 0 {
					break
				}
			}
		}

		nextCursor = nil
		if datasetResp.Paging != nil {
			nextCursor = datasetResp.Paging.NextCursor
		}

		if query.Limit > 0 && len(objectTypes) >= query.Limit {
			break
		}
		if nextCursor == nil {
			break
		}
		cursor = *nextCursor
	}

	response.Entries = objectTypes
	response.NextCursor = nextCursor
	span.SetStatus(codes.Ok, "")
	return response, nil
}

// Extracted helper for processing object type details.
func (ots *objectTypeService) processObjectTypeDetails(ctx context.Context, objectType *interfaces.ObjectType) error {

	// Retrieve views or Vega resources to assemble operations. Assembly is unnecessary because they are persisted on save.
	if objectType.DataSource != nil && objectType.DataSource.ID != "" {
		switch objectType.DataSource.Type {
		case interfaces.DATA_SOURCE_TYPE_RESOURCE:
			res, err := ots.vba.GetResourceByID(ctx, objectType.DataSource.ID)
			if err != nil || res == nil {
				otellog.LogWarn(ctx, fmt.Sprintf("Object type [%s]'s vega Resource %s not found, error: %v",
					objectType.OTID, objectType.DataSource.ID, err))
			} else {
				objectType.DataSource.Name = res.Name
				fieldsMap := logics.VegaResourceSchemaToFieldsMap(res)
				indexCaps := logics.VegaResourceIndexCaps(res)
				dslView := &interfaces.DataView{QueryType: interfaces.VIEW_QueryType_DSL}
				for j, prop := range objectType.DataProperties {
					if prop.MappedField != nil {
						if field, exists := fieldsMap[prop.MappedField.Name]; exists {
							objectType.DataProperties[j].MappedField.DisplayName = field.DisplayName
							objectType.DataProperties[j].MappedField.Type = field.Type
						}
					}
					ops := ots.processConditionOperations(objectType, prop, dslView)
					if prop.MappedField != nil {
						ops = applyIndexCapOps(ops, indexCaps[prop.MappedField.Name])
					}
					objectType.DataProperties[j].ConditionOperations = ops
				}
			}
		}

		// Resolve logical property resource IDs to names.
		for j, logicProp := range objectType.LogicProperties {
			if logicProp.DataSource != nil {
				switch logicProp.DataSource.Type {
				case interfaces.LOGIC_PROPERTY_TYPE_METRIC:
					if logicProp.DataSource.ID != "" {
						ots.enrichLogicMetricProperty(ctx, objectType, logicProp, j)
					}
				}
				// TODO: move dynamic parameters to a dedicated field for unified retrieval and update search accordingly.
			}
		}
	}

	return nil
}

func (ots *objectTypeService) GetTotal(ctx context.Context, filterCondition map[string]any) (total int64, err error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "logic layer: search object type total ")
	defer span.End()

	params := &interfaces.ResourceDataQueryParams{
		FilterCondition: filterCondition,
		Paging: interfaces.ResourceDataPagingRequest{
			Mode:  "single",
			Limit: 1, // Query one entry to obtain the total count.
		},
		NeedTotal: true,
	}
	datasetResp, err := ots.vba.QueryResourceData(ctx, interfaces.BKN_DATASET_ID, params)
	if err != nil {
		span.SetStatus(codes.Error, "Search total documents count failed")
		return total, rest.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_ObjectType_InternalError).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	if datasetResp == nil {
		return 0, nil
	}
	return datasetResp.TotalCount, nil
}

// Internal call without permission checks.
func (ots *objectTypeService) GetObjectTypeIDsByKnID(ctx context.Context,
	knID string, branch string) ([]string, error) {
	// Get object types.
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, fmt.Sprintf("按kn_id[%s]获取对象类IDs", knID))
	defer span.End()

	// Get basic object type information.
	otIDs, err := ots.ota.GetObjectTypeIDsByKnID(ctx, knID, branch)
	if err != nil {
		logger.Errorf("GetObjectTypeIDsByKnID error: %s", err.Error())
		span.SetStatus(codes.Error, fmt.Sprintf("Get object type ids by kn_id[%s] error: %v", knID, err))

		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ObjectType_InternalError_GetObjectTypesByIDsFailed).WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return otIDs, nil
}

func (ots *objectTypeService) GetAllObjectTypesByKnID(ctx context.Context,
	knID string, branch string) (map[string]*interfaces.ObjectType, error) {
	// Get object types.
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, fmt.Sprintf("按kn_id[%s]获取对象类基本信息", knID))
	defer span.End()

	// Get basic object type information.
	objectTypes, err := ots.ota.GetAllObjectTypesByKnID(ctx, knID, branch)
	if err != nil {
		logger.Errorf("GetAllObjectTypesByKnID error: %s", err.Error())
		span.SetStatus(codes.Error, fmt.Sprintf("Get all object type by kn_id[%s] error: %v", knID, err))

		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ObjectType_InternalError_GetObjectTypesByIDsFailed).WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return objectTypes, nil
}

// Internal API without permission checks.
func (ots *objectTypeService) GetObjectTypeByID(ctx context.Context, tx *sql.Tx,
	knID string, branch string, otID string) (*interfaces.ObjectType, error) {
	// Get object types.
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, fmt.Sprintf("查询对象类[%s]信息", otID))
	defer span.End()

	var err error
	// 0. Begin the transaction.
	if tx == nil {
		tx, err = ots.db.Begin()
		if err != nil {
			otellog.LogError(ctx, "Begin transaction error", err)
			return &interfaces.ObjectType{}, rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_ObjectType_InternalError_BeginTransactionFailed).
				WithErrorDetails(err.Error())
		}
		// 0.1 On failure.
		defer func() {
			switch err {
			case nil:
				// Commit the transaction.
				err = tx.Commit()
				if err != nil {
					otellog.LogError(ctx, "GetObjectTypeByID Transaction Commit Failed", err)
					return
				}
				otellog.LogDebug(ctx, "GetObjectTypeByID Transaction Commit Success")
			default:
				rollbackErr := tx.Rollback()
				if rollbackErr != nil {
					otellog.LogError(ctx, "GetObjectTypeByID Transaction Rollback Error", err)
				}
			}
		}()
	}

	// Get basic object type information.
	objectType, err := ots.ota.GetObjectTypeByID(ctx, tx, knID, branch, otID)
	if err != nil {
		logger.Errorf("GetObjectTypeByID error: %s", err.Error())
		span.SetStatus(codes.Error, fmt.Sprintf("Get object type by id[%s] error: %v", otID, err))

		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ObjectType_InternalError_GetObjectTypeByIDFailed).WithErrorDetails(err.Error())
	}
	if objectType == nil {
		return nil, rest.NewHTTPError(ctx, http.StatusNotFound, berrors.BknBackend_ObjectType_ObjectTypeNotFound).
			WithErrorDetails(invalidParameterDetail(ctx, "ObjectTypeNotFound", map[string]any{"objectTypeID": otID}))
	}

	span.SetStatus(codes.Ok, "")
	return objectType, nil
}

// applyIndexCapOps adds search capabilities already present in resource-local indexes to property operators.
//
// Capabilities are added rather than replaced: property-type operators are the baseline even when an
// object type has no index. Resource indexes add available operators. Full-text indexes already support
// match and multi_match through BKN rewriting and Vega full-text field routing; this records that capability.
//
// Mapping vector capability to KNN requires index_config.vector_config with the physical vector field
// and resolved model ID. Table resources originate as strings and vectors are generated by build tasks.
// Callers reset Vector to false when model resolution fails, so this function only records available capability.
func applyIndexCapOps(ops []string, propCaps logics.PropertyIndexCaps) []string {
	if !propCaps.Keyword && !propCaps.Fulltext && !propCaps.Vector {
		return ops
	}

	merged := make([]string, len(ops), len(ops)+len(interfaces.DSL_KEYWORD_OPS)+len(interfaces.DSL_TEXT_OPS)+len(interfaces.DSL_VECTOR_OPS))
	copy(merged, ops)
	seen := make(map[string]struct{}, cap(merged))
	for _, op := range merged {
		seen[op] = struct{}{}
	}
	appendOps := func(candidates []string) {
		for _, op := range candidates {
			if _, exists := seen[op]; exists {
				continue
			}
			seen[op] = struct{}{}
			merged = append(merged, op)
		}
	}

	if propCaps.Keyword {
		appendOps(interfaces.DSL_KEYWORD_OPS)
	}
	if propCaps.Fulltext {
		appendOps(interfaces.DSL_TEXT_OPS)
	}
	if propCaps.Vector {
		appendOps(interfaces.DSL_VECTOR_OPS)
	}
	return merged
}

// Process operators for string property types.
func (ots *objectTypeService) processConditionOperations(objectType *interfaces.ObjectType, prop *interfaces.DataProperty,
	dataView *interfaces.DataView) []string {

	ops := []string{}
	if objectType.Status != nil && !objectType.Status.IndexAvailable {
		// When indexes are unavailable, derive operations from view field types because varchar is a database type and keyword/text are OpenSearch types.
		switch prop.Type {
		case "keyword":
			ops = interfaces.DSL_KEYWORD_OPS
		case "varchar", "string":
			// A string source type may be keyword or varchar, so distinguish by view type.
			if dataView.QueryType == interfaces.VIEW_QueryType_DSL {
				ops = interfaces.DSL_KEYWORD_OPS
			} else {
				ops = interfaces.SQL_STRING_OPS
			}
		case "text":
			if dataView.QueryType == interfaces.VIEW_QueryType_DSL {
				ops = interfaces.DSL_TEXT_OPS // DSL text supports match
				ops = append(ops, interfaces.DSL_KEYWORD_OPS...)
			} else {
				ops = interfaces.SQL_STRING_OPS
			}
		case "vector":
			// KNN operations require an enabled small model.
			if ots.appSetting.ServerSetting.DefaultSmallModelEnabled {
				ops = append(ops, cond.OperationKNN)
			}
		}
	} else {
		opMap := make(map[string]string)
		// text supports match; other string types support ==, !=, in, and not_in.
		switch prop.Type {
		case "keyword", "varchar", "string":
			// Copy map content instead of assigning reference to avoid concurrent map access
			for k, v := range interfaces.DSL_KEYWORD_OPS_MAP {
				opMap[k] = v
			}
		case "text":
			// Copy map content instead of assigning reference to avoid concurrent map access
			for k, v := range interfaces.DSL_KEYWORD_OPS_MAP {
				opMap[k] = v
			}
			for k, v := range interfaces.DSL_TEXT_OPS_MAP {
				opMap[k] = v
			}
		case "vector":
			opMap[cond.OperationKNN] = cond.OperationKNN
		}

		// A keyword index is configured.
		if prop.IndexConfig != nil && prop.IndexConfig.KeywordConfig.Enabled {
			// Add operators supported by keyword indexes.
			for k, v := range interfaces.DSL_KEYWORD_OPS_MAP {
				opMap[k] = v
			}
		}
		// A full-text index enables match operations.
		if prop.IndexConfig != nil && prop.IndexConfig.FulltextConfig.Enabled {
			opMap[cond.OperationMatch] = cond.OperationMatch
			opMap[cond.OperationMultiMatch] = cond.OperationMultiMatch
		}
		// A vector index with an enabled embedding model enables KNN operations.
		if prop.IndexConfig != nil && prop.IndexConfig.VectorConfig.Enabled &&
			ots.appSetting.ServerSetting.DefaultSmallModelEnabled {

			opMap[cond.OperationKNN] = cond.OperationKNN
		}

		for k := range opMap {
			ops = append(ops, k)
		}
	}
	return ops
}

// Process and persist object type-to-group relationships.
func (ots *objectTypeService) handleGroupRelations(ctx context.Context, tx *sql.Tx,
	objectType *interfaces.ObjectType, currentTime int64, strictMode bool) error {

	var err error
	cgIDs := []string{}
	for _, cg := range objectType.ConceptGroups {
		cgIDs = append(cgIDs, cg.CGID)
	}
	// De-duplicate IDs before querying.
	cgIDs = common.DuplicateSlice(cgIDs)

	// When strictMode is true, validate all concept groups exist
	if strictMode {
		conceptGroups, err := ots.cga.GetConceptGroupsByIDs(ctx, tx, objectType.KNID, objectType.Branch, cgIDs)
		if err != nil {
			errStr := fmt.Sprintf("GetConceptGroupsByIDs failed, the kn_id: [%s], branch: [%s], cg_ids: [%v], error: %s",
				objectType.KNID, objectType.Branch, cgIDs, err.Error())
			logger.Errorf(errStr)

			return rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_ObjectType_InternalError).
				WithErrorDetails(errStr)
		}
		if len(conceptGroups) != len(cgIDs) {
			errStr := fmt.Sprintf("Exists any concept group not found, expect concept group nums is [%d], actual concept group num is [%d]",
				len(cgIDs), len(conceptGroups))
			logger.Errorf(errStr)

			return rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_ObjectType_InternalError).
				WithErrorDetails(errStr)
		}
	}

	// Create.
	for _, cg := range objectType.ConceptGroups {
		cgRelationID := xid.New().String()
		err = ots.cga.CreateConceptGroupRelation(ctx, tx, &interfaces.ConceptGroupRelation{
			ID:          cgRelationID,
			KNID:        objectType.KNID,
			Branch:      objectType.Branch,
			CGID:        cg.CGID,
			ConceptType: interfaces.MODULE_TYPE_OBJECT_TYPE,
			ConceptID:   objectType.OTID,
			CreateTime:  currentTime,
		})
		if err != nil {
			errStr := fmt.Sprintf("CreateConceptGroupRelation failed, the concept group is [%s], knowledge network is [%s], branch is [%s], object type is [%s]",
				cg.CGID, objectType.KNID, objectType.Branch, objectType.OTID)
			logger.Errorf(errStr)

			return rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_ObjectType_InternalError_CreateConceptGroupRelationFailed).
				WithErrorDetails(err.Error())
		}
	}
	return nil
}

// syncObjectGroups synchronizes group relationships by full replacement during updates.
func (ots *objectTypeService) syncObjectGroups(ctx context.Context, tx *sql.Tx,
	objectType interfaces.ObjectType, currentTime int64, strictMode bool) error {

	cgIDs := []string{}
	for _, cg := range objectType.ConceptGroups {
		cgIDs = append(cgIDs, cg.CGID)
	}
	// De-duplicate IDs before querying.
	cgIDs = common.DuplicateSlice(cgIDs)

	// When strictMode is true and cgIDs not empty, validate all concept groups exist
	if strictMode && len(cgIDs) > 0 {
		conceptGroups, err := ots.cga.GetConceptGroupsByIDs(ctx, tx, objectType.KNID, objectType.Branch, cgIDs)
		if err != nil {
			errStr := fmt.Sprintf("GetConceptGroupsByIDs failed, the kn_id: [%s], branch: [%s], cg_ids: [%v], error: %s",
				objectType.KNID, objectType.Branch, cgIDs, err.Error())
			logger.Errorf(errStr)

			return rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_ObjectType_InternalError).
				WithErrorDetails(errStr)
		}
		if len(conceptGroups) != len(cgIDs) {
			errStr := fmt.Sprintf("Exists any concept group not found, expect concept group nums is [%d], actual concept group num is [%d]",
				len(cgIDs), len(conceptGroups))
			logger.Errorf(errStr)

			return rest.NewHTTPError(ctx, http.StatusBadRequest,
				berrors.BknBackend_ObjectType_InvalidParameter).
				WithErrorDetails(errStr)
		}
	}

	// 1. Get existing object type group relationships.
	existingRelation, err := ots.cga.GetConceptGroupsByOTIDs(ctx, tx, interfaces.ConceptGroupRelationsQueryParams{
		KNID:   objectType.KNID,
		Branch: objectType.Branch,
		OTIDs:  []string{objectType.OTID},
	})
	if err != nil {
		logger.Errorf(err.Error())
		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ObjectType_InternalError).WithErrorDetails(err.Error())
	}

	// 2. Compute groups to add and delete.
	existingGroupIDs := make(map[string]bool)
	if len(existingRelation) == 1 {
		// Existing object type relationships.
		for _, rel := range existingRelation[objectType.OTID] {
			existingGroupIDs[rel.CGID] = true
		}
	}

	newGroupIDs := make(map[string]bool)
	for _, ref := range objectType.ConceptGroups {
		newGroupIDs[ref.CGID] = true
	}

	// Compute differences.
	groupsToAdd := make([]string, 0)
	groupsToRemove := make([]string, 0)

	for groupID := range newGroupIDs {
		if !existingGroupIDs[groupID] {
			groupsToAdd = append(groupsToAdd, groupID)
		}
	}

	for groupID := range existingGroupIDs {
		if !newGroupIDs[groupID] {
			groupsToRemove = append(groupsToRemove, groupID)
		}
	}

	// 3. Add relationships.
	if len(groupsToAdd) > 0 {
		// Build records for new relationships.
		for _, cgID := range groupsToAdd {
			cgRelationID := xid.New().String()
			err = ots.cga.CreateConceptGroupRelation(ctx, tx, &interfaces.ConceptGroupRelation{
				ID:          cgRelationID,
				KNID:        objectType.KNID,
				Branch:      objectType.Branch,
				CGID:        cgID,
				ConceptType: interfaces.MODULE_TYPE_OBJECT_TYPE,
				ConceptID:   objectType.OTID,
				CreateTime:  currentTime,
			})
			if err != nil {
				errStr := fmt.Sprintf("CreateConceptGroupRelation failed, the concept group is [%s], knowledge network is [%s], branch is [%s], object type is [%s], error is [%s]",
					cgID, objectType.KNID, objectType.Branch, objectType.OTID, err.Error())
				logger.Errorf(errStr)

				return rest.NewHTTPError(ctx, http.StatusInternalServerError,
					berrors.BknBackend_ObjectType_InternalError_CreateConceptGroupRelationFailed).
					WithErrorDetails(errStr)
			}
		}
	}

	// 4. Delete relationships.
	if len(groupsToRemove) > 0 {
		// Delete object type-to-group bindings.
		rowsAffect, err := ots.cga.DeleteObjectTypesFromGroup(ctx, tx, interfaces.ConceptGroupRelationsQueryParams{
			KNID:        objectType.KNID,
			Branch:      objectType.Branch,
			CGIDs:       groupsToRemove,
			ConceptType: interfaces.MODULE_TYPE_OBJECT_TYPE,
			OTIDs:       []string{objectType.OTID},
		})
		if err != nil {
			errStr := fmt.Sprintf("DeleteObjectTypesFromGroup failed, the concept group is [%v], kn_id is [%s], branch is [%s], object type is [%s], error is [%s]",
				groupsToRemove, objectType.KNID, objectType.Branch, objectType.OTID, err.Error())
			logger.Errorf(errStr)

			return rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_ObjectType_InternalError).
				WithErrorDetails(errStr)
		}
		// Record the deleted count in an info log.
		logger.Infof("DeleteObjectTypesFromGroup success, the concept group is [%v], kn_id is [%s], branch is [%s], object type is [%s], rowsAffect is [%d]",
			groupsToRemove, objectType.KNID, objectType.Branch, objectType.OTID, rowsAffect)
	}

	return nil
}

// Query in batches.
func (ots *objectTypeService) GetTotalWithLargeOTIDs(ctx context.Context,
	filterCondition map[string]any,
	otIDs []string) (int64, error) {

	total := int64(0)
	for i := 0; i < len(otIDs); i += interfaces.GET_TOTAL_CONCEPTID_BATCH_SIZE {
		end := i + interfaces.GET_TOTAL_CONCEPTID_BATCH_SIZE
		if end > len(otIDs) {
			end = len(otIDs)
		}

		batchIDs := otIDs[i:end]
		batchTotal, err := ots.GetTotalWithOTIDs(ctx, filterCondition, batchIDs)
		if err != nil {
			return 0, err
		}

		total += batchTotal
	}

	return total, nil
}

// Query the total count for specified object type IDs.
func (ots *objectTypeService) GetTotalWithOTIDs(ctx context.Context,
	filterCondition map[string]any,
	otIDs []string) (int64, error) {

	// Build a filter condition containing the object type ID filter.
	otIDCondition := map[string]any{
		"field":      "id",
		"operation":  "in",
		"value":      otIDs,
		"value_from": "const",
	}

	var combinedCondition map[string]any
	if filterCondition == nil {
		combinedCondition = otIDCondition
	} else {
		combinedCondition = map[string]any{
			"operation": "and",
			"sub_conditions": []map[string]any{
				filterCondition,
				otIDCondition,
			},
		}
	}

	// Execute the count query.
	total, err := ots.GetTotal(ctx, combinedCondition)
	if err != nil {
		return total, err
	}

	return total, nil
}
