// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package resource

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
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
	// create 不在表里:建表根本不走这张表——它直接判目标目录，因为「这张表建在
	// 哪个目录」是通配对象答不了的问题。留一条永不触发的映射就是一句谎话。
	_, createMapped := resourceOpOnCatalog[interfaces.OPERATION_TYPE_CREATE]
	assert.False(t, createMapped)
	assert.Equal(t, interfaces.OPERATION_TYPE_VIEW_DETAIL, resourceOpOnCatalog[interfaces.OPERATION_TYPE_VIEW_DETAIL])
	assert.Equal(t, interfaces.OPERATION_TYPE_QUERY_DATA, resourceOpOnCatalog[interfaces.OPERATION_TYPE_QUERY_DATA])
	assert.Equal(t, interfaces.OPERATION_TYPE_TASK_MANAGE, resourceOpOnCatalog[interfaces.OPERATION_TYPE_TASK_MANAGE])

	_, mapped := resourceOpOnCatalog[interfaces.OPERATION_TYPE_AUTHORIZE]
	assert.False(t, mapped, "authorize 不能向上问，否则目录授权权就变成了对每张表的二次授权")
}

// TestCheckResourceOrCatalogShortCircuits 是「纯放宽」的证据:资源侧批了就到此
// 为止,一次鉴权请求,常态下的开销与改动前一字不差。
//
// 用 view_detail 而不是 modify:资源类型如今只声明两个读动词,管理动词不再向
// 资源侧发问,短路只可能发生在读上。
func TestCheckResourceOrCatalogShortCircuits(t *testing.T) {
	ctrl := gomock.NewController(t)
	ps := vmock.NewMockPermissionService(ctrl)
	rs := &resourceService{ps: ps}

	ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
		Type: interfaces.AUTH_RESOURCE_TYPE_RESOURCE, ID: "r-1",
	}, []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}).Return(nil).Times(1)
	// 目录侧一次都不该被问到。

	require.NoError(t, rs.checkResourceOrCatalog(context.Background(), "r-1", "c-1", false,
		interfaces.OPERATION_TYPE_VIEW_DETAIL))
}

// TestCheckResourceOrCatalogNeverAsksTheResourceForManageVerbs 钉住这一刀真正
// 的边界:管理动词已经从资源类型的词表里撤掉,控制台既发不出也收不回,只有
// 收敛之前留下的存量 p-line 还能答应它。若还按同名去问资源,那批存量就成了
// 一份看不见也撤不掉的常驻权限——实测中它足以删掉一张真实的表。
func TestCheckResourceOrCatalogNeverAsksTheResourceForManageVerbs(t *testing.T) {
	for _, op := range []string{
		interfaces.OPERATION_TYPE_MODIFY,
		interfaces.OPERATION_TYPE_DELETE,
		interfaces.OPERATION_TYPE_TASK_MANAGE,
	} {
		t.Run(op, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			ps := vmock.NewMockPermissionService(ctrl)
			rs := &resourceService{ps: ps}

			// 只允许目录那一问。多问一次 mock 就会失败。
			ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
				Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG, ID: "c-1",
			}, []string{resourceOpOnCatalog[op]}).Return(nil).Times(1)

			require.NoError(t, rs.checkResourceOrCatalog(context.Background(), "r-1", "c-1", false, op))
		})
	}
}

// TestResourceOwnOperations 钉住资源侧还肯回答哪些动词——它必须与 bkn-safe
// 词表里 resource 剩下的那两个一致,否则判定与授权面会各说各话。
func TestResourceOwnOperations(t *testing.T) {
	assert.True(t, resourceOwnOperations[interfaces.OPERATION_TYPE_VIEW_DETAIL])
	assert.True(t, resourceOwnOperations[interfaces.OPERATION_TYPE_QUERY_DATA])
	assert.Len(t, resourceOwnOperations, 2)
}

