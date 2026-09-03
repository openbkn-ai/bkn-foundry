// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package knowledge_network

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bytedance/sonic"
	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"go.opentelemetry.io/otel/codes"

	bknsdk "bkn-backend/bkn-specification/bkn"
	"bkn-backend/common"
	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
	"bkn-backend/logics"
	"bkn-backend/logics/action_type"
	"bkn-backend/logics/batchindex"
	"bkn-backend/logics/concept_group"
	"bkn-backend/logics/metric"
	"bkn-backend/logics/model_factory"
	"bkn-backend/logics/object_type"
	"bkn-backend/logics/permission"
	"bkn-backend/logics/relation_type"
	"bkn-backend/logics/risk_type"
	"bkn-backend/logics/user_mgmt"
	"bkn-backend/logics/vega_backend"
)

var (
	knServiceOnce sync.Once
	knService     interfaces.KNService
)

type knowledgeNetworkService struct {
	appSetting *common.AppSetting
	db         *sql.DB
	ata        interfaces.ActionTypeAccess
	ats        interfaces.ActionTypeService
	cga        interfaces.ConceptGroupAccess
	cgs        interfaces.ConceptGroupService
	kna        interfaces.KNAccess
	ma         interfaces.MetricAccess
	ms         interfaces.MetricService
	mfs        interfaces.ModelFactoryService
	ota        interfaces.ObjectTypeAccess
	ots        interfaces.ObjectTypeService
	rta        interfaces.RelationTypeAccess
	riskTypeA  interfaces.RiskTypeAccess
	riskTypeS  interfaces.RiskTypeService
	ps         interfaces.PermissionService
	rts        interfaces.RelationTypeService
	ums        interfaces.UserMgmtService
	vbs        interfaces.VegaBackendService
}

func NewKNService(appSetting *common.AppSetting) interfaces.KNService {
	knServiceOnce.Do(func() {
		knService = &knowledgeNetworkService{
			appSetting: appSetting,
			ata:        logics.ATA,
			ats:        action_type.NewActionTypeService(appSetting),
			cga:        logics.CGA,
			cgs:        concept_group.NewConceptGroupService(appSetting),
			db:         logics.DB,
			kna:        logics.KNA,
			ma:         logics.MA,
			ms:         metric.NewMetricService(appSetting),
			mfs:        model_factory.NewModelFactoryService(appSetting, logics.MFA),
			ota:        logics.OTA,
			ots:        object_type.NewObjectTypeService(appSetting),
			ps:         permission.NewPermissionService(appSetting),
			rta:        logics.RTA,
			riskTypeA:  logics.RiskTypeAccess,
			riskTypeS:  risk_type.NewRiskTypeService(appSetting),
			rts:        relation_type.NewRelationTypeService(appSetting),
			ums:        user_mgmt.NewUserMgmtService(appSetting),
			vbs:        vega_backend.NewVegaBackendService(appSetting, logics.VBA),
		}
	})
	return knService
}

func (kns *knowledgeNetworkService) CheckKNExistByID(ctx context.Context, KNID string, branch string) (string, bool, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, fmt.Sprintf("校验业务知识网络[%v]的存在性", KNID))
	defer span.End()

	otName, exist, err := kns.kna.CheckKNExistByID(ctx, KNID, branch)
	if err != nil {
		otellog.LogError(ctx, fmt.Sprintf("按ID[%v]获取业务知识网络失败", KNID), err)
		return "", exist, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_KnowledgeNetwork_InternalError_CheckKNIfExistFailed).WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return otName, exist, nil
}

func (kns *knowledgeNetworkService) CheckKNExistByName(ctx context.Context, knName string, branch string) (string, bool, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, fmt.Sprintf("校验业务知识网络[%v]的存在性", knName))
	defer span.End()

	KNID, exist, err := kns.kna.CheckKNExistByName(ctx, knName, branch)
	if err != nil {
		otellog.LogError(ctx, fmt.Sprintf("按名称[%v]获取业务知识网络失败", knName), err)
		return KNID, exist, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_KnowledgeNetwork_InternalError_CheckKNIfExistFailed).WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return KNID, exist, nil
}

