package toolbox

import (
	"context"
	"database/sql"
	"net/http"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	myErr "github.com/openbkn-ai/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/mocks"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
)

// TestUpdateToolBoxMetadataTypeFallback 覆盖编辑请求省略 metadata_type 时回填已存类型的行为。
func TestUpdateToolBoxMetadataTypeFallback(t *testing.T) {
	Convey("TestUpdateToolBox:编辑请求省略 metadata_type", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockDBTx := mocks.NewMockDBTx(ctrl)
		mockToolBoxDB := mocks.NewMockIToolboxDB(ctrl)
		mockCategoryManager := mocks.NewMockCategoryManager(ctrl)
		mockAuthService := mocks.NewMockIAuthorizationService(ctrl)
		mockValidator := mocks.NewMockValidator(ctrl)
		toolbox := &ToolServiceImpl{
			DBTx:            mockDBTx,
			ToolBoxDB:       mockToolBoxDB,
			CategoryManager: mockCategoryManager,
			Logger:          logger.DefaultLogger(),
			Validator:       mockValidator,
			AuthService:     mockAuthService,
		}

		tx := &sql.Tx{}
		rollbackPatch := gomonkey.ApplyFunc((*sql.Tx).Rollback, func(*sql.Tx) error { return nil })
		defer rollbackPatch.Reset()
		commitPatch := gomonkey.ApplyFunc((*sql.Tx).Commit, func(*sql.Tx) error { return nil })
		defer commitPatch.Reset()

		const boxID = "box_id_1"
		// 名称不变,避免走重名校验与权限资源变更通知
		const boxName = "box_name_1"
		newToolBox := func(metadataType interfaces.MetadataType, serverURL string) *model.ToolboxDB {
			return &model.ToolboxDB{
				BoxID:        boxID,
				Name:         boxName,
				Description:  "old_desc",
				ServerURL:    serverURL,
				Category:     "other_category",
				MetadataType: string(metadataType),
			}
		}
		newReq := func() *interfaces.UpdateToolBoxReq {
			return &interfaces.UpdateToolBoxReq{
				UserID:   "user_1",
				BoxID:    boxID,
				BoxName:  boxName,
				BoxDesc:  "new_desc",
				Category: interfaces.BizCategory("other_category"),
			}
		}
		expectPreflight := func(stored *model.ToolboxDB) {
			mockAuthService.EXPECT().GetAccessor(gomock.Any(), "user_1").Return(&interfaces.AuthAccessor{ID: "user_1"}, nil)
			mockAuthService.EXPECT().CheckModifyPermission(gomock.Any(), gomock.Any(), boxID, interfaces.AuthResourceTypeToolBox).Return(nil)
			mockCategoryManager.EXPECT().CheckCategory(gomock.Any()).Return(true)
			mockToolBoxDB.EXPECT().SelectToolBox(gomock.Any(), boxID).Return(true, stored, nil)
		}

		Convey("已存 openapi 工具箱,请求带合法 box_svc_url 应更新成功", func() {
			stored := newToolBox(interfaces.MetadataTypeAPI, "http://old.example.com")
			expectPreflight(stored)
			// 回填后按 openapi 分支走,服务地址仍需校验
			mockValidator.EXPECT().ValidatorURL(gomock.Any(), "http://new.example.com").Return(nil)
			mockDBTx.EXPECT().GetTx(gomock.Any()).Return(tx, nil)
			mockToolBoxDB.EXPECT().UpdateToolBox(gomock.Any(), tx, stored).DoAndReturn(
				func(_ context.Context, _ *sql.Tx, box *model.ToolboxDB) error {
					So(box.ServerURL, ShouldEqual, "http://new.example.com")
					So(box.Description, ShouldEqual, "new_desc")
					So(box.UpdateUser, ShouldEqual, "user_1")
					return nil
				})

			req := newReq()
			req.BoxSvcURL = "http://new.example.com"
			resp, err := toolbox.UpdateToolBox(context.TODO(), req)
			So(err, ShouldBeNil)
			So(resp.BoxID, ShouldEqual, boxID)
			// 回填生效:后续分支拿到的是已存类型
			So(req.MetadataType, ShouldEqual, interfaces.MetadataTypeAPI)
		})

		Convey("已存 openapi 工具箱,请求不带 box_svc_url 应报错(契约:openapi 编辑必须带 URL)", func() {
			expectPreflight(newToolBox(interfaces.MetadataTypeAPI, "http://old.example.com"))
			mockValidator.EXPECT().ValidatorURL(gomock.Any(), "").Return(
				myErr.NewHTTPError(context.TODO(), http.StatusBadRequest, myErr.ErrExtOpenAPIInvalidURLFormat, "URL cannot be empty"))

			resp, err := toolbox.UpdateToolBox(context.TODO(), newReq())
			So(err, ShouldNotBeNil)
			So(resp, ShouldBeNil)
			httpErr, ok := err.(*myErr.HTTPError)
			So(ok, ShouldBeTrue)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
		})

		Convey("已存 function 工具箱,请求不带 metadata_type 不应校验服务地址", func() {
			stored := newToolBox(interfaces.MetadataTypeFunc, "http://function.example.com")
			expectPreflight(stored)
			// 未声明 ValidatorURL 期望:一旦被调用 gomock 直接判失败
			mockDBTx.EXPECT().GetTx(gomock.Any()).Return(tx, nil)
			mockToolBoxDB.EXPECT().UpdateToolBox(gomock.Any(), tx, stored).DoAndReturn(
				func(_ context.Context, _ *sql.Tx, box *model.ToolboxDB) error {
					// function 分支不碰服务地址,保留原值
					So(box.ServerURL, ShouldEqual, "http://function.example.com")
					So(box.Description, ShouldEqual, "new_desc")
					return nil
				})

			req := newReq()
			resp, err := toolbox.UpdateToolBox(context.TODO(), req)
			So(err, ShouldBeNil)
			So(resp.BoxID, ShouldEqual, boxID)
			So(req.MetadataType, ShouldEqual, interfaces.MetadataTypeFunc)
		})
	})
}
