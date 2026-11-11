// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"fmt"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/cypher/parsing"
)

// ScalarFunction is a call to one of the scalar functions a query may make:
// round, abs, toUpper, toLower, toString, toInteger and toFloat. It is a value
// like CASE and coalesce, and is written wherever they may be. Name is the
// function's name in lower case.
type ScalarFunction struct {
	Name      string
	Arguments []Expression
	Pos       Position
}

func (f ScalarFunction) expressionPosition() Position { return f.Pos }

// PlanScalarFunction is a ScalarFunction once its arguments are planned. It is
// generated as SQL only, as CASE and coalesce are.
type PlanScalarFunction struct {
	Name      string
	Arguments []PlanExpression
}

func (PlanScalarFunction) planExpression() {}

// scalarFunctions names each function by its lower-case name, as it is
// written in an error, with how many arguments it takes.
var scalarFunctions = map[string]struct {
	written      string
	minArguments int
	maxArguments int
}{
	"round":     {"round", 1, 2},
	"abs":       {"abs", 1, 1},
	"toupper":   {"toUpper", 1, 1},
	"tolower":   {"toLower", 1, 1},
	"tostring":  {"toString", 1, 1},
	"tointeger": {"toInteger", 1, 1},
	"tofloat":   {"toFloat", 1, 1},
}

// maxRoundPrecision is the most decimal places round() keeps; MySQL keeps at
// most 30 for a DECIMAL.
const maxRoundPrecision = 30

func isScalarFunction(invocation parsing.IOC_FunctionInvocationContext) bool {
	_, ok := scalarFunctions[strings.ToLower(identifier(invocation.OC_FunctionName().GetText()))]
	return ok
}

func analyzeScalarFunction(invocation parsing.IOC_FunctionInvocationContext, readers caseReaders) (Expression, error) {
	name := strings.ToLower(identifier(invocation.OC_FunctionName().GetText()))
	function := scalarFunctions[name]
	arguments := invocation.AllOC_Expression()
	if invocation.DISTINCT() != nil {
		return nil, unsupportedf(invocation, "DISTINCT in "+function.written+"()", "DISTINCT belongs in an aggregate")
	}
	if len(arguments) < function.minArguments || len(arguments) > function.maxArguments {
		usage := function.written + "(x)"
		if name == "round" {
			usage = "round(x) or round(x, 2)"
		}
		return nil, unsupportedf(invocation, "this "+function.written+"()", "write %s", usage)
	}
	value := ScalarFunction{Name: name, Pos: positionOf(invocation)}
	for _, argument := range arguments {
		read, err := caseResult(argument, readers)
		if err != nil {
			return nil, err
		}
		value.Arguments = append(value.Arguments, read)
	}
	return value, nil
}

// planScalarFunction plans a call and says what kind of value it gives. A
// function over numbers refuses a string and one over strings a number, rather
// than letting the database convert one into the other as MySQL would.
func (p *planner) planScalarFunction(node ScalarFunction) (PlanExpression, numberKind, error) {
	p.plan.SQLOnly = true
	written := scalarFunctions[node.Name].written
	argument, kind, err := p.planResult(node.Arguments[0])
	if err != nil {
		return nil, kindUnknown, err
	}
	planned := PlanScalarFunction{Name: node.Name, Arguments: []PlanExpression{argument}}
	numeric := kind == kindInteger || kind == kindFloat || kind == kindNull
	switch node.Name {
	case "round", "abs", "tointeger", "tofloat":
		if !numeric {
			return nil, kindUnknown, planErrorf(node.Pos, "%s() takes a number", written)
		}
	case "toupper", "tolower":
		if kind != kindNotNumber && kind != kindNull {
			return nil, kindUnknown, planErrorf(node.Pos, "%s() takes a string; write toString(x) first for a number", written)
		}
	}
	switch node.Name {
	case "round":
		// Cypher's round gives a float whatever it is given, so a division
		// by its result is never taken for an integer one.
		resultKind := kindFloat
		if kind == kindNull {
			resultKind = kindNull
		}
		if len(node.Arguments) == 2 {
			precision, ok := node.Arguments[1].(Literal)
			if !ok || precision.Kind != LiteralInteger || precision.Integer < 0 || precision.Integer > maxRoundPrecision {
				return nil, kindUnknown, planErrorf(node.Pos,
					"the second argument of round() is how many decimal places to keep: an integer from 0 to %d, as in round(x, 2)",
					maxRoundPrecision)
			}
			planned.Arguments = append(planned.Arguments, PlanValueLiteral{Value: precision, InputPointer: "$.query"})
		}
		return planned, resultKind, nil
	case "abs":
		return planned, kind, nil
	case "tointeger":
		return planned, kindInteger, nil
	case "tofloat":
		return planned, kindFloat, nil
	default:
		return planned, kindNotNumber, nil
	}
}

// scalarFunctionSQL writes a call in MySQL 5.7 with the functions vega-backend
// accepts. Each form keeps the Cypher meaning where SQL's differs: round gives
// a float, rounded on the exact value first; toInteger truncates toward zero,
// while CAST rounds; toFloat makes a double, which 5.7 cannot CAST to.
func scalarFunctionSQL(name string, arguments []string) string {
	switch name {
	case "round":
		if len(arguments) == 2 {
			return fmt.Sprintf("(ROUND(%s, %s) + 0E0)", arguments[0], arguments[1])
		}
		return fmt.Sprintf("(ROUND(%s) + 0E0)", arguments[0])
	case "abs":
		return fmt.Sprintf("ABS(%s)", arguments[0])
	case "toupper":
		return fmt.Sprintf("UPPER(%s)", arguments[0])
	case "tolower":
		return fmt.Sprintf("LOWER(%s)", arguments[0])
	case "tostring":
		return fmt.Sprintf("CAST(%s AS CHAR)", arguments[0])
	case "tointeger":
		return fmt.Sprintf("CAST(CASE WHEN %[1]s < 0 THEN CEIL(%[1]s) ELSE FLOOR(%[1]s) END AS SIGNED)", arguments[0])
	case "tofloat":
		return fmt.Sprintf("(%s + 0E0)", arguments[0])
	}
	panic("scalarFunctionSQL: unplanned function " + name)
}
