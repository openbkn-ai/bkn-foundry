package skill

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/dbaccess"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/drivenadapters"
	infracommon "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common/ormhelper"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/telemetry"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/auth"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/category"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/sandbox"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/utils"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"gopkg.in/yaml.v3"
)

type skillRegistry struct {
	parser             *skillParser
	skillRepo          model.ISkillRepository
	fileRepo           model.ISkillFileIndex
	releaseRepo        model.ISkillReleaseDB
	releaseHistoryRepo model.ISkillReleaseHistoryDB
	assetStore         skillAssetStore
	indexSync          interfaces.SkillIndexSyncService
	sandboxClient      interfaces.SandBoxControlPlane
	sessionPool        sandbox.SessionPool
	dbTx               model.DBTx
	AuthService        interfaces.IAuthorizationService
	UserMgnt           interfaces.UserManagement
	Logger             interfaces.Logger
	CategoryManager    interfaces.CategoryManager
}

var (
	registryOnce sync.Once
	registryInst interfaces.SkillRegistry
)

const maxSkillReleaseHistoryVersions = 10

// NewSkillRegistry creates a skill register.
func NewSkillRegistry() interfaces.SkillRegistry {
	registryOnce.Do(func() {
		registryInst = &skillRegistry{
			Logger:             config.NewConfigLoader().GetLogger(),
			parser:             newSkillParser(),
			skillRepo:          dbaccess.NewSkillRepositoryDB(),
			fileRepo:           dbaccess.NewSkillFileIndexDB(),
			releaseRepo:        dbaccess.NewSkillReleaseDB(),
			releaseHistoryRepo: dbaccess.NewSkillReleaseHistoryDB(),
			assetStore:         newOSSGatewaySkillAssetStore(),
			indexSync:          NewSkillIndexSyncService(),
			sandboxClient:      drivenadapters.NewSandBoxControlPlaneClient(),
			sessionPool:        sandbox.GetSessionPool(),
			dbTx:               dbaccess.NewBaseTx(),
			AuthService:        auth.NewAuthServiceImpl(),
			UserMgnt:           drivenadapters.NewUserManagementClient(),
			CategoryManager:    category.NewCategoryManager(),
		}
	})
	return registryInst
}

// RegisterSkill Register skills.
func (r *skillRegistry) RegisterSkill(ctx context.Context, req *interfaces.RegisterSkillReq) (resp *interfaces.RegisterSkillResp, err error) {
	// record observable.
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	telemetry.SetSpanAttributes(ctx, map[string]interface{}{
		"user_id": req.UserID,
	})
	// Check new permissions.
	accessor, err := r.AuthService.GetAccessor(ctx, req.UserID)
	if err != nil {
		return nil, err
	}
	if err = r.AuthService.CheckCreatePermission(ctx, accessor, interfaces.AuthResourceTypeSkill); err != nil {
		return nil, err
	}
	// Check whether the classification is legal.
	if req.Category != "" {
		if !r.CategoryManager.CheckCategory(req.Category) {
			err = errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtSkillCategoryNotFound,
				fmt.Sprintf(" %s category not found", req.Category))
			return
		}
	}

	skill, files, assets, err := r.parser.parseRegisterReq(req)
	if err != nil {
		return nil, err
	}
	skill.FileManifest = utils.ObjectToJSON(files)

	tx, err := r.dbTx.GetTx(ctx)
	if err != nil {
		return nil, fmt.Errorf("get tx failed: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		} else {
			_ = tx.Commit()
		}
	}()
	// Insert skills.
	skillID, err := r.skillRepo.InsertSkill(ctx, tx, skill)
	if err != nil {
		return nil, err
	}
	if len(assets) > 0 {
		var fileIndices []*model.SkillFileIndexDB
		fileIndices, err = r.persistSkillAssets(ctx, skillID, skill.Version, assets)
		if err != nil {
			return nil, err
		}
		if err = r.fileRepo.BatchInsertSkillFiles(ctx, tx, fileIndices); err != nil {
			return nil, err
		}
	}
	// Triggering a new policy, the creator has all operating permissions on the current resources by default.
	err = r.AuthService.CreateOwnerPolicy(ctx, accessor, &interfaces.AuthResource{
		ID:   skill.SkillID,
		Type: string(interfaces.AuthResourceTypeSkill),
		Name: skill.Name,
	})
	if err != nil {
		return nil, err
	}

	filePaths := make([]string, 0, len(files))
	for _, file := range files {
		filePaths = append(filePaths, file.RelPath)
	}
	resp = &interfaces.RegisterSkillResp{
		SkillID:     skillID,
		Name:        skill.Name,
		Description: skill.Description,
		Version:     skill.Version,
		Status:      interfaces.BizStatus(skill.Status),
		Files:       filePaths,
	}
	// TODO: Audit log to be accessed.
	return resp, nil
}

// UpdateSkillMetadata updates skill metadata.
func (r *skillRegistry) UpdateSkillMetadata(ctx context.Context, req *interfaces.UpdateSkillMetadataReq) (resp *interfaces.UpdateSkillMetadataResp, err error) {
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	telemetry.SetSpanAttributes(ctx, map[string]interface{}{
		"skill_id": req.SkillID,
		"user_id":  req.UserID,
	})

	skill, err := r.skillRepo.SelectSkillByID(ctx, nil, req.SkillID)
	if err != nil {
		return nil, err
	}
	if skill == nil || skill.IsDeleted {
		return nil, errors.DefaultHTTPError(ctx, http.StatusNotFound, fmt.Sprintf("skill not found: %s", req.SkillID))
	}
	accessor, err := r.AuthService.GetAccessor(ctx, req.UserID)
	if err != nil {
		return nil, err
	}
	if err = r.AuthService.CheckModifyPermission(ctx, accessor, req.SkillID, interfaces.AuthResourceTypeSkill); err != nil {
		return nil, err
	}
	if req.Category != "" && !r.CategoryManager.CheckCategory(req.Category) {
		return nil, errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtSkillCategoryNotFound,
			fmt.Sprintf(" %s category not found", req.Category))
	}

	// FR-6: Determine whether metadata has changed and determine whether OSS SKILL.md needs to be rewritten.
	nameChanged := req.Name != skill.Name
	descChanged := req.Description != skill.Description
	needsRewrite := nameChanged || descChanged

	tx, err := r.dbTx.GetTx(ctx)
	if err != nil {
		return nil, fmt.Errorf("get tx failed: %w", err)
	}
	defer func() {
		if err != nil {
			if rollbackErr := tx.Rollback(); rollbackErr != nil {
				r.Logger.WithContext(ctx).Errorf("rollback skill metadata update failed, skill_id=%s, err=%v", req.SkillID, rollbackErr)
			}
		} else {
			commitErr := tx.Commit()
			if commitErr != nil {
				r.Logger.WithContext(ctx).Errorf("commit skill metadata update failed, skill_id=%s, err=%v", req.SkillID, commitErr)
			}
			// FR-6: After the transaction is submitted successfully, rewrite the frontmatter of OSS SKILL.md.
			if needsRewrite {
				if rewriteErr := r.rewriteSkillMDFrontmatter(ctx, skill.SkillID, skill.Version, req.Name, req.Description); rewriteErr != nil {
					r.Logger.WithContext(ctx).Errorf("rewrite SKILL.md frontmatter failed, skill_id=%s, err=%v", skill.SkillID, rewriteErr)
				}
			}
		}
	}()

	skill.Name = req.Name
	skill.Description = req.Description
	skill.Category = req.Category.String()
	if req.Source != "" {
		skill.Source = req.Source
	}
	if req.ExtendInfo != nil {
		skill.ExtendInfo = string(req.ExtendInfo)
	}
	skill.UpdateUser = req.UserID
	if skill.Status == interfaces.BizStatusPublished.String() {
		skill.Status = interfaces.BizStatusEditing.String()
	}
	if err = r.skillRepo.UpdateSkill(ctx, tx, skill); err != nil {
		return nil, err
	}
	return &interfaces.UpdateSkillMetadataResp{
		SkillID: skill.SkillID,
		Version: skill.Version,
		Status:  interfaces.BizStatus(skill.Status),
	}, nil
}

