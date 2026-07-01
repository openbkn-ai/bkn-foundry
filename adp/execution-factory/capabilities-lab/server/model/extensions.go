// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package model

type FunctionParameterDef struct {
	Name        string `json:"name"`
	Type        string `json:"type,omitempty"`
	Description string `json:"description,omitempty"`
}

type CreateFunctionCapabilityRequest struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Code        string                 `json:"code" binding:"required"`
	Category    string                 `json:"category,omitempty"`
	ServiceURL  string                 `json:"service_url,omitempty"`
	Group       GroupInput             `json:"group"`
	Inputs      []FunctionParameterDef `json:"inputs,omitempty"`
	Outputs     []FunctionParameterDef `json:"outputs,omitempty"`
}

type CreateFunctionCapabilityResponse struct {
	Capability Capability `json:"capability"`
}

type ExecutePythonRequest struct {
	Code    string                 `json:"code" binding:"required"`
	Event   map[string]interface{} `json:"event,omitempty"`
	Timeout int                    `json:"timeout,omitempty"`
}

type ExecutePythonResponse struct {
	Output     interface{} `json:"output,omitempty"`
	Stdout     string      `json:"stdout,omitempty"`
	Stderr     string      `json:"stderr,omitempty"`
	Error      string      `json:"error,omitempty"`
	DurationMs int64       `json:"duration_ms,omitempty"`
}

type PythonTemplateResponse struct {
	Template string `json:"template"`
}

type ParseMcpSseRequest struct {
	URL     string            `json:"url" binding:"required"`
	Mode    string            `json:"mode,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

type McpParsedTool struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type ParseMcpSseResponse struct {
	Tools []McpParsedTool `json:"tools"`
}

type SkillFileSummary struct {
	RelPath  string `json:"rel_path"`
	FileType string `json:"file_type,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
	Size     int64  `json:"size,omitempty"`
}

type SkillContentResponse struct {
	Content     string             `json:"content,omitempty"`
	FileType    string             `json:"file_type,omitempty"`
	Files       []SkillFileSummary `json:"files,omitempty"`
	DownloadURL string             `json:"download_url,omitempty"`
}

type ReadSkillFileRequest struct {
	RelPath      string `json:"rel_path" binding:"required"`
	ResponseMode string `json:"response_mode,omitempty"`
}

type ReadSkillFileResponse struct {
	RelPath  string `json:"rel_path"`
	URL      string `json:"url,omitempty"`
	Content  string `json:"content,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
	FileType string `json:"file_type,omitempty"`
	Size     int64  `json:"size,omitempty"`
}
