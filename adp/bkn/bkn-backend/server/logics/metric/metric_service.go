// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package metric

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
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"go.opentelemetry.io/otel/codes"

	bknsdk "bkn-backend/bkn-specification/bkn"
	"bkn-backend/common"
	cond "bkn-backend/common/condition"
	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
	"bkn-backend/logics"
	"bkn-backend/logics/batchindex"
	"bkn-backend/logics/model_factory"
	"bkn-backend/logics/object_type"
	"bkn-backend/logics/permission"
	"bkn-backend/logics/vega_backend"
)

var (
	metricServiceOnce sync.Once
	metricServiceInst interfaces.MetricService
)

type metricService struct {
	appSetting *common.AppSetting
	db         *sql.DB
	ma         interfaces.MetricAccess
	cga        interfaces.ConceptGroupAccess
	ps         interfaces.PermissionService
	uma        interfaces.UserMgmtService
	vbs        interfaces.VegaBackendService
	mfs        interfaces.ModelFactoryService
	ots        interfaces.ObjectTypeService
}

func metricInvalidParameterDetail(ctx context.Context, name string, templateData map[string]any) string {
	return i18n.Translate(rest.GetLanguageByCtx(ctx), "BknBackend.Metric.InvalidParameter.Detail."+name, templateData)
}

func NewMetricService(appSetting *common.AppSetting) interfaces.MetricService {
	metricServiceOnce.Do(func() {
		metricServiceInst = &metricService{
			appSetting: appSetting,
			db:         logics.DB,
			ma:         logics.MA,
			cga:        logics.CGA,
			ps:         permission.NewPermissionService(appSetting),
			uma:        logics.UMA,
			vbs:        vega_backend.NewVegaBackendService(appSetting, logics.VBA),
			mfs:        model_factory.NewModelFactoryService(appSetting, logics.MFA),
			ots:        object_type.NewObjectTypeService(appSetting),
		}
	})
	return metricServiceInst
}

func (ms *metricService) InsertDatasetData(ctx context.Context, metrics []*interfaces.MetricDefinition) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "metric index write")
	defer span.End()

	if len(metrics) == 0 {
		return nil
	}

	if ms.appSetting.ServerSetting.DefaultSmallModelEnabled && ms.mfs != nil {
		words := make([]string, 0, len(metrics))
		for _, m := range metrics {
			arr := []string{m.Name}
			arr = append(arr, m.Tags...)
			arr = append(arr, m.Comment, m.BKNRawContent)
			word := strings.Join(arr, "\n")
			words = append(words, word)
		}
		dftModel, err := ms.mfs.GetDefaultModel(ctx)
		if err != nil {
			logger.Errorf("GetDefaultModel error: %s", err.Error())
			span.SetStatus(codes.Error, "获取默认模型失败")
			return err
		}
		vectors, err := ms.mfs.GetVector(ctx, dftModel, words)
		if err != nil {
			logger.Errorf("GetVector error: %s", err.Error())
			span.SetStatus(codes.Error, "获取指标向量失败")
			return err
		}
		if len(vectors) != len(metrics) {
			return fmt.Errorf("GetVector: expect %d vectors, got %d", len(metrics), len(vectors))
		}
		for i := range metrics {
			metrics[i].Vector = vectors[i].Vector
		}
	}

	documents := make([]map[string]any, 0, len(metrics))
	for _, def := range metrics {
		docid := interfaces.GenerateConceptDocuemtnID(def.KnID, interfaces.MODULE_TYPE_METRIC, def.ID, def.Branch)
		def.ModuleType = interfaces.MODULE_TYPE_METRIC

		docBytes, err := sonic.Marshal(def)
		if err != nil {
			return err
		}
		var doc map[string]any
		if err := sonic.Unmarshal(docBytes, &doc); err != nil {
			return err
		}
		doc["_id"] = docid
		documents = append(documents, doc)
	}

	if err := ms.vbs.WriteDatasetDocuments(ctx, interfaces.BKN_DATASET_ID, documents); err != nil {
		logger.Errorf("WriteDatasetDocuments error: %s", err.Error())
		span.SetStatus(codes.Error, "指标概念索引写入失败")
		return err
	}
	return nil
}

func (ms *metricService) deleteDatasetDocs(ctx context.Context, knID string, branch string, metricIDs []string) {
	for _, id := range metricIDs {
		docid := interfaces.GenerateConceptDocuemtnID(knID, interfaces.MODULE_TYPE_METRIC, id, branch)
		if err := ms.vbs.DeleteDatasetDocumentByID(ctx, interfaces.BKN_DATASET_ID, docid); err != nil {
			logger.Errorf("DeleteDatasetDocumentByID metric %s: %v", id, err)
		}
	}
}

