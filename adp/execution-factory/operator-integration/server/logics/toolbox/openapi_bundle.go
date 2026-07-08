// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package toolbox

import (
	"context"
	"fmt"
	"net/http"

	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-comm-go/otel/oteltrace"
)

// RegisterOpenApiBundle registers OpenAPI operators first, then converts each into toolbox tools.
func (s *ToolServiceImpl) RegisterOpenApiBundle(
	ctx context.Context,
	req *interfaces.RegisterOpenApiBundleReq,
) (resp *interfaces.RegisterOpenApiBundleResp, err error) {
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)

	resp = &interfaces.RegisterOpenApiBundleResp{
		ToolIDs:     []string{},
		OperatorIDs: []string{},
		Links:       []interfaces.OpenApiBundleLink{},
	}

	boxID := req.BoxID
	if boxID == "" {
		if req.BoxName == "" {
			err = errors.DefaultHTTPError(ctx, http.StatusBadRequest, "box_id or box_name is required")
			return
		}
		createResp, createErr := s.CreateToolBox(ctx, &interfaces.CreateToolBoxReq{
			BusinessDomainID: req.BusinessDomainID,
			UserID:           req.UserID,
			BoxName:          req.BoxName,
			BoxDesc:          req.BoxDesc,
			BoxSvcURL:        req.BoxSvcURL,
			Category:         req.Category,
			MetadataType:     interfaces.MetadataTypeAPI,
		})
		if createErr != nil {
			err = createErr
			return
		}
		boxID = createResp.BoxID
	} else {
		exist, toolBox, selectErr := s.ToolBoxDB.SelectToolBox(ctx, boxID)
		if selectErr != nil {
			s.Logger.WithContext(ctx).Errorf("select toolbox failed, err: %v", selectErr)
			err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, selectErr.Error())
			return
		}
		if !exist {
			err = errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtToolBoxNotFound,
				fmt.Sprintf("toolbox %s not found", boxID))
			return
		}
		if toolBox.MetadataType != string(interfaces.MetadataTypeAPI) {
			err = errors.DefaultHTTPError(ctx, http.StatusBadRequest,
				"openapi bundle only supports openapi toolboxes")
			return
		}
	}

	operatorInfo := req.OperatorInfo
	if operatorInfo == nil {
		operatorInfo = &interfaces.OperatorInfo{
			Type:          interfaces.OperatorTypeBase,
			ExecutionMode: interfaces.ExecutionModeSync,
			Category:      req.Category,
			Source:        "custom",
		}
	}
	if operatorInfo.Category == "" {
		operatorInfo.Category = req.Category
	}

	registerResults, registerErr := s.OperatorMgnt.RegisterOperatorByOpenAPI(ctx, &interfaces.OperatorRegisterReq{
		MetadataType:           interfaces.MetadataTypeAPI,
		Data:                   req.Data,
		Description:            req.Description,
		DirectPublish:          req.DirectPublish,
		OperatorInfo:           operatorInfo,
		OperatorExecuteControl: req.OperatorExecuteControl,
		ExtendInfo:             req.ExtendInfo,
	}, req.UserID)
	if registerErr != nil {
		err = registerErr
		return
	}

	for _, result := range registerResults {
		if result == nil || result.Status != interfaces.ResultStatusSuccess || result.OperatorID == "" {
			resp.FailureCount++
			if result != nil && result.Error != nil {
				resp.Failures = append(resp.Failures, result.Error.Error())
			} else {
				resp.Failures = append(resp.Failures, "operator registration failed")
			}
			continue
		}

		if !req.DirectPublish {
			publishErr := s.OperatorMgnt.UpdateOperatorStatus(ctx, &interfaces.OperatorStatusUpdateReq{
				UserID: req.UserID,
				StatusItems: []*interfaces.OperatorStatusItem{{
					OperatorID: result.OperatorID,
					Status:     interfaces.BizStatusPublished,
				}},
			}, req.UserID)
			if publishErr != nil {
				resp.FailureCount++
				resp.Failures = append(resp.Failures, publishErr.Error())
				continue
			}
		}

		convertResp, convertErr := s.ConvertOperatorToTool(ctx, &interfaces.ConvertOperatorToToolReq{
			UserID:     req.UserID,
			OperatorID: result.OperatorID,
			BoxID:      boxID,
			UseRule:    req.UseRule,
		})
		if convertErr != nil {
			resp.FailureCount++
			resp.Failures = append(resp.Failures, convertErr.Error())
			continue
		}

		resp.OperatorIDs = append(resp.OperatorIDs, result.OperatorID)
		resp.ToolIDs = append(resp.ToolIDs, convertResp.ToolID)
		resp.Links = append(resp.Links, interfaces.OpenApiBundleLink{
			OperatorID: result.OperatorID,
			ToolID:     convertResp.ToolID,
		})
	}

	if len(resp.ToolIDs) == 0 {
		err = errors.DefaultHTTPError(ctx, http.StatusBadRequest, "no tools created from openapi bundle")
		return
	}

	resp.BoxID = boxID
	return
}
