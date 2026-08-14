// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package resource

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
)

// TestResourceOpOnCatalogTranslation 钉住翻译表本身。
//
// 按同名照搬是这里唯一真正危险的写法:目录上的 modify 语义是「改目录自己」,照搬
// 就把「能重命名目录」的人升级成「能改目录下每张表」。authorize 不在表里也是
// 有意的——目录的授权权不向下变成对每张表的二次授权。
func TestResourceOpOnCatalogTranslation(t *testing.T) {
	assert.Equal(t, interfaces.OPERATION_TYPE_RESOURCE_MANAGE, resourceOpOnCatalog[interfaces.OPERATION_TYPE_MODIFY])
	assert.Equal(t, interfaces.OPERATION_TYPE_RESOURCE_MANAGE, resourceOpOnCatalog[interfaces.OPERATION_TYPE_DELETE])
	assert.Equal(t, interfaces.OPERATION_TYPE_RESOURCE_MANAGE, resourceOpOnCatalog[interfaces.OPERATION_TYPE_CREATE])
	assert.Equal(t, interfaces.OPERATION_TYPE_VIEW_DETAIL, resourceOpOnCatalog[interfaces.OPERATION_TYPE_VIEW_DETAIL])
	assert.Equal(t, interfaces.OPERATION_TYPE_QUERY_DATA, resourceOpOnCatalog[interfaces.OPERATION_TYPE_QUERY_DATA])
	assert.Equal(t, interfaces.OPERATION_TYPE_TASK_MANAGE, resourceOpOnCatalog[interfaces.OPERATION_TYPE_TASK_MANAGE])

	_, mapped := resourceOpOnCatalog[interfaces.OPERATION_TYPE_AUTHORIZE]
	assert.False(t, mapped, "authorize 不能向上问，否则目录授权权就变成了对每张表的二次授权")
}

// TestCheckResourceOrCatalogShortCircuits 是「纯放宽」的证据:资源侧批了就到此
// 为止,一次鉴权请求,常态下的开销与改动前一字不差。
func TestCheckResourceOrCatalogShortCircuits(t *testing.T) {
	ctrl := gomock.NewController(t)
	ps := vmock.NewMockPermissionService(ctrl)
	rs := &resourceService{ps: ps}

	ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
		Type: interfaces.AUTH_RESOURCE_TYPE_RESOURCE, ID: "r-1",
	}, []string{interfaces.OPERATION_TYPE_MODIFY}).Return(nil).Times(1)
	// 目录侧一次都不该被问到。

	require.NoError(t, rs.checkResourceOrCatalog(context.Background(), "r-1", "c-1", false,
		interfaces.OPERATION_TYPE_MODIFY))
}

// TestCheckResourceOrCatalogFallsBack 是这一刀的主张:目录上有 resource_manage
// 的人，可以改目录下的表。
func TestCheckResourceOrCatalogFallsBack(t *testing.T) {
	ctrl := gomock.NewController(t)
	ps := vmock.NewMockPermissionService(ctrl)
	rs := &resourceService{ps: ps}

	ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
		Type: interfaces.AUTH_RESOURCE_TYPE_RESOURCE, ID: "r-1",
	}, []string{interfaces.OPERATION_TYPE_MODIFY}).Return(errors.New("denied"))
	ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
		Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG, ID: "c-1",
	}, []string{interfaces.OPERATION_TYPE_RESOURCE_MANAGE}).Return(nil)

	require.NoError(t, rs.checkResourceOrCatalog(context.Background(), "r-1", "c-1", false,
		interfaces.OPERATION_TYPE_MODIFY))
}

// TestCheckResourceOrCatalogKeepsTheLegacyError: 两问都拒时返回第一问的错误,
// 客户端看到的错误码与提示因此一字不变。
func TestCheckResourceOrCatalogKeepsTheLegacyError(t *testing.T) {
	ctrl := gomock.NewController(t)
	ps := vmock.NewMockPermissionService(ctrl)
	rs := &resourceService{ps: ps}

	legacy := errors.New("the error clients already see")
	ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(legacy)
	ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("catalog says no"))

	err := rs.checkResourceOrCatalog(context.Background(), "r-1", "c-1", false,
		interfaces.OPERATION_TYPE_MODIFY)
	assert.Same(t, legacy, err)
}

// TestCheckResourceOrCatalogDoesNotFallBackForAuthorize: 没有翻译项的操作到此
// 为止，连问都不问。
func TestCheckResourceOrCatalogDoesNotFallBackForAuthorize(t *testing.T) {
	ctrl := gomock.NewController(t)
	ps := vmock.NewMockPermissionService(ctrl)
	rs := &resourceService{ps: ps}

	denied := errors.New("denied")
	ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(),
		[]string{interfaces.OPERATION_TYPE_AUTHORIZE}).Return(denied).Times(1)

	err := rs.checkResourceOrCatalog(context.Background(), "r-1", "c-1", false,
		interfaces.OPERATION_TYPE_AUTHORIZE)
	assert.Same(t, denied, err)
}