func (kns *knowledgeNetworkService) CreateKN(ctx context.Context, kn *interfaces.KN, mode string, strictMode bool) (id string, err error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Create knowledge network")
	defer span.End()
	ctx, parentTracker, trackerOwner := permission.WithResourceParentTracker(ctx)
	defer func() {
		if trackerOwner && err != nil {
			_ = parentTracker.Cleanup(ctx, kns.ps)
		}
	}()
	ctx, policyTracker, policyTrackerOwner := permission.WithCreatedPolicyTracker(ctx)
	defer func() {
		if policyTrackerOwner && err != nil {
			_ = policyTracker.Cleanup(ctx, kns.ps)
		}
	}()
	var createdNewKN bool
	var datasetWritten bool
	defer func() {
		if err == nil || !createdNewKN {
			return
		}
		cleanupCtx := context.WithoutCancel(ctx)
		if datasetWritten {
			filterCondition := map[string]any{
				"operation": "and",
				"sub_conditions": []map[string]any{
					{
						"field": "kn_id", "operation": "==", "value": kn.KNID, "value_from": "const",
					},
					{
						"field": "branch", "operation": "==", "value": kn.Branch, "value_from": "const",
					},
				},
			}
			if cleanupErr := kns.vbs.DeleteDatasetDocumentsByQuery(cleanupCtx,
				interfaces.BKN_DATASET_ID, filterCondition); cleanupErr != nil {
				otellog.LogError(cleanupCtx, "CreateKN dataset compensation failed", cleanupErr)
			}
		}
	}()

	// Check whether the user ID can create business knowledge networks through policy evaluation.
	err = kns.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: interfaces.RESOURCE_TYPE_KN,
		ID:   interfaces.RESOURCE_ID_ALL,
	}, []string{interfaces.OPERATION_TYPE_CREATE})
	if err != nil {
		return "", err
	}

	currentTime := time.Now().UnixMilli()
	// Generate a distributed ID when the submitted model ID is empty.
	if kn.KNID == "" {
		generatedID, generateErr := uuid.NewV7()
		if generateErr != nil {
			return "", fmt.Errorf("generate knowledge network UUIDv7: %w", generateErr)
		}
		kn.KNID = generatedID.String()
	}
	for _, conceptGroup := range kn.ConceptGroups {
		conceptGroup.KNID = kn.KNID
		conceptGroup.Branch = kn.Branch
	}
	for _, objectType := range kn.ObjectTypes {
		objectType.KNID = kn.KNID
		objectType.Branch = kn.Branch
	}
	for _, relationType := range kn.RelationTypes {
		relationType.KNID = kn.KNID
		relationType.Branch = kn.Branch
	}
	for _, actionType := range kn.ActionTypes {
		actionType.KNID = kn.KNID
		actionType.Branch = kn.Branch
	}
	for _, riskType := range kn.RiskTypes {
		riskType.KNID = kn.KNID
		riskType.Branch = kn.Branch
	}
	for _, m := range kn.Metrics {
		if m == nil {
			continue
		}
		m.KnID = kn.KNID
		m.Branch = kn.Branch
	}

	accountInfo := interfaces.AccountInfo{}
	if ctx.Value(interfaces.ACCOUNT_INFO_KEY) != nil {
		accountInfo = ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	}
	kn.Creator = accountInfo
	kn.Updater = accountInfo

	kn.CreateTime = currentTime
	kn.UpdateTime = currentTime

	bknNetwork := logics.ToBKNNetWork(kn)
	kn.BKNRawContent = bknsdk.SerializeBknNetwork(bknNetwork)

	// 0. Begin the transaction.
	tx, err := kns.db.Begin()
	if err != nil {
		otellog.LogError(ctx, "Begin transaction error", err)
		return "", rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_KnowledgeNetwork_InternalError_BeginTransactionFailed).
			WithErrorDetails(err.Error())
	}

	// 0.1 On failure.
	defer func() {
		switch err {
		case nil:
			// Commit the transaction.
			err = tx.Commit()
			if err != nil {
				otellog.LogError(ctx, "CreateKN Transaction Commit Failed", err)
				return
			}
			otellog.LogDebug(ctx, "CreateKN Transaction Commit Success")
		default:
			rollbackErr := tx.Rollback()
			if rollbackErr != nil {
				otellog.LogError(ctx, "CreateKN Transaction Rollback Error", err)
			}
		}
	}()

	// Process import mode.
	isCreate, isUpdate, err := kns.handleKNImportMode(ctx, mode, kn)
	if err != nil {
		return "", err
	}
	createdNewKN = isCreate
	if isCreate {
		ctx = permission.WithKNImportPermissionPrechecked(ctx)
	}

	// Process creation.
	if isCreate {
		err = kns.kna.CreateKN(ctx, tx, kn)
		if err != nil {
			logger.Errorf("CreateKN error: %s", err.Error())
			span.SetStatus(codes.Error, "创建业务知识网络失败")

			return "", rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_KnowledgeNetwork_InternalError_CreateKNFailed).
				WithErrorDetails(err.Error())
		}

		// Import concept groups.
		if len(kn.ConceptGroups) > 0 {
			for _, cg := range kn.ConceptGroups {
				_, err = kns.cgs.CreateConceptGroup(ctx, tx, cg, mode, strictMode)
				if err != nil {
					logger.Errorf("CreateObjectTypes error: %s", err.Error())
					span.SetStatus(codes.Error, "创建业务知识网络概念分组失败")
					return "", rest.NewHTTPError(ctx, http.StatusInternalServerError,
						berrors.BknBackend_KnowledgeNetwork_InternalError_CreateObjectTypesFailed).
						WithErrorDetails(err.Error())
				}
			}
		}

		if len(kn.ObjectTypes) > 0 {
			_, err = kns.ots.CreateObjectTypes(ctx, tx, kn.ObjectTypes, mode, true, strictMode)
			if err != nil {
				logger.Errorf("CreateObjectTypes error: %s", err.Error())
				span.SetStatus(codes.Error, "创建业务知识网络对象类失败")
				return "", rest.NewHTTPError(ctx, http.StatusInternalServerError,
					berrors.BknBackend_KnowledgeNetwork_InternalError_CreateObjectTypesFailed).
					WithErrorDetails(err.Error())
			}
		}

		if len(kn.RelationTypes) > 0 {
			_, err = kns.rts.CreateRelationTypes(ctx, tx, kn.RelationTypes, mode, strictMode)
			if err != nil {
				logger.Errorf("CreateRelationTypes error: %s", err.Error())
				span.SetStatus(codes.Error, "创建业务知识网络关系类失败")
				return "", rest.NewHTTPError(ctx, http.StatusInternalServerError,
					berrors.BknBackend_KnowledgeNetwork_InternalError_CreateRelationTypesFailed).
					WithErrorDetails(err.Error())
			}
		}

		if len(kn.ActionTypes) > 0 {
			_, err = kns.ats.CreateActionTypes(ctx, tx, kn.ActionTypes, mode, strictMode)
			if err != nil {
				logger.Errorf("CreateActionTypes error: %s", err.Error())
				span.SetStatus(codes.Error, "创建业务知识网络动作类失败")
				return "", rest.NewHTTPError(ctx, http.StatusInternalServerError,
					berrors.BknBackend_KnowledgeNetwork_InternalError_CreateActionTypesFailed).
					WithErrorDetails(err.Error())
			}
		}

		if len(kn.RiskTypes) > 0 {
			_, err = kns.riskTypeS.CreateRiskTypes(ctx, tx, kn.RiskTypes, mode)
			if err != nil {
				logger.Errorf("CreateRiskTypes error: %s", err.Error())
				span.SetStatus(codes.Error, "创建业务知识网络风险类失败")
				return "", rest.NewHTTPError(ctx, http.StatusInternalServerError,
					berrors.BknBackend_RiskType_InternalError).
					WithErrorDetails(err.Error())
			}
		}

		if len(kn.Metrics) > 0 {
			_, err = kns.ms.CreateMetrics(ctx, tx, kn.Metrics, strictMode, mode)
			if err != nil {
				logger.Errorf("CreateMetrics error: %s", err.Error())
				span.SetStatus(codes.Error, "创建业务知识网络指标失败")
				return "", err
			}
		}
	}

	// Process updates.
	if isUpdate {
		// TODO: increment the version when updating an existing submitted item.
		err = kns.UpdateKN(ctx, tx, kn, strictMode)
		if err != nil {
			logger.Errorf("UpdateKN error: %s", err.Error())
			span.SetStatus(codes.Error, "修改业务知识网络失败")
			return "", rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_KnowledgeNetwork_InternalError_UpdateKNFailed).
				WithErrorDetails(err.Error())
		}

		if len(kn.ConceptGroups) > 0 {
			for _, cg := range kn.ConceptGroups {
				_, err = kns.cgs.CreateConceptGroup(ctx, tx, cg, mode, strictMode)
				if err != nil {
					logger.Errorf("CreateObjectTypes error: %s", err.Error())
					span.SetStatus(codes.Error, "创建业务知识网络概念分组失败")
					return "", rest.NewHTTPError(ctx, http.StatusInternalServerError,
						berrors.BknBackend_KnowledgeNetwork_InternalError_CreateObjectTypesFailed).
						WithErrorDetails(err.Error())
				}
			}
		}

		if len(kn.ObjectTypes) > 0 {
			_, err = kns.ots.CreateObjectTypes(ctx, tx, kn.ObjectTypes, mode, true, strictMode)
			if err != nil {
				logger.Errorf("CreateObjectTypes error: %s", err.Error())
				span.SetStatus(codes.Error, "创建业务知识网络对象类失败")
				return "", rest.NewHTTPError(ctx, http.StatusInternalServerError,
					berrors.BknBackend_KnowledgeNetwork_InternalError_CreateObjectTypesFailed).
					WithErrorDetails(err.Error())
			}
		}

		if len(kn.RelationTypes) > 0 {
			_, err = kns.rts.CreateRelationTypes(ctx, tx, kn.RelationTypes, mode, strictMode)
			if err != nil {
				logger.Errorf("CreateRelationTypes error: %s", err.Error())
				span.SetStatus(codes.Error, "创建业务知识网络关系类失败")
				return "", rest.NewHTTPError(ctx, http.StatusInternalServerError,
					berrors.BknBackend_KnowledgeNetwork_InternalError_CreateRelationTypesFailed).
					WithErrorDetails(err.Error())
			}
		}

		if len(kn.ActionTypes) > 0 {
			_, err = kns.ats.CreateActionTypes(ctx, tx, kn.ActionTypes, mode, strictMode)
			if err != nil {
				logger.Errorf("CreateActionTypes error: %s", err.Error())
				span.SetStatus(codes.Error, "创建业务知识网络动作类失败")
				return "", rest.NewHTTPError(ctx, http.StatusInternalServerError,
					berrors.BknBackend_KnowledgeNetwork_InternalError_CreateActionTypesFailed).
					WithErrorDetails(err.Error())
			}
		}

		if len(kn.RiskTypes) > 0 {
			_, err = kns.riskTypeS.CreateRiskTypes(ctx, tx, kn.RiskTypes, mode)
			if err != nil {
				logger.Errorf("CreateRiskTypes error: %s", err.Error())
				span.SetStatus(codes.Error, "创建业务知识网络风险类失败")
				return "", rest.NewHTTPError(ctx, http.StatusInternalServerError,
					berrors.BknBackend_RiskType_InternalError).
					WithErrorDetails(err.Error())
			}
		}

		if len(kn.Metrics) > 0 {
			_, err = kns.ms.CreateMetrics(ctx, tx, kn.Metrics, strictMode, mode)
			if err != nil {
				logger.Errorf("CreateMetrics error: %s", err.Error())
				span.SetStatus(codes.Error, "创建业务知识网络指标失败")
				return "", err
			}
		}
	}

	if isCreate || isUpdate {
		err = kns.InsertDatasetData(ctx, kn)
		if err != nil {
			logger.Errorf("InsertDatasetData error: %s", err.Error())
			span.SetStatus(codes.Error, "业务知识网络概念索引写入失败")

			return "", rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_KnowledgeNetwork_InternalError_InsertOpenSearchDataFailed).
				WithErrorDetails(err.Error())
		}
		if isCreate {
			datasetWritten = true
		}
	}

	// Register resource policies last and only during creation.
	if isCreate {
		resources := []interfaces.PermissionResource{{
			ID:   kn.KNID,
			Type: interfaces.RESOURCE_TYPE_KN,
			Name: kn.KNName,
		}}
		permission.TrackCreatedPolicies(ctx, resources)
		err = kns.ps.CreateResources(ctx, resources, interfaces.KN_CREATOR_OPERATIONS)
		if err != nil {
			logger.Errorf("CreateResources error: %s", err.Error())
			span.SetStatus(codes.Error, "创建业务知识网络资源失败")
			return "", rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_KnowledgeNetwork_InternalError_CreateResourcesFailed).
				WithErrorDetails(err.Error())
		}

	}

	span.SetStatus(codes.Ok, "")
	return kn.KNID, nil
}

