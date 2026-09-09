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
	"unicode"
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
	pattern, inline, err := analyzePattern(match.OC_Pattern())
	if err != nil {
		return nil, err
	}
	query.Pattern = *pattern

	if where := match.OC_Where(); where != nil {
		written, err := analyzePredicate(where.OC_Expression())
		if err != nil {
			return nil, err
		}
		inline = append(inline, written)
	}
	// Conditions from the pattern and from WHERE mean the same thing and are
	// joined the way Cypher joins them.
	query.Where = combine("AND", inline, positionOf(match))
	if err := analyzeProjectionBody(query, returning.OC_ProjectionBody()); err != nil {
		return nil, err
	}
	return query, nil
}

// analyzePattern reads the path and the conditions written inside it. Inline
// property maps come back as ordinary conditions, which is what Cypher defines
// them to be.
func analyzePattern(ctx parsing.IOC_PatternContext) (*Pattern, []Predicate, error) {
	parts := ctx.AllOC_PatternPart()
	if len(parts) != 1 {
		// Several comma-separated parts is a cartesian product between them,
		// which the planner has no shape for yet.
		return nil, nil, unsupportedf(ctx, "multiple pattern parts",
			"MATCH must contain a single path, got %d comma-separated patterns", len(parts))
	}
	part := parts[0]
	if part.OC_Variable() != nil {
		return nil, nil, unsupported(part, "path variables")
	}

	element := part.OC_AnonymousPatternPart().OC_PatternElement()
	// A parenthesized pattern element wraps the real one; unwrap to the node.
	for element.OC_NodePattern() == nil {
		element = element.OC_PatternElement()
	}

	pattern := &Pattern{}
	node, conditions, err := analyzeNode(element.OC_NodePattern(), 0)
	if err != nil {
		return nil, nil, err
	}
	pattern.Nodes = append(pattern.Nodes, *node)
	inline := conditions

	for i, chain := range element.AllOC_PatternElementChain() {
		edge, err := analyzeRelationship(chain.OC_RelationshipPattern())
		if err != nil {
			return nil, nil, err
		}
		node, conditions, err := analyzeNode(chain.OC_NodePattern(), i+1)
		if err != nil {
			return nil, nil, err
		}
		pattern.Edges = append(pattern.Edges, *edge)
		pattern.Nodes = append(pattern.Nodes, *node)
		inline = append(inline, conditions...)
	}
	return pattern, inline, nil
}

func analyzeNode(ctx parsing.IOC_NodePatternContext, index int) (*NodeRef, []Predicate, error) {
	node := &NodeRef{Pos: positionOf(ctx)}
	if variable := ctx.OC_Variable(); variable != nil {
		name, err := namedIdentifier(variable, "variable name")
		if err != nil {
			return nil, nil, err
		}
		node.Variable = name
	}

	var conditions []Predicate
	if properties := ctx.OC_Properties(); properties != nil {
		if node.Variable == "" {
			// An inline map on an anonymous node still has to name something
			// the planner can resolve. The name is unwritable in Cypher, so it
			// cannot collide with one the author chose.
			node.Variable = anonymousVariable(index)
			node.Anonymous = true
		}
		var err error
		conditions, err = analyzeInlineProperties(properties, node.Variable)
		if err != nil {
			return nil, nil, err
		}
	}

	labels := ctx.OC_NodeLabels()
	if labels == nil {
		// Without a label there is no object type, and without an object type
		// there is no table to read.
		return nil, nil, unsupportedf(ctx, "nodes without a label",
			"every node must name one object type, as in (n:ObjectType)")
	}
	all := labels.AllOC_NodeLabel()
	if len(all) != 1 {
		return nil, nil, unsupportedf(ctx, "multiple labels on one node",
			"a node maps to exactly one object type, got %d labels", len(all))
	}
	label, err := namedIdentifier(all[0].OC_LabelName(), "label")
	if err != nil {
		return nil, nil, err
	}
	node.Label = label
	return node, conditions, nil
}

// anonymousVariable names a node the query did not name. A backtick cannot
// appear unescaped in a Cypher identifier, so the name is unreachable from a
// query and cannot shadow one.
func anonymousVariable(index int) string {
	return "`anonymous-" + strconv.Itoa(index)
}

