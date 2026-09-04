// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/antlr4-go/antlr/v4"

	"bkn-backend/logics/cypher/parsing"
)

// The parser accepts all of openCypher; this file decides what the compiler
// will actually run. Everything outside the subset is refused here, by name
// and with a position, so an author reads "OPTIONAL MATCH is not supported"
// rather than a syntax error or, worse, silently different results.

// Unsupported reports a construct that is valid openCypher but outside the
// subset this compiler implements.
type Unsupported struct {
	Pos     Position
	Feature string
	Detail  string
}

func (u *Unsupported) Error() string {
	msg := fmt.Sprintf("line %d:%d: %s is not supported", u.Pos.Line, u.Pos.Column, u.Feature)
	if u.Detail != "" {
		msg += ": " + u.Detail
	}
	return msg
}

func positionOf(ctx antlr.ParserRuleContext) Position {
	token := ctx.GetStart()
	if token == nil {
		return Position{}
	}
	return Position{Line: token.GetLine(), Column: token.GetColumn()}
}

func unsupported(ctx antlr.ParserRuleContext, feature string) error {
	return &Unsupported{Pos: positionOf(ctx), Feature: feature}
}

func unsupportedf(ctx antlr.ParserRuleContext, feature, detail string, args ...any) error {
	return &Unsupported{Pos: positionOf(ctx), Feature: feature, Detail: fmt.Sprintf(detail, args...)}
}

// Analyze reads a parse tree as a query in the supported subset.
func Analyze(tree parsing.IOC_CypherContext) (*Query, error) {
	statement := tree.OC_Statement()
	if statement == nil {
		return nil, &Unsupported{Feature: "empty query"}
	}
	regular := statement.OC_Query().OC_RegularQuery()
	if regular == nil {
		return nil, unsupported(statement.OC_Query(), "procedure calls")
	}
	if len(regular.AllOC_Union()) > 0 {
		return nil, unsupported(regular, "UNION")
	}
	single := regular.OC_SingleQuery()
	if single.OC_MultiPartQuery() != nil {
		return nil, unsupported(single, "WITH")
	}

	part := single.OC_SinglePartQuery()
	if updating := part.AllOC_UpdatingClause(); len(updating) > 0 {
		// The subset is read-only by construction, not by permission check, so
		// a write clause is refused before anything is bound or generated.
		return nil, unsupportedf(updating[0], "writing", "this interface only runs read-only queries")
	}
	returning := part.OC_Return()
	if returning == nil {
		return nil, unsupportedf(part, "a query without RETURN", "add a RETURN clause")
	}

	reading := part.AllOC_ReadingClause()
	if len(reading) != 1 {
		return nil, unsupportedf(part, "multiple reading clauses",
			"a query must have exactly one MATCH, got %d reading clauses", len(reading))
	}
	match := reading[0].OC_Match()
	if match == nil {
		if reading[0].OC_Unwind() != nil {
			return nil, unsupported(reading[0], "UNWIND")
		}
		return nil, unsupported(reading[0], "procedure calls")
	}
	if match.OPTIONAL() != nil {
		return nil, unsupported(match, "OPTIONAL MATCH")
	}

	query := &Query{}
	pattern, err := analyzePattern(match.OC_Pattern())
	if err != nil {
		return nil, err
	}
	query.Pattern = *pattern

	if where := match.OC_Where(); where != nil {
		query.Where, err = analyzePredicates(where.OC_Expression())
		if err != nil {
			return nil, err
		}
	}
	if err := analyzeProjectionBody(query, returning.OC_ProjectionBody()); err != nil {
		return nil, err
	}
	return query, nil
}

