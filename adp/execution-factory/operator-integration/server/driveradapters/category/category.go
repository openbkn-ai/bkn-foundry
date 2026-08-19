package category

import (
	"net/http"

	"github.com/creasty/defaults"
	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	sharedrest "github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

// CategoryList operator classification list.
func (h *categoryHandler) CategoryList(c *gin.Context) {
	result, err := h.CategoryManager.GetCategoryList(c.Request.Context())
	if err != nil {
		rest.ReplyError(c, err)
		return
	}
	sharedrest.MarkLocalizedCacheableResponse(c)
	rest.ReplyOK(c, http.StatusOK, result)
}

// CategoryUpdate operator classification update.
func (h *categoryHandler) CategoryUpdate(c *gin.Context) {
	req := &interfaces.UpdateCategoryReq{}
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
	err = c.ShouldBindJSON(req)
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

	ctx := c.Request.Context()
	result, err := h.CategoryManager.UpdateCategory(ctx, req)
	if err != nil {
		rest.ReplyError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, result)
}

// CategoryCreate operator classification creation.
func (h *categoryHandler) CategoryCreate(c *gin.Context) {
	req := &interfaces.CreateCategoryReq{}
	err := c.ShouldBindHeader(req)
	if err != nil {
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error())
		rest.ReplyError(c, err)
		return
	}
	err = c.ShouldBindJSON(req)
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
	ctx := c.Request.Context()
	result, err := h.CategoryManager.CreateCategory(ctx, req)
	if err != nil {
		rest.ReplyError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, result)
}

// CategoryDelete operator category deletion.
func (h *categoryHandler) CategoryDelete(c *gin.Context) {
	req := &interfaces.DeleteCategoryReq{}
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
	ctx := c.Request.Context()
	err = h.CategoryManager.DeleteCategory(ctx, req)
	if err != nil {
		rest.ReplyError(c, err)
		return
	}
	rest.ReplyOK(c, http.StatusOK, nil)
}
