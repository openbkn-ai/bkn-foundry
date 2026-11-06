// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common/visitor"
	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/auth"
)

// knEventBodyLimit bounds a request body: a rule check may carry up to
// KnEventRuleHitsMax hits with their attributes.
const knEventBodyLimit = 16 << 20

// RegisterKnEventRoutes mounts the public and internal knowledge-network event
// API around an implementation supplied during paid-image assembly. Community
// binaries never call it, so they expose no event routes. The internal change
// feed carries no caller identity: it is reachable only inside the cluster.
func RegisterKnEventRoutes(
	engine *gin.Engine,
	appSetting *common.AppSetting,
	service interfaces.KnEventServiceV1,
	entitlementMiddleware gin.HandlerFunc,
) {
	handler := &restHandler{
		appSetting: appSetting,
		as:         auth.NewAuthService(appSetting),
		kes:        service,
	}
	public := engine.Group("/api/bkn-backend/v1/knowledge-networks/:kn_id")
	internal := engine.Group("/api/bkn-backend/in/v1/knowledge-networks/:kn_id")
	for _, group := range []*gin.RouterGroup{public, internal} {
		group.Use(rest.PrivateNoCacheMiddleware())
		if entitlementMiddleware != nil {
			group.Use(entitlementMiddleware)
		}
	}
	routes := []struct {
		method string
		path   string
		handle func(*gin.Context, context.Context)
	}{
		{http.MethodGet, "/event-types", handler.listKnEventTypes},
		{http.MethodPost, "/event-types", handler.createKnEventType},
		{http.MethodGet, "/event-types/:code", handler.getKnEventType},
		{http.MethodPut, "/event-types/:code", handler.updateKnEventType},
		{http.MethodDelete, "/event-types/:code", handler.deleteKnEventType},
		{http.MethodGet, "/events", handler.listKnEvents},
		{http.MethodPost, "/events", handler.reportKnEvent},
		{http.MethodGet, "/events/:event_id", handler.getKnEvent},
		{http.MethodPost, "/events/:event_id/:action", handler.changeKnEvent},
		{http.MethodGet, "/rule-checks", handler.listKnEventRuleChecks},
		{http.MethodPost, "/rule-checks", handler.recordKnEventRuleCheck},
	}
	for _, route := range routes {
		public.Handle(route.method, route.path, handler.knEventExternal(route.handle))
		internal.Handle(route.method, route.path, knEventInternal(route.handle))
	}
	internal.GET("/event-changes", handler.listKnEventChanges)
}

// knEventExternal authenticates the bearer token and passes on a context
// carrying the caller's account and language.
func (r *restHandler) knEventExternal(next func(*gin.Context, context.Context)) gin.HandlerFunc {
	return func(c *gin.Context) {
		vis, err := r.verifyOAuth(i18n.GetLanguageCtx(c), c)
		if err != nil {
			return
		}
		next(c, knEventContext(c, vis))
	}
}

// knEventInternal trusts the account headers set by cluster callers.
func knEventInternal(next func(*gin.Context, context.Context)) gin.HandlerFunc {
	return func(c *gin.Context) {
		next(c, knEventContext(c, visitor.GenerateVisitor(c)))
	}
}

func knEventContext(c *gin.Context, vis hydra.Visitor) context.Context {
	return context.WithValue(i18n.GetLanguageCtx(c), interfaces.ACCOUNT_INFO_KEY,
		interfaces.AccountInfo{ID: vis.ID, Type: string(vis.Type)})
}

func replyKnEventError(c *gin.Context, err error) {
	var httpErr *httperrors.HTTPError
	if errors.As(err, &httpErr) {
		rest.ReplyError(c, httpErr)
		return
	}
	rest.ReplyError(c, httperrors.NewHTTPError(i18n.GetLanguageCtx(c), http.StatusInternalServerError,
		berrors.BknBackend_KnEvent_InternalError).WithErrorDetails(err.Error()))
}

// bindKnEventBody decodes a JSON body, answering 413 past the size limit and
// 400 for anything that does not decode.
func bindKnEventBody(c *gin.Context, target any) bool {
	return decodeKnEventBody(c, target, false)
}

// decodeKnEventBody decodes a JSON body; an empty body is accepted when optional.
// An unknown field is refused, so a misspelt one is not stored as its default.
func decodeKnEventBody(c *gin.Context, target any, optional bool) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, knEventBodyLimit)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		if optional && errors.Is(err, io.EOF) {
			return true
		}
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			replyKnEventError(c, httperrors.NewHTTPError(i18n.GetLanguageCtx(c), http.StatusRequestEntityTooLarge,
				berrors.BknBackend_KnEvent_TooLarge))
			return false
		}
		replyKnEventError(c, httperrors.NewHTTPError(i18n.GetLanguageCtx(c), http.StatusBadRequest,
			berrors.BknBackend_KnEvent_InvalidParameter).WithErrorDetails(err.Error()))
		return false
	}
	return true
}

func splitKnEventList(raw string) []string {
	var values []string
	for _, value := range strings.Split(raw, ",") {
		if value = strings.TrimSpace(value); value != "" {
			values = append(values, value)
		}
	}
	return values
}

