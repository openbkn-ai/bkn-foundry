// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package rest

import (
	"context"

	"github.com/bytedance/sonic"

	"github.com/openbkn-ai/bkn-comm-go/i18n"
	"github.com/openbkn-ai/bkn-comm-go/logger"
)

type BaseError struct {
	ErrorCode               string         `json:"error_code"`    // 错误码
	Description             string         `json:"description"`   // 错误描述
	Solution                string         `json:"solution"`      // 解决方法
	ErrorLink               string         `json:"error_link"`    // 错误链接
	ErrorDetails            interface{}    `json:"error_details"` // 详细内容
	DescriptionTemplateData map[string]any `json:"-"`             // 错误描述参数
	SolutionTemplateData    map[string]any `json:"-"`             // 解决方法参数
}

var (
	allErrs = PublicErrorI18n
)

func Register(errorCodeList []string) {
	for _, errorCode := range errorCodeList {
		if _, ok := allErrs[errorCode]; ok {
			logger.Fatalf("duplicate errorCode: %s", errorCode)
		}
		allErrs[errorCode] = make(map[string]BaseError)
		for lang := range Languages {
			allErrs[errorCode][lang] = BaseError{
				ErrorCode:               errorCode,
				Description:             i18n.Translate(lang, errorCode+".Description", nil),
				Solution:                i18n.Translate(lang, errorCode+".Solution", nil),
				ErrorLink:               i18n.Translate(lang, errorCode+".ErrorLink", nil),
				ErrorDetails:            "",
				DescriptionTemplateData: make(map[string]any),
				SolutionTemplateData:    make(map[string]any),
			}
		}
	}
}

type HTTPError struct {
	HTTPCode  int
	Language  string
	BaseError BaseError
}

// 创建 HTTPError
func NewHTTPError(ctx context.Context, httpCode int, errorCode string) *HTTPError {
	lang := GetLanguageByCtx(ctx)
	errs, ok := allErrs[errorCode]
	if !ok {
		logger.Fatalf("missing errorCode: %s", errorCode)
		return nil
	}
	err := errs[lang]
	if !ok {
		logger.Fatalf("errorCode %s missing lang: %s", errorCode, lang)
		return nil
	}

	return &HTTPError{
		HTTPCode: httpCode,
		Language: lang,
		BaseError: BaseError{
			ErrorCode:    errorCode,
			Description:  err.Description,
			ErrorLink:    err.ErrorLink,
			Solution:     err.Solution,
			ErrorDetails: err.ErrorDetails,
		},
	}
}

func (e *HTTPError) WithDescription(templateData map[string]interface{}) *HTTPError {
	e.BaseError.DescriptionTemplateData = templateData
	e.BaseError.Description = i18n.Translate(e.Language, e.BaseError.ErrorCode+".Description", templateData)
	return e
}

func (e *HTTPError) WithSolution(templateData map[string]interface{}) *HTTPError {
	e.BaseError.SolutionTemplateData = templateData
	e.BaseError.Solution = i18n.Translate(e.Language, e.BaseError.ErrorCode+".Solution", templateData)
	return e
}

// 设置错误详情
func (e *HTTPError) WithErrorDetails(errorDetails interface{}) *HTTPError {
	e.BaseError.ErrorDetails = errorDetails
	return e
}

func (e *HTTPError) Error() string {
	return e.BaseError.Error()
}

func (e *BaseError) Error() string {
	errStr, _ := sonic.MarshalString(e)
	return errStr
}
