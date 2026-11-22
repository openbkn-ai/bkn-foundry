package skill

import (
	"context"
	"database/sql"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
)

// publishedRelease is a release table holding release for its Skill and nothing for any other.
func publishedRelease(release *model.SkillReleaseDB) *stubSkillReleaseRepo {
	return &stubSkillReleaseRepo{
		selectBySkillID: func(_ context.Context, _ *sql.Tx, skillID string) (*model.SkillReleaseDB, error) {
			if release != nil && skillID == release.SkillID {
				return release, nil
			}
			return nil, nil
		},
	}
}

func TestExecuteSkillRunsThePublishedReleaseNotTheDraft(t *testing.T) {
	Convey("a Skill edited after publishing executes and downloads its published release", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		// No expectation on the Skill row: it holds the draft, and reading it fails the test.
		mockSkillRepo := mocks.NewMockISkillRepository(ctrl)
		mockFileRepo := mocks.NewMockISkillFileIndex(ctrl)
		mockAssetStore := mocks.NewMockskillAssetStore(ctrl)
		mockAuthService := mocks.NewMockIAuthorizationService(ctrl)
		mockSandbox := mocks.NewMockSandBoxControlPlane(ctrl)
		registry := &skillRegistry{
			skillRepo:  mockSkillRepo,
			fileRepo:   mockFileRepo,
			assetStore: mockAssetStore,
			releaseRepo: publishedRelease(&model.SkillReleaseDB{
				SkillID: "skill-pub", Name: "pub-skill", Version: "v-published",
				Status: interfaces.BizStatusPublished.String(),
			}),
			sandboxClient: mockSandbox,
			sessionPool:   &fakeSessionPool{acquireFunc: func(context.Context) (string, error) { return "sess_aoi_0", nil }},
			AuthService:   mockAuthService,
			Logger:        logger.DefaultLogger(),
		}

		mockAuthService.EXPECT().GetAccessor(gomock.Any(), "user-1").Return(&interfaces.AuthAccessor{ID: "user-1"}, nil).AnyTimes()
		mockAuthService.EXPECT().CheckExecutePermission(gomock.Any(), gomock.Any(), "skill-pub", interfaces.AuthResourceTypeSkill).Return(nil)
		mockAuthService.EXPECT().OperationCheckAny(gomock.Any(), gomock.Any(), "skill-pub", interfaces.AuthResourceTypeSkill,
			interfaces.AuthOperationTypeView, interfaces.AuthOperationTypePublicAccess).Return(true, nil)
		mockFileRepo.EXPECT().SelectSkillFileBySkillID(gomock.Any(), gomock.Nil(), "skill-pub", "v-published").Return([]*model.SkillFileIndexDB{
			{SkillID: "skill-pub", SkillVersion: "v-published", RelPath: SkillMD, StorageKey: "pub-md"},
			{SkillID: "skill-pub", SkillVersion: "v-published", RelPath: "scripts/run.py", StorageKey: "pub-run"},
		}, nil).Times(2)
		mockAssetStore.EXPECT().Download(gomock.Any(), &interfaces.OssObject{StorageKey: "pub-md"}).Return([]byte(validSkillMarkdown()), nil).Times(2)
		mockAssetStore.EXPECT().Download(gomock.Any(), &interfaces.OssObject{StorageKey: "pub-run"}).Return([]byte("print('published')"), nil).Times(2)
		mockSandbox.EXPECT().UploadSkillArchive(gomock.Any(), "sess_aoi_0", gomock.Any()).DoAndReturn(
			func(_ context.Context, _ string, req *interfaces.UploadSkillArchiveReq) (*interfaces.UploadSkillArchiveResp, error) {
				So(req.FileName, ShouldEqual, "pub-skill.zip")
				return &interfaces.UploadSkillArchiveResp{WorkDir: req.WorkDir, ExtractedFileCount: 2}, nil
			})
		mockSandbox.EXPECT().ExecuteShell(gomock.Any(), "sess_aoi_0", gomock.Any()).Return(&interfaces.ExecuteShellResp{Stdout: "published"}, nil)

		resp, err := registry.ExecuteSkill(context.Background(), &interfaces.ExecuteSkillReq{
			UserID: "user-1", SkillID: "skill-pub", EntryPoint: "scripts/run.py",
		})
		So(err, ShouldBeNil)
		So(resp.SkillID, ShouldEqual, "skill-pub")
		So(resp.Stdout, ShouldEqual, "published")

		download, err := registry.DownloadSkill(context.Background(), &interfaces.DownloadSkillReq{UserID: "user-1", SkillID: "skill-pub"})
		So(err, ShouldBeNil)
		So(download.FileName, ShouldEqual, "pub-skill.zip")
	})
}

func TestExecuteSkillRefusesASkillWithNoRelease(t *testing.T) {
	Convey("a Skill that is not published (never, or taken offline) is not executed", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockAuthService := mocks.NewMockIAuthorizationService(ctrl)
		acquired := false
		registry := &skillRegistry{
			skillRepo:   mocks.NewMockISkillRepository(ctrl),
			fileRepo:    mocks.NewMockISkillFileIndex(ctrl),
			releaseRepo: publishedRelease(nil),
			sessionPool: &fakeSessionPool{acquireFunc: func(context.Context) (string, error) {
				acquired = true
				return "sess", nil
			}},
			AuthService: mockAuthService,
			Logger:      logger.DefaultLogger(),
		}
		mockAuthService.EXPECT().GetAccessor(gomock.Any(), "user-1").Return(&interfaces.AuthAccessor{ID: "user-1"}, nil)
		mockAuthService.EXPECT().CheckExecutePermission(gomock.Any(), gomock.Any(), "skill-draft", interfaces.AuthResourceTypeSkill).Return(nil)

		resp, err := registry.ExecuteSkill(context.Background(), &interfaces.ExecuteSkillReq{
			UserID: "user-1", SkillID: "skill-draft", EntryPoint: "scripts/run.py",
		})

		So(resp, ShouldBeNil)
		httpErr, ok := err.(*httperrors.HTTPError)
		So(ok, ShouldBeTrue)
		So(httpErr.HTTPCode, ShouldEqual, http.StatusNotFound)
		So(acquired, ShouldBeFalse)
	})
}
