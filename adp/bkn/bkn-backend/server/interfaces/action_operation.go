// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	ActionOperationObject     = "object"
	ActionOperationRelation   = "relation"
	ActionOperationTool       = "tool"
	ActionRelationCreate      = "create"
	ActionRelationDelete      = "delete"
	ActionRoleSource          = "source"
	ActionRoleTarget          = "target"
	ActionValueSourceIdentity = "source_identity"
	ActionValueTargetIdentity = "target_identity"
)

// ActionOperationConfig defines the operation and explicit target semantics.
// Stored actions must be migrated to this model before the new runtime is enabled.
type ActionOperationConfig struct {
	Type                 string         `json:"type" mapstructure:"type"`
	RelationTypeID       string         `json:"relation_type_id,omitempty" mapstructure:"relation_type_id"`
	RelationOperation    string         `json:"relation_operation,omitempty" mapstructure:"relation_operation"`
	AnchorRole           string         `json:"anchor_role,omitempty" mapstructure:"anchor_role"`
	OtherObjectCondition *ActionCondCfg `json:"other_object_condition,omitempty" mapstructure:"other_object_condition"`
}

// ValidateIdentitySchedule keeps the legacy schedule transport within the
// operations it can represent. Other operations need explicit target fields
// before they can be scheduled.
func ValidateIdentitySchedule(action *ActionType, identities []map[string]any) error {
	if action == nil || action.OperationConfig == nil {
		return fmt.Errorf("scheduled action requires a migrated operation_config")
	}
	if action.OperationConfig.Type != ActionOperationObject ||
		(action.ActionType != ACTION_TYPE_MODIFY && action.ActionType != ACTION_TYPE_DELETE) {
		return fmt.Errorf("scheduled execution currently supports only modify or delete object operations")
	}
	if len(identities) == 0 {
		return fmt.Errorf("scheduled object execution requires _instance_identities")
	}
	for index, identity := range identities {
		if len(identity) == 0 {
			return fmt.Errorf("scheduled target %d has an empty identity", index+1)
		}
	}
	return nil
}

// UnmarshalJSON rejects obsolete endpoint-mapping fields instead of silently
// accepting a definition whose execution semantics would be lost.
func (config *ActionOperationConfig) UnmarshalJSON(data []byte) error {
	type definition ActionOperationConfig
	var value definition
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	*config = ActionOperationConfig(value)
	return nil
}

// ValidateActionOperation checks structure independently of dependency lookup.
// Every saved definition uses this model; omitted configs are never inferred.
func ValidateActionOperation(at *ActionType) error {
	cfg := at.OperationConfig
	if cfg == nil {
		return fmt.Errorf("operation_config is required; migrate existing action definitions before use")
	}
	switch cfg.Type {
	case ActionOperationObject, ActionOperationRelation:
		if at.ObjectTypeID == "" {
			return fmt.Errorf("operation_config.type=%s requires object_type_id", cfg.Type)
		}
		if cfg.Type == ActionOperationObject && at.ActionType == ACTION_TYPE_ADD && at.Condition != nil {
			return fmt.Errorf("object creation cannot have an applicability condition")
		}
	case ActionOperationTool:
		if at.ObjectTypeID != "" || at.Condition != nil {
			return fmt.Errorf("tool operations cannot bind an object type or condition")
		}
	default:
		return fmt.Errorf("unsupported operation_config.type %q", cfg.Type)
	}
	if cfg.Type == ActionOperationRelation {
		if cfg.RelationTypeID == "" {
			return fmt.Errorf("relation_type_id is required for relation operations")
		}
		if cfg.RelationOperation != ActionRelationCreate && cfg.RelationOperation != ActionRelationDelete {
			return fmt.Errorf("relation_operation must be create or delete")
		}
		if cfg.AnchorRole != "" && cfg.AnchorRole != ActionRoleSource && cfg.AnchorRole != ActionRoleTarget {
			return fmt.Errorf("anchor_role must be source or target")
		}
	} else if cfg.RelationTypeID != "" || cfg.RelationOperation != "" || cfg.AnchorRole != "" || cfg.OtherObjectCondition != nil {
		return fmt.Errorf("relation fields are only valid for relation operations")
	}
	inputs := make(map[string]bool)
	identities := make(map[string]bool)
	names := make(map[string]bool)
	for _, p := range at.Parameters {
		if p.Name == "" || names[p.Name] {
			return fmt.Errorf("action parameter names must be nonempty and unique: %q", p.Name)
		}
		for name := range names {
			if strings.HasPrefix(name, p.Name+".") || strings.HasPrefix(p.Name, name+".") {
				return fmt.Errorf("action parameter paths overlap")
			}
		}
		for _, segment := range strings.Split(p.Name, ".") {
			if segment == "" || segment == "__proto__" || segment == "constructor" || segment == "prototype" {
				return fmt.Errorf("invalid action parameter path")
			}
		}
		names[p.Name] = true
		switch p.ValueFrom {
		case "const", "input":
			inputs[p.Name] = p.ValueFrom == "input"
		case "property":
			if at.ObjectTypeID == "" {
				return fmt.Errorf("property parameter %q requires an object type", p.Name)
			}
			if value, ok := p.Value.(string); !ok || value == "" {
				return fmt.Errorf("property parameter %q requires a property name", p.Name)
			}
		case ActionValueSourceIdentity, ActionValueTargetIdentity:
			if cfg.Type != ActionOperationRelation {
				return fmt.Errorf("identity parameter %q requires a relation operation", p.Name)
			}
			if p.Value != nil {
				return fmt.Errorf("identity parameter %q derives its value from the object primary key", p.Name)
			}
			identities[p.ValueFrom] = true
		default:
			return fmt.Errorf("unsupported value_from %q for parameter %q", p.ValueFrom, p.Name)
		}
	}
	if cfg.Type == ActionOperationRelation && (!identities[ActionValueSourceIdentity] || !identities[ActionValueTargetIdentity]) {
		return fmt.Errorf("relation parameters must bind both source_identity and target_identity")
	}
	if err := ValidateActionConditionReferences(at.Condition, at.ObjectTypeID, inputs); err != nil {
		return err
	}
	return ValidateActionConditionReferences(cfg.OtherObjectCondition, "", inputs)
}

// ValidateActionConditionReferences validates references without treating their names as literals.
func ValidateActionConditionReferences(cfg *ActionCondCfg, objectTypeID string, inputs map[string]bool) error {
	if cfg == nil {
		return nil
	}
	if objectTypeID != "" && cfg.ObjectTypeID != "" && cfg.ObjectTypeID != objectTypeID {
		return fmt.Errorf("condition references an unrelated object type %q", cfg.ObjectTypeID)
	}
	if len(cfg.SubConds) > 0 {
		for _, sub := range cfg.SubConds {
			if sub == nil {
				return fmt.Errorf("condition contains a null subcondition")
			}
			if err := ValidateActionConditionReferences(sub, objectTypeID, inputs); err != nil {
				return err
			}
		}
		return nil
	}
	switch cfg.ValueFrom {
	case "", "const":
		return nil
	case "property", "input":
		name, ok := cfg.Value.(string)
		if !ok || name == "" {
			return fmt.Errorf("condition %s reference must be a nonempty name", cfg.ValueFrom)
		}
		if cfg.ValueFrom == "input" && !inputs[name] {
			return fmt.Errorf("condition references unknown dynamic parameter %q", name)
		}
	default:
		return fmt.Errorf("unsupported condition value_from %q", cfg.ValueFrom)
	}
	return nil
}