// ValidateKN checks whole-KN dependency existence only; does not write to the database.
func (kns *knowledgeNetworkService) ValidateKN(ctx context.Context, kn *interfaces.KN, strictMode bool, mode string) error {

	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "ValidateKN")
	defer span.End()

	if kn == nil {
		span.SetStatus(codes.Ok, "")
		return nil
	}

	knID := kn.KNID
	branch := kn.Branch
	if branch == "" {
		branch = interfaces.MAIN_BRANCH
	}
	kn.Branch = branch

	// Process import mode.
	_, _, err := kns.handleKNImportMode(ctx, mode, kn)
	if err != nil {
		return err
	}

	batch, err := batchindex.CollectKNFromPayload(kn)
	if err != nil {
		return rest.NewHTTPError(ctx, http.StatusBadRequest,
			berrors.BknBackend_KnowledgeNetwork_InvalidParameter).
			WithErrorDetails(err.Error())
	}

	if len(kn.ConceptGroups) > 0 {
		if err := kns.cgs.ValidateConceptGroups(ctx, knID, branch, kn.ConceptGroups, strictMode, batch, mode); err != nil {
			return err
		}
	}
	if len(kn.ObjectTypes) > 0 {
		if err := kns.ots.ValidateObjectTypes(ctx, knID, branch, kn.ObjectTypes, strictMode, batch, mode); err != nil {
			return err
		}
	}
	if len(kn.RelationTypes) > 0 {
		if err := kns.rts.ValidateRelationTypes(ctx, knID, branch, kn.RelationTypes, strictMode, batch, mode); err != nil {
			return err
		}
	}
	if len(kn.ActionTypes) > 0 {
		if err := kns.ats.ValidateActionTypes(ctx, knID, branch, kn.ActionTypes, strictMode, batch, mode); err != nil {
			return err
		}
	}
	if len(kn.Metrics) > 0 {
		if err := kns.ms.ValidateMetrics(ctx, kn.Metrics, strictMode, mode, batch); err != nil {
			return err
		}
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

func (kns *knowledgeNetworkService) ListKNs(ctx context.Context, parameter interfaces.KNsQueryParams) ([]*interfaces.KN, int, error) {

	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "查询业务知识网络列表")
	defer span.End()

	candidateQuery := parameter
	candidateQuery.OnlyIDs = true
	candidateQuery.Limit = -1
	candidateQuery.Offset = 0
	KNArr, err := kns.kna.ListKNs(ctx, candidateQuery)
	if err != nil {
		logger.Errorf("ListKNs error: %s", err.Error())
		span.SetStatus(codes.Error, "List knowledge networks error")

		return []*interfaces.KN{}, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_KnowledgeNetwork_InternalError).WithErrorDetails(err.Error())
	}
	if len(KNArr) == 0 {
		span.SetStatus(codes.Ok, "")
		return []*interfaces.KN{}, 0, nil
	}

	// Process resource IDs.
	KNIDs := make([]string, 0)
	for _, m := range KNArr {
		KNIDs = append(KNIDs, m.KNID)
	}

	// Filter objects by view permission. The filtered length is the total, so no separate total query is needed.
	matchResoucesMap, err := kns.ps.FilterResources(ctx, interfaces.RESOURCE_TYPE_KN, KNIDs,
		[]string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, true, interfaces.COMMON_OPERATIONS)
	if err != nil {
		span.SetStatus(codes.Error, "Filter resources error")
		return []*interfaces.KN{}, 0, err
	}

	visibleKNIDs := make([]string, 0, len(KNArr))
	for _, kn := range KNArr {
		if _, exist := matchResoucesMap[kn.KNID]; exist {
			visibleKNIDs = append(visibleKNIDs, kn.KNID)
		}
	}
	total := len(visibleKNIDs)

	if total == 0 {
		span.SetStatus(codes.Ok, "")
		return []*interfaces.KN{}, 0, nil
	}

	if parameter.Limit == 0 {
		span.SetStatus(codes.Ok, "")
		return []*interfaces.KN{}, total, nil
	}
	if parameter.Limit != -1 {
		if parameter.Offset < 0 || parameter.Offset >= total {
			span.SetStatus(codes.Ok, "")
			return []*interfaces.KN{}, total, nil
		}
		end := parameter.Offset + parameter.Limit
		if end > total {
			end = total
		}
		visibleKNIDs = visibleKNIDs[parameter.Offset:end]
	}

	detailQuery := parameter
	detailQuery.CandidateIDs = visibleKNIDs
	detailQuery.Limit = -1
	detailQuery.Offset = 0
	if parameter.Limit > 0 {
		detailQuery.Limit = len(visibleKNIDs)
	}
	KNs, err := kns.kna.ListKNs(ctx, detailQuery)
	if err != nil {
		logger.Errorf("ListKNs detail error: %s", err.Error())
		span.SetStatus(codes.Error, "List knowledge network details error")
		return []*interfaces.KN{}, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_KnowledgeNetwork_InternalError).WithErrorDetails(err.Error())
	}
	for _, kn := range KNs {
		kn.Operations = matchResoucesMap[kn.KNID].Operations
	}

	accountInfos := make([]*interfaces.AccountInfo, 0, len(KNs)*2)
	for _, kn := range KNs {
		accountInfos = append(accountInfos, &kn.Creator, &kn.Updater)
	}

	err = kns.ums.GetAccountNames(ctx, accountInfos)
	if err != nil {
		span.SetStatus(codes.Error, "GetAccountNames error")

		return []*interfaces.KN{}, 0, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_KnowledgeNetwork_InternalError).WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return KNs, total, nil
}

