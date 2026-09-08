// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package property_access

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	cond "ontology-query/common/condition"
	"ontology-query/common/maskrule"
	oerrors "ontology-query/errors"
	"ontology-query/interfaces"
)

// Plan is the request-scoped property decision snapshot shared by object,
// relation, metric, and logical-property query exits.
type Plan struct {
	decisions map[string]map[string]interfaces.PropertyAccessDecision
}

// Build resolves one immutable permission snapshot for all supplied object
// types. Invalid masked rules are downgraded to schema so every consumer uses
// the same effective decision as the object projection path.
func Build(ctx context.Context, resolver interfaces.PropertyAccessService,
	objectTypes []interfaces.ObjectType) (*Plan, error) {
	plan := &Plan{decisions: make(map[string]map[string]interfaces.PropertyAccessDecision, len(objectTypes))}
	items := make([]interfaces.PropertyLevelsRequestItem, 0, len(objectTypes))
	propertiesByRef := make(map[string]map[string]cond.DataProperty, len(objectTypes))
	for _, objectType := range objectTypes {
		ref := ObjectTypeRef(objectType.KNID, objectType.OTID)
		if _, duplicate := propertiesByRef[ref]; duplicate {
			continue
		}
		properties := make(map[string]cond.DataProperty, len(objectType.DataProperties))
		names := make([]string, 0, len(objectType.DataProperties))
		for _, property := range objectType.DataProperties {
			properties[property.Name] = property
			names = append(names, property.Name)
		}
		propertiesByRef[ref] = properties
		plan.decisions[ref] = make(map[string]interfaces.PropertyAccessDecision, len(names))
		if len(names) > 0 {
			items = append(items, interfaces.PropertyLevelsRequestItem{ObjectTypeRef: ref, Properties: names})
		}
	}
	if len(items) == 0 {
		return plan, nil
	}
	if resolver == nil {
		return nil, decisionUnavailable(ctx, fmt.Errorf("property access resolver is not configured"))
	}
	entries, err := resolver.ResolvePropertyLevels(ctx, items)
	if err != nil {
		return nil, err
	}
	if len(entries) != len(items) {
		return nil, decisionUnavailable(ctx, fmt.Errorf("property-level response shape mismatch"))
	}
	for index, item := range items {
		entry := entries[index]
		if entry.ObjectTypeRef != item.ObjectTypeRef || len(entry.Properties) != len(item.Properties) {
			return nil, decisionUnavailable(ctx, fmt.Errorf("property-level response shape mismatch"))
		}
		for propertyIndex, name := range item.Properties {
			decision := entry.Properties[propertyIndex]
			if decision.Name != name || !decision.Level.Valid() {
				return nil, decisionUnavailable(ctx, fmt.Errorf("property-level response decision mismatch"))
			}
			if decision.Level == interfaces.PropertyAccessMasked {
				property := propertiesByRef[item.ObjectTypeRef][name]
				if err := maskrule.Validate(property.Type, property.MaskRule); err != nil {
					logger.Warnf("Data property [%s/%s] has unusable mask_rule and is downgraded to schema: %v",
						item.ObjectTypeRef, name, err)
					decision.Level = interfaces.PropertyAccessSchema
				}
			}
			plan.decisions[item.ObjectTypeRef][name] = decision
		}
	}
	return plan, nil
}

func ObjectTypeRef(knID, objectTypeID string) string {
	return strings.TrimSpace(knID) + "/" + strings.TrimSpace(objectTypeID)
}

func (p *Plan) Decision(objectTypeRef, property string) (interfaces.PropertyAccessDecision, bool) {
	if p == nil {
		return interfaces.PropertyAccessDecision{}, false
	}
	decision, exists := p.decisions[objectTypeRef][property]
	return decision, exists
}

// AllFull reports whether every dependency is a known full property.
func (p *Plan) AllFull(objectTypeRef string, properties []string) bool {
	for _, property := range uniqueNonempty(properties) {
		decision, exists := p.Decision(objectTypeRef, property)
		if !exists || decision.Level != interfaces.PropertyAccessFull {
			return false
		}
	}
	return true
}

// CollectConditionFields returns every data property read by a condition.
// A wildcard or an unscoped multi_match depends on all data properties.
func CollectConditionFields(condition *cond.CondCfg, propertyNames []string) []string {
	result := map[string]struct{}{}
	collectConditionFields(condition, propertyNames, result)
	fields := make([]string, 0, len(result))
	for name := range result {
		fields = append(fields, name)
	}
	return fields
}

func collectConditionFields(condition *cond.CondCfg, propertyNames []string, result map[string]struct{}) {
	if condition == nil {
		return
	}
	if condition.Operation == cond.OperationMultiMatch {
		fields, exists := condition.RemainCfg["fields"]
		if !exists {
			addAll(result, propertyNames)
		} else {
			for _, name := range stringValues(fields) {
				if name == "*" {
					addAll(result, propertyNames)
				} else if strings.TrimSpace(name) != "" {
					result[name] = struct{}{}
				}
			}
		}
	} else if condition.Name == "*" {
		addAll(result, propertyNames)
	} else if strings.TrimSpace(condition.Name) != "" {
		result[condition.Name] = struct{}{}
	}
	for _, child := range condition.SubConds {
		collectConditionFields(child, propertyNames, result)
	}
}

func addAll(result map[string]struct{}, values []string) {
	for _, value := range values {
		result[value] = struct{}{}
	}
}

func stringValues(value any) []string {
	switch values := value.(type) {
	case []string:
		return values
	case []any:
		result := make([]string, 0, len(values))
		for _, value := range values {
			if text, ok := value.(string); ok {
				result = append(result, text)
			}
		}
		return result
	case string:
		return []string{values}
	default:
		return nil
	}
}

func uniqueNonempty(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func decisionUnavailable(ctx context.Context, err error) error {
	return rest.NewHTTPError(ctx, http.StatusServiceUnavailable,
		oerrors.OntologyQuery_InternalError_CheckPermissionFailed).WithErrorDetails(err.Error())
}
