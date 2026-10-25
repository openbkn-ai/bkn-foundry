// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var buildTaskZhCN = i18n.I18nMessages{
	Locale: i18n.SimplifiedChinese,
	Messages: map[string]string{
		"VegaBackend.BuildTask.NotFound":                                 "构建任务不存在。请检查构建任务ID是否存在",
		"VegaBackend.BuildTask.InvalidParameter.ResourceID":              "资源ID无效。请检查 resource_id 是否提供且存在",
		"VegaBackend.BuildTask.InvalidParameter.Mode":                    "构建任务模式无效。mode 必须为 streaming / batch 之一",
		"VegaBackend.BuildTask.InvalidParameter.PrimaryKeyFields":        "主键字段无效。请在资源 index_config 中配置至少一个不重复、存在于 schema 且类型受支持的 primary_key_fields 字段",
		"VegaBackend.BuildTask.InvalidParameter.IncrementalFields":       "增量字段无效。请在资源 index_config 中配置至少一个不重复、存在于 schema 且类型受支持的 incremental_fields 字段",
		"VegaBackend.BuildTask.StreamingUnsupported":                     "暂不支持流式构建任务。请改为创建批量构建任务",
		"VegaBackend.BuildTask.InvalidParameter.UnsupportedSchemaFields": "资源包含不支持索引构建的字段类型。请处理 type 为 other 的字段后再创建索引构建任务",
		"VegaBackend.BuildTask.InvalidParameter.Analyzer":                "全文分词器不可用。请选择 OpenSearch 已安装的分词器，或将受影响字段改为使用资源默认分词器",
		"VegaBackend.BuildTask.InvalidParameter.EmbeddingModel":          "向量字段缺少嵌入模型。请为向量特征配置 config.embedding_model，或在资源上配置 index_config.default_embedding_model",
		"VegaBackend.BuildTask.InvalidStateTransition":                   "构建任务状态转移非法。检查当前状态；start 要求 status 为 stopped 或 failed；stop 要求 status 为 pending 或 running",
		"VegaBackend.BuildTask.HasRunningExecution":                      "活动中的构建任务无法删除。请先停止 pending 或 running 任务，并等待 stopping 任务停止后再删除",
		"VegaBackend.BuildTask.IncrementalBaselineUnavailable":           "资源没有可用的增量同步基线。请先完成一次 full batch 构建，以建立可用索引和同步 checkpoint",
		"VegaBackend.BuildTask.IncrementalResetUnsupported":              "增量构建任务不支持 reset。请使用 reset=false 从 Resource checkpoint 继续；如需从头构建，请创建 full batch 任务",
		"VegaBackend.BuildTask.IndexConfigChanged":                       "资源索引构建配置已变化。请根据 Resource 当前配置创建新的构建任务",
		"VegaBackend.BuildTask.InternalError.CreateFailed":               "创建构建任务失败。请联系管理员",
		"VegaBackend.BuildTask.InternalError.ValidateAnalyzerFailed":     "校验全文分词器失败。请检查 OpenSearch 连接后重试",
		"VegaBackend.BuildTask.InternalError.GetFailed":                  "获取构建任务失败。请联系管理员",
		"VegaBackend.BuildTask.Exist":                                    "构建任务已存在。请检查资源是否已存在构建任务",
		"VegaBackend.BuildTask.Running":                                  "构建任务正在运行。请等待任务完成或停止后再操作",
		"VegaBackend.BuildTask.InvalidStatus":                            "无效的构建任务状态。请检查状态值",
		"VegaBackend.BuildTask.InvalidExecuteType":                       "无效的构建任务执行类型。请检查执行类型值",
		"VegaBackend.BuildTask.InternalError.UpdateFailed":               "更新构建任务失败。请联系管理员",
		"VegaBackend.BuildTask.InternalError.DeleteFailed":               "删除构建任务失败。请联系管理员",
		"VegaBackend.BuildTask.InternalError.GetAccountNamesFailed":      "获取账户名称失败。请联系管理员",
	},
}