func (ms *metricService) CreateMetrics(ctx context.Context, tx *sql.Tx, entries []*interfaces.MetricDefinition, strictMode bool, importMode string) (ids []string, err error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "CreateMetrics")
	defer span.End()
	ctx, parentTracker, trackerOwner := permission.WithResourceParentTracker(ctx)
	defer func() {
		if trackerOwner && err != nil {
			_ = parentTracker.Cleanup(ctx, ms.ps)
		}
	}()

	if len(entries) == 0 {
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_RequestBody).
			WithErrorDetails(i18n.Translate(rest.GetLanguageByCtx(ctx), "BknBackend.Validation.Detail.EntriesRequired", nil))
	}

	if !permission.KNImportPermissionPrechecked(ctx) && !permission.KNChildResourcePEPEnabled() {
		err = ms.ps.CheckPermission(ctx, interfaces.PermissionResource{
			Type: interfaces.RESOURCE_TYPE_KN,
			ID:   entries[0].KnID,
		}, []string{interfaces.OPERATION_TYPE_MODIFY})
		if err != nil {
			return nil, err
		}
	}

	// 0. Begin the transaction.
	if tx == nil {
		tx, err = ms.db.Begin()
		if err != nil {
			logger.Errorf("Begin transaction error: %s", err.Error())
			span.SetStatus(codes.Error, "事务开启失败")

			return []string{}, rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_Metric_InternalError_BeginTransactionFailed).
				WithErrorDetails(err.Error())
		}
		// 0.1 On failure.
		defer func() {
			switch err {
			case nil:
				// Commit the transaction.
				err = tx.Commit()
				if err != nil {
					logger.Errorf("CreateMetrics Transaction Commit Failed:%v", err)
					span.SetStatus(codes.Error, "提交事务失败")
					return
				}
				logger.Infof("CreateMetrics Transaction Commit Success")
			default:
				rollbackErr := tx.Rollback()
				if rollbackErr != nil {
					logger.Errorf("CreateMetrics Transaction Rollback Error:%v", rollbackErr)
					span.SetStatus(codes.Error, "事务回滚失败")
				}
			}
		}()
	}

	currentTime := time.Now().UnixMilli()
	var accountInfo interfaces.AccountInfo
	if ctx.Value(interfaces.ACCOUNT_INFO_KEY) != nil {
		accountInfo = ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	}

	for _, m := range entries {
		m.ID, err = permission.PrepareKNChildResourceID(ctx, strings.TrimSpace(m.ID))
		if err != nil {
			return nil, err
		}
		if err = permission.ValidateKNChildAuthorizationIDs(ctx, m.KnID, []string{m.ID}); err != nil {
			return nil, err
		}
		m.Creator = accountInfo
		m.Updater = accountInfo
		m.CreateTime = currentTime
		m.UpdateTime = currentTime
		m.ModuleType = interfaces.MODULE_TYPE_METRIC

		if strictMode {
			if err := ms.validateMetricStrictExternalDeps(ctx, tx, m); err != nil {
				return []string{}, err
			}
		}
		metricObj := logics.ToBKNMetricDefinition(m)
		m.BKNRawContent = bknsdk.SerializeMetric(metricObj)
	}

	var creates []*interfaces.MetricDefinition
	var updates []*interfaces.MetricDefinition
	creates, updates, err = ms.handleMetricImportMode(ctx, importMode, entries)
	if err != nil {
		return nil, err
	}
	if !permission.KNImportPermissionPrechecked(ctx) && permission.KNChildResourcePEPEnabled() {
		if len(creates) > 0 {
			if err = ms.ps.CheckPermission(ctx, interfaces.PermissionResource{
				Type: interfaces.RESOURCE_TYPE_KN,
				ID:   entries[0].KnID,
			}, []string{interfaces.OPERATION_TYPE_MODIFY}); err != nil {
				return nil, err
			}
		}
		updateIDs := make([]string, 0, len(updates))
		for _, metric := range updates {
			updateIDs = append(updateIDs, metric.ID)
		}
		if err = permission.CheckKNChildBatchPermission(ctx, ms.ps,
			interfaces.RESOURCE_TYPE_METRIC, entries[0].KnID, updateIDs,
			interfaces.OPERATION_TYPE_MODIFY, interfaces.OPERATION_TYPE_MODIFY); err != nil {
			return nil, err
		}
	}

	ids = make([]string, 0, len(creates)+len(updates))

	for _, def := range updates {
		if err := ms.UpdateMetric(ctx, tx, def, strictMode); err != nil {
			return nil, err
		}
		ids = append(ids, def.ID)
	}

	if len(creates) == 0 {
		span.SetStatus(codes.Ok, "")
		return ids, nil
	}

	for _, def := range creates {
		err = ms.ma.CreateMetric(ctx, tx, def)
		if err != nil {
			logger.Errorf("CreateMetric error: %s", err.Error())
			span.SetStatus(codes.Error, "创建指标失败")
			return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_Metric_InternalError).WithErrorDetails(err.Error())
		}
		ids = append(ids, def.ID)
	}
	createdIDs := make([]string, 0, len(creates))
	for _, def := range creates {
		createdIDs = append(createdIDs, def.ID)
	}
	parentItems := interfaces.KNChildResourceParents(entries[0].KnID, createdIDs)
	if err = ms.ps.UpsertResourceParents(ctx, interfaces.RESOURCE_TYPE_METRIC,
		interfaces.RESOURCE_TYPE_KN, parentItems); err != nil {
		return nil, err
	}
	permission.TrackResourceParents(ctx, interfaces.RESOURCE_TYPE_METRIC,
		interfaces.RESOURCE_TYPE_KN, parentItems)

	err = ms.InsertDatasetData(ctx, creates)
	if err != nil {
		logger.Errorf("InsertDatasetData error: %s", err.Error())
		span.SetStatus(codes.Error, "指标概念索引写入失败")
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_Metric_InternalError).WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return ids, nil
}