// GetKNNamesByIDs resolves knowledge network names in bulk for object-level authorization display.
// Do not use FilterResources here because authorization pages must display names referenced by objects even when users lack access.
// Perform only a lightweight name query. Skip missing IDs, return empty entries for empty input, and de-duplicate IDs.
func (kns *knowledgeNetworkService) GetKNNamesByIDs(ctx context.Context, ids []string) (*interfaces.KNBatchNamesResp, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "按ID批量查询业务知识网络名称")
	defer span.End()

	resp := &interfaces.KNBatchNamesResp{Entries: []*interfaces.KNNameEntry{}}

	// De-duplicate and filter empty strings.
	seen := make(map[string]struct{}, len(ids))
	uniqueIDs := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		uniqueIDs = append(uniqueIDs, id)
	}
	if len(uniqueIDs) == 0 {
		span.SetStatus(codes.Ok, "")
		return resp, nil
	}

	entries, err := kns.kna.GetKNNamesByIDs(ctx, uniqueIDs, interfaces.MAIN_BRANCH)
	if err != nil {
		logger.Errorf("GetKNNamesByIDs error: %s", err.Error())
		span.SetStatus(codes.Error, "按ID批量查询业务知识网络名称失败")
		return resp, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_KnowledgeNetwork_InternalError).WithErrorDetails(err.Error())
	}
	resp.Entries = entries

	span.SetStatus(codes.Ok, "")
	return resp, nil
}

func (kns *knowledgeNetworkService) GetKNByID(ctx context.Context, knID string, branch string, mode string) (*interfaces.KN, error) {
	return kns.getKNByID(ctx, knID, branch, mode, true)
}

// ExportKNForProjection is deliberately separate from user-facing reads. The
// caller has already been authorized for this exact network by a signed grant,
// so it must not enter account-based resource filtering or identity enrichment.
func (kns *knowledgeNetworkService) ExportKNForProjection(ctx context.Context, knID string) (*interfaces.KN, error) {
	kn, err := kns.getKNByID(ctx, knID, interfaces.MAIN_BRANCH, "", false)
	if err != nil {
		return nil, err
	}
	query := interfaces.PaginationQueryParameters{Limit: -1}
	kn.ConceptGroups, err = kns.cga.ListConceptGroups(ctx, interfaces.ConceptGroupsQueryParams{PaginationQueryParameters: query, KNID: kn.KNID, Branch: kn.Branch})
	if err != nil {
		return nil, err
	}
	kn.ObjectTypes, err = kns.ota.ListObjectTypes(ctx, nil, interfaces.ObjectTypesQueryParams{PaginationQueryParameters: query, KNID: kn.KNID, Branch: kn.Branch})
	if err != nil {
		return nil, err
	}
	kn.RelationTypes, err = kns.rta.ListRelationTypes(ctx, interfaces.RelationTypesQueryParams{PaginationQueryParameters: query, KNID: kn.KNID, Branch: kn.Branch})
	if err != nil {
		return nil, err
	}
	kn.ActionTypes, err = kns.ata.ListActionTypes(ctx, interfaces.ActionTypesQueryParams{PaginationQueryParameters: query, KNID: kn.KNID, Branch: kn.Branch})
	if err != nil {
		return nil, err
	}
	metrics, err := kns.ma.ListMetrics(ctx, interfaces.MetricsListQueryParams{PaginationQueryParameters: query, KNID: kn.KNID, Branch: kn.Branch})
	if err != nil {
		return nil, err
	}
	kn.Metrics = metrics
	if kns.riskTypeA != nil {
		kn.RiskTypes, err = kns.riskTypeA.GetAllRiskTypesByKnID(ctx, kn.KNID, kn.Branch)
		if err != nil {
			return nil, err
		}
	}
	return kn, nil
}

func (kns *knowledgeNetworkService) getKNByID(ctx context.Context, knID string, branch string, mode string, enforceUserPermission bool) (*interfaces.KN, error) {

	// Get business knowledge networks.
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, fmt.Sprintf("查询业务知识网络[%s]信息", knID))
	defer span.End()

	// Get basic model information.
	kn, err := kns.kna.GetKNByID(ctx, knID, branch)
	if err != nil {
		logger.Errorf("GetKNByID error: %s", err.Error())
		span.SetStatus(codes.Error, fmt.Sprintf("Get knowledge network[%s] error: %v", knID, err))
		span.End()

		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_KnowledgeNetwork_InternalError_GetKNByIDFailed).WithErrorDetails(err.Error())
	}

	if kn == nil {
		errStr := fmt.Sprintf("Knowledge network[%s] not found", knID)
		logger.Errorf(errStr)
		span.SetStatus(codes.Error, errStr)
		span.End()

		return nil, rest.NewHTTPError(ctx, http.StatusNotFound, berrors.BknBackend_KnowledgeNetwork_NotFound).
			WithErrorDetails(errStr)
	}

	if enforceUserPermission {
		// Filter objects by view permission. The filtered length is the total, so no separate total query is needed.
		matchResoucesMap, err := kns.ps.FilterResources(ctx, interfaces.RESOURCE_TYPE_KN, []string{kn.KNID},
			[]string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, true, interfaces.COMMON_OPERATIONS)
		if err != nil {
			span.SetStatus(codes.Error, "Filter resources error")
			return nil, err
		}

		if resrc, exist := matchResoucesMap[kn.KNID]; exist {
			kn.Operations = resrc.Operations // Operations currently allowed for the user
		} else {
			return nil, rest.NewHTTPError(ctx, http.StatusForbidden, rest.PublicError_Forbidden)
		}

		accountInfos := []*interfaces.AccountInfo{&kn.Creator, &kn.Updater}
		err = kns.ums.GetAccountNames(ctx, accountInfos)
		if err != nil {
			span.SetStatus(codes.Error, "GetAccountNames error")

			return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_KnowledgeNetwork_InternalError).WithErrorDetails(err.Error())
		}
	}

	if mode == interfaces.Mode_Export {
		conceptGroups, _, err := kns.cgs.ListConceptGroups(ctx, interfaces.ConceptGroupsQueryParams{
			PaginationQueryParameters: interfaces.PaginationQueryParameters{
				Limit: -1,
			},
			KNID:   kn.KNID,
			Branch: kn.Branch,
		})
		if err != nil {
			return nil, err
		}
		kn.ConceptGroups = conceptGroups

		objectTypes, _, err := kns.ots.ListObjectTypes(ctx, nil, interfaces.ObjectTypesQueryParams{
			PaginationQueryParameters: interfaces.PaginationQueryParameters{
				Limit: -1,
			},
			KNID:   kn.KNID,
			Branch: kn.Branch,
		})
		if err != nil {
			return nil, err
		}
		kn.ObjectTypes = objectTypes

		relationTypes, _, err := kns.rts.ListRelationTypes(ctx, interfaces.RelationTypesQueryParams{
			PaginationQueryParameters: interfaces.PaginationQueryParameters{
				Limit: -1,
			},
			KNID:   kn.KNID,
			Branch: kn.Branch,
		})
		if err != nil {
			return nil, err
		}
		kn.RelationTypes = relationTypes

		actionTypes, _, err := kns.ats.ListActionTypes(ctx, interfaces.ActionTypesQueryParams{
			PaginationQueryParameters: interfaces.PaginationQueryParameters{
				Limit: -1,
			},
			KNID:   kn.KNID,
			Branch: kn.Branch,
		})
		if err != nil {
			return nil, err
		}
		kn.ActionTypes = actionTypes

		if kns.riskTypeA != nil {
			riskTypes, err := kns.riskTypeA.GetAllRiskTypesByKnID(ctx, kn.KNID, kn.Branch)
			if err != nil {
				return nil, err
			}
			kn.RiskTypes = riskTypes
		}

		metricsList, err := kns.ms.ListMetrics(ctx, interfaces.MetricsListQueryParams{
			PaginationQueryParameters: interfaces.PaginationQueryParameters{
				Limit: -1,
			},
			KNID:   kn.KNID,
			Branch: kn.Branch,
		})
		if err != nil {
			return nil, err
		}
		kn.Metrics = metricsList.Entries
	}

	span.SetStatus(codes.Ok, "")
	return kn, nil
}

