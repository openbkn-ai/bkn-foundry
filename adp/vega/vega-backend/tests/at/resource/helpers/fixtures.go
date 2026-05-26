// Copyright 2026 kowell.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package helpers

import (
	"fmt"
	"strings"
	"time"
)

// ========== 基本Payload生成函数 ==========

// BuildCreatePayload 构建基本的resource创建payload（必填字段）
func BuildCreatePayload(catalogID string) map[string]any {
	return map[string]any{
		"catalog_id": catalogID,
		"name":       GenerateUniqueName("test-resource"),
	}
}

// BuildFullCreatePayload 构建完整的resource创建payload（包含所有可选字段）
func BuildFullCreatePayload(catalogID string) map[string]any {
	return map[string]any{
		"catalog_id":  catalogID,
		"name":        GenerateUniqueName("full-resource"),
		"description": "完整的测试resource，包含所有可选字段",
		"tags":        []string{"test", "resource", "at", "full-fields"},
		"category":    "table",
	}
}

// BuildMinimalPayload 构建最小字段的resource payload
func BuildMinimalPayload(catalogID string) map[string]any {
	return map[string]any{
		"catalog_id": catalogID,
		"name":       GenerateUniqueName("minimal-resource"),
	}
}

// ========== Name相关Payload ==========

// BuildPayloadWithLongName 构建指定长度name的resource payload
func BuildPayloadWithLongName(catalogID string, length int) map[string]any {
	return map[string]any{
		"catalog_id": catalogID,
		"name":       BuildUniqueStringWithLength(length),
	}
}

// BuildPayloadWithMinName 构建最小长度name的resource payload（单字符）
func BuildPayloadWithMinName(catalogID string) map[string]any {
	chars := "abcdefghijklmnopqrstuvwxyz"
	idx := time.Now().UnixNano() % int64(len(chars))
	return map[string]any{
		"catalog_id": catalogID,
		"name":       string(chars[idx]),
	}
}

// BuildPayloadWithEmptyName 构建空name字段的resource payload
func BuildPayloadWithEmptyName(catalogID string) map[string]any {
	return map[string]any{
		"catalog_id": catalogID,
		"name":       "",
	}
}

// BuildPayloadWithMissingName 构建缺少name字段的resource payload
func BuildPayloadWithMissingName(catalogID string) map[string]any {
	return map[string]any{
		"catalog_id": catalogID,
	}
}

// BuildPayloadWithWhitespaceName 构建只有空格name的resource payload
func BuildPayloadWithWhitespaceName(catalogID string) map[string]any {
	return map[string]any{
		"catalog_id": catalogID,
		"name":       "   ",
	}
}

// BuildPayloadWithSpecialCharsName 构建包含特殊字符名称的resource payload
func BuildPayloadWithSpecialCharsName(catalogID string, specialName string) map[string]any {
	return map[string]any{
		"catalog_id": catalogID,
		"name":       specialName,
	}
}

// ========== Description相关Payload ==========

// BuildPayloadWithLongDescription 构建超长description的resource payload
func BuildPayloadWithLongDescription(catalogID string, descriptionLength int) map[string]any {
	longDescription := strings.Repeat("这是一个很长的注释。", descriptionLength/10+1)
	return map[string]any{
		"catalog_id":  catalogID,
		"name":        GenerateUniqueName("long-description-resource"),
		"description": longDescription,
	}
}

// BuildPayloadWithExactDescription 构建指定长度description的resource payload
func BuildPayloadWithExactDescription(catalogID string, descriptionLength int) map[string]any {
	description := strings.Repeat("c", descriptionLength)
	return map[string]any{
		"catalog_id":  catalogID,
		"name":        GenerateUniqueName("exact-description-resource"),
		"description": description,
	}
}

// ========== Tags相关Payload ==========

// BuildPayloadWithManyTags 构建包含大量tags的resource payload
func BuildPayloadWithManyTags(catalogID string, tagCount int) map[string]any {
	tags := make([]string, tagCount)
	for i := range tags {
		tags[i] = fmt.Sprintf("tag-%d", i)
	}
	return map[string]any{
		"catalog_id": catalogID,
		"name":       GenerateUniqueName("many-tags-resource"),
		"tags":       tags,
	}
}

// BuildPayloadWithEmptyTags 构建空tags数组的resource payload
func BuildPayloadWithEmptyTags(catalogID string) map[string]any {
	return map[string]any{
		"catalog_id": catalogID,
		"name":       GenerateUniqueName("empty-tags-resource"),
		"tags":       []string{},
	}
}

// BuildPayloadWithEmptyTagInArray 构建包含空字符串tag的resource payload
func BuildPayloadWithEmptyTagInArray(catalogID string) map[string]any {
	return map[string]any{
		"catalog_id": catalogID,
		"name":       GenerateUniqueName("empty-tag-resource"),
		"tags":       []string{"valid-tag", ""},
	}
}

// BuildPayloadWithInvalidCharTag 构建包含非法字符tag的resource payload
func BuildPayloadWithInvalidCharTag(catalogID string) map[string]any {
	return map[string]any{
		"catalog_id": catalogID,
		"name":       GenerateUniqueName("invalid-char-tag-resource"),
		"tags":       []string{"tag/name", "valid"},
	}
}

// BuildPayloadWithLongTag 构建指定长度tag的resource payload
func BuildPayloadWithLongTag(catalogID string, tagLength int) map[string]any {
	longTag := strings.Repeat("t", tagLength)
	return map[string]any{
		"catalog_id": catalogID,
		"name":       GenerateUniqueName("long-tag-resource"),
		"tags":       []string{longTag},
	}
}

// ========== Category相关Payload ==========

// BuildPayloadWithCategory 构建带category的resource payload
func BuildPayloadWithCategory(catalogID string, category string) map[string]any {
	return map[string]any{
		"catalog_id": catalogID,
		"name":       GenerateUniqueName("category-resource"),
		"category":   category,
	}
}

// ========== Resource特有负向测试Payload ==========

// BuildPayloadWithMissingCatalogID 构建缺少catalog_id的resource payload
func BuildPayloadWithMissingCatalogID() map[string]any {
	return map[string]any{
		"name": GenerateUniqueName("missing-catalogid-resource"),
	}
}

// BuildPayloadWithInvalidCatalogID 构建无效catalog_id的resource payload
func BuildPayloadWithInvalidCatalogID() map[string]any {
	return map[string]any{
		"catalog_id": "non-existent-catalog-id-12345",
		"name":       GenerateUniqueName("invalid-catalogid-resource"),
	}
}

// ========== 安全测试Payload ==========

// BuildPayloadWithSQLInjection 构建SQL注入测试resource payload
func BuildPayloadWithSQLInjection(catalogID string) map[string]any {
	return map[string]any{
		"catalog_id": catalogID,
		"name":       fmt.Sprintf("test'; DROP TABLE t_resource; --%d", time.Now().UnixNano()),
	}
}

// BuildPayloadWithXSS 构建XSS测试resource payload
func BuildPayloadWithXSS(catalogID string) map[string]any {
	return map[string]any{
		"catalog_id": catalogID,
		"name":       fmt.Sprintf("<script>alert('xss')</script>%d", time.Now().UnixNano()),
	}
}

// BuildPayloadWithPathTraversal 构建路径遍历测试resource payload
func BuildPayloadWithPathTraversal(catalogID string) map[string]any {
	return map[string]any{
		"catalog_id": catalogID,
		"name":       fmt.Sprintf("../../../etc/passwd%d", time.Now().UnixNano()),
	}
}
