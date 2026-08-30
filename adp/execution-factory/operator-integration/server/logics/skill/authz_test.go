package skill

import (
	"context"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
)

// skillPublicCtx constructs the context of the public face, and only the public face makes authorization determination.
func skillPublicCtx() context.Context {
	return common.SetPublicAPIToCtx(context.Background(), true)
}

// TestSkillIndexBuildAuthz Covers #345: Exposing type-level gatekeeping for face index build write interfaces.
func TestSkillIndexBuildAuthz(t *testing.T) {
	Convey("Skill 索引构建写接口授权", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		// expectDenied asserts that type-level skill:modify is not passed; taskRepo does not set any EXPECT.
		// Once the access control fails and reaches the storage layer, gomock will directly fail due to unexpected calls.
		expectDenied := func(authService *mocks.MockIAuthorizationService) {
			authService.EXPECT().GetAccessor(gomock.Any(), "user-1").Return(&interfaces.AuthAccessor{ID: "user-1"}, nil)
			authService.EXPECT().OperationCheckAll(gomock.Any(), gomock.Any(), interfaces.ResourceIDAll,
				interfaces.AuthResourceTypeSkill, interfaces.AuthOperationTypeModify).Return(false, nil)
		}

		Convey("CreateTask 无权限时拒绝且不落库", func() {
			authService := mocks.NewMockIAuthorizationService(ctrl)
			svc := &skillIndexBuildService{
				logger:      logger.DefaultLogger(),
				taskRepo:    mocks.NewMockISkillIndexBuildTaskDB(ctrl),
				authService: authService,
			}
			expectDenied(authService)
			resp, err := svc.CreateTask(skillPublicCtx(), &interfaces.CreateSkillIndexBuildTaskReq{
				UserID:      "user-1",
				ExecuteType: interfaces.SkillIndexBuildExecuteTypeFull,
			})
			So(resp, ShouldBeNil)
			So(err, ShouldNotBeNil)
		})

		Convey("CancelTask 无权限时拒绝且不读任务", func() {
			authService := mocks.NewMockIAuthorizationService(ctrl)
			svc := &skillIndexBuildService{
				logger:      logger.DefaultLogger(),
				taskRepo:    mocks.NewMockISkillIndexBuildTaskDB(ctrl),
				authService: authService,
			}
			expectDenied(authService)
			resp, err := svc.CancelTask(skillPublicCtx(), &interfaces.CancelSkillIndexBuildTaskReq{
				UserID: "user-1", TaskID: "task-1",
			})
			So(resp, ShouldBeNil)
			So(err, ShouldNotBeNil)
		})

		Convey("RetryTask 无权限时拒绝且不读任务", func() {
			authService := mocks.NewMockIAuthorizationService(ctrl)
			svc := &skillIndexBuildService{
				logger:      logger.DefaultLogger(),
				taskRepo:    mocks.NewMockISkillIndexBuildTaskDB(ctrl),
				authService: authService,
			}
			expectDenied(authService)
			resp, err := svc.RetryTask(skillPublicCtx(), &interfaces.RetrySkillIndexBuildTaskReq{
				UserID: "user-1", TaskID: "task-1",
			})
			So(resp, ShouldBeNil)
			So(err, ShouldNotBeNil)
		})

		Convey("有权限时放行到业务逻辑", func() {
			authService := mocks.NewMockIAuthorizationService(ctrl)
			taskRepo := mocks.NewMockISkillIndexBuildTaskDB(ctrl)
			svc := &skillIndexBuildService{
				logger:      logger.DefaultLogger(),
				taskRepo:    taskRepo,
				authService: authService,
			}
			authService.EXPECT().GetAccessor(gomock.Any(), "user-1").Return(&interfaces.AuthAccessor{ID: "user-1"}, nil)
			authService.EXPECT().OperationCheckAll(gomock.Any(), gomock.Any(), interfaces.ResourceIDAll,
				interfaces.AuthResourceTypeSkill, interfaces.AuthOperationTypeModify).Return(true, nil)
			taskRepo.EXPECT().SelectRunningTask(gomock.Any(), gomock.Nil()).Return(nil, nil)
			taskRepo.EXPECT().Insert(gomock.Any(), gomock.Nil(), gomock.Any()).Return(nil)

			resp, err := svc.CreateTask(skillPublicCtx(), &interfaces.CreateSkillIndexBuildTaskReq{
				UserID:      "user-1",
				ExecuteType: interfaces.SkillIndexBuildExecuteTypeFull,
			})
			So(err, ShouldBeNil)
			So(resp.Status, ShouldEqual, interfaces.SkillIndexBuildStatusPending)
		})

		Convey("内部面不判定（调度器与服务间调用）", func() {
			// authService does not set EXPECT: if a judgment is initiated internally, gomock will fail due to unexpected calls.
			taskRepo := mocks.NewMockISkillIndexBuildTaskDB(ctrl)
			svc := &skillIndexBuildService{
				logger:      logger.DefaultLogger(),
				taskRepo:    taskRepo,
				authService: mocks.NewMockIAuthorizationService(ctrl),
			}
			taskRepo.EXPECT().SelectRunningTask(gomock.Any(), gomock.Nil()).Return(nil, nil)
			taskRepo.EXPECT().Insert(gomock.Any(), gomock.Nil(), gomock.Any()).Return(nil)

			resp, err := svc.CreateTask(context.Background(), &interfaces.CreateSkillIndexBuildTaskReq{
				UserID:      "user-1",
				ExecuteType: interfaces.SkillIndexBuildExecuteTypeFull,
			})
			So(err, ShouldBeNil)
			So(resp, ShouldNotBeNil)
		})
	})
}

