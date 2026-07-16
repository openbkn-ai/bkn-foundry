// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package permission

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/openbkn-ai/bkn-comm-go/rest"
	rmock "github.com/openbkn-ai/bkn-comm-go/rest/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/common"
	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
)

func TestPermissionAccessCheckPermission(t *testing.T) {
	t.Run("returns decision", func(t *testing.T) {
		access, call := newMockPermissionAccess(t, http.StatusOK, []byte(`{"result":true}`), nil)

		got, err := access.CheckPermission(context.Background(), samplePermissionCheck())

		require.NoError(t, err)
		assert.True(t, got)
		assert.Equal(t, "http://permission/operation-check", call.url)
		assert.Equal(t, interfaces.CONTENT_TYPE_JSON, call.headers[interfaces.CONTENT_TYPE_NAME])
		assert.Equal(t, http.MethodGet, call.reqParam.(interfaces.PermissionCheck).Method)
	})

	t.Run("nil response body denies without error", func(t *testing.T) {
		access, _ := newMockPermissionAccess(t, http.StatusOK, nil, nil)

		got, err := access.CheckPermission(context.Background(), samplePermissionCheck())

		require.NoError(t, err)
		assert.False(t, got)
	})

	t.Run("non ok response becomes http error", func(t *testing.T) {
		access, _ := newMockPermissionAccess(t, http.StatusForbidden, []byte(`{"code":"Forbidden","message":"denied"}`), nil)

		got, err := access.CheckPermission(context.Background(), samplePermissionCheck())

		require.Error(t, err)
		assert.False(t, got)
		var httpErr *rest.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusForbidden, httpErr.HTTPCode)
		assert.Equal(t, "Forbidden", httpErr.BaseError.ErrorCode)
		assert.Equal(t, "denied", httpErr.BaseError.Description)
	})

	t.Run("http client error is wrapped", func(t *testing.T) {
		access, _ := newMockPermissionAccess(t, 0, nil, errors.New("network down"))

		got, err := access.CheckPermission(context.Background(), samplePermissionCheck())

		require.Error(t, err)
		assert.False(t, got)
		assert.Contains(t, err.Error(), "post operation-check request failed")
	})

	t.Run("invalid decision body returns unmarshal error", func(t *testing.T) {
		access, _ := newMockPermissionAccess(t, http.StatusOK, []byte(`{`), nil)

		got, err := access.CheckPermission(context.Background(), samplePermissionCheck())

		require.Error(t, err)
		assert.False(t, got)
	})
}

