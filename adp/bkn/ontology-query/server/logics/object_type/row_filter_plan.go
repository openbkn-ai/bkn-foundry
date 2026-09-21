// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package object_type

import (
	cond "ontology-query/common/condition"
	"ontology-query/interfaces"
	rowfilter "ontology-query/logics/row_filter"
)

// compileRowFilter remains for package-local callers and tests.
func compileRowFilter(predicate interfaces.RowFilterPredicate,
	objectType interfaces.ObjectType) (*cond.CondCfg, []string, bool, error) {
	return rowfilter.Compile(predicate, objectType)
}

func andRowFilterCondition(user, row *cond.CondCfg) *cond.CondCfg {
	return rowfilter.And(user, row)
}
