package skill

import (
	"context"
	"database/sql"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
)

// skillInternalCtx 构造内部面上下文：带账户身份，但不是公开接口。
// context-loader 把技能读接口包成 MCP 工具后走的就是这条路。
func skillInternalCtx() context.Context {
	return common.SetAccountAuthContextToCtx(context.Background(), &interfaces.AccountAuthContext{
		AccountID:   "user-1",
		AccountType: interfaces.AccessorTypeUser,
	})
}

// TestSkillInternalReadAuthzModes 盯住内部技能读接口的三档授权。
//
// 内部面历史上一律放行（授权押给调用方）。MCP 工具接上来之后 skill_id 由调用方自填，
// 那个前提不再成立，于是加了这道判定——但直接强制会打断存量调用方，所以默认档是
// shadow：查了、记了、不拦。这个测试锁的就是「默认不拦、开了才拦」。
func TestSkillInternalReadAuthzModes(t *testing.T) {
	Convey("内部面技能读接口的授权分档", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		newReader := func(authService interfaces.IAuthorizationService, fileRepo model.ISkillFileIndex, assetStore skillAssetStore) *skillReader {
			return &skillReader{
				releaseRepo: &stubSkillReleaseRepo{
					selectBySkillID: func(_ context.Context, _ *sql.Tx, _ string) (*model.SkillReleaseDB, error) {
						return &model.SkillReleaseDB{
							SkillID: "skill-1", Version: "v1",
							Status: interfaces.BizStatusPublished.String(),
						}, nil
					},
				},
				fileRepo:    fileRepo,
				assetStore:  assetStore,
				AuthService: authService,
				Logger:      logger.DefaultLogger(),
			}
		}

		expectDenied := func(authService *mocks.MockIAuthorizationService) {
			authService.EXPECT().GetAccessor(gomock.Any(), gomock.Any()).Return(&interfaces.AuthAccessor{ID: "user-1"}, nil)
			authService.EXPECT().OperationCheckAny(gomock.Any(), gomock.Any(), "skill-1", interfaces.AuthResourceTypeSkill,
				interfaces.AuthOperationTypeExecute, interfaces.AuthOperationTypePublicAccess, interfaces.AuthOperationTypeView).Return(false, nil)
		}

		Convey("默认档 shadow：判定不通过也放行，只记日志", func() {
			t.Setenv(common.SkillReadAuthzModeEnv, "")
			authService := mocks.NewMockIAuthorizationService(ctrl)
			fileRepo := mocks.NewMockISkillFileIndex(ctrl)
			assetStore := mocks.NewMockskillAssetStore(ctrl)
			expectDenied(authService)
			fileRepo.EXPECT().SelectSkillFileByPath(gomock.Any(), gomock.Nil(), "skill-1", "v1", "refs/guide.md").
				Return(&model.SkillFileIndexDB{SkillID: "skill-1", SkillVersion: "v1", RelPath: "refs/guide.md"}, nil)
			assetStore.EXPECT().GetDownloadURL(gomock.Any(), gomock.Any()).Return("https://download/guide.md", nil)

			resp, err := newReader(authService, fileRepo, assetStore).ReadSkillFile(skillInternalCtx(),
				&interfaces.ReadSkillFileReq{SkillID: "skill-1", RelPath: "refs/guide.md", UserID: "user-1"})

			So(err, ShouldBeNil)
			So(resp.URL, ShouldEqual, "https://download/guide.md")
		})

		Convey("enforce 档：判定不通过直接 403，不碰文件", func() {
			t.Setenv(common.SkillReadAuthzModeEnv, "enforce")
			authService := mocks.NewMockIAuthorizationService(ctrl)
			// fileRepo / assetStore 不设 EXPECT：门禁若失效走到读文件，gomock 会因非预期调用失败。
			expectDenied(authService)

			resp, err := newReader(authService, mocks.NewMockISkillFileIndex(ctrl), mocks.NewMockskillAssetStore(ctrl)).
				ReadSkillFile(skillInternalCtx(),
					&interfaces.ReadSkillFileReq{SkillID: "skill-1", RelPath: "refs/guide.md", UserID: "user-1"})

			So(resp, ShouldBeNil)
			So(err, ShouldNotBeNil)
		})

		Convey("off 档：完全不查授权", func() {
			t.Setenv(common.SkillReadAuthzModeEnv, "off")
			// authService 不设 EXPECT：off 档下一次都不该调。
			authService := mocks.NewMockIAuthorizationService(ctrl)
			fileRepo := mocks.NewMockISkillFileIndex(ctrl)
			assetStore := mocks.NewMockskillAssetStore(ctrl)
			fileRepo.EXPECT().SelectSkillFileByPath(gomock.Any(), gomock.Nil(), "skill-1", "v1", "refs/guide.md").
				Return(&model.SkillFileIndexDB{SkillID: "skill-1", SkillVersion: "v1", RelPath: "refs/guide.md"}, nil)
			assetStore.EXPECT().GetDownloadURL(gomock.Any(), gomock.Any()).Return("https://download/guide.md", nil)

			resp, err := newReader(authService, fileRepo, assetStore).ReadSkillFile(skillInternalCtx(),
				&interfaces.ReadSkillFileReq{SkillID: "skill-1", RelPath: "refs/guide.md", UserID: "user-1"})

			So(err, ShouldBeNil)
			So(resp.URL, ShouldEqual, "https://download/guide.md")
		})

		Convey("无账户身份的内部调用：跳过判定，不误伤存量调用方", func() {
			t.Setenv(common.SkillReadAuthzModeEnv, "enforce")
			authService := mocks.NewMockIAuthorizationService(ctrl)
			fileRepo := mocks.NewMockISkillFileIndex(ctrl)
			assetStore := mocks.NewMockskillAssetStore(ctrl)
			fileRepo.EXPECT().SelectSkillFileByPath(gomock.Any(), gomock.Nil(), "skill-1", "v1", "refs/guide.md").
				Return(&model.SkillFileIndexDB{SkillID: "skill-1", SkillVersion: "v1", RelPath: "refs/guide.md"}, nil)
			assetStore.EXPECT().GetDownloadURL(gomock.Any(), gomock.Any()).Return("https://download/guide.md", nil)

			resp, err := newReader(authService, fileRepo, assetStore).ReadSkillFile(context.Background(),
				&interfaces.ReadSkillFileReq{SkillID: "skill-1", RelPath: "refs/guide.md"})

			So(err, ShouldBeNil)
			So(resp.URL, ShouldEqual, "https://download/guide.md")
		})
	})
}