func analyzePattern(ctx parsing.IOC_PatternContext) (*Pattern, error) {
	parts := ctx.AllOC_PatternPart()
	if len(parts) != 1 {
		// Several comma-separated parts is a cartesian product between them,
		// which the planner has no shape for yet.
		return nil, unsupportedf(ctx, "multiple pattern parts",
			"MATCH must contain a single path, got %d comma-separated patterns", len(parts))
	}
	part := parts[0]
	if part.OC_Variable() != nil {
		return nil, unsupported(part, "path variables")
	}

	element := part.OC_AnonymousPatternPart().OC_PatternElement()
	// A parenthesized pattern element wraps the real one; unwrap to the node.
	for element.OC_NodePattern() == nil {
		element = element.OC_PatternElement()
	}

	pattern := &Pattern{}
	node, err := analyzeNode(element.OC_NodePattern())
	if err != nil {
		return nil, err
	}
	pattern.Nodes = append(pattern.Nodes, *node)

	chains := element.AllOC_PatternElementChain()
	if len(chains) > 1 {
		// Multi-hop needs a join order decision the planner does not make yet.
		return nil, unsupportedf(ctx, "multi-hop patterns",
			"a path may contain at most one relationship, got %d", len(chains))
	}
	for _, chain := range chains {
		edge, err := analyzeRelationship(chain.OC_RelationshipPattern())
		if err != nil {
			return nil, err
		}
		node, err := analyzeNode(chain.OC_NodePattern())
		if err != nil {
			return nil, err
		}
		pattern.Edges = append(pattern.Edges, *edge)
		pattern.Nodes = append(pattern.Nodes, *node)
	}
	return pattern, nil
}

func analyzeNode(ctx parsing.IOC_NodePatternContext) (*NodeRef, error) {
	if ctx.OC_Properties() != nil {
		return nil, unsupportedf(ctx, "inline property maps", "write the condition in WHERE instead")
	}
	node := &NodeRef{Pos: positionOf(ctx)}
	if variable := ctx.OC_Variable(); variable != nil {
		node.Variable = identifier(variable.GetText())
	}

	labels := ctx.OC_NodeLabels()
	if labels == nil {
		// Without a label there is no object type, and without an object type
		// there is no table to read.
		return nil, unsupportedf(ctx, "nodes without a label",
			"every node must name one object type, as in (n:ObjectType)")
	}
	all := labels.AllOC_NodeLabel()
	if len(all) != 1 {
		return nil, unsupportedf(ctx, "multiple labels on one node",
			"a node maps to exactly one object type, got %d labels", len(all))
	}
	node.Label = identifier(all[0].OC_LabelName().GetText())
	return node, nil
}

func analyzeRelationship(ctx parsing.IOC_RelationshipPatternContext) (*EdgeRef, error) {
	edge := &EdgeRef{Pos: positionOf(ctx)}
	left, right := ctx.OC_LeftArrowHead() != nil, ctx.OC_RightArrowHead() != nil
	switch {
	case left && right:
		return nil, unsupportedf(ctx, "a relationship pointing both ways",
			"write either -[:TYPE]-> or <-[:TYPE]-")
	case right:
		edge.Direction = Outgoing
	case left:
		edge.Direction = Incoming
	default:
		// A relation type has a source and a target, so an undirected pattern
		// would have to be compiled as a union of both readings.
		return nil, unsupportedf(ctx, "undirected relationships",
			"write either -[:TYPE]-> or <-[:TYPE]-")
	}

	detail := ctx.OC_RelationshipDetail()
	if detail == nil {
		return nil, unsupportedf(ctx, "relationships without a type",
			"every relationship must name one relation type, as in -[:RELATION]->")
	}
	if detail.OC_Variable() != nil {
		return nil, unsupportedf(detail, "relationship variables",
			"a relationship is a join here and has no properties to project")
	}
	if detail.OC_RangeLiteral() != nil {
		return nil, unsupported(detail, "variable-length relationships")
	}
	if detail.OC_Properties() != nil {
		return nil, unsupportedf(detail, "inline property maps", "write the condition in WHERE instead")
	}

	types := detail.OC_RelationshipTypes()
	if types == nil {
		return nil, unsupportedf(detail, "relationships without a type",
			"every relationship must name one relation type, as in -[:RELATION]->")
	}
	names := types.AllOC_RelTypeName()
	if len(names) != 1 {
		return nil, unsupportedf(detail, "alternative relationship types",
			"a relationship maps to exactly one relation type, got %d", len(names))
	}
	edge.Type = identifier(names[0].GetText())
	return edge, nil
}