func TestPermissionAccessFilterResources(t *testing.T) {
	t.Run("returns allow operations", func(t *testing.T) {
		access, call := newMockPermissionAccess(t, http.StatusOK,
			[]byte(`[{"id":"resource-1","allow_operation":["view_detail","modify"]}]`), nil)

		got, err := access.FilterResources(context.Background(), samplePermissionResourcesFilter())

		require.NoError(t, err)
		assert.Equal(t, "http://permission/resource-filter", call.url)
		assert.Equal(t, http.MethodGet, call.reqParam.(interfaces.PermissionResourcesFilter).Method)
		assert.Equal(t, map[string]interfaces.PermissionResourceOps{
			"resource-1": {
				ResourceID: "resource-1",
				Operations: []string{
					interfaces.OPERATION_TYPE_VIEW_DETAIL,
					interfaces.OPERATION_TYPE_MODIFY,
				},
			},
		}, got)
	})

	t.Run("nil response body returns empty map", func(t *testing.T) {
		access, _ := newMockPermissionAccess(t, http.StatusOK, nil, nil)

		got, err := access.FilterResources(context.Background(), samplePermissionResourcesFilter())

		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("non ok response becomes http error", func(t *testing.T) {
		access, _ := newMockPermissionAccess(t, http.StatusForbidden, []byte(`{"code":"Forbidden","description":"filtered"}`), nil)

		got, err := access.FilterResources(context.Background(), samplePermissionResourcesFilter())

		require.Error(t, err)
		assert.Empty(t, got)
		var httpErr *rest.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusForbidden, httpErr.HTTPCode)
		assert.Equal(t, "filtered", httpErr.BaseError.Description)
	})

	t.Run("invalid response body returns unmarshal error", func(t *testing.T) {
		access, _ := newMockPermissionAccess(t, http.StatusOK, []byte(`{`), nil)

		got, err := access.FilterResources(context.Background(), samplePermissionResourcesFilter())

		require.Error(t, err)
		assert.Empty(t, got)
	})
}

func TestPermissionAccessCreateResources(t *testing.T) {
	t.Run("creates policies", func(t *testing.T) {
		access, call := newMockPermissionAccess(t, http.StatusNoContent, nil, nil)

		err := access.CreateResources(context.Background(), []interfaces.PermissionPolicy{samplePermissionPolicy()})

		require.NoError(t, err)
		assert.Equal(t, "http://permission/policy", call.url)
		assert.Equal(t, []interfaces.PermissionPolicy{samplePermissionPolicy()}, call.reqParam)
	})

	t.Run("wraps http client error", func(t *testing.T) {
		access, _ := newMockPermissionAccess(t, 0, nil, errors.New("network down"))

		err := access.CreateResources(context.Background(), []interfaces.PermissionPolicy{samplePermissionPolicy()})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "post create policy request failed")
	})

	t.Run("handles permission error", func(t *testing.T) {
		access, _ := newMockPermissionAccess(t, http.StatusBadRequest, []byte(`{"code":"BadRequest","message":"bad policy"}`), nil)

		err := access.CreateResources(context.Background(), []interfaces.PermissionPolicy{samplePermissionPolicy()})

		require.Error(t, err)
		var httpErr *rest.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, "BadRequest", httpErr.BaseError.ErrorCode)
	})

	t.Run("returns unmarshal error for invalid permission error body", func(t *testing.T) {
		access, _ := newMockPermissionAccess(t, http.StatusBadRequest, []byte(`{`), nil)

		err := access.CreateResources(context.Background(), []interfaces.PermissionPolicy{samplePermissionPolicy()})

		require.Error(t, err)
	})
}

func TestPermissionAccessDeleteResources(t *testing.T) {
	t.Run("deletes policies", func(t *testing.T) {
		access, call := newMockPermissionAccess(t, http.StatusNoContent, nil, nil)
		resources := []interfaces.PermissionResource{{Type: interfaces.AUTH_RESOURCE_TYPE_RESOURCE, ID: "resource-1"}}

		err := access.DeleteResources(context.Background(), resources)

		require.NoError(t, err)
		assert.Equal(t, "http://permission/policy-delete", call.url)
		body := call.reqParam.(map[string]any)
		assert.Equal(t, http.MethodDelete, body["method"])
		assert.Equal(t, resources, body["resources"])
	})

	t.Run("wraps http client error", func(t *testing.T) {
		access, _ := newMockPermissionAccess(t, 0, nil, errors.New("network down"))

		err := access.DeleteResources(context.Background(), []interfaces.PermissionResource{{Type: interfaces.AUTH_RESOURCE_TYPE_RESOURCE, ID: "resource-1"}})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "post delete policy request failed")
	})

	t.Run("handles permission error", func(t *testing.T) {
		access, _ := newMockPermissionAccess(t, http.StatusForbidden, []byte(`{"code":"Forbidden","description":"delete denied"}`), nil)

		err := access.DeleteResources(context.Background(), []interfaces.PermissionResource{{Type: interfaces.AUTH_RESOURCE_TYPE_RESOURCE, ID: "resource-1"}})

		require.Error(t, err)
		var httpErr *rest.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, "delete denied", httpErr.BaseError.Description)
	})

	t.Run("returns unmarshal error for invalid permission error body", func(t *testing.T) {
		access, _ := newMockPermissionAccess(t, http.StatusForbidden, []byte(`{`), nil)

		err := access.DeleteResources(context.Background(), []interfaces.PermissionResource{{Type: interfaces.AUTH_RESOURCE_TYPE_RESOURCE, ID: "resource-1"}})

		require.Error(t, err)
	})
}

