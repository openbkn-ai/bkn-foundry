package skill

import (
	"context"
	"database/sql"
	"net/http"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
)

// skillInternalCtx constructs the internal context: with account identity, but not a public interface.
// This is the path context-loader took after wrapping the skill reading interface into an MCP tool.
func skillInternalCtx() context.Context {
	return common.SetAccountAuthContextToCtx(context.Background(), &interfaces.AccountAuthContext{
		AccountID:   "user-1",
		AccountType: interfaces.AccessorTypeUser,
	})
}

// TestSkillInternalReadAuthzModes focuses on the third-level authorization of the internal skill reading interface.
//
// Internal aspects are always released (authorization is given to the caller). After the MCP tool is connected, the skill_id is filled in by the caller.
// That premise is no longer true, so this judgment is added - but direct force will interrupt the stock caller, so the default file is.
// shadow: checked, remembered, did not stop. This test lock is "not blocked by default, blocked only after it is opened.".
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
			// fileRepo / assetStore does not set EXPECT: if the access control fails and the file is read, gomock will fail due to unexpected calls.
			expectDenied(authService)

			resp, err := newReader(authService, mocks.NewMockISkillFileIndex(ctrl), mocks.NewMockskillAssetStore(ctrl)).
				ReadSkillFile(skillInternalCtx(),
					&interfaces.ReadSkillFileReq{SkillID: "skill-1", RelPath: "refs/guide.md", UserID: "user-1"})

			So(resp, ShouldBeNil)
			So(err, ShouldNotBeNil)
		})

		Convey("off 档：完全不查授权", func() {
			t.Setenv(common.SkillReadAuthzModeEnv, "off")
			// authService does not set the EXPECT:off file and should not call it next time.
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

		Convey("越出技能包的路径回 400，不是 500", func() {
			// The naked error will be converted into 500 by the rest layer, and the caller (especially the model) will treat it as a service failure and try again.
			// This was originally due to its own transmission of the wrong path. VM actual measurement found that the management state is always 400.
			t.Setenv(common.SkillReadAuthzModeEnv, "off")
			reader := newReader(mocks.NewMockIAuthorizationService(ctrl),
				mocks.NewMockISkillFileIndex(ctrl), mocks.NewMockskillAssetStore(ctrl))

			resp, err := reader.ReadSkillFile(skillInternalCtx(),
				&interfaces.ReadSkillFileReq{SkillID: "skill-1", RelPath: "../../etc/passwd"})

			So(resp, ShouldBeNil)
			So(err, ShouldNotBeNil)
			httpErr, ok := err.(*errors.HTTPError)
			So(ok, ShouldBeTrue)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
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