func analyzeProjectionBody(query *Query, ctx parsing.IOC_ProjectionBodyContext) error {
	query.Distinct = ctx.DISTINCT() != nil

	items := ctx.OC_ProjectionItems()
	if strings.HasPrefix(strings.TrimSpace(items.GetText()), "*") {
		// RETURN * would return whole nodes, and a node here is a row of a
		// table rather than a value the result format can carry.
		return unsupportedf(items, "RETURN *", "list the properties to return")
	}
	for _, item := range items.AllOC_ProjectionItem() {
		property, err := analyzePropertyRef(item.OC_Expression())
		if err != nil {
			return err
		}
		projection := Projection{Property: *property, Alias: property.String()}
		if variable := item.OC_Variable(); variable != nil {
			projection.Alias = identifier(variable.GetText())
		}
		query.Return = append(query.Return, projection)
	}

	if order := ctx.OC_Order(); order != nil {
		for _, item := range order.AllOC_SortItem() {
			property, err := analyzePropertyRef(item.OC_Expression())
			if err != nil {
				return err
			}
			query.OrderBy = append(query.OrderBy, SortKey{
				Property:   *property,
				Descending: item.DESCENDING() != nil || item.DESC() != nil,
			})
		}
	}
	if skip := ctx.OC_Skip(); skip != nil {
		value, err := analyzeRowCount(skip.OC_Expression(), "SKIP")
		if err != nil {
			return err
		}
		query.Skip = &value
	}
	if limit := ctx.OC_Limit(); limit != nil {
		value, err := analyzeRowCount(limit.OC_Expression(), "LIMIT")
		if err != nil {
			return err
		}
		query.Limit = &value
	}
	return nil
}

// analyzeRowCount reads SKIP and LIMIT, which must be plain non-negative
// integers: anything computed would have to be evaluated before the query is
// generated.
func analyzeRowCount(ctx parsing.IOC_ExpressionContext, clause string) (int64, error) {
	value, err := analyzeOperand(ctx)
	if err != nil {
		return 0, err
	}
	if value.literal == nil || value.literal.Kind != LiteralInteger {
		return 0, unsupportedf(ctx, "a non-integer "+clause, "%s takes an integer literal", clause)
	}
	if value.literal.Integer < 0 {
		return 0, unsupportedf(ctx, "a negative "+clause, "%s must not be negative", clause)
	}
	return value.literal.Integer, nil
}

func analyzePropertyRef(ctx parsing.IOC_ExpressionContext) (*PropertyRef, error) {
	value, err := analyzeOperand(ctx)
	if err != nil {
		return nil, err
	}
	if value.property == nil {
		return nil, unsupportedf(ctx, "an expression here",
			"only variable.property references are supported, got %s", value.literal.describe())
	}
	return value.property, nil
}

// analyzePredicates flattens the WHERE expression into the conjunction the
// planner expects. Only AND is accepted for now: OR and NOT change which rows
// a join produces, and getting that wrong returns wrong data rather than an
// error, so they wait until the planner handles them deliberately.
func analyzePredicates(ctx parsing.IOC_ExpressionContext) ([]Comparison, error) {
	orExpression := ctx.OC_OrExpression()
	xorExpressions := orExpression.AllOC_XorExpression()
	if len(xorExpressions) != 1 {
		return nil, unsupported(orExpression, "OR")
	}
	andExpressions := xorExpressions[0].AllOC_AndExpression()
	if len(andExpressions) != 1 {
		return nil, unsupported(xorExpressions[0], "XOR")
	}

	notExpressions := andExpressions[0].AllOC_NotExpression()
	comparisons := make([]Comparison, 0, len(notExpressions))
	for _, notExpression := range notExpressions {
		comparison, err := analyzeComparison(notExpression)
		if err != nil {
			return nil, err
		}
		comparisons = append(comparisons, *comparison)
	}
	return comparisons, nil
}