// UpdateSkillPackage Update skill package.
func (r *skillRegistry) UpdateSkillPackage(ctx context.Context, req *interfaces.UpdateSkillPackageReq) (resp *interfaces.UpdateSkillPackageResp, err error) {
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	telemetry.SetSpanAttributes(ctx, map[string]interface{}{
		"skill_id":  req.SkillID,
		"user_id":   req.UserID,
		"file_type": req.FileType,
	})

	skill, err := r.skillRepo.SelectSkillByID(ctx, nil, req.SkillID)
	if err != nil {
		return nil, err
	}
	if skill == nil || skill.IsDeleted {
		return nil, errors.DefaultHTTPError(ctx, http.StatusNotFound, fmt.Sprintf("skill not found: %s", req.SkillID))
	}
	accessor, err := r.AuthService.GetAccessor(ctx, req.UserID)
	if err != nil {
		return nil, err
	}
	if err = r.AuthService.CheckModifyPermission(ctx, accessor, req.SkillID, interfaces.AuthResourceTypeSkill); err != nil {
		return nil, err
	}

	parsedSkill, files, assets, err := r.parser.parseRegisterReq(&interfaces.RegisterSkillReq{
		UserID:     req.UserID,
		FileType:   req.FileType,
		File:       req.File,
		Category:   interfaces.BizCategory(skill.Category),
		Source:     skill.Source,
		ExtendInfo: json.RawMessage(skill.ExtendInfo),
	})
	if err != nil {
		return nil, err
	}
	replaceCurrentVersion := skill.Status == interfaces.BizStatusEditing.String() ||
		skill.Status == interfaces.BizStatusUnpublish.String()
	targetVersion := parsedSkill.Version
	if replaceCurrentVersion {
		targetVersion = skill.Version
	}

	tx, err := r.dbTx.GetTx(ctx)
	if err != nil {
		return nil, fmt.Errorf("get tx failed: %w", err)
	}
	defer func() {
		if err != nil {
			if rollbackErr := tx.Rollback(); rollbackErr != nil {
				r.Logger.WithContext(ctx).Errorf("rollback skill package update failed, skill_id=%s, err=%v", req.SkillID, rollbackErr)
			}
		} else {
			commitErr := tx.Commit()
			if commitErr != nil {
				r.Logger.WithContext(ctx).Errorf("commit skill package update failed, skill_id=%s, err=%v", req.SkillID, commitErr)
			}
		}
	}()

	skill.Name = parsedSkill.Name
	skill.Description = parsedSkill.Description
	skill.SkillContent = parsedSkill.SkillContent
	skill.Version = targetVersion
	skill.Dependencies = parsedSkill.Dependencies
	skill.ExtendInfo = parsedSkill.ExtendInfo
	skill.FileManifest = utils.ObjectToJSON(files)
	skill.UpdateUser = req.UserID
	if skill.Status == interfaces.BizStatusPublished.String() || skill.Status == interfaces.BizStatusOffline.String() {
		skill.Status = interfaces.BizStatusEditing.String()
	}
	if replaceCurrentVersion {
		existingFiles, selectErr := r.fileRepo.SelectSkillFileBySkillID(ctx, tx, skill.SkillID, targetVersion)
		if selectErr != nil {
			return nil, selectErr
		}
		for _, file := range existingFiles {
			if deleteErr := r.assetStore.Delete(ctx, &interfaces.OssObject{
				StorageID:  file.StorageID,
				StorageKey: file.StorageKey,
			}); deleteErr != nil {
				return nil, deleteErr
			}
		}
		if err = r.fileRepo.DeleteSkillFileBySkillID(ctx, tx, skill.SkillID, targetVersion); err != nil {
			return nil, err
		}
	}
	if err = r.skillRepo.UpdateSkill(ctx, tx, skill); err != nil {
		return nil, err
	}
	if len(assets) > 0 {
		fileIndices, persistErr := r.persistSkillAssets(ctx, skill.SkillID, targetVersion, assets)
		if persistErr != nil {
			return nil, persistErr
		}
		if err = r.fileRepo.BatchInsertSkillFiles(ctx, tx, fileIndices); err != nil {
			return nil, err
		}
	}
	resp = &interfaces.UpdateSkillPackageResp{
		SkillID: skill.SkillID,
		Version: skill.Version,
		Status:  interfaces.BizStatus(skill.Status),
	}
	if r.indexSync != nil {
		if syncErr := r.indexSync.UpdateSkill(ctx, skill); syncErr != nil {
			r.Logger.WithContext(ctx).Errorf("update skill index failed after package update, skill_id=%s, err=%v", req.SkillID, syncErr)
		}
	}
	return resp, nil
}

// RepublishSkillHistory restores historical versions to draft state.
func (r *skillRegistry) RepublishSkillHistory(ctx context.Context, req *interfaces.RepublishSkillHistoryReq) (resp *interfaces.RepublishSkillHistoryResp, err error) {
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	telemetry.SetSpanAttributes(ctx, map[string]interface{}{
		"skill_id": req.SkillID,
		"user_id":  req.UserID,
		"version":  req.Version,
	})

	skill, err := r.skillRepo.SelectSkillByID(ctx, nil, req.SkillID)
	if err != nil {
		return nil, err
	}
	if skill == nil || skill.IsDeleted {
		return nil, errors.DefaultHTTPError(ctx, http.StatusNotFound, fmt.Sprintf("skill not found: %s", req.SkillID))
	}
	accessor, err := r.AuthService.GetAccessor(ctx, req.UserID)
	if err != nil {
		return nil, err
	}
	if err = r.AuthService.CheckModifyPermission(ctx, accessor, req.SkillID, interfaces.AuthResourceTypeSkill); err != nil {
		return nil, err
	}

	history, err := r.releaseHistoryRepo.SelectBySkillIDAndVersion(ctx, nil, req.SkillID, req.Version)
	if err != nil {
		return nil, err
	}
	if history == nil {
		return nil, errors.DefaultHTTPError(ctx, http.StatusNotFound, fmt.Sprintf("skill history not found: %s@%s", req.SkillID, req.Version))
	}
	release := utils.JSONToObject[*model.SkillReleaseDB](history.SkillRelease)
	if release == nil {
		return nil, errors.DefaultHTTPError(ctx, http.StatusInternalServerError, fmt.Sprintf("invalid skill history snapshot: %s@%s", req.SkillID, req.Version))
	}

	tx, err := r.dbTx.GetTx(ctx)
	if err != nil {
		return nil, fmt.Errorf("get tx failed: %w", err)
	}
	defer func() {
		if err != nil {
			if rollbackErr := tx.Rollback(); rollbackErr != nil {
				r.Logger.WithContext(ctx).Errorf("rollback skill history republish failed, skill_id=%s, version=%s, err=%v", req.SkillID, req.Version, rollbackErr)
			}
		} else {
			commitErr := tx.Commit()
			if commitErr != nil {
				r.Logger.WithContext(ctx).Errorf("commit skill history republish failed, skill_id=%s, version=%s, err=%v", req.SkillID, req.Version, commitErr)
			}
		}
	}()

	skill.Name = release.Name
	skill.Description = release.Description
	skill.SkillContent = release.SkillContent
	skill.Version = release.Version
	skill.Category = release.Category
	skill.Source = release.Source
	skill.ExtendInfo = release.ExtendInfo
	skill.Dependencies = release.Dependencies
	skill.FileManifest = release.FileManifest
	skill.Status = interfaces.BizStatusEditing.String()
	skill.UpdateUser = req.UserID
	if err = r.skillRepo.UpdateSkill(ctx, tx, skill); err != nil {
		return nil, err
	}
	return &interfaces.RepublishSkillHistoryResp{
		SkillID: skill.SkillID,
		Version: skill.Version,
		Status:  interfaces.BizStatus(skill.Status),
	}, nil
}

