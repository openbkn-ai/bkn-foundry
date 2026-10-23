// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var cypherZhCN = i18n.I18nMessages{
	Locale: i18n.SimplifiedChinese,
	Messages: map[string]string{
		"BknBackend.Cypher.InvalidParameter":                             "Cypher 查询请求参数无效。请检查请求体与查询参数。",
		"BknBackend.Cypher.SyntaxError":                                  "Cypher 查询语法解析失败。请按提示位置修正查询语句。",
		"BknBackend.Cypher.Unsupported":                                  "Cypher 查询使用了本接口不支持的语法。请改写为受支持的只读子集。",
		"BknBackend.Cypher.InvalidQuery":                                 "Cypher 查询与知识网络模型不匹配。请对照模型检查标签、关系类型与属性。",
		"BknBackend.Cypher.LimitExceeded":                                "请求的 LIMIT 超过接口单次返回上限。请调小 LIMIT 并分页获取结果。",
		"BknBackend.Cypher.InternalError":                                "Cypher 查询编译失败。请联系管理员。",
		"BknBackend.Cypher.QueryFailed":                                  "编译后的查询执行失败。请稍后重试或联系管理员。",
		"BknBackend.Cypher.Forbidden":                                    "数据源拒绝读取查询涉及的资源。请联系管理员发布知识网络并完成代理账号同步，或授予这些对象类所绑定资源的访问权限。",
		"BknBackend.Cypher.PropertyForbidden":                            "查询读取了当前用户无完整访问权限的属性。请从查询中移除所列属性，或联系管理员授予这些属性的完整访问权限。",
		"BknBackend.Cypher.ReadBudgetExceeded":                           "查询需要读取的数据行超过单次执行上限。请为模式中的节点增加过滤条件，减少需要读取的数据行。",
		"BknBackend.Cypher.PropertyForbidden.Detail.NotFull":             "查询读取的每个属性都需要完整访问权限，以下属性未获授权：{{.properties}}。",
		"BknBackend.Cypher.InvalidParameter.Detail.NonSQLSource":         "查询读到的对象类存放在数据集或索引里，或者分布在多个数据源中。这类数据只支持基础写法：MATCH、OPTIONAL MATCH、WHERE、RETURN 属性和聚合、ORDER BY / SKIP / LIMIT、WITH 聚合后过滤和按值过滤（半连接）。CASE、coalesce、对分组再统计、比较两个列表、把节点传给下一个 MATCH 和 collect 都不可用（返回节点和关系可以），请改写。",
		"BknBackend.Cypher.InvalidParameter.Detail.NonSQLSourceTooLarge": "查询读到的对象类存放在数据集或索引里，或者分布在多个数据源中，需要逐表读取后关联；这次要读的数据超过上限或超时，请加条件缩小范围。",
		"BknBackend.Cypher.InvalidParameter.Detail.CartesianProduct":     "模式里有不相连的部分，其中至少两部分没有任何条件限制，结果会是两张表所有行的两两组合。请用关系类或 a.code = b.code 这类相等条件把它们连起来，或者给其中一边加上条件。",
		"BknBackend.Cypher.InvalidParameter.Detail.QueryRequired":        "query 不能为空。",
		"BknBackend.Cypher.InvalidParameter.Detail.QueryTooLong":         "query 长度不能超过 {{.max}} 个字符。",
		"BknBackend.Cypher.InvalidParameter.Detail.LimitExceeded":        "LIMIT 不能超过 {{.max}}。",
	},
}
