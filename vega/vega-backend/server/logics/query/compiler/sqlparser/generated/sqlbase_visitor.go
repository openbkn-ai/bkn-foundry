// Code generated from SqlBase.g4 by ANTLR 4.13.2. DO NOT EDIT.

package generated // SqlBase
import "github.com/antlr4-go/antlr/v4"

// A complete Visitor for a parse tree produced by SqlBaseParser.
type SqlBaseVisitor interface {
	antlr.ParseTreeVisitor

	// Visit a parse tree produced by SqlBaseParser#statement.
	VisitStatement(ctx *StatementContext) interface{}

	// Visit a parse tree produced by SqlBaseParser#selectQuery.
	VisitSelectQuery(ctx *SelectQueryContext) interface{}

	// Visit a parse tree produced by SqlBaseParser#selectItem.
	VisitSelectItem(ctx *SelectItemContext) interface{}

	// Visit a parse tree produced by SqlBaseParser#relation.
	VisitRelation(ctx *RelationContext) interface{}

	// Visit a parse tree produced by SqlBaseParser#relationPrimary.
	VisitRelationPrimary(ctx *RelationPrimaryContext) interface{}

	// Visit a parse tree produced by SqlBaseParser#joinPart.
	VisitJoinPart(ctx *JoinPartContext) interface{}

	// Visit a parse tree produced by SqlBaseParser#sortItem.
	VisitSortItem(ctx *SortItemContext) interface{}

	// Visit a parse tree produced by SqlBaseParser#limitClause.
	VisitLimitClause(ctx *LimitClauseContext) interface{}

	// Visit a parse tree produced by SqlBaseParser#expressionList.
	VisitExpressionList(ctx *ExpressionListContext) interface{}

	// Visit a parse tree produced by SqlBaseParser#expression.
	VisitExpression(ctx *ExpressionContext) interface{}

	// Visit a parse tree produced by SqlBaseParser#orExpression.
	VisitOrExpression(ctx *OrExpressionContext) interface{}

	// Visit a parse tree produced by SqlBaseParser#andExpression.
	VisitAndExpression(ctx *AndExpressionContext) interface{}

	// Visit a parse tree produced by SqlBaseParser#notExpression.
	VisitNotExpression(ctx *NotExpressionContext) interface{}

	// Visit a parse tree produced by SqlBaseParser#predicate.
	VisitPredicate(ctx *PredicateContext) interface{}

	// Visit a parse tree produced by SqlBaseParser#additiveExpression.
	VisitAdditiveExpression(ctx *AdditiveExpressionContext) interface{}

	// Visit a parse tree produced by SqlBaseParser#multiplicativeExpression.
	VisitMultiplicativeExpression(ctx *MultiplicativeExpressionContext) interface{}

	// Visit a parse tree produced by SqlBaseParser#unaryExpression.
	VisitUnaryExpression(ctx *UnaryExpressionContext) interface{}

	// Visit a parse tree produced by SqlBaseParser#primaryExpression.
	VisitPrimaryExpression(ctx *PrimaryExpressionContext) interface{}

	// Visit a parse tree produced by SqlBaseParser#functionCall.
	VisitFunctionCall(ctx *FunctionCallContext) interface{}

	// Visit a parse tree produced by SqlBaseParser#typeName.
	VisitTypeName(ctx *TypeNameContext) interface{}

	// Visit a parse tree produced by SqlBaseParser#literal.
	VisitLiteral(ctx *LiteralContext) interface{}

	// Visit a parse tree produced by SqlBaseParser#identifier.
	VisitIdentifier(ctx *IdentifierContext) interface{}
}