// handleMetricImportMode splits metrics into creates and updates by import_mode (overwrite aligned with object types).
func (ms *metricService) handleMetricImportMode(ctx context.Context, mode string, metrics []*interfaces.MetricDefinition) ([]*interfaces.MetricDefinition, []*interfaces.MetricDefinition, error) {
	creates := make([]*interfaces.MetricDefinition, 0, len(metrics))
	updates := make([]*interfaces.MetricDefinition, 0)

	for _, m := range metrics {
		knID, branch := m.KnID, m.Branch
		id := strings.TrimSpace(m.ID)

		var idExist, nameExist bool
		var existNameByID, existIDByName string
		var qerr error
		if id != "" {
			existNameByID, idExist, qerr = ms.ma.CheckMetricExistByID(ctx, knID, branch, id)
			if qerr != nil {
				return nil, nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
					berrors.BknBackend_Metric_InternalError_CheckMetricIfExistFailed).WithErrorDetails(qerr.Error())
			}
		}
		existIDByName, nameExist, qerr = ms.ma.CheckMetricExistByName(ctx, knID, branch, m.Name)
		if qerr != nil {
			return nil, nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_Metric_InternalError_CheckMetricIfExistFailed).WithErrorDetails(qerr.Error())
		}

		if idExist || nameExist {
			switch mode {
			case interfaces.ImportMode_Normal:
				if idExist {
					return nil, nil, rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Metric_InvalidParameter).
						WithErrorDetails(metricInvalidParameterDetail(ctx, "MetricIDAlreadyExists", map[string]any{"id": id, "name": existNameByID}))
				}
				if nameExist && existIDByName != id {
					return nil, nil, rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Metric_Duplicated_Name).
						WithErrorDetails(metricInvalidParameterDetail(ctx, "MetricNameAlreadyExists", map[string]any{"name": m.Name}))
				}
			case interfaces.ImportMode_Ignore:
				continue
			case interfaces.ImportMode_Overwrite:
				if idExist && nameExist && existIDByName != id {
					return nil, nil, rest.NewHTTPError(ctx, http.StatusForbidden, berrors.BknBackend_Metric_Duplicated_Name).
						WithErrorDetails(metricInvalidParameterDetail(ctx, "MetricIDNameConflict", map[string]any{"id": id, "name": m.Name, "existingID": existIDByName}))
				}
				if idExist && nameExist && existIDByName == id {
					updates = append(updates, m)
					continue
				}
				if idExist && !nameExist {
					updates = append(updates, m)
					continue
				}
				if !idExist && nameExist {
					return nil, nil, rest.NewHTTPError(ctx, http.StatusForbidden, berrors.BknBackend_Metric_Duplicated_Name).
						WithErrorDetails(metricInvalidParameterDetail(ctx, "MetricNameAlreadyExists", map[string]any{"name": m.Name}))
				}
				continue
			default:
				return nil, nil, rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_ImportMode).
					WithErrorDetails(i18n.Translate(rest.GetLanguageByCtx(ctx), "BknBackend.Validation.Detail.ImportMode", nil))
			}
		}

		creates = append(creates, m)
	}
	return creates, updates, nil
}

func (ms *metricService) ListMetrics(ctx context.Context, query interfaces.MetricsListQueryParams) (*interfaces.MetricsList, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "ListMetrics")
	defer span.End()
	if !permission.KNChildResourcePEPEnabled() {
		if err := ms.ps.CheckPermission(ctx, interfaces.PermissionResource{
			Type: interfaces.RESOURCE_TYPE_KN,
			ID:   query.KNID,
		}, []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}); err != nil {
			return nil, err
		}
	}

	candidateQuery := query
	candidateQuery.Offset = 0
	candidateQuery.Limit = -1
	list, err := ms.ma.ListMetrics(ctx, candidateQuery)
	if err != nil {
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_Metric_InternalError).WithErrorDetails(err.Error())
	}
	total := len(list)
	if permission.KNChildResourcePEPEnabled() {
		list, total, err = permission.FilterAndPaginateKNChildren(ctx, ms.ps,
			interfaces.RESOURCE_TYPE_METRIC, query.KNID, list,
			func(metric *interfaces.MetricDefinition) string { return metric.ID }, query.Offset, query.Limit)
		if err != nil {
			return nil, err
		}
	} else {
		list = permission.PaginateKNChildCandidates(list, query.Offset, query.Limit)
	}

	if len(list) > 0 && ms.uma != nil {
		infos := make([]*interfaces.AccountInfo, 0, len(list)*2)
		for _, m := range list {
			infos = append(infos, &m.Creator, &m.Updater)
		}
		_ = ms.uma.GetAccountNames(ctx, infos)
	}

	span.SetStatus(codes.Ok, "")
	return &interfaces.MetricsList{Entries: list, TotalCount: int64(total)}, nil
}

func (ms *metricService) GetMetricByID(ctx context.Context, knID, branch, metricID string) (*interfaces.MetricDefinition, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "GetMetricByID")
	defer span.End()

	if err := permission.ValidateKNChildPEPAuthorizationIDs(ctx, knID, []string{metricID}); err != nil {
		return nil, err
	}

	def, err := ms.ma.GetMetricByID(ctx, knID, branch, metricID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, rest.NewHTTPError(ctx, http.StatusNotFound, berrors.BknBackend_Metric_NotFound)
		}
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_Metric_InternalError).WithErrorDetails(err.Error())
	}
	resource, operation := permission.ResolveKNChildPermissionTarget(interfaces.RESOURCE_TYPE_METRIC,
		knID, metricID, interfaces.OPERATION_TYPE_VIEW_DETAIL, interfaces.OPERATION_TYPE_VIEW_DETAIL)
	if err = ms.ps.CheckPermission(ctx, resource, []string{operation}); err != nil {
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return def, nil
}

func (ms *metricService) GetMetricsByIDs(ctx context.Context, knID, branch string, metricIDs []string) ([]*interfaces.MetricDefinition, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "GetMetricsByIDs")
	defer span.End()

	metricIDs = common.DuplicateSlice(metricIDs)
	list, err := ms.ma.GetMetricsByIDs(ctx, knID, branch, metricIDs)
	if err != nil {
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_Metric_InternalError_GetMetricsByIDsFailed).WithErrorDetails(err.Error())
	}
	span.SetStatus(codes.Ok, "")
	return list, nil
}

