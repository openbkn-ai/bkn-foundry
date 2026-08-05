// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package knskills 提供技能浏览 / 阅读 / 执行工具的内部 REST 入口：
// list_skills、get_skill_content、read_skill_file、execute_skill。
// 这几条与同名 MCP 工具同源，同时作为 operator-integration toolbox(OpenAPI HTTP)的后端。
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

// KnSkillsHandler 技能浏览 / 阅读 / 执行的 HTTP 入口。
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

// NewKnSkillsHandler 创建 KnSkillsHandler 单例。
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

// ListSkills 浏览已发布技能（不需要知识网络上下文，与 find_skills 互补）。
func (h *knSkillsHandler) ListSkills(c *gin.Context) {
	ctx := c.Request.Context()
	req := &logicsSkills.ListSkillsReq{}
	// body 可选；忽略空 body 的绑定错误。
	_ = c.ShouldBindJSON(req)

	resp, err := h.skills.ListSkills(ctx, req)
	if err != nil {
		h.logger.WithContext(ctx).Warnf("[KnSkillsHandler#ListSkills] failed: %v", err)
		rest.ReplyError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, resp)
}

// skillIDReq get_skill_content 入参。
type skillIDReq struct {
	SkillID string `json:"skill_id" form:"skill_id"`
}

// GetSkillContent 取 SKILL.md 正文 + 包内文件清单。
func (h *knSkillsHandler) GetSkillContent(c *gin.Context) {
	ctx := c.Request.Context()
	req := &skillIDReq{}
	_ = c.ShouldBindQuery(req)
	_ = c.ShouldBindJSON(req)
	if req.SkillID == "" {
		rest.ReplyError(c, errors.DefaultHTTPError(ctx, http.StatusBadRequest, logicsSkills.ErrSkillIDRequired.Error()))
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

// ReadSkillFile 读技能包内单个文件（rel_path 取自 get_skill_content 的 files 清单）。
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

// ExecuteSkill 在沙箱内执行技能入口命令。
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
