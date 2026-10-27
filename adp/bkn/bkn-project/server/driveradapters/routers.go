// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	libCommon "github.com/openbkn-ai/bkn-foundry/comm-go/common"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/middleware"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/auth"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/draft"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/lineage"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/project"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/release"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/version"
)

// readyTimeout bounds the dependency check behind the readiness probe.
const readyTimeout = 5 * time.Second

type RestHandler interface {
	RegisterPublic(engine *gin.Engine)
}

// pinger is the part of *sql.DB the readiness probe needs.
type pinger interface {
	PingContext(ctx context.Context) error
}

type restHandler struct {
	appSetting *common.AppSetting
	db         pinger
	as         interfaces.AuthService
	rs         interfaces.ReleaseService
	ls         interfaces.LineageService
	ps         interfaces.ProjectService
	drs        interfaces.DraftService
}

func NewRestHandler(appSetting *common.AppSetting, db pinger) RestHandler {
	return &restHandler{
		appSetting: appSetting,
		db:         db,
		as:         auth.NewAuthService(),
		rs:         release.NewReleaseService(),
		ls:         lineage.NewLineageService(),
		ps:         project.NewProjectService(),
		drs:        draft.NewDraftService(appSetting),
	}
}

func (r *restHandler) RegisterPublic(c *gin.Engine) {
	c.Use(r.AccessLog())
	c.Use(middleware.TracingMiddleware())
	c.Use(r.LanguageMiddleware())

	c.GET("/api/bkn-project/v1/health", r.HealthCheck)
	c.GET("/api/bkn-project/v1/ready", r.ReadyCheck)

	apiV1 := c.Group("/api/bkn-project/v1")
	apiV1.Use(rest.PrivateNoCacheMiddleware())

	// Release lineage.
	apiV1.GET("/lineages/:lineage_id/releases", r.ListReleasesByEx)
	apiV1.POST("/lineages/:lineage_id/releases", r.PublishReleaseByEx)
	apiV1.GET("/releases/:network_id", r.GetReleaseByEx)

	// Lineages and project branches. A branch name may contain "/", so the
	// branch routes catch the rest of the path: {branch}/definitions and {branch}/sync.
	apiV1.POST("/lineages", r.CreateLineageByEx)
	apiV1.POST("/lineages/:lineage_id/branches", r.CreateBranchByEx)
	apiV1.PUT("/lineages/:lineage_id/branches/*branch", r.CommitDefinitionsByEx)
	apiV1.POST("/lineages/:lineage_id/branches/*branch", r.SyncBranchByEx)
	apiV1.POST("/lineages/:lineage_id/drift", r.CheckDriftByEx)
	apiV1.GET("/lineages/:lineage_id/switch-plan", r.PlanSwitchByEx)
	apiV1.POST("/lineages/:lineage_id/switches", r.SwitchByEx)

	// Projects. Actions on a project are custom methods, {project_id}:archive
	// and {project_id}:unarchive, so the POST route reads them from the id.
	apiV1.POST("/projects", r.CreateProjectByEx)
	apiV1.GET("/projects", r.ListProjectsByEx)
	apiV1.GET("/projects/:project_id", r.GetProjectByEx)
	apiV1.POST("/projects/:project_id", r.ProjectActionByEx)

	// Ontology drafts: drafting and restoring an excluded field are custom methods,
	// ontology/draft:generate and draft:restore; people edit items one by one and choose
	// object types' identities.
	apiV1.GET("/projects/:project_id/ontology/draft", r.GetDraftByEx)
	apiV1.POST("/projects/:project_id/ontology/:action", r.OntologyActionByEx)
	apiV1.PATCH("/projects/:project_id/ontology/draft/items/:kind/:item_id", r.EditDraftItemByEx)
	apiV1.PUT("/projects/:project_id/ontology/draft/object_types/:item_id/identity", r.ChooseIdentityByEx)

	logger.Info("RestHandler RegisterPublic")
}

// HealthCheck reports service health.
func (r *restHandler) HealthCheck(c *gin.Context) {
	// Return service information.
	rest.ReplyOK(c, http.StatusOK, gin.H{
		"ServerName":    version.ServerName,
		"ServerVersion": version.ServerVersion,
		"Language":      version.LanguageGo,
		"GoVersion":     version.GoVersion,
		"GoArch":        version.GoArch,
	})
}

// ReadyCheck reports whether the service can serve requests: the database must be reachable.
func (r *restHandler) ReadyCheck(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), readyTimeout)
	defer cancel()

	if err := r.db.PingContext(ctx); err != nil {
		logger.Warnf("Readiness check failed: database ping failed: %v", err)
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(c.Request.Context()), "BknProject.Validation.Detail.DependencyUnavailable", nil)
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		rest.ReplyError(c, httperrors.NewHTTPError(c.Request.Context(), http.StatusServiceUnavailable, httperrors.PublicError_ServiceUnavailable).
			WithErrorDetails(localizedText))
		return
	}
	rest.ReplyOK(c, http.StatusOK, gin.H{"status": "ready"})
}

// LanguageMiddleware resolves Accept-Language once and stores it in request context.
// Register this after TracingMiddleware so the language context is layered onto the trace context.
func (r *restHandler) LanguageMiddleware() gin.HandlerFunc {
	return rest.LanguageMiddleware()
}

// Gin middleware access log.
func (r *restHandler) AccessLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		beginTime := time.Now()
		c.Next()
		endTime := time.Now()
		durTime := endTime.Sub(beginTime).Seconds()

		logger.Debugf("access log: url: %s, method: %s, begin_time: %s, end_time: %s, subTime: %f",
			c.Request.URL.Path,
			c.Request.Method,
			beginTime.Format(libCommon.RFC3339Milli),
			endTime.Format(libCommon.RFC3339Milli),
			durTime,
		)
	}
}

// Verify OAuth credentials.
func (r *restHandler) verifyOAuth(ctx context.Context, c *gin.Context) (hydra.Visitor, error) {
	visitor, err := r.as.VerifyToken(ctx, c)
	if err != nil {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknProject.Validation.Detail.AuthenticationFailed", nil)
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		httpErr := httperrors.NewHTTPError(ctx, http.StatusUnauthorized, httperrors.PublicError_Unauthorized).
			WithErrorDetails(localizedText)
		rest.ReplyError(c, httpErr)
		return visitor, err
	}

	return visitor, nil
}

// withExVisitor authenticates an external request before handing it on.
func (r *restHandler) withExVisitor(c *gin.Context, handle func(*gin.Context, hydra.Visitor)) {
	visitor, err := r.verifyOAuth(i18n.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	handle(c, visitor)
}