func (ms *metricService) CheckMetricExistByID(ctx context.Context, knID, branch, metricID string) (string, bool, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, fmt.Sprintf("校验指标[%s]的存在性", metricID))
	defer span.End()

	name, exist, err := ms.ma.CheckMetricExistByID(ctx, knID, branch, metricID)
	if err != nil {
		logger.Errorf("CheckMetricExistByID error: %s", err.Error())
		span.SetStatus(codes.Error, "check metric existence by id failed")
		return "", exist, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_Metric_InternalError_CheckMetricIfExistFailed).
			WithErrorDetails(err.Error())
	}
	span.SetStatus(codes.Ok, "")
	return name, exist, nil
}

func (ms *metricService) CheckMetricExistByName(ctx context.Context, knID, branch, name string) (string, bool, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, fmt.Sprintf("校验指标名称[%s]的存在性", name))
	defer span.End()

	id, exist, err := ms.ma.CheckMetricExistByName(ctx, knID, branch, name)
	if err != nil {
		logger.Errorf("CheckMetricExistByName error: %s", err.Error())
		span.SetStatus(codes.Error, "check metric existence by name failed")
		return "", exist, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_Metric_InternalError_CheckMetricIfExistFailed).
			WithErrorDetails(err.Error())
	}
	span.SetStatus(codes.Ok, "")
	return id, exist, nil
}

func (ms *metricService) ValidateMetrics(ctx context.Context, entries []*interfaces.MetricDefinition, strictMode bool, importMode string, batch *interfaces.BatchIDIndex) error {
	_ = importMode
	if len(entries) == 0 {
		return nil
	}
	for _, e := range entries {
		if strictMode {
			var err error
			if batch != nil {
				err = ms.validateMetricStrictExternalDepsFromBatch(ctx, e, batch)
			} else {
				err = ms.validateMetricStrictExternalDeps(ctx, nil, e)
			}
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (ms *metricService) UpdateMetric(ctx context.Context, tx *sql.Tx, req *interfaces.MetricDefinition, strictMode bool) (err error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "UpdateMetric")
	defer span.End()

	knID := strings.TrimSpace(req.KnID)
	branch := strings.TrimSpace(req.Branch)
	metricID := strings.TrimSpace(req.ID)
	if knID == "" || branch == "" || metricID == "" {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Metric_InvalidParameter).
			WithErrorDetails(metricInvalidParameterDetail(ctx, "MetricIdentityRequired", nil))
	}
	if err = permission.ValidateKNChildPEPAuthorizationIDs(ctx, knID, []string{metricID}); err != nil {
		return err
	}
	_, exists, err := ms.CheckMetricExistByID(ctx, knID, branch, metricID)
	if err != nil {
		return err
	}
	if !exists {
		return rest.NewHTTPError(ctx, http.StatusNotFound, berrors.BknBackend_Metric_NotFound)
	}

	resource, operation := permission.ResolveKNChildPermissionTarget(interfaces.RESOURCE_TYPE_METRIC,
		knID, metricID, interfaces.OPERATION_TYPE_MODIFY, interfaces.OPERATION_TYPE_MODIFY)
	err = ms.ps.CheckPermission(ctx, resource, []string{operation})
	if err != nil {
		return err
	}

	if tx == nil {
		// 0. Begin the transaction.
		tx, err = ms.db.Begin()
		if err != nil {
			logger.Errorf("Begin transaction error: %s", err.Error())
			span.SetStatus(codes.Error, "事务开启失败")

			return rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_Metric_InternalError_BeginTransactionFailed).
				WithErrorDetails(err.Error())
		}
		// 0.1 On failure.
		defer func() {
			switch err {
			case nil:
				// Commit the transaction.
				err = tx.Commit()
				if err != nil {
					logger.Errorf("UpdateMetric Transaction Commit Failed:%v", err)
					span.SetStatus(codes.Error, "提交事务失败")
				}
				logger.Infof("UpdateMetric Transaction Commit Success:%v", metricID)
			default:
				rollbackErr := tx.Rollback()
				if rollbackErr != nil {
					logger.Errorf("UpdateMetric Transaction Rollback Error:%v", rollbackErr)
					span.SetStatus(codes.Error, "事务回滚失败")
				}
			}
		}()
	}

	if strictMode {
		if err := ms.validateMetricStrictExternalDeps(ctx, tx, req); err != nil {
			return err
		}
	}

	if ctx.Value(interfaces.ACCOUNT_INFO_KEY) != nil {
		req.Updater = ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	}
	req.UpdateTime = time.Now().UnixMilli()

	metricObj := logics.ToBKNMetricDefinition(req)
	req.BKNRawContent = bknsdk.SerializeMetric(metricObj)

	err = ms.ma.UpdateMetric(ctx, tx, req)
	if err != nil {
		logger.Errorf("UpdateMetric error: %s", err.Error())
		span.SetStatus(codes.Error, "修改指标失败")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_Metric_InternalError).WithErrorDetails(err.Error())
	}

	err = ms.InsertDatasetData(ctx, []*interfaces.MetricDefinition{req})
	if err != nil {
		logger.Errorf("InsertDatasetData after update: %s", err.Error())
		span.SetStatus(codes.Error, "指标概念索引写入失败")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_Metric_InternalError).WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

