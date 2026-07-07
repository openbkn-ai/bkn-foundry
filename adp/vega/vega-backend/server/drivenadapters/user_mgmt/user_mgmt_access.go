package user_mgmt

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sync"

	"github.com/bytedance/sonic"
	"github.com/openbkn-ai/bkn-comm-go/logger"
	"github.com/openbkn-ai/bkn-comm-go/rest"

	"vega-backend/common"
	"vega-backend/interfaces"
)

var (
	umAccessOnce sync.Once
	umAccess     interfaces.UserMgmtAccess
)

type userMgmtAccess struct {
	appSetting  *common.AppSetting
	httpClient  rest.HTTPClient
	userMgmtUrl string
	// bkn-safe directory cutover (revertible): DIRECTORY_PROVIDER=bkn-safe +
	// BKN_SAFE_URL routes name resolution to bkn-safe's clean /directory/names
	// instead of ISF /v2/names. Unset to revert (default = ISF).
	directoryProvider string
	bknSafeURL        string
}

func NewUserMgmtAccess(appSetting *common.AppSetting) interfaces.UserMgmtAccess {
	umAccessOnce.Do(func() {
		umAccess = &userMgmtAccess{
			appSetting:        appSetting,
			httpClient:        common.NewHTTPClient(),
			userMgmtUrl:       appSetting.UserMgmtUrl,
			directoryProvider: os.Getenv("DIRECTORY_PROVIDER"),
			bknSafeURL:        os.Getenv("BKN_SAFE_URL"),
		}
	})

	return umAccess
}

// useBknSafe reports whether name resolution should go to bkn-safe.
func (u *userMgmtAccess) useBknSafe() bool {
	return u.directoryProvider == "bkn-safe" && u.bknSafeURL != ""
}

func (u *userMgmtAccess) GetAccountNames(ctx context.Context, accountInfos []*interfaces.AccountInfo) error {
	if len(accountInfos) == 0 {
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

	// Route to bkn-safe (clean /directory/names) or ISF (/v2/names). Both return
	// the same { user_names, app_names } shape, so only URL+body differ.
	var httpUrl string
	var requestBody map[string]any
	if u.useBknSafe() {
		httpUrl = fmt.Sprintf("%s/api/safe/v1/directory/names", u.bknSafeURL)
		requestBody = map[string]any{
			"user_ids": userIDs,
			"app_ids":  appIDs,
		}
	} else {
		httpUrl = fmt.Sprintf("%s/api/user-management/v2/names", u.userMgmtUrl)
		requestBody = map[string]any{
			"method":   http.MethodGet,
			"user_ids": userIDs,
			"app_ids":  appIDs,
			"strict":   false,
		}
	}

	headers := map[string]string{
		"Content-Type": "application/json",
	}

	respCode, result, err := u.httpClient.PostNoUnmarshal(ctx, httpUrl, headers, requestBody)
	logger.Debugf("post [%s] finished, response code is [%d], result is [%s], error is [%v]", httpUrl, respCode, result, err)

	if err != nil {
		logger.Errorf("Get account names request failed: %v", err)
		return fmt.Errorf("get account names request failed: %w", err)
	}

	if respCode != 200 {
		logger.Errorf("Get account names request failed with status code: %d", respCode)
		return fmt.Errorf("get account names request failed with status code: %d", respCode)
	}

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
		logger.Errorf("Unmarshal account names response failed: %v", err)
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

	return nil
}
