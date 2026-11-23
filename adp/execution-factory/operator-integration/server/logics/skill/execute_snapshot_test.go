package skill

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"io"
	"path"
	"sort"
	"strings"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/mocks"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"
)

// snapshotWorkspace stands in for the workspace of one pooled sandbox session, which outlives a
// single execution. Uploads extract the way the control plane does with overwrite=false: a file
// already at the destination path is skipped, whatever its content.
type snapshotWorkspace struct {
	files map[string]string
}

func (w *snapshotWorkspace) upload(req *interfaces.UploadSkillArchiveReq) (*interfaces.UploadSkillArchiveResp, error) {
	zr, err := zip.NewReader(bytes.NewReader(req.Content), int64(len(req.Content)))
	if err != nil {
		return nil, err
	}
	resp := &interfaces.UploadSkillArchiveResp{WorkDir: req.WorkDir, UploadedPath: req.WorkDir, FileName: req.FileName}
	for _, f := range zr.File {
		dest := path.Join(req.WorkDir, f.Name)
		if _, exists := w.files[dest]; exists {
			resp.SkippedFileCount++
			continue
		}
		rc, openErr := f.Open()
		if openErr != nil {
			return nil, openErr
		}
		body, readErr := io.ReadAll(rc)
		_ = rc.Close()
		if readErr != nil {
			return nil, readErr
		}
		w.files[dest] = string(body)
		resp.ExtractedFileCount++
	}
	return resp, nil
}

// run reports what `python scripts/run.py` would see in workDir: the entry, the helper it
// imports, and every other file present.
func (w *snapshotWorkspace) run(workDir string) string {
	present := []string{}
	for p := range w.files {
		if rel, ok := strings.CutPrefix(p, workDir+"/"); ok {
			present = append(present, rel)
		}
	}
	sort.Strings(present)
	return w.files[workDir+"/scripts/run.py"] + "|" + w.files[workDir+"/scripts/helper.py"] + "|" + strings.Join(present, ",")
}

type skillPackageVersion struct {
	version string
	files   map[string]string
}

func TestExecuteSkillRunsTheSelectedSnapshotInAReusedSession(t *testing.T) {
	Convey("executions of one Skill in one pooled session each run their own snapshot (#3311)", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockSkillRepo := mocks.NewMockISkillRepository(ctrl)
		mockFileRepo := mocks.NewMockISkillFileIndex(ctrl)
		mockAssetStore := mocks.NewMockskillAssetStore(ctrl)
		mockAuthService := mocks.NewMockIAuthorizationService(ctrl)
		mockSandbox := mocks.NewMockSandBoxControlPlane(ctrl)
		registry := &skillRegistry{
			skillRepo:     mockSkillRepo,
			fileRepo:      mockFileRepo,
			assetStore:    mockAssetStore,
			sandboxClient: mockSandbox,
			sessionPool: &fakeSessionPool{
				acquireFunc: func(context.Context) (string, error) { return "sess_aoi_1", nil },
			},
			AuthService: mockAuthService,
			Logger:      logger.DefaultLogger(),
		}

		var current skillPackageVersion
		registry.releaseRepo = &stubSkillReleaseRepo{
			selectBySkillID: func(context.Context, *sql.Tx, string) (*model.SkillReleaseDB, error) {
				return &model.SkillReleaseDB{
					SkillID: "skill-3311", Name: "repro", Version: current.version,
					Status: interfaces.BizStatusPublished.String(),
				}, nil
			},
		}
		mockAuthService.EXPECT().GetAccessor(gomock.Any(), "user-1").Return(&interfaces.AuthAccessor{ID: "user-1"}, nil).AnyTimes()
		mockAuthService.EXPECT().CheckExecutePermission(gomock.Any(), gomock.Any(), "skill-3311", interfaces.AuthResourceTypeSkill).
			Return(nil).AnyTimes()
		mockFileRepo.EXPECT().SelectSkillFileBySkillID(gomock.Any(), gomock.Nil(), "skill-3311", gomock.Any()).DoAndReturn(
			func(_ context.Context, _ *sql.Tx, _ string, version string) ([]*model.SkillFileIndexDB, error) {
				So(version, ShouldEqual, current.version)
				rels := make([]string, 0, len(current.files))
				for rel := range current.files {
					rels = append(rels, rel)
				}
				sort.Strings(rels)
				rows := make([]*model.SkillFileIndexDB, 0, len(rels))
				for _, rel := range rels {
					rows = append(rows, &model.SkillFileIndexDB{
						SkillID: "skill-3311", SkillVersion: version, RelPath: rel, StorageKey: version + "/" + rel,
					})
				}
				return rows, nil
			}).AnyTimes()
		mockAssetStore.EXPECT().Download(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, obj *interfaces.OssObject) ([]byte, error) {
				rel := strings.TrimPrefix(obj.StorageKey, current.version+"/")
				return []byte(current.files[rel]), nil
			}).AnyTimes()

		workspace := &snapshotWorkspace{files: map[string]string{}}
		mockSandbox.EXPECT().UploadSkillArchive(gomock.Any(), "sess_aoi_1", gomock.Any()).DoAndReturn(
			func(_ context.Context, _ string, req *interfaces.UploadSkillArchiveReq) (*interfaces.UploadSkillArchiveResp, error) {
				return workspace.upload(req)
			}).AnyTimes()
		mockSandbox.EXPECT().ExecuteShell(gomock.Any(), "sess_aoi_1", gomock.Any()).DoAndReturn(
			func(_ context.Context, _ string, req *interfaces.ExecuteShellReq) (*interfaces.ExecuteShellResp, error) {
				return &interfaces.ExecuteShellResp{WorkDir: req.WorkDir, Command: req.Command, Stdout: workspace.run(req.WorkDir)}, nil
			}).AnyTimes()

		execute := func(pkg skillPackageVersion) *interfaces.ExecuteSkillResp {
			current = pkg
			resp, err := registry.ExecuteSkill(context.Background(), &interfaces.ExecuteSkillReq{
				UserID: "user-1", SkillID: "skill-3311", EntryPoint: "scripts/run.py",
			})
			So(err, ShouldBeNil)
			return resp
		}

		v1Files := map[string]string{
			SkillMD:             validSkillMarkdown(),
			"scripts/run.py":    "entry v1",
			"scripts/helper.py": "helper v1",
			"obsolete.py":       "OLD = True",
		}
		v2Files := map[string]string{
			SkillMD:             validSkillMarkdown(),
			"scripts/run.py":    "entry v2",
			"scripts/helper.py": "helper v2",
			"new.py":            "NEW = True",
		}

		v1 := execute(skillPackageVersion{version: "1.0.0", files: v1Files})
		So(v1.Stdout, ShouldEqual, "entry v1|helper v1|SKILL.md,obsolete.py,scripts/helper.py,scripts/run.py")

		// The same entry path, published again with other content and obsolete.py deleted.
		v2 := execute(skillPackageVersion{version: "1.0.1", files: v2Files})
		So(v2.Stdout, ShouldEqual, "entry v2|helper v2|SKILL.md,new.py,scripts/helper.py,scripts/run.py")
		So(v2.WorkDir, ShouldNotEqual, v1.WorkDir)
		So(v2.WorkDir, ShouldStartWith, "skills/skill-3311/")

		// Rolling back to the V1 package reuses the V1 directory, whose files are the same bytes.
		rollback := execute(skillPackageVersion{version: "1.0.2", files: v1Files})
		So(rollback.WorkDir, ShouldEqual, v1.WorkDir)
		So(rollback.Stdout, ShouldEqual, v1.Stdout)

		// A version string whose files were replaced is a different snapshot.
		replaced := execute(skillPackageVersion{version: "1.0.2", files: map[string]string{
			SkillMD:             validSkillMarkdown(),
			"scripts/run.py":    "entry v3",
			"scripts/helper.py": "helper v1",
		}})
		So(replaced.WorkDir, ShouldNotEqual, rollback.WorkDir)
		So(replaced.Stdout, ShouldEqual, "entry v3|helper v1|SKILL.md,scripts/helper.py,scripts/run.py")
	})
}