func (ms *metricService) DeleteMetricsByIDs(ctx context.Context, tx *sql.Tx, knID, branch string, metricIDs []string) (err error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "DeleteMetricsByIDs")
	defer span.End()
	if tx == nil {
		var cleanupTracker *permission.AuthorizationCleanupTracker
		var trackerOwner bool
		ctx, cleanupTracker, trackerOwner = permission.WithAuthorizationCleanupTracker(ctx)
		defer func() {
			if trackerOwner && err == nil {
				_ = cleanupTracker.Cleanup(ctx, ms.ps)
			}
		}()
	}

	if len(metricIDs) == 0 {
		return nil
	}

	metricIDs = common.DuplicateSlice(metricIDs)
	if err = permission.ValidateKNChildPEPAuthorizationIDs(ctx, knID, metricIDs); err != nil {
		return err
	}
	if len(metricIDs) == 1 {
		_, exists, checkErr := ms.CheckMetricExistByID(ctx, knID, branch, metricIDs[0])
		if checkErr != nil {
			return checkErr
		}
		if !exists {
			return rest.NewHTTPError(ctx, http.StatusNotFound, berrors.BknBackend_Metric_NotFound)
		}
		resource, operation := permission.ResolveKNChildPermissionTarget(interfaces.RESOURCE_TYPE_METRIC,
			knID, metricIDs[0], interfaces.OPERATION_TYPE_MODIFY, interfaces.OPERATION_TYPE_DELETE)
		if err := ms.ps.CheckPermission(ctx, resource, []string{operation}); err != nil {
			return err
		}
	} else {
		if permission.KNChildResourcePEPEnabled() {
			metrics, queryErr := ms.ma.GetMetricsByIDs(ctx, knID, branch, metricIDs)
			if queryErr != nil {
				return rest.NewHTTPError(ctx, http.StatusInternalServerError,
					berrors.BknBackend_Metric_InternalError_GetMetricsByIDsFailed).WithErrorDetails(queryErr.Error())
			}
			if len(metrics) != len(metricIDs) {
				return rest.NewHTTPError(ctx, http.StatusNotFound, berrors.BknBackend_Metric_NotFound)
			}
		}
		if err := permission.CheckKNChildBatchPermission(ctx, ms.ps,
			interfaces.RESOURCE_TYPE_METRIC, knID, metricIDs,
			interfaces.OPERATION_TYPE_MODIFY, interfaces.OPERATION_TYPE_DELETE); err != nil {
			return err
		}
	}

	if tx == nil {
		// 0. Begin the transaction.
		tx, err = ms.db.Begin()
		if err != nil {
			logger.Errorf("Begin transaction error: %s", err.Error())
			span.SetStatus(codes.Error, "事务开启失败")

			return rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_Metric_InternalError_BeginTransactionFailed).
				WithErrorDetails(err.Error())
		}
		// 0.1 On failure.
		defer func() {
			switch err {
			case nil:
				// Commit the transaction.
				err = tx.Commit()
				if err != nil {
					logger.Errorf("DeleteMetricsByIDs Transaction Commit Failed:%v", err)
					span.SetStatus(codes.Error, "提交事务失败")
				}
				logger.Infof("DeleteMetricsByIDs Transaction Commit Success: kn_id:%s,metric_ids:%v", knID, metricIDs)
			default:
				rollbackErr := tx.Rollback()
				if rollbackErr != nil {
					logger.Errorf("DeleteMetricsByIDs Transaction Rollback Error:%v", rollbackErr)
					span.SetStatus(codes.Error, "事务回滚失败")
				}
			}
		}()
	}

	dErr := ms.ma.DeleteMetricsByIDs(ctx, tx, knID, branch, metricIDs)
	if dErr != nil {
		logger.Errorf("DeleteMetricsByIDs error: %s", dErr.Error())
		span.SetStatus(codes.Error, "删除指标失败")
		err = rest.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_Metric_InternalError).WithErrorDetails(dErr.Error())
		return err
	}

	ms.deleteDatasetDocs(ctx, knID, branch, metricIDs)
	permission.TrackKNChildAuthorizationCleanup(ctx,
		interfaces.RESOURCE_TYPE_METRIC, knID, metricIDs)
	return nil
}

// DeleteMetricsByKnID is an internal API that does not check permissions; tx is required to match DeleteActionTypesByKnID.
func (ms *metricService) DeleteMetricsByKnID(ctx context.Context, tx *sql.Tx, knID, branch string) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "DeleteMetricsByKnID")
	defer span.End()

	if tx == nil {
		logger.Errorf("DeleteMetricsByKnID: missing transaction")
		span.SetStatus(codes.Error, "missing transaction")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_Metric_InternalError).
			WithErrorDetails("missing transaction")
	}

	ids, err := ms.ma.GetMetricIDsByKnID(ctx, knID, branch)
	if err != nil {
		logger.Errorf("GetMetricIDsByKnID error: %v", err)
		span.SetStatus(codes.Error, "list metric ids failed")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_Metric_InternalError).WithErrorDetails(err.Error())
	}

	rowsAff, err := ms.ma.DeleteMetricsByKnID(ctx, tx, knID, branch)
	if err != nil {
		logger.Errorf("DeleteMetricsByKnID access error: %v", err)
		span.SetStatus(codes.Error, "delete metrics by kn failed")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_Metric_InternalError).WithErrorDetails(err.Error())
	}

	ms.deleteDatasetDocs(ctx, knID, branch, ids)
	logger.Infof("DeleteMetricsByKnID success, kn_id=%s branch=%s rows=%d metric_docs=%d", knID, branch, rowsAff, len(ids))
	permission.TrackKNChildAuthorizationCleanup(ctx,
		interfaces.RESOURCE_TYPE_METRIC, knID, ids)
	span.SetStatus(codes.Ok, "")
	return nil
}

