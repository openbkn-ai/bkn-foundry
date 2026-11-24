// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package cypher

import (
	"context"
	"fmt"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics"
)

// A resource can keep a field readable while refusing a condition on it: vega-backend's
// filterable switch, false by default for JSON. The resource data API then rejects the
// whole read, which reached the caller as a failed query with no reason (#3365). The
// bind-join executor checks the conditions it pushes against the resources' own schemas
// first, and refuses the statement naming the property.
//
// Only a condition on the field itself is checked. The executor pushes comparisons
// (==, !=, in, like, prefix, null), and vega-backend checks those against the field,
// except on a text field with a keyword subfield in a queryable local index, which
// serves them instead (logics.VegaPropertyRefusesConditions). A condition kept as a
// residual is evaluated here and never reaches the data source.

// UnfilterableConditionError is a statement whose condition names a property the
// resource that stores it cannot filter on.
type UnfilterableConditionError struct {
	ObjectType string
	Property   string
}

func (e *UnfilterableConditionError) Error() string {
	return fmt.Sprintf("property %q of object type %q cannot be filtered on by its data source", e.Property, e.ObjectType)
}

// unfilterableColumns reads the resources a plan reads and lists, per resource, the
// columns their data API refuses a condition on. A lookup that fails leaves the check
// out: the data source still refuses the condition, as a parameter error.
func (s *cypherQueryService) unfilterableColumns(ctx context.Context, plan *Plan) map[string]map[string]bool {
	ids := planResourceIDs(plan)
	if len(ids) == 0 {
		return nil
	}
	resources, err := s.vba.GetResourcesByIDs(ctx, ids)
	if err != nil {
		logger.Infof("Cypher could not read the schemas of %d resources to check its conditions: %s",
			len(ids), common.SafeErrorSummary(err))
		return nil
	}
	unfilterable := map[string]map[string]bool{}
	for _, resource := range resources {
		if resource == nil {
			continue
		}
		for _, property := range resource.SchemaDefinition {
			if !logics.VegaPropertyRefusesConditions(resource, property) {
				continue
			}
			if unfilterable[resource.ID] == nil {
				unfilterable[resource.ID] = map[string]bool{}
			}
			unfilterable[resource.ID][property.Name] = true
		}
	}
	return unfilterable
}

// withUnfilterable is the schema a query read through the resource data API runs
// with. It is a copy: the compiled query's schema is not changed.
func (s *Schema) withUnfilterable(unfilterable map[string]map[string]bool) *Schema {
	if s == nil || len(unfilterable) == 0 {
		return s
	}
	copied := *s
	copied.unfilterable = unfilterable
	return &copied
}

// checkFilterable refuses a condition the executor would push to a table whose
// resource cannot filter on one of the columns it names.
func (e *bindJoinExecutor) checkFilterable(table int, predicate PlanPredicate) error {
	if e.schema == nil || len(e.schema.unfilterable) == 0 || table < 0 || table >= len(e.plan.Tables) {
		return nil
	}
	planTable := e.plan.Tables[table]
	columns := e.schema.unfilterable[planTable.ResourceID]
	if len(columns) == 0 {
		return nil
	}
	var refused string
	walkPredicateColumns(predicate, func(_ int, column string) {
		if refused == "" && columns[column] {
			refused = column
		}
	})
	if refused == "" {
		return nil
	}
	objectType := e.schema.objectTypesByID[planTable.ObjectTypeID]
	if objectType == nil {
		// A backing resource is no object type the caller named; the data
		// source refuses the condition as a parameter error.
		return nil
	}
	property := refused
	for _, dp := range objectType.DataProperties {
		if dp != nil && dp.MappedField != nil && dp.MappedField.Name == refused {
			property = dp.Name
			break
		}
	}
	return &UnfilterableConditionError{ObjectType: objectType.OTName, Property: property}
}
