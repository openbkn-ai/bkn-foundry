// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package catalog

import (
	"context"
	"testing"

	"go.uber.org/mock/gomock"

	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
)

func newS2SCatalogService(t *testing.T) (
	*catalogService, *vmock.MockCatalogAccess, *vmock.MockPermissionService, *vmock.MockUserMgmtService) {
	ctrl := gomock.NewController(t)
	ca := vmock.NewMockCatalogAccess(ctrl)
	ps := vmock.NewMockPermissionService(ctrl)
	ums := vmock.NewMockUserMgmtService(ctrl)
	cs := &catalogService{ca: ca, ps: ps, ums: ums}
	return cs, ca, ps, ums
}

// 内部目录经 S2S 标记访问 → 跳过 view_detail 鉴权放行。FilterResources 不应被调用。
func TestCatalogGetByID_S2SInternal_Bypass(t *testing.T) {
	cs, ca, _, ums := newS2SCatalogService(t)
	ca.EXPECT().GetByID(gomock.Any(), "c1").
		Return(&interfaces.Catalog{ID: "c1", Internal: true}, nil)
	ums.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil)
	// 不设置 ps.FilterResources EXPECT：若被调用，gomock 会因 unexpected call 失败。

	ctx := interfaces.WithS2SInternalAccess(context.Background())
	cat, err := cs.GetByID(ctx, "c1", false)
	if err != nil {
		t.Fatalf("内部目录 S2S 访问应放行，却报错: %v", err)
	}
	if cat == nil || len(cat.Operations) == 0 {
		t.Fatalf("放行后应回填 operations，got %+v", cat)
	}
}

// 内部目录无 S2S 标记 → FilterResources 空 → 403。
func TestCatalogGetByID_Internal_NoMarker_Forbidden(t *testing.T) {
	cs, ca, ps, _ := newS2SCatalogService(t)
	ca.EXPECT().GetByID(gomock.Any(), "c1").
		Return(&interfaces.Catalog{ID: "c1", Internal: true}, nil)
	ps.EXPECT().FilterResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_INTERNAL_CATALOG,
		gomock.Any(), gomock.Any(), true, gomock.Any()).
		Return(map[string]interfaces.PermissionResourceOps{}, nil)

	_, err := cs.GetByID(context.Background(), "c1", false)
	if err == nil {
		t.Fatalf("内部目录无 S2S 标记应被拒，却放行了")
	}
}

// 非内部目录即便带 S2S 标记 → 仍按 per-account 鉴权（FilterResources 空 → 403）。
func TestCatalogGetByID_NonInternal_WithMarker_StillAuthz(t *testing.T) {
	cs, ca, ps, _ := newS2SCatalogService(t)
	ca.EXPECT().GetByID(gomock.Any(), "c1").
		Return(&interfaces.Catalog{ID: "c1", Internal: false}, nil)
	ps.EXPECT().FilterResources(gomock.Any(), interfaces.AUTH_RESOURCE_TYPE_CATALOG,
		gomock.Any(), gomock.Any(), true, gomock.Any()).
		Return(map[string]interfaces.PermissionResourceOps{}, nil)

	ctx := interfaces.WithS2SInternalAccess(context.Background())
	_, err := cs.GetByID(ctx, "c1", false)
	if err == nil {
		t.Fatalf("非内部目录即便带 S2S 标记也应按 per-account 鉴权拒绝，却放行了")
	}
}
