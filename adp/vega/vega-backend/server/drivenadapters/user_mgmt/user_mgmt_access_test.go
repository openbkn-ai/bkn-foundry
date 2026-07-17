// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package user_mgmt

import (
	"context"
	"errors"
	"testing"

	rmock "github.com/openbkn-ai/bkn-comm-go/rest/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/common"
	"vega-backend/interfaces"
)

func TestUserMgmtAccessUseBknSafe(t *testing.T) {
	t.Run("returns false by default", func(t *testing.T) {
		assert.False(t, (&userMgmtAccess{}).useBknSafe())
	})

	t.Run("returns false without safe url", func(t *testing.T) {
		assert.False(t, (&userMgmtAccess{directoryProvider: "bkn-safe"}).useBknSafe())
	})

	t.Run("returns true for bkn safe provider with url", func(t *testing.T) {
		assert.True(t, (&userMgmtAccess{directoryProvider: "bkn-safe", bknSafeURL: "http://safe"}).useBknSafe())
	})
}

func TestUserMgmtAccessGetAccountNames(t *testing.T) {
	t.Run("isf route fills user and app names", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		client := rmock.NewMockHTTPClient(ctrl)
		var gotURL string
		var gotHeaders map[string]string
		var gotReqParam any
		client.EXPECT().
			PostNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, url string, headers map[string]string, reqParam any) (int, []byte, error) {
				gotURL = url
				gotHeaders = headers
				gotReqParam = reqParam
				return 200, []byte(`{"user_names":[{"id":"u1","name":"User One"}],"app_names":[{"id":"app1","name":"App One"}]}`), nil
			})
		access := &userMgmtAccess{
			appSetting:  &common.AppSetting{},
			httpClient:  client,
			userMgmtUrl: "http://user-mgmt",
		}
		accounts := []*interfaces.AccountInfo{
			{ID: "u1", Type: interfaces.ACCESSOR_TYPE_USER},
			{ID: "u1", Type: interfaces.ACCESSOR_TYPE_USER},
			{ID: "app1", Type: interfaces.ACCESSOR_TYPE_APP},
			{ID: "app-missing", Type: interfaces.ACCESSOR_TYPE_APP},
		}

		err := access.GetAccountNames(context.Background(), accounts)

		require.NoError(t, err)
		assert.Equal(t, "http://user-mgmt/api/user-management/v2/names", gotURL)
		assert.Equal(t, "application/json", gotHeaders["Content-Type"])
		assert.Equal(t, "GET", gotReqParam.(map[string]any)["method"])
		assert.Equal(t, []string{"u1"}, gotReqParam.(map[string]any)["user_ids"])
		assert.Equal(t, []string{"app1", "app-missing"}, gotReqParam.(map[string]any)["app_ids"])
		assert.Equal(t, "User One", accounts[0].Name)
		assert.Equal(t, "User One", accounts[1].Name)
		assert.Equal(t, "App One", accounts[2].Name)
		assert.Equal(t, "-", accounts[3].Name)
	})

	t.Run("bkn safe route uses clean directory endpoint", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		client := rmock.NewMockHTTPClient(ctrl)
		var gotURL string
		var gotReqParam any
		client.EXPECT().
			PostNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, url string, _ map[string]string, reqParam any) (int, []byte, error) {
				gotURL = url
				gotReqParam = reqParam
				return 200, []byte(`{"user_names":[{"id":"u1","name":"User One"}],"app_names":[]}`), nil
			})
		access := &userMgmtAccess{
			appSetting:        &common.AppSetting{},
			httpClient:        client,
			directoryProvider: "bkn-safe",
			bknSafeURL:        "http://safe",
			userMgmtUrl:       "http://user-mgmt",
		}
		accounts := []*interfaces.AccountInfo{{ID: "u1", Type: interfaces.ACCESSOR_TYPE_USER}}

		err := access.GetAccountNames(context.Background(), accounts)

		require.NoError(t, err)
		assert.Equal(t, "http://safe/api/safe/v1/directory/names", gotURL)
		assert.NotContains(t, gotReqParam.(map[string]any), "method")
		assert.Equal(t, "User One", accounts[0].Name)
	})

	t.Run("empty input skips request", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		client := rmock.NewMockHTTPClient(ctrl)
		access := &userMgmtAccess{httpClient: client}

		require.NoError(t, access.GetAccountNames(context.Background(), nil))
	})

	t.Run("request error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		client := rmock.NewMockHTTPClient(ctrl)
		client.EXPECT().
			PostNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(0, nil, errors.New("network down"))
		access := &userMgmtAccess{httpClient: client}

		err := access.GetAccountNames(context.Background(), []*interfaces.AccountInfo{{ID: "u1", Type: interfaces.ACCESSOR_TYPE_USER}})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "network down")
	})

	t.Run("non ok status", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		client := rmock.NewMockHTTPClient(ctrl)
		client.EXPECT().
			PostNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(500, []byte(`{"error":"boom"}`), nil)
		access := &userMgmtAccess{httpClient: client}

		err := access.GetAccountNames(context.Background(), []*interfaces.AccountInfo{{ID: "u1", Type: interfaces.ACCESSOR_TYPE_USER}})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "status code: 500")
	})

	t.Run("invalid response json", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		client := rmock.NewMockHTTPClient(ctrl)
		client.EXPECT().
			PostNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(200, []byte(`{`), nil)
		access := &userMgmtAccess{httpClient: client}

		err := access.GetAccountNames(context.Background(), []*interfaces.AccountInfo{{ID: "u1", Type: interfaces.ACCESSOR_TYPE_USER}})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "unmarshal account names response failed")
	})
}
