package skill

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/openbkn-ai/bkn-comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/dbaccess"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/telemetry"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/auth"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/business_domain"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/utils"
)

type skillReader struct {
	skillRepo             model.ISkillRepository
	releaseRepo           model.ISkillReleaseDB
	releaseHistoryRepo    model.ISkillReleaseHistoryDB
	fileRepo              model.ISkillFileIndex
	assetStore            skillAssetStore
	AuthService           interfaces.IAuthorizationService
	BusinessDomainService interfaces.IBusinessDomainService
	Logger                interfaces.Logger
}

var (
	readerOnce sync.Once
	readerInst interfaces.SkillReader
)

// NewSkillReader 创建技能读取服务对象
func NewSkillReader() interfaces.SkillReader {
	readerOnce.Do(func() {
		conf := config.NewConfigLoader()
		readerInst = &skillReader{
			skillRepo:             dbaccess.NewSkillRepositoryDB(),
			releaseRepo:           dbaccess.NewSkillReleaseDB(),
			releaseHistoryRepo:    dbaccess.NewSkillReleaseHistoryDB(),
			fileRepo:              dbaccess.NewSkillFileIndexDB(),
			assetStore:            newOSSGatewaySkillAssetStore(),
			AuthService:           auth.NewAuthServiceImpl(),
			BusinessDomainService: business_domain.NewBusinessDomainService(),
			Logger:                conf.GetLogger(),
		}
	})
	return readerInst
}

// authorizeSkillRead 校验调用方对该技能是否有读取权限（执行 / 公开访问 / 查看三者之一）。
//
// 公开接口一律强制。内部接口（internal-v1）看 SKILL_INTERNAL_READ_AUTHZ：
// off 直接放行；shadow（默认）查但不拦，未通过只打日志；enforce 与公开接口一致返回 403。
// 分档是为了不打断存量内部调用方——context-loader 把这两个接口包成 MCP 工具之后，
// 内部路径再无条件放行就等于任意账户可读任意技能全文，但直接翻强制会误伤，先影子观察。
//
// 内部接口若拿不到账户身份（accessor 解析失败），shadow 档同样只记日志：
// 那是「无从判断」，不是「判定为无权」，此时拦下来纯属误伤。
func (r *skillReader) authorizeSkillRead(ctx context.Context, userID, skillID string) error {
	isPublic := common.IsPublicAPIFromCtx(ctx)
	mode := common.GetSkillReadAuthzMode()
	if !isPublic {
		if mode == common.SkillReadAuthzOff {
			return nil
		}
		// 内部路径上没有账户身份就没得判：调用方连自己是谁都没说，此时查授权只会
		// 把「无从判断」误判成「无权」。公开路径不走这里——那条的身份由令牌保证。
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

// GetSkillContent 获取技能内容
func (r *skillReader) GetSkillContent(ctx context.Context, req *interfaces.GetSkillContentReq) (resp *interfaces.GetSkillContentResp, err error) {
	// 记录可观测
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
	// 查询对应的"SKILL.md文件
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
	// TODO: 待接入审计日志
	return &interfaces.GetSkillContentResp{
		SkillID: skill.SkillID,
		URL:     downloadURL,
		Files:   utils.JSONToObject[[]*interfaces.SkillFileSummary](skill.FileManifest),
		Status:  interfaces.BizStatus(skill.Status),
	}, nil
}

// ReadSkillFile 读取技能文件内容
func (r *skillReader) ReadSkillFile(ctx context.Context, req *interfaces.ReadSkillFileReq) (resp *interfaces.ReadSkillFileResp, err error) {
	// 记录可观测
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	telemetry.SetSpanAttributes(ctx, map[string]interface{}{
		"user_id":  req.UserID,
		"bd_id":    req.BusinessDomainID,
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
	// 越出技能包的路径是调用方参数错，回 400；裸 error 会被兜成 500，让调用方
	// 以为服务坏了而不是自己传错。管理态那条（mgmt_reader）一直是 400，这里对齐。
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

// GetSkillReleaseHistory 查询 Skill 发布历史
func (r *skillReader) GetSkillReleaseHistory(ctx context.Context, req *interfaces.GetSkillReleaseHistoryReq) (resp []*interfaces.SkillReleaseHistoryInfo, err error) {
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	telemetry.SetSpanAttributes(ctx, map[string]interface{}{
		"skill_id": req.SkillID,
	})

	// 如果是外部接口，口径与同文件其余只读接口一致：执行、公共访问、查看三者有其一即可
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