func TestSafeClientCheckOne(t *testing.T) {
	t.Run("returns allowed decision", func(t *testing.T) {
		client := newSafeTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/api/safe/v1/authz/check", r.URL.Path)
			var body map[string]any
			decodeRequestJSON(t, r, &body)
			assert.Equal(t, "u1", body["accessor_id"])
			assert.Equal(t, interfaces.OPERATION_TYPE_VIEW_DETAIL, body["operation"])
			_, _ = w.Write([]byte(`{"allowed":true}`))
		})

		got, err := client.checkOne(context.Background(), "u1", interfaces.AUTH_RESOURCE_TYPE_RESOURCE, "resource-1", interfaces.OPERATION_TYPE_VIEW_DETAIL)

		require.NoError(t, err)
		assert.True(t, got)
	})

	t.Run("returns error for non success response", func(t *testing.T) {
		client := newSafeTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "denied", http.StatusForbidden)
		})

		got, err := client.checkOne(context.Background(), "u1", interfaces.AUTH_RESOURCE_TYPE_RESOURCE, "resource-1", interfaces.OPERATION_TYPE_VIEW_DETAIL)

		require.Error(t, err)
		assert.False(t, got)
	})
}

func TestSafeClientAllowedOps(t *testing.T) {
	t.Run("returns allowed subset", func(t *testing.T) {
		client := newSafeTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Operation string `json:"operation"`
			}
			decodeRequestJSON(t, r, &body)
			allowed := body.Operation == interfaces.OPERATION_TYPE_VIEW_DETAIL
			_, _ = w.Write([]byte(`{"allowed":` + boolJSON(allowed) + `}`))
		})

		got, err := client.allowedOps(context.Background(), "u1", interfaces.AUTH_RESOURCE_TYPE_RESOURCE, "resource-1", []string{
			interfaces.OPERATION_TYPE_VIEW_DETAIL,
			interfaces.OPERATION_TYPE_MODIFY,
		})

		require.NoError(t, err)
		assert.Equal(t, []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, got)
	})

	t.Run("returns error when check fails", func(t *testing.T) {
		client := newSafeTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		})

		got, err := client.allowedOps(context.Background(), "u1", interfaces.AUTH_RESOURCE_TYPE_RESOURCE, "resource-1", []string{interfaces.OPERATION_TYPE_VIEW_DETAIL})

		require.Error(t, err)
		assert.Nil(t, got)
	})
}

func TestSafeClientAllowedAll(t *testing.T) {
	t.Run("returns true when all operations are allowed", func(t *testing.T) {
		client := newSafeTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"allowed":true}`))
		})

		got, err := client.allowedAll(context.Background(), "u1", interfaces.AUTH_RESOURCE_TYPE_RESOURCE, "resource-1", []string{
			interfaces.OPERATION_TYPE_VIEW_DETAIL,
			interfaces.OPERATION_TYPE_MODIFY,
		})

		require.NoError(t, err)
		assert.True(t, got)
	})

	t.Run("returns false when any operation is denied", func(t *testing.T) {
		call := 0
		client := newSafeTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			call++
			allowed := call == 1
			_, _ = w.Write([]byte(`{"allowed":` + boolJSON(allowed) + `}`))
		})

		got, err := client.allowedAll(context.Background(), "u1", interfaces.AUTH_RESOURCE_TYPE_RESOURCE, "resource-1", []string{
			interfaces.OPERATION_TYPE_VIEW_DETAIL,
			interfaces.OPERATION_TYPE_MODIFY,
		})

		require.NoError(t, err)
		assert.False(t, got)
	})
}

func TestSafeClientDo(t *testing.T) {
	t.Run("returns invalid json error", func(t *testing.T) {
		client := newSafeTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{`))
		})

		var out struct {
			Allowed bool `json:"allowed"`
		}
		err := client.do(context.Background(), http.MethodPost, "/api/safe/v1/authz/check", map[string]any{}, &out)

		require.Error(t, err)
	})
}

func TestShadowPermissionAccessCheckPermission(t *testing.T) {
	t.Run("returns authoritative inner result", func(t *testing.T) {
		client := newSafeTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"allowed":false}`))
		})
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		inner := vmock.NewMockPermissionAccess(ctrl)
		inner.EXPECT().CheckPermission(gomock.Any(), samplePermissionCheck()).Return(true, nil)
		access := &shadowPermissionAccess{
			PermissionAccess: inner,
			safe:             client,
		}

		got, err := access.CheckPermission(context.Background(), samplePermissionCheck())

		require.NoError(t, err)
		assert.True(t, got)
	})

	t.Run("returns inner error even when safe allows", func(t *testing.T) {
		client := newSafeTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"allowed":true}`))
		})
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		innerErr := errors.New("isf down")
		inner := vmock.NewMockPermissionAccess(ctrl)
		inner.EXPECT().CheckPermission(gomock.Any(), samplePermissionCheck()).Return(false, innerErr)
		access := &shadowPermissionAccess{
			PermissionAccess: inner,
			safe:             client,
		}

		got, err := access.CheckPermission(context.Background(), samplePermissionCheck())

		require.ErrorIs(t, err, innerErr)
		assert.False(t, got)
	})
}

