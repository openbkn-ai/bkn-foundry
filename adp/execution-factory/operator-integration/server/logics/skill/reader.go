package skill

import (
	"context"
	"fmt"
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

type skillReader struct {
	skillRepo          model.ISkillRepository
	releaseRepo        model.ISkillReleaseDB
	releaseHistoryRepo model.ISkillReleaseHistoryDB
	fileRepo           model.ISkillFileIndex
	assetStore         skillAssetStore
	AuthService        interfaces.IAuthorizationService
	Logger             interfaces.Logger
}

var (
	readerOnce sync.Once
	readerInst interfaces.SkillReader
)

// NewSkillReader creates a skill reading service object.
func NewSkillReader() interfaces.SkillReader {
	readerOnce.Do(func() {
		conf := config.NewConfigLoader()
		readerInst = &skillReader{
			skillRepo:          dbaccess.NewSkillRepositoryDB(),
			releaseRepo:        dbaccess.NewSkillReleaseDB(),
			releaseHistoryRepo: dbaccess.NewSkillReleaseHistoryDB(),
			fileRepo:           dbaccess.NewSkillFileIndexDB(),
			assetStore:         newOSSGatewaySkillAssetStore(),
			AuthService:        auth.NewAuthServiceImpl(),
			Logger:             conf.GetLogger(),
		}
	})
	return readerInst
}

// authorizeSkillRead verifies whether the caller has read permission (one of execution / public access / view) for the skill.
//
// Public interfaces are always mandatory. Internal interface (internal-v1) see SKILL_INTERNAL_READ_AUTHZ:
// off will be allowed directly; shadow (default) will check but not block, and will only log if it fails; enforce will return 403 consistent with the public interface.
// The purpose of binning is to not interrupt the existing internal callers - after context-loader packages these two interfaces into MCP tools,
// If the internal path is unconditionally allowed, it means that any account can read the full text of any skill, but direct browsing will cause accidental damage, so shadow observation first.
//
// If the internal interface cannot obtain the account identity (accessor resolution fails), the shadow file will also only record the log:
// That is "unable to judge", not "judged to have no right". Stopping him at this time is purely accidental.
func (r *skillReader) authorizeSkillRead(ctx context.Context, userID, skillID string) error {
	isPublic := common.IsPublicAPIFromCtx(ctx)
	mode := common.GetSkillReadAuthzMode()
	if !isPublic {
		if mode == common.SkillReadAuthzOff {
			return nil
		}
		// If there is no account identity on the internal path, there is no judgment: the caller has not even said who he is, and checking the authorization at this time will only.
		// Misjudge "no way to judge" as "no right". The public path does not go here - the identity of that path is guaranteed by the token.
		if authContext, ok := common.GetAccountAuthContextFromCtx(ctx); (!ok || authContext.AccountID == "") && userID == "" {
			return nil
		}
	}

	shadow := !isPublic && mode == common.SkillReadAuthzShadow
	accessor, err := r.AuthService.GetAccessor(ctx, userID)
	if err != nil {
		if shadow {
			r.Logger.WithContext(ctx).Warnf("[skill.read.authz.shadow] resolve accessor failed, skill %s, user %s: %v", skillID, userID, err)
			return nil
		}
		return err
	}
	authorized, err := r.AuthService.OperationCheckAny(ctx, accessor, skillID, interfaces.AuthResourceTypeSkill,
		interfaces.AuthOperationTypeExecute, interfaces.AuthOperationTypePublicAccess, interfaces.AuthOperationTypeView)
	if err != nil {
		if shadow {
			r.Logger.WithContext(ctx).Warnf("[skill.read.authz.shadow] check failed, skill %s, user %s: %v", skillID, userID, err)
			return nil
		}
		return err
	}
	if authorized {
		return nil
	}
	if shadow {
		r.Logger.WithContext(ctx).Warnf("[skill.read.authz.shadow] user %s would be denied on skill %s (mode=shadow, request allowed)", userID, skillID)
		return nil
	}
	r.Logger.WithContext(ctx).Errorf("user %s has no permission to execute、view、public access skill %s", userID, skillID)
	return errors.NewHTTPError(ctx, http.StatusForbidden, errors.ErrExtCommonOperationForbidden,
		fmt.Sprintf("user has no permission to execute、view、public access skill %s", skillID))
}

// GetSkillContent Gets skill content.
func (r *skillReader) GetSkillContent(ctx context.Context, req *interfaces.GetSkillContentReq) (resp *interfaces.GetSkillContentResp, err error) {
	// record observable.
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	telemetry.SetSpanAttributes(ctx, map[string]interface{}{
		"skill_id": req.SkillID,
	})

	skill, err := r.getPublishedSkill(ctx, req.SkillID)
	if err != nil {
		return
	}
	if err = r.authorizeSkillRead(ctx, req.UserID, req.SkillID); err != nil {
		return nil, err
	}
	// Query the corresponding "SKILL.md file.
	skillFile, err := r.fileRepo.SelectSkillFileByPath(ctx, nil, skill.SkillID, skill.Version, SkillMD)
	if err != nil {
		r.Logger.WithContext(ctx).Errorf("select skill file failed: %v", err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return nil, err
	}
	if skillFile == nil {
		err = errors.DefaultHTTPError(ctx, http.StatusNotFound, fmt.Sprintf("skill file not found: %s", SkillMD))
		return nil, err
	}
	contentObject := &interfaces.OssObject{
		StorageID:  skillFile.StorageID,
		StorageKey: skillFile.StorageKey,
	}
	downloadURL, err := r.assetStore.GetDownloadURL(ctx, contentObject)
	if err != nil {
		return nil, err
	}
	// TODO: Audit log to be accessed.
	return &interfaces.GetSkillContentResp{
		SkillID: skill.SkillID,
		URL:     downloadURL,
		Files:   utils.JSONToObject[[]*interfaces.SkillFileSummary](skill.FileManifest),
		Status:  interfaces.BizStatus(skill.Status),
	}, nil
}

// ReadSkillFile reads the content of the skill file.
func (r *skillReader) ReadSkillFile(ctx context.Context, req *interfaces.ReadSkillFileReq) (resp *interfaces.ReadSkillFileResp, err error) {
	// record observable.
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	telemetry.SetSpanAttributes(ctx, map[string]interface{}{
		"user_id":  req.UserID,
		"skill_id": req.SkillID,
		"rel_path": req.RelPath,
	})
	skill, err := r.getPublishedSkill(ctx, req.SkillID)
	if err != nil {
		r.Logger.WithContext(ctx).Errorf("read skill file failed: %v", err)
		return nil, err
	}
	if err = r.authorizeSkillRead(ctx, req.UserID, req.SkillID); err != nil {
		return nil, err
	}
	// If the path beyond the skill package is that the caller parameter is wrong, 400 will be returned; the naked error will be converted to 500, allowing the caller to.
	// I thought the service was broken instead of sending an error myself. The management state one (mgmt_reader) is always 400, so align it here.
	relPath, err := normalizeZipPath(req.RelPath)
	if err != nil {
		return nil, errors.DefaultHTTPError(ctx, http.StatusBadRequest, err.Error())
	}
	file, err := r.fileRepo.SelectSkillFileByPath(ctx, nil, req.SkillID, skill.Version, relPath)
	if err != nil {
		r.Logger.WithContext(ctx).Errorf("read skill file failed: %v", err)
		return nil, err
	}
	if file == nil {
		r.Logger.WithContext(ctx).Warnf("skill file not found: %s", relPath)
		err = errors.DefaultHTTPError(ctx, http.StatusNotFound, fmt.Sprintf("skill file not found: %s", relPath))
		return nil, err
	}
	downloadURL, err := r.assetStore.GetDownloadURL(ctx, &interfaces.OssObject{
		StorageID:  file.StorageID,
		StorageKey: file.StorageKey,
	})
	if err != nil {
		r.Logger.WithContext(ctx).Errorf("read skill file failed: %v", err)
		return nil, err
	}

	return &interfaces.ReadSkillFileResp{
		SkillID:  req.SkillID,
		RelPath:  relPath,
		URL:      downloadURL,
		MimeType: file.MimeType,
		FileType: file.FileType,
	}, nil
}

// GetSkillReleaseHistory Query Skill release history.
func (r *skillReader) GetSkillReleaseHistory(ctx context.Context, req *interfaces.GetSkillReleaseHistoryReq) (resp []*interfaces.SkillReleaseHistoryInfo, err error) {
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	telemetry.SetSpanAttributes(ctx, map[string]interface{}{
		"skill_id": req.SkillID,
	})

	// If it is an external interface, the semantics is the same as the other read-only interfaces in the same file: one of execution, public access, and view is enough.
	if common.IsPublicAPIFromCtx(ctx) {
		var accessor *interfaces.AuthAccessor
		accessor, err = r.AuthService.GetAccessor(ctx, req.UserID)
		if err != nil {
			return nil, err
		}
		var authorized bool
		authorized, err = r.AuthService.OperationCheckAny(ctx, accessor, req.SkillID, interfaces.AuthResourceTypeSkill,
			interfaces.AuthOperationTypeExecute, interfaces.AuthOperationTypePublicAccess, interfaces.AuthOperationTypeView)
		if err != nil {
			return nil, err
		}
		if !authorized {
			r.Logger.WithContext(ctx).Errorf("user has no permission to view release history of skill %s", req.SkillID)
			err = errors.NewHTTPError(ctx, http.StatusForbidden, errors.ErrExtCommonOperationForbidden,
				fmt.Sprintf("user has no permission to view release history of skill %s", req.SkillID))
			return nil, err
		}
	}

	histories, err := r.releaseHistoryRepo.SelectBySkillID(ctx, nil, req.SkillID)
	if err != nil {
		return nil, errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
	}
	if len(histories) == 0 {
		return []*interfaces.SkillReleaseHistoryInfo{}, nil
	}
	resp = make([]*interfaces.SkillReleaseHistoryInfo, 0, len(histories))
	for _, history := range histories {
		release := &model.SkillReleaseDB{}
		if history.SkillRelease != "" {
			release = utils.JSONToObject[*model.SkillReleaseDB](history.SkillRelease)
		}
		if release == nil {
			release = &model.SkillReleaseDB{}
		}
		resp = append(resp, &interfaces.SkillReleaseHistoryInfo{
			SkillID:     history.SkillID,
			Name:        release.Name,
			Description: release.Description,
			Version:     history.Version,
			Status:      interfaces.BizStatus(release.Status),
			Category:    interfaces.BizCategory(release.Category),
			Source:      release.Source,
			ReleaseDesc: history.ReleaseDesc,
			ReleaseUser: release.ReleaseUser,
			ReleaseTime: release.ReleaseTime,
			CreateUser:  release.CreateUser,
			CreateTime:  release.CreateTime,
			UpdateUser:  release.UpdateUser,
			UpdateTime:  release.UpdateTime,
		})
	}
	return resp, nil
}

func (r *skillReader) getPublishedSkill(ctx context.Context, skillID string) (*model.SkillRepositoryDB, error) {
	release, err := r.releaseRepo.SelectBySkillID(ctx, nil, skillID)
	if err != nil {
		return nil, errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
	}
	if release == nil {
		return nil, errors.DefaultHTTPError(ctx, http.StatusNotFound, fmt.Sprintf("skill not found: %s", skillID))
	}
	return &model.SkillRepositoryDB{
		SkillID:      release.SkillID,
		Name:         release.Name,
		Description:  release.Description,
		SkillContent: release.SkillContent,
		Version:      release.Version,
		Status:       release.Status,
		Source:       release.Source,
		Dependencies: release.Dependencies,
		ExtendInfo:   release.ExtendInfo,
		FileManifest: release.FileManifest,
		CreateUser:   release.CreateUser,
		CreateTime:   release.CreateTime,
		UpdateUser:   release.UpdateUser,
		UpdateTime:   release.UpdateTime,
		Category:     release.Category,
	}, nil
}
