package driveradapters

import (
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/driveradapters/skill"
)

type SkillRestHandler interface {
	// RegisterPrivate register internal API.
	RegisterPrivate(engine *gin.RouterGroup)
	// RegisterPublic Register public API.
	RegisterPublic(engine *gin.RouterGroup)
}

type skillRestHandler struct {
	SkillHandler skill.SkillHandler
}

var (
	sOnce    sync.Once
	sHandler SkillRestHandler
)

func NewSkillRestHandler() SkillRestHandler {
	sOnce.Do(func() {
		sHandler = &skillRestHandler{
			SkillHandler: skill.NewSkillHandler(),
		}
	})
	return sHandler
}
func (r *skillRestHandler) RegisterPrivate(engine *gin.RouterGroup) {
	// Market interface.
	// Query skill market list.
	engine.GET("/skills/market", r.SkillHandler.QuerySkillMarketList)
	// Check skills market details.
	engine.GET("/skills/market/:skill_id", r.SkillHandler.GetSkillMarketDetail)
	// Metadata interface.
	// Batch names by skill ID. Same handler as the public face; FilterViewableIDs returns
	// the IDs unchanged on the internal face, so callers see existence, not their own grants.
	engine.POST("/skills/names", r.SkillHandler.QuerySkillNamesByIDs)
	// Query skill details. Registered here for bkn-backend, whose execution-factory client
	// is pinned to internal-v1. Unlike /skills/market/:skill_id it applies no public_access
	// filter and reports an unpublished skill through `status` instead of 404.
	engine.GET("/skills/:skill_id", r.SkillHandler.GetSkillDetail)
	// Retrieval interface. The skills segment is a literal, so it does not collide with :skill_id.
	// Internal face only: the whitelist carries the caller's authorization decision, and a public
	// caller supplying its own whitelist would be deciding its own scope.
	engine.POST("/skills/search", r.SkillHandler.SearchSkills)
	// Read interface.
	// Query skill content.
	engine.GET("/skills/:skill_id/content", r.SkillHandler.GetSkillContent)
	// Read skill file.
	engine.POST("/skills/:skill_id/files/read", r.SkillHandler.ReadSkillFile)
	// executive skills.
	engine.POST("/skills/:skill_id/execute", r.SkillHandler.ExecuteSkill)
	// Management state reading interface.
	engine.GET("/skills/:skill_id/management/content", r.SkillHandler.GetManagementContent)
	engine.POST("/skills/:skill_id/management/files/read", r.SkillHandler.ReadManagementFile)
	engine.GET("/skills/:skill_id/management/download", r.SkillHandler.DownloadManagementSkill)
}

func (r *skillRestHandler) RegisterPublic(engine *gin.RouterGroup) {
	// Management interface.
	// Register skills.
	engine.POST("/skills", r.SkillHandler.RegisterSkill)
	// Query skill list.
	engine.GET("/skills", r.SkillHandler.QuerySkillList)
	// POST /api/agent-operator-integration/v1/skills/names Batch names based on skill ID (front-end object-level authorization page echo)
	engine.POST("/skills/names", r.SkillHandler.QuerySkillNamesByIDs)
	// Query skill details.
	engine.GET("/skills/:skill_id", r.SkillHandler.GetSkillDetail)
	// Download skills.
	engine.GET("/skills/:skill_id/download", r.SkillHandler.DownloadSkill)
	// Delete skills.
	engine.DELETE("/skills/:skill_id", r.SkillHandler.DeleteSkill)
	// update status.
	engine.PUT("/skills/:skill_id/status", r.SkillHandler.UpdateSkillStatus)
	// Update metadata.
	engine.PUT("/skills/:skill_id/metadata", r.SkillHandler.UpdateSkillMetadata)
	// Update skill pack.
	engine.PUT("/skills/:skill_id/package", r.SkillHandler.UpdateSkillPackage)
	// Restore historical versions to draft state.
	engine.POST("/skills/:skill_id/history/republish", r.SkillHandler.RepublishSkillHistory)
	// Publish historical version directly.
	engine.POST("/skills/:skill_id/history/publish", r.SkillHandler.PublishSkillHistory)
	// Market interface.
	// Query skill market list.
	engine.GET("/skills/market", r.SkillHandler.QuerySkillMarketList)
	// Check skills market details.
	engine.GET("/skills/market/:skill_id", r.SkillHandler.GetSkillMarketDetail)
	// Read interface.
	// Query skill content.
	engine.GET("/skills/:skill_id/content", r.SkillHandler.GetSkillContent)
	// Read skill file.
	engine.POST("/skills/:skill_id/files/read", r.SkillHandler.ReadSkillFile)
	// executive skills.
	engine.POST("/skills/:skill_id/execute", r.SkillHandler.ExecuteSkill)
	// Query skill release history.
	engine.GET("/skills/:skill_id/history", r.SkillHandler.GetSkillReleaseHistory)
	// Management state reading interface.
	engine.GET("/skills/:skill_id/management/content", r.SkillHandler.GetManagementContent)
	engine.POST("/skills/:skill_id/management/files/read", r.SkillHandler.ReadManagementFile)
	engine.GET("/skills/:skill_id/management/download", r.SkillHandler.DownloadManagementSkill)
	// Build interface.
	engine.POST("/skills/index/build", r.SkillHandler.CreateSkillIndexBuildTask)
	engine.GET("/skills/index/build", r.SkillHandler.QuerySkillIndexBuildTaskList)
	engine.GET("/skills/index/build/:task_id", r.SkillHandler.GetSkillIndexBuildTask)
	engine.POST("/skills/index/build/:task_id/cancel", r.SkillHandler.CancelSkillIndexBuildTask)
	engine.POST("/skills/index/build/:task_id/retry", r.SkillHandler.RetrySkillIndexBuildTask)
}
