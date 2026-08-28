package skill

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/dbaccess"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/telemetry"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/auth"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/utils"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
)

type skillManagementReader struct {
	skillRepo   model.ISkillRepository
	fileRepo    model.ISkillFileIndex
	assetStore  skillAssetStore
	AuthService interfaces.IAuthorizationService
	Logger      interfaces.Logger
}

var (
	mgmtReaderOnce sync.Once
	mgmtReaderInst interfaces.SkillManagementReader
)

// NewSkillManagementReader creates a management skill reading service.
func NewSkillManagementReader() interfaces.SkillManagementReader {
	mgmtReaderOnce.Do(func() {
		conf := config.NewConfigLoader()
		mgmtReaderInst = &skillManagementReader{
			skillRepo:   dbaccess.NewSkillRepositoryDB(),
			fileRepo:    dbaccess.NewSkillFileIndexDB(),
			assetStore:  newOSSGatewaySkillAssetStore(),
			AuthService: auth.NewAuthServiceImpl(),
			Logger:      conf.GetLogger(),
		}
	})
	return mgmtReaderInst
}

// GetManagementContent Gets the management state SKILL.md content.
func (r *skillManagementReader) GetManagementContent(ctx context.Context, req *interfaces.GetManagementContentReq) (resp *interfaces.GetManagementContentResp, err error) {
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	telemetry.SetSpanAttributes(ctx, map[string]interface{}{
		"skill_id": req.SkillID,
	})

	skill, err := r.skillRepo.SelectSkillByID(ctx, nil, req.SkillID)
	if err != nil {
		return nil, err
	}
	if skill == nil || skill.IsDeleted {
		return nil, errors.DefaultHTTPError(ctx, http.StatusNotFound, fmt.Sprintf("skill not found: %s", req.SkillID))
	}

	if common.IsPublicAPIFromCtx(ctx) {
		accessor, err := r.AuthService.GetAccessor(ctx, req.UserID)
		if err != nil {
			return nil, err
		}
		authorized, err := r.AuthService.OperationCheckAny(ctx, accessor, req.SkillID, interfaces.AuthResourceTypeSkill,
			interfaces.AuthOperationTypeView, interfaces.AuthOperationTypeModify)
		if err != nil {
			return nil, err
		}
		if !authorized {
			r.Logger.WithContext(ctx).Errorf("user has no permission to view/modify skill %s", req.SkillID)
			return nil, errors.NewHTTPError(ctx, http.StatusForbidden, errors.ErrExtCommonOperationForbidden,
				fmt.Sprintf("user has no permission to view/modify skill %s", req.SkillID))
		}
	}

	resp = &interfaces.GetManagementContentResp{
		SkillID:     skill.SkillID,
		Name:        skill.Name,
		Description: skill.Description,
		Version:     skill.Version,
		Status:      interfaces.BizStatus(skill.Status),
		Source:      skill.Source,
		FileType:    detectSkillFileType(skill),
	}

	resp.Files = utils.JSONToObject[[]*interfaces.SkillFileSummary](skill.FileManifest)
	if resp.Files == nil {
		resp.Files = []*interfaces.SkillFileSummary{}
	}

	// Query the OSS records of SKILL.md for subsequent use.
	skillFile, err := r.fileRepo.SelectSkillFileByPath(ctx, nil, skill.SkillID, skill.Version, SkillMD)
	if err != nil {
		return nil, err
	}

	// Determine whether to return URL or text content based on response_mode:
	// url (default) — populate url, Content is empty.
	// content — populate Content, url is empty.
	switch req.ResponseMode {
	case "content":
		if skill.SkillContent != "" {
			resp.Content = skill.SkillContent
		} else if skillFile != nil {
			ossContent, err := r.assetStore.Download(ctx, &interfaces.OssObject{
				StorageID:  skillFile.StorageID,
				StorageKey: skillFile.StorageKey,
			})
			if err != nil {
				r.Logger.WithContext(ctx).Errorf("download SKILL.md from OSS failed: %v", err)
			} else {
				resp.Content = string(ossContent)
			}
		}
	default:
		// url pattern (with default empty value): URL is populated, Content remains zero.
		if skillFile != nil {
			downloadURL, err := r.assetStore.GetDownloadURL(ctx, &interfaces.OssObject{
				StorageID:  skillFile.StorageID,
				StorageKey: skillFile.StorageKey,
			})
			if err != nil {
				return nil, err
			}
			resp.URL = downloadURL
		}
	}

	return resp, nil
}