// PublishSkillHistory directly publishes historical versions.
func (r *skillRegistry) PublishSkillHistory(ctx context.Context, req *interfaces.PublishSkillHistoryReq) (resp *interfaces.PublishSkillHistoryResp, err error) {
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	telemetry.SetSpanAttributes(ctx, map[string]interface{}{
		"skill_id": req.SkillID,
		"user_id":  req.UserID,
		"version":  req.Version,
	})

	skill, err := r.skillRepo.SelectSkillByID(ctx, nil, req.SkillID)
	if err != nil {
		return nil, err
	}
	if skill == nil || skill.IsDeleted {
		return nil, errors.DefaultHTTPError(ctx, http.StatusNotFound, fmt.Sprintf("skill not found: %s", req.SkillID))
	}
	accessor, err := r.AuthService.GetAccessor(ctx, req.UserID)
	if err != nil {
		return nil, err
	}
	if err = r.AuthService.CheckModifyPermission(ctx, accessor, req.SkillID, interfaces.AuthResourceTypeSkill); err != nil {
		return nil, err
	}
	if err = r.AuthService.CheckPublishPermission(ctx, accessor, req.SkillID, interfaces.AuthResourceTypeSkill); err != nil {
		return nil, err
	}

	history, err := r.releaseHistoryRepo.SelectBySkillIDAndVersion(ctx, nil, req.SkillID, req.Version)
	if err != nil {
		return nil, err
	}
	if history == nil {
		return nil, errors.DefaultHTTPError(ctx, http.StatusNotFound, fmt.Sprintf("skill history not found: %s@%s", req.SkillID, req.Version))
	}
	release := utils.JSONToObject[*model.SkillReleaseDB](history.SkillRelease)
	if release == nil {
		return nil, errors.DefaultHTTPError(ctx, http.StatusInternalServerError, fmt.Sprintf("invalid skill history snapshot: %s@%s", req.SkillID, req.Version))
	}
	if err = r.checkSkillDuplicateName(ctx, release.Name, req.SkillID); err != nil {
		return nil, err
	}

	tx, err := r.dbTx.GetTx(ctx)
	if err != nil {
		return nil, fmt.Errorf("get tx failed: %w", err)
	}
	defer func() {
		if err != nil {
			if rollbackErr := tx.Rollback(); rollbackErr != nil {
				r.Logger.WithContext(ctx).Errorf("rollback skill history publish failed, skill_id=%s, version=%s, err=%v", req.SkillID, req.Version, rollbackErr)
			}
		} else {
			commitErr := tx.Commit()
			if commitErr != nil {
				r.Logger.WithContext(ctx).Errorf("commit skill history publish failed, skill_id=%s, version=%s, err=%v", req.SkillID, req.Version, commitErr)
			}
		}
	}()

	skill.Name = release.Name
	skill.Description = release.Description
	skill.SkillContent = release.SkillContent
	skill.Version = release.Version
	skill.Category = release.Category
	skill.Source = release.Source
	skill.ExtendInfo = release.ExtendInfo
	skill.Dependencies = release.Dependencies
	skill.FileManifest = release.FileManifest
	skill.Status = interfaces.BizStatusPublished.String()
	skill.UpdateUser = req.UserID
	if err = r.skillRepo.UpdateSkill(ctx, tx, skill); err != nil {
		return nil, err
	}
	if err = r.publishSkillSnapshot(ctx, tx, skill, req.UserID); err != nil {
		return nil, err
	}
	if r.indexSync != nil {
		if syncErr := r.indexSync.UpsertSkill(ctx, skill); syncErr != nil {
			r.Logger.WithContext(ctx).Errorf("sync published historical skill index failed, skill_id=%s, version=%s, err=%v", req.SkillID, req.Version, syncErr)
		}
	}
	return &interfaces.PublishSkillHistoryResp{
		SkillID: skill.SkillID,
		Version: skill.Version,
		Status:  interfaces.BizStatus(skill.Status),
	}, nil
}

// DeleteSkill delete skill.
func (r *skillRegistry) DeleteSkill(ctx context.Context, req *interfaces.DeleteSkillReq) (err error) {
	// record observable.
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	telemetry.SetSpanAttributes(ctx, map[string]interface{}{
		"skill_id": req.SkillID,
		"user_id":  req.UserID,
	})
	// Check delete permissions.
	accessor, err := r.AuthService.GetAccessor(ctx, req.UserID)
	if err != nil {
		return err
	}
	if err = r.AuthService.CheckDeletePermission(ctx, accessor, req.SkillID, interfaces.AuthResourceTypeSkill); err != nil {
		return err
	}
	skill, err := r.skillRepo.SelectSkillByID(ctx, nil, req.SkillID)
	if err != nil {
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, fmt.Sprintf("select skill by id failed: %s", err.Error()))
		return
	}
	// The skill does not exist or has been deleted.
	if skill == nil || skill.IsDeleted {
		err = errors.DefaultHTTPError(ctx, http.StatusNotFound, fmt.Sprintf("skill not found: %s", req.SkillID))
		return
	}
	// Delete status verification and use public status management.
	if !common.CanDelete(interfaces.BizStatus(skill.Status)) {
		err = errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtSkillUnSupportDelete,
			fmt.Sprintf("skill can not be deleted in status: %s", skill.Status))
		return
	}
	tx, err := r.dbTx.GetTx(ctx)
	if err != nil {
		return fmt.Errorf("get tx failed: %w", err)
	}
	defer func() {
		if err != nil {
			if rollbackErr := tx.Rollback(); rollbackErr != nil {
				r.Logger.WithContext(ctx).Errorf("rollback skill delete failed, skill_id=%s, err=%v", req.SkillID, rollbackErr)
				return
			}
		} else {
			if commitErr := tx.Commit(); commitErr != nil {
				r.Logger.WithContext(ctx).Errorf("commit skill delete failed, skill_id=%s, err=%v", req.SkillID, commitErr)
				return
			}
		}
	}()

	// Mark the skill as being deleted, TODO: You need to design a separate coroutine to handle the deletion interruption strategy.
	if err = r.skillRepo.UpdateSkillDeleted(ctx, tx, req.SkillID, true, req.UserID); err != nil {
		return err
	}
	// Find index file.
	files, err := r.fileRepo.SelectSkillFileBySkillID(ctx, tx, req.SkillID, skill.Version)
	if err != nil {
		return err
	}
	// Delete records in object storage first, then delete records in database.
	for _, file := range files {
		if err = r.assetStore.Delete(ctx, &interfaces.OssObject{
			StorageID:  file.StorageID,
			StorageKey: file.StorageKey,
		}); err != nil {
			r.Logger.WithContext(ctx).Warnf("delete file failed, err:%s", err.Error())
			return err
		}
	}
	if err = r.fileRepo.DeleteSkillFileBySkillID(ctx, tx, req.SkillID, skill.Version); err != nil {
		return err
	}
	if r.releaseRepo != nil {
		if err = r.deletePublishedSkillSnapshot(ctx, tx, req.SkillID); err != nil {
			return err
		}
	}
	if r.releaseHistoryRepo != nil {
		if err = r.releaseHistoryRepo.DeleteBySkillID(ctx, tx, req.SkillID); err != nil {
			return err
		}
	}
	if err = r.skillRepo.DeleteSkillByID(ctx, tx, req.SkillID); err != nil {
		return err
	}
	// Delete the permission policy of a skill.
	if err = r.AuthService.DeletePolicy(ctx, []string{req.SkillID}, interfaces.AuthResourceTypeSkill); err != nil {
		return err
	}
	if r.indexSync != nil {
		if syncErr := r.indexSync.DeleteSkill(ctx, req.SkillID); syncErr != nil {
			r.Logger.WithContext(ctx).Errorf("delete skill index failed after skill delete, skill_id=%s, err=%v", req.SkillID, syncErr)
		}
	}
	return nil
}

