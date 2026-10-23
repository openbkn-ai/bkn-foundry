// Package rest provides HTTP response helpers.
// @file rest.go
// @description: HTTP response handling
package rest

import (
	"errors"
	"net/http"

	"github.com/bytedance/sonic"
	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	myErr "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
	validatorv "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/validator"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

const (
	ContentTypeKey  = "Content-Type"
	ContentTypeJSON = "application/json"
)

// ReplyOK writes a successful JSON response.
func ReplyOK(c *gin.Context, statusCode int, body interface{}) {
	var (
		bodyStr string
		err     error
	)

	if body != nil {
		bodyStr, err = sonic.MarshalString(body)
		if err != nil {
			logger.DefaultLogger().Errorf("marshal body error: %v", err)
			statusCode = http.StatusInternalServerError
			ctx := c.Request.Context()
			bodyStr = myErr.DefaultHTTPError(ctx, statusCode, err.Error()).Error()
			rest.MarkLocalizedResponse(c)
		}
	}

	c.Writer.Header().Set(ContentTypeKey, ContentTypeJSON)
	c.String(statusCode, bodyStr)
}

// ReplyError writes an error response.
func ReplyError(c *gin.Context, err error) {
	var upstream *ExHTTPError
	if errors.As(err, &upstream) && upstream != nil {
		c.Header(ContentTypeKey, ContentTypeJSON)
		c.String(upstream.HTTPCode, string(upstream.Body))
		return
	}
	var validationErrors validator.ValidationErrors
	if errors.As(err, &validationErrors) {
		ctx := c.Request.Context()
		if len(validationErrors) > 0 {
			vErr := validationErrors
			extCode := validatorv.TagToErrorType[vErr[0].Tag()]
			friendlyDetails := vErr[0].Error()
			if extCode == "" {
				err = myErr.DefaultHTTPError(ctx, http.StatusBadRequest, friendlyDetails)
			} else {
				err = myErr.NewHTTPError(ctx, http.StatusBadRequest, extCode, friendlyDetails)
			}
		} else {
			err = myErr.DefaultHTTPError(ctx, http.StatusBadRequest, err.Error())
		}
	}

	var httpErr *httperrors.HTTPError
	if !errors.As(err, &httpErr) || httpErr == nil {
		logger.DefaultLogger().Errorf("unhandled response error: %v", err)
		err = myErr.DefaultHTTPError(c.Request.Context(), http.StatusInternalServerError, err.Error())
	}
	rest.ReplyError(c, err)
}

// ExHTTPError preserves an error response owned by a dependency.
type ExHTTPError struct {
	HTTPCode int
	Body     []byte
}

func (e *ExHTTPError) Error() string {
	return string(e.Body)
}

// ReplyWithExecutionMode writes a response according to the requested execution mode.
func ReplyWithExecutionMode(c *gin.Context, resp interface{}, err error) {
	// Select the response transport for streaming requests.
	ctx := c.Request.Context()
	executionMode := common.GetExecutionModeFromCtx(ctx)
	streamingMode, _ := common.GetStreamingModeFromCtx(ctx)
	switch executionMode {
	case interfaces.ExecutionModeStream:
		switch streamingMode {
		case interfaces.StreamingModeSSE:
			if err == nil {
				return
			}

			var upstream *ExHTTPError
			if errors.As(err, &upstream) && upstream != nil {
				c.SSEvent("error", upstream)
				return
			}
			var httpErr *httperrors.HTTPError
			if !errors.As(err, &httpErr) || httpErr == nil {
				httpErr = myErr.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
			}
			if renderErr := httpErr.Render(); renderErr != nil {
				logger.DefaultLogger().Errorf("localized error rendering failed: %v", renderErr)
			}
			c.Header(i18n.ContentLanguageHeader, string(httpErr.Lang))
			c.SSEvent("error", httpErr.BaseError)
			return
		case interfaces.StreamingModeHTTP:
			if err != nil {
				ReplyError(c, err)
				return
			}
		}
	case interfaces.ExecutionModeAsync, interfaces.ExecutionModeSync:
		if err != nil {
			ReplyError(c, err)
			return
		}
		ReplyOK(c, http.StatusOK, resp)
	}
}
