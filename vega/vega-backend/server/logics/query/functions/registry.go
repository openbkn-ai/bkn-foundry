// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package functions 保存编译和绑定共同使用的函数签名。
package functions

import (
	"strings"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/querymodel"
)

type Signature struct {
	Name             string
	MinArgs, MaxArgs int
	Evaluation       querymodel.EvaluationKind
	AllowDistinct    bool
	AllowStar        bool
}

// Lookup 只查询首批已批准签名，不透传任意客户端函数名。
func Lookup(name string) (Signature, bool) {
	name = strings.ToUpper(name)
	switch name {
	case "COUNT":
		return Signature{name, 1, 1, querymodel.EvaluationAggregate, true, true}, true
	case "SUM", "AVG", "MIN", "MAX":
		return Signature{name, 1, 1, querymodel.EvaluationAggregate, false, false}, true
	case "ABS":
		return Signature{name, 1, 1, querymodel.EvaluationPure, false, false}, true
	case "COALESCE":
		return Signature{name, 2, 0, querymodel.EvaluationPure, false, false}, true
	case "IFNULL", "NULLIF":
		return Signature{name, 2, 2, querymodel.EvaluationPure, false, false}, true
	case "NOW", "CURRENT_TIMESTAMP":
		return Signature{name, 0, 0, querymodel.EvaluationStable, false, false}, true
	default:
		return Signature{}, false
	}
}
