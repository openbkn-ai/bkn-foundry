package common

import (
	"os"
	"strings"
)

// SkillReadAuthzMode 内部（internal-v1）技能读接口的授权模式。
//
// 历史上 GetSkillContent / ReadSkillFile 只在公开接口上做授权，内部接口一律放行——
// 授权被押给调用方（bkn-agent 只读它自己挂载过的技能）。context-loader 把这两个接口
// 包成 MCP 工具之后这个前提不再成立：MCP 客户端的 skill_id 是自己填的，内部接口再放行
// 就等于任意账户可读任意技能全文。
//
// 直接翻成强制会打断存量调用方（它们未必都带得出账户上下文），所以按三段式走：
// 先 shadow 只记日志不拦，观察线上没有误伤了再翻 enforce。
type SkillReadAuthzMode string

const (
	// SkillReadAuthzOff 内部接口完全不查授权（翻车时的回退档）。
	SkillReadAuthzOff SkillReadAuthzMode = "off"
	// SkillReadAuthzShadow 内部接口查授权但不拦，未通过只打日志。默认档。
	SkillReadAuthzShadow SkillReadAuthzMode = "shadow"
	// SkillReadAuthzEnforce 内部接口按账户强制授权，未通过返回 403。
	SkillReadAuthzEnforce SkillReadAuthzMode = "enforce"
)

// SkillReadAuthzModeEnv 控制内部技能读接口授权模式的环境变量。
const SkillReadAuthzModeEnv = "SKILL_INTERNAL_READ_AUTHZ"

// GetSkillReadAuthzMode 读取内部技能读接口的授权模式，取值非法时按默认档 shadow。
func GetSkillReadAuthzMode() SkillReadAuthzMode {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(SkillReadAuthzModeEnv))) {
	case string(SkillReadAuthzOff):
		return SkillReadAuthzOff
	case string(SkillReadAuthzEnforce):
		return SkillReadAuthzEnforce
	default:
		return SkillReadAuthzShadow
	}
}