func (ms *metricService) SearchMetrics(ctx context.Context, query *interfaces.ConceptsQuery) (interfaces.MetricSearchResult, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "SearchMetrics")
	defer span.End()

	response := interfaces.MetricSearchResult{
		Type:   interfaces.MODULE_TYPE_METRIC,
		Groups: []any{},
	}

	var err error
	var visibleIDs []string
	if !permission.KNChildResourcePEPEnabled() {
		err = ms.ps.CheckPermission(ctx, interfaces.PermissionResource{
			Type: interfaces.RESOURCE_TYPE_KN,
			ID:   query.KNID,
		}, []string{interfaces.OPERATION_TYPE_VIEW_DETAIL})
		if err != nil {
			return response, err
		}
	} else {
		candidateIDs, candidateErr := ms.ma.GetMetricIDsByKnID(ctx, query.KNID, query.Branch)
		if candidateErr != nil {
			return response, rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_Metric_InternalError).WithErrorDetails(candidateErr.Error())
		}
		visibleIDs, err = permission.FilterKNChildIDs(ctx, ms.ps,
			interfaces.RESOURCE_TYPE_METRIC, query.KNID, candidateIDs,
			interfaces.OPERATION_TYPE_VIEW_DETAIL)
		if err != nil {
			return response, err
		}
		if len(visibleIDs) == 0 {
			return response, nil
		}
	}

	var filterCondition map[string]any
	if query.ActualCondition != nil {
		filterCondition, err = cond.ConvertCondCfgToFilterCondition(ctx, query.ActualCondition,
			interfaces.CONCPET_QUERY_FIELD,
			func(ctx context.Context, word string) ([]*cond.VectorResp, error) {
				if !ms.appSetting.ServerSetting.DefaultSmallModelEnabled {
					err = errors.New(cond.DEFAULT_SMALL_MODEL_ENABLED_FALSE_ERROR)
					span.SetStatus(codes.Error, err.Error())
					return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
						berrors.BknBackend_Metric_InternalError).
						WithErrorDetails(err.Error())
				}
				dftModel, err := ms.mfs.GetDefaultModel(ctx)
				if err != nil {
					return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
						berrors.BknBackend_Metric_InternalError).
						WithErrorDetails(err.Error())
				}
				result, err := ms.mfs.GetVector(ctx, dftModel, []string{word})
				if err != nil {
					logger.Errorf("GetVector error: %s", err.Error())
					span.SetStatus(codes.Error, "vector embedding failed")
					return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
						berrors.BknBackend_Metric_InternalError).
						WithErrorDetails(err.Error())
				}
				return result, nil
			})
		if err != nil {
			logger.Errorf("convert metric condition to filter condition failed: %v", err)
			return response, rest.NewHTTPError(ctx, http.StatusBadRequest,
				berrors.BknBackend_InvalidParameter_Condition).
				WithErrorDetails(i18n.Translate(rest.GetLanguageByCtx(ctx), "BknBackend.Validation.Detail.ConditionDecodeFailed", nil))
		}
	}
	if permission.KNChildResourcePEPEnabled() {
		filterCondition = permission.RestrictDatasetFilterToIDs(filterCondition, visibleIDs)
	}

	otIDMap := map[string]bool{}
	otIDs := []string{}
	if len(query.ConceptGroups) > 0 {
		cgCnt, err := ms.cga.GetConceptGroupsTotal(ctx, interfaces.ConceptGroupsQueryParams{
			KNID:   query.KNID,
			Branch: query.Branch,
			CGIDs:  query.ConceptGroups,
		})
		if err != nil {
			logger.Errorf("GetConceptGroupsTotal in knowledge network[%s] error: %s", query.KNID, err.Error())
			span.SetStatus(codes.Error, fmt.Sprintf("GetConceptGroupsTotal in knowledge network[%s], error: %v", query.KNID, err))

			return response, rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_Metric_InternalError).WithErrorDetails(err.Error())
		}
		if cgCnt == 0 {
			errStr := fmt.Sprintf("all concept group not found, expect concept group nums is [%d], actual concept group num is [%d]",
				cgCnt, len(query.ConceptGroups))
			logger.Errorf(errStr)

			// Return 404 when none of the requested concept groups exists.
			return response, rest.NewHTTPError(ctx, http.StatusNotFound,
				berrors.BknBackend_ConceptGroup_ConceptGroupNotFound).
				WithErrorDetails(errStr)
		}

		otIDArr, err := ms.cga.GetConceptIDsByConceptGroupIDs(ctx, query.KNID,
			query.Branch, query.ConceptGroups, interfaces.MODULE_TYPE_OBJECT_TYPE)
		if err != nil {
			errStr := fmt.Sprintf("GetConceptIDsByConceptGroupIDs failed, kn_id:[%s],branch:[%s],cg_ids:[%v], error: %v",
				query.KNID, query.Branch, query.ConceptGroups, err)
			logger.Errorf(errStr)
			span.SetStatus(codes.Error, errStr)
			return response, rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_Metric_InternalError).WithErrorDetails(err.Error())
		}

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

	if query.NeedTotal {
		if len(otIDMap) == 0 {
			total, err := ms.getMetricDatasetTotal(ctx, filterCondition)
			if err != nil {
				return response, err
			}
			response.TotalCount = total
		} else {
			total, err := ms.getTotalWithLargeScopeRefs(ctx, filterCondition, otIDs)
			if err != nil {
				return response, err
			}
			response.TotalCount = total
		}
	}

	entries := make([]*interfaces.MetricDefinition, 0)
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
		params := &interfaces.ResourceDataQueryParams{
			FilterCondition: filterCondition,
			Paging:          paging,
			NeedTotal:       false,
			Sort:            sort,
		}
		datasetResp, err := ms.vbs.QueryResourceData(ctx, interfaces.BKN_DATASET_ID, params)
		if err != nil {
			logger.Errorf("metric concept search query QueryResourceData error: %s", err.Error())
			span.SetStatus(codes.Error, "metric concept search query failed")
			return response, rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_Metric_InternalError).
				WithErrorDetails(err.Error())
		}

		if len(datasetResp.Entries) == 0 {
			break
		}

		for _, entry := range datasetResp.Entries {
			jsonByte, err := json.Marshal(entry)
			if err != nil {
				return response, rest.NewHTTPError(ctx, http.StatusBadRequest,
					berrors.BknBackend_InternalError_MarshalDataFailed).
					WithErrorDetails(fmt.Sprintf("failed to Marshal dataset entry, %s", err.Error()))
			}
			var m interfaces.MetricDefinition
			if err := json.Unmarshal(jsonByte, &m); err != nil {
				return response, rest.NewHTTPError(ctx, http.StatusBadRequest,
					berrors.BknBackend_InternalError_UnMarshalDataFailed).
					WithErrorDetails(fmt.Sprintf("failed to Unmarshal dataset entry to MetricDefinition, %s", err.Error()))
			}
			if scoreVal, ok := entry["_score"]; ok {
				if score, err := common.AnyToFloat64(scoreVal); err == nil {
					m.Score = &score
				}
			}
			m.Vector = nil

			if len(otIDMap) == 0 || otIDMap[m.ScopeRef] {
				entries = append(entries, &m)
				if query.Limit > 0 && len(entries) >= query.Limit {
					break
				}
			}
		}

		nextCursor = nil
		if datasetResp.Paging != nil {
			nextCursor = datasetResp.Paging.NextCursor
		}

		if query.Limit > 0 && len(entries) >= query.Limit {
			break
		}
		if nextCursor == nil {
			break
		}
		cursor = *nextCursor
	}

	response.Entries = entries
	response.NextCursor = nextCursor
	span.SetStatus(codes.Ok, "")
	return response, nil
}