func TestSafePermissionAccessCheckPermission(t *testing.T) {
	t.Run("uses bkn safe decision", func(t *testing.T) {
		client := newSafeTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"allowed":true}`))
		})
		access := &safePermissionAccess{safe: client}

		got, err := access.CheckPermission(context.Background(), samplePermissionCheck())

		require.NoError(t, err)
		assert.True(t, got)
	})
}

func TestSafePermissionAccessFilterResources(t *testing.T) {
	t.Run("returns resources with allowed operations", func(t *testing.T) {
		client := newSafeTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Resource struct {
					ID string `json:"id"`
				} `json:"resource"`
				Operation string `json:"operation"`
			}
			decodeRequestJSON(t, r, &body)
			allowed := body.Resource.ID == "resource-1" && body.Operation == interfaces.OPERATION_TYPE_VIEW_DETAIL
			_, _ = w.Write([]byte(`{"allowed":` + boolJSON(allowed) + `}`))
		})
		access := &safePermissionAccess{safe: client}

		got, err := access.FilterResources(context.Background(), interfaces.PermissionResourcesFilter{
			Accessor: interfaces.PermissionAccessor{Type: interfaces.ACCESSOR_TYPE_USER, ID: "u1"},
			Resources: []interfaces.PermissionResource{
				{Type: interfaces.AUTH_RESOURCE_TYPE_RESOURCE, ID: "resource-1"},
				{Type: interfaces.AUTH_RESOURCE_TYPE_RESOURCE, ID: "resource-2"},
			},
			Operations: []string{interfaces.OPERATION_TYPE_VIEW_DETAIL, interfaces.OPERATION_TYPE_MODIFY},
		})

		require.NoError(t, err)
		assert.Equal(t, map[string]interfaces.PermissionResourceOps{
			"resource-1": {
				ResourceID: "resource-1",
				Operations: []string{
					interfaces.OPERATION_TYPE_VIEW_DETAIL,
				},
			},
		}, got)
	})
}

func TestSafePermissionAccessGetResourcesOperations(t *testing.T) {
	t.Run("returns all resources with operation slices", func(t *testing.T) {
		client := newSafeTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"allowed":false}`))
		})
		access := &safePermissionAccess{safe: client}

		got, err := access.GetResourcesOperations(context.Background(), interfaces.PermissionResourcesFilter{
			Accessor:   interfaces.PermissionAccessor{Type: interfaces.ACCESSOR_TYPE_USER, ID: "u1"},
			Resources:  []interfaces.PermissionResource{{Type: interfaces.AUTH_RESOURCE_TYPE_RESOURCE, ID: "resource-1"}},
			Operations: []string{interfaces.OPERATION_TYPE_VIEW_DETAIL},
		})

		require.NoError(t, err)
		assert.Equal(t, map[string]interfaces.PermissionResourceOps{
			"resource-1": {ResourceID: "resource-1", Operations: []string{}},
		}, got)
	})
}

func TestSafePermissionAccessCreateResources(t *testing.T) {
	t.Run("posts policies", func(t *testing.T) {
		var bodies []map[string]any
		client := newSafeTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodPost, r.Method)
			assert.Equal(t, "/api/safe/v1/authz/policies", r.URL.Path)
			var body map[string]any
			decodeRequestJSON(t, r, &body)
			bodies = append(bodies, body)
			w.WriteHeader(http.StatusNoContent)
		})
		access := &safePermissionAccess{safe: client}

		err := access.CreateResources(context.Background(), []interfaces.PermissionPolicy{samplePermissionPolicy()})

		require.NoError(t, err)
		require.Len(t, bodies, 1)
		assert.Equal(t, "u1", bodies[0]["accessor_id"])
		assert.Equal(t, []any{interfaces.OPERATION_TYPE_VIEW_DETAIL}, bodies[0]["operations"])
	})
}