// TestCheckResourceOrCatalogUsesInternalCatalogType: 内部目录下的资源回落到
// internal_catalog，与 resourceAuthResourceType 的分型对称。
func TestCheckResourceOrCatalogUsesInternalCatalogType(t *testing.T) {
	ctrl := gomock.NewController(t)
	ps := vmock.NewMockPermissionService(ctrl)
	rs := &resourceService{ps: ps}

	ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
		Type: interfaces.AUTH_RESOURCE_TYPE_INTERNAL_RESOURCE, ID: "r-1",
	}, gomock.Any()).Return(errors.New("denied"))
	ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
		Type: interfaces.AUTH_RESOURCE_TYPE_INTERNAL_CATALOG, ID: "c-1",
	}, []string{interfaces.OPERATION_TYPE_RESOURCE_MANAGE}).Return(nil)

	require.NoError(t, rs.checkResourceOrCatalog(context.Background(), "r-1", "c-1", true,
		interfaces.OPERATION_TYPE_MODIFY))
}

// TestMergeCatalogPermissionsSkipsWhenNothingIsMissing 是列表面「不加钱」的证据:
// 资源侧已经全批时,一次额外请求都不发——连资源详情都不去查。
func TestMergeCatalogPermissionsSkipsWhenNothingIsMissing(t *testing.T) {
	ctrl := gomock.NewController(t)
	rs := &resourceService{
		ps: vmock.NewMockPermissionService(ctrl), // 任何调用都会让 gomock 报错
		ra: vmock.NewMockResourceAccess(ctrl),
		cs: vmock.NewMockCatalogService(ctrl),
	}
	result := map[string]interfaces.PermissionResourceOps{
		"r-1": {ResourceID: "r-1", Operations: []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}},
	}

	require.NoError(t, rs.mergeCatalogPermissions(context.Background(), []string{"r-1"},
		[]string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, result))
	assert.Len(t, result["r-1"].Operations, 1)
}

// TestMergeCatalogPermissionsFillsFromCatalog: 一张资源侧完全没批的表，因为它
// 所在目录批了 view_detail 而出现在结果里——列表页因此看得到它。
func TestMergeCatalogPermissionsFillsFromCatalog(t *testing.T) {
	ctrl := gomock.NewController(t)
	ps := vmock.NewMockPermissionService(ctrl)
	ra := vmock.NewMockResourceAccess(ctrl)
	cs := vmock.NewMockCatalogService(ctrl)
	rs := &resourceService{ps: ps, ra: ra, cs: cs}

	ra.EXPECT().GetByIDsBasic(gomock.Any(), []string{"r-1"}).Return([]*interfaces.Resource{
		{ID: "r-1", CatalogID: "c-1"},
	}, nil)
	cs.EXPECT().ListInternalIDs(gomock.Any()).Return(nil, nil)
	ps.EXPECT().FilterResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_CATALOG, []string{"c-1"},
		[]string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, true, gomock.Any()).
		Return(map[string]interfaces.PermissionResourceOps{
			"c-1": {ResourceID: "c-1", Operations: []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}},
		}, nil)

	result := map[string]interfaces.PermissionResourceOps{}
	require.NoError(t, rs.mergeCatalogPermissions(context.Background(), []string{"r-1"},
		[]string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, result))

	entry, ok := result["r-1"]
	require.True(t, ok, "目录批了 view_detail，这张表应该出现在结果里")
	assert.Equal(t, []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, entry.Operations)
}

