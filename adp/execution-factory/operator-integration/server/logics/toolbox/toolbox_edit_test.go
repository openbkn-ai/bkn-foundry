package toolbox

import (
	"context"
	"database/sql"
	"net/http"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	myErr "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
)

// TestUpdateToolBoxMetadataTypeFallback Overrides the behavior of backfilling existing types when metadata_type is omitted in an edit request.
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
		// The name remains unchanged to avoid duplicate name verification and permission resource change notifications.
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
			// After backfilling, follow the openapi branch. The service address still needs to be verified.
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
			// Backfill takes effect: subsequent branches get the existing types.
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
			// ValidatorURL is not declared. Expectation: Once called, gomock will directly fail.
			mockDBTx.EXPECT().GetTx(gomock.Any()).Return(tx, nil)
			mockToolBoxDB.EXPECT().UpdateToolBox(gomock.Any(), tx, stored).DoAndReturn(
				func(_ context.Context, _ *sql.Tx, box *model.ToolboxDB) error {
					// The function branch does not touch the service address and retains the original value.
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
