// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package object_type

import (
	cond "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common/condition"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	rowfilter "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/logics/row_filter"
)

// compileRowFilter remains for package-local callers and tests.
func compileRowFilter(predicate interfaces.RowFilterPredicate,
	objectType interfaces.ObjectType) (*cond.CondCfg, []string, bool, error) {
	return rowfilter.Compile(predicate, objectType)
}

func andRowFilterCondition(user, row *cond.CondCfg) *cond.CondCfg {
	return rowfilter.And(user, row)
}
