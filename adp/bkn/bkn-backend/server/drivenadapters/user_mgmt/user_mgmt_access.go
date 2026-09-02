// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package user_mgmt

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"go.opentelemetry.io/otel/codes"

	"bkn-backend/common"
	"bkn-backend/interfaces"
)

type userMgmtAccess struct {
	httpClient rest.HTTPClient
	bknSafeURL string
}

// NewUserMgmtAccess creates a bkn-safe directory access instance.
func NewUserMgmtAccess(baseURL string) interfaces.UserMgmtAccess {
	return &userMgmtAccess{
		httpClient: common.NewHTTPClient(),
		bknSafeURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
	}
}

func (uma *userMgmtAccess) GetAccountNames(ctx context.Context, accountInfos []*interfaces.AccountInfo) error {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "GetAccountNames")
	defer span.End()

	if len(accountInfos) == 0 {
		span.SetStatus(codes.Ok, "")
		return nil
	}

	userIDMap := map[string]string{}
	appIDMap := map[string]string{}
	userIDs := []string{}
	appIDs := []string{}
	for _, accountInfo := range accountInfos {
		switch accountInfo.Type {
		case interfaces.ACCESSOR_TYPE_USER:
			if _, ok := userIDMap[accountInfo.ID]; !ok {
				userIDMap[accountInfo.ID] = "-"
				userIDs = append(userIDs, accountInfo.ID)
			}
		case interfaces.ACCESSOR_TYPE_APP:
			if _, ok := appIDMap[accountInfo.ID]; !ok {
				appIDMap[accountInfo.ID] = "-"
				appIDs = append(appIDs, accountInfo.ID)
			}
		}
	}

	httpUrl := fmt.Sprintf("%s/api/safe/v1/directory/names", uma.bknSafeURL)
	requestBody := map[string]any{
		"user_ids": userIDs,
		"app_ids":  appIDs,
	}
	oteltrace.AddAttrs4InternalHttp(span, oteltrace.TraceAttrs{
		HttpUrl:         httpUrl,
		HttpMethod:      http.MethodPost,
		HttpContentType: rest.ContentTypeJson,
	})

	// Set request headers.
	headers := map[string]string{
		"Content-Type": "application/json",
	}

	// Send the POST request to retrieve user information.
	respCode, result, err := uma.httpClient.PostNoUnmarshal(ctx, httpUrl, headers, requestBody)
	logger.Debugf("GetAccountNames finished, response code is [%d], %s", respCode, common.SafeErrorSummary(err))

	if err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http get account names failed")
		common.LogSafeError(ctx, "Get account names request failed", err)
		return fmt.Errorf("get account names request failed: %w", err)
	}

	if respCode != 200 {
		err := fmt.Errorf("get account names request failed with status code: %d", respCode)
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http status is not 200")
		common.LogSafeError(ctx, "Get account names request failed", err)
		return err
	}

	// "{\"app_names\":[{\"id\":\"91efa756-11cc-49d7-ab25-f6e18f9305fe\",\"name\":\"kwww\"}],\"user_names\":[{\"id\":\"f6c6e398-ce82-11f0-888f-3ac1298ec09f\",\"name\":\"kww\"}],\"department_names\":[],\"contactor_names\":[],\"group_names\":[]}"
	// Parse the response data.
	response := struct {
		AppNames []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"app_names"`
		UserNames []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"user_names"`
	}{}

	if err := sonic.Unmarshal(result, &response); err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Unmarshal account names response failed")
		common.LogSafeError(ctx, "Unmarshal account names response failed", err)
		return fmt.Errorf("unmarshal account names response failed: %w", err)
	}

	for _, user := range response.UserNames {
		userIDMap[user.ID] = user.Name
	}
	for _, app := range response.AppNames {
		appIDMap[app.ID] = app.Name
	}
	for _, accountInfo := range accountInfos {
		switch accountInfo.Type {
		case interfaces.ACCESSOR_TYPE_USER:
			if name, ok := userIDMap[accountInfo.ID]; ok {
				accountInfo.Name = name
			} else {
				accountInfo.Name = "-"
			}
		case interfaces.ACCESSOR_TYPE_APP:
			if name, ok := appIDMap[accountInfo.ID]; ok {
				accountInfo.Name = name
			} else {
				accountInfo.Name = "-"
			}
		}
	}

	oteltrace.AddHttpAttrs4Ok(span, respCode)
	return nil
}
