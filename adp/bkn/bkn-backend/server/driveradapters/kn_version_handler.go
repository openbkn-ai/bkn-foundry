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
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics"
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

// disableKNVersioning is the operator's route that takes a network out of versioning.
func (r *restHandler) disableKNVersioning(c *gin.Context, ctx context.Context) {
	var req interfaces.KNDisableVersioningRequest
	if !bindKNVersionBody(c, &req) {
		return
	}
	state, err := r.kvs.Disable(ctx, c.Param("kn_id"), &req)
	if err != nil {
		replyKNVersionError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, state)
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

// draftRevisionWriter adds X-Kn-Draft-Revision to the response once a draft write has
// committed, just before the status line is written.
type draftRevisionWriter struct {
	gin.ResponseWriter
	dw *logics.DraftWrite
}

func (w *draftRevisionWriter) WriteHeader(code int) {
	if w.dw.Written && code < http.StatusMultipleChoices {
		w.Header().Set("X-Kn-Draft-Revision", strconv.FormatInt(w.dw.Revision, 10))
	}
	w.ResponseWriter.WriteHeader(code)
}

func optionalInt64Query(c *gin.Context, name string) (*int64, bool) {
	raw := c.Query(name)
	if raw == "" {
		return nil, true
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		knVersionBadRequest(c, name+" must be a non-negative integer")
		return nil, false
	}
	return &value, true
}

// resolveBranch maps the branch and kn_version parameters of a request to the storage
// branch, replying with the error when it fails. A write gets a context that carries its
// expected_draft_revision; a read of a versioned network gets the X-Kn-Version header.
// writePrecondition is what a write to the draft of a versioned network must carry (design
// section 10). Unversioned networks accept the same preconditions but never require them.
type writePrecondition int

const (
	// preconditionNone: creating definitions, or changing only the network's basic information.
	preconditionNone writePrecondition = iota
	// preconditionIfMatch: changing one definition.
	preconditionIfMatch
	// preconditionDelete: deleting definitions, by If-Match for one or the draft revision for several.
	preconditionDelete
	// preconditionDraftRevision: changing the draft as a whole, such as an import.
	preconditionDraftRevision
)

// preconditionByMethod is the precondition of a write to one kind of definition.
func preconditionByMethod(method string) writePrecondition {
	switch method {
	case http.MethodPut, http.MethodPatch:
		return preconditionIfMatch
	case http.MethodDelete:
		return preconditionDelete
	default:
		return preconditionNone
	}
}

// ifMatchRevision returns the revision an If-Match header names, without quotes or a weak prefix.
func ifMatchRevision(header string) string {
	value := strings.TrimSpace(header)
	value = strings.TrimPrefix(value, "W/")
	return strings.Trim(value, `"`)
}

func (r *restHandler) resolveBranch(ctx *context.Context, c *gin.Context, knID string, write bool) (*interfaces.KNResolvedBranch, bool) {
	if write {
		return r.resolveWrite(ctx, c, knID, preconditionByMethod(c.Request.Method))
	}
	return r.resolveRead(ctx, c, knID, false)
}

// resolveWrite resolves the branch of a write, records its preconditions in ctx for the write
// transaction, and on a versioned network refuses with 428 a request that lacks the one need.
func (r *restHandler) resolveWrite(ctx *context.Context, c *gin.Context, knID string, need writePrecondition) (*interfaces.KNResolvedBranch, bool) {
	resolved, ok := r.resolveRead(ctx, c, knID, true)
	if !ok {
		return nil, false
	}
	expected, ok := optionalInt64Query(c, "expected_draft_revision")
	if !ok {
		return nil, false
	}
	var dw *logics.DraftWrite
	*ctx, dw = logics.WithDraftWrite(*ctx, expected)
	dw.IfMatch = ifMatchRevision(c.GetHeader("If-Match"))
	if resolved.Versioned {
		missing := ""
		switch need {
		case preconditionIfMatch:
			if dw.IfMatch == "" {
				missing = "If-Match is required"
			}
		case preconditionDelete:
			if dw.IfMatch == "" && expected == nil {
				missing = "If-Match or expected_draft_revision is required"
			}
		case preconditionDraftRevision:
			if expected == nil {
				missing = "expected_draft_revision is required"
			}
		}
		if missing != "" {
			replyKNVersionError(c, httperrors.NewHTTPError(*ctx, http.StatusPreconditionRequired,
				berrors.BknBackend_KNVersion_PreconditionRequired).WithErrorDetails(missing))
			return nil, false
		}
		c.Writer = &draftRevisionWriter{ResponseWriter: c.Writer, dw: dw}
	}
	return resolved, true
}

func (r *restHandler) resolveRead(ctx *context.Context, c *gin.Context, knID string, write bool) (*interfaces.KNResolvedBranch, bool) {
	req := interfaces.KNBranchRequest{Branch: c.Query("branch"), KNVersion: c.Query("kn_version"), Write: write}
	var ok bool
	if req.DraftRevision, ok = optionalInt64Query(c, "draft_revision"); !ok {
		return nil, false
	}
	if r.kvs == nil {
		branch := req.Branch
		if branch == "" {
			branch = interfaces.MAIN_BRANCH
		}
		return &interfaces.KNResolvedBranch{Branch: branch}, true
	}
	resolved, err := r.kvs.ResolveBranch(*ctx, knID, req)
	if err != nil {
		replyKNVersionError(c, err)
		return nil, false
	}
	if write {
		return resolved, true
	}
	if resolved.Version != "" {
		c.Header("X-Kn-Version", resolved.Version)
	}
	if resolved.Branch == interfaces.DRAFT_BRANCH && resolved.Versioned {
		c.Header("X-Kn-Draft-Revision", strconv.FormatInt(resolved.DraftRevision, 10))
	}
	return resolved, true
}

func (r *restHandler) readBranch(ctx *context.Context, c *gin.Context, knID string) (string, bool) {
	resolved, ok := r.resolveBranch(ctx, c, knID, false)
	if !ok {
		return "", false
	}
	return resolved.Branch, true
}

func (r *restHandler) writeBranch(ctx *context.Context, c *gin.Context, knID string) (string, bool) {
	return r.writeBranchNeeding(ctx, c, knID, preconditionByMethod(c.Request.Method))
}

// writeBranchNeeding is writeBranch for a write whose precondition does not follow its method.
func (r *restHandler) writeBranchNeeding(ctx *context.Context, c *gin.Context, knID string, need writePrecondition) (string, bool) {
	resolved, ok := r.resolveWrite(ctx, c, knID, need)
	if !ok {
		return "", false
	}
	return resolved.Branch, true
}

// searchBranch resolves the branch whose concept index a search reads.
func (r *restHandler) searchBranch(ctx *context.Context, c *gin.Context, knID string) (string, bool) {
	resolved, ok := r.resolveBranch(ctx, c, knID, false)
	if !ok {
		return "", false
	}
	if resolved.IndexBranch != "" {
		return resolved.IndexBranch, true
	}
	return resolved.Branch, true
}

// writeWholeDraftBranch resolves the branch of a write that replaces several definitions at
// once, such as an import: on a versioned network it needs expected_draft_revision.
func (r *restHandler) writeWholeDraftBranch(ctx *context.Context, c *gin.Context, knID string) (string, bool) {
	return r.writeBranchNeeding(ctx, c, knID, preconditionDraftRevision)
}

// replyWithRevisions sets the revision of each definition about to be returned, and, when the
// response is one definition, the ETag that a later write sends back as If-Match. A failure is
// replied and reported as false.
func replyWithRevisions[T interfaces.Revisioned](ctx context.Context, c *gin.Context, kind, knID, branch string, items []T) bool {
	if err := logics.FillRevisions(ctx, kind, knID, branch, items); err != nil {
		replyKNVersionError(c, err)
		return false
	}
	setETag(c, items)
	return true
}

// setETag sets the ETag of a response that returns one definition.
func setETag[T interfaces.Revisioned](c *gin.Context, items []T) {
	if len(items) != 1 {
		return
	}
	if revision := items[0].GetRevision(); revision != "" {
		c.Header("ETag", `"`+revision+`"`)
	}
}
