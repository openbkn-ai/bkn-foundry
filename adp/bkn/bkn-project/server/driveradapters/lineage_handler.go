// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	attr "go.opentelemetry.io/otel/attribute"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/common"
	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// Lineages and their project branches. Every authenticated caller may use
// these routes; the bkn-backend export behind lineage creation and the drift
// check runs with the caller's token, so bkn-backend applies its own permissions.

const (
	// maxDefinitionsBytes bounds the BKN tar a definitions upload may carry.
	maxDefinitionsBytes = 64 << 20

	branchActionDefinitions = "/definitions"
	branchActionSync        = "/sync"
)

func (r *restHandler) CreateLineageByEx(c *gin.Context) { r.withExVisitor(c, r.CreateLineage) }
func (r *restHandler) CreateBranchByEx(c *gin.Context)  { r.withExVisitor(c, r.CreateBranch) }
func (r *restHandler) CheckDriftByEx(c *gin.Context)    { r.withExVisitor(c, r.CheckDrift) }
func (r *restHandler) PlanSwitchByEx(c *gin.Context)    { r.withExVisitor(c, r.PlanSwitch) }
func (r *restHandler) SwitchByEx(c *gin.Context)        { r.withExVisitor(c, r.SwitchConsumers) }

// CommitDefinitionsByEx serves PUT .../branches/{branch}/definitions. A branch
// name may hold "/", so the route catches the rest of the path and the action is
// its last segment.
func (r *restHandler) CommitDefinitionsByEx(c *gin.Context) {
	r.withBranchAction(c, branchActionDefinitions, r.CommitDefinitions)
}

// SyncBranchByEx serves POST .../branches/{branch}/sync.
func (r *restHandler) SyncBranchByEx(c *gin.Context) {
	r.withBranchAction(c, branchActionSync, r.SyncBranch)
}

func (r *restHandler) withBranchAction(c *gin.Context, action string,
	handle func(*gin.Context, hydra.Visitor, string)) {
	trimmed, ok := strings.CutSuffix(c.Param("branch"), action)
	if !ok {
		replyNotFound(c)
		return
	}
	branch := strings.TrimPrefix(trimmed, "/")
	r.withExVisitor(c, func(c *gin.Context, visitor hydra.Visitor) { handle(c, visitor, branch) })
}

func replyNotFound(c *gin.Context) {
	rest.ReplyError(c, httperrors.NewHTTPError(c.Request.Context(), http.StatusNotFound, httperrors.PublicError_NotFound))
}

// gitAuthor records the caller as the author of the commits a request makes.
func gitAuthor(visitor hydra.Visitor) interfaces.GitAuthor {
	return interfaces.GitAuthor{Name: visitor.ID}
}

func invalidBody(ctx context.Context) error {
	return httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Lineage_InvalidRequestBody)
}

// CreateLineage serves POST /lineages with {"kn_id"}.
func (r *restHandler) CreateLineage(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	req := interfaces.CreateLineageRequest{}
	if err := c.ShouldBindJSON(&req); err != nil {
		replyError(c, span, invalidBody(ctx))
		return
	}
	span.SetAttributes(attr.Key("kn_id").String(req.KNID))

	result, err := r.ls.CreateLineage(ctx, visitor.TokenID, req.KNID, gitAuthor(visitor))
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusCreated)
	rest.ReplyOK(c, http.StatusCreated, result)
}

// CreateBranch serves POST /lineages/{lineage_id}/branches with {"name", "from"}.
func (r *restHandler) CreateBranch(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	lineageID := c.Param("lineage_id")
	span.SetAttributes(attr.Key("lineage_id").String(lineageID))

	req := interfaces.CreateBranchRequest{}
	if err := c.ShouldBindJSON(&req); err != nil {
		replyError(c, span, invalidBody(ctx))
		return
	}
	result, err := r.ls.CreateBranch(ctx, lineageID, req.Name, req.From)
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusCreated)
	rest.ReplyOK(c, http.StatusCreated, result)
}

// CommitDefinitions takes the raw BKN tar as the request body
// (Content-Type: application/octet-stream) and the commit message from the
// "message" query parameter.
func (r *restHandler) CommitDefinitions(c *gin.Context, visitor hydra.Visitor, branch string) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	lineageID := c.Param("lineage_id")
	span.SetAttributes(attr.Key("lineage_id").String(lineageID), attr.Key("branch").String(branch))

	data, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, maxDefinitionsBytes))
	var files map[string][]byte
	if err == nil {
		files, err = common.ReadBKNTar(data)
	}
	if err != nil {
		replyError(c, span, httperrors.NewHTTPError(ctx, http.StatusBadRequest, perrors.BknProject_Lineage_InvalidDefinitions).
			WithErrorDetails(err.Error()))
		return
	}
	result, err := r.ls.CommitDefinitions(ctx, lineageID, branch, files, c.Query("message"), gitAuthor(visitor))
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, result)
}

// SyncBranch merges main into the branch.
func (r *restHandler) SyncBranch(c *gin.Context, visitor hydra.Visitor, branch string) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	lineageID := c.Param("lineage_id")
	span.SetAttributes(attr.Key("lineage_id").String(lineageID), attr.Key("branch").String(branch))

	result, err := r.ls.SyncBranch(ctx, lineageID, branch, gitAuthor(visitor))
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, result)
}

// CheckDrift serves POST /lineages/{lineage_id}/drift with {"kn_id"}; with
// ?commit=true the live definitions are also committed to main.
func (r *restHandler) CheckDrift(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	lineageID := c.Param("lineage_id")
	span.SetAttributes(attr.Key("lineage_id").String(lineageID))

	req := interfaces.DriftRequest{}
	if err := c.ShouldBindJSON(&req); err != nil {
		replyError(c, span, invalidBody(ctx))
		return
	}
	commit := c.Query("commit") == "true"
	result, err := r.ls.CheckDrift(ctx, visitor.TokenID, lineageID, req.KNID, commit, gitAuthor(visitor))
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, result)
}

// PlanSwitch serves GET /lineages/{lineage_id}/switch-plan?to={network_id}.
func (r *restHandler) PlanSwitch(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	lineageID, to := c.Param("lineage_id"), c.Query("to")
	span.SetAttributes(attr.Key("lineage_id").String(lineageID), attr.Key("to_network_id").String(to))

	plan, err := r.ls.PlanSwitch(ctx, visitor.TokenID, lineageID, to)
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, plan)
}

// SwitchConsumers serves POST /lineages/{lineage_id}/switches with {"to_network_id"}.
func (r *restHandler) SwitchConsumers(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	lineageID := c.Param("lineage_id")
	span.SetAttributes(attr.Key("lineage_id").String(lineageID))

	req := interfaces.SwitchRequest{}
	if err := c.ShouldBindJSON(&req); err != nil {
		replyError(c, span, invalidBody(ctx))
		return
	}
	span.SetAttributes(attr.Key("to_network_id").String(req.ToNetworkID))

	operator := interfaces.AccountInfo{ID: visitor.ID, Type: string(visitor.Type)}
	sw, err := r.ls.SwitchConsumers(ctx, visitor.TokenID, lineageID, req, operator)
	if err != nil {
		replyError(c, span, err)
		return
	}
	oteltrace.AddHttpAttrs4Ok(span, http.StatusCreated)
	rest.ReplyOK(c, http.StatusCreated, sw)
}
