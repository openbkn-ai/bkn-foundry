// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

// ==================== Skill 浏览 / 读取 / 执行（执行工厂）====================
//
// find_skills 只回 skill_id + name + description，拿到之后无路可走：既读不到 SKILL.md，
// 也列不出附属文件，更执行不了脚本。这里补的是那条链路的出站契约——执行工厂
// (operator-integration) 的 internal-v1 技能接口。
//
// 发布态的文件接口只回对象存储的 presigned URL，不回正文，所以适配层统一走两跳：
// 先拿元数据再取正文，正文以 []byte 交给上层，由上层决定文本判定与截断。

// ListSkillsRequest 浏览已发布技能（技能市场）请求。
type ListSkillsRequest struct {
	Name     string `json:"name,omitempty"`     // 按名称模糊过滤
	Category string `json:"category,omitempty"` // 按分类过滤
	Page     int    `json:"page,omitempty"`     // 页码，从 1 开始
	PageSize int    `json:"page_size,omitempty"`
}

// SkillBrief 技能条目摘要（浏览用，不含文件与正文）。
type SkillBrief struct {
	SkillID     string `json:"skill_id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Version     string `json:"version,omitempty"`
	Category    string `json:"category,omitempty"`
	Status      string `json:"status,omitempty"`
}

// ListSkillsResponse 浏览已发布技能响应。
type ListSkillsResponse struct {
	Entries    []SkillBrief `json:"entries"`
	TotalCount int          `json:"total_count"`
	Page       int          `json:"page"`
	PageSize   int          `json:"page_size"`
}

// SkillFileSummary 技能包内的文件条目。
type SkillFileSummary struct {
	RelPath  string `json:"rel_path"`
	FileType string `json:"file_type,omitempty"`
	Size     int64  `json:"size,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
}

// GetSkillContentResponse 技能主文档（SKILL.md）正文 + 包内文件清单。
type GetSkillContentResponse struct {
	SkillID string
	Content []byte
	Status  string
	Files   []SkillFileSummary
}

// ReadSkillFileRequest 读取技能包内单个文件。
type ReadSkillFileRequest struct {
	SkillID string
	RelPath string
}

// ReadSkillFileResponse 技能包内单个文件的元数据 + 正文。
type ReadSkillFileResponse struct {
	SkillID  string
	RelPath  string
	MimeType string
	FileType string
	Content  []byte
}

// ExecuteSkillRequest 在沙箱内执行技能入口命令。
type ExecuteSkillRequest struct {
	SkillID    string `json:"-"`
	EntryShell string `json:"entry_shell"`
	Timeout    int    `json:"timeout,omitempty"` // 秒
}

// ExecuteSkillResponse 沙箱执行结果。
type ExecuteSkillResponse struct {
	SkillID       string `json:"skill_id"`
	SessionID     string `json:"session_id"`
	WorkDir       string `json:"work_dir"`
	Command       string `json:"command"`
	ExitCode      int    `json:"exit_code"`
	Stdout        string `json:"stdout"`
	Stderr        string `json:"stderr"`
	ExecutionTime int64  `json:"execution_time"`
	Mocked        bool   `json:"mocked"`
}
