// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package i18n

var publicErrorsZhCN = I18nMessages{
	Locale: SimplifiedChinese,
	Messages: map[string]string{
		"Public.BadRequest":          "请求参数无效。请检查请求参数后重试。",
		"Public.Unauthorized":        "认证失败。请提供有效的认证凭据。",
		"Public.Forbidden":           "没有执行此操作的权限。请联系管理员授予所需权限。",
		"Public.NotFound":            "请求的资源不存在。请检查资源标识是否正确。",
		"Public.MethodNotAllowed":    "不支持当前请求方法。请使用接口支持的请求方法。",
		"Public.Conflict":            "请求与当前资源状态冲突。请刷新资源状态后重试。",
		"Public.InternalServerError": "服务内部错误。请稍后重试。",
		"Public.NotImplemented":      "服务端未实现该请求。请确认接口能力后重试。",
		"Public.ServiceUnavailable":  "服务暂时不可用。请稍后重试。",

		"Public.RequestTimeout":        "请求超时。请稍后重试。",
		"Public.RequestEntityTooLarge": "请求内容过大。请减小请求内容后重试。",
		"Public.UnprocessableEntity":   "执行输入校验失败，请修正输入后重试。",
		"Public.BadGateway":            "下游服务请求失败。请稍后重试。",
		"Public.GatewayTimeout":        "下游服务请求超时。请稍后重试。",
	},
}
