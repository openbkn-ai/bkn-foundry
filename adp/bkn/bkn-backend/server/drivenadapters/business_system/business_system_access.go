// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package business_system

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	attr "go.opentelemetry.io/otel/attribute"

	"bkn-backend/common"
	"bkn-backend/interfaces"
)

var (
	bsAccessOnce sync.Once
	bsAccess     interfaces.BusinessSystemAccess
)

type businessSystemAccess struct {
	appSetting *common.AppSetting
	httpClient rest.HTTPClient
	bsUrl      string
}

// NewBusinessSystemAccess creates a business-system access instance.
func NewBusinessSystemAccess(appSetting *common.AppSetting) interfaces.BusinessSystemAccess {
	bsAccessOnce.Do(func() {
		bsAccess = &businessSystemAccess{
			appSetting: appSetting,
			httpClient: common.NewHTTPClient(),
			bsUrl:      appSetting.BusinessSystemUrl,
		}
	})

	return bsAccess
}

func (bsa *businessSystemAccess) BindResource(ctx context.Context, bd_id string, rid string, rtype string) error {

	ctx, span := oteltrace.StartNamedClientSpan(ctx, "driven layer: Bind resource to business system")
	defer span.End()

	span.SetAttributes(
		attr.Key("business_system_id").String(bd_id),
		attr.Key("resource_id").String(rid),
		attr.Key("resource_type").String(rtype))

	httpUrl := fmt.Sprintf("%s/resource", bsa.bsUrl)
	oteltrace.AddAttrs4InternalHttp(span, oteltrace.TraceAttrs{
		HttpUrl:         httpUrl,
		HttpMethod:      http.MethodPost,
		HttpContentType: rest.ContentTypeJson,
	})

	accountInfo := interfaces.AccountInfo{}
	if ctx.Value(interfaces.ACCOUNT_INFO_KEY) != nil {
		accountInfo = ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	}
	headers := map[string]string{
		interfaces.CONTENT_TYPE_NAME:        interfaces.CONTENT_TYPE_JSON,
		interfaces.HTTP_HEADER_ACCOUNT_ID:   accountInfo.ID,
		interfaces.HTTP_HEADER_ACCOUNT_TYPE: accountInfo.Type,
	}

	body := map[string]string{
		"bd_id": bd_id,
		"id":    rid,
		"type":  rtype,
	}
	respCode, respData, err := bsa.httpClient.PostNoUnmarshal(ctx, httpUrl, headers, body)
	logger.Debugf("BindResource finished, response code is [%d], %s", respCode, common.SafeErrorSummary(err))

	if err != nil {
		common.LogSafeError(ctx, "BindResource http request failed", err)
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http bind resource failed")
		return fmt.Errorf("business system dependency request failed")
	}

	if respCode != http.StatusOK {
		logger.Errorf("BindResource failed: response_code=%d, %s", respCode, common.SafeTextSummary("response", string(respData)))
		err = fmt.Errorf("BindResource returned HTTP %d", respCode)
		return err
	}

	return nil
}

func (bsa *businessSystemAccess) UnbindResource(ctx context.Context, bd_id string, rid string, rtype string) error {

	ctx, span := oteltrace.StartNamedClientSpan(ctx, "driven layer: Unbind resource from business system")
	defer span.End()

	span.SetAttributes(
		attr.Key("business_system_id").String(bd_id),
		attr.Key("resource_id").String(rid),
		attr.Key("resource_type").String(rtype))

	httpUrl := fmt.Sprintf("%s/resource?bd_id=%s&id=%s&type=%s", bsa.bsUrl, bd_id, rid, rtype)
	oteltrace.AddAttrs4InternalHttp(span, oteltrace.TraceAttrs{
		HttpUrl:         httpUrl,
		HttpMethod:      http.MethodDelete,
		HttpContentType: rest.ContentTypeJson,
	})

	accountInfo := interfaces.AccountInfo{}
	if ctx.Value(interfaces.ACCOUNT_INFO_KEY) != nil {
		accountInfo = ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	}
	headers := map[string]string{
		interfaces.CONTENT_TYPE_NAME:        interfaces.CONTENT_TYPE_JSON,
		interfaces.HTTP_HEADER_ACCOUNT_ID:   accountInfo.ID,
		interfaces.HTTP_HEADER_ACCOUNT_TYPE: accountInfo.Type,
	}

	respCode, respData, err := bsa.httpClient.DeleteNoUnmarshal(ctx, httpUrl, headers)
	logger.Debugf("UnbindResource finished, response code is [%d], %s", respCode, common.SafeErrorSummary(err))

	if err != nil {
		common.LogSafeError(ctx, "UnbindResource http request failed", err)
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http unbind resource failed")
		return fmt.Errorf("business system dependency request failed")
	}

	if respCode != http.StatusOK {
		logger.Errorf("UnbindResource failed: response_code=%d, %s", respCode, common.SafeTextSummary("response", string(respData)))
		err = fmt.Errorf("UnbindResource returned HTTP %d", respCode)
		return err
	}

	return nil
}
