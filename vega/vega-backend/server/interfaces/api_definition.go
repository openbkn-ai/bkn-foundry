// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

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
type HTTPResponseDefinition struct {
	Description string         `json:"description" mapstructure:"description"`
	Schema      any            `json:"schema" mapstructure:"schema"`
	Headers     map[string]any `json:"headers,omitempty" mapstructure:"headers"`
}

// HTTPSuccessRule 保留原始状态码和条件树，由消费者执行语义校验及条件解析。
type HTTPSuccessRule struct {
	HTTPStatus          []any          `json:"http_status" mapstructure:"http_status"`
	BodyFilterCondition map[string]any `json:"body_filter_condition,omitempty" mapstructure:"body_filter_condition"`
}
