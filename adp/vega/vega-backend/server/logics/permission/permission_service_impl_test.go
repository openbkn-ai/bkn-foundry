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

	"vega-backend/interfaces"
)

type fakePermissionAccess struct {
	checkCalled bool
	check       interfaces.PermissionCheck
	checkOK     bool
	checkErr    error

	createCalled bool
	policies     []interfaces.PermissionPolicy
	createErr    error

	deleteCalled bool
	resources    []interfaces.PermissionResource
	deleteErr    error

	filterCalled bool
	filter       interfaces.PermissionResourcesFilter
	filterResult map[string]interfaces.PermissionResourceOps
	filterErr    error
}

func (f *fakePermissionAccess) CheckPermission(ctx context.Context, check interfaces.PermissionCheck) (bool, error) {
	f.checkCalled = true
	f.check = check
	return f.checkOK, f.checkErr
}

func (f *fakePermissionAccess) FilterResources(ctx context.Context,
	filter interfaces.PermissionResourcesFilter) (map[string]interfaces.PermissionResourceOps, error) {
	f.filterCalled = true
	f.filter = filter
	return f.filterResult, f.filterErr
}

func (f *fakePermissionAccess) CreateResources(ctx context.Context, policies []interfaces.PermissionPolicy) error {
	f.createCalled = true
	f.policies = policies
	return f.createErr
}

