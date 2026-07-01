// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package resource

import (
	"context"
	"testing"

	"go.uber.org/mock/gomock"

	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
)

// newS2STestService 构建 resourceService，internalCatalogIDs 指定哪些目录为系统内部目录。
func newS2STestService(t *testing.T, internalCatalogIDs []string) (
	*resourceService, *vmock.MockResourceAccess, *vmock.MockPermissionService, *vmock.MockUserMgmtService) {
	ctrl := gomock.NewController(t)
	ra := vmock.NewMockResourceAccess(ctrl)
	ps := vmock.NewMockPermissionService(ctrl)
	ums := vmock.NewMockUserMgmtService(ctrl)
	cs := vmock.NewMockCatalogService(ctrl)
	rs := &resourceService{ra: ra, ps: ps, ums: ums, cs: cs}
	cs.EXPECT().ListInternalIDs(gomock.Any()).Return(internalCatalogIDs, nil).AnyTimes()
	return rs, ra, ps, ums
}

// 内部资源经 S2S 标记访问 → 跳过 view_detail 鉴权，放行。FilterResources 不应被调用。
func TestGetByID_S2SInternal_Bypass(t *testing.T) {
	rs, ra, _, ums := newS2STestService(t, []string{"cat-int"})
	ra.EXPECT().GetByID(gomock.Any(), "r1").
		Return(&interfaces.Resource{ID: "r1", CatalogID: "cat-int"}, nil)
	ums.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil)
	// 不设置 ps.FilterResources EXPECT：若被调用，gomock 会因 unexpected call 失败。

	ctx := interfaces.WithS2SInternalAccess(context.Background())
	res, err := rs.GetByID(ctx, "r1")
	if err != nil {
		t.Fatalf("内部资源 S2S 访问应放行，却报错: %v", err)
	}
	if res == nil || len(res.Operations) == 0 {
		t.Fatalf("放行后应回填 operations，got %+v", res)
	}
}

// 内部资源无 S2S 标记（外网 / 普通用户）→ FilterResources 命中空 → 403。
func TestGetByID_Internal_NoMarker_Forbidden(t *testing.T) {
	rs, ra, ps, _ := newS2STestService(t, []string{"cat-int"})
	ra.EXPECT().GetByID(gomock.Any(), "r1").
		Return(&interfaces.Resource{ID: "r1", CatalogID: "cat-int"}, nil)
	ps.EXPECT().FilterResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_INTERNAL_RESOURCE,
		gomock.Any(), gomock.Any(), true, gomock.Any()).
		Return(map[string]interfaces.PermissionResourceOps{}, nil)

	_, err := rs.GetByID(context.Background(), "r1")
	if err == nil {
		t.Fatalf("内部资源无 S2S 标记应被拒，却放行了")
	}
}

// 非内部资源即便带 S2S 标记 → 仍走 per-account 鉴权（FilterResources 空 → 403）。
func TestGetByID_NonInternal_WithMarker_StillAuthz(t *testing.T) {
	rs, ra, ps, _ := newS2STestService(t, []string{}) // 无内部目录 → 该资源非内部
	ra.EXPECT().GetByID(gomock.Any(), "r1").
		Return(&interfaces.Resource{ID: "r1", CatalogID: "cat-user"}, nil)
	ps.EXPECT().FilterResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_RESOURCE,
		gomock.Any(), gomock.Any(), true, gomock.Any()).
		Return(map[string]interfaces.PermissionResourceOps{}, nil)

	ctx := interfaces.WithS2SInternalAccess(context.Background())
	_, err := rs.GetByID(ctx, "r1")
	if err == nil {
		t.Fatalf("非内部资源即便带 S2S 标记也应按 per-account 鉴权拒绝，却放行了")
	}
}
