// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package drivenadapters

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/utils"
)

// executeFunctionURI 沙箱代码执行，走执行工厂的**公开面**。
//
// 内部面的 /internal-v1/function/exec/:version 只能跑已注册的函数版本
// （version 校验为 uuid4），跑不了任意代码，用不上。
//
// 更要紧的是不能用内部面：公开面对调用方校验算子类型上的 execute 权限（#345
// 补的，此前任何持有有效令牌的账号都能拿到沙箱代码执行能力）。以服务端身份走
// 内部面等于把那道检查洗掉。因此这里带调用方本人的 bearer 令牌。
const executeFunctionURI = "/v1/function/execute"

// ErrCallerTokenMissing 上下文里没有调用方令牌。
//
// 不静默降级成服务端身份：那样会绕过执行工厂的 execute 权限判定。
var ErrCallerTokenMissing = fmt.Errorf("caller token is required for sandbox execution")

// ExecuteFunction 在沙箱内执行一段代码。
func (o *operatorIntegrationClient) ExecuteFunction(
	ctx context.Context, req *interfaces.ExecuteFunctionRequest,
) (*interfaces.ExecuteFunctionResponse, error) {
	if req == nil || req.Code == "" {
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusBadRequest, "执行代码不能为空")
	}

	token, ok := common.GetRawTokenFromCtx(ctx)
	if !ok {
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusUnauthorized, ErrCallerTokenMissing.Error())
	}

	header := o.skillHeader(ctx, "operator.function.execute")
	header["Authorization"] = "Bearer " + token

	event := req.Event
	if event == nil {
		// 执行工厂要求 event 在场，无入参也得传 {}，省略会 400。
		event = map[string]any{}
	}
	body := map[string]any{"code": req.Code, "language": req.Language, "event": event}
	if req.Timeout > 0 {
		body["timeout"] = req.Timeout
	}

	fullURL := o.baseURL + executeFunctionURI
	o.logger.WithContext(ctx).Debugf("[OperatorIntegration#ExecuteFunction] URL: %s, language: %s", fullURL, req.Language)

	code, respBody, err := o.httpClient.Post(ctx, fullURL, header, body)
	if err != nil {
		o.logger.WithContext(ctx).Errorf("[OperatorIntegration#ExecuteFunction] Request failed, err: %v", err)
		return nil, skillUpstreamError(ctx, code, "沙箱代码执行接口调用失败", err)
	}

	resp := &interfaces.ExecuteFunctionResponse{}
	if err = json.Unmarshal(utils.ObjectToByte(respBody), resp); err != nil {
		o.logger.WithContext(ctx).Errorf("[OperatorIntegration#ExecuteFunction] Unmarshal failed, err: %v", err)
		return nil, infraErr.DefaultHTTPError(ctx, http.StatusInternalServerError,
			fmt.Sprintf("解析沙箱执行响应失败: %v", err))
	}
	return resp, nil
}
