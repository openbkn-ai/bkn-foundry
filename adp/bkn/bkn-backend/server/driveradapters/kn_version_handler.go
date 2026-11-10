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

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

const (
	knVersionBodyLimit         = 1 << 20
	knVersionIdempotencyKeyMax = 64
)

// registerKNVersionRoutes mounts the version API of knowledge networks.
func (r *restHandler) registerKNVersionRoutes(apiV1 *gin.RouterGroup) {
	routes := []struct {
		method string
		path   string
		handle func(*gin.Context, context.Context)
	}{
		{http.MethodPost, "/versioning", r.enableKNVersioning},
		{http.MethodGet, "/versioning", r.getKNVersioning},
		{http.MethodGet, "/versions", r.listKNVersions},
		{http.MethodPost, "/versions", r.publishKNVersion},
		{http.MethodGet, "/versions/events", r.listKNVersionEvents},
		{http.MethodGet, "/versions/:version", r.getKNVersion},
		{http.MethodPost, "/versions/:version/activate", r.activateKNVersion},
		{http.MethodPost, "/versions/:version/deprecate", r.deprecateKNVersion},
		{http.MethodPost, "/versions/:version/restore", r.restoreKNVersion},
		{http.MethodPost, "/versions/:version/purge", r.purgeKNVersion},
		{http.MethodPost, "/draft/reset", r.resetKNDraft},
	}
	group := apiV1.Group("/knowledge-networks/:kn_id")
	for _, route := range routes {
		group.Handle(route.method, route.path, r.knEventExternal(route.handle))
	}
}

func replyKNVersionError(c *gin.Context, err error) {
	var httpErr *httperrors.HTTPError
	if errors.As(err, &httpErr) {
		rest.ReplyError(c, httpErr)
		return
	}
	rest.ReplyError(c, httperrors.NewHTTPError(i18n.GetLanguageCtx(c), http.StatusInternalServerError,
		berrors.BknBackend_KNVersion_InternalError).WithErrorDetails(err.Error()))
}

func knVersionBadRequest(c *gin.Context, detail string) {
	replyKNVersionError(c, httperrors.NewHTTPError(i18n.GetLanguageCtx(c), http.StatusBadRequest,
		berrors.BknBackend_KNVersion_InvalidParameter).WithErrorDetails(detail))
}

// bindKNVersionBody decodes an optional JSON body; unknown fields are refused.
func bindKNVersionBody(c *gin.Context, target any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, knVersionBodyLimit)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil && !errors.Is(err, io.EOF) {
		knVersionBadRequest(c, err.Error())
		return false
	}
	return true
}

func idempotencyKey(c *gin.Context) (string, bool) {
	key := c.GetHeader("Idempotency-Key")
	if len(key) > knVersionIdempotencyKeyMax {
		knVersionBadRequest(c, "Idempotency-Key is longer than 64 characters")
		return "", false
	}
	return key, true
}

func versionParam(c *gin.Context) (int, bool) {
	version, err := strconv.Atoi(c.Param("version"))
	if err != nil || version <= 0 {
		replyKNVersionError(c, httperrors.NewHTTPError(i18n.GetLanguageCtx(c), http.StatusBadRequest,
			berrors.BknBackend_KNVersion_InvalidVersion).WithErrorDetails("version must be a positive integer"))
		return 0, false
	}
	return version, true
}

func pageParams(c *gin.Context) (offset, limit int, ok bool) {
	offset, limit = 0, 20
	for name, target := range map[string]*int{"offset": &offset, "limit": &limit} {
		raw := c.Query(name)
		if raw == "" {
			continue
		}
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 || (name == "limit" && (value == 0 || value > 1000)) {
			knVersionBadRequest(c, name+" is out of range")
			return 0, 0, false
		}
		*target = value
	}
	return offset, limit, true
}

func (r *restHandler) enableKNVersioning(c *gin.Context, ctx context.Context) {
	var req interfaces.KNEnableVersioningRequest
	if !bindKNVersionBody(c, &req) {
		return
	}
	var ok bool
	if req.IdempotencyKey, ok = idempotencyKey(c); !ok {
		return
	}
	state, err := r.kvs.Enable(ctx, c.Param("kn_id"), &req)
	if err != nil {
		replyKNVersionError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusCreated, state)
}

