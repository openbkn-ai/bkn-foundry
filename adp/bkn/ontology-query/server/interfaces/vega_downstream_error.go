// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// VegaDownstreamError 保留 vega-backend 返回的状态码与错误内容。
//
// 之前这里只留一句 fmt.Errorf，上层无从判断下游到底是「参数不对」还是「服务挂了」，
// 只能一律按 500 处理。结果是：调用方发了一个不被支持的算子，拿到的却是「调用依赖
// 服务异常，请检查服务」——去查了半天服务健康，而真正该做的是换算子或者建索引。
type VegaDownstreamError struct {
	StatusCode  int
	ErrorCode   string
	Description string
	Details     string
	Raw         string
}

func (e *VegaDownstreamError) Error() string {
	msg := fmt.Sprintf("vega-backend responded %d", e.StatusCode)
	if e.ErrorCode != "" {
		msg += ": " + e.ErrorCode
	}
	if detail := e.Message(); detail != "" {
		msg += ": " + detail
	}
	return msg
}

// maxRawMessageLen 是原始报文回退时的长度上限。
//
// 4xx 不一定来自 vega 自己：中间的 ingress/网关在 413、502 一类情况下返回的是整页
// HTML。这种报文解析不出结构，整段回退会让终端调用方在 error_details 里收到一坨
// HTML 当作错误原因。截断后仍足够辨认来源，也不至于淹没响应。
const maxRawMessageLen = 512

// Message 返回最有信息量的那一句：优先 error_details（里面是具体到字段与算子的原因），
// 其次 description，最后退回原始报文（截断）。
func (e *VegaDownstreamError) Message() string {
	if e.Details != "" {
		return e.Details
	}
	if e.Description != "" {
		return e.Description
	}
	return truncateRaw(e.Raw)
}

func truncateRaw(raw string) string {
	if len(raw) <= maxRawMessageLen {
		return raw
	}
	return raw[:maxRawMessageLen] + "...(truncated)"
}

// IsClientError 表示这是请求侧的问题，调用方改请求即可，不该被当成依赖故障。
func (e *VegaDownstreamError) IsClientError() bool {
	return e.StatusCode >= http.StatusBadRequest && e.StatusCode < http.StatusInternalServerError
}

// AsVegaDownstreamError 从错误链里取出 VegaDownstreamError。
func AsVegaDownstreamError(err error) (*VegaDownstreamError, bool) {
	var target *VegaDownstreamError
	if errors.As(err, &target) {
		return target, true
	}
	return nil, false
}

// NewVegaDownstreamError 解析 vega-backend 的错误报文。报文格式不认识时不报错，
// 原样留在 Raw 里——状态码本身已经足够上层做分级。
func NewVegaDownstreamError(statusCode int, raw string) *VegaDownstreamError {
	de := &VegaDownstreamError{StatusCode: statusCode, Raw: raw}

	var payload struct {
		ErrorCode    string `json:"error_code"`
		Description  string `json:"description"`
		ErrorDetails string `json:"error_details"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err == nil {
		de.ErrorCode = payload.ErrorCode
		de.Description = payload.Description
		de.Details = payload.ErrorDetails
	}
	return de
}
