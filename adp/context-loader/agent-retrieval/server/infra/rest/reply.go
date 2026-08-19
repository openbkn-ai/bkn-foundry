// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package rest responsehandle.
// @file rest.go
// @description: responsehandle.
package rest

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	validator "github.com/go-playground/validator/v10"
	sharedrest "github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	errorwrap "github.com/pkg/errors"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	myErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/localize"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/logger"
	validatorv "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/validator"
)

const (
	ContentTypeKey  = "Content-Type"
	ContentTypeJSON = "application/json"
)

// ReplyOK response is successful; output JSON or TOON depending on response_format in context.
func ReplyOK(c *gin.Context, statusCode int, body interface{}) {
	format := FormatJSON
	if v, ok := common.GetResponseFormatFromCtx(c.Request.Context()); ok {
		if f, ok := v.(ResponseFormat); ok {
			format = f
		}
	}

	contentType := ContentTypeJSON
	var bodyBytes []byte
	var err error
	if body != nil {
		contentType, bodyBytes, err = MarshalResponse(format, body)
		if err != nil {
			logger.DefaultLogger().Errorf("marshal body error: %v", err)
			statusCode = http.StatusInternalServerError
			ctx := c.Request.Context()
			bodyStr := myErr.DefaultHTTPError(ctx, statusCode, err.Error()).Error()
			sharedrest.MarkLocalizedResponse(c)
			c.Writer.Header().Set(ContentTypeKey, ContentTypeJSON)
			c.String(statusCode, bodyStr)
			return
		}
	}

	if len(bodyBytes) > 0 {
		c.Data(statusCode, contentType, bodyBytes)
	} else {
		c.Writer.Header().Set(ContentTypeKey, contentType)
		c.String(statusCode, "")
	}
}

// ReplyError responseerror.
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
				// Generate friendly multi-language error details (understandable by large models)
				friendlyDetails := formatValidatorErrorDetails(ctx, vErr[0])
				body = myErr.NewHTTPError(ctx, http.StatusBadRequest, extCode, friendlyDetails).Error()
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

// ExHTTPError error code depending on the service.
type ExHTTPError struct {
	HTTPCode int
	Body     []byte
}

func (e *ExHTTPError) Error() string {
	return string(e.Body)
}

// formatValidatorErrorDetails formats validator error details and generates multi-language friendly error information that can be understood by large models.
func formatValidatorErrorDetails(ctx context.Context, err validator.FieldError) string {
	// Get language settings (format: zh-CN or en-US)
	lang := common.GetLanguageFromCtx(ctx)
	// Convert zh-CN to zh_CN format (the format used by internationalization systems)
	langKey := strings.ReplaceAll(lang, "-", "_")
	tr := localize.NewI18nTranslator(langKey)

	fieldName := err.Field()
	tag := err.Tag()
	param := err.Param()
	currentValue := ""
	if err.Value() != nil {
		currentValue = fmt.Sprintf("%v", err.Value())
	}

	// Generate friendly multi-language error messages based on different validation tags.
	var templateKey string
	var formatArgs []interface{}

	switch tag {
	case "min", "gte", "gt":
		if currentValue != "" {
			templateKey = "desc.ValidationDetailMin"
			formatArgs = []interface{}{fieldName, currentValue, param, param}
		} else {
			templateKey = "desc.ValidationDetailMinNoValue"
			formatArgs = []interface{}{fieldName, param}
		}
	case "max", "lte", "lt":
		if currentValue != "" {
			templateKey = "desc.ValidationDetailMax"
			formatArgs = []interface{}{fieldName, currentValue, param, param}
		} else {
			templateKey = "desc.ValidationDetailMaxNoValue"
			formatArgs = []interface{}{fieldName, param}
		}
	case "required":
		templateKey = "desc.ValidationDetailRequired"
		formatArgs = []interface{}{fieldName}
	case "oneof":
		templateKey = "desc.ValidationDetailOneof"
		// Formatting option list: use "," in Chinese and ", " in English.
		options := strings.ReplaceAll(param, " ", "、")
		if langKey == "en_US" {
			options = strings.ReplaceAll(param, " ", ", ")
		}
		formatArgs = []interface{}{fieldName, options}
	default:
		// Other validation tags, using a common format.
		if currentValue != "" {
			templateKey = "desc.ValidationDetailUnknown"
			formatArgs = []interface{}{fieldName, currentValue, tag, param}
		} else {
			templateKey = "desc.ValidationDetailUnknownNoValue"
			formatArgs = []interface{}{fieldName, tag, param}
		}
	}

	// Get the translation template and format it.
	template := tr.Trans(templateKey)
	return fmt.Sprintf(template, formatArgs...)
}