func (kns *knowledgeNetworkService) GetStatByKN(ctx context.Context, kn *interfaces.KN) (*interfaces.Statistics, error) {
	// Get business knowledge networks.
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, fmt.Sprintf("查询业务知识网络[%s]信息", kn.KNID))
	defer span.End()

	// Get counts of object, relation, and action types in the business knowledge network.
	otCnt, err := kns.ota.GetObjectTypesTotal(ctx, interfaces.ObjectTypesQueryParams{
		KNID:   kn.KNID,
		Branch: kn.Branch,
	})
	if err != nil {
		logger.Errorf("GetObjectTypesTotal in knowledge network[%s] error: %s", kn.KNID, err.Error())
		span.SetStatus(codes.Error, fmt.Sprintf("GetObjectTypesTotal in knowledge network[%s], error: %v", kn.KNID, err))
		span.End()

		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_KnowledgeNetwork_InternalError_GetObjectTypesTotalFailed).WithErrorDetails(err.Error())
	}

	// Relation type count.
	rtCnt, err := kns.rta.GetRelationTypesTotal(ctx, interfaces.RelationTypesQueryParams{
		KNID:   kn.KNID,
		Branch: kn.Branch,
	})
	if err != nil {
		logger.Errorf("GetRelationTypesTotal in knowledge network[%s] error: %s", kn.KNID, err.Error())
		span.SetStatus(codes.Error, fmt.Sprintf("GetRelationTypesTotal in knowledge network[%s], error: %v", kn.KNID, err))
		span.End()

		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_KnowledgeNetwork_InternalError_GetRelationTypesTotalFailed).WithErrorDetails(err.Error())
	}

	// Action type count.
	atCnt, err := kns.ata.GetActionTypesTotal(ctx, interfaces.ActionTypesQueryParams{
		KNID:   kn.KNID,
		Branch: kn.Branch,
	})
	if err != nil {
		logger.Errorf("GetActionTypesTotal in knowledge network[%s] error: %s", kn.KNID, err.Error())
		span.SetStatus(codes.Error, fmt.Sprintf("GetActionTypesTotal in knowledge network[%s], error: %v", kn.KNID, err))
		span.End()

		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_KnowledgeNetwork_InternalError_GetRelationTypesTotalFailed).WithErrorDetails(err.Error())
	}

	// Concept group count.
	cgCnt, err := kns.cga.GetConceptGroupsTotal(ctx, interfaces.ConceptGroupsQueryParams{
		KNID:   kn.KNID,
		Branch: kn.Branch,
	})
	if err != nil {
		logger.Errorf("GetConceptGroupsTotal in knowledge network[%s] error: %s", kn.KNID, err.Error())
		span.SetStatus(codes.Error, fmt.Sprintf("GetConceptGroupsTotal in knowledge network[%s], error: %v", kn.KNID, err))
		span.End()

		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_KnowledgeNetwork_InternalError_GetRelationTypesTotalFailed).WithErrorDetails(err.Error())
	}

	// Risk type count.
	riskTypeCnt, err := kns.riskTypeA.GetRiskTypesTotal(ctx, interfaces.RiskTypesQueryParams{
		KNID:   kn.KNID,
		Branch: kn.Branch,
	})
	if err != nil {
		logger.Errorf("GetRiskTypesTotal in knowledge network[%s] error: %s", kn.KNID, err.Error())
		span.SetStatus(codes.Error, fmt.Sprintf("GetRiskTypesTotal in knowledge network[%s], error: %v", kn.KNID, err))
		span.End()

		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_KnowledgeNetwork_InternalError_GetRiskTypesTotalFailed).WithErrorDetails(err.Error())
	}

	// Metric count.
	metricsCnt, err := kns.ma.GetMetricsTotal(ctx, interfaces.MetricsListQueryParams{
		KNID:   kn.KNID,
		Branch: kn.Branch,
	})
	if err != nil {
		logger.Errorf("GetMetricsTotal in knowledge network[%s] error: %s", kn.KNID, err.Error())
		span.SetStatus(codes.Error, fmt.Sprintf("GetMetricsTotal in knowledge network[%s], error: %v", kn.KNID, err))
		span.End()

		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_KnowledgeNetwork_InternalError_GetMetricsTotalFailed).WithErrorDetails(err.Error())
	}

	statistics := &interfaces.Statistics{
		CgTotal:       cgCnt,
		OtTotal:       otCnt,
		RtTotal:       rtCnt,
		AtTotal:       atCnt,
		RiskTypeTotal: riskTypeCnt,
		MetricsTotal:  metricsCnt,
	}

	span.SetStatus(codes.Ok, "")
	return statistics, nil
}