// analyzeInlineProperties reads (n:Label {a: 1, b: $b}) as the equalities it
// stands for. Cypher defines it as exactly that, and turning it into
// conditions here means the planner and the generator have one shape to
// handle rather than two.
func analyzeInlineProperties(ctx parsing.IOC_PropertiesContext, variable string) ([]Predicate, error) {
	if ctx.OC_Parameter() != nil {
		return nil, unsupportedf(ctx, "a parameter in place of a property map",
			"write the properties out, as in {name: $name}")
	}
	literal := ctx.OC_MapLiteral()
	if literal == nil {
		return nil, unsupported(ctx, "this property map")
	}

	keys := literal.AllOC_PropertyKeyName()
	values := literal.AllOC_Expression()
	conditions := make([]Predicate, 0, len(keys))
	for i, key := range keys {
		value, err := analyzeOperand(values[i])
		if err != nil {
			return nil, err
		}
		if value.operand() == nil {
			return nil, unsupportedf(values[i], "a property map holding something other than a value",
				"inline properties take literals or parameters")
		}
		if value.literal != nil && value.literal.Kind == LiteralNull {
			return nil, unsupportedf(values[i], "null in a property map",
				"a null there matches nothing; write IS NULL in WHERE if that is the intent")
		}
		property, err := namedIdentifier(key, "property name")
		if err != nil {
			return nil, err
		}
		conditions = append(conditions, Comparison{
			Left: PropertyRef{
				Variable: variable,
				Property: property,
				Pos:      positionOf(key),
			},
			Operator: "=",
			Right:    *value.operand(),
			Pos:      positionOf(key),
		})
	}
	return conditions, nil
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
		edge.Direction = Undirected
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
	relationshipType, err := namedIdentifier(names[0], "relationship type")
	if err != nil {
		return nil, err
	}
	edge.Type = relationshipType
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
		projection, err := analyzeProjection(item.OC_Expression())
		if err != nil {
			return err
		}
		if variable := item.OC_Variable(); variable != nil {
			alias, err := namedIdentifier(variable, "column name")
			if err != nil {
				return err
			}
			projection.Alias = alias
		}
		query.Return = append(query.Return, *projection)
	}

	if order := ctx.OC_Order(); order != nil {
		for _, item := range order.AllOC_SortItem() {
			key, err := analyzeSortKey(item.OC_Expression())
			if err != nil {
				return err
			}
			key.Descending = item.DESCENDING() != nil || item.DESC() != nil
			query.OrderBy = append(query.OrderBy, *key)
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

// analyzeProjection reads one RETURN item: a property, or an aggregate over
// one. The alias defaults to what was written, which is how Cypher names a
// column nobody named.
func analyzeProjection(ctx parsing.IOC_ExpressionContext) (*Projection, error) {
	if aggregate, ok, err := analyzeAggregate(ctx); err != nil {
		return nil, err
	} else if ok {
		return &Projection{Aggregate: aggregate, Alias: aggregate.String()}, nil
	}

	property, err := analyzePropertyRef(ctx)
	if err != nil {
		return nil, err
	}
	return &Projection{Property: property, Alias: property.String()}, nil
}

// analyzeSortKey reads one ORDER BY item. Besides a property it accepts the
// name of a returned column and an aggregate written out again, because with
// aggregates in RETURN those are the only ways to say what to sort by.
func analyzeSortKey(ctx parsing.IOC_ExpressionContext) (*SortKey, error) {
	if aggregate, ok, err := analyzeAggregate(ctx); err != nil {
		return nil, err
	} else if ok {
		return &SortKey{Aggregate: aggregate, Pos: aggregate.Pos}, nil
	}

	if name, ok := bareVariable(ctx); ok {
		return &SortKey{Alias: name, Pos: positionOf(ctx)}, nil
	}

	property, err := analyzePropertyRef(ctx)
	if err != nil {
		return nil, err
	}
	return &SortKey{Property: property, Pos: property.Pos}, nil
}

// bareVariable reports a term that is nothing but a name. In ORDER BY that is
// a reference to a returned column; everywhere else it is a whole node, which
// the analyzer refuses.
func bareVariable(ctx parsing.IOC_ExpressionContext) (string, bool) {
	atom, ok := singleExpressionAtom(ctx)
	if !ok || len(atom.propertyLookups) > 0 {
		return "", false
	}
	variable := atom.atom.OC_Variable()
	if variable == nil {
		return "", false
	}
	name := identifier(variable.GetText())
	// The grammar allows a pair of backticks with nothing between them. An
	// empty name refers to nothing, so it is left for the property path to
	// refuse by name rather than taken as a reference.
	return name, name != ""
}

// aggregateFunctions are the ones whose SQL spelling is the same and whose
// meaning over a grouped result matches. Anything else stays a function call,
// which is refused.
var aggregateFunctions = map[string]string{
	"count": "COUNT",
	"sum":   "SUM",
	"avg":   "AVG",
	"min":   "MIN",
	"max":   "MAX",
}

// analyzeAggregate reads count(*), count(x.p), sum(x.p) and their DISTINCT
// forms. It reports whether the term was an aggregate at all, so a caller can
// fall through to reading a plain property.
func analyzeAggregate(ctx parsing.IOC_ExpressionContext) (*Aggregate, bool, error) {
	term, ok := singleExpressionAtom(ctx)
	if !ok {
		return nil, false, nil
	}
	if len(term.propertyLookups) > 0 {
		return nil, false, nil
	}

	// count(*) is its own shape in the grammar rather than a function call.
	if count := term.atom.COUNT(); count != nil {
		return &Aggregate{Function: "COUNT", Name: count.GetText(), Pos: positionOf(term.atom)}, true, nil
	}

	invocation := term.atom.OC_FunctionInvocation()
	if invocation == nil {
		return nil, false, nil
	}
	written := identifier(invocation.OC_FunctionName().GetText())
	name := strings.ToLower(written)
	function, isAggregate := aggregateFunctions[name]
	if !isAggregate {
		return nil, false, nil
	}

	arguments := invocation.AllOC_Expression()
	if len(arguments) != 1 {
		return nil, false, unsupportedf(invocation, "an aggregate over "+strconv.Itoa(len(arguments))+" arguments",
			"%s takes one property", name)
	}
	property, err := analyzePropertyRef(arguments[0])
	if err != nil {
		return nil, false, err
	}
	return &Aggregate{
		Function: function,
		Name:     written,
		Distinct: invocation.DISTINCT() != nil,
		Property: property,
		Pos:      positionOf(invocation),
	}, true, nil
}

// expressionAtom is one term stripped of the precedence chain around it: the
// atom itself and the property lookups applied to it.
type expressionAtom struct {
	atom            parsing.IOC_AtomContext
	propertyLookups []parsing.IOC_PropertyLookupContext
}

// singleExpressionAtom descends an expression that is one term, reporting
// false for anything with an operator in it.
func singleExpressionAtom(ctx parsing.IOC_ExpressionContext) (expressionAtom, bool) {
	xors := ctx.OC_OrExpression().AllOC_XorExpression()
	if len(xors) != 1 {
		return expressionAtom{}, false
	}
	ands := xors[0].AllOC_AndExpression()
	if len(ands) != 1 {
		return expressionAtom{}, false
	}
	nots := ands[0].AllOC_NotExpression()
	if len(nots) != 1 || len(nots[0].AllNOT()) > 0 {
		return expressionAtom{}, false
	}
	comparison := nots[0].OC_ComparisonExpression()
	if len(comparison.AllOC_PartialComparisonExpression()) > 0 {
		return expressionAtom{}, false
	}
	stringListNull := comparison.OC_StringListNullPredicateExpression()
	if len(stringListNull.AllOC_StringPredicateExpression())+
		len(stringListNull.AllOC_ListPredicateExpression())+
		len(stringListNull.AllOC_NullPredicateExpression()) > 0 {
		return expressionAtom{}, false
	}
	multiplications := stringListNull.OC_AddOrSubtractExpression().AllOC_MultiplyDivideModuloExpression()
	if len(multiplications) != 1 {
		return expressionAtom{}, false
	}
	powers := multiplications[0].AllOC_PowerOfExpression()
	if len(powers) != 1 {
		return expressionAtom{}, false
	}
	unaries := powers[0].AllOC_UnaryAddOrSubtractExpression()
	if len(unaries) != 1 {
		return expressionAtom{}, false
	}
	listOperator := unaries[0].OC_ListOperatorExpression()
	if listOperator.GetChildCount() > 1 {
		return expressionAtom{}, false
	}
	propertyOrLabels := listOperator.OC_PropertyOrLabelsExpression()
	if propertyOrLabels.OC_NodeLabels() != nil {
		return expressionAtom{}, false
	}
	return expressionAtom{atom: propertyOrLabels.OC_Atom(),
		propertyLookups: propertyOrLabels.AllOC_PropertyLookup()}, true
}

func analyzePropertyRef(ctx parsing.IOC_ExpressionContext) (*PropertyRef, error) {
	value, err := analyzeOperand(ctx)
	if err != nil {
		return nil, err
	}
	if value.property == nil {
		if value.literal == nil {
			return nil, unsupportedf(ctx, "an expression here",
				"only variable.property references are supported")
		}
		return nil, unsupportedf(ctx, "an expression here",
			"only variable.property references are supported, got %s", value.literal.describe())
	}
	return value.property, nil
}

// analyzePredicate reads the WHERE expression as a tree. The boolean grammar
// is a chain of one rule per precedence level, so the descent below is the
// grammar read downwards: OR over XOR over AND over NOT over comparison.
func analyzePredicate(ctx parsing.IOC_ExpressionContext) (Predicate, error) {
	return analyzeOrPredicate(ctx.OC_OrExpression())
}

func analyzeOrPredicate(ctx parsing.IOC_OrExpressionContext) (Predicate, error) {
	operands := make([]Predicate, 0, len(ctx.AllOC_XorExpression()))
	for _, xor := range ctx.AllOC_XorExpression() {
		operand, err := analyzeXorPredicate(xor)
		if err != nil {
			return nil, err
		}
		operands = append(operands, operand)
	}
	return combine("OR", operands, positionOf(ctx)), nil
}

func analyzeXorPredicate(ctx parsing.IOC_XorExpressionContext) (Predicate, error) {
	// XOR has no portable spelling: MySQL has the operator, PostgreSQL does
	// not, and the rewrites differ in how they treat a null operand. It is
	// rare enough to refuse rather than to guess at.
	if len(ctx.AllOC_AndExpression()) > 1 {
		return nil, unsupportedf(ctx, "XOR", "write it with AND, OR and NOT")
	}
	operands := make([]Predicate, 0, len(ctx.AllOC_AndExpression()))
	for _, and := range ctx.AllOC_AndExpression() {
		operand, err := analyzeAndPredicate(and)
		if err != nil {
			return nil, err
		}
		operands = append(operands, operand)
	}
	return combine("XOR", operands, positionOf(ctx)), nil
}

func analyzeAndPredicate(ctx parsing.IOC_AndExpressionContext) (Predicate, error) {
	operands := make([]Predicate, 0, len(ctx.AllOC_NotExpression()))
	for _, not := range ctx.AllOC_NotExpression() {
		operand, err := analyzeNotPredicate(not)
		if err != nil {
			return nil, err
		}
		operands = append(operands, operand)
	}
	return combine("AND", operands, positionOf(ctx)), nil
}

// combine keeps a single operand as itself rather than wrapping it in an
// operator with nothing to combine, so the tree carries only what was written.
func combine(operator string, operands []Predicate, pos Position) Predicate {
	switch len(operands) {
	case 0:
		// A query with neither a WHERE nor inline properties has no condition
		// at all, which is not the same as one that is always true.
		return nil
	case 1:
		return operands[0]
	}
	return LogicalOperator{Operator: operator, Operands: operands, Pos: pos}
}

func analyzeNotPredicate(ctx parsing.IOC_NotExpressionContext) (Predicate, error) {
	inner, err := analyzeComparison(ctx.OC_ComparisonExpression())
	if err != nil {
		return nil, err
	}
	// Two NOTs cancel, which is worth doing here rather than emitting them:
	// the generated SQL should read like the condition, not like its history.
	if len(ctx.AllNOT())%2 == 1 {
		return Negation{Operand: inner, Pos: positionOf(ctx)}, nil
	}
	return inner, nil
}

func analyzeComparison(ctx parsing.IOC_ComparisonExpressionContext) (Predicate, error) {
	// The left side is read first so that a predicate written with IN or IS
	// NULL is recognised as one: those forms carry no comparison operator, and
	// reporting the missing operator instead would point at the wrong thing.
	left, leftValue, err := analyzeStringListNullPredicate(ctx.OC_StringListNullPredicateExpression(), groupsConditions)
	if err != nil {
		return nil, err
	}

	partials := ctx.AllOC_PartialComparisonExpression()
	switch {
	case len(partials) == 0:
		if left == nil {
			return nil, unsupportedf(ctx, "a non-comparison predicate",
				"WHERE takes comparisons such as n.property = 'value'")
		}
		return left, nil
	case len(partials) > 1:
		return nil, unsupportedf(ctx, "chained comparisons",
			"write a < b AND b < c instead")
	}
	if left != nil {
		return nil, unsupportedf(ctx, "comparing a condition",
			"a comparison must be variable.property against a value")
	}

	right, rightValue, err := analyzeStringListNullPredicate(partials[0].OC_StringListNullPredicateExpression(), groupsConditions)
	if err != nil {
		return nil, err
	}
	if right != nil {
		return nil, unsupportedf(ctx, "comparing a condition",
			"a comparison must be variable.property against a value")
	}
	if leftValue.property == nil || rightValue.property != nil || rightValue.operand() == nil {
		// One side has to be a column and the other a value; anything else
		// would need expression generation the subset does not have yet.
		return nil, unsupportedf(ctx, "this comparison",
			"a comparison must be variable.property against a literal or a parameter")
	}
	if rightValue.literal != nil && rightValue.literal.Kind == LiteralNull {
		return nil, unsupportedf(ctx, "comparing against null",
			"write IS NULL or IS NOT NULL instead")
	}
	return Comparison{
		Left:     *leftValue.property,
		Operator: comparisonOperator(partials[0]),
		Right:    *rightValue.operand(),
		Pos:      positionOf(ctx),
	}, nil
}

// analyzeStringListNullPredicate reads one side of a comparison. A side that
// carries an IN or IS NULL suffix is a predicate in its own right and is
// returned as one; otherwise it is a value.
// grouping says whether parentheses at this position mean a grouped condition.
// In a WHERE they do, and refusing them would leave OR and AND stuck in their
// default precedence. In a projection they do not: there is nothing there to
// group, so they are refused by name.
type grouping bool

const (
	groupsConditions   grouping = true
	refusesParentheses grouping = false
)

func analyzeStringListNullPredicate(ctx parsing.IOC_StringListNullPredicateExpressionContext,
	parentheses grouping) (Predicate, operand, error) {

	if len(ctx.AllOC_StringPredicateExpression()) > 0 {
		return nil, operand{}, unsupported(ctx, "STARTS WITH, ENDS WITH and CONTAINS")
	}

	// Parentheses are how a reader groups OR against AND, so a parenthesised
	// condition is read as one rather than refused: without it the operators
	// below could only ever be written in their default precedence.
	if parentheses == groupsConditions &&
		len(ctx.AllOC_ListPredicateExpression())+len(ctx.AllOC_NullPredicateExpression()) == 0 {
		if inner, ok := parenthesizedExpression(ctx.OC_AddOrSubtractExpression()); ok {
			predicate, err := analyzePredicate(inner)
			if err != nil {
				return nil, operand{}, err
			}
			return predicate, operand{}, nil
		}
	}

	subject, err := analyzeAddOrSubtractOperand(ctx.OC_AddOrSubtractExpression())
	if err != nil {
		return nil, operand{}, err
	}

	lists := ctx.AllOC_ListPredicateExpression()
	nulls := ctx.AllOC_NullPredicateExpression()
	if len(lists)+len(nulls) == 0 {
		return nil, subject, nil
	}
	if len(lists)+len(nulls) > 1 {
		return nil, operand{}, unsupportedf(ctx, "combining IN and IS NULL in one term",
			"write them as separate terms joined by AND")
	}
	if subject.property == nil {
		return nil, operand{}, unsupportedf(ctx, "this predicate",
			"IN and IS NULL apply to variable.property")
	}

	if len(nulls) == 1 {
		return NullCheck{
			Property: *subject.property,
			Negated:  nulls[0].NOT() != nil,
			Pos:      positionOf(ctx),
		}, operand{}, nil
	}

	values, err := analyzeListOperands(lists[0].OC_AddOrSubtractExpression())
	if err != nil {
		return nil, operand{}, err
	}
	return Membership{Property: *subject.property, Values: values, Pos: positionOf(ctx)}, operand{}, nil
}

// analyzeListOperands reads the right side of IN, which has to be a list
// written in the query. A list computed at run time would need evaluation the
// compiler does not do.
func analyzeListOperands(ctx parsing.IOC_AddOrSubtractExpressionContext) ([]Operand, error) {
	multiplications := ctx.AllOC_MultiplyDivideModuloExpression()
	if len(multiplications) != 1 {
		return nil, unsupported(ctx, "arithmetic")
	}
	atom, err := singleAtom(multiplications[0])
	if err != nil {
		return nil, unsupportedf(ctx, "IN over something other than a list",
			"write the values as a list, as in n.property IN [1, 2]")
	}
	literal := atom.OC_Literal()
	if literal == nil || literal.OC_ListLiteral() == nil {
		return nil, unsupportedf(ctx, "IN over something other than a list",
			"write the values as a list, as in n.property IN [1, 2]")
	}

	var values []Operand
	for _, element := range literal.OC_ListLiteral().AllOC_Expression() {
		value, err := analyzeOperand(element)
		if err != nil {
			return nil, err
		}
		if value.operand() == nil || value.property != nil {
			return nil, unsupportedf(element, "a list holding something other than values",
				"IN takes literals or parameters")
		}
		if value.literal != nil && value.literal.Kind == LiteralNull {
			return nil, unsupportedf(element, "null in an IN list",
				"a null there matches nothing and hides a mistake")
		}
		values = append(values, *value.operand())
	}
	return values, nil
}

// parenthesizedExpression reports the expression inside ( ), when the term is
// nothing but a parenthesised one.
func parenthesizedExpression(ctx parsing.IOC_AddOrSubtractExpressionContext) (parsing.IOC_ExpressionContext, bool) {
	multiplications := ctx.AllOC_MultiplyDivideModuloExpression()
	if len(multiplications) != 1 {
		return nil, false
	}
	atom, err := singleAtom(multiplications[0])
	if err != nil || atom == nil {
		return nil, false
	}
	parenthesized := atom.OC_ParenthesizedExpression()
	if parenthesized == nil {
		return nil, false
	}
	return parenthesized.OC_Expression(), true
}

// singleAtom descends the arithmetic chain to the one atom under it, refusing
// anything that is an operation rather than a value.
func singleAtom(ctx parsing.IOC_MultiplyDivideModuloExpressionContext) (parsing.IOC_AtomContext, error) {
	powers := ctx.AllOC_PowerOfExpression()
	if len(powers) != 1 {
		return nil, unsupported(ctx, "arithmetic")
	}
	unaries := powers[0].AllOC_UnaryAddOrSubtractExpression()
	if len(unaries) != 1 {
		return nil, unsupported(powers[0], "arithmetic")
	}
	listOperator := unaries[0].OC_ListOperatorExpression()
	if listOperator.GetChildCount() > 1 {
		return nil, unsupported(listOperator, "list indexing and slicing")
	}
	propertyOrLabels := listOperator.OC_PropertyOrLabelsExpression()
	if len(propertyOrLabels.AllOC_PropertyLookup()) > 0 {
		return nil, unsupportedf(propertyOrLabels, "a property here", "expected a value")
	}
	return propertyOrLabels.OC_Atom(), nil
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

// operand is what one side of a comparison reads as: a column reference, a
// constant, or a parameter the request supplies. Exactly one field is set.
type operand struct {
	property  *PropertyRef
	literal   *Literal
	parameter *ParameterRef
}

// operand returns the value this side carries, or nil when it is a column.
func (o operand) operand() *Operand {
	switch {
	case o.literal != nil:
		return &Operand{Literal: o.literal}
	case o.parameter != nil:
		return &Operand{Parameter: o.parameter}
	default:
		return nil
	}
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
	predicate, value, err := analyzeStringListNullPredicate(comparisonExpression.OC_StringListNullPredicateExpression(), refusesParentheses)
	if err != nil {
		return operand{}, err
	}
	if predicate != nil {
		return operand{}, unsupportedf(comparisonExpression, "a condition here", "expected a value")
	}
	return value, nil
}

// analyzeAddOrSubtractOperand reads a value, refusing the arithmetic the
// grammar allows around it.
func analyzeAddOrSubtractOperand(ctx parsing.IOC_AddOrSubtractExpressionContext) (operand, error) {
	multiplications := ctx.AllOC_MultiplyDivideModuloExpression()
	if len(multiplications) != 1 {
		return operand{}, unsupported(ctx, "arithmetic")
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
	if value.parameter != nil {
		return operand{}, unsupportedf(ctx, "negating a parameter",
			"pass the negative value instead")
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
		if parameter := atom.OC_Parameter(); parameter != nil {
			name, err := parameterName(parameter)
			if err != nil {
				return operand{}, err
			}
			return operand{parameter: &ParameterRef{Name: name, Pos: positionOf(parameter)}}, nil
		}
		if variable := atom.OC_Variable(); variable != nil {
			name, err := namedIdentifier(variable, "variable name")
			if err != nil {
				return operand{}, err
			}
			// A bare variable is a whole node, which cannot be projected or
			// compared as a value.
			return operand{}, unsupportedf(ctx, "referring to a node as a value",
				"use %s.property", name)
		}
		return analyzeAtomLiteral(atom)
	case 1:
		variable := atom.OC_Variable()
		if variable == nil {
			return operand{}, unsupportedf(ctx, "property access on an expression",
				"only variable.property references are supported")
		}
		name, err := namedIdentifier(variable, "variable name")
		if err != nil {
			return operand{}, err
		}
		property, err := namedIdentifier(lookups[0].OC_PropertyKeyName(), "property name")
		if err != nil {
			return operand{}, err
		}
		return operand{property: &PropertyRef{
			Variable: name,
			Property: property,
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

// parameterName reads $name. The numeric form ($0) is refused: it is a
// positional parameter, and the request carries parameters by name.
func parameterName(ctx parsing.IOC_ParameterContext) (string, error) {
	if symbolic := ctx.OC_SymbolicName(); symbolic != nil {
		return namedIdentifier(symbolic, "parameter name")
	}
	return "", unsupportedf(ctx, "positional parameters", "name the parameter, as in $value")
}

// identifier strips the backticks of an escaped symbolic name, where a literal
// backtick is written doubled.
//
// The grammar allows a pair of backticks with nothing between them, so this can
// produce an empty name. Callers reject that: an empty name matches nothing in
// the model, and letting one through reaches SQL as an empty identifier, which
// the database rejects with an error the caller cannot act on.
func identifier(text string) string {
	if len(text) >= 2 && strings.HasPrefix(text, "`") && strings.HasSuffix(text, "`") {
		return strings.ReplaceAll(text[1:len(text)-1], "``", "`")
	}
	return text
}

// namedIdentifier is identifier for the places where an empty name is not a
// name at all: a variable, a label, a property, an alias.
func namedIdentifier(ctx antlr.ParserRuleContext, kind string) (string, error) {
	name := identifier(ctx.GetText())
	if name == "" {
		return "", unsupportedf(ctx, "an empty "+kind,
			"a pair of backticks with nothing between them names nothing")
	}
	return name, nil
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
// eight-digit form is tried first because the lexer matches greedily: eight
// hex digits are one escape, not a four-digit escape followed by four literal
// characters, and reading them the other way would silently produce a
// different string.
func decodeUnicodeEscape(rest string) (int, rune, error) {
	if len(rest) >= 8 {
		if value, err := strconv.ParseUint(rest[:8], 16, 32); err == nil {
			decoded, err := codePoint(value, rest[:8])
			if err != nil {
				return 0, 0, err
			}
			return 8, decoded, nil
		}
	}
	if len(rest) < 4 {
		return 0, 0, fmt.Errorf("incomplete unicode escape sequence")
	}
	value, err := strconv.ParseUint(rest[:4], 16, 32)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid unicode escape sequence \\u%s", rest[:4])
	}
	decoded, err := codePoint(value, rest[:4])
	if err != nil {
		return 0, 0, err
	}
	return 4, decoded, nil
}

// codePoint turns a parsed escape into a rune, refusing the values that are
// not one. Eight hex digits reach well past the last code point, and a rune is
// a signed 32-bit value, so converting without this check would wrap a large
// escape into a negative rune that later encodes as a replacement character --
// a query that silently means something else instead of an error.
func codePoint(value uint64, digits string) (rune, error) {
	if value > unicode.MaxRune {
		return 0, fmt.Errorf("unicode escape sequence \\u%s is beyond the last code point", digits)
	}
	decoded := rune(value)
	if utf16.IsSurrogate(decoded) {
		return 0, fmt.Errorf("unpaired surrogate in unicode escape sequence \\u%s", digits)
	}
	return decoded, nil
}
