// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package permission

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/openbkn-ai/bkn-comm-go/rest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/common"
	"vega-backend/interfaces"
)

func TestPermissionAccessCheckPermission(t *testing.T) {
	t.Run("returns decision", func(t *testing.T) {
		client := &fakeHTTPClient{code: http.StatusOK, body: []byte(`{"result":true}`)}
		access := newPermissionAccess(client)

		got, err := access.CheckPermission(context.Background(), samplePermissionCheck())

		require.NoError(t, err)
		assert.True(t, got)
		assert.Equal(t, "http://permission/operation-check", client.url)
		assert.Equal(t, interfaces.CONTENT_TYPE_JSON, client.headers[interfaces.CONTENT_TYPE_NAME])
		assert.Equal(t, http.MethodGet, client.reqParam.(interfaces.PermissionCheck).Method)
	})

	t.Run("nil response body denies without error", func(t *testing.T) {
		access := newPermissionAccess(&fakeHTTPClient{code: http.StatusOK})

		got, err := access.CheckPermission(context.Background(), samplePermissionCheck())

		require.NoError(t, err)
		assert.False(t, got)
	})

	t.Run("non ok response becomes http error", func(t *testing.T) {
		access := newPermissionAccess(&fakeHTTPClient{
			code: http.StatusForbidden,
			body: []byte(`{"code":"Forbidden","message":"denied"}`),
		})

		got, err := access.CheckPermission(context.Background(), samplePermissionCheck())

		require.Error(t, err)
		assert.False(t, got)
		var httpErr *rest.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusForbidden, httpErr.HTTPCode)
		assert.Equal(t, "Forbidden", httpErr.BaseError.ErrorCode)
		assert.Equal(t, "denied", httpErr.BaseError.Description)
	})
}

func TestPermissionAccessFilterResources(t *testing.T) {
	client := &fakeHTTPClient{
		code: http.StatusOK,
		body: []byte(`[{"id":"resource-1","allow_operation":["view_detail","modify"]}]`),
	}
	access := newPermissionAccess(client)

	got, err := access.FilterResources(context.Background(), interfaces.PermissionResourcesFilter{
		Accessor:   interfaces.PermissionAccessor{Type: interfaces.ACCESSOR_TYPE_USER, ID: "u1"},
		Resources:  []interfaces.PermissionResource{{Type: interfaces.AUTH_RESOURCE_TYPE_RESOURCE, ID: "resource-1"}},
		Operations: []string{interfaces.OPERATION_TYPE_VIEW_DETAIL, interfaces.OPERATION_TYPE_MODIFY},
	})

	require.NoError(t, err)
	assert.Equal(t, "http://permission/resource-filter", client.url)
	assert.Equal(t, http.MethodGet, client.reqParam.(interfaces.PermissionResourcesFilter).Method)
	assert.Equal(t, map[string]interfaces.PermissionResourceOps{
		"resource-1": {
			ResourceID: "resource-1",
			Operations: []string{
				interfaces.OPERATION_TYPE_VIEW_DETAIL,
				interfaces.OPERATION_TYPE_MODIFY,
			},
		},
	}, got)
}

func TestPermissionAccessCreateAndDeleteResources(t *testing.T) {
	t.Run("create policies", func(t *testing.T) {
		client := &fakeHTTPClient{code: http.StatusNoContent}
		access := newPermissionAccess(client)

		err := access.CreateResources(context.Background(), []interfaces.PermissionPolicy{samplePermissionPolicy()})

		require.NoError(t, err)
		assert.Equal(t, "http://permission/policy", client.url)
		assert.Equal(t, []interfaces.PermissionPolicy{samplePermissionPolicy()}, client.reqParam)
	})

	t.Run("delete policies", func(t *testing.T) {
		client := &fakeHTTPClient{code: http.StatusNoContent}
		access := newPermissionAccess(client)
		resources := []interfaces.PermissionResource{{Type: interfaces.AUTH_RESOURCE_TYPE_RESOURCE, ID: "resource-1"}}

		err := access.DeleteResources(context.Background(), resources)

		require.NoError(t, err)
		assert.Equal(t, "http://permission/policy-delete", client.url)
		body := client.reqParam.(map[string]any)
		assert.Equal(t, http.MethodDelete, body["method"])
		assert.Equal(t, resources, body["resources"])
	})
}

func TestMaybeShadow(t *testing.T) {
	inner := newPermissionAccess(&fakeHTTPClient{code: http.StatusOK, body: []byte(`{"result":true}`)})
	t.Setenv("AUTHZ_PROVIDER", "isf")
	t.Setenv("BKN_SAFE_URL", "http://safe")
	assert.Same(t, inner, MaybeShadow(inner))

	t.Setenv("AUTHZ_PROVIDER", "unknown")
	t.Setenv("BKN_SAFE_URL", "")
	assert.Same(t, inner, MaybeShadow(inner))
}

func newPermissionAccess(client *fakeHTTPClient) *permissionAccess {
	return &permissionAccess{
		appSetting:    &common.AppSetting{},
		permissionUrl: "http://permission",
		httpClient:    client,
	}
}

func samplePermissionCheck() interfaces.PermissionCheck {
	return interfaces.PermissionCheck{
		Accessor:   interfaces.PermissionAccessor{Type: interfaces.ACCESSOR_TYPE_USER, ID: "u1"},
		Resource:   interfaces.PermissionResource{Type: interfaces.AUTH_RESOURCE_TYPE_RESOURCE, ID: "resource-1"},
		Operations: []string{interfaces.OPERATION_TYPE_VIEW_DETAIL},
	}
}

func samplePermissionPolicy() interfaces.PermissionPolicy {
	return interfaces.PermissionPolicy{
		Accessor: interfaces.PermissionAccessor{Type: interfaces.ACCESSOR_TYPE_USER, ID: "u1"},
		Resource: interfaces.PermissionResource{Type: interfaces.AUTH_RESOURCE_TYPE_RESOURCE, ID: "resource-1"},
		Operations: interfaces.PermissionPolicyOps{
			Allow: []interfaces.PermissionOperation{{Operation: interfaces.OPERATION_TYPE_VIEW_DETAIL}},
		},
	}
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
