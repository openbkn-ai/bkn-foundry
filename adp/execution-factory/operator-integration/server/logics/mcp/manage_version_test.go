// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mcp

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/utils"
)

// lifecycleRecordingInstances stands in for the instance service and records what the version
// lifecycle asks of it. Anything else it is asked panics through the nil embedded interface.
type lifecycleRecordingInstances struct {
	interfaces.InstanceService
	deleted  []int
	created  []int
	upgraded []int
}

func (r *lifecycleRecordingInstances) CreateMCPInstance(_ context.Context, req *interfaces.MCPInstanceCreateRequest) (*interfaces.MCPInstanceCreateResponse, error) {
	r.created = append(r.created, req.Version)
	return &interfaces.MCPInstanceCreateResponse{MCPID: req.MCPID, Version: req.Version}, nil
}

func (r *lifecycleRecordingInstances) DeleteMCPInstance(_ context.Context, _ string, version int) error {
	r.deleted = append(r.deleted, version)
	return nil
}

func (r *lifecycleRecordingInstances) UpgradeMCPInstance(_ context.Context, req *interfaces.MCPInstanceCreateRequest) (*interfaces.MCPInstanceCreateResponse, error) {
	r.upgraded = append(r.upgraded, req.Version)
	return &interfaces.MCPInstanceCreateResponse{MCPID: req.MCPID, Version: req.Version}, nil
}

func releaseHistory(version int) *model.MCPServerReleaseHistoryDB {
	return &model.MCPServerReleaseHistoryDB{
		ID:      int64(version),
		MCPID:   "mcp-1",
		Version: version,
		MCPRelease: utils.ObjectToJSON(model.MCPServerReleaseDB{
			MCPID:        "mcp-1",
			Version:      version,
			CreationType: interfaces.MCPCreationTypeToolImported.String(),
		}),
	}
}

// Entering editing by the status API changes nothing but the status. Moving the config to a new
// version there left a version with no tools and no instance, which a republish then released (#1478).
func TestEnteringEditingKeepsTheVersion(t *testing.T) {
	Convey("published → editing:只改状态,版本号不动,不查发布历史", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		configs := mocks.NewMockDBMCPServerConfig(ctrl)
		configs.EXPECT().SelectByID(gomock.Any(), gomock.Any(), "mcp-1").Return(&model.MCPServerConfigDB{
			MCPID:   "mcp-1",
			Status:  string(interfaces.BizStatusPublished),
			Version: 2,
		}, nil)
		configs.EXPECT().UpdateStatus(gomock.Any(), gomock.Any(), "mcp-1", string(interfaces.BizStatusEditing), "u-1", 2).Return(nil)
		svc := &mcpServiceImpl{
			logger:                    logger.DefaultLogger(),
			DBMCPServerConfig:         configs,
			DBMCPServerReleaseHistory: mocks.NewMockDBMCPServerReleaseHistory(ctrl),
		}

		_, resp, err := svc.modifyMCPStatus(context.Background(), nil, &interfaces.UpdateMCPStatusRequest{
			MCPID: "mcp-1", Status: interfaces.BizStatusEditing, UserID: "u-1",
		})
		So(err, ShouldBeNil)
		So(resp.Status, ShouldEqual, interfaces.BizStatusEditing)
	})
}

// A released version is immutable: the release history keeps its tools and the instance pool may
// be serving it. An edit therefore moves to the next version whenever the current one has been
// released, whatever the status is.
func TestUpdateMCPConfigVersionMovesOffAReleasedVersion(t *testing.T) {
	cases := []struct {
		name      string
		status    interfaces.BizStatus
		version   int
		histories []*model.MCPServerReleaseHistoryDB
		want      int
	}{
		{"从未发布:原地编辑", interfaces.BizStatusUnpublish, 1, nil, 1},
		{"published:移到下一版", interfaces.BizStatusPublished, 2, []*model.MCPServerReleaseHistoryDB{releaseHistory(2)}, 3},
		{"offline:移到下一版", interfaces.BizStatusOffline, 2, []*model.MCPServerReleaseHistoryDB{releaseHistory(2)}, 3},
		{"仅切入 editing 后首次编辑:移到下一版", interfaces.BizStatusEditing, 2, []*model.MCPServerReleaseHistoryDB{releaseHistory(2)}, 3},
		{"下架后仅切回 unpublish 再首次编辑:移到下一版", interfaces.BizStatusUnpublish, 2, []*model.MCPServerReleaseHistoryDB{releaseHistory(2)}, 3},
		{"草稿已在新版本:原地编辑", interfaces.BizStatusEditing, 3, []*model.MCPServerReleaseHistoryDB{releaseHistory(2)}, 3},
	}
	for _, c := range cases {
		Convey(c.name, t, func() {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			histories := mocks.NewMockDBMCPServerReleaseHistory(ctrl)
			histories.EXPECT().SelectByMCPID(gomock.Any(), gomock.Any(), "mcp-1").Return(c.histories, nil)
			svc := &mcpServiceImpl{logger: logger.DefaultLogger(), DBMCPServerReleaseHistory: histories}
			config := &model.MCPServerConfigDB{MCPID: "mcp-1", Status: string(c.status), Version: c.version}

			version, err := svc.updateMCPConfigVersion(context.Background(), nil, config)
			So(err, ShouldBeNil)
			So(version, ShouldEqual, c.want)
			So(config.Version, ShouldEqual, c.want)
		})
	}
}

