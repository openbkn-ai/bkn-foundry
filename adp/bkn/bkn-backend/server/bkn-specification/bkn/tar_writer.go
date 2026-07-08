// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"archive/tar"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// WriteNetworkToTar serializes a BknDocument to a tar stream.
// The document is written as:
// - network.bkn (frontmatter only)
// - object_types/*.bkn for each ObjectType
// - relation_types/*.bkn for each RelationType
// - action_types/*.bkn for each ActionType
// - risk_types/*.bkn for each RiskType
// - concept_groups/*.bkn for each ConceptGroup
// - metrics/*.bkn for each Metric
// - SKILL.md (auto-generated)
// - CHECKSUM (auto-generated)
func WriteNetworkToTar(doc *BknNetwork, w io.Writer) error {
	tw := tar.NewWriter(w)
	defer func() { _ = tw.Close() }()

	now := time.Now()
	mfs := NewMemoryFileSystem()

	// Write network.bkn
	rootContent := SerializeBknNetwork(doc)
	mfs.AddFile("network.bkn", []byte(rootContent))
	if err := writeTarEntry(tw, "network.bkn", []byte(rootContent), now); err != nil {
		return err
	}

	// Write ObjectTypes
	for _, ot := range doc.ObjectTypes {
		content := SerializeObjectType(ot)
		path := "object_types/" + ot.ID + ".bkn"
		mfs.AddFile(path, []byte(content))
		if err := writeTarEntry(tw, path, []byte(content), now); err != nil {
			return err
		}
	}

	// Write RelationTypes
	for _, rt := range doc.RelationTypes {
		content := SerializeRelationType(rt)
		path := "relation_types/" + rt.ID + ".bkn"
		mfs.AddFile(path, []byte(content))
		if err := writeTarEntry(tw, path, []byte(content), now); err != nil {
			return err
		}
	}

	// Write ActionTypes
	for _, at := range doc.ActionTypes {
		content := SerializeActionType(at)
		path := "action_types/" + at.ID + ".bkn"
		mfs.AddFile(path, []byte(content))
		if err := writeTarEntry(tw, path, []byte(content), now); err != nil {
			return err
		}
	}

	// Write RiskTypes
	for _, rt := range doc.RiskTypes {
		content := SerializeRiskType(rt)
		path := "risk_types/" + rt.ID + ".bkn"
		mfs.AddFile(path, []byte(content))
		if err := writeTarEntry(tw, path, []byte(content), now); err != nil {
			return err
		}
	}

	// Build ObjectType lookup for ConceptGroup serialization
	otIndex := make(map[string]*BknObjectType, len(doc.ObjectTypes))
	for _, ot := range doc.ObjectTypes {
		otIndex[ot.ID] = ot
	}

	// Write ConceptGroups
	for _, cg := range doc.ConceptGroups {
		content := SerializeConceptGroup(cg, otIndex)
		path := "concept_groups/" + cg.ID + ".bkn"
		mfs.AddFile(path, []byte(content))
		if err := writeTarEntry(tw, path, []byte(content), now); err != nil {
			return err
		}
	}

	// Write Metrics
	for _, met := range doc.Metrics {
		content := SerializeMetric(met)
		path := "metrics/" + met.ID + ".bkn"
		mfs.AddFile(path, []byte(content))
		if err := writeTarEntry(tw, path, []byte(content), now); err != nil {
			return err
		}
	}

	// Write SKILL.md: use existing content if loaded, otherwise generate.
	skillContent := doc.SkillContent
	if skillContent == "" {
		skillContent = generateSkillMd(doc)
	}
	mfs.AddFile("SKILL.md", []byte(skillContent))
	if err := writeTarEntry(tw, "SKILL.md", []byte(skillContent), now); err != nil {
		return err
	}

	// Generate and write CHECKSUM
	checksumContent, err := GenerateChecksumFileWithFS(mfs, ".")
	if err != nil {
		return fmt.Errorf("failed to generate checksum: %w", err)
	}
	if err := writeTarEntry(tw, ChecksumFileName, []byte(checksumContent), now); err != nil {
		return err
	}

	return nil
}

