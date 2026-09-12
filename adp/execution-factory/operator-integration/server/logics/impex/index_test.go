// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package impex

import (
	"context"
	"errors"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
)

// Imports write rows straight to the tables inside one transaction, so none of the per-write
// index syncs fire for them (#1483). The manager asks for a reconcile once the transaction has
// committed — and not at all when the import failed and was rolled back.
func TestImportReconcilesTheCapabilityIndexAfterCommit(t *testing.T) {
	newManager := func(ctrl *gomock.Controller, commit bool) (*componentImpexManager, *mocks.MockIToolService, *mocks.MockIMCPService) {
		db, mock, err := sqlmock.New()
		So(err, ShouldBeNil)
		mock.ExpectBegin()
		if commit {
			mock.ExpectCommit()
		} else {
			mock.ExpectRollback()
		}
		tx, err := db.Begin()
		So(err, ShouldBeNil)
		dbTx := mocks.NewMockDBTx(ctrl)
		dbTx.EXPECT().GetTx(gomock.Any()).Return(tx, nil)
		boxes := mocks.NewMockIToolService(ctrl)
		servers := mocks.NewMockIMCPService(ctrl)
		return &componentImpexManager{Logger: logger.DefaultLogger(), DBTx: dbTx, ToolboxMgr: boxes, MCPMgr: servers}, boxes, servers
	}
	data := &interfaces.ComponentImpexConfigModel{}

	Convey("工具箱导入成功:提交后触发一次工具箱索引对账", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		m, boxes, _ := newManager(ctrl, true)
		boxes.EXPECT().Import(gomock.Any(), gomock.Any(), gomock.Any(), data, "u1").Return(nil)
		boxes.EXPECT().ReconcileCapabilityIndexAsync(gomock.Any()).Times(1)
		So(m.importConfigWithTx(context.Background(), interfaces.ComponentTypeToolBox, data, "", "u1"), ShouldBeNil)
	})

	Convey("MCP 导入成功:MCP 与工具箱的对账都触发(MCP 包里可能带工具箱)", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		m, boxes, servers := newManager(ctrl, true)
		servers.EXPECT().Import(gomock.Any(), gomock.Any(), gomock.Any(), data, "u1").Return(nil)
		servers.EXPECT().ReconcileCapabilityIndexAsync(gomock.Any()).Times(1)
		boxes.EXPECT().ReconcileCapabilityIndexAsync(gomock.Any()).Times(1)
		So(m.importConfigWithTx(context.Background(), interfaces.ComponentTypeMCP, data, "", "u1"), ShouldBeNil)
	})

	Convey("导入失败回滚:不触发对账(没有什么可对)", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		m, boxes, _ := newManager(ctrl, false)
		boxes.EXPECT().Import(gomock.Any(), gomock.Any(), gomock.Any(), data, "u1").Return(errors.New("bad archive"))
		// No ReconcileCapabilityIndexAsync expectation: a call would fail the strict mock.
		So(m.importConfigWithTx(context.Background(), interfaces.ComponentTypeToolBox, data, "", "u1"), ShouldNotBeNil)
	})
}
