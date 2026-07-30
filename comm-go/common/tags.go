// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package common

import (
	"fmt"
	"slices"
	"strings"
)

// 把tags数组转成数据库存储的字符串的形式，格式为 "a","b","c"
func TagSlice2TagString(strs []string) string {
	newStrs := make([]string, len(strs))
	for i, str := range strs {
		newStrs[i] = fmt.Sprintf("\"%s\"", str)
	}
	return strings.Join(newStrs, ",")
}

// 把数据库存储的tags字符串(格式为 "a","b","c")转成tags数组
func TagString2TagSlice(str string) []string {
	if str == "" {
		return []string{}
	}

	oldStrs := strings.Split(str, ",")
	newStrs := make([]string, len(oldStrs))
	for i, oldStr := range oldStrs {
		newStrs[i] = strings.Trim(oldStr, "\"")
	}
	return newStrs
}

// 除去tag前后的空格, 数组去重, 并排序
func TagSliceTransform(origTags []string) []string {
	uniqueMap := make(map[string]bool)
	var tags []string

	for _, tag := range origTags {
		trimmedTag := strings.Trim(tag, " ")
		if trimmedTag != "" && !uniqueMap[trimmedTag] {
			tags = append(tags, trimmedTag)
			uniqueMap[trimmedTag] = true
		}
	}
	slices.Sort(tags)

	return tags
}
