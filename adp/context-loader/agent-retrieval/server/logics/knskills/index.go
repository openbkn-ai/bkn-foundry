// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package knskills 提供技能的浏览 / 阅读 / 执行能力，补齐 find_skills 之后的链路：
// find_skills 只回 skill_id + name + description，拿到之后既读不到 SKILL.md，
// 也列不出附属文件，更执行不了脚本。
//
// 这一层是薄的：出站两跳（元数据 + 对象存储正文）由 drivenadapters 完成，这里只管
// 面向大模型的整形——文本判定、体积截断、空结果说明。授权在执行工厂侧按账户判定。
package knskills

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/drivenadapters"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

const (
	// maxDocChars SKILL.md / 技能文件返回给模型的字符上限。
	// 超出即截断并显式标注，宁可让模型知道自己只看了一半，也不要静默丢尾。
	maxDocChars = 40000
	// maxStreamChars 执行结果 stdout / stderr 各自的字符上限。
	maxStreamChars = 8000
)

// ErrSkillIDRequired 入参缺 skill_id。
var ErrSkillIDRequired = errors.New("skill_id is required")

// ErrRelPathRequired read_skill_file 缺 rel_path。
var ErrRelPathRequired = errors.New("rel_path is required (take it from get_skill_content 的 files 清单)")

// ErrEntryShellRequired execute_skill 缺 entry_shell。
var ErrEntryShellRequired = errors.New("entry_shell is required (取自 SKILL.md 中声明的入口命令)")

// ListSkillsReq list_skills 入参。
type ListSkillsReq struct {
	Name     string `json:"name"`      // 可选，按名称模糊过滤
	Category string `json:"category"`  // 可选，按分类过滤
	Page     int    `json:"page"`      // 可选，页码，从 1 开始
	PageSize int    `json:"page_size"` // 可选，每页大小
}

// SkillEntry list_skills 的技能条目。
type SkillEntry struct {
	SkillID     string `json:"skill_id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Version     string `json:"version,omitempty"`
	Category    string `json:"category,omitempty"`
}

// ListSkillsResp list_skills 响应。
type ListSkillsResp struct {
	Entries    []SkillEntry `json:"entries"`
	TotalCount int          `json:"total_count"`
	Page       int          `json:"page,omitempty"`
	PageSize   int          `json:"page_size,omitempty"`
	Message    string       `json:"message,omitempty"`
}

// SkillFileEntry 技能包内的文件条目（渐进式阅读的下钻线索）。
type SkillFileEntry struct {
	RelPath  string `json:"rel_path"`
	FileType string `json:"file_type,omitempty"`
	Size     int64  `json:"size,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
}

// GetSkillContentResp get_skill_content 响应：SKILL.md 正文 + 包内文件清单。
type GetSkillContentResp struct {
	SkillID   string           `json:"skill_id"`
	Status    string           `json:"status,omitempty"`
	Content   string           `json:"content"`
	Truncated bool             `json:"truncated,omitempty"`
	Files     []SkillFileEntry `json:"files"`
	Message   string           `json:"message,omitempty"`
}

// ReadSkillFileReq read_skill_file 入参。
type ReadSkillFileReq struct {
	SkillID string `json:"skill_id"`
	RelPath string `json:"rel_path"`
}

