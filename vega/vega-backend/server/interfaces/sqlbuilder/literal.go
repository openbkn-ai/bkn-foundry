// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package sqlbuilder

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
)

// ScalarValue 读取标量节点供格式、精度及类型校验；不改变原节点的绑定或内联语义。
func ScalarValue(node SqlNode) (any, bool) {
	switch n := node.(type) {
	case *Literal:
		if n != nil {
			return n.Value, true
		}
	case *Param:
		if n != nil {
			return n.Value, true
		}
	}
	return nil, false
}

// LiteralNumber 校验并规范化数值常量，保留十进制文本，不将精确数值转成浮点数。
// 目标数据库的精度和范围限制仍由各生成器检查。
func LiteralNumber(value any) (json.Number, error) {
	var text string
	switch v := value.(type) {
	case json.Number:
		text = string(v)
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		text = fmt.Sprint(v)
	case float32:
		text = strconv.FormatFloat(float64(v), 'e', -1, 32)
	case float64:
		text = strconv.FormatFloat(v, 'e', -1, 64)
	default:
		return "", fmt.Errorf("unsupported SQL literal type %T", value)
	}
	if text == "" || (text[0] != '-' && (text[0] < '0' || text[0] > '9')) || !json.Valid([]byte(text)) {
		return "", fmt.Errorf("invalid numeric literal")
	}
	// 科学计数法按近似数值处理；拒绝无法表示的非有限值。
	for _, c := range text {
		if c == 'e' || c == 'E' {
			n, err := strconv.ParseFloat(text, 64)
			if err != nil || math.IsInf(n, 0) || math.IsNaN(n) {
				return "", fmt.Errorf("floating literal is out of range")
			}
			break
		}
	}
	return json.Number(text), nil
}
