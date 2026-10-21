// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package compiler

import (
	"strings"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/querymodel"
)

type functionSignature struct {
	Name             string
	MinArgs, MaxArgs int
	Evaluation       querymodel.EvaluationKind
	AllowDistinct    bool
	AllowStar        bool
}

// lookupFunction 只查询首批已批准签名，不透传任意客户端函数名。
func lookupFunction(name string) (functionSignature, bool) {
	name = strings.ToUpper(name)
	switch name {
	case "COUNT":
		return functionSignature{name, 1, 1, querymodel.EvaluationAggregate, true, true}, true
	case "SUM", "AVG", "MIN", "MAX":
		return functionSignature{name, 1, 1, querymodel.EvaluationAggregate, false, false}, true
	case "ABS":
		return functionSignature{name, 1, 1, querymodel.EvaluationPure, false, false}, true
	case "COALESCE":
		return functionSignature{name, 2, 0, querymodel.EvaluationPure, false, false}, true
	case "IFNULL", "NULLIF":
		return functionSignature{name, 2, 2, querymodel.EvaluationPure, false, false}, true
	case "NOW", "CURRENT_TIMESTAMP":
		return functionSignature{name, 0, 0, querymodel.EvaluationStable, false, false}, true
	default:
		return functionSignature{}, false
	}
}