func (r *restHandler) getKNVersioning(c *gin.Context, ctx context.Context) {
	state, err := r.kvs.GetState(ctx, c.Param("kn_id"))
	if err != nil {
		replyKNVersionError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, state)
}

func (r *restHandler) listKNVersions(c *gin.Context, ctx context.Context) {
	offset, limit, ok := pageParams(c)
	if !ok {
		return
	}
	query := &interfaces.KNVersionsQuery{Statuses: splitKnEventList(c.Query("status")), Offset: offset, Limit: limit}
	versions, total, err := r.kvs.ListVersions(ctx, c.Param("kn_id"), query)
	if err != nil {
		replyKNVersionError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, gin.H{"entries": versions, "total_count": total})
}

func (r *restHandler) getKNVersion(c *gin.Context, ctx context.Context) {
	version, ok := versionParam(c)
	if !ok {
		return
	}
	v, err := r.kvs.GetVersion(ctx, c.Param("kn_id"), version)
	if err != nil {
		replyKNVersionError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, v)
}

func (r *restHandler) publishKNVersion(c *gin.Context, ctx context.Context) {
	var req interfaces.KNPublishRequest
	if !bindKNVersionBody(c, &req) {
		return
	}
	var ok bool
	if req.IdempotencyKey, ok = idempotencyKey(c); !ok {
		return
	}
	result, err := r.kvs.Publish(ctx, c.Param("kn_id"), &req)
	if err != nil {
		replyKNVersionError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusCreated, result)
}

func (r *restHandler) activateKNVersion(c *gin.Context, ctx context.Context) {
	version, ok := versionParam(c)
	if !ok {
		return
	}
	var req interfaces.KNActivateRequest
	if !bindKNVersionBody(c, &req) {
		return
	}
	if req.IdempotencyKey, ok = idempotencyKey(c); !ok {
		return
	}
	state, err := r.kvs.Activate(ctx, c.Param("kn_id"), version, &req)
	if err != nil {
		replyKNVersionError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, state)
}

func (r *restHandler) resetKNDraft(c *gin.Context, ctx context.Context) {
	var req interfaces.KNResetDraftRequest
	if !bindKNVersionBody(c, &req) {
		return
	}
	var ok bool
	if req.IdempotencyKey, ok = idempotencyKey(c); !ok {
		return
	}
	state, err := r.kvs.ResetDraft(ctx, c.Param("kn_id"), &req)
	if err != nil {
		replyKNVersionError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, state)
}

func (r *restHandler) changeKNVersionStatus(c *gin.Context, ctx context.Context,
	change func(context.Context, string, int, *interfaces.KNVersionStatusRequest) (*interfaces.KNVersion, error)) {
	version, ok := versionParam(c)
	if !ok {
		return
	}
	var req interfaces.KNVersionStatusRequest
	if !bindKNVersionBody(c, &req) {
		return
	}
	if req.IdempotencyKey, ok = idempotencyKey(c); !ok {
		return
	}
	v, err := change(ctx, c.Param("kn_id"), version, &req)
	if err != nil {
		replyKNVersionError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, v)
}

func (r *restHandler) deprecateKNVersion(c *gin.Context, ctx context.Context) {
	r.changeKNVersionStatus(c, ctx, r.kvs.Deprecate)
}

func (r *restHandler) restoreKNVersion(c *gin.Context, ctx context.Context) {
	r.changeKNVersionStatus(c, ctx, r.kvs.Restore)
}

func (r *restHandler) purgeKNVersion(c *gin.Context, ctx context.Context) {
	r.changeKNVersionStatus(c, ctx, r.kvs.Purge)
}

func (r *restHandler) listKNVersionEvents(c *gin.Context, ctx context.Context) {
	offset, limit, ok := pageParams(c)
	if !ok {
		return
	}
	events, total, err := r.kvs.ListEvents(ctx, c.Param("kn_id"), offset, limit)
	if err != nil {
		replyKNVersionError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, gin.H{"entries": events, "total_count": total})
}