func analyzeComparison(ctx parsing.IOC_NotExpressionContext) (*Comparison, error) {
	if len(ctx.AllNOT()) > 0 {
		return nil, unsupported(ctx, "NOT")
	}
	comparisonExpression := ctx.OC_ComparisonExpression()
	// The left side is read first so that a predicate written with IN, IS
	// NULL or STARTS WITH is named for what it is: those forms carry no
	// comparison operator, and reporting the missing operator instead would
	// point at the wrong thing.
	left, err := analyzeStringListNullOperand(comparisonExpression.OC_StringListNullPredicateExpression())
	if err != nil {
		return nil, err
	}

	partials := comparisonExpression.AllOC_PartialComparisonExpression()
	switch {
	case len(partials) == 0:
		return nil, unsupportedf(comparisonExpression, "a non-comparison predicate",
			"WHERE takes comparisons such as n.property = 'value'")
	case len(partials) > 1:
		return nil, unsupportedf(comparisonExpression, "chained comparisons",
			"write a < b AND b < c instead")
	}
	right, err := analyzeStringListNullOperand(partials[0].OC_StringListNullPredicateExpression())
	if err != nil {
		return nil, err
	}
	if left.property == nil || right.literal == nil {
		// One side has to be a column and the other a constant; anything else
		// would need expression generation the subset does not have yet.
		return nil, unsupportedf(comparisonExpression, "this comparison",
			"a comparison must be variable.property against a literal")
	}
	if right.literal.Kind == LiteralNull {
		return nil, unsupportedf(comparisonExpression, "comparing against null",
			"use IS NULL semantics once they are supported")
	}
	return &Comparison{
		Left:     *left.property,
		Operator: comparisonOperator(partials[0]),
		Right:    *right.literal,
		Pos:      positionOf(comparisonExpression),
	}, nil
}

// comparisonOperator reads the operator token of a partial comparison. The
// whitespace tokens the grammar allows around it are skipped.
func comparisonOperator(ctx parsing.IOC_PartialComparisonExpressionContext) string {
	for i := 0; i < ctx.GetChildCount(); i++ {
		terminal, ok := ctx.GetChild(i).(antlr.TerminalNode)
		if !ok {
			continue
		}
		if text := strings.TrimSpace(terminal.GetText()); text != "" {
			return text
		}
	}
	return ""
}

// operand is either a column reference or a constant. Exactly one field is set.
type operand struct {
	property *PropertyRef
	literal  *Literal
}

func analyzeOperand(ctx parsing.IOC_ExpressionContext) (operand, error) {
	orExpression := ctx.OC_OrExpression()
	xorExpressions := orExpression.AllOC_XorExpression()
	if len(xorExpressions) != 1 {
		return operand{}, unsupported(orExpression, "OR")
	}
	andExpressions := xorExpressions[0].AllOC_AndExpression()
	if len(andExpressions) != 1 {
		return operand{}, unsupported(xorExpressions[0], "XOR")
	}
	notExpressions := andExpressions[0].AllOC_NotExpression()
	if len(notExpressions) != 1 {
		return operand{}, unsupported(andExpressions[0], "AND")
	}
	if len(notExpressions[0].AllNOT()) > 0 {
		return operand{}, unsupported(notExpressions[0], "NOT")
	}
	comparisonExpression := notExpressions[0].OC_ComparisonExpression()
	if len(comparisonExpression.AllOC_PartialComparisonExpression()) > 0 {
		return operand{}, unsupportedf(comparisonExpression, "a comparison here",
			"expected a value, not a condition")
	}
	return analyzeStringListNullOperand(comparisonExpression.OC_StringListNullPredicateExpression())
}

func analyzeStringListNullOperand(ctx parsing.IOC_StringListNullPredicateExpressionContext) (operand, error) {
	if len(ctx.AllOC_StringPredicateExpression()) > 0 {
		return operand{}, unsupported(ctx, "STARTS WITH, ENDS WITH and CONTAINS")
	}
	if len(ctx.AllOC_ListPredicateExpression()) > 0 {
		return operand{}, unsupported(ctx, "IN")
	}
	if len(ctx.AllOC_NullPredicateExpression()) > 0 {
		return operand{}, unsupported(ctx, "IS NULL and IS NOT NULL")
	}

	addOrSubtract := ctx.OC_AddOrSubtractExpression()
	multiplications := addOrSubtract.AllOC_MultiplyDivideModuloExpression()
	if len(multiplications) != 1 {
		return operand{}, unsupported(addOrSubtract, "arithmetic")
	}
	powers := multiplications[0].AllOC_PowerOfExpression()
	if len(powers) != 1 {
		return operand{}, unsupported(multiplications[0], "arithmetic")
	}
	unaries := powers[0].AllOC_UnaryAddOrSubtractExpression()
	if len(unaries) != 1 {
		return operand{}, unsupported(powers[0], "arithmetic")
	}
	return analyzeUnaryOperand(unaries[0])
}

