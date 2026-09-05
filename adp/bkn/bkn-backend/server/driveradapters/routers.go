// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	libCommon "github.com/openbkn-ai/bkn-foundry/comm-go/common"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/middleware"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	"bkn-backend/common"
	"bkn-backend/common/bkntrace"
	"bkn-backend/common/operationaudit"
	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
	"bkn-backend/logics/action_schedule"
	"bkn-backend/logics/action_type"
	"bkn-backend/logics/auth"
	"bkn-backend/logics/bkn"
	"bkn-backend/logics/concept_group"
	"bkn-backend/logics/knowledge_network"
	metriclogics "bkn-backend/logics/metric"
	"bkn-backend/logics/object_type"
	"bkn-backend/logics/relation_type"
	"bkn-backend/logics/risk_type"
	"bkn-backend/version"
)

type RestHandler interface {
	RegisterPublic(engine *gin.Engine)
}

type restHandler struct {
	appSetting              *common.AppSetting
	auditRecorder           operationAuditRecorder
	auditQueryStore         operationAuditQueryStore
	auditIdentityResolver   func(context.Context, string, hydra.Visitor) operationAuditActor
	auditAccessResolver     func(context.Context, string, string) (bkntrace.OperationAuditProfile, error)
	as                      interfaces.AuthService
	ass                     interfaces.ActionScheduleService
	ats                     interfaces.ActionTypeService
	cgs                     interfaces.ConceptGroupService
	kns                     interfaces.KNService
	knProxyPublisher        interfaces.KNProxyMutationPublisher
	ots                     interfaces.ObjectTypeService
	rts                     interfaces.RelationTypeService
	rtsRisk                 interfaces.RiskTypeService
	ms                      interfaces.MetricService
	bs                      interfaces.BKNService
	projectionGrantVerifier *bkntrace.ProjectionGrantVerifier
}

func NewRestHandler(appSetting *common.AppSetting, auditStore *operationaudit.Store) RestHandler {
	var projectionVerifier *bkntrace.ProjectionGrantVerifier
	if projectionGrantVerifierEnabled() {
		verifier, err := bkntrace.NewProjectionGrantVerifierFromEnv()
		if err != nil {
			panic(fmt.Sprintf("invalid projection grant verifier configuration: %v", err))
		}
		projectionVerifier = &verifier
	}
	knService := knowledge_network.NewKNService(appSetting)
	r := &restHandler{
		appSetting:          appSetting,
		auditRecorder:       auditStore,
		auditQueryStore:     auditStore,
		auditAccessResolver: bkntrace.ResolveOperationAuditProfile,
		auditIdentityResolver: func(ctx context.Context, authorization string, visitor hydra.Visitor) operationAuditActor {
			actor := basicOperationAuditActor(authorization, visitor)
			profile, err := bkntrace.ResolveOperationAuditIdentity(ctx, authorization, visitor.ID)
			if err == nil {
				actor.ActorName = profile.ActorName
			}
			return actor
		},
		as:                      auth.NewAuthService(appSetting),
		ass:                     action_schedule.NewActionScheduleService(appSetting),
		ats:                     action_type.NewActionTypeService(appSetting),
		cgs:                     concept_group.NewConceptGroupService(appSetting),
		kns:                     knService,
		knProxyPublisher:        knService,
		ots:                     object_type.NewObjectTypeService(appSetting),
		rts:                     relation_type.NewRelationTypeService(appSetting),
		rtsRisk:                 risk_type.NewRiskTypeService(appSetting),
		ms:                      metriclogics.NewMetricService(appSetting),
		bs:                      bkn.NewBKNService(appSetting),
		projectionGrantVerifier: projectionVerifier,
	}
	return r
}

func projectionGrantVerifierEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("BKN_TRACE_PROJECTION_GRANT_ENABLED")), "true")
}

