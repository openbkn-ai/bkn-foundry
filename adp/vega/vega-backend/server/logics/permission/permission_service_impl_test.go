package permission

import (
	"context"
	"errors"
	"net/http"
	"testing"

	mqclient "github.com/openbkn-ai/bkn-comm-go/mq"
	"github.com/openbkn-ai/bkn-comm-go/rest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
)

type fakeMQClient struct {
	topic string
	msg   []byte
	err   error
}

func (f *fakeMQClient) Pub(topic string, msg []byte) error {
	f.topic = topic
	f.msg = msg
	return f.err
}

func (f *fakeMQClient) Sub(topic string, channel string, handler mqclient.MessageHandler,
	pollIntervalMilliseconds int64, maxInFlight int, opts ...mqclient.SubOpt) error {
	return nil
}

func (f *fakeMQClient) Close() {}

func TestPermissionServiceImplCheckPermission(t *testing.T) {
	t.Run("rejects missing account", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		access := vmock.NewMockPermissionAccess(ctrl)
		svc := &PermissionServiceImpl{pa: access}

		err := svc.CheckPermission(context.Background(),
			interfaces.PermissionResource{ID: "catalog-1", Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG},
			[]string{interfaces.OPERATION_TYPE_VIEW_DETAIL})

		assertHTTPStatus(t, err, http.StatusForbidden)
	})

	t.Run("delegates account and resource to access", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		access := vmock.NewMockPermissionAccess(ctrl)
		svc := &PermissionServiceImpl{pa: access}
		ctx := contextWithAccount("user-1", interfaces.ACCESSOR_TYPE_USER)
		resource := interfaces.PermissionResource{ID: "catalog-1", Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG}
		var check interfaces.PermissionCheck
		access.EXPECT().
			CheckPermission(gomock.Any(), gomock.AssignableToTypeOf(interfaces.PermissionCheck{})).
			DoAndReturn(func(_ context.Context, got interfaces.PermissionCheck) (bool, error) {
				check = got
				return true, nil
			})

		err := svc.CheckPermission(ctx, resource, []string{interfaces.OPERATION_TYPE_MODIFY})

		require.NoError(t, err)
		assert.Equal(t, interfaces.PermissionAccessor{ID: "user-1", Type: interfaces.ACCESSOR_TYPE_USER}, check.Accessor)
		assert.Equal(t, resource, check.Resource)
		assert.Equal(t, []string{interfaces.OPERATION_TYPE_MODIFY}, check.Operations)
	})

	t.Run("wraps access error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		access := vmock.NewMockPermissionAccess(ctrl)
		access.EXPECT().CheckPermission(gomock.Any(), gomock.Any()).Return(false, errors.New("safe unavailable"))
		svc := &PermissionServiceImpl{pa: access}

		err := svc.CheckPermission(contextWithAccount("user-1", interfaces.ACCESSOR_TYPE_USER),
			interfaces.PermissionResource{ID: "catalog-1", Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG},
			[]string{interfaces.OPERATION_TYPE_DELETE})

		assertHTTPStatus(t, err, http.StatusInternalServerError)
	})

	t.Run("rejects denied result", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		access := vmock.NewMockPermissionAccess(ctrl)
		access.EXPECT().CheckPermission(gomock.Any(), gomock.Any()).Return(false, nil)
		svc := &PermissionServiceImpl{pa: access}

		err := svc.CheckPermission(contextWithAccount("user-1", interfaces.ACCESSOR_TYPE_USER),
			interfaces.PermissionResource{ID: "catalog-1", Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG},
			[]string{interfaces.OPERATION_TYPE_AUTHORIZE})

		assertHTTPStatus(t, err, http.StatusForbidden)
		assert.ErrorContains(t, err, "insufficient permissions")
	})
}