// Update business knowledge networks.
func (kns *knowledgeNetworkService) UpdateKN(ctx context.Context, tx *sql.Tx, kn *interfaces.KN, strictMode bool) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Update knowledge network")
	defer span.End()

	// Check whether the user ID can create business knowledge networks through policy evaluation.
	err := kns.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: interfaces.RESOURCE_TYPE_KN,
		ID:   kn.KNID,
	}, []string{interfaces.OPERATION_TYPE_MODIFY})
	if err != nil {
		return err
	}

	if strictMode {
		if err := kns.ValidateKN(ctx, kn, strictMode, interfaces.ImportMode_Overwrite); err != nil {
			return err
		}
	}

	accountInfo := interfaces.AccountInfo{}
	if ctx.Value(interfaces.ACCOUNT_INFO_KEY) != nil {
		accountInfo = ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	}
	kn.Updater = accountInfo

	currentTime := time.Now().UnixMilli() // Business knowledge network update_time uses an integer type.
	kn.UpdateTime = currentTime

	bknNetwork := logics.ToBKNNetWork(kn)
	kn.BKNRawContent = bknsdk.SerializeBknNetwork(bknNetwork)

	if tx == nil {
		// 0. Begin the transaction.
		tx, err = kns.db.Begin()
		if err != nil {
			otellog.LogError(ctx, "Begin transaction error", err)
			return rest.NewHTTPError(ctx, http.StatusInternalServerError,
				berrors.BknBackend_KnowledgeNetwork_InternalError_BeginTransactionFailed).
				WithErrorDetails(err.Error())
		}
		// 0.1 On failure.
		defer func() {
			switch err {
			case nil:
				// Commit the transaction.
				err = tx.Commit()
				if err != nil {
					otellog.LogError(ctx, "UpdateKN Transaction Commit Failed", err)
					return
				}
				otellog.LogDebug(ctx, fmt.Sprintf("UpdateKN Transaction Commit Success: %s", kn.KNName))
			default:
				rollbackErr := tx.Rollback()
				if rollbackErr != nil {
					otellog.LogError(ctx, "UpdateKN Transaction Rollback Error", rollbackErr)
				}
			}
		}()
	}

	// Update model information.
	err = kns.kna.UpdateKN(ctx, tx, kn)
	if err != nil {
		logger.Errorf("UpdateKN error: %s", err.Error())
		span.SetStatus(codes.Error, "修改业务知识网络失败")

		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_KnowledgeNetwork_InternalError).
			WithErrorDetails(err.Error())
	}

	err = kns.InsertDatasetData(ctx, kn)
	if err != nil {
		logger.Errorf("InsertDatasetData error: %s", err.Error())
		span.SetStatus(codes.Error, "业务知识网络概念索引写入失败")

		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_KnowledgeNetwork_InternalError_InsertOpenSearchDataFailed).
			WithErrorDetails(err.Error())
	}

	// Call the resource name update API.
	if kn.IfNameModify {
		err = kns.ps.UpdateResource(ctx, interfaces.PermissionResource{
			ID:   kn.KNID,
			Type: interfaces.RESOURCE_TYPE_KN,
			Name: kn.KNName,
		})
		if err != nil {
			return err
		}
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

func (kns *knowledgeNetworkService) DeleteKN(ctx context.Context, kn *interfaces.KN) (err error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Delete knowledge network")
	defer span.End()
	ctx, cleanupTracker, trackerOwner := permission.WithAuthorizationCleanupTracker(ctx)
	defer func() {
		if trackerOwner && err == nil {
			_ = cleanupTracker.Cleanup(ctx, kns.ps)
		}
	}()

	// Check whether the user ID can delete business knowledge networks.
	err = kns.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: interfaces.RESOURCE_TYPE_KN,
		ID:   kn.KNID,
	}, []string{interfaces.OPERATION_TYPE_DELETE})
	if err != nil {
		return err
	}

	// 0. Begin the transaction.
	tx, err := kns.db.Begin()
	if err != nil {
		otellog.LogError(ctx, "Begin transaction error", err)
		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_KnowledgeNetwork_InternalError_BeginTransactionFailed).
			WithErrorDetails(err.Error())
	}

	// 0.1 On failure.
	defer func() {
		switch err {
		case nil:
			// Commit the transaction.
			err = tx.Commit()
			if err != nil {
				otellog.LogError(ctx, "CreateKN Transaction Commit Failed", err)
				return
			}
			otellog.LogDebug(ctx, "DeleteKN Transaction Commit Success")
		default:
			rollbackErr := tx.Rollback()
			if rollbackErr != nil {
				otellog.LogError(ctx, "CreateKN Transaction Rollback Error", err)
			}
		}
	}()

	// Delete business knowledge networks.
	rowsAffect, err := kns.kna.DeleteKN(ctx, tx, kn.KNID, kn.Branch)
	if err != nil {
		logger.Errorf("DeleteKN error: %s", err.Error())
		span.SetStatus(codes.Error, "删除业务知识网络失败")

		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_KnowledgeNetwork_InternalError).WithErrorDetails(err.Error())
	}
	logger.Infof("DeleteKN: Rows affected is %v, request delete KNID is %s!", rowsAffect, kn.KNID)
	if rowsAffect != 1 {
		otellog.LogWarn(ctx, fmt.Sprintf("Delete kns number %v not equal 1!", rowsAffect))
	}

	// Delete all object types, relation types, action types, and concept groups under the business knowledge network.
	// Get object type IDs under the business knowledge network.
	err = kns.ots.DeleteObjectTypesByKnID(ctx, tx, kn.KNID, kn.Branch)
	if err != nil {
		logger.Errorf("DeleteObjectTypesByKnID error: %s", err.Error())
		span.SetStatus(codes.Error, "删除业务知识网络对象类失败")
		return err
	}

	// Delete all relation types under the business knowledge network.
	err = kns.rts.DeleteRelationTypesByKnID(ctx, tx, kn.KNID, kn.Branch)
	if err != nil {
		logger.Errorf("DeleteRelationTypesByKnID error: %s", err.Error())
		span.SetStatus(codes.Error, "删除业务知识网络关系类失败")
		return err
	}

	// Delete all action types under the business knowledge network.
	err = kns.ats.DeleteActionTypesByKnID(ctx, tx, kn.KNID, kn.Branch)
	if err != nil {
		logger.Errorf("DeleteActionTypesByKnID error: %s", err.Error())
		span.SetStatus(codes.Error, "删除业务知识网络动作类失败")
		return err
	}

	// Delete all metrics under the business knowledge network.
	err = kns.ms.DeleteMetricsByKnID(ctx, tx, kn.KNID, kn.Branch)
	if err != nil {
		logger.Errorf("DeleteMetricsByKnID error: %s", err.Error())
		span.SetStatus(codes.Error, "删除业务知识网络指标失败")
		return err
	}

	// Delete all risk types under the business knowledge network.
	err = kns.riskTypeS.DeleteRiskTypesByKnID(ctx, tx, kn.KNID, kn.Branch)
	if err != nil {
		logger.Errorf("DeleteRiskTypesByKnID error: %s", err.Error())
		span.SetStatus(codes.Error, "删除业务知识网络风险类失败")
		return err
	}

	// Delete all concept groups under the business knowledge network.
	err = kns.cgs.DeleteConceptGroupsByKnID(ctx, tx, kn.KNID, kn.Branch)
	if err != nil {
		logger.Errorf("DeleteConceptGroupsByKnID error: %s", err.Error())
		span.SetStatus(codes.Error, "删除业务知识网络概念分组失败")
		return err
	}

	docid := interfaces.GenerateConceptDocuemtnID(kn.KNID,
		interfaces.MODULE_TYPE_KN, kn.KNID, kn.Branch)
	err = kns.vbs.DeleteDatasetDocumentByID(ctx, interfaces.BKN_DATASET_ID, docid)
	if err != nil {
		logger.Errorf("DeleteDatasetDocumentByID error: %s", err.Error())
		span.SetStatus(codes.Error, "删除业务知识网络概念失败")
		return err
	}

	// Delete all concepts under this KN by query condition
	filterCondition := map[string]any{
		"operation": "and",
		"sub_conditions": []map[string]any{
			{
				"field":      "kn_id",
				"operation":  "==",
				"value":      kn.KNID,
				"value_from": "const",
			},
			{
				"field":      "branch",
				"operation":  "==",
				"value":      kn.Branch,
				"value_from": "const",
			},
		},
	}
	err = kns.vbs.DeleteDatasetDocumentsByQuery(ctx, interfaces.BKN_DATASET_ID, filterCondition)
	if err != nil {
		logger.Errorf("DeleteDatasetDocumentsByQuery error: %s", err.Error())
		span.SetStatus(codes.Error, "删除业务知识网络概念失败")
		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_KnowledgeNetwork_InternalError).
			WithErrorDetails(err.Error())
	}

	// Clear resource policies.
	err = kns.ps.DeleteResources(ctx, interfaces.RESOURCE_TYPE_KN, []string{kn.KNID})
	if err != nil {
		logger.Errorf("DeleteResources error: %s", err.Error())
		span.SetStatus(codes.Error, "删除业务知识网络资源策略失败")
		return err
	}
	span.SetStatus(codes.Ok, "")
	return nil
}

// Update knowledge network details.
func (kns *knowledgeNetworkService) UpdateKNDetail(ctx context.Context, knID string, branch string, detail string) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "UpdateKNDetail")
	defer span.End()

	// Update knowledge network details.
	err := kns.kna.UpdateKNDetail(ctx, knID, branch, detail)
	if err != nil {
		logger.Errorf("UpdateKNDetail error: %s", err.Error())
		span.SetStatus(codes.Error, "修改知识网络详情失败")

		return rest.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_KnowledgeNetwork_InternalError).
			WithErrorDetails(err.Error())
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

