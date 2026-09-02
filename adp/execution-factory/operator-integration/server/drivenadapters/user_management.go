// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package drivenadapters defines outbound service adapters.
package drivenadapters

import (
	"context"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/utils"
)

var (
	syncOnce sync.Once
	um       interfaces.UserManagement
)

type noopUserManagementClient struct{}

// NewUserManagementClient creates the bkn-safe directory adapter.
func NewUserManagementClient() interfaces.UserManagement {
	if !config.GetAuthEnabled() {
		return &noopUserManagementClient{}
	}
	syncOnce.Do(func() {
		conf := config.NewConfigLoader()
		baseURL := mustBknSafeURL()
		conf.GetLogger().Infof("[user-mgnt] provider=bkn-safe directory at %s", baseURL)
		um = newSafeUserManagement(baseURL, conf.GetLogger())
	})
	return um
}

func (n *noopUserManagementClient) GetAppInfo(_ context.Context, appID string) (*interfaces.AppInfo, error) {
	return &interfaces.AppInfo{ID: appID, Name: appID}, nil
}

func (n *noopUserManagementClient) GetUserInfo(_ context.Context, userID string, _ ...string) (*interfaces.UserInfo, error) {
	displayName := interfaces.UnknownUser
	if userID != "" {
		displayName = userID
	}
	return &interfaces.UserInfo{UserID: userID, DisplayName: displayName}, nil
}

func (n *noopUserManagementClient) GetUsersInfo(_ context.Context, userIDs, _ []string) ([]*interfaces.UserInfo, error) {
	infos := make([]*interfaces.UserInfo, 0, len(userIDs))
	for _, userID := range utils.UniqueStrings(userIDs) {
		displayName := interfaces.UnknownUser
		if userID == interfaces.SystemUser {
			displayName = interfaces.SystemUser
		} else if userID != "" {
			displayName = userID
		}
		infos = append(infos, &interfaces.UserInfo{UserID: userID, DisplayName: displayName})
	}
	return infos, nil
}

func (n *noopUserManagementClient) GetUsersName(_ context.Context, userIDs []string) (map[string]string, error) {
	userMap := make(map[string]string, len(userIDs))
	for _, userID := range utils.UniqueStrings(userIDs) {
		if userID == interfaces.SystemUser {
			userMap[userID] = interfaces.SystemUser
			continue
		}
		if userID != "" {
			userMap[userID] = userID
			continue
		}
		userMap[userID] = interfaces.UnknownUser
	}
	return userMap, nil
}