// UpdateSkillStatus updates skill status.
func (r *skillRegistry) UpdateSkillStatus(ctx context.Context, req *interfaces.UpdateSkillStatusReq) (resp *interfaces.UpdateSkillStatusResp, err error) {
	// record observable.
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	telemetry.SetSpanAttributes(ctx, map[string]interface{}{
		"skill_id": req.SkillID,
		"user_id":  req.UserID,
		"status":   req.Status,
	})
	// Acquire skills.
	skill, err := r.skillRepo.SelectSkillByID(ctx, nil, req.SkillID)
	if err != nil {
		return nil, err
	}
	// The skill does not exist or has been deleted.
	if skill == nil || skill.IsDeleted {
		err = errors.DefaultHTTPError(ctx, http.StatusNotFound, fmt.Sprintf("skill not found: %s", req.SkillID))
		return
	}
	// Check whether the state transition is legal.
	if !common.CheckStatusTransition(interfaces.BizStatus(skill.Status), req.Status) {
		err = errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtSkillStatusInvalid,
			fmt.Sprintf("skill status can not be updated from %s to %s", skill.Status, req.Status))
		return
	}
	// Check update permissions.
	accessor, err := r.AuthService.GetAccessor(ctx, req.UserID)
	if err != nil {
		return nil, err
	}
	switch req.Status {
	case interfaces.BizStatusPublished:
		// Check if you have publishing permissions.
		err = r.AuthService.CheckPublishPermission(ctx, accessor, req.SkillID, interfaces.AuthResourceTypeSkill)
		if err != nil {
			return nil, err
		}
		// Check if there is a duplicate name.
		err = r.checkSkillDuplicateName(ctx, skill.Name, skill.SkillID)
		if err != nil {
			return nil, err
		}
	case interfaces.BizStatusUnpublish, interfaces.BizStatusEditing:
	case interfaces.BizStatusOffline:
		err = r.AuthService.CheckUnpublishPermission(ctx, accessor, req.SkillID, interfaces.AuthResourceTypeSkill)
		if err != nil {
			return nil, err
		}
	}
	tx, err := r.dbTx.GetTx(ctx)
	if err != nil {
		return nil, fmt.Errorf("get tx failed: %w", err)
	}
	defer func() {
		if err != nil {
			if rollbackErr := tx.Rollback(); rollbackErr != nil {
				r.Logger.WithContext(ctx).Errorf("rollback skill status update failed, skill_id=%s, err=%v", req.SkillID, rollbackErr)
			}
		} else {
			if commitErr := tx.Commit(); commitErr != nil {
				r.Logger.WithContext(ctx).Errorf("commit skill status update failed, skill_id=%s, err=%v", req.SkillID, commitErr)
				return
			}
		}
	}()
	// Update skill status.
	if err = r.skillRepo.UpdateSkillStatus(ctx, tx, req.SkillID, string(req.Status), req.UserID); err != nil {
		return nil, err
	}
	switch req.Status {
	case interfaces.BizStatusPublished:
		if err = r.publishSkillSnapshot(ctx, tx, skill, req.UserID); err != nil {
			return nil, err
		}
	case interfaces.BizStatusOffline:
		if err = r.deletePublishedSkillSnapshot(ctx, tx, req.SkillID); err != nil {
			return nil, err
		}
	}
	resp = &interfaces.UpdateSkillStatusResp{
		SkillID: req.SkillID,
		Status:  req.Status,
	}
	// Write skill data to dataset without blocking the main process.
	switch req.Status {
	case interfaces.BizStatusPublished:
		if r.indexSync != nil {
			if syncErr := r.indexSync.UpsertSkill(ctx, skill); syncErr != nil {
				r.Logger.WithContext(ctx).Errorf("sync published skill index failed, skill_id=%s, err=%v", req.SkillID, syncErr)
			}
		}
	case interfaces.BizStatusOffline:
		if r.indexSync != nil {
			if syncErr := r.indexSync.DeleteSkill(ctx, req.SkillID); syncErr != nil {
				r.Logger.WithContext(ctx).Errorf("delete skill index failed after status update, skill_id=%s, err=%v", req.SkillID, syncErr)
			}
		}
	}
	return resp, nil
}

// Duplicate name check.
func (r *skillRegistry) checkSkillDuplicateName(ctx context.Context, name string, skillID string) (err error) {
	has, skillDB, err := r.skillRepo.SelectSkillByName(ctx, nil, name, []string{string(interfaces.BizStatusPublished)})
	if err != nil {
		r.Logger.WithContext(ctx).Errorf("select skill by name failed, err: %v", err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, "select skill by name failed")
		return
	}
	if !has || (skillID != "" && skillDB.SkillID == skillID) {
		return
	}
	// exist.
	err = errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtSkillNameDuplicate,
		fmt.Sprintf("skill name %s already exists", name), name)
	return
}

// DownloadSkill download skills.
func (r *skillRegistry) DownloadSkill(ctx context.Context, req *interfaces.DownloadSkillReq) (resp *interfaces.DownloadSkillResp, err error) {
	// Record observability.
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	telemetry.SetSpanAttributes(ctx, map[string]interface{}{
		"skill_id": req.SkillID,
		"user_id":  req.UserID,
	})

	accessor, err := r.AuthService.GetAccessor(ctx, req.UserID)
	if err != nil {
		return nil, err
	}
	// Check if you have view or public access rights.
	authorized, err := r.AuthService.OperationCheckAny(ctx, accessor, req.SkillID, interfaces.AuthResourceTypeSkill,
		interfaces.AuthOperationTypeView, interfaces.AuthOperationTypePublicAccess)
	if err != nil {
		return
	}
	if !authorized {
		err = errors.NewHTTPError(ctx, http.StatusForbidden, errors.ErrExtCommonOperationForbidden, nil)
		return
	}
	skill, fileName, archive, err := r.buildSkillArchive(ctx, req.SkillID)
	if err != nil {
		return nil, err
	}

	return &interfaces.DownloadSkillResp{
		SkillID:  skill.SkillID,
		FileName: fileName,
		Content:  archive,
	}, nil
}