func (kns *knowledgeNetworkService) handleKNImportMode(ctx context.Context, mode string,
	kn *interfaces.KN) (isCreate bool, isUpdate bool, err error) {

	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "knowledge network import mode logic")
	defer span.End()

	isCreate = false
	isUpdate = false

	// Validate import mode for a single knowledge network.
	idExist := false
	_, idExist, err = kns.CheckKNExistByID(ctx, kn.KNID, kn.Branch)
	if err != nil {
		return false, false, err
	}

	// Validate conflicts between the request and existing model names.
	existID, nameExist, err := kns.CheckKNExistByName(ctx, kn.KNName, kn.Branch)
	if err != nil {
		return false, false, err
	}

	// Handle mode: ignore removes it from results, overwrite updates it, and normal returns an error.
	if idExist || nameExist {
		switch mode {
		case interfaces.ImportMode_Normal:
			if idExist {
				errDetails := fmt.Sprintf("The knowledge network with id [%s] already exists!", kn.KNID)
				logger.Error(errDetails)
				span.SetStatus(codes.Error, errDetails)
				return false, false, rest.NewHTTPError(ctx, http.StatusBadRequest,
					berrors.BknBackend_KnowledgeNetwork_KNIDExisted).
					WithErrorDetails(errDetails)
			}

			if nameExist {
				errDetails := fmt.Sprintf("knowledge network name '%s' already exists", kn.KNName)
				logger.Error(errDetails)
				span.SetStatus(codes.Error, errDetails)
				return false, false, rest.NewHTTPError(ctx, http.StatusForbidden,
					berrors.BknBackend_KnowledgeNetwork_KNNameExisted).
					WithDescription(map[string]any{"kn_name": kn.KNName}).
					WithErrorDetails(errDetails)
			}

		case interfaces.ImportMode_Ignore:
			// Skip duplicates without creating or updating.
			return false, false, nil
		case interfaces.ImportMode_Overwrite:
			if idExist && nameExist {
				// Return an error when both ID and name exist but the named view has a different ID.
				if existID != kn.KNID {
					errDetails := fmt.Sprintf("KN ID '%s' and name '%s' already exist, but the exist knowledge network id is '%s'",
						kn.KNID, kn.KNName, existID)
					logger.Error(errDetails)
					span.SetStatus(codes.Error, errDetails)
					return false, false, rest.NewHTTPError(ctx, http.StatusForbidden,
						berrors.BknBackend_KnowledgeNetwork_KNNameExisted).
						WithErrorDetails(errDetails)
				} else {
					// Overwrite when ID, name, and metric name exist and the named model ID matches the current model ID.
					isUpdate = true
					return isCreate, isUpdate, nil
				}
			}

			// Overwrite when the ID exists and the name does not.
			if idExist && !nameExist {
				isUpdate = true
				return isCreate, isUpdate, nil
			}

			// Return an error when the ID does not exist but the name exists.
			if !idExist && nameExist {
				errDetails := fmt.Sprintf("KN ID '%s' does not exist, but name '%s' already exists",
					kn.KNID, kn.KNName)
				logger.Error(errDetails)
				span.SetStatus(codes.Error, errDetails)
				return false, false, rest.NewHTTPError(ctx, http.StatusForbidden,
					berrors.BknBackend_KnowledgeNetwork_KNNameExisted).
					WithErrorDetails(errDetails)
			}

			// Create when ID, name, and metric name do not exist.
			// if !idExist && !nameExist {}
		}
	}

	// Default behavior is creation.
	isCreate = true
	return isCreate, isUpdate, nil
}

func (kns *knowledgeNetworkService) InsertDatasetData(ctx context.Context, origKN *interfaces.KN) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "业务知识网络概念索引写入")
	defer span.End()

	kn := &interfaces.KN{
		KNID:   origKN.KNID,
		KNName: origKN.KNName,
		CommonInfo: interfaces.CommonInfo{
			Tags:          origKN.Tags,
			Comment:       origKN.Comment,
			Icon:          origKN.Icon,
			Color:         origKN.Color,
			BKNRawContent: origKN.BKNRawContent,
		},
		Branch:     origKN.Branch,
		Creator:    origKN.Creator,
		CreateTime: origKN.CreateTime,
		Updater:    origKN.Updater,
		UpdateTime: origKN.UpdateTime,
		ModuleType: interfaces.MODULE_TYPE_KN,
	}

	if kns.appSetting.ServerSetting.DefaultSmallModelEnabled {
		words := []string{kn.KNName}
		words = append(words, kn.Tags...)
		words = append(words, kn.Comment, kn.BKNRawContent)
		word := strings.Join(words, "\n")

		defaultModel, err := kns.mfs.GetDefaultModel(ctx)
		if err != nil {
			logger.Errorf("GetDefaultModel error: %s", err.Error())
			span.SetStatus(codes.Error, "获取默认模型失败")
			return err
		}
		vectors, err := kns.mfs.GetVector(ctx, defaultModel, []string{word})
		if err != nil {
			logger.Errorf("GetVector error: %s", err.Error())
			span.SetStatus(codes.Error, "获取业务知识网络向量失败")
			return err
		}

		kn.Vector = vectors[0].Vector
	}

	docid := interfaces.GenerateConceptDocuemtnID(kn.KNID, interfaces.MODULE_TYPE_KN, kn.KNID, kn.Branch)

	// Convert to map for dataset
	docBytes, err := sonic.Marshal(kn)
	if err != nil {
		logger.Errorf("Failed to marshal KN: %s", err.Error())
		span.SetStatus(codes.Error, "序列化业务知识网络失败")
		return err
	}

	var doc map[string]any
	if err := sonic.Unmarshal(docBytes, &doc); err != nil {
		logger.Errorf("Failed to unmarshal KN: %s", err.Error())
		span.SetStatus(codes.Error, "反序列化业务知识网络失败")
		return err
	}

	// Set document ID
	doc["_id"] = docid

	err = kns.vbs.WriteDatasetDocuments(ctx, interfaces.BKN_DATASET_ID, []map[string]any{doc})
	if err != nil {
		logger.Errorf("WriteDatasetDocuments error: %s", err.Error())
		span.SetStatus(codes.Error, "业务知识网络概念索引写入失败")
		return err
	}

	return nil
}

// Intermediate state for batched queries.
type batchQueryState struct {
	visited   map[string]bool
	batchSize int
}

