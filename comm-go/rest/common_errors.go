// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package rest

// 系统默认错误
const (
	// 公共错误码
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

var (
	PublicErrorI18n = map[string]map[string]BaseError{
		PublicError_BadRequest: {
			"zh-CN": {
				ErrorCode:   PublicError_BadRequest,
				Description: "参数错误",
				Solution:    "暂无",
				ErrorLink:   "暂无",
			},
			"en-US": {
				ErrorCode:   PublicError_BadRequest,
				Description: "Internal Server Error",
				Solution:    "None",
				ErrorLink:   "None",
			},
		},
		PublicError_Unauthorized: {
			"zh-CN": {
				ErrorCode:   PublicError_Unauthorized,
				Description: "认证失败",
				Solution:    "暂无",
				ErrorLink:   "暂无",
			},
			"en-US": {
				ErrorCode:   PublicError_Unauthorized,
				Description: "authorized failed",
				Solution:    "None",
				ErrorLink:   "None",
			},
		},
		PublicError_Forbidden: {
			"zh-CN": {
				ErrorCode:   PublicError_Forbidden,
				Description: "权限错误",
				Solution:    "暂无",
				ErrorLink:   "暂无",
			},
			"en-US": {
				ErrorCode:   PublicError_Forbidden,
				Description: "permission error",
				Solution:    "None",
				ErrorLink:   "None",
			},
		},
		PublicError_NotFound: {
			"zh-CN": {
				ErrorCode:   PublicError_NotFound,
				Description: "对象不存在",
				Solution:    "暂无",
				ErrorLink:   "暂无",
			},
			"en-US": {
				ErrorCode:   PublicError_NotFound,
				Description: "not found",
				Solution:    "None",
				ErrorLink:   "None",
			},
		},
		PublicError_MethodNotAllowed: {
			"zh-CN": {
				ErrorCode:   PublicError_MethodNotAllowed,
				Description: "不支持的mtehod方法",
				Solution:    "暂无",
				ErrorLink:   "暂无",
			},
			"en-US": {
				ErrorCode:   PublicError_MethodNotAllowed,
				Description: "method not allowed",
				Solution:    "None",
				ErrorLink:   "None",
			},
		},
		PublicError_Conflict: {
			"zh-CN": {
				ErrorCode:   PublicError_Conflict,
				Description: "资源冲突",
				Solution:    "暂无",
				ErrorLink:   "暂无",
			},
			"en-US": {
				ErrorCode:   PublicError_Conflict,
				Description: "conflict",
				Solution:    "None",
				ErrorLink:   "None",
			},
		},
		PublicError_InternalServerError: {
			"zh-CN": {
				ErrorCode:   PublicError_InternalServerError,
				Description: "内部错误",
				Solution:    "暂无",
				ErrorLink:   "暂无",
			},
			"en-US": {
				ErrorCode:   PublicError_InternalServerError,
				Description: "internal server error",
				Solution:    "None",
				ErrorLink:   "None",
			},
		},
		PublicError_NotImplemented: {
			"zh-CN": {
				ErrorCode:   PublicError_NotImplemented,
				Description: "服务端未实现请求方法",
				Solution:    "暂无",
				ErrorLink:   "暂无",
			},
			"en-US": {
				ErrorCode:   PublicError_NotImplemented,
				Description: "not implemented",
				Solution:    "None",
				ErrorLink:   "None",
			},
		},
		PublicError_ServiceUnavailable: {
			"zh-CN": {
				ErrorCode:   PublicError_ServiceUnavailable,
				Description: "服务端暂时不可用",
				Solution:    "暂无",
				ErrorLink:   "暂无",
			},
			"en-US": {
				ErrorCode:   PublicError_ServiceUnavailable,
				Description: "service unavailable",
				Solution:    "None",
				ErrorLink:   "None",
			},
		},
	}
)