func (f *fakePermissionAccess) DeleteResources(ctx context.Context, resources []interfaces.PermissionResource) error {
	f.deleteCalled = true
	f.resources = resources
	return f.deleteErr
}

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
		access := &fakePermissionAccess{}
		svc := &PermissionServiceImpl{pa: access}

		err := svc.CheckPermission(context.Background(),
			interfaces.PermissionResource{ID: "catalog-1", Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG},
			[]string{interfaces.OPERATION_TYPE_VIEW_DETAIL})

		assertHTTPStatus(t, err, http.StatusForbidden)
		assert.False(t, access.checkCalled)
	})

	t.Run("delegates account and resource to access", func(t *testing.T) {
		access := &fakePermissionAccess{checkOK: true}
		svc := &PermissionServiceImpl{pa: access}
		ctx := contextWithAccount("user-1", interfaces.ACCESSOR_TYPE_USER)
		resource := interfaces.PermissionResource{ID: "catalog-1", Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG}

		err := svc.CheckPermission(ctx, resource, []string{interfaces.OPERATION_TYPE_MODIFY})

		require.NoError(t, err)
		require.True(t, access.checkCalled)
		assert.Equal(t, interfaces.PermissionAccessor{ID: "user-1", Type: interfaces.ACCESSOR_TYPE_USER}, access.check.Accessor)
		assert.Equal(t, resource, access.check.Resource)
		assert.Equal(t, []string{interfaces.OPERATION_TYPE_MODIFY}, access.check.Operations)
	})

	t.Run("wraps access error", func(t *testing.T) {
		access := &fakePermissionAccess{checkErr: errors.New("safe unavailable")}
		svc := &PermissionServiceImpl{pa: access}

		err := svc.CheckPermission(contextWithAccount("user-1", interfaces.ACCESSOR_TYPE_USER),
			interfaces.PermissionResource{ID: "catalog-1", Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG},
			[]string{interfaces.OPERATION_TYPE_DELETE})

		assertHTTPStatus(t, err, http.StatusInternalServerError)
	})

	t.Run("rejects denied result", func(t *testing.T) {
		access := &fakePermissionAccess{checkOK: false}
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
		access := &fakePermissionAccess{}
		svc := &PermissionServiceImpl{pa: access}
		resources := []interfaces.PermissionResource{
			{ID: "catalog-1", Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG, Name: "Catalog 1"},
			{ID: "catalog-2", Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG, Name: "Catalog 2"},
		}
		ops := []string{interfaces.OPERATION_TYPE_VIEW_DETAIL, interfaces.OPERATION_TYPE_MODIFY}

		err := svc.CreateResources(contextWithAccount("user-1", interfaces.ACCESSOR_TYPE_USER), resources, ops)

		require.NoError(t, err)
		require.True(t, access.createCalled)
		require.Len(t, access.policies, 2)
		assert.Equal(t, resources[0], access.policies[0].Resource)
		assert.Equal(t, interfaces.PermissionAccessor{ID: "user-1", Type: interfaces.ACCESSOR_TYPE_USER},
			access.policies[0].Accessor)
		assert.Equal(t, []interfaces.PermissionOperation{
			{Operation: interfaces.OPERATION_TYPE_VIEW_DETAIL},
			{Operation: interfaces.OPERATION_TYPE_MODIFY},
		}, access.policies[0].Operations.Allow)
		assert.Empty(t, access.policies[0].Operations.Deny)
	})

	t.Run("rejects missing account before access call", func(t *testing.T) {
		access := &fakePermissionAccess{}
		svc := &PermissionServiceImpl{pa: access}

		err := svc.CreateResources(context.Background(),
			[]interfaces.PermissionResource{{ID: "catalog-1", Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG}},
			[]string{interfaces.OPERATION_TYPE_CREATE})

		assertHTTPStatus(t, err, http.StatusForbidden)
		assert.False(t, access.createCalled)
	})

	t.Run("wraps access error", func(t *testing.T) {
		access := &fakePermissionAccess{createErr: errors.New("create failed")}
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
		access := &fakePermissionAccess{}
		svc := &PermissionServiceImpl{pa: access}

		err := svc.DeleteResources(context.Background(), interfaces.AUTH_RESOURCE_TYPE_RESOURCE, nil)

		require.NoError(t, err)
		assert.False(t, access.deleteCalled)
	})

	t.Run("converts ids into resources", func(t *testing.T) {
		access := &fakePermissionAccess{}
		svc := &PermissionServiceImpl{pa: access}

		err := svc.DeleteResources(context.Background(), interfaces.AUTH_RESOURCE_TYPE_RESOURCE, []string{"r1", "r2"})

		require.NoError(t, err)
		assert.Equal(t, []interfaces.PermissionResource{
			{ID: "r1", Type: interfaces.AUTH_RESOURCE_TYPE_RESOURCE},
			{ID: "r2", Type: interfaces.AUTH_RESOURCE_TYPE_RESOURCE},
		}, access.resources)
	})

	t.Run("wraps access error", func(t *testing.T) {
		access := &fakePermissionAccess{deleteErr: errors.New("delete failed")}
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
		access := &fakePermissionAccess{}
		svc := &PermissionServiceImpl{pa: access}

		got, err := svc.FilterResources(contextWithAccount("user-1", interfaces.ACCESSOR_TYPE_USER),
			interfaces.AUTH_RESOURCE_TYPE_CATALOG, nil, []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, true,
			interfaces.COMMON_OPERATIONS)

		require.NoError(t, err)
		assert.Empty(t, got)
		assert.False(t, access.filterCalled)
	})

	t.Run("delegates filter request and maps result by id", func(t *testing.T) {
		access := &fakePermissionAccess{
			filterResult: map[string]interfaces.PermissionResourceOps{
				"ignored-key": {ResourceID: "catalog-2", Operations: []string{interfaces.OPERATION_TYPE_MODIFY}},
			},
		}
		svc := &PermissionServiceImpl{pa: access}

		got, err := svc.FilterResources(contextWithAccount("user-1", interfaces.ACCESSOR_TYPE_USER),
			interfaces.AUTH_RESOURCE_TYPE_CATALOG, []string{"catalog-1", "catalog-2"},
			[]string{interfaces.OPERATION_TYPE_MODIFY}, false, interfaces.COMMON_OPERATIONS)

		require.NoError(t, err)
		assert.Equal(t, map[string]interfaces.PermissionResourceOps{
			"catalog-2": {ResourceID: "catalog-2", Operations: []string{interfaces.OPERATION_TYPE_MODIFY}},
		}, got)
		assert.Equal(t, interfaces.PermissionAccessor{ID: "user-1", Type: interfaces.ACCESSOR_TYPE_USER},
			access.filter.Accessor)
		assert.Equal(t, []interfaces.PermissionResource{
			{ID: "catalog-1", Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG},
			{ID: "catalog-2", Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG},
		}, access.filter.Resources)
		assert.Equal(t, []string{interfaces.OPERATION_TYPE_MODIFY}, access.filter.Operations)
		assert.False(t, access.filter.AllowOperation)
	})

	t.Run("rejects missing account", func(t *testing.T) {
		access := &fakePermissionAccess{}
		svc := &PermissionServiceImpl{pa: access}

		got, err := svc.FilterResources(context.Background(),
			interfaces.AUTH_RESOURCE_TYPE_CATALOG, []string{"catalog-1"},
			[]string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, true, interfaces.COMMON_OPERATIONS)

		assertHTTPStatus(t, err, http.StatusForbidden)
		assert.Nil(t, got)
		assert.False(t, access.filterCalled)
	})

	t.Run("wraps access error", func(t *testing.T) {
		access := &fakePermissionAccess{filterErr: errors.New("filter failed")}
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
