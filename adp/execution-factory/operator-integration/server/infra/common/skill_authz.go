package common

import (
	"os"
	"strings"
)

// SkillReadAuthzMode The authorization mode of the internal (internal-v1) skill reading interface.
//
// Historically, GetSkillContent / ReadSkillFile was only authorized on public interfaces, and all internal interfaces were allowed -.
// Authorization is pledged to the caller (bkn-agent only reads skills it has mounted). context-loader combines these two interfaces.
// After being packaged into an MCP tool, this premise is no longer true: the skill_id of the MCP client is filled in by yourself, and the internal interface is released.
// It means that any account can read the full text of any skill.
//
// Translating directly into force will interrupt the existing callers (they may not all bring out the account context), so follow the three-stage approach:
// First, shadow only records logs without blocking, and then turns to enforce after observing that there are no accidental injuries on the line.
type SkillReadAuthzMode string

const (
	// SkillReadAuthzOff The internal interface does not check authorization at all (the fallback gear when rolling over).
	SkillReadAuthzOff SkillReadAuthzMode = "off"
	// SkillReadAuthzShadow internal interface checks authorization but does not block it. If it fails, it will only log. Default file.
	SkillReadAuthzShadow SkillReadAuthzMode = "shadow"
	// SkillReadAuthzEnforce internal interface enforces authorization by account, and returns 403 if failed.
	SkillReadAuthzEnforce SkillReadAuthzMode = "enforce"
)

// SkillReadAuthzModeEnv Environment variable that controls the authorization mode of the internal skill read interface.
const SkillReadAuthzModeEnv = "SKILL_INTERNAL_READ_AUTHZ"

// GetSkillReadAuthzMode reads the authorization mode of the internal skill reading interface. If the value is illegal, use the default file shadow.
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