func (r *restHandler) RegisterPublic(c *gin.Engine) {
	c.Use(r.AccessLog())
	c.Use(middleware.TracingMiddleware())
	c.Use(r.LanguageMiddleware())
	c.Use(r.OperationAudit())

	c.GET("/api/bkn-backend/v1/health", r.HealthCheck)

	bknApiV1 := c.Group("/api/bkn-backend/v1")
	otlApiV1 := c.Group("/api/ontology-manager/v1")
	bknApiV1.Use(rest.PrivateNoCacheMiddleware())
	otlApiV1.Use(rest.PrivateNoCacheMiddleware())
	bknApiV1.GET("/trace/outbox", r.ListTraceOutbox)
	bknApiV1.GET("/trace/outbox/:outbox_id", r.GetTraceOutbox)
	bknApiV1.POST("/trace/outbox/:outbox_id/retry", r.verifyJsonContentType(), r.RetryTraceOutbox)
	bknApiV1.POST("/trace/outbox/:outbox_id/abandon", r.verifyJsonContentType(), r.AbandonTraceOutbox)
	bknApiV1.GET("/operation-audits", r.ListOperationAudits)
	bknApiV1.GET("/operation-audits/:event_id", r.GetOperationAudit)

	for _, apiV1 := range []*gin.RouterGroup{bknApiV1, otlApiV1} {
		// Knowledge networks.
		apiV1.POST("/knowledge-networks", r.verifyJsonContentType(), r.CreateKNByEx)
		// Resolve names by ID in batch for object-level authorization views; the names segment does not conflict with :kn_id.
		apiV1.POST("/knowledge-networks/names", r.verifyJsonContentType(), r.QueryKNNamesByIDs)
		apiV1.DELETE("/knowledge-networks/:kn_id", r.DeleteKN)
		apiV1.PUT("/knowledge-networks/:kn_id", r.verifyJsonContentType(), r.UpdateKNByEx)
		apiV1.GET("/knowledge-networks", r.ListKNsByEx)
		apiV1.GET("/knowledge-networks/:kn_id", r.GetKNByEx)
		apiV1.POST("/knowledge-networks/:kn_id/validation", r.verifyJsonContentType(), r.ValidateKNByEx)
		apiV1.POST("/knowledge-networks/:kn_id/relation-type-paths", r.GetRelationTypePathsByEx)

		// Concept groups.
		apiV1.POST("/knowledge-networks/:kn_id/concept-groups", r.verifyJsonContentType(), r.CreateConceptGroupByEx)
		apiV1.POST("/knowledge-networks/:kn_id/concept-groups/validation", r.verifyJsonContentType(), r.ValidateConceptGroupsByEx)
		apiV1.DELETE("/knowledge-networks/:kn_id/concept-groups/:cg_id", r.DeleteConceptGroup) // Batch deletion is not supported.
		apiV1.PUT("/knowledge-networks/:kn_id/concept-groups/:cg_id", r.verifyJsonContentType(), r.UpdateConceptGroupByEx)
		apiV1.GET("/knowledge-networks/:kn_id/concept-groups", r.ListConceptGroupsByEx)
		apiV1.GET("/knowledge-networks/:kn_id/concept-groups/:cg_id", r.GetConceptGroupByEx)
		apiV1.POST("/knowledge-networks/:kn_id/concept-groups/:cg_id/object-types", r.AddObjectTypesToConceptGroupByEx)
		apiV1.DELETE("/knowledge-networks/:kn_id/concept-groups/:cg_id/object-types/:ot_ids", r.DeleteObjectTypesFromGroupByEx)

		// Object types.
		apiV1.POST("/knowledge-networks/:kn_id/object-types", r.verifyJsonContentType(), r.HandleObjectTypeGetOverrideByEx)
		apiV1.POST("/knowledge-networks/:kn_id/object-types/validation", r.verifyJsonContentType(), r.ValidateObjectTypesByEx)
		apiV1.DELETE("/knowledge-networks/:kn_id/object-types/:ot_ids", r.DeleteObjectTypes) // The path uses the plural parameter name but accepts one ID only.
		apiV1.PUT("/knowledge-networks/:kn_id/object-types/:ot_id", r.verifyJsonContentType(), r.UpdateObjectTypeByEx)
		apiV1.PUT("/knowledge-networks/:kn_id/object-types/:ot_id/data_properties/:property_names", r.verifyJsonContentType(), r.UpdateDataProperties)
		apiV1.GET("/knowledge-networks/:kn_id/object-types", r.ListObjectTypesByEx) // The path uses the plural parameter name but accepts one ID only.
		apiV1.GET("/knowledge-networks/:kn_id/object-types/:ot_ids/sample-data", r.GetObjectTypeSampleDataByEx)
		apiV1.GET("/knowledge-networks/:kn_id/object-types/:ot_ids", r.GetObjectTypesByEx) // The path uses the plural parameter name but accepts one ID only.

		// Relation types.
		apiV1.POST("/knowledge-networks/:kn_id/relation-types", r.verifyJsonContentType(), r.HandleRelationTypeGetOverrideByEx)
		apiV1.POST("/knowledge-networks/:kn_id/relation-types/validation", r.verifyJsonContentType(), r.ValidateRelationTypesByEx)
		apiV1.DELETE("/knowledge-networks/:kn_id/relation-types/:rt_ids", r.DeleteRelationTypes)
		apiV1.PUT("/knowledge-networks/:kn_id/relation-types/:rt_id", r.verifyJsonContentType(), r.UpdateRelationTypeByEx)
		apiV1.GET("/knowledge-networks/:kn_id/relation-types", r.ListRelationTypesByEx)
		apiV1.GET("/knowledge-networks/:kn_id/relation-types/:rt_ids", r.GetRelationTypesByEx)

		// Action types.
		apiV1.POST("/knowledge-networks/:kn_id/action-types", r.verifyJsonContentType(), r.HandleActionTypeGetOverrideByEx)
		apiV1.POST("/knowledge-networks/:kn_id/action-types/validation", r.verifyJsonContentType(), r.ValidateActionTypesByEx)
		apiV1.DELETE("/knowledge-networks/:kn_id/action-types/:at_ids", r.DeleteActionTypes)
		apiV1.PUT("/knowledge-networks/:kn_id/action-types/:at_id", r.verifyJsonContentType(), r.UpdateActionTypeByEx)
		apiV1.GET("/knowledge-networks/:kn_id/action-types", r.ListActionTypesByEx)
		apiV1.GET("/knowledge-networks/:kn_id/action-types/:at_ids", r.GetActionTypesByEx)

		// Metrics.
		apiV1.POST("/knowledge-networks/:kn_id/metrics", r.verifyJsonContentType(), r.HandleMetricGetOverrideByEx)
		apiV1.POST("/knowledge-networks/:kn_id/metrics/validation", r.verifyJsonContentType(), r.ValidateMetricsByEx)
		// SDK/CLI compatibility alias (@openbkn/bkn-sdk uses /metrics/validate)
		apiV1.POST("/knowledge-networks/:kn_id/metrics/validate", r.verifyJsonContentType(), r.ValidateMetricsByEx)
		apiV1.DELETE("/knowledge-networks/:kn_id/metrics/:metric_ids", r.DeleteMetricsByIDsByEx)
		apiV1.PUT("/knowledge-networks/:kn_id/metrics/:metric_ids", r.verifyJsonContentType(), r.UpdateMetricByEx)
		apiV1.GET("/knowledge-networks/:kn_id/metrics", r.ListMetricsByEx)
		apiV1.GET("/knowledge-networks/:kn_id/metrics/:metric_ids", r.GetMetricsByIDsByEx)

		// Risk types.
		apiV1.POST("/knowledge-networks/:kn_id/risk-types", r.verifyJsonContentType(), r.HandleRiskTypeGetOverrideByEx)
		apiV1.DELETE("/knowledge-networks/:kn_id/risk-types/:rt_ids", r.DeleteRiskTypes)
		apiV1.PUT("/knowledge-networks/:kn_id/risk-types/:rt_id", r.verifyJsonContentType(), r.UpdateRiskTypeByEx)
		apiV1.GET("/knowledge-networks/:kn_id/risk-types", r.ListRiskTypesByEx)
		apiV1.GET("/knowledge-networks/:kn_id/risk-types/:rt_ids", r.GetRiskTypesByEx)

		// Action schedule management.
		apiV1.POST("/knowledge-networks/:kn_id/action-schedules", r.verifyJsonContentType(), r.CreateActionScheduleByEx)
		apiV1.DELETE("/knowledge-networks/:kn_id/action-schedules/:schedule_ids", r.DeleteActionSchedulesByEx)
		apiV1.PUT("/knowledge-networks/:kn_id/action-schedules/:schedule_id", r.verifyJsonContentType(), r.UpdateActionScheduleByEx)
		apiV1.PUT("/knowledge-networks/:kn_id/action-schedules/:schedule_id/status", r.verifyJsonContentType(), r.UpdateActionScheduleStatusByEx)
		apiV1.GET("/knowledge-networks/:kn_id/action-schedules", r.ListActionSchedulesByEx)
		apiV1.GET("/knowledge-networks/:kn_id/action-schedules/:schedule_id", r.GetActionScheduleByEx)

		// Knowledge network resource example list.
		apiV1.GET("/resources", r.ListResources)

		// BKN import and export (RESTful design).
		apiV1.POST("/bkns", r.UploadBKN)         // Upload a BKN tar archive for import.
		apiV1.GET("/bkns/:kn_id", r.DownloadBKN) // Download a BKN tar archive for export.
	}

	bknApiInV1 := c.Group("/api/bkn-backend/in/v1")
	otlApiInV1 := c.Group("/api/ontology-manager/in/v1")
	bknApiInV1.Use(rest.PrivateNoCacheMiddleware())
	otlApiInV1.Use(rest.PrivateNoCacheMiddleware())
	bknApiInV1.GET("/trace/projection/knowledge-networks/:kn_id", r.GetKNByProjectionGrant)
	bknApiInV1.GET("/knowledge-networks/:kn_id/proxy-account", r.GetKNProxy)
	bknApiInV1.GET("/knowledge-networks/:kn_id/proxy-account/plan", r.PlanKNProxySync)
	bknApiInV1.POST("/knowledge-networks/:kn_id/proxy-account/sync", r.RetryKNProxySync)
	bknApiInV1.POST("/knowledge-networks/:kn_id/proxy-account/deletion/finalize", r.FinalizeKNProxyDeletion)
	bknApiInV1.POST("/proxy-accounts/reconcile", r.ReconcileKNProxies)

	for _, apiInV1 := range []*gin.RouterGroup{bknApiInV1, otlApiInV1} {
		// Knowledge networks.
		apiInV1.POST("/knowledge-networks", r.verifyJsonContentType(), r.CreateKNByIn)
		// Resolve names by ID in batch for object-level authorization views; the names segment does not conflict with :kn_id.
		apiInV1.POST("/knowledge-networks/names", r.verifyJsonContentType(), r.QueryKNNamesByIDs)
		apiInV1.PUT("/knowledge-networks/:kn_id", r.verifyJsonContentType(), r.UpdateKNByIn)
		apiInV1.GET("/knowledge-networks", r.ListKNsByIn)
		apiInV1.GET("/knowledge-networks/:kn_id", r.GetKNByIn)
		apiInV1.POST("/knowledge-networks/:kn_id/validation", r.verifyJsonContentType(), r.ValidateKNByIn)
		apiInV1.POST("/knowledge-networks/:kn_id/relation-type-paths", r.GetRelationTypePathsByIn)

		// Concept groups.
		apiInV1.POST("/knowledge-networks/:kn_id/concept-groups", r.verifyJsonContentType(), r.CreateConceptGroupByIn)
		apiInV1.POST("/knowledge-networks/:kn_id/concept-groups/validation", r.verifyJsonContentType(), r.ValidateConceptGroupsByIn)
		apiInV1.PUT("/knowledge-networks/:kn_id/concept-groups/:cg_id", r.verifyJsonContentType(), r.UpdateConceptGroupByIn)
		apiInV1.GET("/knowledge-networks/:kn_id/concept-groups", r.ListConceptGroupsByIn)
		apiInV1.GET("/knowledge-networks/:kn_id/concept-groups/:cg_id", r.GetConceptGroupByIn)
		apiInV1.POST("/knowledge-networks/:kn_id/concept-groups/:cg_id/object-types", r.AddObjectTypesToConceptGroupByIn)
		apiInV1.DELETE("/knowledge-networks/:kn_id/concept-groups/:cg_id/object-types/:ot_ids", r.DeleteObjectTypesFromGroupByIn)

		// Object types.
		apiInV1.POST("/knowledge-networks/:kn_id/object-types", r.verifyJsonContentType(), r.HandleObjectTypeGetOverrideByIn)
		apiInV1.POST("/knowledge-networks/:kn_id/object-types/validation", r.verifyJsonContentType(), r.ValidateObjectTypesByIn)
		apiInV1.PUT("/knowledge-networks/:kn_id/object-types/:ot_id", r.verifyJsonContentType(), r.UpdateObjectTypeByIn)
		apiInV1.GET("/knowledge-networks/:kn_id/object-types", r.ListObjectTypesByIn)
		apiInV1.GET("/knowledge-networks/:kn_id/object-types/:ot_ids/sample-data", r.GetObjectTypeSampleDataByIn)
		apiInV1.GET("/knowledge-networks/:kn_id/object-types/:ot_ids", r.GetObjectTypesByIn) // The path uses the plural parameter name but accepts one ID only.

		// Relation types.
		apiInV1.POST("/knowledge-networks/:kn_id/relation-types", r.verifyJsonContentType(), r.HandleRelationTypeGetOverrideByIn)
		apiInV1.POST("/knowledge-networks/:kn_id/relation-types/validation", r.verifyJsonContentType(), r.ValidateRelationTypesByIn)
		apiInV1.PUT("/knowledge-networks/:kn_id/relation-types/:rt_id", r.verifyJsonContentType(), r.UpdateRelationTypeByIn)
		apiInV1.GET("/knowledge-networks/:kn_id/relation-types", r.ListRelationTypesByIn)
		apiInV1.GET("/knowledge-networks/:kn_id/relation-types/:rt_ids", r.GetRelationTypesByIn)

		// Action types.
		apiInV1.POST("/knowledge-networks/:kn_id/action-types", r.verifyJsonContentType(), r.HandleActionTypeGetOverrideByIn)
		apiInV1.POST("/knowledge-networks/:kn_id/action-types/validation", r.verifyJsonContentType(), r.ValidateActionTypesByIn)
		apiInV1.PUT("/knowledge-networks/:kn_id/action-types/:at_id", r.verifyJsonContentType(), r.UpdateActionTypeByIn)
		apiInV1.GET("/knowledge-networks/:kn_id/action-types", r.ListActionTypesByIn)
		apiInV1.GET("/knowledge-networks/:kn_id/action-types/:at_ids", r.GetActionTypesByIn)

		// Metrics (internal).
		apiInV1.POST("/knowledge-networks/:kn_id/metrics", r.verifyJsonContentType(), r.HandleMetricGetOverrideByIn)
		apiInV1.POST("/knowledge-networks/:kn_id/metrics/validation", r.verifyJsonContentType(), r.ValidateMetricsByIn)
		// SDK/CLI compatibility alias (@openbkn/bkn-sdk uses /metrics/validate)
		apiInV1.POST("/knowledge-networks/:kn_id/metrics/validate", r.verifyJsonContentType(), r.ValidateMetricsByIn)
		apiInV1.DELETE("/knowledge-networks/:kn_id/metrics/:metric_ids", r.DeleteMetricsByIDsByIn)
		apiInV1.PUT("/knowledge-networks/:kn_id/metrics/:metric_ids", r.verifyJsonContentType(), r.UpdateMetricByIn)
		apiInV1.GET("/knowledge-networks/:kn_id/metrics", r.ListMetricsByIn)
		apiInV1.GET("/knowledge-networks/:kn_id/metrics/:metric_ids", r.GetMetricsByIDsByIn)

		// Risk types (internal); GetRiskTypesByIn supports the risk_type_ids query parameter.
		apiInV1.POST("/knowledge-networks/:kn_id/risk-types", r.verifyJsonContentType(), r.HandleRiskTypeGetOverrideByIn)
		apiInV1.PUT("/knowledge-networks/:kn_id/risk-types/:rt_id", r.verifyJsonContentType(), r.UpdateRiskTypeByIn)
		apiInV1.GET("/knowledge-networks/:kn_id/risk-types", r.GetRiskTypesByIn)
		apiInV1.GET("/knowledge-networks/:kn_id/risk-types/:rt_ids", r.GetRiskTypesByInWithPath)
		apiInV1.DELETE("/knowledge-networks/:kn_id/risk-types/:rt_ids", r.DeleteRiskTypes)

		// Action schedule management.
		apiInV1.POST("/knowledge-networks/:kn_id/action-schedules", r.verifyJsonContentType(), r.CreateActionScheduleByIn)
		apiInV1.DELETE("/knowledge-networks/:kn_id/action-schedules/:schedule_ids", r.DeleteActionSchedulesByIn)
		apiInV1.PUT("/knowledge-networks/:kn_id/action-schedules/:schedule_id", r.verifyJsonContentType(), r.UpdateActionScheduleByIn)
		apiInV1.PUT("/knowledge-networks/:kn_id/action-schedules/:schedule_id/status", r.verifyJsonContentType(), r.UpdateActionScheduleStatusByIn)
		apiInV1.GET("/knowledge-networks/:kn_id/action-schedules", r.ListActionSchedulesByIn)
		apiInV1.GET("/knowledge-networks/:kn_id/action-schedules/:schedule_id", r.GetActionScheduleByIn)

	}

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

// verifyJsonContentType middleware
func (r *restHandler) verifyJsonContentType() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Reject requests whose Content-Type is not application/json.
		if c.ContentType() != interfaces.CONTENT_TYPE_JSON {
			httpErr := rest.NewHTTPError(c, http.StatusNotAcceptable, berrors.BknBackend_InvalidRequestHeader_ContentType).
				WithErrorDetails(commonValidationDetail(c.Request.Context(), "ContentTypeUnsupported", map[string]any{"contentType": c.ContentType()}))
			rest.ReplyError(c, httpErr)

			c.Abort()
			return
		}

		// Continue with the next handler.
		c.Next()
	}
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
		httpErr := rest.NewHTTPError(ctx, http.StatusUnauthorized, rest.PublicError_Unauthorized).
			WithErrorDetails(commonValidationDetail(ctx, "AuthenticationFailed", nil))
		rest.ReplyError(c, httpErr)
		return visitor, err
	}
	c.Set(operationAuditVisitorKey, visitor)

	return visitor, nil
}