// ExecuteSkill execution skills.
func (r *skillRegistry) ExecuteSkill(ctx context.Context, req *interfaces.ExecuteSkillReq) (resp *interfaces.ExecuteSkillResp, err error) {
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	telemetry.SetSpanAttributes(ctx, map[string]interface{}{
		"user_id":  req.UserID,
		"skill_id": req.SkillID,
	})

	accessor, err := r.AuthService.GetAccessor(ctx, req.UserID)
	if err != nil {
		return nil, err
	}
	authorized, err := r.AuthService.OperationCheckAny(ctx, accessor, req.SkillID, interfaces.AuthResourceTypeSkill,
		interfaces.AuthOperationTypeExecute, interfaces.AuthOperationTypePublicAccess)
	if err != nil {
		return nil, err
	}
	if !authorized {
		return nil, errors.NewHTTPError(ctx, http.StatusForbidden, errors.ErrExtCommonOperationForbidden, nil)
	}

	skill, fileName, archive, err := r.buildSkillArchive(ctx, req.SkillID)
	if err != nil {
		return nil, err
	}

	sessionID, err := r.sessionPool.AcquireSession(ctx)
	if err != nil {
		return nil, err
	}
	defer r.sessionPool.ReleaseSession(sessionID)

	uploadWorkDir := path.Join("skills", req.SkillID)

	uploadResp, err := r.sandboxClient.UploadSkillArchive(ctx, sessionID, &interfaces.UploadSkillArchiveReq{
		WorkDir:  uploadWorkDir,
		FileName: fileName,
		Content:  archive,
	})
	if err != nil {
		return nil, err
	}
	execResp, err := r.sandboxClient.ExecuteShell(ctx, sessionID, &interfaces.ExecuteShellReq{
		WorkDir: uploadResp.WorkDir,
		Command: req.EntryShell,
		Timeout: req.Timeout,
	})
	if err != nil {
		return nil, err
	}

	return &interfaces.ExecuteSkillResp{
		SkillID:       skill.SkillID,
		SessionID:     sessionID,
		WorkDir:       uploadResp.WorkDir,
		FileName:      uploadResp.FileName,
		UploadedPath:  uploadResp.UploadedPath,
		Command:       execResp.Command,
		ExitCode:      execResp.ExitCode,
		Stdout:        execResp.Stdout,
		Stderr:        execResp.Stderr,
		ExecutionTime: execResp.ExecutionTime,
		Mocked:        uploadResp.Mocked || execResp.Mocked,
	}, nil
}

func (r *skillRegistry) buildSkillArchive(ctx context.Context, skillID string) (*model.SkillRepositoryDB, string, []byte, error) {
	skillDB, err := r.skillRepo.SelectSkillByID(ctx, nil, skillID)
	if err != nil {
		return nil, "", nil, err
	}
	if skillDB == nil {
		return nil, "", nil, fmt.Errorf("skill not found: %s", skillID)
	}
	files, err := r.fileRepo.SelectSkillFileBySkillID(ctx, nil, skillID, skillDB.Version)
	if err != nil {
		return nil, "", nil, err
	}
	return r.buildSkillArchiveFromSnapshot(ctx, skillDB, files)
}

func (r *skillRegistry) buildSkillArchiveFromSnapshot(ctx context.Context, skill *model.SkillRepositoryDB,
	files []*model.SkillFileIndexDB) (*model.SkillRepositoryDB, string, []byte, error) {
	var buf bytes.Buffer
	var err error
	zw := zip.NewWriter(&buf)
	writeFile := func(name string, content []byte) error {
		w, createErr := zw.Create(name)
		if createErr != nil {
			return createErr
		}
		_, writeErr := io.Copy(w, bytes.NewReader(content))
		return writeErr
	}

	for _, file := range files {
		content, readErr := r.assetStore.Download(ctx, &interfaces.OssObject{
			StorageID:  file.StorageID,
			StorageKey: file.StorageKey,
		})
		if readErr != nil {
			_ = zw.Close()
			return nil, "", nil, readErr
		}
		if err = writeFile(file.RelPath, content); err != nil {
			_ = zw.Close()
			return nil, "", nil, err
		}
	}
	if err = zw.Close(); err != nil {
		return nil, "", nil, err
	}

	return skill, fmt.Sprintf("%s.zip", skill.Name), buf.Bytes(), nil
}

// QuerySkillList Query skills list (management interface)
func (r *skillRegistry) QuerySkillList(ctx context.Context, req *interfaces.QuerySkillListReq) (resp *interfaces.QuerySkillListResp, err error) {
	// record observable.
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	telemetry.SetSpanAttributes(ctx, map[string]interface{}{
		"user_id": req.UserID,
	})
	resp = &interfaces.QuerySkillListResp{
		CommonPageResult: interfaces.CommonPageResult{
			Page:     req.Page,
			PageSize: req.PageSize,
		},
		Data: []*interfaces.SkillInfo{},
	}
	// Conditional construction.
	filter := map[string]interface{}{
		"all":         req.All,
		"name":        req.Name,
		"create_user": req.CreateUser,
		"status":      req.Status.String(),
	}
	// Check whether the classification is legal.
	if req.Category != "" {
		if !r.CategoryManager.CheckCategory(req.Category) {
			err = errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtSkillCategoryNotFound,
				fmt.Sprintf(" %s category not found", req.Category))
			return
		}
		filter["category"] = req.Category.String()
	}

	authResp, err := r.querySkillListPage(ctx, filter, req.CommonPageParams, req.UserID, interfaces.AuthOperationTypeView)
	if err != nil {
		return nil, err
	}
	resp.CommonPageResult = authResp.CommonPageResult
	if len(authResp.Data) == 0 {
		return resp, nil
	}
	skillInfos, err := r.assembleSkillInfoList(ctx, authResp.Data)
	if err != nil {
		return nil, err
	}
	resp.Data = skillInfos
	return resp, nil
}

// Assemble Skill Market Summary List.
func (r *skillRegistry) assembleMarketSkillInfoList(ctx context.Context, releaseDB []*model.SkillReleaseDB) (skillInfos []*interfaces.SkillInfo, err error) {
	var userIDs []string
	skillInfos = []*interfaces.SkillInfo{}
	for _, relese := range releaseDB {
		skillInfos = append(skillInfos, convertSkillMarketDetail(relese, r.CategoryManager.GetCategoryName(ctx, interfaces.BizCategory(relese.Category))))
		userIDs = append(userIDs, relese.CreateUser, relese.UpdateUser, relese.ReleaseUser)
	}
	// Get user name.
	userMap, err := r.UserMgnt.GetUsersName(ctx, userIDs)
	if err != nil {
		return
	}
	for _, skill := range skillInfos {
		skill.CreateUser = utils.GetValueOrDefault(userMap, skill.CreateUser, interfaces.UnknownUser)
		skill.UpdateUser = utils.GetValueOrDefault(userMap, skill.UpdateUser, interfaces.UnknownUser)
		skill.ReleaseUser = utils.GetValueOrDefault(userMap, skill.ReleaseUser, interfaces.UnknownUser)
	}
	return
}

// Assembly skills return information list.
func (r *skillRegistry) assembleSkillInfoList(ctx context.Context, skillDBs []*model.SkillRepositoryDB) (skillInfos []*interfaces.SkillInfo, err error) {
	var userIDs []string
	skillInfos = []*interfaces.SkillInfo{}
	for _, skill := range skillDBs {
		skillInfos = append(skillInfos, convertSkillDetail(skill, r.CategoryManager.GetCategoryName(ctx, interfaces.BizCategory(skill.Category))))
		userIDs = append(userIDs, skill.CreateUser, skill.UpdateUser)
	}
	// Get user name.
	userMap, err := r.UserMgnt.GetUsersName(ctx, userIDs)
	if err != nil {
		return
	}
	for _, skill := range skillInfos {
		skill.CreateUser = utils.GetValueOrDefault(userMap, skill.CreateUser, interfaces.UnknownUser)
		skill.UpdateUser = utils.GetValueOrDefault(userMap, skill.UpdateUser, interfaces.UnknownUser)
	}
	return
}

