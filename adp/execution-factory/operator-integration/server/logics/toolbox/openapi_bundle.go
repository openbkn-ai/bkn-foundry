// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package toolbox

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
)

// RegisterOpenApiBundle creates one tool per OpenAPI operation in the target toolbox, creating
// the toolbox first when only box_name is given. Tools are created through CreateTool, so they
// start disabled like any other newly created tool.
func (s *ToolServiceImpl) RegisterOpenApiBundle(
	ctx context.Context,
	req *interfaces.RegisterOpenApiBundleReq,
) (resp *interfaces.RegisterOpenApiBundleResp, err error) {
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)

	resp = &interfaces.RegisterOpenApiBundleResp{
		ToolIDs: []string{},
		Links:   []interfaces.OpenApiBundleLink{},
	}

	boxID := req.BoxID
	if boxID == "" {
		if req.BoxName == "" {
			err = errors.DefaultHTTPError(ctx, http.StatusBadRequest, "box_id or box_name is required")
			return
		}
		createResp, createErr := s.CreateToolBox(ctx, &interfaces.CreateToolBoxReq{
			UserID:       req.UserID,
			BoxName:      req.BoxName,
			BoxDesc:      req.BoxDesc,
			BoxSvcURL:    req.BoxSvcURL,
			Category:     req.Category,
			MetadataType: interfaces.MetadataTypeAPI,
		})
		if createErr != nil {
			err = createErr
			return
		}
		boxID = createResp.BoxID
	}

	// CreateTool checks the toolbox exists, is an editable openapi toolbox, and that the caller
	// may modify it.
	createResp, err := s.CreateTool(ctx, &interfaces.CreateToolReq{
		UserID:       req.UserID,
		BoxID:        boxID,
		MetadataType: interfaces.MetadataTypeAPI,
		UseRule:      req.UseRule,
		ExtendInfo:   req.ExtendInfo,
		OpenAPIInput: &interfaces.OpenAPIInput{Data: json.RawMessage(req.Data)},
	})
	if err != nil {
		return nil, err
	}

	for _, failure := range createResp.Failures {
		resp.FailureCount++
		msg := "tool creation failed"
		if failure.Error != nil {
			msg = failure.Error.Error()
		}
		resp.Failures = append(resp.Failures, fmt.Sprintf("%s: %s", failure.ToolName, msg))
	}
	for _, toolID := range createResp.SuccessIDs {
		resp.ToolIDs = append(resp.ToolIDs, toolID)
		resp.Links = append(resp.Links, interfaces.OpenApiBundleLink{ToolID: toolID})
	}

	if len(resp.ToolIDs) == 0 {
		err = errors.DefaultHTTPError(ctx, http.StatusBadRequest, "no tools created from openapi bundle")
		return nil, err
	}

	resp.BoxID = boxID
	return resp, nil
}
