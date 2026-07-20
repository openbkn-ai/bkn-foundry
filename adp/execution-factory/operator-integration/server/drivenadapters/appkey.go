// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package drivenadapters 定义驱动适配器
// @file appkey.go
// @description: 实现 AppKey 校验接口，由 bkn-safe 完成校验
package drivenadapters

import (
	"context"
	"net/http"
	"os"
	"sync"

	jsoniter "github.com/json-iterator/go"

	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/infra/config"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/infra/rest"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/utils"
)

// appKeyIntrospectURI 是 bkn-safe 的 AppKey 校验端点（集群内 ClusterIP，免令牌）。
// 响应结构对齐 OAuth2 introspection。
const appKeyIntrospectURI = "/api/safe/v1/api-keys/introspect"

// appKeyIntrospectTimeout 限制校验请求的耗时。
// rest.NewHTTPClient() 的默认超时是 600 秒，用在认证路径上意味着 bkn-safe 一旦 hang 住，
// 单个请求会占住连接 10 分钟。认证必须快速失败，故与 authorization_safe.go 对 bkn-safe
// 的调用取同一档超时。
const appKeyIntrospectTimeout = 5

type appKeyVerifier struct {
	introspectURL string
	logger        interfaces.Logger
	httpClient    interfaces.HTTPClient
}

var (
	appKeyOnce sync.Once
	appKeyInst interfaces.AppKeyVerifier
)

// appKeyIntrospectResp 是 bkn-safe 的校验响应：任何失败都返回 200 {active:false}，
// 成功时带出持有者身份。
type appKeyIntrospectResp struct {
	Active      bool   `json:"active"`
	Sub         string `json:"sub"`          // 持有者 accessor id
	AccountType string `json:"account_type"` // 持有者在 bkn-safe 中的 account_type
	KeyID       string `json:"key_id"`
}

// NewAppKeyVerifier 构造由 bkn-safe 支撑的 AppKey 校验器。
// 以下两种情况返回 nil，调用方将其视为「不支持 AppKey」并回落到 hydra：
//   - AUTH_ENABLED=false，认证整体关闭
//   - BKN_SAFE_URL 为空，无法访问 bkn-safe
//
// 该降级与 authorization_safe.go 中 BKN_SAFE_URL 缺失时回落的处理保持一致。
func NewAppKeyVerifier() interfaces.AppKeyVerifier {
	appKeyOnce.Do(func() {
		if !config.GetAuthEnabled() {
			return // appKeyInst 保持 nil
		}
		baseURL := os.Getenv("BKN_SAFE_URL")
		if baseURL == "" {
			config.NewConfigLoader().GetLogger().Warnf("[appkey] BKN_SAFE_URL empty; AppKey verification disabled")
			return // appKeyInst 保持 nil
		}
		appKeyInst = &appKeyVerifier{
			introspectURL: baseURL + appKeyIntrospectURI,
			logger:        config.NewConfigLoader().GetLogger(),
			httpClient:    rest.NewHTTPClientWithOptions(rest.HTTPClientOptions{TimeOut: appKeyIntrospectTimeout}),
		}
	})
	return appKeyInst
}

// Verify 经 bkn-safe 把 AppKey 解析为持有者的 TokenInfo。
// 结果结构与 hydra 内省持有者 OAuth 令牌的产出完全一致，
// 因此下游的 AccountAuthContext 与全部授权判定不受凭据类型影响。
func (v *appKeyVerifier) Verify(ctx context.Context, key string) (*interfaces.TokenInfo, error) {
	header := map[string]string{"Content-Type": "application/json"}
	_, resp, err := v.httpClient.Post(ctx, v.introspectURL, header, map[string]string{"token": key})
	if err != nil {
		v.logger.WithContext(ctx).Errorf("AppKey introspect request failed: %v", err)
		return nil, errors.DefaultHTTPError(ctx, http.StatusUnauthorized, "api key is invalid")
	}

	introspect := &appKeyIntrospectResp{}
	if err := jsoniter.Unmarshal(utils.ObjectToByte(resp), introspect); err != nil {
		v.logger.WithContext(ctx).Warnf("AppKey introspect decode failed: %+v, resp:%+v", err, resp)
		return nil, errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
	}
	if !introspect.Active {
		return nil, errors.DefaultHTTPError(ctx, http.StatusUnauthorized, "api key is invalid")
	}

	return &interfaces.TokenInfo{
		Active:     true,
		VisitorID:  introspect.Sub,
		VisitorTyp: appKeyVisitorType(introspect.AccountType),
		AccountTyp: appKeyAccountType(introspect.AccountType),
	}, nil
}

// appKeyVisitorType 把 bkn-safe 的 account_type 映射为访问者类型。
// "app"（应用账户）映射为 Business，其余持有者视为实名访问者映射为 RealName，
// 从而使普通用户的 AccountAuthContext.AccountType 与 OAuth 令牌路径一致。
func appKeyVisitorType(accountType string) interfaces.VisitorType {
	if accountType == "app" {
		return interfaces.Business
	}
	return interfaces.RealName
}

// appKeyAccountType 把 bkn-safe 的 account_type 映射为账户类型。
func appKeyAccountType(accountType string) interfaces.AccountType {
	if accountType == "id_card" {
		return interfaces.IDCard
	}
	return interfaces.Other
}
