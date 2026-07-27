package skill

import (
	"context"
	"testing"

	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/adp/execution-factory/operator-integration/server/mocks"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
)

// skillPublicCtx 构造公开面上下文，公开面才做授权判定
func skillPublicCtx() context.Context {
	return common.SetPublicAPIToCtx(context.Background(), true)
}

// TestSkillIndexBuildAuthz 覆盖 #345：公开面索引构建写接口的类型级门禁
func TestSkillIndexBuildAuthz(t *testing.T) {
	Convey("Skill 索引构建写接口授权", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		// expectDenied 断言类型级 skill:modify 判定为不通过；taskRepo 不设任何 EXPECT，
		// 一旦门禁失效走到仓储层，gomock 会因非预期调用直接失败。
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
				BusinessDomainID: "bd-1", UserID: "user-1",
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
				BusinessDomainID: "bd-1", UserID: "user-1", TaskID: "task-1",
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
				BusinessDomainID: "bd-1", UserID: "user-1", TaskID: "task-1",
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
				BusinessDomainID: "bd-1", UserID: "user-1",
				ExecuteType: interfaces.SkillIndexBuildExecuteTypeFull,
			})
			So(err, ShouldBeNil)
			So(resp.Status, ShouldEqual, interfaces.SkillIndexBuildStatusPending)
		})

		Convey("内部面不判定（调度器与服务间调用）", func() {
			// authService 不设 EXPECT：内部面若发起判定，gomock 会因非预期调用失败
			taskRepo := mocks.NewMockISkillIndexBuildTaskDB(ctrl)
			svc := &skillIndexBuildService{
				logger:      logger.DefaultLogger(),
				taskRepo:    taskRepo,
				authService: mocks.NewMockIAuthorizationService(ctrl),
			}
			taskRepo.EXPECT().SelectRunningTask(gomock.Any(), gomock.Nil()).Return(nil, nil)
			taskRepo.EXPECT().Insert(gomock.Any(), gomock.Nil(), gomock.Any()).Return(nil)

			resp, err := svc.CreateTask(context.Background(), &interfaces.CreateSkillIndexBuildTaskReq{
				BusinessDomainID: "bd-1", UserID: "user-1",
				ExecuteType: interfaces.SkillIndexBuildExecuteTypeFull,
			})
			So(err, ShouldBeNil)
			So(resp, ShouldNotBeNil)
		})
	})
}

// TestGetSkillReleaseHistoryAuthz 覆盖 #345：发布历史读接口按 skill_id 的对象级门禁
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
				BusinessDomainID: "bd-1", UserID: "user-1", SkillID: "skill-1",
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
				BusinessDomainID: "bd-1", UserID: "user-1", SkillID: "skill-1",
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
				BusinessDomainID: "bd-1", UserID: "user-1", SkillID: "skill-1",
			})
			So(err, ShouldBeNil)
			So(resp, ShouldBeEmpty)
		})
	})
}

// TestGetSkillNamesByIDsAuthz 覆盖 #345：批量取名按查看权限过滤，避免枚举全量技能名
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
