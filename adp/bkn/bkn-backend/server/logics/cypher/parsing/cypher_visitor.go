// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Code generated from Cypher.g4 by ANTLR 4.13.1. DO NOT EDIT.

package parsing // Cypher
import "github.com/antlr4-go/antlr/v4"

// A complete Visitor for a parse tree produced by CypherParser.
type CypherVisitor interface {
	antlr.ParseTreeVisitor

	// Visit a parse tree produced by CypherParser#oC_Cypher.
	VisitOC_Cypher(ctx *OC_CypherContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_Statement.
	VisitOC_Statement(ctx *OC_StatementContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_Query.
	VisitOC_Query(ctx *OC_QueryContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_RegularQuery.
	VisitOC_RegularQuery(ctx *OC_RegularQueryContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_Union.
	VisitOC_Union(ctx *OC_UnionContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_SingleQuery.
	VisitOC_SingleQuery(ctx *OC_SingleQueryContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_SinglePartQuery.
	VisitOC_SinglePartQuery(ctx *OC_SinglePartQueryContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_MultiPartQuery.
	VisitOC_MultiPartQuery(ctx *OC_MultiPartQueryContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_UpdatingClause.
	VisitOC_UpdatingClause(ctx *OC_UpdatingClauseContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_ReadingClause.
	VisitOC_ReadingClause(ctx *OC_ReadingClauseContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_Match.
	VisitOC_Match(ctx *OC_MatchContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_Unwind.
	VisitOC_Unwind(ctx *OC_UnwindContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_Merge.
	VisitOC_Merge(ctx *OC_MergeContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_MergeAction.
	VisitOC_MergeAction(ctx *OC_MergeActionContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_Create.
	VisitOC_Create(ctx *OC_CreateContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_Set.
	VisitOC_Set(ctx *OC_SetContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_SetItem.
	VisitOC_SetItem(ctx *OC_SetItemContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_Delete.
	VisitOC_Delete(ctx *OC_DeleteContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_Remove.
	VisitOC_Remove(ctx *OC_RemoveContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_RemoveItem.
	VisitOC_RemoveItem(ctx *OC_RemoveItemContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_InQueryCall.
	VisitOC_InQueryCall(ctx *OC_InQueryCallContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_StandaloneCall.
	VisitOC_StandaloneCall(ctx *OC_StandaloneCallContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_YieldItems.
	VisitOC_YieldItems(ctx *OC_YieldItemsContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_YieldItem.
	VisitOC_YieldItem(ctx *OC_YieldItemContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_With.
	VisitOC_With(ctx *OC_WithContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_Return.
	VisitOC_Return(ctx *OC_ReturnContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_ProjectionBody.
	VisitOC_ProjectionBody(ctx *OC_ProjectionBodyContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_ProjectionItems.
	VisitOC_ProjectionItems(ctx *OC_ProjectionItemsContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_ProjectionItem.
	VisitOC_ProjectionItem(ctx *OC_ProjectionItemContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_Order.
	VisitOC_Order(ctx *OC_OrderContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_Skip.
	VisitOC_Skip(ctx *OC_SkipContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_Limit.
	VisitOC_Limit(ctx *OC_LimitContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_SortItem.
	VisitOC_SortItem(ctx *OC_SortItemContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_Where.
	VisitOC_Where(ctx *OC_WhereContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_Pattern.
	VisitOC_Pattern(ctx *OC_PatternContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_PatternPart.
	VisitOC_PatternPart(ctx *OC_PatternPartContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_AnonymousPatternPart.
	VisitOC_AnonymousPatternPart(ctx *OC_AnonymousPatternPartContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_PatternElement.
	VisitOC_PatternElement(ctx *OC_PatternElementContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_RelationshipsPattern.
	VisitOC_RelationshipsPattern(ctx *OC_RelationshipsPatternContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_NodePattern.
	VisitOC_NodePattern(ctx *OC_NodePatternContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_PatternElementChain.
	VisitOC_PatternElementChain(ctx *OC_PatternElementChainContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_RelationshipPattern.
	VisitOC_RelationshipPattern(ctx *OC_RelationshipPatternContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_RelationshipDetail.
	VisitOC_RelationshipDetail(ctx *OC_RelationshipDetailContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_Properties.
	VisitOC_Properties(ctx *OC_PropertiesContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_RelationshipTypes.
	VisitOC_RelationshipTypes(ctx *OC_RelationshipTypesContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_NodeLabels.
	VisitOC_NodeLabels(ctx *OC_NodeLabelsContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_NodeLabel.
	VisitOC_NodeLabel(ctx *OC_NodeLabelContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_RangeLiteral.
	VisitOC_RangeLiteral(ctx *OC_RangeLiteralContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_LabelName.
	VisitOC_LabelName(ctx *OC_LabelNameContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_RelTypeName.
	VisitOC_RelTypeName(ctx *OC_RelTypeNameContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_PropertyExpression.
	VisitOC_PropertyExpression(ctx *OC_PropertyExpressionContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_Expression.
	VisitOC_Expression(ctx *OC_ExpressionContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_OrExpression.
	VisitOC_OrExpression(ctx *OC_OrExpressionContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_XorExpression.
	VisitOC_XorExpression(ctx *OC_XorExpressionContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_AndExpression.
	VisitOC_AndExpression(ctx *OC_AndExpressionContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_NotExpression.
	VisitOC_NotExpression(ctx *OC_NotExpressionContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_ComparisonExpression.
	VisitOC_ComparisonExpression(ctx *OC_ComparisonExpressionContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_PartialComparisonExpression.
	VisitOC_PartialComparisonExpression(ctx *OC_PartialComparisonExpressionContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_StringListNullPredicateExpression.
	VisitOC_StringListNullPredicateExpression(ctx *OC_StringListNullPredicateExpressionContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_StringPredicateExpression.
	VisitOC_StringPredicateExpression(ctx *OC_StringPredicateExpressionContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_ListPredicateExpression.
	VisitOC_ListPredicateExpression(ctx *OC_ListPredicateExpressionContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_NullPredicateExpression.
	VisitOC_NullPredicateExpression(ctx *OC_NullPredicateExpressionContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_AddOrSubtractExpression.
	VisitOC_AddOrSubtractExpression(ctx *OC_AddOrSubtractExpressionContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_MultiplyDivideModuloExpression.
	VisitOC_MultiplyDivideModuloExpression(ctx *OC_MultiplyDivideModuloExpressionContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_PowerOfExpression.
	VisitOC_PowerOfExpression(ctx *OC_PowerOfExpressionContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_UnaryAddOrSubtractExpression.
	VisitOC_UnaryAddOrSubtractExpression(ctx *OC_UnaryAddOrSubtractExpressionContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_ListOperatorExpression.
	VisitOC_ListOperatorExpression(ctx *OC_ListOperatorExpressionContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_PropertyOrLabelsExpression.
	VisitOC_PropertyOrLabelsExpression(ctx *OC_PropertyOrLabelsExpressionContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_PropertyLookup.
	VisitOC_PropertyLookup(ctx *OC_PropertyLookupContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_Atom.
	VisitOC_Atom(ctx *OC_AtomContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_CaseExpression.
	VisitOC_CaseExpression(ctx *OC_CaseExpressionContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_CaseAlternative.
	VisitOC_CaseAlternative(ctx *OC_CaseAlternativeContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_ListComprehension.
	VisitOC_ListComprehension(ctx *OC_ListComprehensionContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_PatternComprehension.
	VisitOC_PatternComprehension(ctx *OC_PatternComprehensionContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_Quantifier.
	VisitOC_Quantifier(ctx *OC_QuantifierContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_FilterExpression.
	VisitOC_FilterExpression(ctx *OC_FilterExpressionContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_PatternPredicate.
	VisitOC_PatternPredicate(ctx *OC_PatternPredicateContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_ParenthesizedExpression.
	VisitOC_ParenthesizedExpression(ctx *OC_ParenthesizedExpressionContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_IdInColl.
	VisitOC_IdInColl(ctx *OC_IdInCollContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_FunctionInvocation.
	VisitOC_FunctionInvocation(ctx *OC_FunctionInvocationContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_FunctionName.
	VisitOC_FunctionName(ctx *OC_FunctionNameContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_ExistentialSubquery.
	VisitOC_ExistentialSubquery(ctx *OC_ExistentialSubqueryContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_ExplicitProcedureInvocation.
	VisitOC_ExplicitProcedureInvocation(ctx *OC_ExplicitProcedureInvocationContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_ImplicitProcedureInvocation.
	VisitOC_ImplicitProcedureInvocation(ctx *OC_ImplicitProcedureInvocationContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_ProcedureResultField.
	VisitOC_ProcedureResultField(ctx *OC_ProcedureResultFieldContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_ProcedureName.
	VisitOC_ProcedureName(ctx *OC_ProcedureNameContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_Namespace.
	VisitOC_Namespace(ctx *OC_NamespaceContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_Variable.
	VisitOC_Variable(ctx *OC_VariableContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_Literal.
	VisitOC_Literal(ctx *OC_LiteralContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_BooleanLiteral.
	VisitOC_BooleanLiteral(ctx *OC_BooleanLiteralContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_NumberLiteral.
	VisitOC_NumberLiteral(ctx *OC_NumberLiteralContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_IntegerLiteral.
	VisitOC_IntegerLiteral(ctx *OC_IntegerLiteralContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_DoubleLiteral.
	VisitOC_DoubleLiteral(ctx *OC_DoubleLiteralContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_ListLiteral.
	VisitOC_ListLiteral(ctx *OC_ListLiteralContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_MapLiteral.
	VisitOC_MapLiteral(ctx *OC_MapLiteralContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_PropertyKeyName.
	VisitOC_PropertyKeyName(ctx *OC_PropertyKeyNameContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_Parameter.
	VisitOC_Parameter(ctx *OC_ParameterContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_SchemaName.
	VisitOC_SchemaName(ctx *OC_SchemaNameContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_ReservedWord.
	VisitOC_ReservedWord(ctx *OC_ReservedWordContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_SymbolicName.
	VisitOC_SymbolicName(ctx *OC_SymbolicNameContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_LeftArrowHead.
	VisitOC_LeftArrowHead(ctx *OC_LeftArrowHeadContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_RightArrowHead.
	VisitOC_RightArrowHead(ctx *OC_RightArrowHeadContext) interface{}

	// Visit a parse tree produced by CypherParser#oC_Dash.
	VisitOC_Dash(ctx *OC_DashContext) interface{}
}
