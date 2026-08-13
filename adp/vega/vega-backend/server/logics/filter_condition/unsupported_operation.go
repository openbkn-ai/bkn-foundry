// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package filter_condition

import (
	"errors"
	"fmt"
)

// 查询通道。同一个算子在不同通道上能力不同：全文与向量算子只有索引通道有实现，
// 表通道（SQL）没有，也不可能有。
const (
	QueryChannelSQL        = "sql"
	QueryChannelOpenSearch = "opensearch"
	QueryChannelFileset    = "fileset"
)

// UnsupportedOperationError 表示某个过滤算子在当前查询通道上没有实现。
//
// 这是请求侧的问题而不是服务故障：调用方换一个算子、或者给资源建索引就能解决。
// 之所以要一个具名类型而不是裸 error，是因为错误要跨好几层才到调用方，中间每层
// 都需要判断「这是参数错误」还是「下游真的挂了」——前者 400 且可自纠，后者 500。
// 裸 fmt.Errorf 到了上层就只剩一句话，只能一律当成内部错误。
type UnsupportedOperationError struct {
	Operation string // 请求里写的算子名
	Channel   string // 实际执行的查询通道
}

// NewUnsupportedOperationError 构造一个算子不支持错误。
func NewUnsupportedOperationError(operation, channel string) *UnsupportedOperationError {
	return &UnsupportedOperationError{Operation: operation, Channel: channel}
}

func (e *UnsupportedOperationError) Error() string {
	msg := fmt.Sprintf("operation %s is not supported", e.Operation)
	if e.Channel != "" {
		msg += fmt.Sprintf(" by the %s query channel", e.Channel)
	}
	if hint := e.hint(); hint != "" {
		msg += "; " + hint
	}
	return msg
}

// hint 给出可执行的下一步。最常见的情形是在没有本地索引的表资源上发全文/向量
// 检索——只说「不支持」会让调用方以为这个能力不存在，而实际上建个索引就有了。
func (e *UnsupportedOperationError) hint() string {
	if e.Channel != QueryChannelSQL {
		return ""
	}
	switch e.Operation {
	case OperationMatch, OperationMatchPhrase, OperationMultiMatch:
		return "full-text operations need a local index on the resource: build one, " +
			"or use like/not_like/contain/prefix/regex on this channel"
	case OperationKnnVector:
		return "vector search needs a local index with a vector feature on the field: build one first"
	}
	return ""
}

// AsUnsupportedOperationError 从错误链里取出 UnsupportedOperationError。
func AsUnsupportedOperationError(err error) (*UnsupportedOperationError, bool) {
	var target *UnsupportedOperationError
	if errors.As(err, &target) {
		return target, true
	}
	return nil, false
}

// IsFulltextOperation 判断是否全文检索算子。全文能力只存在于索引里，因此这些
// 算子能不能用，取决于资源有没有本地索引，而不取决于字段类型。
func IsFulltextOperation(operation string) bool {
	switch operation {
	case OperationMatch, OperationMatchPhrase, OperationMultiMatch:
		return true
	}
	return false
}