func knEventIntQuery(c *gin.Context, name string) (int64, bool) {
	raw := c.Query(name)
	if raw == "" {
		return 0, true
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		replyKnEventError(c, httperrors.NewHTTPError(i18n.GetLanguageCtx(c), http.StatusBadRequest,
			berrors.BknBackend_KnEvent_InvalidParameter).WithErrorDetails(name+" must be a non-negative integer"))
		return 0, false
	}
	return value, true
}

func (r *restHandler) listKnEventTypes(c *gin.Context, ctx context.Context) {
	types, err := r.kes.ListEventTypes(ctx, c.Param("kn_id"))
	if err != nil {
		replyKnEventError(c, err)
		return
	}
	if types == nil {
		types = []interfaces.KnEventType{}
	}
	rest.ReplyOK(c, http.StatusOK, gin.H{"entries": types, "total": len(types)})
}

func (r *restHandler) createKnEventType(c *gin.Context, ctx context.Context) {
	var input interfaces.KnEventTypeInput
	if !bindKnEventBody(c, &input) {
		return
	}
	eventType, err := r.kes.CreateEventType(ctx, c.Param("kn_id"), input)
	if err != nil {
		replyKnEventError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusCreated, eventType)
}

func (r *restHandler) getKnEventType(c *gin.Context, ctx context.Context) {
	eventType, err := r.kes.GetEventType(ctx, c.Param("kn_id"), c.Param("code"))
	if err != nil {
		replyKnEventError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, eventType)
}

func (r *restHandler) updateKnEventType(c *gin.Context, ctx context.Context) {
	var input interfaces.KnEventTypeInput
	if !bindKnEventBody(c, &input) {
		return
	}
	eventType, err := r.kes.UpdateEventType(ctx, c.Param("kn_id"), c.Param("code"), input)
	if err != nil {
		replyKnEventError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, eventType)
}

func (r *restHandler) deleteKnEventType(c *gin.Context, ctx context.Context) {
	if err := r.kes.DeleteEventType(ctx, c.Param("kn_id"), c.Param("code")); err != nil {
		replyKnEventError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (r *restHandler) listKnEvents(c *gin.Context, ctx context.Context) {
	limit, ok := knEventIntQuery(c, "limit")
	if !ok {
		return
	}
	list, err := r.kes.ListEvents(ctx, c.Param("kn_id"), interfaces.KnEventFilter{
		EventTypes: splitKnEventList(c.Query("event_type")),
		Statuses:   splitKnEventList(c.Query("status")),
		ObjectKey:  c.Query("object_key"),
		Cursor:     c.Query("cursor"),
		Limit:      int(limit),
	})
	if err != nil {
		replyKnEventError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, list)
}

func (r *restHandler) reportKnEvent(c *gin.Context, ctx context.Context) {
	var input interfaces.KnEventReportInput
	if !bindKnEventBody(c, &input) {
		return
	}
	result, err := r.kes.ReportEvent(ctx, c.Param("kn_id"), input)
	if err != nil {
		replyKnEventError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusCreated, result)
}

func (r *restHandler) getKnEvent(c *gin.Context, ctx context.Context) {
	event, err := r.kes.GetEvent(ctx, c.Param("kn_id"), c.Param("event_id"))
	if err != nil {
		replyKnEventError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, event)
}

func (r *restHandler) changeKnEvent(c *gin.Context, ctx context.Context) {
	action := c.Param("action")
	switch action {
	case interfaces.KnEventActionVerify, interfaces.KnEventActionReject,
		interfaces.KnEventActionClose, interfaces.KnEventActionReopen:
	default:
		rest.ReplyError(c, httperrors.NewHTTPError(ctx, http.StatusNotFound, httperrors.PublicError_NotFound))
		return
	}
	var input interfaces.KnEventReasonInput
	if !decodeKnEventBody(c, &input, true) {
		return
	}
	result, err := r.kes.ChangeEvent(ctx, c.Param("kn_id"), c.Param("event_id"), action, input)
	if err != nil {
		replyKnEventError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, result)
}

func (r *restHandler) listKnEventRuleChecks(c *gin.Context, ctx context.Context) {
	checks, err := r.kes.ListRuleChecks(ctx, c.Param("kn_id"))
	if err != nil {
		replyKnEventError(c, err)
		return
	}
	if checks == nil {
		checks = []interfaces.KnEventRuleCheck{}
	}
	rest.ReplyOK(c, http.StatusOK, gin.H{"entries": checks, "total": len(checks)})
}

func (r *restHandler) recordKnEventRuleCheck(c *gin.Context, ctx context.Context) {
	var input interfaces.KnEventRuleCheckInput
	if !bindKnEventBody(c, &input) {
		return
	}
	result, err := r.kes.RecordRuleCheck(ctx, c.Param("kn_id"), input)
	if err != nil {
		replyKnEventError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, result)
}

func (r *restHandler) listKnEventChanges(c *gin.Context) {
	after, ok := knEventIntQuery(c, "after")
	if !ok {
		return
	}
	limit, ok := knEventIntQuery(c, "limit")
	if !ok {
		return
	}
	list, err := r.kes.ListChanges(i18n.GetLanguageCtx(c), c.Param("kn_id"), after, int(limit))
	if err != nil {
		replyKnEventError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, list)
}
