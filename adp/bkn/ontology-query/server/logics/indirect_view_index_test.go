// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package logics

import (
	"math/rand"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
)

func rule(source, target string) interfaces.Mapping {
	return interfaces.Mapping{SourceProp: interfaces.SimpleProperty{Name: source},
		TargetProp: interfaces.SimpleProperty{Name: target}}
}

// randomValue mixes the shapes CompareValues has to tell apart: integers and
// their string forms, nil, and a missing field (returned as absent).
func randomValue(random *rand.Rand) (any, bool) {
	switch random.Intn(6) {
	case 0:
		return nil, true
	case 1:
		return nil, false
	case 2:
		return "1", true
	case 3:
		return "<nil>", true
	default:
		return random.Intn(3), true
	}
}

func randomRecord(random *rand.Rand, fields ...string) map[string]any {
	record := map[string]any{}
	for _, field := range fields {
		if value, present := randomValue(random); present {
			record[field] = value
		}
	}
	return record
}

// The index must accept exactly the pairs the row-by-row check accepts, for
// single and composite keys in both directions.
func TestIndirectViewIndexMatchesTheRowByRowCheck(t *testing.T) {
	random := rand.New(rand.NewSource(2660))
	mappings := map[string]*interfaces.InDirectMapping{
		"single": {
			SourceMappingRules: []interfaces.Mapping{rule("source_key", "view_source")},
			TargetMappingRules: []interfaces.Mapping{rule("view_target", "target_key")},
		},
		"composite": {
			SourceMappingRules: []interfaces.Mapping{rule("source_key", "view_source"), rule("source_org", "view_source_org")},
			TargetMappingRules: []interfaces.Mapping{rule("view_target", "target_key"), rule("view_target_org", "target_org")},
		},
	}
	viewFields := []string{"view_source", "view_source_org", "view_target", "view_target_org"}
	for name, mappingRules := range mappings {
		for _, isForward := range []bool{true, false} {
			checked, related := 0, 0
			for round := 0; round < 200; round++ {
				currentFields := []string{"source_key", "source_org"}
				nextFields := []string{"target_key", "target_org"}
				if !isForward {
					currentFields, nextFields = nextFields, currentFields
				}
				current := randomRecord(random, currentFields...)
				viewData := make([]map[string]any, random.Intn(6))
				for index := range viewData {
					viewData[index] = randomRecord(random, viewFields...)
				}
				index := IndexIndirectViewData(current, mappingRules, isForward, viewData)
				for next := 0; next < 20; next++ {
					nextObject := randomRecord(random, nextFields...)
					want := CheckIndirectMappingConditionsWithViewData(current, nextObject, mappingRules, isForward, viewData)
					if got := index.Relates(IndirectNextObjectKey(nextObject, mappingRules, isForward)); got != want {
						t.Fatalf("%s forward=%t: index=%t check=%t current=%v next=%v rows=%v",
							name, isForward, got, want, current, nextObject, viewData)
					}
					checked++
					if want {
						related++
					}
				}
			}
			if related == 0 || related == checked {
				t.Fatalf("%s forward=%t: %d of %d pairs related, the cases do not discriminate", name, isForward, related, checked)
			}
		}
	}
}