// getMetricDatasetTotal returns total document count for the metric concept query (same pattern as object_type.GetTotal).
func (ms *metricService) getMetricDatasetTotal(ctx context.Context, filterCondition map[string]any) (int64, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "logic layer: search metric concept total")
	defer span.End()

	params := &interfaces.ResourceDataQueryParams{
		FilterCondition: filterCondition,
		Paging: interfaces.ResourceDataPagingRequest{
			Mode:  "single",
			Limit: 1,
		},
		NeedTotal: true,
	}
	datasetResp, err := ms.vbs.QueryResourceData(ctx, interfaces.BKN_DATASET_ID, params)
	if err != nil {
		span.SetStatus(codes.Error, "Search total metric documents count failed")
		return 0, rest.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_Metric_InternalError).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	if datasetResp == nil {
		return 0, nil
	}
	return datasetResp.TotalCount, nil
}

func (ms *metricService) getTotalWithScopeRefs(ctx context.Context, filterCondition map[string]any, scopeRefs []string) (int64, error) {
	srCondition := map[string]any{
		"field":      "scope_ref",
		"operation":  "in",
		"value":      scopeRefs,
		"value_from": "const",
	}

	var combinedCondition map[string]any
	if filterCondition == nil {
		combinedCondition = srCondition
	} else {
		combinedCondition = map[string]any{
			"operation": "and",
			"sub_conditions": []map[string]any{
				filterCondition,
				srCondition,
			},
		}
	}

	return ms.getMetricDatasetTotal(ctx, combinedCondition)
}

func (ms *metricService) getTotalWithLargeScopeRefs(ctx context.Context, filterCondition map[string]any, otIDs []string) (int64, error) {
	total := int64(0)
	for i := 0; i < len(otIDs); i += interfaces.GET_TOTAL_CONCEPTID_BATCH_SIZE {
		end := i + interfaces.GET_TOTAL_CONCEPTID_BATCH_SIZE
		if end > len(otIDs) {
			end = len(otIDs)
		}

		batchIDs := otIDs[i:end]
		batchTotal, err := ms.getTotalWithScopeRefs(ctx, filterCondition, batchIDs)
		if err != nil {
			return 0, err
		}

		total += batchTotal
	}

	return total, nil
}

func (ms *metricService) validateMetricStrictExternalDeps(ctx context.Context, tx *sql.Tx, metric *interfaces.MetricDefinition) error {
	scopeRef := strings.TrimSpace(metric.ScopeRef)
	if scopeRef == "" {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Metric_InvalidParameter).
			WithErrorDetails(metricInvalidParameterDetail(ctx, "ScopeRefRequired", nil))
	}

	ot, err := ms.ots.GetObjectTypeByID(ctx, tx, metric.KnID, metric.Branch, scopeRef)
	if err != nil {
		return err
	}
	if ot == nil {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Metric_InvalidParameter).
			WithErrorDetails(metricInvalidParameterDetail(ctx, "ScopeObjectTypeNotFound", map[string]any{"metricID": metric.ID, "scopeRef": scopeRef}))
	}
	batchindex.EnsureObjectTypePropertyMap(ot)
	return ms.validateMetricAgainstResolvedOT(ctx, metric, ot, scopeRef)
}

func (ms *metricService) validateMetricStrictExternalDepsFromBatch(ctx context.Context, metric *interfaces.MetricDefinition, batch *interfaces.BatchIDIndex) error {
	if batch == nil {
		return ms.validateMetricStrictExternalDeps(ctx, nil, metric)
	}
	scopeRef := strings.TrimSpace(metric.ScopeRef)
	if scopeRef == "" {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Metric_InvalidParameter).
			WithErrorDetails(metricInvalidParameterDetail(ctx, "ScopeRefRequired", nil))
	}
	ot := batch.ObjectTypes[scopeRef]
	if ot == nil {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Metric_InvalidParameter).
			WithErrorDetails(metricInvalidParameterDetail(ctx, "ScopeObjectTypeMissingFromBatch", map[string]any{"metricID": metric.ID, "scopeRef": scopeRef}))
	}
	batchindex.EnsureObjectTypePropertyMap(ot)
	return ms.validateMetricAgainstResolvedOT(ctx, metric, ot, scopeRef)
}