func (r *skillRegistry) queryReleaseListPage(ctx context.Context, filter map[string]interface{}, pageParamsReq interfaces.CommonPageParams, userID string, operations ...interfaces.AuthOperationType) (
	authResp *interfaces.QueryResponse[model.SkillReleaseDB], err error) {
	// Build query executor.
	sortField := "f_update_time"
	switch pageParamsReq.SortBy {
	case "create_time":
		sortField = "f_create_time"
	case "name":
		sortField = "f_name"
	}
	sortOrder := ormhelper.SortOrderDesc
	if pageParamsReq.SortOrder == "asc" {
		sortOrder = ormhelper.SortOrderAsc
	}
	sort := &ormhelper.SortParams{Fields: []ormhelper.SortField{{Field: sortField, Order: sortOrder}}}
	// Total number of statistics.
	queryTotal := func(newCtx context.Context) (int64, error) {
		var count int64
		count, err = r.releaseRepo.CountByWhereClause(ctx, nil, filter)
		if err != nil {
			r.Logger.WithContext(newCtx).Errorf("count skill list failed, err: %v", err)
			err = errors.DefaultHTTPError(newCtx, http.StatusInternalServerError, "count skill list failed")
			return 0, err
		}
		return count, nil
	}
	// queryBatch query skill list paging.
	queryBatch := func(newCtx context.Context, pageSize int, offset int, cursorValue *model.SkillReleaseDB) ([]*model.SkillReleaseDB, error) {
		var skills []*model.SkillReleaseDB
		var cursor *ormhelper.CursorParams
		if cursorValue != nil {
			cursor = &ormhelper.CursorParams{
				Field:     sortField,
				Direction: ormhelper.SortOrder(pageParamsReq.SortOrder),
			}
			switch sortField {
			case "f_update_time":
				cursor.Value = cursorValue.UpdateTime
			case "f_create_time":
				cursor.Value = cursorValue.CreateTime
			case "f_name":
				cursor.Value = cursorValue.Name
			}
			// If using a cursor, offset is not required.
			offset = 0
		}
		filter["limit"] = pageSize
		filter["offset"] = offset
		skills, err = r.releaseRepo.SelectListPage(ctx, nil, filter, sort, cursor)
		if err != nil {
			r.Logger.WithContext(newCtx).Errorf("select skill list page failed, err: %v", err)
			err = errors.DefaultHTTPError(newCtx, http.StatusInternalServerError, "select skill list page failed")
			return nil, err
		}
		return skills, nil
	}
	queryBuilder := auth.NewQueryBuilder[model.SkillReleaseDB]().
		SetPage(pageParamsReq.Page, pageParamsReq.PageSize).SetAll(pageParamsReq.All).
		SetQueryFunctions(queryTotal, queryBatch).
		SetFilteredQueryFunctions( // Query function with filter conditions.
			func(newCtx context.Context, ids []string) (int64, error) {
				filter["in"] = ids
				return queryTotal(newCtx)
			},
			func(newCtx context.Context, pageSize int, offset int, ids []string, cursorValue *model.SkillReleaseDB) ([]*model.SkillReleaseDB, error) {
				filter["in"] = ids
				return queryBatch(newCtx, pageSize, offset, cursorValue)
			},
		)
	if infracommon.IsPublicAPIFromCtx(ctx) {
		// If it is an external interface, permission check.
		queryBuilder.SetAuthFilter(func(newCtx context.Context) ([]string, error) {
			// Check viewing permissions.
			var accessor *interfaces.AuthAccessor
			accessor, err = r.AuthService.GetAccessor(newCtx, userID)
			if err != nil {
				return nil, err
			}
			return r.AuthService.ResourceListIDs(newCtx, accessor, interfaces.AuthResourceTypeSkill, operations...)
		})
	}
	authResp, err = queryBuilder.Execute(ctx)
	return
}

func (r *skillRegistry) querySkillListPage(ctx context.Context, filter map[string]interface{}, pageParamsReq interfaces.CommonPageParams, userID string, operations ...interfaces.AuthOperationType) (
	authResp *interfaces.QueryResponse[model.SkillRepositoryDB], err error) {
	// Build query executor.
	sortField := "f_update_time"
	switch pageParamsReq.SortBy {
	case "create_time":
		sortField = "f_create_time"
	case "name":
		sortField = "f_name"
	}
	sortOrder := ormhelper.SortOrderDesc
	if pageParamsReq.SortOrder == "asc" {
		sortOrder = ormhelper.SortOrderAsc
	}
	sort := &ormhelper.SortParams{Fields: []ormhelper.SortField{{Field: sortField, Order: sortOrder}}}
	// Total number of statistics.
	queryTotal := func(newCtx context.Context) (int64, error) {
		var count int64
		count, err = r.skillRepo.CountByWhereClause(ctx, nil, filter)
		if err != nil {
			r.Logger.WithContext(newCtx).Errorf("count skill list failed, err: %v", err)
			err = errors.DefaultHTTPError(newCtx, http.StatusInternalServerError, "count skill list failed")
			return 0, err
		}
		return count, nil
	}
	// queryBatch query skill list paging.
	queryBatch := func(newCtx context.Context, pageSize int, offset int, cursorValue *model.SkillRepositoryDB) ([]*model.SkillRepositoryDB, error) {
		var skills []*model.SkillRepositoryDB
		var cursor *ormhelper.CursorParams
		if cursorValue != nil {
			cursor = &ormhelper.CursorParams{
				Field:     sortField,
				Direction: ormhelper.SortOrder(pageParamsReq.SortOrder),
			}
			switch sortField {
			case "f_update_time":
				cursor.Value = cursorValue.UpdateTime
			case "f_create_time":
				cursor.Value = cursorValue.CreateTime
			case "f_name":
				cursor.Value = cursorValue.Name
			}
			// If using a cursor, offset is not required.
			offset = 0
		}
		filter["limit"] = pageSize
		filter["offset"] = offset
		skills, err = r.skillRepo.SelectSkillListPage(ctx, nil, filter, sort, cursor)
		if err != nil {
			r.Logger.WithContext(newCtx).Errorf("select skill list page failed, err: %v", err)
			err = errors.DefaultHTTPError(newCtx, http.StatusInternalServerError, "select skill list page failed")
			return nil, err
		}
		return skills, nil
	}
	queryBuilder := auth.NewQueryBuilder[model.SkillRepositoryDB]().
		SetPage(pageParamsReq.Page, pageParamsReq.PageSize).SetAll(pageParamsReq.All).
		SetQueryFunctions(queryTotal, queryBatch).
		SetFilteredQueryFunctions( // Query function with filter conditions.
			func(newCtx context.Context, ids []string) (int64, error) {
				filter["in"] = ids
				return queryTotal(newCtx)
			},
			func(newCtx context.Context, pageSize int, offset int, ids []string, cursorValue *model.SkillRepositoryDB) ([]*model.SkillRepositoryDB, error) {
				filter["in"] = ids
				return queryBatch(newCtx, pageSize, offset, cursorValue)
			},
		)
	if infracommon.IsPublicAPIFromCtx(ctx) {
		// If it is an external interface, permission check.
		queryBuilder.SetAuthFilter(func(newCtx context.Context) ([]string, error) {
			// Check viewing permissions.
			var accessor *interfaces.AuthAccessor
			accessor, err = r.AuthService.GetAccessor(newCtx, userID)
			if err != nil {
				return nil, err
			}
			return r.AuthService.ResourceListIDs(newCtx, accessor, interfaces.AuthResourceTypeSkill, operations...)
		})
	}
	authResp, err = queryBuilder.Execute(ctx)
	return
}