func TestPermissionServiceImplCreateResources(t *testing.T) {
	t.Run("builds policies for account resources and ops", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		access := vmock.NewMockPermissionAccess(ctrl)
		svc := &PermissionServiceImpl{pa: access}
		resources := []interfaces.PermissionResource{
			{ID: "catalog-1", Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG, Name: "Catalog 1"},
			{ID: "catalog-2", Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG, Name: "Catalog 2"},
		}
		ops := []string{interfaces.OPERATION_TYPE_VIEW_DETAIL, interfaces.OPERATION_TYPE_MODIFY}
		var policies []interfaces.PermissionPolicy
		access.EXPECT().
			CreateResources(gomock.Any(), gomock.AssignableToTypeOf([]interfaces.PermissionPolicy{})).
			DoAndReturn(func(_ context.Context, got []interfaces.PermissionPolicy) error {
				policies = got
				return nil
			})

		err := svc.CreateResources(contextWithAccount("user-1", interfaces.ACCESSOR_TYPE_USER), resources, ops)

		require.NoError(t, err)
		require.Len(t, policies, 2)
		assert.Equal(t, resources[0], policies[0].Resource)
		assert.Equal(t, interfaces.PermissionAccessor{ID: "user-1", Type: interfaces.ACCESSOR_TYPE_USER},
			policies[0].Accessor)
		assert.Equal(t, []interfaces.PermissionOperation{
			{Operation: interfaces.OPERATION_TYPE_VIEW_DETAIL},
			{Operation: interfaces.OPERATION_TYPE_MODIFY},
		}, policies[0].Operations.Allow)
		assert.Empty(t, policies[0].Operations.Deny)
	})

	t.Run("rejects missing account before access call", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		access := vmock.NewMockPermissionAccess(ctrl)
		svc := &PermissionServiceImpl{pa: access}

		err := svc.CreateResources(context.Background(),
			[]interfaces.PermissionResource{{ID: "catalog-1", Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG}},
			[]string{interfaces.OPERATION_TYPE_CREATE})

		assertHTTPStatus(t, err, http.StatusForbidden)
	})

	t.Run("wraps access error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		access := vmock.NewMockPermissionAccess(ctrl)
		access.EXPECT().CreateResources(gomock.Any(), gomock.Any()).Return(errors.New("create failed"))
		svc := &PermissionServiceImpl{pa: access}

		err := svc.CreateResources(contextWithAccount("user-1", interfaces.ACCESSOR_TYPE_USER),
			[]interfaces.PermissionResource{{ID: "catalog-1", Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG}},
			[]string{interfaces.OPERATION_TYPE_CREATE})

		assertHTTPStatus(t, err, http.StatusInternalServerError)
		assert.ErrorContains(t, err, "create failed")
	})
}

func TestPermissionServiceImplDeleteResources(t *testing.T) {
	t.Run("skips empty ids", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		access := vmock.NewMockPermissionAccess(ctrl)
		svc := &PermissionServiceImpl{pa: access}

		err := svc.DeleteResources(context.Background(), interfaces.AUTH_RESOURCE_TYPE_RESOURCE, nil)

		require.NoError(t, err)
	})

	t.Run("converts ids into resources", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		access := vmock.NewMockPermissionAccess(ctrl)
		svc := &PermissionServiceImpl{pa: access}
		var resources []interfaces.PermissionResource
		access.EXPECT().
			DeleteResources(gomock.Any(), gomock.AssignableToTypeOf([]interfaces.PermissionResource{})).
			DoAndReturn(func(_ context.Context, got []interfaces.PermissionResource) error {
				resources = got
				return nil
			})

		err := svc.DeleteResources(context.Background(), interfaces.AUTH_RESOURCE_TYPE_RESOURCE, []string{"r1", "r2"})

		require.NoError(t, err)
		assert.Equal(t, []interfaces.PermissionResource{
			{ID: "r1", Type: interfaces.AUTH_RESOURCE_TYPE_RESOURCE},
			{ID: "r2", Type: interfaces.AUTH_RESOURCE_TYPE_RESOURCE},
		}, resources)
	})

	t.Run("wraps access error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		access := vmock.NewMockPermissionAccess(ctrl)
		access.EXPECT().DeleteResources(gomock.Any(), gomock.Any()).Return(errors.New("delete failed"))
		svc := &PermissionServiceImpl{pa: access}

		err := svc.DeleteResources(context.Background(), interfaces.AUTH_RESOURCE_TYPE_RESOURCE, []string{"r1"})

		assertHTTPStatus(t, err, http.StatusInternalServerError)
	})
}