// Get paths by source object type, direction, and length.
func (kns *knowledgeNetworkService) GetRelationTypePaths(ctx context.Context,
	query interfaces.RelationTypePathsBaseOnSource) ([]interfaces.RelationTypePath, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "GetRelationTypePaths")
	defer span.End()

	// 1. Get the source object type.

	allPaths := []interfaces.RelationTypePath{}

	// Search paths with BFS.
	queue := []interfaces.RelationTypePath{
		{
			ObjectTypes: []interfaces.ObjectTypeWithKeyField{
				{
					OTID: query.SourceObjecTypeId,
				},
			},
			Length: 0,
		},
	}

	// Initialize state.
	state := &batchQueryState{
		visited: map[string]bool{}, // Prevent cyclic paths
		// objectTypeCache: map[string]interfaces.ObjectType{},
		batchSize: 50, // Number of nodes queried per batch
	}
	for len(queue) > 0 {
		currentLevelSize := len(queue)
		var nextLevelNodes []string
		currentLevelPaths := make([]interfaces.RelationTypePath, 0, currentLevelSize)

		// Process all paths at the current depth.
		for i := 0; i < currentLevelSize; i++ {
			currentPath := queue[i]
			currentNode := currentPath.ObjectTypes[len(currentPath.ObjectTypes)-1]
			// Retrieve current node information on demand.
			if currentNode.OTName == "" {
				// This function returns an object type not found error when currentNode.OTID does not exist.
				objectType, err := kns.ots.GetObjectTypeByID(ctx, nil, query.KNID, query.Branch, currentNode.OTID)
				if err != nil {
					otellog.LogError(ctx, "Get source object type failed", err)
					return nil, err
				}
				currentNode = interfaces.ObjectTypeWithKeyField{
					OTID:            objectType.OTID,
					OTName:          objectType.OTName,
					DataSource:      objectType.DataSource,
					DataProperties:  objectType.DataProperties,
					LogicProperties: objectType.LogicProperties,
					PrimaryKeys:     objectType.PrimaryKeys,
					DisplayKey:      objectType.DisplayKey,
					IncrementalKey:  objectType.IncrementalKey,
				}
				currentPath.ObjectTypes[len(currentPath.ObjectTypes)-1] = currentNode
			}

			// Save the path when maximum depth is reached.
			if currentPath.Length >= query.PathLength {
				allPaths = append(allPaths, currentPath)
				continue
			}

			// Collect node IDs whose neighbors must be queried.
			nextLevelNodes = append(nextLevelNodes, currentNode.OTID)
			currentLevelPaths = append(currentLevelPaths, currentPath)
		}

		// Query next-level neighbors in batches.
		if len(nextLevelNodes) > 0 {
			neighborPathsMap, err := kns.getNeighborsBatch(ctx, nextLevelNodes, query, state)
			if err != nil {
				otellog.LogError(ctx, "Get neighbor paths failed", err)
				return nil, err
			}

			// Extend every path at the current depth.
			for i, currentPath := range currentLevelPaths {

				currentNodeID := nextLevelNodes[i]
				neighborPaths := neighborPathsMap[currentNodeID]

				// Save the current path when no neighbor exists.
				if len(neighborPaths) == 0 {
					allPaths = append(allPaths, currentPath)
					continue
				}

				// Create a new path for each neighbor. Reset incomplete paths from the current source.
				for _, neighbor := range neighborPaths {
					// Build a path key to detect cycles.
					// In this one-hop path, the second object type is the target.
					pathKey := buildPathKey(currentPath, neighbor)
					if state.visited[pathKey] {
						continue // Skip an already visited path.
					}
					state.visited[pathKey] = true

					newPath := interfaces.RelationTypePath{
						ObjectTypes: make([]interfaces.ObjectTypeWithKeyField, len(currentPath.ObjectTypes)),
						TypeEdges:   make([]interfaces.TypeEdge, len(currentPath.TypeEdges)),
						Length:      currentPath.Length + 1,
					}
					copy(newPath.ObjectTypes, currentPath.ObjectTypes)
					copy(newPath.TypeEdges, currentPath.TypeEdges)
					newPath.ObjectTypes = append(newPath.ObjectTypes, neighbor.ObjectTypes[1])
					newPath.TypeEdges = append(newPath.TypeEdges, neighbor.TypeEdges...)

					queue = append(queue, newPath)
				}
			}
		}
		// Remove processed paths at the current depth.
		if currentLevelSize > 0 {
			queue = queue[currentLevelSize:]
		}
	}
	// Add remaining queued paths when the limit is not reached.
	for i := 0; i < len(queue); i++ {
		allPaths = append(allPaths, queue[i])
	}

	span.SetStatus(codes.Ok, "")
	return allPaths, nil
}

// Query neighboring nodes in batches as the core optimization.
func (kns *knowledgeNetworkService) getNeighborsBatch(ctx context.Context, objectClassIDs []string,
	query interfaces.RelationTypePathsBaseOnSource, state *batchQueryState) (map[string][]interfaces.RelationTypePath, error) {

	if len(objectClassIDs) == 0 {
		return nil, nil
	}

	// Process in batches to avoid too many SQL parameters.
	batchSize := state.batchSize
	neighborPathsMap := map[string][]interfaces.RelationTypePath{}

	for start := 0; start < len(objectClassIDs); start += batchSize {
		// Traverse neighbor paths of the current node.
		end := start + batchSize
		if end > len(objectClassIDs) {
			end = len(objectClassIDs)
		}

		batchIDs := objectClassIDs[start:end]
		batchNeighborPathsMap, err := kns.kna.GetNeighborPathsBatch(ctx, batchIDs, query)
		if err != nil {
			return nil, err
		}

		// Merge results.
		for k, v := range batchNeighborPathsMap {
			neighborPathsMap[k] = append(neighborPathsMap[k], v...)
		}
	}

	return neighborPathsMap, nil
}

// Build a path key for cycle detection.
func buildPathKey(path interfaces.RelationTypePath, neighborPath interfaces.RelationTypePath) string {

	key := ""
	for i := 1; i < len(path.ObjectTypes); i++ {
		key += fmt.Sprintf("%s:%s->%s", path.TypeEdges[i-1].RelationTypeId, path.ObjectTypes[i-1].OTID, path.ObjectTypes[i].OTID)
	}
	key += fmt.Sprintf("%s:%s->%s",
		neighborPath.TypeEdges[0].RelationTypeId, neighborPath.ObjectTypes[0].OTID, neighborPath.ObjectTypes[1].OTID)
	return key
}

// Get business knowledge network resource list.
func (kns *knowledgeNetworkService) ListKnSrcs(ctx context.Context,
	parameter interfaces.KNsQueryParams) ([]interfaces.PermissionResource, int, error) {

	listCtx, listSpan := oteltrace.StartNamedInternalSpan(ctx, "查询业务知识网络实例列表")
	defer listSpan.End()

	// Get all business knowledge networks without pagination.
	knList, err := kns.kna.ListKnSrcs(listCtx, parameter)
	emptyResources := []interfaces.PermissionResource{}
	if err != nil {
		logger.Errorf("ListSimpleKns error: %s", err.Error())
		listSpan.SetStatus(codes.Error, "List simple knowledge networks error")
		listSpan.End()
		return emptyResources, 0, rest.NewHTTPError(listCtx, http.StatusInternalServerError,
			berrors.BknBackend_KnowledgeNetwork_InternalError).WithErrorDetails(err.Error())
	}
	if len(knList) == 0 {
		return emptyResources, 0, nil
	}

	// Filter objects by view permission. The filtered length is the total, so no separate total query is needed.
	// Process resource IDs.
	resMids := make([]string, 0)
	for _, m := range knList {
		resMids = append(resMids, m.ID)
	}
	// Validate permission-management operations.
	matchResoucesMap, err := kns.ps.FilterResources(ctx, interfaces.RESOURCE_TYPE_KN, resMids,
		[]string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, false, interfaces.COMMON_OPERATIONS)
	if err != nil {
		return emptyResources, 0, err
	}

	// Traverse objects.
	results := make([]interfaces.PermissionResource, 0)
	for _, knSrc := range knList {
		if _, exist := matchResoucesMap[knSrc.ID]; exist {
			results = append(results, knSrc)
		}
	}

	// Return all entries when limit is -1.
	if parameter.Limit == -1 {
		return results, len(results), nil
	}

	// Paginate results.
	// Check whether the start offset is out of range.
	if parameter.Offset < 0 || parameter.Offset >= len(results) {
		return nil, len(results), nil
	}
	// Calculate the end offset.
	end := parameter.Offset + parameter.Limit
	if end > len(results) {
		end = len(results)
	}

	listSpan.SetStatus(codes.Ok, "")
	return results[parameter.Offset:end], len(results), nil
}
