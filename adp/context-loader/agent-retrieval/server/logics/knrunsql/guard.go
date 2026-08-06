// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package knrunsql provides the shared read-only SQL execution service used by
// both the MCP run_sql tool and the internal REST endpoint.
package knrunsql

import (
	"fmt"
	"regexp"
	"strings"
)

// run_sql 只读 SQL 守卫。
//
// vega 的原始查询接口会以 SQLGlot AST 策略做最终裁决，拒绝非顶层 SELECT、WITH/CTE、
// 集合运算和写入/DDL。这里用「剥离注释与字符串字面量后再做词法判定」做纵深防御：
// 先消除可藏关键字的注释/字符串/占位符，再要求单语句、以 SELECT/WITH 开头、不含写/DDL 关键字。
// 它不是完整 SQL 解析器，不能替代 vega 端的最终策略校验。

var (
	// resourcePlaceholderRe 与 vega extractResourceIDs 保持一致：{{.resource_id}} 或 {{resource_id}}。
	resourcePlaceholderRe = regexp.MustCompile(`\{\{\.?([a-z0-9][a-z0-9_-]{0,39})\}\}`)
	// anyPlaceholderRe 在关键字判定前把占位符整体替换掉，避免 {{.delete}} 之类内部词触发误判。
	anyPlaceholderRe = regexp.MustCompile(`\{\{[^}]*\}\}`)
	// startsWithSelectRe 只校验入口形态，允许前导空白与左括号；集合运算可能通过本地守卫，
	// 但会由 vega 的最终策略拒绝。
	startsWithSelectRe = regexp.MustCompile(`(?is)^[\s(]*(SELECT|WITH)\b`)
	// forbiddenKeywordRe 写入 / DDL / 权限 / 过程类关键字黑名单（剥离注释与字符串后判定）。
	forbiddenKeywordRe = regexp.MustCompile(`(?i)\b(INSERT|UPDATE|DELETE|DROP|ALTER|CREATE|TRUNCATE|GRANT|REVOKE|REPLACE|MERGE|UPSERT|CALL|EXEC|EXECUTE|RENAME|LOAD|COPY|INTO|ATTACH|DETACH|USE|VACUUM|ANALYZE|REFRESH|COMMENT|PREPARE|DEALLOCATE)\b`)
)

// ExtractResourceIDs 从 SQL 中提取所有 {{.resource_id}} 占位符内的 resource_id（去重，保序）。
func ExtractResourceIDs(sql string) []string {
	matches := resourcePlaceholderRe.FindAllStringSubmatch(sql, -1)
	ids := make([]string, 0, len(matches))
	seen := make(map[string]bool)
	for _, m := range matches {
		if len(m) > 1 && !seen[m[1]] {
			seen[m[1]] = true
			ids = append(ids, m[1])
		}
	}
	return ids
}

// stripSQLNoise 去除行注释(-- 与 #)、块注释(/* */)、MySQL 字符串与反引号标识符，
// 以免其中藏有关键字或分号干扰守卫判定。
func stripSQLNoise(sql string) string {
	var b strings.Builder
	runes := []rune(sql)
	n := len(runes)
	for i := 0; i < n; i++ {
		c := runes[i]
		switch {
		case c == '-' && i+1 < n && runes[i+1] == '-': // 行注释 --
			for i < n && runes[i] != '\n' {
				i++
			}
			b.WriteByte(' ')
		case c == '#': // 行注释 #
			for i < n && runes[i] != '\n' {
				i++
			}
			b.WriteByte(' ')
		case c == '/' && i+1 < n && runes[i+1] == '*': // 块注释 /* */
			i += 2
			for i+1 < n && !(runes[i] == '*' && runes[i+1] == '/') {
				i++
			}
			i++ // 跳过结尾的 '/'
			b.WriteByte(' ')
		case c == '\'' || c == '"': // MySQL 单/双引号字符串（反斜杠与重复引号转义）
			i = skipQuotedSQLLiteral(runes, i, c)
			b.WriteByte(' ')
		case c == '`': // 反引号标识符
			i = skipQuotedSQLLiteral(runes, i, c)
			b.WriteByte(' ')
		default:
			b.WriteRune(c)
		}
	}
	return b.String()
}

// skipQuotedSQLLiteral 返回从 openingQuote 开始的 MySQL 字符串或反引号标识符的结束位置。
// 成对引号不会提前结束字面量；反斜杠转义只对单引号字符串生效，
// 反引号标识符与双引号字符串内的反斜杠是普通字符。
func skipQuotedSQLLiteral(runes []rune, start int, quote rune) int {
	for i := start + 1; i < len(runes); i++ {
		if quote == '\'' && runes[i] == '\\' && i+1 < len(runes) {
			i++
			continue
		}
		if runes[i] == quote {
			if i+1 < len(runes) && runes[i+1] == quote {
				i++
				continue
			}
			return i
		}
	}
	return len(runes) - 1
}

// EnsureReadOnlySQL 校验 SQL 为单条只读 SELECT/WITH 查询；违规返回错误。
func EnsureReadOnlySQL(sql string) error {
	if strings.TrimSpace(sql) == "" {
		return fmt.Errorf("sql is empty")
	}

	cleaned := stripSQLNoise(sql)
	// 占位符整体替换为中性词，避免内部词触发关键字判定。
	cleaned = anyPlaceholderRe.ReplaceAllString(cleaned, " _rid_ ")
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return fmt.Errorf("sql has no executable statement")
	}

	// 去掉结尾的分号与空白后，不允许再出现分号（即禁止多语句）。
	trimmed := strings.TrimRight(cleaned, "; \t\r\n")
	if strings.Contains(trimmed, ";") {
		return fmt.Errorf("multiple statements are not allowed; only a single read-only SELECT is permitted")
	}

	if !startsWithSelectRe.MatchString(trimmed) {
		return fmt.Errorf("only read-only queries are allowed: statement must start with SELECT or WITH")
	}

	if loc := forbiddenKeywordRe.FindString(trimmed); loc != "" {
		return fmt.Errorf("forbidden keyword %q detected: run_sql is read-only (no writes/DDL)", strings.ToUpper(loc))
	}

	return nil
}