// ReadManagementFile reads the specified file in the management state.
func (r *skillManagementReader) ReadManagementFile(ctx context.Context, req *interfaces.ReadManagementFileReq) (resp *interfaces.ReadManagementFileResp, err error) {
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	telemetry.SetSpanAttributes(ctx, map[string]interface{}{
		"skill_id": req.SkillID,
		"rel_path": req.RelPath,
	})

	skill, err := r.skillRepo.SelectSkillByID(ctx, nil, req.SkillID)
	if err != nil {
		return nil, err
	}
	if skill == nil || skill.IsDeleted {
		return nil, errors.DefaultHTTPError(ctx, http.StatusNotFound, fmt.Sprintf("skill not found: %s", req.SkillID))
	}

	if common.IsPublicAPIFromCtx(ctx) {
		accessor, err := r.AuthService.GetAccessor(ctx, req.UserID)
		if err != nil {
			return nil, err
		}
		authorized, err := r.AuthService.OperationCheckAny(ctx, accessor, req.SkillID, interfaces.AuthResourceTypeSkill,
			interfaces.AuthOperationTypeView, interfaces.AuthOperationTypeModify)
		if err != nil {
			return nil, err
		}
		if !authorized {
			r.Logger.WithContext(ctx).Errorf("user has no permission to view/modify skill %s", req.SkillID)
			return nil, errors.NewHTTPError(ctx, http.StatusForbidden, errors.ErrExtCommonOperationForbidden,
				fmt.Sprintf("user has no permission to view/modify skill %s", req.SkillID))
		}
	}

	relPath, err := normalizeZipPath(req.RelPath)
	if err != nil {
		return nil, errors.DefaultHTTPError(ctx, http.StatusBadRequest, err.Error())
	}

	file, err := r.fileRepo.SelectSkillFileByPath(ctx, nil, req.SkillID, skill.Version, relPath)
	if err != nil {
		r.Logger.WithContext(ctx).Errorf("read management file failed: %v", err)
		return nil, err
	}
	if file == nil {
		r.Logger.WithContext(ctx).Warnf("management file not found: %s", relPath)
		return nil, errors.DefaultHTTPError(ctx, http.StatusNotFound, fmt.Sprintf("file not found: %s", relPath))
	}

	resp = &interfaces.ReadManagementFileResp{
		SkillID:  req.SkillID,
		RelPath:  relPath,
		MimeType: file.MimeType,
		FileType: file.FileType,
		Size:     file.Size,
	}

	switch req.ResponseMode {
	case "content":
		if relPath == SkillMD && skill.SkillContent != "" {
			resp.Content = skill.SkillContent
			break
		}
		ossContent, downloadErr := r.assetStore.Download(ctx, &interfaces.OssObject{
			StorageID:  file.StorageID,
			StorageKey: file.StorageKey,
		})
		if downloadErr != nil {
			r.Logger.WithContext(ctx).Errorf("download management file failed: %v", downloadErr)
			return nil, downloadErr
		}
		resp.Content = string(ossContent)
	default:
		downloadURL, urlErr := r.assetStore.GetDownloadURL(ctx, &interfaces.OssObject{
			StorageID:  file.StorageID,
			StorageKey: file.StorageKey,
		})
		if urlErr != nil {
			r.Logger.WithContext(ctx).Errorf("read management file failed: %v", urlErr)
			return nil, urlErr
		}
		resp.URL = downloadURL
	}

	return resp, nil
}