// generateSkillMd generates SKILL.md content from BknNetwork.
func generateSkillMd(doc *BknNetwork) string {
	fm := doc.BknNetworkFrontmatter

	var sb strings.Builder

	// Header
	_, _ = fmt.Fprintf(&sb, "# %s - Agent 使用指南\n\n", fm.Name)
	_, _ = fmt.Fprintf(&sb, "> **网络ID**: %s  \n", fm.ID)
	_, _ = fmt.Fprintf(&sb, "> **版本**: %s  \n", fm.Version)
	if len(fm.Tags) > 0 {
		_, _ = fmt.Fprintf(&sb, "> **标签**: %s  \n", strings.Join(fm.Tags, ", "))
	}
	_, _ = fmt.Fprintf(&sb, "\n")

	// Overview
	_, _ = fmt.Fprintf(&sb, "## 网络概览\n\n")
	if doc.Description != "" {
		_, _ = fmt.Fprintf(&sb, "%s\n\n", doc.Description)
	}

	// Objects table
	if len(doc.ObjectTypes) > 0 {
		_, _ = fmt.Fprintf(&sb, "### 核心对象\n\n")
		_, _ = fmt.Fprintf(&sb, "| 对象 | 文件路径 | 说明 |\n")
		_, _ = fmt.Fprintf(&sb, "|------|----------|------|\n")
		ots := append([]*BknObjectType(nil), doc.ObjectTypes...)
		sort.Slice(ots, func(i, j int) bool { return ots[i].ID < ots[j].ID })
		for _, ot := range ots {
			path := "object_types/" + ot.ID + ".bkn"
			_, _ = fmt.Fprintf(&sb, "| %s | `%s` | %s |\n", ot.Name, path, ot.Description)
		}
		_, _ = fmt.Fprintf(&sb, "\n")
	}

	// Relations table
	if len(doc.RelationTypes) > 0 {
		_, _ = fmt.Fprintf(&sb, "### 核心关系\n\n")
		_, _ = fmt.Fprintf(&sb, "| 关系 | 文件路径 | 说明 |\n")
		_, _ = fmt.Fprintf(&sb, "|------|----------|------|\n")
		rts := append([]*BknRelationType(nil), doc.RelationTypes...)
		sort.Slice(rts, func(i, j int) bool { return rts[i].ID < rts[j].ID })
		for _, rt := range rts {
			path := "relation_types/" + rt.ID + ".bkn"
			_, _ = fmt.Fprintf(&sb, "| %s | `%s` | %s |\n", rt.Name, path, rt.Description)
		}
		_, _ = fmt.Fprintf(&sb, "\n")
	}

	// Actions table
	if len(doc.ActionTypes) > 0 {
		_, _ = fmt.Fprintf(&sb, "### 可用行动\n\n")
		_, _ = fmt.Fprintf(&sb, "| 行动 | 文件路径 | 说明 |\n")
		_, _ = fmt.Fprintf(&sb, "|------|----------|------|\n")
		ats := append([]*BknActionType(nil), doc.ActionTypes...)
		sort.Slice(ats, func(i, j int) bool { return ats[i].ID < ats[j].ID })
		for _, at := range ats {
			path := "action_types/" + at.ID + ".bkn"
			_, _ = fmt.Fprintf(&sb, "| %s | `%s` | %s |\n", at.Name, path, at.Description)
		}
		_, _ = fmt.Fprintf(&sb, "\n")
	}

	// Metrics table
	if len(doc.Metrics) > 0 {
		sb.WriteString("### 指标（Metrics）\n\n")
		sb.WriteString("| 指标 | 文件路径 | 说明 |\n")
		sb.WriteString("|------|----------|------|\n")
		mts := append([]*BknMetric(nil), doc.Metrics...)
		sort.Slice(mts, func(i, j int) bool { return mts[i].ID < mts[j].ID })
		for _, met := range mts {
			path := "metrics/" + met.ID + ".bkn"
			sb.WriteString(fmt.Sprintf("| %s | `%s` | %s |\n", met.Name, path, met.Description))
		}
		sb.WriteString("\n")
	}

	// Directory structure
	_, _ = fmt.Fprintf(&sb, "## 目录结构\n\n")
	_, _ = fmt.Fprintf(&sb, "```\n")
	_, _ = fmt.Fprintf(&sb, ".\n")
	_, _ = fmt.Fprintf(&sb, "├── network.bkn\n")
	_, _ = fmt.Fprintf(&sb, "├── SKILL.md\n")
	_, _ = fmt.Fprintf(&sb, "├── CHECKSUM\n")
	if len(doc.ObjectTypes) > 0 {
		_, _ = fmt.Fprintf(&sb, "├── object_types/\n")
	}
	if len(doc.RelationTypes) > 0 {
		_, _ = fmt.Fprintf(&sb, "├── relation_types/\n")
	}
	if len(doc.ActionTypes) > 0 {
		_, _ = fmt.Fprintf(&sb, "└── action_types/\n")
	}
	if len(doc.ConceptGroups) > 0 {
		_, _ = fmt.Fprintf(&sb, "├── concept_groups/\n")
	}
	if len(doc.RiskTypes) > 0 {
		_, _ = fmt.Fprintf(&sb, "├── risk_types/\n")
	}
	if len(doc.Metrics) > 0 {
		_, _ = fmt.Fprintf(&sb, "├── metrics/\n")
	}
	_, _ = fmt.Fprintf(&sb, "```\n\n")

	// Usage suggestions
	_, _ = fmt.Fprintf(&sb, "## 使用建议\n\n")
	_, _ = fmt.Fprintf(&sb, "### 查询场景\n\n")
	_, _ = fmt.Fprintf(&sb, "1. **获取所有对象定义**\n")
	_, _ = fmt.Fprintf(&sb, "   - 查看 `object_types/` 目录下的文件\n\n")
	_, _ = fmt.Fprintf(&sb, "2. **查找关系定义**\n")
	_, _ = fmt.Fprintf(&sb, "   - 查看 `relation_types/` 目录下的文件\n\n")
	if len(doc.ActionTypes) > 0 {
		_, _ = fmt.Fprintf(&sb, "### 运维场景\n\n")
		_, _ = fmt.Fprintf(&sb, "1. **执行运维操作**\n")
		_, _ = fmt.Fprintf(&sb, "   - 查看 `action_types/` 目录下的行动定义\n")
		_, _ = fmt.Fprintf(&sb, "   - 了解触发条件和参数绑定\n\n")
	}

	// Index tables
	_, _ = fmt.Fprintf(&sb, "## 索引表\n\n")
	_, _ = fmt.Fprintf(&sb, "### 按类型索引\n\n")
	if len(doc.ObjectTypes) > 0 {
		_, _ = fmt.Fprintf(&sb, "- **对象定义**: `object_types/`\n")
	}
	if len(doc.RelationTypes) > 0 {
		_, _ = fmt.Fprintf(&sb, "- **关系定义**: `relation_types/`\n")
	}
	if len(doc.ActionTypes) > 0 {
		_, _ = fmt.Fprintf(&sb, "- **行动定义**: `action_types/`\n")
	}
	if len(doc.ConceptGroups) > 0 {
		_, _ = fmt.Fprintf(&sb, "- **概念分组**: `concept_groups/`\n")
	}
	if len(doc.RiskTypes) > 0 {
		_, _ = fmt.Fprintf(&sb, "- **风险定义**: `risk_types/`\n")
	}
	if len(doc.Metrics) > 0 {
		_, _ = fmt.Fprintf(&sb, "- **指标定义**: `metrics/`\n")
	}
	_, _ = fmt.Fprintf(&sb, "\n")

	_, _ = fmt.Fprintf(&sb, "1. 本网络由 BKN SDK 自动生成 SKILL.md\n")
	_, _ = fmt.Fprintf(&sb, "2. 所有定义遵循 BKN 规范\n")
	_, _ = fmt.Fprintf(&sb, "3. 使用 CHECKSUM 文件验证网络完整性\n")

	return sb.String()
}

func writeTarEntry(tw *tar.Writer, name string, data []byte, modTime time.Time) error {
	header := &tar.Header{
		Name:    name,
		Size:    int64(len(data)),
		Mode:    0644,
		ModTime: modTime,
	}
	if err := tw.WriteHeader(header); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}