// QuerySkillMarketList Query skill market list.
func (r *skillRegistry) QuerySkillMarketList(ctx context.Context, req *interfaces.QuerySkillMarketListReq) (resp *interfaces.QuerySkillMarketListResp, err error) {
	// record observable.
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	telemetry.SetSpanAttributes(ctx, map[string]interface{}{
		"user_id": req.UserID,
	})

	resp = &interfaces.QuerySkillMarketListResp{
		CommonPageResult: interfaces.CommonPageResult{
			Page:     req.Page,
			PageSize: req.PageSize,
		},
		Data: []*interfaces.SkillInfo{},
	}
	// Conditional construction.
	filter := map[string]interface{}{
		"all":         req.All,
		"name":        req.Name,
		"create_user": req.CreateUser,
		"status":      interfaces.BizStatusPublished.String(),
	}
	// Check whether the classification is legal.
	if req.Category != "" {
		if !r.CategoryManager.CheckCategory(req.Category) {
			err = errors.NewHTTPError(ctx, http.StatusBadRequest, errors.ErrExtSkillCategoryNotFound,
				fmt.Sprintf(" %s category not found", req.Category))
			return
		}
		filter["category"] = req.Category.String()
	}

	authResp, err := r.queryReleaseListPage(ctx, filter, req.CommonPageParams, req.UserID,
		interfaces.AuthOperationTypePublicAccess)
	if err != nil {
		return nil, err
	}
	resp.CommonPageResult = authResp.CommonPageResult
	if len(authResp.Data) == 0 {
		return resp, nil
	}
	skillInfos, err := r.assembleMarketSkillInfoList(ctx, authResp.Data)
	if err != nil {
		return nil, err
	}
	resp.Data = skillInfos
	return resp, nil
}

// GetSkillMarketDetail Gets skills market details.
func (r *skillRegistry) GetSkillMarketDetail(ctx context.Context, req *interfaces.GetSkillMarketDetailReq) (resp *interfaces.SkillInfo, err error) {
	// record observable.
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	telemetry.SetSpanAttributes(ctx, map[string]interface{}{
		"user_id":  req.UserID,
		"skill_id": req.SkillID,
	})
	accessor, err := r.AuthService.GetAccessor(ctx, req.UserID)
	if err != nil {
		return nil, err
	}
	if err = r.AuthService.CheckPublicAccessPermission(ctx, accessor, req.SkillID, interfaces.AuthResourceTypeSkill); err != nil {
		return nil, err
	}
	release, err := r.releaseRepo.SelectBySkillID(ctx, nil, req.SkillID)
	if err != nil {
		return nil, err
	}
	if release == nil {
		err = errors.DefaultHTTPError(ctx, http.StatusNotFound, fmt.Sprintf("skill not found: %s", req.SkillID))
		return nil, err
	}
	skillInfo := convertSkillMarketDetail(release, r.CategoryManager.GetCategoryName(ctx, interfaces.BizCategory(release.Category)))
	userNames, err := r.UserMgnt.GetUsersName(ctx, []string{release.CreateUser, release.UpdateUser, release.ReleaseUser})
	if err != nil {
		return nil, err
	}
	skillInfo.CreateUser = utils.GetValueOrDefault(userNames, release.CreateUser, interfaces.UnknownUser)
	skillInfo.UpdateUser = utils.GetValueOrDefault(userNames, release.UpdateUser, interfaces.UnknownUser)
	skillInfo.ReleaseUser = utils.GetValueOrDefault(userNames, release.ReleaseUser, interfaces.UnknownUser)
	return skillInfo, nil
}

// GetSkillDetail Gets skill details.
func (r *skillRegistry) GetSkillDetail(ctx context.Context, req *interfaces.GetSkillDetailReq) (resp *interfaces.SkillInfo, err error) {
	// record observable.
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	telemetry.SetSpanAttributes(ctx, map[string]interface{}{
		"user_id":  req.UserID,
		"skill_id": req.SkillID,
	})
	accessor, err := r.AuthService.GetAccessor(ctx, req.UserID)
	if err != nil {
		return nil, err
	}
	if err = r.AuthService.CheckViewPermission(ctx, accessor, req.SkillID, interfaces.AuthResourceTypeSkill); err != nil {
		return nil, err
	}
	skill, err := r.skillRepo.SelectSkillByID(ctx, nil, req.SkillID)
	if err != nil {
		return nil, err
	}
	if skill == nil || skill.IsDeleted {
		return nil, fmt.Errorf("skill not found: %s", req.SkillID)
	}
	skillInfo := convertSkillDetail(skill, r.CategoryManager.GetCategoryName(ctx, interfaces.BizCategory(skill.Category)))
	// Get user information.
	userNames, err := r.UserMgnt.GetUsersName(ctx, []string{skill.CreateUser, skill.UpdateUser})
	if err != nil {
		return nil, err
	}
	skillInfo.CreateUser = utils.GetValueOrDefault(userNames, skill.CreateUser, interfaces.UnknownUser)
	skillInfo.UpdateUser = utils.GetValueOrDefault(userNames, skill.UpdateUser, interfaces.UnknownUser)
	return skillInfo, nil
}

// GetSkillNamesByIDs batch names based on skill IDs (lightweight read-only, non-existing IDs are ignored)
// The public page is filtered by viewing permissions: IDs without permissions are silently ignored like IDs that do not exist.
func (r *skillRegistry) GetSkillNamesByIDs(ctx context.Context, ids []string) (resp *interfaces.BatchNamesResp, err error) {
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	resp = &interfaces.BatchNamesResp{Entries: []*interfaces.NameEntry{}}
	ids = utils.UniqueStrings(ids)
	if len(ids) == 0 {
		return
	}
	ids, err = auth.FilterViewableIDs(ctx, r.AuthService, "", ids, interfaces.AuthResourceTypeSkill)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return
	}
	skills, err := r.skillRepo.SelectSkillListByIDs(ctx, ids)
	if err != nil {
		r.Logger.WithContext(ctx).Errorf("select skills by ids failed, err: %v", err)
		err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	for _, skill := range skills {
		resp.Entries = append(resp.Entries, &interfaces.NameEntry{ID: skill.SkillID, Name: skill.Name})
	}
	return
}

func convertSkillDetail(skill *model.SkillRepositoryDB, categoryName string) *interfaces.SkillInfo {
	return &interfaces.SkillInfo{
		SkillID:      skill.SkillID,
		Name:         skill.Name,
		Description:  skill.Description,
		Version:      skill.Version,
		Category:     interfaces.BizCategory(skill.Category),
		CategoryName: categoryName,
		Status:       interfaces.BizStatus(skill.Status),
		Source:       skill.Source,
		Dependencies: utils.JSONToObject[map[string]interface{}](skill.Dependencies),
		ExtendInfo:   utils.JSONToObject[map[string]interface{}](skill.ExtendInfo),
		CreateUser:   skill.CreateUser,
		CreateTime:   skill.CreateTime,
		UpdateUser:   skill.UpdateUser,
		UpdateTime:   skill.UpdateTime,
	}
}

