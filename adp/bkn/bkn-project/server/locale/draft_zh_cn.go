// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var draftZhCN = i18n.I18nMessages{
	Locale: i18n.SimplifiedChinese,
	Messages: map[string]string{
		"BknProject.Draft.NoCatalogs":            "项目没有数据目录 请先为项目选择结构化数据所在的数据目录，再起草本体。",
		"BknProject.Draft.NotFound":              "草稿不存在 该项目还没有起草本体。",
		"BknProject.Draft.Drafting":              "正在起草 该项目的本体正在起草，请等待完成后再重新起草。",
		"BknProject.Draft.InternalError":         "内部错误 请联系管理员。",
		"BknProject.Draft.InvalidRequestBody":    "请求体不合法 请求体必须是合法的 JSON。",
		"BknProject.Draft.EmptyEdit":             "没有要修改的内容 请至少提供 adopted、name 或 description 中的一项。",
		"BknProject.Draft.InvalidName":           "名称不合法 名称不能为空，最长 128 个字符。",
		"BknProject.Draft.InvalidDescription":    "描述不合法 描述最长 1000 个字符。",
		"BknProject.Draft.EndpointNotAdopted":    "对象类未采用 请先采用关系类两端的对象类，再采用该关系类。",
		"BknProject.Draft.ItemNotFound":          "草稿项不存在 草稿中没有该项。",
		"BknProject.Draft.ProjectArchived":       "项目已归档 请先取消归档，再修改草稿。",
		"BknProject.Draft.NotReady":              "草稿未就绪 只有起草完成的草稿才能修改。",
		"BknProject.Draft.Stale":                 "草稿已被修改 加载之后草稿已被他人修改，请重新加载后再试。",
		"BknProject.Draft.HasDecisions":          "草稿已有决定 重新起草会丢弃草稿上已做的决定，确认丢弃后才能重新起草。",
		"BknProject.Draft.InvalidIdentity":       "唯一标识不合法 请给出该对象类的一个或多个不重复的属性，类型须为 string、text、integer 或 unsigned integer。",
		"BknProject.Draft.RelationName":          "{{.object_type}}的{{.field}}",
		"BknProject.Draft.InvalidIncrementalKey": "更新字段不合法 请给出该对象类的一个属性，记录每次变化时它的值都会增大，类型须为 integer、unsigned integer、string、date、time、datetime 或 timestamp；传空值可清除。",
		"BknProject.Draft.NotRestorable":         "字段无法恢复 该字段因类型无法映射到 BKN 而被排除。",
		"BknProject.Draft.FieldNotFound":         "源字段不存在 该字段已不在它的表中，或你无权读取该表，请重新起草。",
		"BknProject.Draft.NothingAdopted":        "没有采纳任何对象类 至少采纳一个对象类，才能确认草稿。",
		"BknProject.Draft.InvalidItem":           "草稿项不完整 有已采纳的项缺少必要信息，无法写成定义，见 error_details。请重新起草。",
		"BknProject.Draft.NotOwner":              "不是项目负责人 只有项目负责人可以确认草稿。",
		"BknProject.Check.NotOwner":              "不是项目负责人 只有项目负责人可以重新运行校验。",
		"BknProject.Check.NotFound":              "还没有校验 本体确认后会自动校验。",
		"BknProject.Check.NothingConfirmed":      "本体还没有确认 请先确认本体草稿，确认后会自动校验。",
		"BknProject.Check.ConfirmAgain":          "请重新确认 确认之后草稿又改过，上次确认的内容已无法校验，请再确认一次。",
		"BknProject.Check.Running":               "正在校验 本次确认的内容正在校验，请等待完成。",
		"BknProject.Index.NotOwner":              "不是项目负责人 只有项目负责人可以重新构建索引。",
		"BknProject.Index.NothingConfirmed":      "本体还没有确认 请先确认本体草稿，确认后会自动构建索引。",
		"BknProject.Index.ConfirmAgain":          "请重新确认 确认之后草稿又改过，无法再为上次确认的内容构建索引，请再确认一次。",
		"BknProject.Understand.InvalidApplyMode": "写入方式不合法 请给出 fill_empty（只填写空缺的内容）或 force（覆盖名称和描述）。",
		"BknProject.Understand.NotOwner":         "不是项目负责人 只有项目负责人可以对项目数据进行语义理解。",
		"BknProject.Understand.NoCatalogs":       "还没有数据目录 请先为项目选择存放结构化数据的数据目录，再进行语义理解。",
		"BknProject.Draft.PartialNotAccepted":    "草稿不完整 起草时有表没能读取，草稿里没有这些表的内容；确认时请明确认可，见 error_details。",
		"BknProject.Draft.UndecidedNotAccepted":  "还有没决定的项 有些表和关系起草时没有采纳，也还没人决定要不要；确认时请明确认可暂不纳入，或先处理它们，见 error_details。",
		"BknProject.Draft.HasPending":            "还有待你决定的事 确认前请先为已采纳的对象类选定唯一标识，并处理连接了未采纳对象类的关系，见 error_details。",
		"BknProject.Draft.CatalogMissing":        "数据目录不存在 项目引用的数据目录已不存在，请先更换项目的数据目录，再起草。",
		"BknProject.Draft.MissingUpdateTime":     "缺少更新时间 请提供 update_time，即所修改草稿的更新时间。",
		"BknProject.Draft.NeedsIdentity":         "未选定唯一标识 请先为该对象类选定唯一标识，再采用它。",
	},
}