// TestMergeCatalogPermissionsLeavesUngrantedCatalogsAlone: 目录也没批的表不会
// 凭空出现——回落只放宽到「目录给了什么」为止。
func TestMergeCatalogPermissionsLeavesUngrantedCatalogsAlone(t *testing.T) {
	ctrl := gomock.NewController(t)
	ps := vmock.NewMockPermissionService(ctrl)
	ra := vmock.NewMockResourceAccess(ctrl)
	cs := vmock.NewMockCatalogService(ctrl)
	rs := &resourceService{ps: ps, ra: ra, cs: cs}

	ra.EXPECT().GetByIDsBasic(gomock.Any(), gomock.Any()).Return([]*interfaces.Resource{
		{ID: "r-1", CatalogID: "c-1"},
	}, nil)
	cs.EXPECT().ListInternalIDs(gomock.Any()).Return(nil, nil)
	ps.EXPECT().FilterResources(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(map[string]interfaces.PermissionResourceOps{}, nil)

	result := map[string]interfaces.PermissionResourceOps{}
	require.NoError(t, rs.mergeCatalogPermissions(context.Background(), []string{"r-1"},
		[]string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, result))
	assert.Empty(t, result)
}

// TestMergeCatalogPermissionsCarriesEveryMappedOp: 一张资源侧完全没批的表，从
// 目录拿到它给的全部操作——详情页的按钮因此是完整的，而不是只够「看得见」。
//
// modify 与 delete 都翻译成 resource_manage，所以目录侧只问一次。
func TestMergeCatalogPermissionsCarriesEveryMappedOp(t *testing.T) {
	ctrl := gomock.NewController(t)
	ps := vmock.NewMockPermissionService(ctrl)
	ra := vmock.NewMockResourceAccess(ctrl)
	cs := vmock.NewMockCatalogService(ctrl)
	rs := &resourceService{ps: ps, ra: ra, cs: cs}

	ra.EXPECT().GetByIDsBasic(gomock.Any(), []string{"r-1"}).Return([]*interfaces.Resource{
		{ID: "r-1", CatalogID: "c-1"},
	}, nil)
	cs.EXPECT().ListInternalIDs(gomock.Any()).Return(nil, nil)
	ps.EXPECT().FilterResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_CATALOG, []string{"c-1"},
		[]string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, true, gomock.Any()).
		Return(map[string]interfaces.PermissionResourceOps{
			"c-1": {ResourceID: "c-1", Operations: []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}},
		}, nil).Times(1)
	ps.EXPECT().FilterResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_CATALOG, []string{"c-1"},
		[]string{interfaces.OPERATION_TYPE_RESOURCE_MANAGE}, true, gomock.Any()).
		Return(map[string]interfaces.PermissionResourceOps{
			"c-1": {ResourceID: "c-1", Operations: []string{interfaces.OPERATION_TYPE_RESOURCE_MANAGE}},
		}, nil).Times(1)

	result := map[string]interfaces.PermissionResourceOps{}
	require.NoError(t, rs.mergeCatalogPermissions(context.Background(), []string{"r-1"},
		[]string{interfaces.OPERATION_TYPE_VIEW_DETAIL, interfaces.OPERATION_TYPE_MODIFY,
			interfaces.OPERATION_TYPE_DELETE}, result))

	got := append([]string(nil), result["r-1"].Operations...)
	sort.Strings(got)
	assert.Equal(t, []string{
		interfaces.OPERATION_TYPE_DELETE,
		interfaces.OPERATION_TYPE_MODIFY,
		interfaces.OPERATION_TYPE_VIEW_DETAIL,
	}, got)
}

// TestMergeCatalogPermissionsLeavesGrantedResourcesAlone: 资源侧已经放行的表
// 不会被重新评估——判据是「在不在结果里」，不是操作集齐不齐。这条也是列表面
// 常态零额外开销的保证。
func TestMergeCatalogPermissionsLeavesGrantedResourcesAlone(t *testing.T) {
	ctrl := gomock.NewController(t)
	rs := &resourceService{
		ps: vmock.NewMockPermissionService(ctrl),
		ra: vmock.NewMockResourceAccess(ctrl),
		cs: vmock.NewMockCatalogService(ctrl),
	}
	// 资源侧放行了，但没带回任何操作——存量调用方就是这么用的。
	result := map[string]interfaces.PermissionResourceOps{"r-1": {ResourceID: "r-1"}}

	require.NoError(t, rs.mergeCatalogPermissions(context.Background(), []string{"r-1"},
		[]string{interfaces.OPERATION_TYPE_VIEW_DETAIL, interfaces.OPERATION_TYPE_MODIFY}, result))
	assert.Empty(t, result["r-1"].Operations)
}

// TestMergeCatalogPermissionsIgnoresUnmappedOps: 只请求 authorize 时，目录侧
// 一次都不问。
func TestMergeCatalogPermissionsIgnoresUnmappedOps(t *testing.T) {
	ctrl := gomock.NewController(t)
	rs := &resourceService{
		ps: vmock.NewMockPermissionService(ctrl),
		ra: vmock.NewMockResourceAccess(ctrl),
		cs: vmock.NewMockCatalogService(ctrl),
	}
	result := map[string]interfaces.PermissionResourceOps{}
	require.NoError(t, rs.mergeCatalogPermissions(context.Background(), []string{"r-1"},
		[]string{interfaces.OPERATION_TYPE_AUTHORIZE}, result))
	assert.Empty(t, result)
}