// Publishing retires the previous release's instance, but republishing the version that is
// already released — offline → published, or editing → published with nothing edited — must keep
// it: it is the instance about to be served.
func TestAddMCPHistoryRetiresOnlyASupersededInstance(t *testing.T) {
	Convey("重新发布同一版本:保留该版本实例,只替换历史记录", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		histories := mocks.NewMockDBMCPServerReleaseHistory(ctrl)
		histories.EXPECT().SelectByMCPID(gomock.Any(), gomock.Any(), "mcp-1").
			Return([]*model.MCPServerReleaseHistoryDB{releaseHistory(2)}, nil)
		histories.EXPECT().DeleteByMCPIDAndVersion(gomock.Any(), gomock.Any(), "mcp-1", 2).Return(nil)
		histories.EXPECT().Insert(gomock.Any(), gomock.Any(), gomock.Any()).Return("h-2", nil)
		instances := &lifecycleRecordingInstances{}
		svc := &mcpServiceImpl{logger: logger.DefaultLogger(), DBMCPServerReleaseHistory: histories, MCPInstanceService: instances}

		err := svc.addMCPHistory(context.Background(), nil, &model.MCPServerReleaseDB{
			MCPID: "mcp-1", Version: 2, CreationType: interfaces.MCPCreationTypeToolImported.String(),
		}, "u-1")
		So(err, ShouldBeNil)
		So(instances.deleted, ShouldBeEmpty)
	})

	Convey("发布新版本:下线上一发布版本的实例", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		histories := mocks.NewMockDBMCPServerReleaseHistory(ctrl)
		histories.EXPECT().SelectByMCPID(gomock.Any(), gomock.Any(), "mcp-1").
			Return([]*model.MCPServerReleaseHistoryDB{releaseHistory(2)}, nil)
		histories.EXPECT().Insert(gomock.Any(), gomock.Any(), gomock.Any()).Return("h-3", nil)
		instances := &lifecycleRecordingInstances{}
		svc := &mcpServiceImpl{logger: logger.DefaultLogger(), DBMCPServerReleaseHistory: histories, MCPInstanceService: instances}

		err := svc.addMCPHistory(context.Background(), nil, &model.MCPServerReleaseDB{
			MCPID: "mcp-1", Version: 3, CreationType: interfaces.MCPCreationTypeToolImported.String(),
		}, "u-1")
		So(err, ShouldBeNil)
		So(instances.deleted, ShouldResemble, []int{2})
	})
}

// Refreshing the instance after an edit must leave a persisted deployment for the config's version
// even when there is none yet. Drafts moved to a new version by the old status-only editing have
// no deployment; updating one only rebuilt it in memory, lost on the next restart.
func TestRefreshMCPServerInstanceCreatesOrUpdates(t *testing.T) {
	Convey("编辑同一版本:走 create-or-update,不依赖部署记录已存在", t, func() {
		instances := &lifecycleRecordingInstances{}
		svc := &mcpServiceImpl{logger: logger.DefaultLogger(), MCPInstanceService: instances}

		err := svc.refreshMCPServerInstance(context.Background(), 3, 3, &model.MCPServerConfigDB{MCPID: "mcp-1", Version: 3}, nil)
		So(err, ShouldBeNil)
		So(instances.upgraded, ShouldResemble, []int{3})
	})

	Convey("编辑移到新版本:新建部署", t, func() {
		instances := &lifecycleRecordingInstances{}
		svc := &mcpServiceImpl{logger: logger.DefaultLogger(), MCPInstanceService: instances}

		err := svc.refreshMCPServerInstance(context.Background(), 2, 3, &model.MCPServerConfigDB{MCPID: "mcp-1", Version: 3}, nil)
		So(err, ShouldBeNil)
		So(instances.created, ShouldResemble, []int{3})
		So(instances.upgraded, ShouldBeEmpty)
	})
}
