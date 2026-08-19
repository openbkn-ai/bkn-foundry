// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

// ==================== Skill Browse / Read / Execute (Execution Factory) ====================.
//
// find_skills only returns skill_id + name + description. After getting it, there is no way to go: neither SKILL.md can be read,
// Attached files cannot be listed, nor can scripts be executed. What is added here is the outbound contract of that link - the execution factory.
// (operator-integration) internal-v1 skill interface.
//
// The published file interface only returns the presigned URL of the object storage, not the text, so the adaptation layer takes two hops:
// Get the metadata first and then the text. The text is handed to the upper layer in []byte, and the upper layer determines text judgment and truncation.

// ListSkillsRequest Browse published skills (Skills Marketplace) requests.
type ListSkillsRequest struct {
	Name     string `json:"name,omitempty"`     // Fuzzy-filter by name.
	Category string `json:"category,omitempty"` // Filter by category.
	Page     int    `json:"page,omitempty"`     // Page number, starting from 1.
	PageSize int    `json:"page_size,omitempty"`
}

// SkillBrief Summary of skill entries (for browsing, excluding files and text).
type SkillBrief struct {
	SkillID     string `json:"skill_id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Version     string `json:"version,omitempty"`
	Category    string `json:"category,omitempty"`
	Status      string `json:"status,omitempty"`
}

// ListSkillsResponse Browse published skill responses.
type ListSkillsResponse struct {
	Entries    []SkillBrief `json:"entries"`
	TotalCount int          `json:"total_count"`
	Page       int          `json:"page"`
	PageSize   int          `json:"page_size"`
}

// SkillFileSummary The file entry in the skill package.
type SkillFileSummary struct {
	RelPath  string `json:"rel_path"`
	FileType string `json:"file_type,omitempty"`
	Size     int64  `json:"size,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
}

// GetSkillContentResponse Skill master document (SKILL.md) text + file list in the package.
type GetSkillContentResponse struct {
	SkillID string
	Content []byte
	Status  string
	Files   []SkillFileSummary
}

// ReadSkillFileRequest reads a single file in the skill package.
type ReadSkillFileRequest struct {
	SkillID string
	RelPath string
}

// ReadSkillFileResponse Metadata + text of a single file within the skill package.
type ReadSkillFileResponse struct {
	SkillID  string
	RelPath  string
	MimeType string
	FileType string
	Content  []byte
}

// ExecuteSkillRequest executes the skill entry command in the sandbox.
type ExecuteSkillRequest struct {
	SkillID    string `json:"-"`
	EntryShell string `json:"entry_shell"`
	Timeout    int    `json:"timeout,omitempty"` // seconds.
}

// ExecuteSkillResponse sandbox execution result.
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
