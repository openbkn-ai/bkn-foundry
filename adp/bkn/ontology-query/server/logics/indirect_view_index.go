// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package logics

import (
	"fmt"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
)

// IndirectViewIndex holds the far-side keys of the backing rows that connect
// one current object, so relating a next object is a lookup rather than a scan
// of every row. It accepts exactly the pairs
// CheckIndirectMappingConditionsWithViewData accepts.
type IndirectViewIndex map[string]struct{}

// IndexIndirectViewData indexes the rows of viewData that connect
// currentObjectData by the values of their far-side fields.
func IndexIndirectViewData(currentObjectData map[string]any, mappingRules *interfaces.InDirectMapping,
	isForward bool, viewData []map[string]any) IndirectViewIndex {
	near, far := indirectMappingSides(mappingRules, isForward)
	index := make(IndirectViewIndex, len(viewData))
	for _, viewRecord := range viewData {
		connected := true
		for _, mapping := range near {
			objectProperty, viewField := mappingEnds(mapping, isForward)
			currentValue, ok1 := currentObjectData[objectProperty]
			viewValue, ok2 := viewRecord[viewField]
			if !ok1 || !ok2 || !CompareValues(currentValue, viewValue) {
				connected = false
				break
			}
		}
		if !connected {
			continue
		}
		values := make([]any, len(far))
		complete := true
		for position, mapping := range far {
			viewField, _ := mappingEnds(mapping, isForward)
			value, ok := viewRecord[viewField]
			if !ok {
				complete = false
				break
			}
			values[position] = value
		}
		if complete {
			index[comparableKey(values)] = struct{}{}
		}
	}
	return index
}

// IndirectNextObjectKey returns the key IndexIndirectViewData would give a
// row reaching nextObject; ok is false when nextObject lacks a mapped
// property, so no row can reach it.
func IndirectNextObjectKey(nextObject map[string]any, mappingRules *interfaces.InDirectMapping,
	isForward bool) (string, bool) {
	_, far := indirectMappingSides(mappingRules, isForward)
	values := make([]any, len(far))
	for position, mapping := range far {
		_, objectProperty := mappingEnds(mapping, isForward)
		value, ok := nextObject[objectProperty]
		if !ok {
			return "", false
		}
		values[position] = value
	}
	return comparableKey(values), true
}

// Relates reports whether a backing row connects the indexed object to the
// next object with the given key.
func (index IndirectViewIndex) Relates(nextKey string, ok bool) bool {
	if !ok {
		return false
	}
	_, exists := index[nextKey]
	return exists
}

// indirectMappingSides returns the rules between the current object and the
// backing rows (near) and between the backing rows and the next object (far).
func indirectMappingSides(mappingRules *interfaces.InDirectMapping, isForward bool) (near, far []interfaces.Mapping) {
	if isForward {
		return mappingRules.SourceMappingRules, mappingRules.TargetMappingRules
	}
	return mappingRules.TargetMappingRules, mappingRules.SourceMappingRules
}

// mappingEnds returns the two property names a rule compares, in the order
// CheckIndirectMappingConditionsWithViewData reads them.
func mappingEnds(mapping interfaces.Mapping, isForward bool) (string, string) {
	if isForward {
		return mapping.SourceProp.Name, mapping.TargetProp.Name
	}
	return mapping.TargetProp.Name, mapping.SourceProp.Name
}

// comparableKey encodes values so that two keys are equal exactly when
// CompareValues holds for every position: nil equals only nil, and other
// values compare by their default formatting.
func comparableKey(values []any) string {
	var key strings.Builder
	for _, value := range values {
		if value == nil {
			key.WriteString("N;")
			continue
		}
		text := fmt.Sprintf("%v", value)
		fmt.Fprintf(&key, "V%d:%s;", len(text), text)
	}
	return key.String()
}
