// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	attr "go.opentelemetry.io/otel/attribute"

	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
)

// KNDiffSideRequest names one side of a comparison.
type KNDiffSideRequest struct {
	KNID   string `json:"kn_id"`
	Branch string `json:"branch"`
}

// KNDiffRequestBody is the request body of the comparison endpoint.
type KNDiffRequestBody struct {
	Base   KNDiffSideRequest `json:"base"`
	Target KNDiffSideRequest `json:"target"`

	// FallbackByName pairs definitions by name once id matching is exhausted. Off by default: a
	// name match is a guess, and a wrong pair reads exactly like a real modification.
	FallbackByName bool `json:"fallback_by_name"`
}

// DiffKNsByEx compares two knowledge network branches (external endpoint).
func (r *restHandler) DiffKNsByEx(c *gin.Context) {
	vis, err := r.verifyOAuth(rest.GetLanguageCtx(c), c)
	if err != nil {
		return
	}
	r.diffKNs(c, vis)
}

// diffKNs reports what differs between two knowledge network branches.
//
// Authorization is not repeated here. Each side is loaded through the knowledge network service,
// which checks the caller against that network; a check in the handler would either duplicate it
// or, worse, disagree with it.
func (r *restHandler) diffKNs(c *gin.Context, vis hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	accountInfo := interfaces.AccountInfo{ID: vis.ID, Type: string(vis.Type)}
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, accountInfo)
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	var body KNDiffRequestBody
	if err := c.ShouldBindJSON(&body); err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_RequestBody)
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	if httpErr := validateKNDiffBody(ctx, &body); httpErr != nil {
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	span.SetAttributes(
		attr.Key("base_kn_id").String(body.Base.KNID),
		attr.Key("target_kn_id").String(body.Target.KNID),
	)

	result, err := r.bs.DiffNetworks(ctx, interfaces.KNDiffRequest{
		Base:           interfaces.KNRef{KNID: body.Base.KNID, Branch: body.Base.Branch},
		Target:         interfaces.KNRef{KNID: body.Target.KNID, Branch: body.Target.Branch},
		FallbackByName: body.FallbackByName,
	})
	if err != nil {
		replyHandlerError(c, span, ctx, err)
		return
	}

	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, result)
}

// validateKNDiffBody fills in the default branch and rejects a request that names no network.
//
// Comparing a network with itself is allowed: it is the honest way to ask "did anything change",
// and the answer — everything unchanged — is a real answer rather than a mistake to reject.
func validateKNDiffBody(ctx context.Context, body *KNDiffRequestBody) *rest.HTTPError {
	if body.Base.KNID == "" {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_KNDiff_InvalidParameter).
			WithErrorDetails(knDiffValidationDetail(ctx, "BaseKNIDRequired"))
	}
	if body.Target.KNID == "" {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_KNDiff_InvalidParameter).
			WithErrorDetails(knDiffValidationDetail(ctx, "TargetKNIDRequired"))
	}
	if body.Base.Branch == "" {
		body.Base.Branch = interfaces.MAIN_BRANCH
	}
	if body.Target.Branch == "" {
		body.Target.Branch = interfaces.MAIN_BRANCH
	}
	return nil
}

// knDiffValidationDetail renders one detail message of the comparison error code.
func knDiffValidationDetail(ctx context.Context, name string) string {
	return i18n.Translate(
		rest.GetLanguageByCtx(ctx),
		berrors.BknBackend_KNDiff_InvalidParameter+".Detail."+name,
		nil,
	)
}