func TestSafePermissionAccessDeleteResources(t *testing.T) {
	t.Run("deletes policies", func(t *testing.T) {
		var method string
		client := newSafeTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			method = r.Method
			assert.Equal(t, "/api/safe/v1/authz/policies", r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
		})
		access := &safePermissionAccess{safe: client}

		err := access.DeleteResources(context.Background(), []interfaces.PermissionResource{
			{Type: interfaces.AUTH_RESOURCE_TYPE_RESOURCE, ID: "resource-1"},
		})

		require.NoError(t, err)
		assert.Equal(t, http.MethodDelete, method)
	})
}

func TestMaybeShadow(t *testing.T) {
	t.Run("returns inner for isf provider", func(t *testing.T) {
		inner, _ := newMockPermissionAccessWithoutExpectation(t)
		t.Setenv("AUTHZ_PROVIDER", "isf")
		t.Setenv("BKN_SAFE_URL", "http://safe")

		assert.Same(t, inner, MaybeShadow(inner))
	})

	t.Run("returns inner for unknown provider or empty safe url", func(t *testing.T) {
		inner, _ := newMockPermissionAccessWithoutExpectation(t)
		t.Setenv("AUTHZ_PROVIDER", "unknown")
		t.Setenv("BKN_SAFE_URL", "")

		assert.Same(t, inner, MaybeShadow(inner))
	})

	t.Run("wraps inner in shadow mode", func(t *testing.T) {
		inner, _ := newMockPermissionAccessWithoutExpectation(t)
		t.Setenv("AUTHZ_PROVIDER", "shadow")
		t.Setenv("BKN_SAFE_URL", "http://safe")

		got := MaybeShadow(inner)

		require.IsType(t, &shadowPermissionAccess{}, got)
		assert.Same(t, inner, got.(*shadowPermissionAccess).PermissionAccess)
	})

	t.Run("returns safe access in bkn safe mode", func(t *testing.T) {
		inner, _ := newMockPermissionAccessWithoutExpectation(t)
		t.Setenv("AUTHZ_PROVIDER", "bkn-safe")
		t.Setenv("BKN_SAFE_URL", "http://safe")

		got := MaybeShadow(inner)

		require.IsType(t, &safePermissionAccess{}, got)
	})
}

type postNoUnmarshalCall struct {
	url      string
	headers  map[string]string
	reqParam any
}

func newMockPermissionAccess(t *testing.T, code int, body []byte, err error) (*permissionAccess, *postNoUnmarshalCall) {
	t.Helper()

	access, client := newMockPermissionAccessWithoutExpectation(t)
	call := &postNoUnmarshalCall{}
	client.EXPECT().
		PostNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, url string, headers map[string]string, reqParam any) (int, []byte, error) {
			call.url = url
			call.headers = headers
			call.reqParam = reqParam
			return code, body, err
		})
	return access, call
}

func newMockPermissionAccessWithoutExpectation(t *testing.T) (*permissionAccess, *rmock.MockHTTPClient) {
	t.Helper()

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	client := rmock.NewMockHTTPClient(ctrl)
	return newPermissionAccess(client), client
}

func newPermissionAccess(client rest.HTTPClient) *permissionAccess {
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

func samplePermissionResourcesFilter() interfaces.PermissionResourcesFilter {
	return interfaces.PermissionResourcesFilter{
		Accessor:   interfaces.PermissionAccessor{Type: interfaces.ACCESSOR_TYPE_USER, ID: "u1"},
		Resources:  []interfaces.PermissionResource{{Type: interfaces.AUTH_RESOURCE_TYPE_RESOURCE, ID: "resource-1"}},
		Operations: []string{interfaces.OPERATION_TYPE_VIEW_DETAIL, interfaces.OPERATION_TYPE_MODIFY},
	}
}

func newSafeTestClient(t *testing.T, handler http.HandlerFunc) *safeClient {
	t.Helper()

	return &safeClient{
		baseURL: "http://safe.test",
		http: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				recorder := httptest.NewRecorder()
				handler.ServeHTTP(recorder, req)
				return recorder.Result(), nil
			}),
		},
	}
}

func boolJSON(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func decodeRequestJSON(t *testing.T, r *http.Request, out any) {
	t.Helper()

	body, err := io.ReadAll(r.Body)
	require.NoError(t, err)
	require.NoError(t, sonic.Unmarshal(body, out))
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
