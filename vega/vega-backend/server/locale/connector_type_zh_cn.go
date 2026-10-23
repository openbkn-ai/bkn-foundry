// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var connectorTypeZhCN = i18n.I18nMessages{
	Locale: i18n.SimplifiedChinese,
	Messages: map[string]string{
		"VegaBackend.ConnectorType.InvalidParameter":               "无效的连接器类型参数。请检查参数",
		"VegaBackend.ConnectorType.InvalidParameter.Type":          "无效的连接器类型。请检查参数",
		"VegaBackend.ConnectorType.InvalidParameter.Name":          "无效的连接器类型名称。请检查参数",
		"VegaBackend.ConnectorType.InvalidParameter.Mode":          "无效的连接器类型模式。请检查参数",
		"VegaBackend.ConnectorType.InvalidParameter.Category":      "无效的连接器类型分类。请检查参数",
		"VegaBackend.ConnectorType.InvalidParameter.Endpoint":      "无效的连接器类型端点。请检查参数",
		"VegaBackend.ConnectorType.BadRequest":                     "参数无效。请检查参数",
		"VegaBackend.ConnectorType.NotFound":                       "连接器类型不存在。请检查连接器类型ID",
		"VegaBackend.ConnectorType.TypeExists":                     "连接器类型已存在。请使用其他类型",
		"VegaBackend.ConnectorType.NameExists":                     "连接器类型名称已存在。请使用其他名称",
		"VegaBackend.ConnectorType.TypeMismatch":                   "请求体中的连接器类型与路径不一致。省略请求体中的 type 字段，或使其与路径参数保持一致",
		"VegaBackend.ConnectorType.InternalError":                  "连接器类型内部错误。请联系管理员",
		"VegaBackend.ConnectorType.FieldConfigUnavailable":         "连接器字段配置暂不可用。请确认连接器实现可用后重试",
		"VegaBackend.ConnectorType.InternalError.RegisterFailed":   "注册连接器类型失败。请联系管理员",
		"VegaBackend.ConnectorType.InternalError.GetFailed":        "获取连接器类型失败。请联系管理员",
		"VegaBackend.ConnectorType.InternalError.UpdateFailed":     "更新连接器类型失败。请联系管理员",
		"VegaBackend.ConnectorType.InternalError.DeleteFailed":     "删除连接器类型失败。请联系管理员",
		"VegaBackend.ConnectorType.PostgreSQL.Options.Name":        "连接参数",
		"VegaBackend.ConnectorType.PostgreSQL.Options.Description": "可选；仅支持 sslmode、connect_timeout、application_name、fallback_application_name、statement_timeout、lock_timeout、idle_in_transaction_session_timeout；connect_timeout 为非负整数秒，会话超时为毫秒或带 PostgreSQL 时间单位的字符串，0 表示禁用；sslmode 默认为 disable，不支持 SSL 文件；客户端编码使用驱动默认 UTF8，不支持配置",
	},
}