func (ms *metricService) validateMetricAgainstResolvedOT(ctx context.Context, metric *interfaces.MetricDefinition, ot *interfaces.ObjectType, scopeRef string) error {
	ds := ot.DataSource
	if ds == nil || strings.TrimSpace(ds.ID) == "" {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Metric_InvalidParameter).
			WithErrorDetails(metricInvalidParameterDetail(ctx, "ScopeDataSourceIDRequired", map[string]any{"metricID": metric.ID}))
	}
	dsType := strings.TrimSpace(ds.Type)
	if dsType == "" {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Metric_InvalidParameter).
			WithErrorDetails(metricInvalidParameterDetail(ctx, "ScopeDataSourceTypeRequired", map[string]any{"metricID": metric.ID}))
	}
	if dsType != interfaces.DATA_SOURCE_TYPE_RESOURCE {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Metric_InvalidParameter).
			WithErrorDetails(metricInvalidParameterDetail(ctx, "ScopeDataSourceTypeUnsupported", map[string]any{"metricID": metric.ID, "type": ds.Type}))
	}

	propertyMap := map[string]*interfaces.DataProperty{}
	for _, prop := range ot.DataProperties {
		propertyMap[prop.Name] = prop
	}

	// Validate that every property referenced by the metric belongs to the scope object type.
	// 1. When a time dimension is present, its property must belong to the object type.
	if metric.TimeDimension != nil {
		if p := strings.TrimSpace(metric.TimeDimension.Property); p != "" {
			if _, ok := propertyMap[p]; !ok {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Metric_InvalidParameter).
					WithErrorDetails(metricInvalidParameterDetail(ctx, "TimeDimensionPropertyNotFound", map[string]any{"metricID": metric.ID, "property": p, "scopeRef": scopeRef}))
			}
		}
	}

	// 2. When analysis dimensions are present, each property must belong to the object type.
	for i, ad := range metric.AnalysisDimensions {
		if n := strings.TrimSpace(ad.Name); n != "" {
			if _, ok := propertyMap[n]; !ok {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Metric_InvalidParameter).
					WithErrorDetails(metricInvalidParameterDetail(ctx, "AnalysisDimensionPropertyNotFound", map[string]any{"metricID": metric.ID, "index": i, "property": n, "scopeRef": scopeRef}))
			}
		}
	}

	// 3. Fields referenced by the calculation formula condition must exist on the object type.
	if metric.CalculationFormula == nil {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Metric_InvalidParameter).
			WithErrorDetails(metricInvalidParameterDetail(ctx, "CalculationFormulaRequiredForMetric", map[string]any{"metricID": metric.ID}))
	}

	// 3. Recursively validate all fields in calculation_formula.condition, including and/or/knn,
	// multi_match.fields, and leaf-condition fields.
	if metric.CalculationFormula.Condition != nil {
		if err := validateConditionFieldsReferenceObjectType(ctx, metric.CalculationFormula.Condition, propertyMap, metric.ID); err != nil {
			return err
		}
	}

	// 4. The aggregation property must exist on the object type.
	if metric.CalculationFormula.Aggregation.Aggr != "" {
		if _, ok := propertyMap[metric.CalculationFormula.Aggregation.Property]; !ok {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Metric_InvalidParameter).
				WithErrorDetails(metricInvalidParameterDetail(ctx, "AggregationPropertyNotFound", map[string]any{"metricID": metric.ID, "property": metric.CalculationFormula.Aggregation.Property, "scopeRef": scopeRef}))
		}
	}

	// 5. Each group-by property must exist on the object type.
	if metric.CalculationFormula.GroupBy != nil {
		for i, g := range metric.CalculationFormula.GroupBy {
			if _, ok := propertyMap[g.Property]; !ok {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Metric_InvalidParameter).
					WithErrorDetails(metricInvalidParameterDetail(ctx, "GroupByPropertyNotFound", map[string]any{"metricID": metric.ID, "index": i, "property": g.Property, "scopeRef": scopeRef}))
			}
		}
	}
	// 6. Each order-by property must exist on the object type.
	if metric.CalculationFormula.OrderBy != nil {
		for i, o := range metric.CalculationFormula.OrderBy {
			if _, ok := propertyMap[o.Property]; !ok {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Metric_InvalidParameter).
					WithErrorDetails(metricInvalidParameterDetail(ctx, "OrderByPropertyNotFound", map[string]any{"metricID": metric.ID, "index": i, "property": o.Property, "scopeRef": scopeRef}))
			}
		}
	}

	// 7. The having filter may only reference the __value field.
	if metric.CalculationFormula.Having != nil {
		if metric.CalculationFormula.Having.Field != interfaces.MetricHavingFieldValue {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Metric_InvalidParameter).
				WithErrorDetails(metricInvalidParameterDetail(ctx, "HavingFieldInvalid", map[string]any{"metricID": metric.ID, "expected": interfaces.MetricHavingFieldValue, "actual": metric.CalculationFormula.Having.Field}))
		}
	}
	return nil
}

func validateConditionFieldsReferenceObjectType(ctx context.Context, cfg *cond.CondCfg, propertyMap map[string]*interfaces.DataProperty, metricID string) error {
	if cfg == nil {
		return nil
	}

	switch cfg.Operation {
	case cond.OperationAnd, cond.OperationOr:
		for _, s := range cfg.SubConds {
			if err := validateConditionFieldsReferenceObjectType(ctx, s, propertyMap, metricID); err != nil {
				return err
			}
		}
		return nil
	default:
		n := strings.TrimSpace(cfg.Field)
		if n == "" {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Metric_InvalidParameter).
				WithErrorDetails(metricInvalidParameterDetail(ctx, "ConditionPropertyRequired", map[string]any{"metricID": metricID}))
		}

		if _, ok := propertyMap[n]; !ok {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_Metric_InvalidParameter).
				WithErrorDetails(metricInvalidParameterDetail(ctx, "ConditionPropertyNotFound", map[string]any{"metricID": metricID, "property": n}))
		}
		return nil
	}
}