// TestCheckResourceOrCatalogFallsBack 是这一刀的主张:目录上有 resource_manage
// 的人，可以改目录下的表。
func TestCheckResourceOrCatalogFallsBack(t *testing.T) {
	ctrl := gomock.NewController(t)
	ps := vmock.NewMockPermissionService(ctrl)
	rs := &resourceService{ps: ps}

	ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
		Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG, ID: "c-1",
	}, []string{interfaces.OPERATION_TYPE_RESOURCE_MANAGE}).Return(nil)

	require.NoError(t, rs.checkResourceOrCatalog(context.Background(), "r-1", "c-1", false,
		interfaces.OPERATION_TYPE_MODIFY))
}

// TestCheckResourceOrCatalogKeepsTheLegacyError: 两问都拒时返回第一问的错误,
// 客户端看到的错误码与提示因此一字不变。
//
// 只有资源侧真被问到过才有「第一问」可留——读动词就是这种情形。
func TestCheckResourceOrCatalogKeepsTheLegacyError(t *testing.T) {
	ctrl := gomock.NewController(t)
	ps := vmock.NewMockPermissionService(ctrl)
	rs := &resourceService{ps: ps}

	legacy := errors.New("the error clients already see")
	ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(legacy)
	ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("catalog says no"))

	err := rs.checkResourceOrCatalog(context.Background(), "r-1", "c-1", false,
		interfaces.OPERATION_TYPE_VIEW_DETAIL)
	assert.Same(t, legacy, err)
}

// TestCheckResourceOrCatalogSurfacesTheCatalogError: 管理动词没有第一问,拒绝
// 的理由只能来自目录那一问——不能因为没有旧错误可留就把 nil 当作放行。
func TestCheckResourceOrCatalogSurfacesTheCatalogError(t *testing.T) {
	ctrl := gomock.NewController(t)
	ps := vmock.NewMockPermissionService(ctrl)
	rs := &resourceService{ps: ps}

	catalogErr := errors.New("catalog says no")
	ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(catalogErr).Times(1)

	err := rs.checkResourceOrCatalog(context.Background(), "r-1", "c-1", false,
		interfaces.OPERATION_TYPE_DELETE)
	assert.Same(t, catalogErr, err)
}

// TestCheckResourceOrCatalogDoesNotFallBackForAuthorize: authorize 两头都无路
// 可走——资源类型不再声明它,目录侧也有意不给它翻译项——所以一次鉴权请求都
// 不发,直接 403。这是全篇唯一「谁都答不了」的动词。
func TestCheckResourceOrCatalogDoesNotFallBackForAuthorize(t *testing.T) {
	ctrl := gomock.NewController(t)
	ps := vmock.NewMockPermissionService(ctrl) // 任何调用都会让 gomock 报错
	rs := &resourceService{ps: ps}

	err := rs.checkResourceOrCatalog(context.Background(), "r-1", "c-1", false,
		interfaces.OPERATION_TYPE_AUTHORIZE)

	require.Error(t, err)
	var httpErr *rest.HTTPError
	require.True(t, errors.As(err, &httpErr), "want an HTTPError, got %v", err)
	assert.Equal(t, http.StatusForbidden, httpErr.HTTPCode)
}

