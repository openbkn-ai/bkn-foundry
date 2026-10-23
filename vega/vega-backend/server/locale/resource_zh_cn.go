// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var resourceZhCN = i18n.I18nMessages{
	Locale: i18n.SimplifiedChinese,
	Messages: map[string]string{
		"VegaBackend.Resource.InvalidParameter":                    "无效的数据资源参数。请检查参数",
		"VegaBackend.Resource.Duplicated.FieldFeatureType":         "字段 {{.FieldName}} 已存在 {{.FieldFeatureType}} 类型的特征。请只保留一个该类型的字段特征",
		"VegaBackend.Resource.CategoryNotCreatable":                "该类型资源不允许通过 API 创建。通过该 API 仅可创建 'dataset' 与 'logical_view' 类型资源，其他类型请通过发现任务（discover）创建。",
		"VegaBackend.Resource.InvalidParameter.Type":               "无效的数据资源类型。请检查参数",
		"VegaBackend.Resource.InvalidParameter.Name":               "无效的数据资源名称。请检查参数",
		"VegaBackend.Resource.InvalidParameter.CatalogID":          "目录ID为必填项。请提供有效的目录ID",
		"VegaBackend.Resource.InvalidParameter.Analyzer":           "配置的全文分词器不可用。请选择当前索引能力快照支持的分词器",
		"VegaBackend.Resource.InvalidParameter.PrimaryKeyFields":   "主键字段无效。主键必须是不重复、存在于 schema 且类型受支持的字段",
		"VegaBackend.Resource.InvalidParameter.IncrementalFields":  "增量字段无效。增量字段必须是不重复、存在于 schema 且类型受支持的字段",
		"VegaBackend.Resource.LengthExceeded.Name":                 "数据资源名称长度超限。请缩短名称",
		"VegaBackend.Resource.LengthExceeded.Description":          "数据资源描述长度超限。请缩短描述",
		"VegaBackend.Resource.NotFound":                            "数据资源不存在。请检查数据资源ID",
		"VegaBackend.Resource.SourceReadForbidden":                 "数据源账号无权读取该资源。请检查连接的 Schema 范围，或为源表授予 SELECT 权限",
		"VegaBackend.Resource.NameExists":                          "数据资源名称已存在于该目录。请使用其他名称",
		"VegaBackend.Resource.IDExists":                            "数据资源ID已存在。请使用其他ID",
		"VegaBackend.Resource.CatalogNotFound":                     "数据资源所属目录不存在。请检查目录ID",
		"VegaBackend.Resource.IsDisabled":                          "数据资源已禁用。请先启用数据资源",
		"VegaBackend.Resource.AlreadyEnabled":                      "数据资源已处于启用状态。无需操作",
		"VegaBackend.Resource.AlreadyDisabled":                     "数据资源已处于禁用状态。无需操作",
		"VegaBackend.Resource.EnabledFieldNotAllowed":              "不能通过 PUT 修改数据资源启停状态。请使用资源启用或禁用动作",
		"VegaBackend.Resource.NotQueryable":                        "数据资源当前状态不支持查询。仅 active/deprecated 状态可查询；请先启用资源或刷新 schema",
		"VegaBackend.Resource.MetadataUnavailable":                 "数据资源元数据不可用。请重新探查并刷新资源字段；若源端资源已消失，请先恢复后再查询",
		"VegaBackend.Resource.InternalError":                       "数据资源内部错误。请联系管理员",
		"VegaBackend.Resource.InternalError.CreateFailed":          "创建数据资源失败。请联系管理员",
		"VegaBackend.Resource.InternalError.GetFailed":             "获取数据资源失败。请联系管理员",
		"VegaBackend.Resource.InternalError.UpdateFailed":          "更新数据资源失败。请联系管理员",
		"VegaBackend.Resource.InternalError.DeleteFailed":          "删除数据资源失败。请联系管理员",
		"VegaBackend.Resource.InternalError.SyncFailed":            "同步数据资源失败。请联系管理员",
		"VegaBackend.Resource.InternalError.InvalidCategory":       "无效的数据资源分类。请检查参数",
		"VegaBackend.Resource.InternalError.GetAccountNamesFailed": "获取账户名称失败。请联系管理员",
		"VegaBackend.Resource.UpdateConflict":                      "资源在读取后已被更新。请重新加载资源后再提交修改",
		"VegaBackend.Resource.DeleteConflict":                      "资源已不再处于失效且探查缺失状态。请重新加载资源后再删除",
		"VegaBackend.Resource.QueryValueExceedsKeywordLimit":       "查询值超过关键词最大索引长度。请缩短筛选值，或在支持时主动选择查询原始数据。",
	},
}
