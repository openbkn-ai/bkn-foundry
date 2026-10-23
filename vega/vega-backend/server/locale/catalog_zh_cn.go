// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var catalogZhCN = i18n.I18nMessages{
	Locale: i18n.SimplifiedChinese,
	Messages: map[string]string{
		"VegaBackend.Catalog.InvalidParameter":                            "无效的目录参数。请检查参数",
		"VegaBackend.Catalog.InvalidParameter.Type":                       "无效的目录类型。请检查参数",
		"VegaBackend.Catalog.InvalidParameter.Name":                       "无效的目录名称。请检查参数",
		"VegaBackend.Catalog.InvalidParameter.ID":                         "请求体缺少 id 字段。请在请求体中携带 id 字段，且与路径参数保持一致",
		"VegaBackend.Catalog.InvalidParameter.ConnectorType":              "无效的连接类型。请检查参数",
		"VegaBackend.Catalog.InvalidParameter.ConnectorConfig":            "无效的连接配置。请检查参数",
		"VegaBackend.Catalog.LengthExceeded.Name":                         "目录名称长度超限。请缩短名称",
		"VegaBackend.Catalog.LengthExceeded.Description":                  "目录描述长度超限。请缩短描述",
		"VegaBackend.Catalog.EnabledFieldNotAllowed":                      "目录的启用状态不能通过 PUT /catalogs/{id} 修改。请改用 POST /catalogs/{id}/enable 或 /disable 切换启用状态",
		"VegaBackend.Catalog.InternalError.GetAccountNamesFailed":         "获取账号名称失败。请联系管理员",
		"VegaBackend.Catalog.NotFound":                                    "目录不存在。请检查目录ID",
		"VegaBackend.Catalog.NameExists":                                  "目录名称已存在。请使用其他名称",
		"VegaBackend.Catalog.IDMismatch":                                  "请求体中的目录 id 与路径不一致。请使请求体中的 id 字段与路径参数保持一致",
		"VegaBackend.Catalog.IDExists":                                    "目录ID已存在。请使用其他ID",
		"VegaBackend.Catalog.HasAssets":                                   "目录下存在资产，无法删除。请先删除目录下的所有资产",
		"VegaBackend.Catalog.IsDisabled":                                  "目录已禁用。请先启用目录",
		"VegaBackend.Catalog.InternalError":                               "目录内部错误。请联系管理员",
		"VegaBackend.Catalog.InternalError.CreateFailed":                  "创建目录失败。请联系管理员",
		"VegaBackend.Catalog.InternalError.CreateResourcesFailed":         "创建目录资源权限失败。请联系管理员",
		"VegaBackend.Catalog.InternalError.GetFailed":                     "获取目录失败。请联系管理员",
		"VegaBackend.Catalog.InternalError.UpdateFailed":                  "更新目录失败。请联系管理员",
		"VegaBackend.Catalog.InternalError.DeleteFailed":                  "删除目录失败。请联系管理员",
		"VegaBackend.Catalog.InternalError.TestConnectionFailed":          "测试目录连接失败。请联系管理员",
		"VegaBackend.Catalog.InternalError.EncryptFailed":                 "加密失败。请联系管理员",
		"VegaBackend.Catalog.InvalidParameter.SensitiveFieldNotEncrypted": "敏感字段未正确加密。请使用公钥加密敏感字段后再提交",
		"VegaBackend.Catalog.UpdateConflict":                              "目录配置在读取后已被更新。请重新加载目录配置后再提交修改",
	},
}