func TestExecuteSkillRefusesAPartlyPlacedPackage(t *testing.T) {
	Convey("ExecuteSkill does not run a directory the sandbox did not place every file in", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockSkillRepo := mocks.NewMockISkillRepository(ctrl)
		mockFileRepo := mocks.NewMockISkillFileIndex(ctrl)
		mockAssetStore := mocks.NewMockskillAssetStore(ctrl)
		mockAuthService := mocks.NewMockIAuthorizationService(ctrl)
		mockSandbox := mocks.NewMockSandBoxControlPlane(ctrl)
		released := false
		registry := &skillRegistry{
			skillRepo:     mockSkillRepo,
			fileRepo:      mockFileRepo,
			assetStore:    mockAssetStore,
			sandboxClient: mockSandbox,
			sessionPool: &fakeSessionPool{
				acquireFunc: func(context.Context) (string, error) { return "sess_aoi_2", nil },
				releaseFunc: func(string) { released = true },
			},
			AuthService: mockAuthService,
			Logger:      logger.DefaultLogger(),
		}

		mockAuthService.EXPECT().GetAccessor(gomock.Any(), "user-1").Return(&interfaces.AuthAccessor{ID: "user-1"}, nil)
		mockAuthService.EXPECT().CheckExecutePermission(gomock.Any(), gomock.Any(), "skill-partial", interfaces.AuthResourceTypeSkill).Return(nil)
		registry.releaseRepo = publishedRelease(&model.SkillReleaseDB{
			SkillID: "skill-partial", Name: "partial", Version: "v1", Status: interfaces.BizStatusPublished.String(),
		})
		mockFileRepo.EXPECT().SelectSkillFileBySkillID(gomock.Any(), gomock.Nil(), "skill-partial", "v1").Return([]*model.SkillFileIndexDB{
			{SkillID: "skill-partial", RelPath: SkillMD, StorageKey: "md"},
			{SkillID: "skill-partial", RelPath: "scripts/run.py", StorageKey: "run"},
			{SkillID: "skill-partial", RelPath: "scripts/helper.py", StorageKey: "helper"},
		}, nil)
		mockAssetStore.EXPECT().Download(gomock.Any(), gomock.Any()).Return([]byte("content"), nil).Times(3)
		mockSandbox.EXPECT().UploadSkillArchive(gomock.Any(), "sess_aoi_2", gomock.Any()).DoAndReturn(
			func(_ context.Context, _ string, req *interfaces.UploadSkillArchiveReq) (*interfaces.UploadSkillArchiveResp, error) {
				return &interfaces.UploadSkillArchiveResp{WorkDir: req.WorkDir, ExtractedFileCount: 1, SkippedFileCount: 1}, nil
			})
		mockSandbox.EXPECT().ExecuteShell(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)

		resp, err := registry.ExecuteSkill(context.Background(), &interfaces.ExecuteSkillReq{
			UserID: "user-1", SkillID: "skill-partial", EntryPoint: "scripts/run.py",
		})

		So(resp, ShouldBeNil)
		So(err, ShouldNotBeNil)
		So(released, ShouldBeTrue)
	})
}
