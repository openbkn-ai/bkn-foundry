package skill

import (
	"context"
	"net/http"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
)

// TestGetSkillDetailInternalFace covers #1255: skill details are registered on internal-v1 so
// bkn-backend can validate capability bindings. The internal face must answer the same way for
// every caller and must not turn "unpublished" into "not found".
func TestGetSkillDetailInternalFace(t *testing.T) {
	Convey("内部面技能详情", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		newRegistry := func(skillRepo model.ISkillRepository, authService interfaces.IAuthorizationService) *skillRegistry {
			categoryManager := mocks.NewMockCategoryManager(ctrl)
			categoryManager.EXPECT().GetCategoryName(gomock.Any(), gomock.Any()).Return("通用").AnyTimes()
			userMgnt := mocks.NewMockUserManagement(ctrl)
			userMgnt.EXPECT().GetUsersName(gomock.Any(), gomock.Any()).
				Return(map[string]string{"user-1": "创建者"}, nil).AnyTimes()
			return &skillRegistry{
				skillRepo:       skillRepo,
				AuthService:     authService,
				CategoryManager: categoryManager,
				UserMgnt:        userMgnt,
				Logger:          logger.DefaultLogger(),
			}
		}

		Convey("未发布技能返回 200 与 status，不是 404", func() {
			skillRepo := mocks.NewMockISkillRepository(ctrl)
			skillRepo.EXPECT().SelectSkillByID(gomock.Any(), gomock.Nil(), "skill-1").
				Return(&model.SkillRepositoryDB{
					SkillID: "skill-1", Name: "交期评估", Description: "评估交期",
					Status: interfaces.BizStatusUnpublish.String(), CreateUser: "user-1", UpdateUser: "user-1",
				}, nil)
			// No authorization call is expected at all: the mock controller fails the test if one happens.
			authService := mocks.NewMockIAuthorizationService(ctrl)

			resp, err := newRegistry(skillRepo, authService).GetSkillDetail(context.Background(),
				&interfaces.GetSkillDetailReq{SkillID: "skill-1"})

			So(err, ShouldBeNil)
			So(resp.SkillID, ShouldEqual, "skill-1")
			So(resp.Name, ShouldEqual, "交期评估")
			So(resp.Status, ShouldEqual, interfaces.BizStatusUnpublish)
		})

		Convey("技能不存在返回 404，与「存在但未发布」可区分", func() {
			skillRepo := mocks.NewMockISkillRepository(ctrl)
			skillRepo.EXPECT().SelectSkillByID(gomock.Any(), gomock.Nil(), "skill-missing").Return(nil, nil)
			authService := mocks.NewMockIAuthorizationService(ctrl)

			resp, err := newRegistry(skillRepo, authService).GetSkillDetail(context.Background(),
				&interfaces.GetSkillDetailReq{SkillID: "skill-missing"})

			So(resp, ShouldBeNil)
			httpErr, ok := err.(*errors.HTTPError)
			So(ok, ShouldBeTrue)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusNotFound)
		})

		Convey("同一技能对不同调用者返回相同结果", func() {
			skillRepo := mocks.NewMockISkillRepository(ctrl)
			skillRepo.EXPECT().SelectSkillByID(gomock.Any(), gomock.Nil(), "skill-1").
				Return(&model.SkillRepositoryDB{
					SkillID: "skill-1", Name: "交期评估",
					Status: interfaces.BizStatusPublished.String(), CreateUser: "user-1", UpdateUser: "user-1",
				}, nil).Times(2)
			authService := mocks.NewMockIAuthorizationService(ctrl)
			registry := newRegistry(skillRepo, authService)

			first, err := registry.GetSkillDetail(context.Background(), &interfaces.GetSkillDetailReq{
				UserID: "user-1", SkillID: "skill-1",
			})
			So(err, ShouldBeNil)
			second, err := registry.GetSkillDetail(context.Background(), &interfaces.GetSkillDetailReq{
				UserID: "user-2", SkillID: "skill-1",
			})
			So(err, ShouldBeNil)
			So(second.Name, ShouldEqual, first.Name)
			So(second.Status, ShouldEqual, first.Status)
		})

		Convey("公开面仍按账户判定查看权限", func() {
			skillRepo := mocks.NewMockISkillRepository(ctrl)
			authService := mocks.NewMockIAuthorizationService(ctrl)
			authService.EXPECT().GetAccessor(gomock.Any(), "user-2").
				Return(&interfaces.AuthAccessor{ID: "user-2"}, nil)
			authService.EXPECT().CheckViewPermission(gomock.Any(), gomock.Any(), "skill-1",
				interfaces.AuthResourceTypeSkill).
				Return(errors.NewHTTPError(context.Background(), http.StatusForbidden, errors.ErrExtCommonViewForbidden, nil))

			resp, err := newRegistry(skillRepo, authService).GetSkillDetail(skillPublicCtx(),
				&interfaces.GetSkillDetailReq{UserID: "user-2", SkillID: "skill-1"})

			So(resp, ShouldBeNil)
			httpErr, ok := err.(*errors.HTTPError)
			So(ok, ShouldBeTrue)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusForbidden)
		})
	})
}

// TestGetSkillNamesByIDsInternalFace locks the internal-face contract of the batch name endpoint:
// no per-caller filtering, non-existing IDs silently skipped, response IDs a subset of the request.
func TestGetSkillNamesByIDsInternalFace(t *testing.T) {
	Convey("内部面技能批量取名", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		Convey("不按调用者过滤，不存在的 ID 静默略过", func() {
			skillRepo := mocks.NewMockISkillRepository(ctrl)
			skillRepo.EXPECT().SelectSkillListByIDs(gomock.Any(), []string{"skill-1", "skill-2"}).
				Return([]*model.SkillRepositoryDB{
					{SkillID: "skill-1", Name: "skill one"},
					{SkillID: "skill-2", Name: "skill two"},
				}, nil)
			// No GetAccessor / ResourceListIDs: FilterViewableIDs short-circuits off the public face.
			registry := &skillRegistry{
				skillRepo:   skillRepo,
				AuthService: mocks.NewMockIAuthorizationService(ctrl),
				Logger:      logger.DefaultLogger(),
			}

			resp, err := registry.GetSkillNamesByIDs(context.Background(), []string{"skill-1", "skill-2"})

			So(err, ShouldBeNil)
			So(len(resp.Entries), ShouldEqual, 2)
		})

		Convey("返回的 ID 集合是请求集合的子集", func() {
			skillRepo := mocks.NewMockISkillRepository(ctrl)
			skillRepo.EXPECT().SelectSkillListByIDs(gomock.Any(), []string{"skill-1", "skill-missing"}).
				Return([]*model.SkillRepositoryDB{{SkillID: "skill-1", Name: "skill one"}}, nil)
			registry := &skillRegistry{
				skillRepo:   skillRepo,
				AuthService: mocks.NewMockIAuthorizationService(ctrl),
				Logger:      logger.DefaultLogger(),
			}

			resp, err := registry.GetSkillNamesByIDs(context.Background(), []string{"skill-1", "skill-missing"})

			So(err, ShouldBeNil)
			So(len(resp.Entries), ShouldEqual, 1)
			So(resp.Entries[0].ID, ShouldEqual, "skill-1")
		})
	})
}
