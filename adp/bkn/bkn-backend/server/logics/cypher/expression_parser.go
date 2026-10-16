// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package cypher

import (
	"math"
	"strconv"
	"strings"

	"github.com/antlr4-go/antlr/v4"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/cypher/parsing"
)

// OperationExpression is shared by projections and residual predicates. The
// operator is chosen by this parser, never used as a physical SQL fragment.
type OperationExpression struct {
	Operator  string
	Arguments []ValueExpression
	Pos       Position
}
type ParameterExpression struct{ Ref ParameterRef }
type AggregateExpression struct {
	Function string
	Value    ValueExpression
	Distinct bool
	Pos      Position
}

func (OperationExpression) valueExpression() {}
func (ParameterExpression) valueExpression() {}
func (AggregateExpression) valueExpression() {}

func readValueExpression(ctx antlr.ParserRuleContext, depth int) (ValueExpression, error) {
	if depth > 256 {
		return nil, unsupported(ctx, "expression parse nesting exceeds 256 levels")
	}
	var err error
	read := func(child antlr.ParserRuleContext) (ValueExpression, error) {
		return readValueExpression(child, depth+1)
	}
	op := func(name string, args ...ValueExpression) ValueExpression {
		return OperationExpression{Operator: name, Arguments: args, Pos: positionOf(ctx)}
	}
	switch node := ctx.(type) {
	case parsing.IOC_ExpressionContext:
		return read(node.OC_OrExpression())
	case parsing.IOC_OrExpressionContext, parsing.IOC_XorExpressionContext, parsing.IOC_AndExpressionContext, parsing.IOC_AddOrSubtractExpressionContext, parsing.IOC_MultiplyDivideModuloExpressionContext, parsing.IOC_PowerOfExpressionContext:
		var value ValueExpression
		operator := ""
		for _, child := range ctx.GetChildren() {
			if term, ok := child.(antlr.TerminalNode); ok {
				if text := strings.TrimSpace(term.GetText()); text != "" {
					operator = strings.ToUpper(text)
				}
				continue
			}
			rule, ok := child.(antlr.ParserRuleContext)
			if !ok {
				continue
			}
			next, err := read(rule)
			if err != nil {
				return nil, err
			}
			if value == nil {
				value = next
			} else {
				value = op(operator, value, next)
			}
		}
		return value, nil
	case parsing.IOC_NotExpressionContext:
		value, err := read(node.OC_ComparisonExpression())
		if err != nil {
			return nil, err
		}
		for range node.AllNOT() {
			value = op("NOT", value)
		}
		return value, nil
	case parsing.IOC_ComparisonExpressionContext:
		left, err := read(node.OC_StringListNullPredicateExpression())
		if err != nil {
			return nil, err
		}
		previous := left
		var result ValueExpression
		for _, part := range node.AllOC_PartialComparisonExpression() {
			right, err := read(part.OC_StringListNullPredicateExpression())
			if err != nil {
				return nil, err
			}
			comparison := op(comparisonOperator(part), previous, right)
			if result == nil {
				result = comparison
			} else {
				result = op("AND", result, comparison)
			}
			previous = right
		}
		if result == nil {
			return left, nil
		}
		return result, nil
	case parsing.IOC_StringListNullPredicateExpressionContext:
		value, err := read(node.OC_AddOrSubtractExpression())
		if err != nil {
			return nil, err
		}
		for _, child := range node.GetChildren() {
			switch suffix := child.(type) {
			case parsing.IOC_StringPredicateExpressionContext:
				right, err := read(suffix.OC_AddOrSubtractExpression())
				if err != nil {
					return nil, err
				}
				operator := "CONTAINS"
				if suffix.STARTS() != nil {
					operator = "STARTS WITH"
				} else if suffix.ENDS() != nil {
					operator = "ENDS WITH"
				}
				value = op(operator, value, right)
			case parsing.IOC_ListPredicateExpressionContext:
				right, err := read(suffix.OC_AddOrSubtractExpression())
				if err != nil {
					return nil, err
				}
				value = op("IN", value, right)
			case parsing.IOC_NullPredicateExpressionContext:
				operator := "IS NULL"
				if suffix.NOT() != nil {
					operator = "IS NOT NULL"
				}
				value = op(operator, value)
			}
		}
		return value, nil
	case parsing.IOC_UnaryAddOrSubtractExpressionContext:
		for _, child := range node.GetChildren() {
			if token, ok := child.(antlr.TerminalNode); ok && token.GetText() == "-" {
				if number, err := strconv.ParseUint(node.OC_ListOperatorExpression().GetText(), 0, 64); err == nil && number == uint64(math.MaxInt64)+1 {
					return LiteralExpression{Value: Literal{Kind: LiteralInteger, Integer: math.MinInt64, Pos: positionOf(node)}}, nil
				}
			}
		}
		value, err := read(node.OC_ListOperatorExpression())
		if err != nil {
			return nil, err
		}
		for _, child := range node.GetChildren() {
			if term, ok := child.(antlr.TerminalNode); ok {
				switch strings.TrimSpace(term.GetText()) {
				case "-":
					return op("NEGATE", value), nil
				case "+":
					return op("POSITIVE", value), nil
				}
			}
		}
		return value, nil
	case parsing.IOC_ListOperatorExpressionContext:
		value, err := read(node.OC_PropertyOrLabelsExpression())
		if err != nil {
			return nil, err
		}
		children := node.GetChildren()
		for i := 1; i < len(children); i++ {
			if terminal, ok := children[i].(antlr.TerminalNode); !ok || strings.TrimSpace(terminal.GetText()) != "[" {
				continue
			}
			var bounds []ValueExpression
			hasSlice := false
			beforeSlice := true
			var left, right ValueExpression
			for i++; i < len(children); i++ {
				if term, ok := children[i].(antlr.TerminalNode); ok {
					switch strings.TrimSpace(term.GetText()) {
					case "]":
						goto completeIndex
					case "..":
						hasSlice = true
						beforeSlice = false
					}
					continue
				}
				if child, ok := children[i].(parsing.IOC_ExpressionContext); ok {
					bound, err := read(child)
					if err != nil {
						return nil, err
					}
					bounds = append(bounds, bound)
					if beforeSlice {
						left = bound
					} else {
						right = bound
					}
				}
			}
		completeIndex:
			if hasSlice {
				if left == nil {
					left = OperationExpression{Operator: "OMITTED_BOUND"}
				}
				if right == nil {
					right = OperationExpression{Operator: "OMITTED_BOUND"}
				}
				value = op("SLICE", value, left, right)
			} else if len(bounds) == 1 {
				value = op("INDEX", value, bounds[0])
			} else {
				return nil, unsupported(node, "this list index")
			}
		}
		return value, nil
	case parsing.IOC_PropertyOrLabelsExpressionContext:
		if node.OC_NodeLabels() != nil {
			return nil, unsupported(node, "label predicates")
		}
		value, err := read(node.OC_Atom())
		if err != nil {
			return nil, err
		}
		for _, lookup := range node.AllOC_PropertyLookup() {
			name, err := namedIdentifier(lookup.OC_PropertyKeyName(), "property name")
			if err != nil {
				return nil, err
			}
			if variable, ok := value.(VariableExpression); ok {
				value = PropertyExpression{Ref: PropertyRef{Variable: variable.Name, Property: name, Pos: positionOf(lookup)}}
			} else {
				value = op("INDEX", value, LiteralExpression{Value: Literal{Kind: LiteralString, String: name}})
			}
		}
		return value, nil
	case parsing.IOC_AtomContext:
		switch {
		case node.OC_ParenthesizedExpression() != nil:
			return read(node.OC_ParenthesizedExpression().OC_Expression())
		case node.OC_Variable() != nil:
			name, err := namedIdentifier(node.OC_Variable(), "variable name")
			return VariableExpression{Name: name, Pos: positionOf(node)}, err
		case node.OC_Parameter() != nil:
			name, err := parameterName(node.OC_Parameter())
			return ParameterExpression{Ref: ParameterRef{Name: name, Pos: positionOf(node)}}, err
		case node.COUNT() != nil:
			return AggregateExpression{Function: "count", Value: LiteralExpression{Value: Literal{Kind: LiteralInteger, Integer: 1}}, Pos: positionOf(node)}, nil
		case node.OC_FunctionInvocation() != nil:
			call := node.OC_FunctionInvocation()
			name := strings.ToLower(identifier(call.OC_FunctionName().GetText()))
			args := []ValueExpression{}
			for _, arg := range call.AllOC_Expression() {
				value, err := read(arg)
				if err != nil {
					return nil, err
				}
				args = append(args, value)
			}
			if _, aggregate := aggregateFunctions[name]; aggregate || name == "collect" {
				if len(args) != 1 {
					return nil, unsupported(node, "an aggregate without exactly one argument")
				}
				return AggregateExpression{Function: name, Value: args[0], Distinct: call.DISTINCT() != nil, Pos: positionOf(node)}, nil
			}
			if call.DISTINCT() != nil {
				return nil, unsupported(node, "DISTINCT on a scalar function")
			}
			return op("FUNCTION:"+name, args...), nil
		case node.OC_CaseExpression() != nil:
			c := node.OC_CaseExpression()
			direct := c.AllOC_Expression()
			var subject, otherwise ValueExpression
			index := 0
			if len(direct) > 0 && c.GetChild(1) != nil {
				// Direct expression order is optional subject followed by optional ELSE.
				for _, child := range c.GetChildren() {
					if _, ok := child.(parsing.IOC_CaseAlternativeContext); ok {
						break
					}
					if expression, ok := child.(parsing.IOC_ExpressionContext); ok {
						subject, err = read(expression)
						if err != nil {
							return nil, err
						}
						index++
					}
				}
			}
			args := []ValueExpression{}
			for _, alternative := range c.AllOC_CaseAlternative() {
				condition, err := read(alternative.OC_Expression(0))
				if err != nil {
					return nil, err
				}
				if subject != nil {
					condition = op("=", subject, condition)
				}
				value, err := read(alternative.OC_Expression(1))
				if err != nil {
					return nil, err
				}
				args = append(args, condition, value)
			}
			otherwise = LiteralExpression{Value: Literal{Kind: LiteralNull}}
			if len(direct) > index {
				otherwise, err = read(direct[len(direct)-1])
				if err != nil {
					return nil, err
				}
			}
			args = append(args, otherwise)
			return op("CASE", args...), nil
		case node.OC_Literal() != nil:
			literal := node.OC_Literal()
			if list := literal.OC_ListLiteral(); list != nil {
				args := []ValueExpression{}
				for _, item := range list.AllOC_Expression() {
					value, err := read(item)
					if err != nil {
						return nil, err
					}
					args = append(args, value)
				}
				return op("LIST", args...), nil
			}
			if mapping := literal.OC_MapLiteral(); mapping != nil {
				fields := []MapField{}
				seen := map[string]bool{}
				for i, key := range mapping.AllOC_PropertyKeyName() {
					name, err := namedIdentifier(key, "map field")
					if err != nil {
						return nil, err
					}
					if seen[name] {
						return nil, unsupported(key, "duplicate map fields")
					}
					seen[name] = true
					value, err := read(mapping.OC_Expression(i))
					if err != nil {
						return nil, err
					}
					fields = append(fields, MapField{Name: name, Value: value})
				}
				return MapExpression{Fields: fields}, nil
			}
			value, err := analyzeAtomLiteral(node)
			if err != nil {
				return nil, err
			}
			return LiteralExpression{Value: *value.literal}, nil
		default:
			return nil, unsupported(node, describeAtom(node))
		}
	default:
		return nil, unsupported(ctx, "this value expression")
	}
}