// TestCheckResourceOrCatalogUsesInternalCatalogType: 内部目录下的资源回落到
// internal_catalog，与 resourceAuthResourceType 的分型对称。
func TestCheckResourceOrCatalogUsesInternalCatalogType(t *testing.T) {
	ctrl := gomock.NewController(t)
	ps := vmock.NewMockPermissionService(ctrl)
	rs := &resourceService{ps: ps}

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

// TestCreateJudgesTheCatalogOnly 钉住建表只问目标目录的 resource_manage。
//
// 老动词不再作为第二次机会:resource:* 这个通配对象答不了「这张表要建在哪个目录」,
// 持有它的人可以往任意目录里建表——那正是要去掉的东西。mock 只允许目录那一问,
// 多问一次就会失败。
func TestCreateJudgesTheCatalogOnly(t *testing.T) {
	ctrl := gomock.NewController(t)
	ra := vmock.NewMockResourceAccess(ctrl)
	ps := vmock.NewMockPermissionService(ctrl)
	cs := vmock.NewMockCatalogService(ctrl)
	rs := &resourceService{ra: ra, ps: ps, cs: cs}
	expectResourceServiceTransaction(t, rs, true)

	cs.EXPECT().ListInternalIDs(gomock.Any()).Return(nil, nil)
	ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
		Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG,
		ID:   "c-1",
	}, []string{interfaces.OPERATION_TYPE_RESOURCE_MANAGE}).Return(nil).Times(1)
	cs.EXPECT().CheckExistByID(gomock.Any(), "c-1").Return(true, nil)
	ra.EXPECT().Create(gomock.Any(), gomock.Not(nil), gomock.Any()).Return(nil)

	// 创建者只拿 view_detail:管理权坐在目录上,取数权要显式发。
	ps.EXPECT().CreateResources(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ []interfaces.PermissionResource, ops []string) error {
			if len(ops) != 1 || ops[0] != interfaces.OPERATION_TYPE_VIEW_DETAIL {
				t.Errorf("创建者授权 = %v, want 只有 view_detail", ops)
			}
			return nil
		},
	)

	if _, err := rs.Create(context.Background(), &interfaces.ResourceRequest{
		CatalogID: "c-1", Name: "t1", Category: "table",
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestViewDetailAloneDoesNotGrantQueryData 是拆分两个读动词的意义所在:看得见
// 结构不等于读得到行。
//
// 在取数路径判上 query_data 之前(#571),这个区分只存在于词表里——取数只验
// view_detail,拆不拆一个样。这条用例钉住判定本身;取数入口的那次调用在
// PostResourceDataByEx 里,它开头要 verifyOAuth,没有 hydra 桩没法在单测里走完。
func TestViewDetailAloneDoesNotGrantQueryData(t *testing.T) {
	ctrl := gomock.NewController(t)
	ps := vmock.NewMockPermissionService(ctrl)
	ra := vmock.NewMockResourceAccess(ctrl)
	cs := vmock.NewMockCatalogService(ctrl)
	rs := &resourceService{ps: ps, ra: ra, cs: cs}

	ra.EXPECT().GetByID(gomock.Any(), "r-1").Return(&interfaces.Resource{
		ID: "r-1", CatalogID: "c-1",
	}, nil)
	cs.EXPECT().ListInternalIDs(gomock.Any()).Return(nil, nil)

	denied := errors.New("denied")
	// 表上只有 view_detail,所以 query_data 被拒;目录上也没有,回落同样拒。
	ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
		Type: interfaces.AUTH_RESOURCE_TYPE_RESOURCE, ID: "r-1",
	}, []string{interfaces.OPERATION_TYPE_QUERY_DATA}).Return(denied)
	ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
		Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG, ID: "c-1",
	}, []string{interfaces.OPERATION_TYPE_QUERY_DATA}).Return(errors.New("catalog says no"))

	err := rs.CheckResourcePermission(context.Background(), "r-1", interfaces.OPERATION_TYPE_QUERY_DATA)
	assert.Same(t, denied, err, "只有 view_detail 的人不该读到数据")
}

// TestCheckResourcePermissionHidesUnknownResources: 查不到的资源报 403 而不是
// 404——调用方还没证明它能看见这个资源，告诉它哪些 id 存在本身就是泄露。
func TestCheckResourcePermissionHidesUnknownResources(t *testing.T) {
	ctrl := gomock.NewController(t)
	ra := vmock.NewMockResourceAccess(ctrl)
	rs := &resourceService{ra: ra}

	ra.EXPECT().GetByID(gomock.Any(), "gone").Return(nil, nil)

	err := rs.CheckResourcePermission(context.Background(), "gone", interfaces.OPERATION_TYPE_QUERY_DATA)
	require.Error(t, err)
	var httpErr *rest.HTTPError
	require.True(t, errors.As(err, &httpErr), "want an HTTPError, got %v", err)
	assert.Equal(t, http.StatusForbidden, httpErr.HTTPCode,
		"查不到就报 404 会把「哪些 id 存在」告诉一个还没证明自己能看的调用方")
}