func convertSkillMarketDetail(skill *model.SkillReleaseDB, categoryName string) *interfaces.SkillInfo {
	return &interfaces.SkillInfo{
		SkillID:      skill.SkillID,
		Name:         skill.Name,
		Description:  skill.Description,
		Version:      skill.Version,
		Category:     interfaces.BizCategory(skill.Category),
		CategoryName: categoryName,
		Status:       interfaces.BizStatus(skill.Status),
		Source:       skill.Source,
		Dependencies: utils.JSONToObject[map[string]interface{}](skill.Dependencies),
		ExtendInfo:   utils.JSONToObject[map[string]interface{}](skill.ExtendInfo),
		CreateUser:   skill.CreateUser,
		CreateTime:   skill.CreateTime,
		UpdateUser:   skill.UpdateUser,
		UpdateTime:   skill.UpdateTime,
		ReleaseTime:  skill.ReleaseTime,
		ReleaseUser:  skill.ReleaseUser,
	}
}

func (r *skillRegistry) persistSkillAssets(ctx context.Context, skillID, version string, assets []*skillAsset) ([]*model.SkillFileIndexDB, error) {
	indices := make([]*model.SkillFileIndexDB, 0, len(assets))
	for _, asset := range assets {
		object, checksum, err := r.assetStore.Upload(ctx, skillID, version, asset.RelPath, asset.Content)
		if err != nil {
			return nil, err
		}
		indices = append(indices, &model.SkillFileIndexDB{
			SkillID:       skillID,
			SkillVersion:  version,
			RelPath:       asset.RelPath,
			PathHash:      utils.MD5(asset.RelPath),
			StorageID:     object.StorageID,
			StorageKey:    object.StorageKey,
			FileType:      asset.FileType,
			ContentSHA256: checksum,
			MimeType:      asset.MimeType,
			Size:          int64(len(asset.Content)),
		})
	}
	return indices, nil
}

func (r *skillRegistry) publishSkillSnapshot(ctx context.Context, tx *sql.Tx, skill *model.SkillRepositoryDB, userID string) error {
	now := time.Now().UnixNano()
	release := &model.SkillReleaseDB{
		SkillID:      skill.SkillID,
		Name:         skill.Name,
		Description:  skill.Description,
		SkillContent: skill.SkillContent,
		Version:      skill.Version,
		Category:     skill.Category,
		Source:       skill.Source,
		ExtendInfo:   skill.ExtendInfo,
		Dependencies: skill.Dependencies,
		FileManifest: skill.FileManifest,
		Status:       interfaces.BizStatusPublished.String(),
		CreateTime:   skill.CreateTime,
		CreateUser:   skill.CreateUser,
		UpdateTime:   skill.UpdateTime,
		UpdateUser:   skill.UpdateUser,
		ReleaseTime:  now,
		ReleaseUser:  userID,
	}
	history := &model.SkillReleaseHistoryDB{
		SkillID:     skill.SkillID,
		Version:     skill.Version,
		ReleaseDesc: "",
		CreateTime:  now,
		CreateUser:  userID,
		UpdateTime:  now,
		UpdateUser:  userID,
	}
	release.SkillContent = skill.SkillContent
	history.SkillRelease = utils.ObjectToJSON(release)

	existingRelease, err := r.releaseRepo.SelectBySkillID(ctx, tx, skill.SkillID)
	if err != nil {
		return err
	}
	if existingRelease == nil {
		if err = r.releaseRepo.Insert(ctx, tx, release); err != nil {
			return err
		}
	} else {
		if err = r.releaseRepo.UpdateBySkillID(ctx, tx, release); err != nil {
			return err
		}
	}
	// add history.
	existingHistory, err := r.releaseHistoryRepo.SelectBySkillIDAndVersion(ctx, tx, skill.SkillID, skill.Version)
	if err != nil {
		return err
	}
	histories, err := r.releaseHistoryRepo.SelectBySkillID(ctx, tx, skill.SkillID)
	if err != nil {
		return err
	}
	if existingHistory != nil {
		if err = r.releaseHistoryRepo.DeleteByID(ctx, tx, existingHistory.ID); err != nil {
			return err
		}
	} else if len(histories) >= maxSkillReleaseHistoryVersions {
		recordsToDelete := len(histories) - maxSkillReleaseHistoryVersions + 1
		startIndex := len(histories) - recordsToDelete
		for i := startIndex; i < len(histories); i++ {
			if err = r.releaseHistoryRepo.DeleteByID(ctx, tx, histories[i].ID); err != nil {
				return err
			}
		}
	}
	if err = r.releaseHistoryRepo.Insert(ctx, tx, history); err != nil {
		return err
	}
	return nil
}

func (r *skillRegistry) deletePublishedSkillSnapshot(ctx context.Context, tx *sql.Tx, skillID string) error {
	release, err := r.releaseRepo.SelectBySkillID(ctx, tx, skillID)
	if err != nil {
		return err
	}
	if release == nil {
		return nil
	}
	if err = r.releaseRepo.DeleteBySkillID(ctx, tx, skillID); err != nil {
		return err
	}
	return nil
}

// ========== FR-6: OSS SKILL.md Frontmatter Rewrite ==========

// rewriteSkillMDFrontmatter rewrites the name/description of SKILL.md in OSS.
// Called after the UpdateSkillMetadata transaction is successfully committed.
// Failure only records the log and does not block the main process.
func (r *skillRegistry) rewriteSkillMDFrontmatter(ctx context.Context, skillID, version, newName, newDesc string) error {
	skillFile, err := r.fileRepo.SelectSkillFileByPath(ctx, nil, skillID, version, SkillMD)
	if err != nil {
		return fmt.Errorf("query SKILL.md file_index failed: %w", err)
	}
	if skillFile == nil {
		return fmt.Errorf("SKILL.md not found in file_index: skill_id=%s, version=%s", skillID, version)
	}

	content, err := r.assetStore.Download(ctx, &interfaces.OssObject{
		StorageID:  skillFile.StorageID,
		StorageKey: skillFile.StorageKey,
	})
	if err != nil {
		return fmt.Errorf("download SKILL.md from OSS failed: %w", err)
	}

	newContent, err := updateFrontmatterNameDesc(string(content), newName, newDesc)
	if err != nil {
		return fmt.Errorf("update SKILL.md frontmatter failed: %w", err)
	}

	_, _, err = r.assetStore.Upload(ctx, skillID, version, SkillMD, []byte(newContent))
	if err != nil {
		return fmt.Errorf("upload rewritten SKILL.md to OSS failed: %w", err)
	}
	return nil
}

// updateFrontmatterNameDesc only replaces name and description in YAML frontmatter.
// All remaining custom fields remain unchanged.
func updateFrontmatterNameDesc(rawMD, newName, newDesc string) (string, error) {
	parts := strings.SplitN(rawMD, "---", 3)
	if len(parts) < 3 {
		return "", fmt.Errorf("invalid SKILL.md format: missing frontmatter")
	}

	frontmatter := make(map[string]interface{})
	if err := yaml.Unmarshal([]byte(parts[1]), &frontmatter); err != nil {
		return "", fmt.Errorf("failed to unmarshal frontmatter: %w", err)
	}

	if newName != "" {
		frontmatter["name"] = newName
	}
	if newDesc != "" {
		frontmatter["description"] = newDesc
	}

	newFrontmatter, err := yaml.Marshal(frontmatter)
	if err != nil {
		return "", fmt.Errorf("failed to marshal frontmatter: %w", err)
	}

	return "---\n" + string(newFrontmatter) + "---\n" + strings.TrimPrefix(parts[2], "\n"), nil
}
