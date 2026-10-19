// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package cypher

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// evaluateGraph assembles a graph value for one scope. A node or relationship
// an OPTIONAL MATCH found nothing for is NULL; collect gathers its argument
// over the scope's group, skipping NULL and keeping duplicates.
func evaluateGraph(value PlanGraph, s scope) (any, error) {
	switch node := value.(type) {
	case PlanNodeValue:
		row := scopeRow(s, node.Table)
		if row == nil {
			return nil, nil
		}
		object, err := objectValue(node.Identity, row)
		if err != nil {
			return nil, err
		}
		if len(node.Snapshot) > 0 {
			object.Properties = make(map[string]any, len(node.Snapshot))
			for _, property := range node.Snapshot {
				object.Properties[property.Property] = row[property.Column]
			}
		}
		return object, nil

	case PlanRelationValue:
		sourceRow, targetRow := scopeRow(s, node.Source.Table), scopeRow(s, node.Target.Table)
		if sourceRow == nil || targetRow == nil {
			return nil, nil
		}
		source, err := objectValue(node.Source, sourceRow)
		if err != nil {
			return nil, err
		}
		target, err := objectValue(node.Target, targetRow)
		if err != nil {
			return nil, err
		}
		relation := interfaces.CypherRelationValue{
			Kind: interfaces.CYPHER_KIND_RELATION, RelationTypeID: node.RelationTypeID,
			Source: source, Target: target, BindingDigest: node.Digest,
			Basis: make([]interfaces.CypherRelationBasis, 0, len(node.Basis)),
		}
		for _, key := range node.Basis {
			relation.Basis = append(relation.Basis, interfaces.CypherRelationBasis{
				SourceProperty: key.SourceProperty, SourceValue: sourceRow[key.SourceColumn],
				TargetProperty: key.TargetProperty, TargetValue: targetRow[key.TargetColumn],
			})
		}
		return relation, nil

	case PlanGraphConstant:
		return literalValue(node.Value), nil

	case PlanGraphProperty:
		return s.column(node.Table, node.Column), nil

	case PlanGraphMap:
		fields := make(map[string]any, len(node.Keys))
		for i, key := range node.Keys {
			field, err := evaluateGraph(node.Values[i], s)
			if err != nil {
				return nil, err
			}
			fields[key] = field
		}
		return fields, nil

	case PlanGraphCollect:
		collected := make([]any, 0, len(s.group))
		for _, row := range s.group {
			item, err := evaluateGraph(node.Argument, scope{row: row})
			if err != nil {
				return nil, err
			}
			if item != nil {
				collected = append(collected, item)
			}
		}
		return collected, nil

	default:
		return nil, fmt.Errorf("cannot evaluate a %T graph value", value)
	}
}

func scopeRow(s scope, table int) map[string]any {
	if table < 0 || table >= len(s.row) {
		return nil
	}
	return s.row[table]
}

// objectValue identifies a row: its object type and its complete key, each
// key value as exact text with its declared type. A row without a key value
// has no identity, which is an error rather than a node to guess at.
func objectValue(identity PlanIdentity, row map[string]any) (interfaces.CypherObjectValue, error) {
	object := interfaces.CypherObjectValue{
		Kind: interfaces.CYPHER_KIND_OBJECT, NetworkID: identity.NetworkID, Branch: identity.Branch,
		ObjectTypeID: identity.ObjectTypeID, Key: make(map[string]interfaces.CypherKeyValue, len(identity.Keys)),
	}
	for _, key := range identity.Keys {
		value := row[key.Column]
		if value == nil {
			return object, &missingKeyError{objectType: identity.ObjectTypeID, property: key.Property}
		}
		kind := key.Type
		if kind == "" {
			kind = valueType(value)
		}
		object.Key[key.Property] = interfaces.CypherKeyValue{Type: kind, Value: keyText(value)}
	}
	return object, nil
}

// missingKeyError is a matched row without a value for one of its object
// type's key properties: the data does not meet the model, so the row has no
// identity to return.
type missingKeyError struct {
	objectType, property string
}

func (e *missingKeyError) Error() string {
	return fmt.Sprintf("a %q row has no value for its key property %q, so it cannot be returned as a node; "+
		"return its properties instead", e.objectType, e.property)
}

// keyText writes a key value exactly: a number as its decimal digits, never
// through a float.
func keyText(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	case int64:
		return strconv.FormatInt(v, 10)
	case int:
		return strconv.Itoa(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	default:
		return fmt.Sprint(v)
	}
}

// graphKey encodes a graph value for grouping. A node is its object type and
// typed key, never its snapshot properties, so a group is one object however
// its properties read; a relationship is its type and its two ends.
func graphKey(value any) (string, bool) {
	switch v := value.(type) {
	case interfaces.CypherObjectValue:
		return "o" + objectKey(v), true
	case interfaces.CypherRelationValue:
		return "r" + v.RelationTypeID + "\x00" + v.BindingDigest + "\x00" + objectKey(v.Source) + "\x00" + objectKey(v.Target), true
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		var b strings.Builder
		b.WriteString("m")
		for _, key := range keys {
			b.WriteString(strconv.Quote(key) + "=" + strconv.Quote(canonicalKey(v[key])) + ";")
		}
		return b.String(), true
	case []any:
		var b strings.Builder
		b.WriteString("l")
		for _, item := range v {
			b.WriteString(strconv.Quote(canonicalKey(item)) + ";")
		}
		return b.String(), true
	default:
		return "", false
	}
}

func objectKey(object interfaces.CypherObjectValue) string {
	properties := make([]string, 0, len(object.Key))
	for property := range object.Key {
		properties = append(properties, property)
	}
	sort.Strings(properties)
	var b strings.Builder
	b.WriteString(object.NetworkID + "\x00" + object.Branch + "\x00" + object.ObjectTypeID)
	for _, property := range properties {
		key := object.Key[property]
		b.WriteString("\x00" + property + "\x01" + key.Type + "\x01" + key.Value)
	}
	return b.String()
}

// walkGraphColumns calls visit for every column assembling a graph value
// reads.
func walkGraphColumns(value PlanGraph, visit func(table int, column string)) {
	identity := func(id PlanIdentity) {
		for _, key := range id.Keys {
			visit(id.Table, key.Column)
		}
	}
	switch node := value.(type) {
	case PlanNodeValue:
		identity(node.Identity)
		for _, property := range node.Snapshot {
			visit(node.Table, property.Column)
		}
	case PlanRelationValue:
		identity(node.Source)
		identity(node.Target)
		for _, key := range node.Basis {
			visit(node.Source.Table, key.SourceColumn)
			visit(node.Target.Table, key.TargetColumn)
		}
	case PlanGraphProperty:
		visit(node.Table, node.Column)
	case PlanGraphMap:
		for _, field := range node.Values {
			walkGraphColumns(field, visit)
		}
	case PlanGraphCollect:
		walkGraphColumns(node.Argument, visit)
	}
}