// TestFilterAuthorizedResourcesRunsTheTwoQuestions 钉住批量过滤走的是同一套判定:
// 先问这张表,拒了再问它所在的目录。内部目录下的表按 internal_resource 类型问,
// 所以持业务 resource:* 的人答不上——平台自己的表不会从任务列表里漏出去;而被
// 单独授过那个内部目录的人,通过目录回落照样看得到。
func TestFilterAuthorizedResourcesRunsTheTwoQuestions(t *testing.T) {
	ctrl := gomock.NewController(t)
	ps := vmock.NewMockPermissionService(ctrl)
	ra := vmock.NewMockResourceAccess(ctrl)
	cs := vmock.NewMockCatalogService(ctrl)
	rs := &resourceService{ps: ps, ra: ra, cs: cs}

	cs.EXPECT().ListInternalIDs(gomock.Any()).Return([]string{"int-cat"}, nil).AnyTimes()
	ra.EXPECT().ListIDs(gomock.Any(), interfaces.ResourcesQueryParams{CatalogID: "int-cat"}).
		Return([]string{"int-a"}, nil)

	// 业务表按 resource 类型问,内部表按 internal_resource 类型问——分型不能混。
	ps.EXPECT().FilterResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_RESOURCE,
		[]string{"biz-a", "biz-b"}, []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, true, gomock.Any()).
		Return(map[string]interfaces.PermissionResourceOps{"biz-a": {ResourceID: "biz-a"}}, nil)
	ps.EXPECT().FilterResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_INTERNAL_RESOURCE,
		[]string{"int-a"}, []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, true, gomock.Any()).
		Return(map[string]interfaces.PermissionResourceOps{}, nil)

	// 资源侧没批的两张再问各自的目录:业务目录没批,内部目录批了。
	ra.EXPECT().GetByIDsBasic(gomock.Any(), []string{"biz-b", "int-a"}).Return([]*interfaces.Resource{
		{ID: "biz-b", CatalogID: "biz-cat"},
		{ID: "int-a", CatalogID: "int-cat"},
	}, nil)
	ps.EXPECT().FilterResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_CATALOG,
		[]string{"biz-cat"}, []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, true, gomock.Any()).
		Return(map[string]interfaces.PermissionResourceOps{}, nil)
	ps.EXPECT().FilterResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_INTERNAL_CATALOG,
		[]string{"int-cat"}, []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}, true, gomock.Any()).
		Return(map[string]interfaces.PermissionResourceOps{
			"int-cat": {ResourceID: "int-cat",
				Operations: []string{interfaces.OPERATION_TYPE_VIEW_DETAIL}},
		}, nil)

	allowed, err := rs.FilterAuthorizedResources(context.Background(),
		[]string{"biz-a", "biz-b", "int-a"}, interfaces.OPERATION_TYPE_VIEW_DETAIL)
	require.NoError(t, err)
	assert.True(t, allowed["biz-a"], "表上直接授过权")
	assert.False(t, allowed["biz-b"], "表和目录都没批")
	assert.True(t, allowed["int-a"], "内部目录授过权,它下面的表就该看得见")
}

// TestFilterAuthorizedResourcesDeduplicatesAndShortCircuits: 同一张表在一页里出现
// 多次只问一次;一个 id 都没有时一次请求都不发。
func TestFilterAuthorizedResourcesDeduplicatesAndShortCircuits(t *testing.T) {
	ctrl := gomock.NewController(t)
	ps := vmock.NewMockPermissionService(ctrl)
	ra := vmock.NewMockResourceAccess(ctrl)
	cs := vmock.NewMockCatalogService(ctrl)
	rs := &resourceService{ps: ps, ra: ra, cs: cs}

	// 空输入:ListInternalIDs 都不该被调用。
	allowed, err := rs.FilterAuthorizedResources(context.Background(), nil, interfaces.OPERATION_TYPE_VIEW_DETAIL)
	require.NoError(t, err)
	assert.Empty(t, allowed)

	cs.EXPECT().ListInternalIDs(gomock.Any()).Return(nil, nil)
	ps.EXPECT().FilterResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_RESOURCE,
		[]string{"res-1"}, gomock.Any(), true, gomock.Any()).
		Return(map[string]interfaces.PermissionResourceOps{"res-1": {ResourceID: "res-1"}}, nil).Times(1)

	allowed, err = rs.FilterAuthorizedResources(context.Background(),
		[]string{"res-1", "res-1", "", "res-1"}, interfaces.OPERATION_TYPE_VIEW_DETAIL)
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{"res-1": true}, allowed)
}