// ReadSkillFileResp read_skill_file 响应。
// 二进制文件不回正文，只回元数据 + message 说明，避免把乱码灌进上下文。
type ReadSkillFileResp struct {
	SkillID   string `json:"skill_id"`
	RelPath   string `json:"rel_path"`
	MimeType  string `json:"mime_type,omitempty"`
	FileType  string `json:"file_type,omitempty"`
	Content   string `json:"content,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	Message   string `json:"message,omitempty"`
}

// ExecuteSkillReq execute_skill 入参。
type ExecuteSkillReq struct {
	SkillID    string `json:"skill_id"`
	EntryShell string `json:"entry_shell"`
	Timeout    int    `json:"timeout"` // 秒，可选
}

// ExecuteSkillResp execute_skill 响应。
type ExecuteSkillResp struct {
	SkillID       string `json:"skill_id"`
	ExitCode      int    `json:"exit_code"`
	Stdout        string `json:"stdout"`
	Stderr        string `json:"stderr,omitempty"`
	Truncated     bool   `json:"truncated,omitempty"`
	ExecutionTime int64  `json:"execution_time,omitempty"`
	WorkDir       string `json:"work_dir,omitempty"`
	Command       string `json:"command,omitempty"`
	Mocked        bool   `json:"mocked,omitempty"`
}

// KnSkillsService 技能浏览 / 阅读 / 执行。
type KnSkillsService interface {
	ListSkills(ctx context.Context, req *ListSkillsReq) (*ListSkillsResp, error)
	GetSkillContent(ctx context.Context, skillID string) (*GetSkillContentResp, error)
	ReadSkillFile(ctx context.Context, req *ReadSkillFileReq) (*ReadSkillFileResp, error)
	ExecuteSkill(ctx context.Context, req *ExecuteSkillReq) (*ExecuteSkillResp, error)
}

type knSkillsService struct {
	operator interfaces.DrivenOperatorIntegration
}

var (
	once     sync.Once
	instance KnSkillsService
)

// NewKnSkillsService 创建 KnSkillsService 单例。
func NewKnSkillsService() KnSkillsService {
	once.Do(func() {
		instance = &knSkillsService{operator: drivenadapters.NewOperatorIntegrationClient()}
	})
	return instance
}

// NewKnSkillsServiceWith 注入依赖创建（测试用）。
func NewKnSkillsServiceWith(operator interfaces.DrivenOperatorIntegration) KnSkillsService {
	return &knSkillsService{operator: operator}
}

// ListSkills 浏览已发布技能。与 find_skills 互补：那条按对象类召回，这条不需要知识网络上下文。
func (s *knSkillsService) ListSkills(ctx context.Context, req *ListSkillsReq) (*ListSkillsResp, error) {
	if req == nil {
		req = &ListSkillsReq{}
	}
	resp, err := s.operator.ListSkills(ctx, &interfaces.ListSkillsRequest{
		Name:     strings.TrimSpace(req.Name),
		Category: strings.TrimSpace(req.Category),
		Page:     req.Page,
		PageSize: req.PageSize,
	})
	if err != nil {
		return nil, err
	}

	out := &ListSkillsResp{
		Entries:    make([]SkillEntry, 0, len(resp.Entries)),
		TotalCount: resp.TotalCount,
		Page:       resp.Page,
		PageSize:   resp.PageSize,
	}
	for _, item := range resp.Entries {
		out.Entries = append(out.Entries, SkillEntry{
			SkillID:     item.SkillID,
			Name:        item.Name,
			Description: item.Description,
			Version:     item.Version,
			Category:    item.Category,
		})
	}
	if len(out.Entries) == 0 {
		out.Message = "没有匹配的已发布技能。技能需先在执行工厂注册并发布；也可放宽 name / category 过滤后重试。"
	}
	return out, nil
}

// GetSkillContent 取技能主文档正文与包内文件清单，供模型判断要不要继续下钻。
func (s *knSkillsService) GetSkillContent(ctx context.Context, skillID string) (*GetSkillContentResp, error) {
	skillID = strings.TrimSpace(skillID)
	if skillID == "" {
		return nil, ErrSkillIDRequired
	}
	resp, err := s.operator.GetSkillContent(ctx, skillID)
	if err != nil {
		return nil, err
	}

	content, truncated := truncateRunes(string(resp.Content), maxDocChars)
	out := &GetSkillContentResp{
		SkillID:   resp.SkillID,
		Status:    resp.Status,
		Content:   content,
		Truncated: truncated,
		Files:     make([]SkillFileEntry, 0, len(resp.Files)),
	}
	for _, f := range resp.Files {
		out.Files = append(out.Files, SkillFileEntry{
			RelPath:  f.RelPath,
			FileType: f.FileType,
			Size:     f.Size,
			MimeType: f.MimeType,
		})
	}
	if truncated {
		out.Message = "SKILL.md 超长已截断；需要完整内容请用 read_skill_file 分文件读取。"
	}
	return out, nil
}

// ReadSkillFile 读技能包内单个文件。二进制文件只回元数据不回正文。
func (s *knSkillsService) ReadSkillFile(ctx context.Context, req *ReadSkillFileReq) (*ReadSkillFileResp, error) {
	if req == nil {
		return nil, ErrSkillIDRequired
	}
	skillID := strings.TrimSpace(req.SkillID)
	if skillID == "" {
		return nil, ErrSkillIDRequired
	}
	relPath := strings.TrimSpace(req.RelPath)
	if relPath == "" {
		return nil, ErrRelPathRequired
	}

	resp, err := s.operator.ReadSkillFile(ctx, &interfaces.ReadSkillFileRequest{SkillID: skillID, RelPath: relPath})
	if err != nil {
		return nil, err
	}

	out := &ReadSkillFileResp{
		SkillID:  resp.SkillID,
		RelPath:  resp.RelPath,
		MimeType: resp.MimeType,
		FileType: resp.FileType,
	}
	if !isTextual(resp.MimeType, resp.Content) {
		out.Message = "该文件不是文本（mime_type=" + resp.MimeType + "），未返回正文。二进制内容请用技能包下载接口获取。"
		return out, nil
	}
	out.Content, out.Truncated = truncateRunes(string(resp.Content), maxDocChars)
	if out.Truncated {
		out.Message = "文件超长已截断，仅返回前 " + strconv.Itoa(maxDocChars) + " 个字符。"
	}
	return out, nil
}

// ExecuteSkill 在沙箱内执行技能入口命令。
// entry_shell 取自 SKILL.md 声明的入口；授权由执行工厂按账户强制。
func (s *knSkillsService) ExecuteSkill(ctx context.Context, req *ExecuteSkillReq) (*ExecuteSkillResp, error) {
	if req == nil {
		return nil, ErrSkillIDRequired
	}
	skillID := strings.TrimSpace(req.SkillID)
	if skillID == "" {
		return nil, ErrSkillIDRequired
	}
	entryShell := strings.TrimSpace(req.EntryShell)
	if entryShell == "" {
		return nil, ErrEntryShellRequired
	}

	resp, err := s.operator.ExecuteSkill(ctx, &interfaces.ExecuteSkillRequest{
		SkillID:    skillID,
		EntryShell: entryShell,
		Timeout:    req.Timeout,
	})
	if err != nil {
		return nil, err
	}

	stdout, stdoutTruncated := truncateRunes(resp.Stdout, maxStreamChars)
	stderr, stderrTruncated := truncateRunes(resp.Stderr, maxStreamChars)
	return &ExecuteSkillResp{
		SkillID:       resp.SkillID,
		ExitCode:      resp.ExitCode,
		Stdout:        stdout,
		Stderr:        stderr,
		Truncated:     stdoutTruncated || stderrTruncated,
		ExecutionTime: resp.ExecutionTime,
		WorkDir:       resp.WorkDir,
		Command:       resp.Command,
		Mocked:        resp.Mocked,
	}, nil
}

// truncateRunes 按字符（非字节）截断，避免切在多字节字符中间产出乱码。
func truncateRunes(text string, limit int) (string, bool) {
	if utf8.RuneCountInString(text) <= limit {
		return text, false
	}
	count := 0
	for idx := range text {
		if count == limit {
			return text[:idx], true
		}
		count++
	}
	return text, false
}

// isTextual 判定文件正文能否直接喂给模型。
// mime 是弱证据（对象存储常回 application/octet-stream），最终以正文是否合法 UTF-8 为准。
func isTextual(mimeType string, content []byte) bool {
	mimeType = strings.ToLower(strings.TrimSpace(mimeType))
	switch {
	case strings.HasPrefix(mimeType, "image/"),
		strings.HasPrefix(mimeType, "audio/"),
		strings.HasPrefix(mimeType, "video/"),
		strings.HasPrefix(mimeType, "application/zip"),
		strings.HasPrefix(mimeType, "application/x-tar"),
		strings.HasPrefix(mimeType, "application/gzip"),
		strings.HasPrefix(mimeType, "application/pdf"):
		return false
	}
	if !utf8.Valid(content) {
		return false
	}
	// 合法 UTF-8 里混着 NUL 基本只可能是二进制。
	return !strings.ContainsRune(string(content), '\x00')
}

// ExecuteEnabledEnv 控制 execute_skill 是否装配（MCP 工具面与 REST 路由共用）。
const ExecuteEnabledEnv = "EXECUTE_SKILL_ENABLED"

// legacyExecuteEnabledEnv 是 MCP-only 时期的旧名。那会儿开关只管工具面，
// 名字里带 MCP 是准确的；改成总闸后名字不再合适，但已经有人按旧名配过，
// 继续认它，免得升上来的部署突然把开着的能力关掉。
const legacyExecuteEnabledEnv = "MCP_EXECUTE_SKILL_ENABLED"

// ExecuteEnabled 判断本部署是否提供技能执行能力。默认关。
//
// 它是总闸而不只是工具面开关：关闭时 MCP 不装配 execute_skill，/in 与公开面的
// execute_skill 路由也不注册。文档把这条描述成「唯一的命令执行通道」，若 REST
// 那侧仍然开着，这句话就是假的——而看文档决定要不要开的人会据此误判风险。
func ExecuteEnabled() bool {
	for _, key := range []string{ExecuteEnabledEnv, legacyExecuteEnabledEnv} {
		value := strings.TrimSpace(os.Getenv(key))
		if value == "" {
			continue
		}
		enabled, err := strconv.ParseBool(value)
		return err == nil && enabled
	}
	return false
}
