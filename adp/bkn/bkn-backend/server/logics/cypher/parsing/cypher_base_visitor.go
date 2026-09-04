// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Code generated from Cypher.g4 by ANTLR 4.13.1. DO NOT EDIT.

package parsing // Cypher
import "github.com/antlr4-go/antlr/v4"

type BaseCypherVisitor struct {
	*antlr.BaseParseTreeVisitor
}

func (v *BaseCypherVisitor) VisitOC_Cypher(ctx *OC_CypherContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_Statement(ctx *OC_StatementContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_Query(ctx *OC_QueryContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_RegularQuery(ctx *OC_RegularQueryContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_Union(ctx *OC_UnionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_SingleQuery(ctx *OC_SingleQueryContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_SinglePartQuery(ctx *OC_SinglePartQueryContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_MultiPartQuery(ctx *OC_MultiPartQueryContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_UpdatingClause(ctx *OC_UpdatingClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_ReadingClause(ctx *OC_ReadingClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_Match(ctx *OC_MatchContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_Unwind(ctx *OC_UnwindContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_Merge(ctx *OC_MergeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_MergeAction(ctx *OC_MergeActionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_Create(ctx *OC_CreateContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_Set(ctx *OC_SetContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_SetItem(ctx *OC_SetItemContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_Delete(ctx *OC_DeleteContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_Remove(ctx *OC_RemoveContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_RemoveItem(ctx *OC_RemoveItemContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_InQueryCall(ctx *OC_InQueryCallContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_StandaloneCall(ctx *OC_StandaloneCallContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_YieldItems(ctx *OC_YieldItemsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_YieldItem(ctx *OC_YieldItemContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_With(ctx *OC_WithContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_Return(ctx *OC_ReturnContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_ProjectionBody(ctx *OC_ProjectionBodyContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_ProjectionItems(ctx *OC_ProjectionItemsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_ProjectionItem(ctx *OC_ProjectionItemContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_Order(ctx *OC_OrderContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_Skip(ctx *OC_SkipContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_Limit(ctx *OC_LimitContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_SortItem(ctx *OC_SortItemContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_Where(ctx *OC_WhereContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_Pattern(ctx *OC_PatternContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_PatternPart(ctx *OC_PatternPartContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_AnonymousPatternPart(ctx *OC_AnonymousPatternPartContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_PatternElement(ctx *OC_PatternElementContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_RelationshipsPattern(ctx *OC_RelationshipsPatternContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_NodePattern(ctx *OC_NodePatternContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_PatternElementChain(ctx *OC_PatternElementChainContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_RelationshipPattern(ctx *OC_RelationshipPatternContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_RelationshipDetail(ctx *OC_RelationshipDetailContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_Properties(ctx *OC_PropertiesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_RelationshipTypes(ctx *OC_RelationshipTypesContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_NodeLabels(ctx *OC_NodeLabelsContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_NodeLabel(ctx *OC_NodeLabelContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_RangeLiteral(ctx *OC_RangeLiteralContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_LabelName(ctx *OC_LabelNameContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_RelTypeName(ctx *OC_RelTypeNameContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_PropertyExpression(ctx *OC_PropertyExpressionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_Expression(ctx *OC_ExpressionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_OrExpression(ctx *OC_OrExpressionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_XorExpression(ctx *OC_XorExpressionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_AndExpression(ctx *OC_AndExpressionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_NotExpression(ctx *OC_NotExpressionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_ComparisonExpression(ctx *OC_ComparisonExpressionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_PartialComparisonExpression(ctx *OC_PartialComparisonExpressionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_StringListNullPredicateExpression(ctx *OC_StringListNullPredicateExpressionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_StringPredicateExpression(ctx *OC_StringPredicateExpressionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_ListPredicateExpression(ctx *OC_ListPredicateExpressionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_NullPredicateExpression(ctx *OC_NullPredicateExpressionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_AddOrSubtractExpression(ctx *OC_AddOrSubtractExpressionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_MultiplyDivideModuloExpression(ctx *OC_MultiplyDivideModuloExpressionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_PowerOfExpression(ctx *OC_PowerOfExpressionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_UnaryAddOrSubtractExpression(ctx *OC_UnaryAddOrSubtractExpressionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_ListOperatorExpression(ctx *OC_ListOperatorExpressionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_PropertyOrLabelsExpression(ctx *OC_PropertyOrLabelsExpressionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_PropertyLookup(ctx *OC_PropertyLookupContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_Atom(ctx *OC_AtomContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_CaseExpression(ctx *OC_CaseExpressionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_CaseAlternative(ctx *OC_CaseAlternativeContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_ListComprehension(ctx *OC_ListComprehensionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_PatternComprehension(ctx *OC_PatternComprehensionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_Quantifier(ctx *OC_QuantifierContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_FilterExpression(ctx *OC_FilterExpressionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_PatternPredicate(ctx *OC_PatternPredicateContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_ParenthesizedExpression(ctx *OC_ParenthesizedExpressionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_IdInColl(ctx *OC_IdInCollContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_FunctionInvocation(ctx *OC_FunctionInvocationContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_FunctionName(ctx *OC_FunctionNameContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_ExistentialSubquery(ctx *OC_ExistentialSubqueryContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_ExplicitProcedureInvocation(ctx *OC_ExplicitProcedureInvocationContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_ImplicitProcedureInvocation(ctx *OC_ImplicitProcedureInvocationContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_ProcedureResultField(ctx *OC_ProcedureResultFieldContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_ProcedureName(ctx *OC_ProcedureNameContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_Namespace(ctx *OC_NamespaceContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_Variable(ctx *OC_VariableContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_Literal(ctx *OC_LiteralContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_BooleanLiteral(ctx *OC_BooleanLiteralContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_NumberLiteral(ctx *OC_NumberLiteralContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_IntegerLiteral(ctx *OC_IntegerLiteralContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_DoubleLiteral(ctx *OC_DoubleLiteralContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_ListLiteral(ctx *OC_ListLiteralContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_MapLiteral(ctx *OC_MapLiteralContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_PropertyKeyName(ctx *OC_PropertyKeyNameContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_Parameter(ctx *OC_ParameterContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_SchemaName(ctx *OC_SchemaNameContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_ReservedWord(ctx *OC_ReservedWordContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_SymbolicName(ctx *OC_SymbolicNameContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_LeftArrowHead(ctx *OC_LeftArrowHeadContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_RightArrowHead(ctx *OC_RightArrowHeadContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseCypherVisitor) VisitOC_Dash(ctx *OC_DashContext) interface{} {
	return v.VisitChildren(ctx)
}
