// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var releaseZhCN = i18n.I18nMessages{
	Locale: i18n.SimplifiedChinese,
	Messages: map[string]string{
		"BknProject.Release.InvalidVersion":      "版本号不合法 version 必须是 X.Y.Z 形式的纯数字版本号，不能有前导零，长度不超过 32 个字符。",
		"BknProject.Release.InvalidProjectID":    "项目 ID 不合法 project_id 长度不超过 40 个字符。",
		"BknProject.Release.InvalidNotes":        "发布说明不合法 notes 长度不超过 65535 字节。",
		"BknProject.Release.NotFound":            "发布记录不存在 该知识网络不是由发布产生的。",
		"BknProject.Release.NetworkExists":       "网络已存在 该版本对应 ID 的知识网络已经存在，且不是本谱系发布创建的，不会被覆盖。请换一个版本号或先处理该网络。",
		"BknProject.Release.VersionExists":       "版本已存在 该版本或其对应的网络 ID 已经发布，或该版本标签指向其他提交，请换一个版本号。",
		"BknProject.Release.BranchBehind":        "分支落后于 main main 上有该分支没有的变更，请先同步分支。",
		"BknProject.Release.Drift":               "线上定义已漂移 线上网络与 main 不一致，路径明细见 error_details。请先把漂移提交到 main 并同步分支，再重新发布。",
		"BknProject.Release.CapabilitiesSkipped": "能力绑定被跳过 网络已导入，但部分 Skill 或函数未能绑定，明细见 error_details。本次发布未记录，请补齐权限后用同一版本号重新发布。",
		"BknProject.Release.InvalidRequestBody":  "请求体不合法 请求体必须是合法的 JSON。",
		"BknProject.Release.InvalidAccepts":      "可接受项不合法 只能接受近似重名、部分未核对和索引未就绪。",
		"BknProject.Release.NotProjectRelease":   "不是本项目发布的版本 只能切换到本项目发布的版本。",
		"BknProject.Release.NotOwner":            "不是项目负责人 只有项目负责人可以发布、同步或切换使用方。",
		"BknProject.Release.ProjectArchived":     "项目已归档 请先取消归档，再发布。",
		"BknProject.Release.ChecksFailed":        "发布检查未全部通过 未通过的检查见 error_details，处理后重新打开发布确认。",
		"BknProject.Release.ReviewOutdated":      "发布确认已过期 打开发布确认之后，草稿或线上版本又变了，请重新打开发布确认。",
		"BknProject.Release.InternalError":       "内部错误 请联系管理员。",
	},
}
