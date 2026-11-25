// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_target

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/jsoncodec"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
)

// Columns of a table result. Every other column is an event attribute.
const (
	eventKeyColumn  = "key"
	eventNameColumn = "name"
)

// eventFunctionTimeoutSeconds bounds one call of a rule function.
const eventFunctionTimeoutSeconds int64 = 300

// eventTable is a table result: its column names and rows.
type eventTable struct {
	Columns []string
	Entries []map[string]any
	Limited bool
}

// byCypher runs the query with the check's parameters, $check_time and $check_date among them.
// It returns either a table with a key column, or one column of nodes of the bound object type.
func (s *Selector) byCypher(ctx context.Context, knID, branch string, cypher *interfaces.ActionCypherTargets,
	subject *interfaces.ObjectType, clock CheckClock) (*Selection, error) {
	if s.Rules == nil {
		return nil, selectionFailed("Cypher queries are not available")
	}
	parameters, err := clock.parameters(cypher.Parameters)
	if err != nil {
		return nil, &SelectionError{Reason: "targets.cypher: " + err.Error()}
	}
	for _, builtIn := range []string{interfaces.ActionValueFromCheckTime, interfaces.ActionValueFromCheckDate} {
		parameters[builtIn], _ = clock.Value(builtIn, nil)
	}
	result, err := s.Rules.QueryCypher(ctx, knID, branch, &interfaces.CypherQueryRequest{Query: cypher.Query, Parameters: parameters})
	if err != nil {
		return nil, selectionFailed("the Cypher query failed: %s", DependencyReason(err))
	}
	rowFilter := false
	for _, impact := range result.PermissionImpacts {
		if impact.ObjectTypeID == subject.OTID && impact.RowFilterApplied {
			rowFilter = true
		}
	}
	columns := make([]string, 0, len(result.Columns))
	nodeColumn := ""
	for _, column := range result.Columns {
		columns = append(columns, column.Name)
		valueType := result.ValueTypes[column.Name]
		if valueType == nil || valueType.Kind != interfaces.CypherKindObject || valueType.ObjectTypeID != subject.OTID {
			continue
		}
		if nodeColumn != "" {
			return nil, selectionFailed("the Cypher query returns more than one column of %s nodes; return one, or a key column", subject.OTID)
		}
		nodeColumn = column.Name
	}
	table := eventTable{Columns: columns, Entries: result.Entries, Limited: result.Limited}
	var selection *Selection
	if nodeColumn != "" {
		selection, err = nodeHits(table, nodeColumn, subject)
	} else {
		selection, err = tableHits(table, subject)
		if err != nil {
			err = selectionFailed("the Cypher query must return a key column with the primary key of %s, or one column of %s nodes",
				subject.OTID, subject.OTID)
		}
	}
	if err != nil {
		return nil, err
	}
	selection.RowFilterApplied = &rowFilter
	return selection, nil
}

// nodeHits reads a result whose column holds nodes of the subject: the node's key is the hit's,
// a name column names it, and every other column is an attribute.
func nodeHits(table eventTable, nodeColumn string, subject *interfaces.ObjectType) (*Selection, error) {
	selection := &Selection{Limited: table.Limited}
	for index, entry := range table.Entries {
		node, _ := entry[nodeColumn].(map[string]any)
		if node == nil {
			continue
		}
		keys, _ := node["key"].(map[string]any)
		if len(keys) == 0 {
			return nil, selectionFailed("row %d: the %s node has no key", index+1, nodeColumn)
		}
		identity := make(map[string]any, len(keys))
		for property, raw := range keys {
			typed, _ := raw.(map[string]any)
			text, ok := interfaces.ScalarText(typed["value"])
			if !ok {
				return nil, selectionFailed("row %d: the key of the %s node is not readable", index+1, nodeColumn)
			}
			identity[property] = typedKeyValue(subject, property, text)
		}
		target := EventTarget{Identity: identity, EventHit: interfaces.EventHit{
			Key: interfaces.TargetIdentityKey(identity), Attributes: map[string]interfaces.EventAttribute{},
		}}
		for column, value := range entry {
			switch {
			case column == nodeColumn || value == nil:
			case column == eventNameColumn:
				target.Name = interfaces.AttributeText(value)
			default:
				target.Attributes[column] = textAttribute(value)
			}
		}
		selection.Targets = append(selection.Targets, target)
	}
	return selection, nil
}