func analyzeUnaryOperand(ctx parsing.IOC_UnaryAddOrSubtractExpressionContext) (operand, error) {
	negated := false
	for i := 0; i < ctx.GetChildCount(); i++ {
		terminal, ok := ctx.GetChild(i).(antlr.TerminalNode)
		if !ok {
			continue
		}
		if strings.TrimSpace(terminal.GetText()) == "-" {
			negated = !negated
		}
	}

	listOperator := ctx.OC_ListOperatorExpression()
	propertyOrLabels := listOperator.OC_PropertyOrLabelsExpression()
	if listOperator.GetChildCount() > 1 {
		return operand{}, unsupported(listOperator, "list indexing and slicing")
	}
	if propertyOrLabels.OC_NodeLabels() != nil {
		return operand{}, unsupported(propertyOrLabels, "label predicates")
	}

	value, err := analyzeAtomOperand(propertyOrLabels)
	if err != nil {
		return operand{}, err
	}
	if !negated {
		return value, nil
	}
	// A leading minus only means anything on a number; on anything else it is
	// arithmetic the subset does not generate.
	if value.literal == nil {
		return operand{}, unsupported(ctx, "arithmetic")
	}
	switch value.literal.Kind {
	case LiteralInteger:
		value.literal.Integer = -value.literal.Integer
	case LiteralFloat:
		value.literal.Float = -value.literal.Float
	default:
		return operand{}, unsupportedf(ctx, "negating a non-numeric literal",
			"cannot negate %s", value.literal.describe())
	}
	return value, nil
}

func analyzeAtomOperand(ctx parsing.IOC_PropertyOrLabelsExpressionContext) (operand, error) {
	atom := ctx.OC_Atom()
	lookups := ctx.AllOC_PropertyLookup()

	switch len(lookups) {
	case 0:
		if atom.OC_Variable() != nil {
			// A bare variable is a whole node, which cannot be projected or
			// compared as a value.
			return operand{}, unsupportedf(ctx, "referring to a node as a value",
				"use %s.property", identifier(atom.OC_Variable().GetText()))
		}
		return analyzeAtomLiteral(atom)
	case 1:
		variable := atom.OC_Variable()
		if variable == nil {
			return operand{}, unsupportedf(ctx, "property access on an expression",
				"only variable.property references are supported")
		}
		return operand{property: &PropertyRef{
			Variable: identifier(variable.GetText()),
			Property: identifier(lookups[0].OC_PropertyKeyName().GetText()),
			Pos:      positionOf(ctx),
		}}, nil
	default:
		return operand{}, unsupported(ctx, "nested property access")
	}
}

func analyzeAtomLiteral(ctx parsing.IOC_AtomContext) (operand, error) {
	literal := ctx.OC_Literal()
	if literal == nil {
		return operand{}, unsupported(ctx, describeAtom(ctx))
	}

	value := Literal{Pos: positionOf(literal)}
	switch {
	case literal.OC_BooleanLiteral() != nil:
		value.Kind = LiteralBoolean
		value.Boolean = literal.OC_BooleanLiteral().TRUE() != nil
	case literal.NULL() != nil:
		value.Kind = LiteralNull
	case literal.StringLiteral() != nil:
		decoded, err := decodeStringLiteral(literal.StringLiteral().GetText())
		if err != nil {
			return operand{}, unsupportedf(literal, "this string literal", "%v", err)
		}
		value.Kind = LiteralString
		value.String = decoded
	case literal.OC_NumberLiteral() != nil:
		number := literal.OC_NumberLiteral()
		if double := number.OC_DoubleLiteral(); double != nil {
			parsed, err := strconv.ParseFloat(double.GetText(), 64)
			if err != nil {
				return operand{}, unsupportedf(literal, "this number", "%v", err)
			}
			value.Kind = LiteralFloat
			value.Float = parsed
		} else {
			// Base 0 covers the decimal, hexadecimal and octal forms the
			// grammar allows, with the same reading Cypher gives them.
			parsed, err := strconv.ParseInt(number.GetText(), 0, 64)
			if err != nil {
				return operand{}, unsupportedf(literal, "this number", "%v", err)
			}
			value.Kind = LiteralInteger
			value.Integer = parsed
		}
	default:
		return operand{}, unsupported(literal, "list and map literals")
	}
	return operand{literal: &value}, nil
}

