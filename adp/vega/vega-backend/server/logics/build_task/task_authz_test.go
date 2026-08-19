// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package build_task

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/interfaces"
	mock_interfaces "vega-backend/interfaces/mock"
)

// 构建任务此前全线没有任何权限判定：列表返回全量，详情/启停/删除对任意已登录
// 账号开放（#472）。这组用例钉住「拒绝时到此为止」——不是少返回几个字段，是
// 根本不执行。

func TestBuildTaskWritesRequireTaskManage(t *testing.T) {
	denied := errors.New("forbidden")

	t.Run("start", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bta := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		rs := mock_interfaces.NewMockResourceService(ctrl)
		svc := &buildTaskService{bta: bta, rs: rs}

		bta.EXPECT().GetByID(gomock.Any(), "task-1").Return(&interfaces.BuildTask{
			ID: "task-1", ResourceID: "res-1", CatalogID: "cat-1",
			Status: interfaces.BuildTaskStatusStopped,
		}, nil)
		rs.EXPECT().CheckResourcePermission(gomock.Any(), "res-1",
			interfaces.OPERATION_TYPE_TASK_MANAGE).Return(denied)
		// 状态流转与落库一次都不该发生。

		assert.Same(t, denied, svc.Start(context.Background(), "task-1", false))
	})

	t.Run("stop", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bta := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		rs := mock_interfaces.NewMockResourceService(ctrl)
		svc := &buildTaskService{bta: bta, rs: rs}

		bta.EXPECT().GetByID(gomock.Any(), "task-1").Return(&interfaces.BuildTask{
			ID: "task-1", ResourceID: "res-1", Status: interfaces.BuildTaskStatusRunning,
		}, nil)
		rs.EXPECT().CheckResourcePermission(gomock.Any(), "res-1",
			interfaces.OPERATION_TYPE_TASK_MANAGE).Return(denied)

		assert.Same(t, denied, svc.Stop(context.Background(), "task-1"))
	})

	t.Run("delete 整批停下，不删已通过的那些", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		bta := mock_interfaces.NewMockBuildTaskAccess(ctrl)
		rs := mock_interfaces.NewMockResourceService(ctrl)
		svc := &buildTaskService{bta: bta, rs: rs}

		bta.EXPECT().GetByID(gomock.Any(), "task-1").Return(&interfaces.BuildTask{
			ID: "task-1", ResourceID: "res-1", Status: interfaces.BuildTaskStatusStopped,
		}, nil)
		rs.EXPECT().CheckResourcePermission(gomock.Any(), "res-1",
			interfaces.OPERATION_TYPE_TASK_MANAGE).Return(denied)
		// DeleteByIDs 未被期望——一条没权限就该整批不删。

		assert.Same(t, denied, svc.DeleteByIDs(context.Background(), []string{"task-1"}, false, false))
	})
}

// TestBuildTaskReadRequiresViewDetail：读一个任务等于读它构建的那张表。
func TestBuildTaskReadRequiresViewDetail(t *testing.T) {
	ctrl := gomock.NewController(t)
	bta := mock_interfaces.NewMockBuildTaskAccess(ctrl)
	rs := mock_interfaces.NewMockResourceService(ctrl)
	svc := &buildTaskService{bta: bta, rs: rs}

	denied := errors.New("forbidden")
	bta.EXPECT().GetByID(gomock.Any(), "task-1").Return(&interfaces.BuildTask{
		ID: "task-1", ResourceID: "res-1",
	}, nil)
	rs.EXPECT().CheckResourcePermission(gomock.Any(), "res-1",
		interfaces.OPERATION_TYPE_VIEW_DETAIL).Return(denied)

	task, err := svc.GetByID(context.Background(), "task-1")
	require.Nil(t, task)
	assert.Same(t, denied, err)
}

// TestBuildTaskCreateRequiresTaskManage: 建任务是对那张表的写操作，看得见不等于
// 能建——GetByID 只证明了前者。
func TestBuildTaskCreateRequiresTaskManage(t *testing.T) {
	ctrl := gomock.NewController(t)
	rs := mock_interfaces.NewMockResourceService(ctrl)
	svc := &buildTaskService{rs: rs}

	denied := errors.New("forbidden")
	rs.EXPECT().GetByID(gomock.Any(), "res-1").Return(&interfaces.Resource{
		ID: "res-1", CatalogID: "cat-1", Category: interfaces.ResourceCategoryTable,
	}, nil)
	rs.EXPECT().CheckResourcePermission(gomock.Any(), "res-1",
		interfaces.OPERATION_TYPE_TASK_MANAGE).Return(denied)

	id, err := svc.Create(context.Background(), &interfaces.CreateBuildTaskRequest{ResourceID: "res-1"})
	assert.Empty(t, id)
	assert.Same(t, denied, err)
}