func TestPermissionServiceImplUpdateResource(t *testing.T) {
	t.Run("publishes resource name modification", func(t *testing.T) {
		mq := &fakeMQClient{}
		svc := &PermissionServiceImpl{mqClient: mq}

		err := svc.UpdateResource(context.Background(),
			interfaces.PermissionResource{ID: "catalog-1", Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG, Name: "Catalog 1"})

		require.NoError(t, err)
		assert.Equal(t, interfaces.AUTHORIZATION_RESOURCE_NAME_MODIFY, mq.topic)
		assert.JSONEq(t, `{"id":"catalog-1","type":"catalog","name":"Catalog 1"}`, string(mq.msg))
	})

	t.Run("wraps publish error", func(t *testing.T) {
		mq := &fakeMQClient{err: errors.New("mq down")}
		svc := &PermissionServiceImpl{mqClient: mq}

		err := svc.UpdateResource(context.Background(),
			interfaces.PermissionResource{ID: "catalog-1", Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG})

		assertHTTPStatus(t, err, http.StatusInternalServerError)
	})
}

func TestPermissionServiceImplFilterResources(t *testing.T) {
	t.Run("returns empty map for empty ids without access call", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		access := vmock.NewMockPermissionAccess(ctrl)
		svc := &PermissionServiceImpl{pa: access}

		got, err := svc.FilterResources(contextWithAccount("user-1", interfaces.ACCESSOR_TYPE_USER),
			interfaces.AUTH_RESOURCE_TYPE_CATALOG, nil, []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, true,
			interfaces.COMMON_OPERATIONS)

		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("delegates filter request and maps result by id", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		access := vmock.NewMockPermissionAccess(ctrl)
		var filter interfaces.PermissionResourcesFilter
		access.EXPECT().
			FilterResources(gomock.Any(), gomock.AssignableToTypeOf(interfaces.PermissionResourcesFilter{})).
			DoAndReturn(func(_ context.Context, got interfaces.PermissionResourcesFilter) (map[string]interfaces.PermissionResourceOps, error) {
				filter = got
				return map[string]interfaces.PermissionResourceOps{
					"ignored-key": {ResourceID: "catalog-2", Operations: []string{interfaces.OPERATION_TYPE_MODIFY}},
				}, nil
			})
		svc := &PermissionServiceImpl{pa: access}

		got, err := svc.FilterResources(contextWithAccount("user-1", interfaces.ACCESSOR_TYPE_USER),
			interfaces.AUTH_RESOURCE_TYPE_CATALOG, []string{"catalog-1", "catalog-2"},
			[]string{interfaces.OPERATION_TYPE_MODIFY}, false, interfaces.COMMON_OPERATIONS)

		require.NoError(t, err)
		assert.Equal(t, map[string]interfaces.PermissionResourceOps{
			"catalog-2": {ResourceID: "catalog-2", Operations: []string{interfaces.OPERATION_TYPE_MODIFY}},
		}, got)
		assert.Equal(t, interfaces.PermissionAccessor{ID: "user-1", Type: interfaces.ACCESSOR_TYPE_USER},
			filter.Accessor)
		assert.Equal(t, []interfaces.PermissionResource{
			{ID: "catalog-1", Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG},
			{ID: "catalog-2", Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG},
		}, filter.Resources)
		assert.Equal(t, []string{interfaces.OPERATION_TYPE_MODIFY}, filter.Operations)
		assert.False(t, filter.AllowOperation)
	})

	t.Run("rejects missing account", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		access := vmock.NewMockPermissionAccess(ctrl)
		svc := &PermissionServiceImpl{pa: access}

		got, err := svc.FilterResources(context.Background(),
			interfaces.AUTH_RESOURCE_TYPE_CATALOG, []string{"catalog-1"},
			[]string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, true, interfaces.COMMON_OPERATIONS)

		assertHTTPStatus(t, err, http.StatusForbidden)
		assert.Nil(t, got)
	})

	t.Run("wraps access error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		access := vmock.NewMockPermissionAccess(ctrl)
		access.EXPECT().FilterResources(gomock.Any(), gomock.Any()).Return(nil, errors.New("filter failed"))
		svc := &PermissionServiceImpl{pa: access}

		got, err := svc.FilterResources(contextWithAccount("user-1", interfaces.ACCESSOR_TYPE_USER),
			interfaces.AUTH_RESOURCE_TYPE_CATALOG, []string{"catalog-1"},
			[]string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, true, interfaces.COMMON_OPERATIONS)

		assertHTTPStatus(t, err, http.StatusInternalServerError)
		assert.Nil(t, got)
	})
}

func contextWithAccount(id string, typ string) context.Context {
	return context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{
		ID:   id,
		Type: typ,
	})
}

func assertHTTPStatus(t *testing.T, err error, status int) {
	t.Helper()
	require.Error(t, err)

	var httpErr *rest.HTTPError
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, status, httpErr.HTTPCode)
}
