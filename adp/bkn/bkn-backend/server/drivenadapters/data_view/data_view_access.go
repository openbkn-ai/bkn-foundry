// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package data_view

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/bytedance/sonic"
	"github.com/bytedance/sonic/decoder"
	"github.com/openbkn-ai/bkn-comm-go/logger"
	"github.com/openbkn-ai/bkn-comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-comm-go/rest"
	attr "go.opentelemetry.io/otel/attribute"

	"bkn-backend/common"
	"bkn-backend/interfaces"
)

var (
	dvAccessOnce sync.Once
	dvAccess     interfaces.DataViewAccess
)

type dataViewAccess struct {
	appSetting *common.AppSetting
	httpClient rest.HTTPClient
}

func NewDataViewAccess(appSetting *common.AppSetting) interfaces.DataViewAccess {
	dvAccessOnce.Do(func() {
		dvAccess = &dataViewAccess{
			appSetting: appSetting,
			httpClient: common.NewHTTPClient(),
		}
	})
	return dvAccess
}

// 根据 id 获取视图
func (dva *dataViewAccess) GetDataViewByID(ctx context.Context, id string) (*interfaces.DataView, error) {

	ctx, span := oteltrace.StartNamedClientSpan(ctx, "driven layer: Get views by IDs from data-model service")
	defer span.End()

	span.SetAttributes(attr.Key("view_id").String(id))

	httpUrl := fmt.Sprintf("%s/data-views/%s", dva.appSetting.DataViewUrl, id)
	oteltrace.AddAttrs4InternalHttp(span, oteltrace.TraceAttrs{
		HttpUrl:         httpUrl,
		HttpMethod:      http.MethodGet,
		HttpContentType: rest.ContentTypeJson,
	})

	accountInfo := interfaces.AccountInfo{}
	if ctx.Value(interfaces.ACCOUNT_INFO_KEY) != nil {
		accountInfo = ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	}

	headers := map[string]string{
		interfaces.CONTENT_TYPE_NAME:        interfaces.CONTENT_TYPE_JSON,
		"X-Language":                        rest.GetLanguageByCtx(ctx),
		interfaces.HTTP_HEADER_ACCOUNT_ID:   accountInfo.ID,
		interfaces.HTTP_HEADER_ACCOUNT_TYPE: accountInfo.Type,
	}

	respCode, respData, err := dva.httpClient.GetNoUnmarshal(ctx, httpUrl, nil, headers)
	logger.Debugf("GetDataViewByID finished, response code is [%d], %s", respCode, common.SafeErrorSummary(err))

	if err != nil {
		common.LogSafeError(ctx, "GetDataViewByID http request failed", err)
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http get view failed")
		return nil, fmt.Errorf("data view dependency request failed")
	}

	if respCode == http.StatusNotFound {
		// 添加异常时的 trace 属性
		oteltrace.AddHttpAttrs4Ok(span, respCode)
		// 记录模型不存在的日志
		otellog.LogWarn(ctx, fmt.Sprintf("data view [%s] not found", id))
		return nil, nil
	}

	if respCode != http.StatusOK {
		logger.Errorf("get data view failed: response_code=%d, %s", respCode, common.SafeTextSummary("response", string(respData)))

		var baseError rest.BaseError
		if err = sonic.Unmarshal(respData, &baseError); err != nil {
			common.LogSafeError(ctx, "Unmarshal data view error response failed", err)
			oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Unmarshal baseError failed")
			return nil, fmt.Errorf("GetDataViewByIDs failed: dependency returned HTTP %d", respCode)
		}

		common.LogSafeError(ctx, "GetDataViewByID returned non-success status", fmt.Errorf("HTTP %d", respCode))
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http status code is not 200")
		return nil, fmt.Errorf("GetDataViewByIDs failed: dependency returned HTTP %d", respCode)
	}

	var views []*interfaces.DataView
	if err = sonic.Unmarshal(respData, &views); err != nil {
		common.LogSafeError(ctx, "Unmarshal data view failed", err)
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Unmarshal data view info failed")
		return nil, err
	}

	if len(views) == 0 {
		return nil, nil
	}

	// 将字段转成 map 结构
	fieldsMap := make(map[string]*interfaces.ViewField)
	for _, f := range views[0].Fields {
		field := f
		fieldsMap[f.Name] = field
	}
	views[0].FieldsMap = fieldsMap

	oteltrace.AddHttpAttrs4Ok(span, respCode)
	return views[0], nil
}

