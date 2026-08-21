// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package catalog

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	verrors "vega-backend/errors"
	"vega-backend/interfaces"
	mock_interfaces "vega-backend/interfaces/mock"
)

// CheckTaskPermission 是构建 / 探查 / 语义三类任务共用的判定入口。它与
// CheckCatalogPermission 的唯一区别是：目录被删之后仍然要有出路。
//
// 删目录时任务不跟着删，只被标成 cancelled，行还在。如果判在一个已经查不到的
// 目录上，答案恒定是 403——而且是在查库那一步就返回，连 casbin 都问不到，所以
// 连超级管理员也清理不掉。这些任务会成为谁都看不见、谁都删不掉的死行，还继续
// 计入 total。
func TestCheckTaskPermissionThreeLevels(t *testing.T) {
	forbidden := func() error {
		return rest.NewHTTPError(context.Background(), http.StatusForbidden, rest.PublicError_Forbidden)
	}

	t.Run("目录还在，就判这个目录", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		ca := mock_interfaces.NewMockCatalogAccess(ctrl)
		ps := mock_interfaces.NewMockPermissionService(ctrl)
		cs := &catalogService{ca: ca, ps: ps}

		ca.EXPECT().GetByID(gomock.Any(), "cat-1").
			Return(&interfaces.Catalog{ID: "cat-1"}, nil).Times(2)
		ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
			Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG, ID: "cat-1",
		}, []string{interfaces.OPERATION_TYPE_TASK_MANAGE}).Return(nil)
		// 类型级授权一次都不该被问到。

		require.NoError(t, cs.CheckTaskPermission(context.Background(), "cat-1",
			interfaces.OPERATION_TYPE_TASK_MANAGE))
	})

	t.Run("目录没了，退到类型级授权", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		ca := mock_interfaces.NewMockCatalogAccess(ctrl)
		ps := mock_interfaces.NewMockPermissionService(ctrl)
		cs := &catalogService{ca: ca, ps: ps}

		ca.EXPECT().GetByID(gomock.Any(), "cat-gone").Return(nil, nil)
		ps.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{
			Type: interfaces.AUTH_RESOURCE_TYPE_CATALOG, ID: interfaces.RESOURCE_ID_ALL,
		}, []string{interfaces.OPERATION_TYPE_TASK_MANAGE}).Return(nil)

		require.NoError(t, cs.CheckTaskPermission(context.Background(), "cat-gone",
			interfaces.OPERATION_TYPE_TASK_MANAGE),
			"持 catalog:* 的人本来就看得见每一个目录，多看一条没有父的任务不构成新的暴露")
	})

	t.Run("目录没了且没有类型级授权，仍然拒绝", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		ca := mock_interfaces.NewMockCatalogAccess(ctrl)
		ps := mock_interfaces.NewMockPermissionService(ctrl)
		cs := &catalogService{ca: ca, ps: ps}

		ca.EXPECT().GetByID(gomock.Any(), "cat-gone").Return(nil, nil)
		ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(forbidden())

		err := cs.CheckTaskPermission(context.Background(), "cat-gone",
			interfaces.OPERATION_TYPE_TASK_MANAGE)
		require.Error(t, err)
		var httpErr *rest.HTTPError
		require.True(t, errors.As(err, &httpErr))
		assert.Equal(t, http.StatusForbidden, httpErr.HTTPCode)
	})

	t.Run("鉴权服务答不上来要上抛，不能当成拒绝", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		ca := mock_interfaces.NewMockCatalogAccess(ctrl)
		ps := mock_interfaces.NewMockPermissionService(ctrl)
		cs := &catalogService{ca: ca, ps: ps}

		ca.EXPECT().GetByID(gomock.Any(), "cat-gone").Return(nil, nil)
		boom := rest.NewHTTPError(context.Background(), http.StatusInternalServerError,
			verrors.VegaBackend_InternalError_FilterResourcesFailed)
		ps.EXPECT().CheckPermission(gomock.Any(), gomock.Any(), gomock.Any()).Return(boom)

		err := cs.CheckTaskPermission(context.Background(), "cat-gone",
			interfaces.OPERATION_TYPE_TASK_MANAGE)
		require.Error(t, err)
		var httpErr *rest.HTTPError
		require.True(t, errors.As(err, &httpErr))
		assert.Equal(t, http.StatusInternalServerError, httpErr.HTTPCode,
			"把故障读成拒绝，会让一次不可用变成一个静默的权限决定")
	})
}
