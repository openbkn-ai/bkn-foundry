package common

import (
	"fmt"
	"net/http"
	"sync"

	"github.com/creasty/defaults"
	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/validator"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

var (
	// CodeTemplate code template.
	CodeTemplate = "template.%s" // code template.
)

// TemplateHandler template processing interface.
type TemplateHandler interface {
	GetTemplate(c *gin.Context)
}

type templateHandler struct {
	Validator interfaces.Validator
}

var (
	tOnce sync.Once
	th    TemplateHandler
)

func NewTemplateHandler() TemplateHandler {
	tOnce.Do(func() {
		th = &templateHandler{
			Validator: validator.NewValidator(),
		}
	})
	return th
}

// TemplateParams template parameters.
type TemplateParams struct {
	TemplateType string `uri:"template_type" default:"python" validate:"required,oneof=python"`
}

// TemplateResponse template response parameters.
type TemplateResponse struct {
	TemplateType string `json:"template_type"`
	CodeTemplate string `json:"code_template"`
}

func (t *templateHandler) GetTemplate(c *gin.Context) {
	req := &TemplateParams{}
	if err := c.ShouldBindUri(req); err != nil {
		rest.ReplyError(c, err)
		return
	}

	if err := defaults.Set(req); err != nil {
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error())
		rest.ReplyError(c, err)
		return
	}
	if err := t.Validator.ValidatorStruct(c.Request.Context(), req); err != nil {
		rest.ReplyError(c, err)
		return
	}
	language := common.GetLanguageByCtx(c.Request.Context())
	codeTemplate, renderErr := i18n.Render(language, "AgentOperatorIntegration."+fmt.Sprintf(CodeTemplate, req.TemplateType), nil)
	if renderErr != nil {
		rest.ReplyError(c, renderErr)
		return
	}

	rest.ReplyOK(c, http.StatusOK, &TemplateResponse{
		TemplateType: req.TemplateType,
		CodeTemplate: codeTemplate,
	})
}
