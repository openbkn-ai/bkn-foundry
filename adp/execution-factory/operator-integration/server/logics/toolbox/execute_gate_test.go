// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package toolbox

import (
	"context"
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
)

// The execution gate for a box's publication (#1483). A box taken offline used to keep every
// enabled tool runnable for anyone still holding its ids; the tool's own flag was the only check.
// The tool table is not consulted at all here — a strict mock with no expectation proves it.
func TestExecuteToolCoreRefusesAToolOfAnUnpublishedBox(t *testing.T) {
	for _, status := range []interfaces.BizStatus{interfaces.BizStatusOffline, interfaces.BizStatusUnpublish} {
		Convey("工具箱状态为 "+string(status)+":执行被拒,不再去读工具", t, func() {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			boxes := mocks.NewMockIToolboxDB(ctrl)
			boxes.EXPECT().SelectToolBox(gomock.Any(), "box-1").
				Return(true, &model.ToolboxDB{BoxID: "box-1", Name: "下线的箱", Status: string(status)}, nil)
			svc := &ToolServiceImpl{Logger: logger.DefaultLogger(), ToolBoxDB: boxes, ToolDB: mocks.NewMockIToolDB(ctrl)}

			_, err := svc.ExecuteToolCore(context.Background(), &interfaces.ExecuteToolReq{BoxID: "box-1", ToolID: "t-1"})
			So(err, ShouldNotBeNil)
			So(strings.Contains(err.Error(), "ToolNotAvailable"), ShouldBeTrue)
		})
	}
}
