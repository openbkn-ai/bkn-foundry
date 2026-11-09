// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package locale

import "github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

var releaseZhCN = i18n.I18nMessages{
	Locale: i18n.SimplifiedChinese,
	Messages: map[string]string{
		"BknProject.Release.SourcePermission":    "发布者需要全部来源知识库的查询权限，以及目标知识网络的配置权限。",
		"BknProject.Rollback.PrecheckRequired":   "未做回退预检 请先读取回退预检并带上其签名；没有预检不能回退。",
		"BknProject.Rollback.PlanOutdated":       "回退预检已过期 读取预检之后使用方又有切换，或调度有变化；请重新读取再回退。",
		"BknProject.Release.NotPrepared":         "版本尚未准备 这个版本还没有写入网络，请先准备发布。",
		"BknProject.Release.DraftHasChanges":     "网络有未发布的修改 网络里有尚未发布的在线修改，也不在项目里。请选择先把它们提交到项目，还是覆盖。",
		"BknProject.Release.DraftChanged":        "网络已被修改 准备发布之后网络又被修改了，请重新准备发布。",
		"BknProject.Release.DraftOutdated":       "需要确认带回改动 运行版本已经回退，这次发布会把回退掉的改动重新带回。确认后才能发布，详见 error_details。",
		"BknProject.Release.RunningChanged":      "运行版本已变化 发布期间运行版本被其他人改变了，请刷新后再试。",
		"BknProject.Release.NothingToPublish":    "网络没有变化 确认的内容与运行版本相同，不需要发布。",
		"BknProject.Release.ValidationFailed":    "发布前检查没有通过 网络还有需要修改的地方，详见 error_details。",
		"BknProject.Release.NotVersioned":        "网络未开启版本管理 这个项目的网络还没有开启版本管理，请先开启。",
		"BknProject.Release.VersionPurged":       "版本只保留了记录 这个版本的内容已经清理，只保留了记录。可以从项目历史里复制后重新发布。",
		"BknProject.Release.NotReady":            "网络还没准备好 网络正在准备检索或数据访问，请稍后再试。",
		"BknProject.Rollback.BrokenReferences":   "无法回退 目标版本引用的内容已经不存在，详见 error_details。",
		"BknProject.Rollback.NotActivatable":     "不能切换到这个版本 这个版本已废止或只保留了记录。",
		"BknProject.Rollback.EditionRestricted":  "社区版只能回退到上一版 更早的版本可以复制为草稿后重新发布。",
		"BknProject.Rollback.NoPrevious":         "没有可回退的版本 网络还没有上一版。",
		"BknProject.Rollback.NotProjectRelease":  "不是本项目发布的版本 使用方正在用其他项目发布的版本，只有该项目可以回退它。",
		"BknProject.Release.InvalidVersion":      "版本号不合法 version 必须是 X.Y.Z 形式的纯数字版本号，不能有前导零，长度不超过 32 个字符。",
		"BknProject.Release.InvalidProjectID":    "项目 ID 不合法 project_id 长度不超过 40 个字符。",
		"BknProject.Release.InvalidNotes":        "发布说明不合法 notes 长度不超过 65535 字节。",
		"BknProject.Release.NotFound":            "发布记录不存在 该知识网络不是由发布产生的。",
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
		"BknProject.Release.RegressionNotFound":  "没有回归结果 这个版本还没有运行过发布后回归。",
		"BknProject.Release.RegressionRunning":   "回归正在运行 请等这一轮结束后再运行。",
		"BknProject.Release.InternalError":       "内部错误 请联系管理员。",
	},
}
