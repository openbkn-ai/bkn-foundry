// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
)

const APIEffectRead = "read"

// HTTPOperationDefinition 描述一次 HTTP 操作，供登录和业务请求复用，不包含运行时解析状态。
type HTTPOperationDefinition struct {
	Request     *HTTPRequestDefinition   `json:"request" mapstructure:"request"`
	Response    *HTTPResponsesDefinition `json:"response" mapstructure:"response"`
	SuccessRule *HTTPSuccessRule         `json:"success_rule" mapstructure:"success_rule"`
}

// HTTPRequestDefinition 保存 HTTP 请求声明；动态构造、Query 和 Body 保持原始结构。
type HTTPRequestDefinition struct {
	Method     string            `json:"method" mapstructure:"method"`
	Path       string            `json:"path" mapstructure:"path"`
	Headers    map[string]string `json:"headers,omitempty" mapstructure:"headers"`
	Parameters []map[string]any  `json:"parameters,omitempty" mapstructure:"parameters"`
	Body       map[string]any    `json:"body,omitempty" mapstructure:"body"`
}

// HTTPResponsesDefinition 在集合层声明媒体类型，并按状态键保存各响应。
type HTTPResponsesDefinition struct {
	ContentType string                             `json:"content_type" mapstructure:"content_type"`
	Statuses    map[string]*HTTPResponseDefinition `json:"statuses" mapstructure:"statuses"`
}

// HTTPResponseDefinition 保存单个状态响应；Schema 保留对象或布尔值，包括 false。
// ResultRows、ResultTotal 是相对于完整正文的可选路径，仅供业务 Resource 使用。
// 仅 ResultRows 接受空字符串表示正文根节点；分页元数据路径必须非空。
type HTTPResponseDefinition struct {
	Description string         `json:"description" mapstructure:"description"`
	Schema      any            `json:"schema" mapstructure:"schema"`
	Headers     map[string]any `json:"headers,omitempty" mapstructure:"headers"`
	ResultRows  *string        `json:"result_rows,omitempty" mapstructure:"result_rows"`
	ResultTotal *string        `json:"result_total,omitempty" mapstructure:"result_total"`
}

// HTTPSuccessRule 保留原始状态码和条件树，由消费者执行语义校验及条件解析。
type HTTPSuccessRule struct {
	HTTPStatus          []any          `json:"http_status" mapstructure:"http_status"`
	BodyFilterCondition map[string]any `json:"body_filter_condition,omitempty" mapstructure:"body_filter_condition"`
}

// APIDefinition 在通用 HTTP 操作上增加业务变量、分页和各状态响应的行映射。
type APIDefinition struct {
	HTTPOperationDefinition `mapstructure:",squash"`
	Variables               map[string]CatalogVariable `json:"variables,omitempty" mapstructure:"variables"`
	PagingRule              *APIPagingRule             `json:"paging_rule,omitempty" mapstructure:"paging_rule"`
}

type APIPagingRule struct {
	Mode         string `json:"mode" mapstructure:"mode"`
	DefaultLimit int64  `json:"default_limit" mapstructure:"default_limit"`
	MaxLimit     int64  `json:"max_limit" mapstructure:"max_limit"`
	PageBase     *int   `json:"page_base,omitempty" mapstructure:"page_base"`
}

// ForStatus 按精确状态、状态类别、default 顺序选择响应；显式空项不回退。
func (d *HTTPResponsesDefinition) ForStatus(status int) *HTTPResponseDefinition {
	if d == nil {
		return nil
	}
	if response, exists := d.Statuses[strconv.Itoa(status)]; exists {
		return response
	}
	if response, exists := d.Statuses[strconv.Itoa(status/100)+"XX"]; exists {
		return response
	}
	return d.Statuses["default"]
}

func (d *APIDefinition) UnmarshalJSON(data []byte) error {
	type definition APIDefinition
	var value definition
	if err := decodeAPIFixedJSON(data, &value); err != nil {
		return err
	}
	*d = APIDefinition(value)
	return nil
}

func (d *APIPagingRule) UnmarshalJSON(data []byte) error {
	type definition APIPagingRule
	var value definition
	if err := decodeAPIFixedJSON(data, &value); err != nil {
		return err
	}
	*d = APIPagingRule(value)
	return nil
}

// decodeAPIFixedJSON 仅约束资源固定结构；通用 HTTP 字段的语义由连接器校验。
func decodeAPIFixedJSON(data []byte, result any) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '{' {
		return errors.New("API definition must be an object")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(result); err != nil {
		// 解码器错误可能包含用户提交的敏感值，只返回固定说明。
		return errors.New("invalid API definition field or value")
	}
	if len(bytes.TrimSpace(data[decoder.InputOffset():])) != 0 {
		return errors.New("API definition must contain one JSON object")
	}
	return nil
}