// DownloadManagementSkill Download the complete management skills package.
func (r *skillManagementReader) DownloadManagementSkill(ctx context.Context, req *interfaces.DownloadManagementSkillReq) (resp *interfaces.DownloadSkillResp, err error) {
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	telemetry.SetSpanAttributes(ctx, map[string]interface{}{
		"skill_id": req.SkillID,
	})

	skill, err := r.skillRepo.SelectSkillByID(ctx, nil, req.SkillID)
	if err != nil {
		return nil, err
	}
	if skill == nil || skill.IsDeleted {
		return nil, errors.DefaultHTTPError(ctx, http.StatusNotFound, fmt.Sprintf("skill not found: %s", req.SkillID))
	}

	if common.IsPublicAPIFromCtx(ctx) {
		accessor, err := r.AuthService.GetAccessor(ctx, req.UserID)
		if err != nil {
			return nil, err
		}
		authorized, err := r.AuthService.OperationCheckAny(ctx, accessor, req.SkillID, interfaces.AuthResourceTypeSkill,
			interfaces.AuthOperationTypeView, interfaces.AuthOperationTypeModify)
		if err != nil {
			return nil, err
		}
		if !authorized {
			r.Logger.WithContext(ctx).Errorf("user has no permission to view/modify skill %s", req.SkillID)
			return nil, errors.NewHTTPError(ctx, http.StatusForbidden, errors.ErrExtCommonOperationForbidden,
				fmt.Sprintf("user has no permission to view/modify skill %s", req.SkillID))
		}
	}

	files, err := r.fileRepo.SelectSkillFileBySkillID(ctx, nil, req.SkillID, skill.Version)
	if err != nil {
		r.Logger.WithContext(ctx).Errorf("select management files failed: %v", err)
		return nil, err
	}

	_, zipName, content, err := buildArchiveFromFiles(ctx, r.assetStore, skill, files)
	if err != nil {
		r.Logger.WithContext(ctx).Errorf("build management archive failed: %v", err)
		return nil, err
	}

	return &interfaces.DownloadSkillResp{
		SkillID:  req.SkillID,
		FileName: zipName,
		Content:  content,
	}, nil
}

// buildArchiveFromFiles Builds a ZIP archive from a list of files.
func buildArchiveFromFiles(ctx context.Context, store skillAssetStore, skill *model.SkillRepositoryDB,
	files []*model.SkillFileIndexDB) (*model.SkillRepositoryDB, string, []byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, file := range files {
		content, readErr := store.Download(ctx, &interfaces.OssObject{
			StorageID:  file.StorageID,
			StorageKey: file.StorageKey,
		})
		if readErr != nil {
			_ = zw.Close()
			return nil, "", nil, readErr
		}
		w, createErr := zw.Create(file.RelPath)
		if createErr != nil {
			_ = zw.Close()
			return nil, "", nil, createErr
		}
		if _, writeErr := io.Copy(w, bytes.NewReader(content)); writeErr != nil {
			_ = zw.Close()
			return nil, "", nil, writeErr
		}
	}
	if err := zw.Close(); err != nil {
		return nil, "", nil, err
	}
	return skill, fmt.Sprintf("%s.zip", skill.Name), buf.Bytes(), nil
}

// detectSkillFileType infers the registration type from the repository record.
// FR-5: The content registered manifest only has one record of SKILL.md, and the zip registered manifest has more files.
func detectSkillFileType(skill *model.SkillRepositoryDB) string {
	manifest := utils.JSONToObject[[]*interfaces.SkillFileSummary](skill.FileManifest)
	if len(manifest) == 0 {
		return "content"
	}
	if len(manifest) == 1 && manifest[0].RelPath == SkillMD && skill.SkillContent != "" {
		return "content"
	}
	return "zip"
}
