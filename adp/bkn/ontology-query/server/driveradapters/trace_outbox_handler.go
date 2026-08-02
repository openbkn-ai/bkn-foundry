// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-comm-go/hydra"
	"github.com/openbkn-ai/bkn-comm-go/rest"

	"ontology-query/common/bkntrace"
	"ontology-query/common/bkntrace/outbox"
)

type traceOutboxActionBody struct {
	ExpectedStateVersion uint64 `json:"expected_state_version"`
	ReasonCode           string `json:"reason_code"`
	ReasonNote           string `json:"reason_note"`
}

func (r *restHandler) ListTraceOutbox(c *gin.Context) {
	_, repository, ok := r.authorizeTraceOutbox(c)
	if !ok {
		return
	}
	options := outbox.ListOptions{
		Statuses:         splitQueryValues(c.QueryArray("status")),
		ProducerStreamID: c.Query("producer_stream_id"),
		EventID:          c.Query("event_id"),
		Page:             queryPositiveInt(c, "page", 1),
		PageSize:         queryPositiveInt(c, "page_size", 50),
	}
	items, err := repository.List(c.Request.Context(), options)
	if err != nil {
		replyTraceOutboxError(c, err)
		return
	}
	total, err := repository.Count(c.Request.Context(), options)
	if err != nil {
		replyTraceOutboxError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, gin.H{"entries": items, "total": total})
}

func (r *restHandler) GetTraceOutbox(c *gin.Context) {
	_, repository, ok := r.authorizeTraceOutbox(c)
	if !ok {
		return
	}
	outboxID, ok := traceOutboxID(c)
	if !ok {
		return
	}
	detail, err := repository.Get(c.Request.Context(), outboxID)
	if err != nil {
		replyTraceOutboxError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, detail)
}

func (r *restHandler) RetryTraceOutbox(c *gin.Context) {
	r.actTraceOutbox(c, func(repository *outbox.Repository, outboxID int64, request outbox.ActionRequest) (outbox.ActionResult, error) {
		return repository.Retry(c.Request.Context(), outboxID, request)
	})
}

func (r *restHandler) AbandonTraceOutbox(c *gin.Context) {
	r.actTraceOutbox(c, func(repository *outbox.Repository, outboxID int64, request outbox.ActionRequest) (outbox.ActionResult, error) {
		return repository.Abandon(c.Request.Context(), outboxID, request)
	})
}

func (r *restHandler) actTraceOutbox(c *gin.Context, action func(*outbox.Repository, int64, outbox.ActionRequest) (outbox.ActionResult, error)) {
	visitor, repository, ok := r.authorizeTraceOutbox(c)
	if !ok {
		return
	}
	outboxID, ok := traceOutboxID(c)
	if !ok {
		return
	}
	var body traceOutboxActionBody
	if err := c.ShouldBindJSON(&body); err != nil {
		replyTraceOutboxHTTPError(c, http.StatusBadRequest, rest.PublicError_BadRequest, "invalid JSON request body")
		return
	}
	result, err := action(repository, outboxID, outbox.ActionRequest{
		ExpectedStateVersion: body.ExpectedStateVersion,
		ReasonCode:           body.ReasonCode,
		ReasonNote:           body.ReasonNote,
		IdempotencyKey:       strings.TrimSpace(c.GetHeader("Idempotency-Key")),
		OperatorID:           visitor.ID,
		OperatorType:         string(visitor.Type),
	})
	if err != nil {
		replyTraceOutboxError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, result)
}

func (r *restHandler) authorizeTraceOutbox(c *gin.Context) (hydra.Visitor, *outbox.Repository, bool) {
	visitor, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return hydra.Visitor{}, nil, false
	}
	profile, err := bkntrace.ResolveAccessProfile(c.Request.Context(), c.GetHeader("Authorization"), visitor.ID)
	if err != nil {
		if errors.Is(err, bkntrace.ErrAccessProfileDenied) {
			replyTraceOutboxHTTPError(c, http.StatusForbidden, rest.PublicError_Forbidden, "Outbox management requires an active BKN Safe account")
		} else {
			replyTraceOutboxHTTPError(c, http.StatusServiceUnavailable, rest.PublicError_ServiceUnavailable, "BKN Safe access profile is unavailable")
		}
		return hydra.Visitor{}, nil, false
	}
	if !profile.IsOutboxAdmin() {
		replyTraceOutboxHTTPError(c, http.StatusForbidden, rest.PublicError_Forbidden, "Outbox management requires the admin or super_admin role")
		return hydra.Visitor{}, nil, false
	}
	repository := bkntrace.ProducerOutbox()
	if repository == nil {
		replyTraceOutboxHTTPError(c, http.StatusServiceUnavailable, rest.PublicError_ServiceUnavailable, "BKN Trace producer outbox is disabled")
		return hydra.Visitor{}, nil, false
	}
	return visitor, repository, true
}

func traceOutboxID(c *gin.Context) (int64, bool) {
	outboxID, err := strconv.ParseInt(c.Param("outbox_id"), 10, 64)
	if err != nil || outboxID < 1 {
		replyTraceOutboxHTTPError(c, http.StatusBadRequest, rest.PublicError_BadRequest, "outbox_id must be a positive integer")
		return 0, false
	}
	return outboxID, true
}

func queryPositiveInt(c *gin.Context, key string, defaultValue int) int {
	value := strings.TrimSpace(c.Query(key))
	if value == "" {
		return defaultValue
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 {
		return defaultValue
	}
	return parsed
}

func splitQueryValues(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			if part = strings.TrimSpace(part); part != "" {
				result = append(result, part)
			}
		}
	}
	return result
}

func replyTraceOutboxError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, outbox.ErrNotFound):
		replyTraceOutboxHTTPError(c, http.StatusNotFound, rest.PublicError_NotFound, "Outbox record was not found")
	case errors.Is(err, outbox.ErrStateConflict), errors.Is(err, outbox.ErrIdempotencyKeyReused):
		replyTraceOutboxHTTPError(c, http.StatusConflict, rest.PublicError_Conflict, "Outbox record changed or the idempotency key was reused")
	case errors.Is(err, outbox.ErrActionNotAllowed), errors.Is(err, outbox.ErrInvalidActionRequest):
		replyTraceOutboxHTTPError(c, http.StatusUnprocessableEntity, rest.PublicError_BadRequest, "Outbox action is not valid for the current record state")
	default:
		replyTraceOutboxHTTPError(c, http.StatusInternalServerError, rest.PublicError_InternalServerError, "Outbox operation failed")
	}
}

func replyTraceOutboxHTTPError(c *gin.Context, status int, code, detail string) {
	rest.ReplyError(c, rest.NewHTTPError(rest.GetLanguageCtx(c), status, code).WithErrorDetails(detail))
}
