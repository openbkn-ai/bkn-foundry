// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package user_mgmt

import (
	"context"
	"errors"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/common"
	"vega-backend/interfaces"
)

func TestUserMgmtAccessUseBknSafe(t *testing.T) {
	assert.False(t, (&userMgmtAccess{}).useBknSafe())
	assert.False(t, (&userMgmtAccess{directoryProvider: "bkn-safe"}).useBknSafe())
	assert.True(t, (&userMgmtAccess{directoryProvider: "bkn-safe", bknSafeURL: "http://safe"}).useBknSafe())
}

func TestUserMgmtAccessGetAccountNames(t *testing.T) {
	t.Run("isf route fills user and app names", func(t *testing.T) {
		client := &fakeHTTPClient{
			code: 200,
			body: []byte(`{"user_names":[{"id":"u1","name":"User One"}],"app_names":[{"id":"app1","name":"App One"}]}`),
		}
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
		assert.Equal(t, "http://user-mgmt/api/user-management/v2/names", client.url)
		assert.Equal(t, "application/json", client.headers["Content-Type"])
		assert.Equal(t, "GET", client.reqParam.(map[string]any)["method"])
		assert.Equal(t, []string{"u1"}, client.reqParam.(map[string]any)["user_ids"])
		assert.Equal(t, []string{"app1", "app-missing"}, client.reqParam.(map[string]any)["app_ids"])
		assert.Equal(t, "User One", accounts[0].Name)
		assert.Equal(t, "User One", accounts[1].Name)
		assert.Equal(t, "App One", accounts[2].Name)
		assert.Equal(t, "-", accounts[3].Name)
	})

	t.Run("bkn safe route uses clean directory endpoint", func(t *testing.T) {
		client := &fakeHTTPClient{
			code: 200,
			body: []byte(`{"user_names":[{"id":"u1","name":"User One"}],"app_names":[]}`),
		}
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
		assert.Equal(t, "http://safe/api/safe/v1/directory/names", client.url)
		assert.NotContains(t, client.reqParam.(map[string]any), "method")
		assert.Equal(t, "User One", accounts[0].Name)
	})

	t.Run("empty input skips request", func(t *testing.T) {
		client := &fakeHTTPClient{}
		access := &userMgmtAccess{httpClient: client}

		require.NoError(t, access.GetAccountNames(context.Background(), nil))
		assert.Empty(t, client.url)
	})

	t.Run("request error", func(t *testing.T) {
		access := &userMgmtAccess{httpClient: &fakeHTTPClient{err: errors.New("network down")}}

		err := access.GetAccountNames(context.Background(), []*interfaces.AccountInfo{{ID: "u1", Type: interfaces.ACCESSOR_TYPE_USER}})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "network down")
	})

	t.Run("non ok status", func(t *testing.T) {
		access := &userMgmtAccess{httpClient: &fakeHTTPClient{code: 500, body: []byte(`{"error":"boom"}`)}}

		err := access.GetAccountNames(context.Background(), []*interfaces.AccountInfo{{ID: "u1", Type: interfaces.ACCESSOR_TYPE_USER}})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "status code: 500")
	})

	t.Run("invalid response json", func(t *testing.T) {
		access := &userMgmtAccess{httpClient: &fakeHTTPClient{code: 200, body: []byte(`{`)}}

		err := access.GetAccountNames(context.Background(), []*interfaces.AccountInfo{{ID: "u1", Type: interfaces.ACCESSOR_TYPE_USER}})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "unmarshal account names response failed")
	})
}

type fakeHTTPClient struct {
	code     int
	body     []byte
	err      error
	url      string
	headers  map[string]string
	reqParam any
}

func (f *fakeHTTPClient) PostNoUnmarshal(_ context.Context, url string, headers map[string]string, reqParam interface{}) (int, []byte, error) {
	f.url = url
	f.headers = headers
	f.reqParam = reqParam
	return f.code, f.body, f.err
}

func (f *fakeHTTPClient) Get(context.Context, string, url.Values, map[string]string) (int, interface{}, error) {
	return 0, nil, nil
}

func (f *fakeHTTPClient) GetNoUnmarshal(context.Context, string, url.Values, map[string]string) (int, []byte, error) {
	return 0, nil, nil
}

func (f *fakeHTTPClient) Delete(context.Context, string, map[string]string) (int, interface{}, error) {
	return 0, nil, nil
}

func (f *fakeHTTPClient) DeleteNoUnmarshal(context.Context, string, map[string]string) (int, []byte, error) {
	return 0, nil, nil
}

func (f *fakeHTTPClient) Post(context.Context, string, map[string]string, interface{}) (int, interface{}, error) {
	return 0, nil, nil
}

func (f *fakeHTTPClient) Put(context.Context, string, map[string]string, interface{}) (int, interface{}, error) {
	return 0, nil, nil
}

func (f *fakeHTTPClient) PutNoUnmarshal(context.Context, string, map[string]string, interface{}) (int, []byte, error) {
	return 0, nil, nil
}

func (f *fakeHTTPClient) Patch(context.Context, string, map[string]string, interface{}) (int, interface{}, error) {
	return 0, nil, nil
}

func (f *fakeHTTPClient) PatchNoUnmarshal(context.Context, string, map[string]string, interface{}) (int, []byte, error) {
	return 0, nil, nil
}