// describeAtom names the construct found where a value was expected, so the
// rejection says what was written rather than that something was wrong.
func describeAtom(ctx parsing.IOC_AtomContext) string {
	switch {
	case ctx.OC_FunctionInvocation() != nil:
		return "function calls"
	case ctx.COUNT() != nil:
		return "count(*)"
	case ctx.OC_Parameter() != nil:
		return "query parameters"
	case ctx.OC_CaseExpression() != nil:
		return "CASE"
	case ctx.OC_ListComprehension() != nil:
		return "list comprehensions"
	case ctx.OC_PatternComprehension() != nil:
		return "pattern comprehensions"
	case ctx.OC_Quantifier() != nil:
		return "quantified expressions"
	case ctx.OC_PatternPredicate() != nil:
		return "pattern predicates"
	case ctx.OC_ExistentialSubquery() != nil:
		return "EXISTS subqueries"
	case ctx.OC_ParenthesizedExpression() != nil:
		return "parenthesized expressions"
	default:
		return "this expression"
	}
}

// identifier strips the backticks of an escaped symbolic name, where a literal
// backtick is written doubled.
func identifier(text string) string {
	if len(text) >= 2 && strings.HasPrefix(text, "`") && strings.HasSuffix(text, "`") {
		return strings.ReplaceAll(text[1:len(text)-1], "``", "`")
	}
	return text
}

// decodeStringLiteral turns the source form of a string literal into its
// value. Decoding here means the target dialect escapes a Go string once, at
// generation, instead of trying to translate Cypher escapes into SQL ones.
func decodeStringLiteral(text string) (string, error) {
	if len(text) < 2 {
		return "", fmt.Errorf("malformed string literal %s", text)
	}
	body := text[1 : len(text)-1]

	var out strings.Builder
	out.Grow(len(body))
	for i := 0; i < len(body); {
		if body[i] != '\\' {
			out.WriteByte(body[i])
			i++
			continue
		}
		i++
		if i >= len(body) {
			return "", fmt.Errorf("string literal ends with a trailing backslash")
		}
		switch escape := body[i]; escape {
		case '\\', '\'', '"':
			out.WriteByte(escape)
			i++
		case 'b', 'B':
			out.WriteByte('\b')
			i++
		case 'f', 'F':
			out.WriteByte('\f')
			i++
		case 'n', 'N':
			out.WriteByte('\n')
			i++
		case 'r', 'R':
			out.WriteByte('\r')
			i++
		case 't', 'T':
			out.WriteByte('\t')
			i++
		case 'u', 'U':
			i++
			consumed, decoded, err := decodeUnicodeEscape(body[i:])
			if err != nil {
				return "", err
			}
			out.WriteRune(decoded)
			i += consumed
		default:
			return "", fmt.Errorf("unknown escape sequence \\%c", escape)
		}
	}
	return out.String(), nil
}

// decodeUnicodeEscape reads the four- or eight-digit form after \u. The
// eight-digit form is tried first because a valid one starts with four digits
// that would otherwise be read as the short form.
func decodeUnicodeEscape(rest string) (int, rune, error) {
	if len(rest) >= 8 {
		if value, err := strconv.ParseUint(rest[:8], 16, 32); err == nil {
			return 8, rune(value), nil
		}
	}
	if len(rest) < 4 {
		return 0, 0, fmt.Errorf("incomplete unicode escape sequence")
	}
	value, err := strconv.ParseUint(rest[:4], 16, 32)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid unicode escape sequence \\u%s", rest[:4])
	}
	decoded := rune(value)
	if utf16.IsSurrogate(decoded) {
		return 0, 0, fmt.Errorf("unpaired surrogate in unicode escape sequence \\u%s", rest[:4])
	}
	return 4, decoded, nil
}
