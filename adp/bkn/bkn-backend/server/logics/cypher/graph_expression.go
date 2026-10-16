// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package cypher

import "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/cypher/parsing"

// ValueExpression is a logical value, independent of the SQL columns needed
// to materialize it. Predicates, projections and ordering share this tree.
type ValueExpression interface{ valueExpression() }
type PropertyExpression struct{ Ref PropertyRef }
type VariableExpression struct {
	Name string
	Pos  Position
}
type LiteralExpression struct{ Value Literal }
type MapField struct {
	Name  string
	Value ValueExpression
}
type MapExpression struct{ Fields []MapField }

func (PropertyExpression) valueExpression() {}
func (VariableExpression) valueExpression() {}
func (LiteralExpression) valueExpression()  {}
func (MapExpression) valueExpression()      {}

func analyzeGraphProjection(ctx parsing.IOC_ExpressionContext) (*Projection, error) {
	value, err := readValueExpression(ctx, 0)
	if err != nil {
		return nil, err
	}
	return &Projection{Value: value, Alias: ctx.GetText()}, nil
}
