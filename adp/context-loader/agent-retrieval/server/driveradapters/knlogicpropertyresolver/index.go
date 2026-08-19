// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package knlogicpropertyresolver provides HTTP handler for logic property resolver operations.
package knlogicpropertyresolver

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/creasty/defaults"
	"github.com/gin-gonic/gin"
	validator "github.com/go-playground/validator/v10"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	logicskn "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/logics/knlogicpropertyresolver"
)

// KnLogicPropertyResolverHandler logical propertyparse Handler.
type KnLogicPropertyResolverHandler interface {
	ResolveLogicProperties(c *gin.Context)
}

type knLogicPropertyResolverHandle struct {
	Logger  interfaces.Logger
	Service interfaces.IKnLogicPropertyResolverService
}

var (
	handlerOnce sync.Once
	handler     KnLogicPropertyResolverHandler
)

// NewKnLogicPropertyResolverHandler create KnLogicPropertyResolverHandler.
func NewKnLogicPropertyResolverHandler() KnLogicPropertyResolverHandler {
	handlerOnce.Do(func() {
		conf := config.NewConfigLoader()
		handler = &knLogicPropertyResolverHandle{
			Logger:  conf.GetLogger(),
			Service: logicskn.NewKnLogicPropertyResolverService(),
		}
	})
	return handler
}

// ResolveLogicProperties resolves logic properties.
// @Summary Resolve logic properties
// @Description Generates dynamic_params from the query and context, then retrieves logic-property values (metrics and tools) through ontology-query.
// @Tags kn-context-loader
// @Accept json
// @Produce json
// @Param x-account-id header string false "Account ID"
// @Param x-account-type header string false "Account type"
// @Param x-kn-id header string true "Knowledge network ID"
// @Param body body interfaces.ResolveLogicPropertiesRequest true "Request payload"
// @Success 200 {object} interfaces.ResolveLogicPropertiesResponse "Success response"
// @Failure 400 {object} interfaces.MissingParamsError "Missing parameter error"
// @Failure 404 {object} interfaces.KnBaseError "Object type not found"
// @Failure 500 {object} interfaces.KnBaseError "Internal server error"
// @Router /api/kn/logic-property-resolver [post]
func (k *knLogicPropertyResolverHandle) ResolveLogicProperties(c *gin.Context) {
	var err error
	req := &interfaces.ResolveLogicPropertiesRequest{
		Options: &interfaces.ResolveOptions{},
	}

	// Bind Header parameters.
	if err = c.ShouldBindHeader(req); err != nil {
		k.Logger.Errorf("[KnLogicPropertyResolverHandler] Bind header failed: %v", err)
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error())
		rest.ReplyError(c, err)
		return
	}

	// Bind the JSON body.
	if err = c.ShouldBindJSON(req); err != nil {
		k.Logger.Errorf("[KnLogicPropertyResolverHandler] Bind JSON failed: %v", err)
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error())
		rest.ReplyError(c, err)
		return
	}

	// Set default values.
	if err = defaults.Set(req.Options); err != nil {
		k.Logger.Errorf("[KnLogicPropertyResolverHandler] Set defaults failed: %v", err)
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error())
		rest.ReplyError(c, err)
		return
	}

	// Validate parameters.
	err = validator.New().Struct(req)
	if err != nil {
		k.Logger.Errorf("[KnLogicPropertyResolverHandler] Validate failed: %v", err)
		rest.ReplyError(c, err)
		return
	}

	// 📥 Record request input parameters (structured)
	reqJSON, _ := json.Marshal(req)
	k.Logger.Infof("========== [kn-logic-property-resolver] 请求开始 ==========")
	k.Logger.Infof("📥 请求参数: %s", string(reqJSON))

	// Call the Service layer (record the time taken)
	startTime := time.Now()
	resp, err := k.Service.ResolveLogicProperties(c.Request.Context(), req)
	elapsed := time.Since(startTime).Milliseconds()

	if err != nil {
		k.Logger.Errorf("========== [kn-logic-property-resolver] 请求失败 ========== (耗时: %dms)", elapsed)
		k.Logger.Errorf("❌ 错误信息: %v", err)
		rest.ReplyError(c, err)
		return
	}

	// 📤 Record response results.
	respJSON, _ := json.Marshal(resp)
	k.Logger.Infof("========== [kn-logic-property-resolver] 请求成功 ========== (耗时: %dms)", elapsed)
	k.Logger.Infof("📤 响应数据: %s", string(respJSON))

	// Return a successful response.
	rest.ReplyOK(c, http.StatusOK, resp)
}