// 分批获取视图数据
func (dva *dataViewAccess) GetDataStart(ctx context.Context, id string,
	incKey string, incValue any, limit int) (*interfaces.ViewQueryResult, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "driven layer: GetDataStart")
	defer span.End()

	span.SetAttributes(attr.Key("view_id").String(id))

	httpUrl := fmt.Sprintf("%s/data-views/%s?include_view=true&timeout=5m", dva.appSetting.UniQueryUrl, id)

	accountInfo := interfaces.AccountInfo{}
	if ctx.Value(interfaces.ACCOUNT_INFO_KEY) != nil {
		accountInfo = ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	}
	headers := map[string]string{
		interfaces.CONTENT_TYPE_NAME:           interfaces.CONTENT_TYPE_JSON,
		interfaces.HTTP_HEADER_METHOD_OVERRIDE: http.MethodGet,
		interfaces.HTTP_HEADER_ACCOUNT_ID:      accountInfo.ID,
		interfaces.HTTP_HEADER_ACCOUNT_TYPE:    accountInfo.Type,
	}

	params := map[string]any{
		"need_total":       true,
		"use_search_after": true,
		"limit":            limit,
	}

	if incKey != "" {
		params["sort"] = []map[string]any{
			{
				"field":     incKey,
				"direction": "asc",
			},
		}
		if incValue != nil {
			params["filters"] = map[string]any{
				"field":     incKey,
				"operation": ">",
				"value":     incValue,
			}
		}
	}

	respCode, respData, err := dva.httpClient.PostNoUnmarshal(ctx, httpUrl, headers, params)
	logger.Debugf("GetDataStart finished, response code is [%d], %s", respCode, common.SafeErrorSummary(err))

	if err != nil {
		common.LogSafeError(ctx, "GetDataStart http request failed", err)
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http post failed")
		return nil, fmt.Errorf("data view dependency request failed")
	}

	if respCode != http.StatusOK {
		err = fmt.Errorf("DataPlatform get_data_start returned HTTP %d", respCode)
		common.LogSafeError(ctx, "GetDataStart returned non-success status", err)
		logger.Debugf("GetDataStart response: %s", common.SafeTextSummary("response", string(respData)))
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http status code is not 200")
		return nil, err
	}

	var result interfaces.ViewQueryResult
	d := decoder.NewDecoder(string(respData))
	d.UseInt64()
	if err = d.Decode(&result); err != nil {
		common.LogSafeError(ctx, "GetDataStart unmarshal result failed", err)
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Unmarshal result failed")
		return nil, err
	}

	oteltrace.AddHttpAttrs4Ok(span, respCode)
	return &result, nil
}

// 分批获取视图数据
func (dva *dataViewAccess) GetDataNext(ctx context.Context, id string,
	searchAfter []any, limit int) (*interfaces.ViewQueryResult, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "driven layer: GetDataNext")
	defer span.End()

	span.SetAttributes(attr.Key("view_id").String(id))

	httpUrl := fmt.Sprintf("%s/data-views/%s?timeout=5m", dva.appSetting.UniQueryUrl, id)

	accountInfo := interfaces.AccountInfo{}
	if ctx.Value(interfaces.ACCOUNT_INFO_KEY) != nil {
		accountInfo = ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	}
	headers := map[string]string{
		interfaces.CONTENT_TYPE_NAME:           interfaces.CONTENT_TYPE_JSON,
		interfaces.HTTP_HEADER_METHOD_OVERRIDE: http.MethodGet,
		interfaces.HTTP_HEADER_ACCOUNT_ID:      accountInfo.ID,
		interfaces.HTTP_HEADER_ACCOUNT_TYPE:    accountInfo.Type,
	}

	params := map[string]any{
		"use_search_after": true,
		"search_after":     searchAfter,
		"limit":            limit,
	}

	respCode, respData, err := dva.httpClient.PostNoUnmarshal(ctx, httpUrl, headers, params)
	logger.Debugf("GetDataNext finished, response code is [%d], %s", respCode, common.SafeErrorSummary(err))

	if err != nil {
		common.LogSafeError(ctx, "GetDataNext http request failed", err)
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http post failed")
		return nil, fmt.Errorf("data view dependency request failed")
	}

	if respCode != http.StatusOK {
		err = fmt.Errorf("DataPlatform get_data_next returned HTTP %d", respCode)
		common.LogSafeError(ctx, "GetDataNext returned non-success status", err)
		logger.Debugf("GetDataNext response: %s", common.SafeTextSummary("response", string(respData)))
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http status code is not 200")
		return nil, err
	}

	var result interfaces.ViewQueryResult
	d := decoder.NewDecoder(string(respData))
	d.UseInt64()
	if err = d.Decode(&result); err != nil {
		common.LogSafeError(ctx, "GetDataNext unmarshal result failed", err)
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Unmarshal result failed")
		return nil, err
	}

	oteltrace.AddHttpAttrs4Ok(span, respCode)
	return &result, nil
}