// tableHits reads a table whose key column holds the primary key of the subject's objects. A row
// without a key is skipped; a key that is not a string or a number fails the selection.
func tableHits(table eventTable, subject *interfaces.ObjectType) (*Selection, error) {
	hasKey := false
	for _, column := range table.Columns {
		if column == eventKeyColumn {
			hasKey = true
		}
	}
	if !hasKey && len(table.Columns) == 0 && len(table.Entries) > 0 {
		_, hasKey = table.Entries[0][eventKeyColumn]
	}
	if !hasKey {
		return nil, selectionFailed("the result has no key column")
	}
	selection := &Selection{Limited: table.Limited}
	for index, entry := range table.Entries {
		raw := entry[eventKeyColumn]
		if raw == nil {
			continue
		}
		key, ok := interfaces.ScalarText(raw)
		if !ok {
			return nil, selectionFailed("the key of row %d is not a string or a number", index+1)
		}
		target := EventTarget{Identity: identityOfKey(subject, key), EventHit: interfaces.EventHit{
			Key: key, Attributes: map[string]interfaces.EventAttribute{},
		}}
		for column, value := range entry {
			switch {
			case column == eventKeyColumn || value == nil:
			case column == eventNameColumn:
				target.Name = interfaces.AttributeText(value)
			default:
				target.Attributes[column] = textAttribute(value)
			}
		}
		selection.Targets = append(selection.Targets, target)
	}
	return selection, nil
}

// byFunction calls the rule function as the executor, with the check's parameters, and reads the
// table it returns.
func (s *Selector) byFunction(ctx context.Context, function *interfaces.ActionFunctionTargets,
	subject *interfaces.ObjectType, clock CheckClock) (*Selection, error) {
	if s.Tools == nil {
		return nil, selectionFailed("functions are not available")
	}
	arguments, err := clock.parameters(function.Parameters)
	if err != nil {
		return nil, &SelectionError{Reason: "targets.function: " + err.Error()}
	}
	callCtx, cancel := context.WithTimeout(ctx, time.Duration(eventFunctionTimeoutSeconds+5)*time.Second)
	defer cancel()
	result, err := s.Tools.ExecuteTool(callCtx, function.BoxID, function.ToolID, interfaces.ToolExecutionRequest{
		Body: arguments, Timeout: eventFunctionTimeoutSeconds,
	})
	if err != nil {
		return nil, selectionFailed("the function %s failed: %s", function.ToolID, DependencyReason(err))
	}
	table, err := tableOf(result)
	if err != nil {
		return nil, selectionFailed("the function %s did not return a table", function.ToolID)
	}
	selection, err := tableHits(table, subject)
	if err != nil {
		return nil, selectionFailed("the function %s must return a table with a key column: the primary key of %s",
			function.ToolID, subject.OTID)
	}
	return selection, nil
}

// tableOf reads a table function's result: {columns, entries, limited}, each column a name or
// an object with one.
func tableOf(result any) (eventTable, error) {
	encoded, err := json.Marshal(result)
	if err != nil {
		return eventTable{}, err
	}
	var reply struct {
		Columns []json.RawMessage `json:"columns"`
		Entries []map[string]any  `json:"entries"`
		Limited bool              `json:"limited"`
	}
	if err := jsoncodec.Unmarshal(encoded, &reply); err != nil {
		return eventTable{}, err
	}
	if reply.Entries == nil && reply.Columns == nil {
		return eventTable{}, fmt.Errorf("not a table")
	}
	table := eventTable{Entries: reply.Entries, Limited: reply.Limited}
	for _, raw := range reply.Columns {
		var column struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(raw, &column) != nil {
			var name string
			_ = json.Unmarshal(raw, &name)
			column.Name = name
		}
		table.Columns = append(table.Columns, strings.TrimSpace(column.Name))
	}
	return table, nil
}
