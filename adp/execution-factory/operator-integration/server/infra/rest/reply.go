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
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	myErr "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/logger"
	validatorv "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/validator"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	sharedrest "github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	errorwrap "github.com/pkg/errors"
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
			sharedrest.MarkLocalizedResponse(c)
		}
	}

	c.Writer.Header().Set(ContentTypeKey, ContentTypeJSON)
	c.String(statusCode, bodyStr)
}

// ReplyError writes an error response.
func ReplyError(c *gin.Context, err error) {
	if err != nil {
		errWithStack := errorwrap.WithStack(err)
		logger.DefaultLogger().Debug("Error:", errWithStack.Error())
	}
	var httpCode int
	ctx := c.Request.Context()
	var body string
	localized := true
	switch e := err.(type) {
	case *ExHTTPError:
		httpCode = e.HTTPCode
		body = e.Error()
		localized = false
	default:
		httpError := &myErr.HTTPError{}
		vErr := make(validator.ValidationErrors, 0)
		if errors.As(err, &httpError) {
			httpCode = httpError.HTTPCode
			body = err.Error()
		} else if errors.As(err, &vErr) {
			httpCode = http.StatusBadRequest
			if len(vErr) > 0 {
				extCode := validatorv.TagToErrorType[vErr[0].Tag()]
				body = myErr.NewHTTPError(ctx, http.StatusBadRequest, extCode, vErr[0].Error()).Error()
			} else {
				body = myErr.DefaultHTTPError(ctx, httpCode, err.Error()).Error()
			}
		} else {
			httpCode = http.StatusInternalServerError
			body = myErr.DefaultHTTPError(ctx, httpCode, err.Error()).Error()
		}
	}
	if localized {
		sharedrest.MarkLocalizedResponse(c)
	}
	c.Writer.Header().Set(ContentTypeKey, ContentTypeJSON)
	c.String(httpCode, body)
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
			switch e := err.(type) {
			case *ExHTTPError:
				c.SSEvent("error", e)
			case *myErr.HTTPError:
				sharedrest.MarkLocalizedResponse(c)
				c.SSEvent("error", e)
			default:
				localizedErr := myErr.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
				sharedrest.MarkLocalizedResponse(c)
				c.SSEvent("error", localizedErr)
			}
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
