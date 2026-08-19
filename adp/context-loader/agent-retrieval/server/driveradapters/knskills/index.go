// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package knskills provides an internal REST portal for skill browsing/reading/execution tools:
// list_skills、get_skill_content、read_skill_file、execute_skill。
// These items have the same origin as the MCP tool of the same name and serve as the backend of the operator-integration toolbox (OpenAPI HTTP).
package knskills

import (
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	logicsSkills "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/knskills"
)

// KnSkillsHandler HTTP entry for skill browsing/reading/execution.
type KnSkillsHandler interface {
	ListSkills(c *gin.Context)
	GetSkillContent(c *gin.Context)
	ReadSkillFile(c *gin.Context)
	ExecuteSkill(c *gin.Context)
}

type knSkillsHandler struct {
	logger interfaces.Logger
	skills logicsSkills.KnSkillsService
}

var (
	handlerOnce sync.Once
	handlerInst KnSkillsHandler
)

// NewKnSkillsHandler create KnSkillsHandler singleton.
func NewKnSkillsHandler() KnSkillsHandler {
	handlerOnce.Do(func() {
		conf := config.NewConfigLoader()
		handlerInst = &knSkillsHandler{
			logger: conf.GetLogger(),
			skills: logicsSkills.NewKnSkillsService(),
		}
	})
	return handlerInst
}

// ListSkills Browse published skills (no knowledge network context required, complementary to find_skills).
func (h *knSkillsHandler) ListSkills(c *gin.Context) {
	ctx := c.Request.Context()
	req := &logicsSkills.ListSkillsReq{}
	// The body is optional; ignore binding errors for an empty body.
	_ = c.ShouldBindJSON(req)

	resp, err := h.skills.ListSkills(ctx, req)
	if err != nil {
		h.logger.WithContext(ctx).Warnf("[KnSkillsHandler#ListSkills] failed: %v", err)
		rest.ReplyError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, resp)
}

// skillIDReq get_skill_content input parameter.
type skillIDReq struct {
	SkillID string `json:"skill_id" form:"skill_id"`
}

// GetSkillContent gets the text of SKILL.md + the file list in the package.
func (h *knSkillsHandler) GetSkillContent(c *gin.Context) {
	ctx := c.Request.Context()
	req := &skillIDReq{}
	_ = c.ShouldBindQuery(req)
	_ = c.ShouldBindJSON(req)
	if req.SkillID == "" {
		rest.ReplyError(c, errors.DefaultHTTPError(ctx, http.StatusBadRequest, logicsSkills.SkillIDRequiredError(ctx).Error()))
		return
	}

	resp, err := h.skills.GetSkillContent(ctx, req.SkillID)
	if err != nil {
		h.logger.WithContext(ctx).Warnf("[KnSkillsHandler#GetSkillContent] failed: %v", err)
		rest.ReplyError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, resp)
}

// ReadSkillFile reads a single file in the skill package (rel_path is taken from the files list of get_skill_content).
func (h *knSkillsHandler) ReadSkillFile(c *gin.Context) {
	ctx := c.Request.Context()
	req := &logicsSkills.ReadSkillFileReq{}
	_ = c.ShouldBindQuery(req)
	if err := c.ShouldBindJSON(req); err != nil && req.SkillID == "" {
		rest.ReplyError(c, errors.DefaultHTTPError(ctx, http.StatusBadRequest, err.Error()))
		return
	}

	resp, err := h.skills.ReadSkillFile(ctx, req)
	if err != nil {
		h.logger.WithContext(ctx).Warnf("[KnSkillsHandler#ReadSkillFile] failed: %v", err)
		rest.ReplyError(c, errors.DefaultHTTPError(ctx, http.StatusBadRequest, err.Error()))
		return
	}
	rest.ReplyOK(c, http.StatusOK, resp)
}

// ExecuteSkill executes the skill entry command in the sandbox.
func (h *knSkillsHandler) ExecuteSkill(c *gin.Context) {
	ctx := c.Request.Context()
	req := &logicsSkills.ExecuteSkillReq{}
	if err := c.ShouldBindJSON(req); err != nil {
		rest.ReplyError(c, errors.DefaultHTTPError(ctx, http.StatusBadRequest, err.Error()))
		return
	}

	resp, err := h.skills.ExecuteSkill(ctx, req)
	if err != nil {
		h.logger.WithContext(ctx).Warnf("[KnSkillsHandler#ExecuteSkill] failed: %v", err)
		rest.ReplyError(c, errors.DefaultHTTPError(ctx, http.StatusBadRequest, err.Error()))
		return
	}
	rest.ReplyOK(c, http.StatusOK, resp)
}