// TestGetSkillReleaseHistoryAuthz overrides #345: Release history read interface object-level access by skill_id.
func TestGetSkillReleaseHistoryAuthz(t *testing.T) {
	Convey("Skill 发布历史授权", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		Convey("无权限时拒绝且不读历史", func() {
			authService := mocks.NewMockIAuthorizationService(ctrl)
			reader := &skillReader{
				releaseHistoryRepo: mocks.NewMockISkillReleaseHistoryDB(ctrl),
				AuthService:        authService,
				Logger:             logger.DefaultLogger(),
			}
			authService.EXPECT().GetAccessor(gomock.Any(), "user-1").Return(&interfaces.AuthAccessor{ID: "user-1"}, nil)
			authService.EXPECT().OperationCheckAny(gomock.Any(), gomock.Any(), "skill-1", interfaces.AuthResourceTypeSkill,
				interfaces.AuthOperationTypeExecute, interfaces.AuthOperationTypePublicAccess,
				interfaces.AuthOperationTypeView).Return(false, nil)

			resp, err := reader.GetSkillReleaseHistory(skillPublicCtx(), &interfaces.GetSkillReleaseHistoryReq{
				UserID: "user-1", SkillID: "skill-1",
			})
			So(resp, ShouldBeNil)
			So(err, ShouldNotBeNil)
		})

		Convey("有权限时返回历史", func() {
			authService := mocks.NewMockIAuthorizationService(ctrl)
			historyRepo := mocks.NewMockISkillReleaseHistoryDB(ctrl)
			reader := &skillReader{
				releaseHistoryRepo: historyRepo,
				AuthService:        authService,
				Logger:             logger.DefaultLogger(),
			}
			authService.EXPECT().GetAccessor(gomock.Any(), "user-1").Return(&interfaces.AuthAccessor{ID: "user-1"}, nil)
			authService.EXPECT().OperationCheckAny(gomock.Any(), gomock.Any(), "skill-1", interfaces.AuthResourceTypeSkill,
				gomock.Any(), gomock.Any(), gomock.Any()).Return(true, nil)
			historyRepo.EXPECT().SelectBySkillID(gomock.Any(), gomock.Nil(), "skill-1").
				Return([]*model.SkillReleaseHistoryDB{{SkillID: "skill-1", Version: "v1"}}, nil)

			resp, err := reader.GetSkillReleaseHistory(skillPublicCtx(), &interfaces.GetSkillReleaseHistoryReq{
				UserID: "user-1", SkillID: "skill-1",
			})
			So(err, ShouldBeNil)
			So(len(resp), ShouldEqual, 1)
		})

		Convey("内部面不判定", func() {
			historyRepo := mocks.NewMockISkillReleaseHistoryDB(ctrl)
			reader := &skillReader{
				releaseHistoryRepo: historyRepo,
				AuthService:        mocks.NewMockIAuthorizationService(ctrl),
				Logger:             logger.DefaultLogger(),
			}
			historyRepo.EXPECT().SelectBySkillID(gomock.Any(), gomock.Nil(), "skill-1").
				Return([]*model.SkillReleaseHistoryDB{}, nil)

			resp, err := reader.GetSkillReleaseHistory(context.Background(), &interfaces.GetSkillReleaseHistoryReq{
				UserID: "user-1", SkillID: "skill-1",
			})
			So(err, ShouldBeNil)
			So(resp, ShouldBeEmpty)
		})
	})
}

// TestGetSkillNamesByIDsAuthz covers #345: batch name filtering based on viewing permissions to avoid enumerating all skill names.
func TestGetSkillNamesByIDsAuthz(t *testing.T) {
	Convey("Skill 批量取名授权过滤", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		Convey("只返回有查看权限的技能", func() {
			authService := mocks.NewMockIAuthorizationService(ctrl)
			skillRepo := mocks.NewMockISkillRepository(ctrl)
			registry := &skillRegistry{
				skillRepo:   skillRepo,
				AuthService: authService,
				Logger:      logger.DefaultLogger(),
			}
			authService.EXPECT().GetAccessor(gomock.Any(), "").Return(&interfaces.AuthAccessor{ID: "user-1"}, nil)
			authService.EXPECT().ResourceListIDs(gomock.Any(), gomock.Any(), interfaces.AuthResourceTypeSkill,
				interfaces.AuthOperationTypeView).Return([]string{"skill-1"}, nil)
			skillRepo.EXPECT().SelectSkillListByIDs(gomock.Any(), []string{"skill-1"}).
				Return([]*model.SkillRepositoryDB{{SkillID: "skill-1", Name: "skill one"}}, nil)

			resp, err := registry.GetSkillNamesByIDs(skillPublicCtx(), []string{"skill-1", "skill-2"})
			So(err, ShouldBeNil)
			So(len(resp.Entries), ShouldEqual, 1)
			So(resp.Entries[0].ID, ShouldEqual, "skill-1")
		})

		Convey("权限集为空时返回空列表且不查库", func() {
			authService := mocks.NewMockIAuthorizationService(ctrl)
			registry := &skillRegistry{
				skillRepo:   mocks.NewMockISkillRepository(ctrl),
				AuthService: authService,
				Logger:      logger.DefaultLogger(),
			}
			authService.EXPECT().GetAccessor(gomock.Any(), "").Return(&interfaces.AuthAccessor{ID: "user-1"}, nil)
			authService.EXPECT().ResourceListIDs(gomock.Any(), gomock.Any(), interfaces.AuthResourceTypeSkill,
				interfaces.AuthOperationTypeView).Return(nil, nil)

			resp, err := registry.GetSkillNamesByIDs(skillPublicCtx(), []string{"skill-1"})
			So(err, ShouldBeNil)
			So(resp.Entries, ShouldBeEmpty)
		})
	})
}
