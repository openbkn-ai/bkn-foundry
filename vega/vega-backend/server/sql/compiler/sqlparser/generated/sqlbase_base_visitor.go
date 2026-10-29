// Code generated from SqlBase.g4 by ANTLR 4.13.2. DO NOT EDIT.

package generated // SqlBase
import "github.com/antlr4-go/antlr/v4"

type BaseSqlBaseVisitor struct {
	*antlr.BaseParseTreeVisitor
}

func (v *BaseSqlBaseVisitor) VisitStatement(ctx *StatementContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseSqlBaseVisitor) VisitSelectQuery(ctx *SelectQueryContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseSqlBaseVisitor) VisitSelectItem(ctx *SelectItemContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseSqlBaseVisitor) VisitRelation(ctx *RelationContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseSqlBaseVisitor) VisitRelationPrimary(ctx *RelationPrimaryContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseSqlBaseVisitor) VisitJoinPart(ctx *JoinPartContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseSqlBaseVisitor) VisitSortItem(ctx *SortItemContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseSqlBaseVisitor) VisitLimitClause(ctx *LimitClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseSqlBaseVisitor) VisitExpressionList(ctx *ExpressionListContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseSqlBaseVisitor) VisitExpression(ctx *ExpressionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseSqlBaseVisitor) VisitOrExpression(ctx *OrExpressionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseSqlBaseVisitor) VisitAndExpression(ctx *AndExpressionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseSqlBaseVisitor) VisitNotExpression(ctx *NotExpressionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseSqlBaseVisitor) VisitPredicate(ctx *PredicateContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseSqlBaseVisitor) VisitAdditiveExpression(ctx *AdditiveExpressionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseSqlBaseVisitor) VisitMultiplicativeExpression(ctx *MultiplicativeExpressionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseSqlBaseVisitor) VisitUnaryExpression(ctx *UnaryExpressionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseSqlBaseVisitor) VisitPrimaryExpression(ctx *PrimaryExpressionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseSqlBaseVisitor) VisitFunctionCall(ctx *FunctionCallContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseSqlBaseVisitor) VisitTypeName(ctx *TypeNameContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseSqlBaseVisitor) VisitLiteral(ctx *LiteralContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseSqlBaseVisitor) VisitIdentifier(ctx *IdentifierContext) interface{} {
	return v.VisitChildren(ctx)
}
