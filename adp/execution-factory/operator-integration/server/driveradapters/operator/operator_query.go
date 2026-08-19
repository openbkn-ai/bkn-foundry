package operator

import (
	"net/http"

	"github.com/creasty/defaults"
	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

// OperatorQuery operator information query.
func (op *operatorHandle) OperatorQueryByOperatorID(c *gin.Context) {
	req := &interfaces.GetOperatorInfoByOperatorIDReq{}
	err := c.ShouldBindHeader(req)
	if err != nil {
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error())
		rest.ReplyError(c, err)
		return
	}
	err = c.ShouldBindUri(req)
	if err != nil {
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error())
		rest.ReplyError(c, err)
		return
	}
	result, err := op.OperatorManager.GetOperatorInfoByOperatorID(c.Request.Context(), req)
	if err != nil {
		rest.ReplyError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, result)
}

// OperatorQueryPage operator paging query.
func (op *operatorHandle) OperatorQueryPage(c *gin.Context) {
	ctx := c.Request.Context()
	req := &interfaces.PageQueryRequest{}
	err := c.ShouldBindHeader(req)
	if err != nil {
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error())
		rest.ReplyError(c, err)
		return
	}
	err = c.ShouldBindQuery(req)
	if err != nil {
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error())
		rest.ReplyError(c, err)
		return
	}
	err = defaults.Set(req)
	if err != nil {
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error())
		rest.ReplyError(c, err)
		return
	}
	err = validator.New().Struct(req)
	if err != nil {
		rest.ReplyError(c, err)
		return
	}
	if req.PageSize < 0 {
		req.All = true
	}
	result, err := op.OperatorManager.GetOperatorQueryPage(ctx, req)
	if err != nil {
		rest.ReplyError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, result)
}

// OperatorQueryNamesByIDs batch names based on operator ID (used to echo names on the front-end object-level authorization page)
func (op *operatorHandle) OperatorQueryNamesByIDs(c *gin.Context) {
	req := &interfaces.BatchNamesReq{}
	if err := c.ShouldBindJSON(req); err != nil {
		rest.ReplyError(c, errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error()))
		return
	}
	result, err := op.OperatorManager.GetOperatorNamesByIDs(c.Request.Context(), req.IDs)
	if err != nil {
		rest.ReplyError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, result)
}
