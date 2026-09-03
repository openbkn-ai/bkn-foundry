// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package drivenadapters

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/utils"
	sharedrest "github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

type safeUserManagement struct {
	baseURL string
	http    *http.Client
	logger  interfaces.Logger
}

func newSafeUserManagement(baseURL string, logger interfaces.Logger) *safeUserManagement {
	return &safeUserManagement{baseURL: baseURL, http: &http.Client{Timeout: 5 * time.Second}, logger: logger}
}

// safeUserDetail is the subset of bkn-safe's directory UserFull this adapter reads.
type safeUserDetail struct {
	ID      string   `json:"id"`
	Account string   `json:"account"`
	Name    string   `json:"name"`
	Roles   []string `json:"roles"`
}

// GetUsersInfo returns the directory record for each existing user ID. Missing
// IDs are omitted. The fields argument
// is ignored: bkn-safe returns the full record and callers pick what they need.
func (s *safeUserManagement) GetUsersInfo(ctx context.Context, userIDs, fields []string) (infos []*interfaces.UserInfo, err error) {
	infos = []*interfaces.UserInfo{}
	userIDs = utils.UniqueStrings(userIDs)
	if len(userIDs) == 0 {
		return infos, nil
	}
	var out struct {
		Users []safeUserDetail `json:"users"`
	}
	if err = s.post(ctx, "/api/safe/v1/directory/users-detail", map[string]any{"user_ids": userIDs}, &out); err != nil {
		s.logger.WithContext(ctx).Warnf("[bkn-safe] users-detail failed: %v", err)
		return nil, err
	}
	for _, u := range out.Users {
		infos = append(infos, &interfaces.UserInfo{
			UserID:      u.ID,
			DisplayName: u.Name,
			Account:     u.Account,
			Roles:       u.Roles,
		})
	}
	return infos, nil
}

func (s *safeUserManagement) GetUserInfo(ctx context.Context, userID string, fields ...string) (info *interfaces.UserInfo, err error) {
	infos, err := s.GetUsersInfo(ctx, []string{userID}, fields)
	if err != nil {
		return nil, err
	}
	if len(infos) == 0 {
		s.logger.WithContext(ctx).Errorf("GetUserInfo failed, user %s info not found", userID)
		return nil, fmt.Errorf("user %s info not found", userID)
	}
	return infos[0], nil
}

// GetUsersName resolves display names. Unknown IDs degrade to UnknownUser so
// audit and list rendering do not fail for a deleted account.
func (s *safeUserManagement) GetUsersName(ctx context.Context, userIDs []string) (userMap map[string]string, err error) {
	userMap = make(map[string]string)
	lookup := make([]string, 0, len(userIDs))
	for _, id := range utils.UniqueStrings(userIDs) {
		if id == interfaces.SystemUser {
			userMap[id] = interfaces.SystemUser
			continue
		}
		lookup = append(lookup, id)
	}
	if len(lookup) == 0 {
		return userMap, nil
	}
	infos, err := s.GetUsersInfo(ctx, lookup, []string{interfaces.DisplayName})
	if err != nil {
		return nil, err
	}
	for _, info := range infos {
		userMap[info.UserID] = info.DisplayName
	}
	for _, id := range lookup {
		if _, ok := userMap[id]; !ok {
			userMap[id] = interfaces.UnknownUser
		}
	}
	return userMap, nil
}

// GetAppInfo resolves an app account name via the directory names endpoint.
// Unknown ids fall back to the id itself (same degradation as the noop client).
func (s *safeUserManagement) GetAppInfo(ctx context.Context, appID string) (appInfo *interfaces.AppInfo, err error) {
	var out struct {
		AppNames []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"app_names"`
	}
	if err = s.post(ctx, "/api/safe/v1/directory/names", map[string]any{"app_ids": []string{appID}}, &out); err != nil {
		s.logger.WithContext(ctx).Warnf("[bkn-safe] app name lookup failed: %v", err)
		return nil, err
	}
	appInfo = &interfaces.AppInfo{ID: appID, Name: appID}
	for _, a := range out.AppNames {
		if a.ID == appID && a.Name != "" {
			appInfo.Name = a.Name
		}
	}
	return appInfo, nil
}

func (s *safeUserManagement) post(ctx context.Context, path string, body, out any) error {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(sharedrest.AcceptLanguageHeader, sharedrest.GetLanguageByCtx(ctx))
	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("bkn-safe POST %s: %d: %s", path, resp.StatusCode, data)
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}
