// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package rest

import (
	_ "embed"

	"github.com/BurntSushi/toml"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
)

// System default error codes.
const (
	PublicError_BadRequest          = "Public.BadRequest"
	PublicError_Unauthorized        = "Public.Unauthorized"
	PublicError_Forbidden           = "Public.Forbidden"
	PublicError_NotFound            = "Public.NotFound"
	PublicError_MethodNotAllowed    = "Public.MethodNotAllowed"
	PublicError_Conflict            = "Public.Conflict"
	PublicError_InternalServerError = "Public.InternalServerError"
	PublicError_NotImplemented      = "Public.NotImplemented"
	PublicError_ServiceUnavailable  = "Public.ServiceUnavailable"
)

var publicErrorCodes = []string{
	PublicError_BadRequest,
	PublicError_Unauthorized,
	PublicError_Forbidden,
	PublicError_NotFound,
	PublicError_MethodNotAllowed,
	PublicError_Conflict,
	PublicError_InternalServerError,
	PublicError_NotImplemented,
	PublicError_ServiceUnavailable,
}

var publicErrorLocales = []Language{SimplifiedChinese, AmericanEnglish}

//go:embed locales/public-errors.zh-CN.toml
var publicErrorsChinese []byte

//go:embed locales/public-errors.en-US.toml
var publicErrorsEnglish []byte

type publicErrorMessage struct {
	Description string
	Solution    string
	ErrorLink   string
}

// PublicErrorI18n is loaded from resources embedded in comm-go. Embedding keeps
// the public HTTP error catalog available in every consumer image.
var PublicErrorI18n = loadPublicErrorI18n()

func loadPublicErrorI18n() map[string]map[string]BaseError {
	resources := map[Language][]byte{
		SimplifiedChinese: publicErrorsChinese,
		AmericanEnglish:   publicErrorsEnglish,
	}
	parsed := make(map[Language]map[string]publicErrorMessage, len(resources))
	for lang, contents := range resources {
		messages := make(map[string]publicErrorMessage)
		if err := toml.Unmarshal(contents, &messages); err != nil {
			logger.Warnf("load embedded public error locale %s: %v", lang, err)
			continue
		}
		parsed[lang] = messages
	}

	errors := make(map[string]map[string]BaseError, len(publicErrorCodes))
	for _, code := range publicErrorCodes {
		errors[code] = make(map[string]BaseError, len(publicErrorLocales))
		for _, lang := range publicErrorLocales {
			message, ok := parsed[lang][code]
			if !ok || message.Description == "" || message.Solution == "" {
				logger.Warnf("embedded public error locale missing message: locale=%s error_code=%s", lang, code)
				message = fallbackPublicError(lang)
			}
			errors[code][lang] = BaseError{
				ErrorCode:   code,
				Description: message.Description,
				Solution:    message.Solution,
				ErrorLink:   message.ErrorLink,
			}
		}
	}
	return errors
}

func fallbackPublicError(lang Language) publicErrorMessage {
	if lang == AmericanEnglish {
		return publicErrorMessage{
			Description: "Request failed.",
			Solution:    "See the request details or contact an administrator.",
		}
	}
	return publicErrorMessage{
		Description: "请求失败。",
		Solution:    "请查看请求详情或联系管理员。",
	}
}
