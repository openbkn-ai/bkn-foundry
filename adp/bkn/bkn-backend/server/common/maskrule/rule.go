// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package maskrule defines and validates the DataProperty masking contract.
package maskrule

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"unicode"
	"unicode/utf8"
)

const (
	KindFixed           = "fixed"
	KindPartial         = "partial"
	KindEmail           = "email"
	KindRound           = "round"
	KindDateGranularity = "date_granularity"

	maxKeepCodePoints        = 64
	maxReplacementCodePoints = 8
)

// Rule is the strongly typed wire and persistence model for a DataProperty mask_rule.
// Fields that do not belong to the selected kind are rejected by Validate.
type Rule struct {
	Kind string `json:"kind" mapstructure:"kind"`

	Replacement    string `json:"replacement,omitempty" mapstructure:"replacement,omitempty"`
	KeepStart      *int   `json:"keep_start,omitempty" mapstructure:"keep_start,omitempty"`
	KeepEnd        *int   `json:"keep_end,omitempty" mapstructure:"keep_end,omitempty"`
	LocalKeepStart *int   `json:"local_keep_start,omitempty" mapstructure:"local_keep_start,omitempty"`
	PreserveDomain *bool  `json:"preserve_domain,omitempty" mapstructure:"preserve_domain,omitempty"`

	Step        *float64 `json:"step,omitempty" mapstructure:"step,omitempty"`
	Granularity string   `json:"granularity,omitempty" mapstructure:"granularity,omitempty"`
}

// UnmarshalJSON keeps the REST, persistence, and .bkn representations strict:
// misspelled or future fields must not be silently dropped during a round trip.
func (rule *Rule) UnmarshalJSON(data []byte) error {
	type ruleAlias Rule
	var decoded ruleAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	allowed := map[string]bool{
		"kind":             true,
		"replacement":      true,
		"keep_start":       true,
		"keep_end":         true,
		"local_keep_start": true,
		"preserve_domain":  true,
		"step":             true,
		"granularity":      true,
	}
	for field := range fields {
		if !allowed[field] {
			return fmt.Errorf("unknown mask rule field %q", field)
		}
	}
	allowedForKind := map[string]map[string]bool{
		KindFixed:           {"kind": true, "replacement": true},
		KindPartial:         {"kind": true, "keep_start": true, "keep_end": true, "replacement": true},
		KindEmail:           {"kind": true, "local_keep_start": true, "preserve_domain": true, "replacement": true},
		KindRound:           {"kind": true, "step": true},
		KindDateGranularity: {"kind": true, "granularity": true},
	}
	if kindFields, knownKind := allowedForKind[decoded.Kind]; knownKind {
		for field := range fields {
			if !kindFields[field] {
				return fmt.Errorf("mask rule field %q is not valid for %s", field, decoded.Kind)
			}
		}
	}

	*rule = Rule(decoded)
	return nil
}

var stringPropertyTypes = map[string]bool{
	"string":  true,
	"text":    true,
	"keyword": true,
}

var numberPropertyTypes = map[string]bool{
	"integer":          true,
	"unsigned integer": true,
	"float":            true,
	"decimal":          true,
}

var dateGranularities = map[string]map[string]bool{
	"date": {
		"year":  true,
		"month": true,
	},
	"time": {
		"hour": true,
	},
	"datetime": {
		"year":  true,
		"month": true,
		"day":   true,
		"hour":  true,
	},
	"timestamp": {
		"year":  true,
		"month": true,
		"day":   true,
		"hour":  true,
	},
}

// Validate checks the rule kind, property-type compatibility, required parameters,
// parameter bounds, and kind-specific parameter shape. A nil rule is valid.
func Validate(propertyType string, rule *Rule) error {
	if rule == nil {
		return nil
	}
	if propertyType == "" {
		return errors.New("property type is required when mask_rule is set")
	}

	switch rule.Kind {
	case KindFixed:
		if !stringPropertyTypes[propertyType] {
			return incompatibleType(rule.Kind, propertyType)
		}
		if err := validateReplacement(rule.Replacement); err != nil {
			return err
		}
		return rejectUnexpected(rule, true, false, false, false, false, false, false)
	case KindPartial:
		if !stringPropertyTypes[propertyType] {
			return incompatibleType(rule.Kind, propertyType)
		}
		if err := validateReplacement(rule.Replacement); err != nil {
			return err
		}
		if err := validateBoundedInt("keep_start", rule.KeepStart); err != nil {
			return err
		}
		if err := validateBoundedInt("keep_end", rule.KeepEnd); err != nil {
			return err
		}
		return rejectUnexpected(rule, true, true, true, false, false, false, false)
	case KindEmail:
		if !stringPropertyTypes[propertyType] {
			return incompatibleType(rule.Kind, propertyType)
		}
		if err := validateReplacement(rule.Replacement); err != nil {
			return err
		}
		if err := validateBoundedInt("local_keep_start", rule.LocalKeepStart); err != nil {
			return err
		}
		if rule.PreserveDomain == nil {
			return fmt.Errorf("preserve_domain is required for %s", rule.Kind)
		}
		return rejectUnexpected(rule, true, false, false, true, true, false, false)
	case KindRound:
		if !numberPropertyTypes[propertyType] {
			return incompatibleType(rule.Kind, propertyType)
		}
		if err := validateStep(rule.Step); err != nil {
			return err
		}
		return rejectUnexpected(rule, false, false, false, false, false, true, false)
	case KindDateGranularity:
		allowed, ok := dateGranularities[propertyType]
		if !ok {
			return incompatibleType(rule.Kind, propertyType)
		}
		if !allowed[rule.Granularity] {
			return fmt.Errorf("granularity %q is not coarser than property type %q", rule.Granularity, propertyType)
		}
		return rejectUnexpected(rule, false, false, false, false, false, false, true)
	default:
		return fmt.Errorf("unsupported mask rule kind %q", rule.Kind)
	}
}

func incompatibleType(kind, propertyType string) error {
	return fmt.Errorf("mask rule kind %q does not support property type %q", kind, propertyType)
}

func validateReplacement(value string) error {
	count := utf8.RuneCountInString(value)
	if count < 1 || count > maxReplacementCodePoints {
		return fmt.Errorf("replacement must contain 1 to %d Unicode code points", maxReplacementCodePoints)
	}
	for _, r := range value {
		if !unicode.IsPrint(r) {
			return fmt.Errorf("replacement must contain only printable Unicode code points")
		}
	}
	return nil
}

func validateBoundedInt(name string, value *int) error {
	if value == nil {
		return fmt.Errorf("%s is required", name)
	}
	if *value < 0 || *value > maxKeepCodePoints {
		return fmt.Errorf("%s must be between 0 and %d", name, maxKeepCodePoints)
	}
	return nil
}

func validateStep(value *float64) error {
	if value == nil {
		return fmt.Errorf("step is required")
	}
	if *value <= 0 || math.IsInf(*value, 0) || math.IsNaN(*value) {
		return fmt.Errorf("step must be a finite decimal number greater than 0")
	}
	return nil
}

func rejectUnexpected(rule *Rule, replacement, keepStart, keepEnd, localKeepStart, preserveDomain, step, granularity bool) error {
	if !replacement && rule.Replacement != "" {
		return fmt.Errorf("replacement is not valid for %s", rule.Kind)
	}
	if !keepStart && rule.KeepStart != nil {
		return fmt.Errorf("keep_start is not valid for %s", rule.Kind)
	}
	if !keepEnd && rule.KeepEnd != nil {
		return fmt.Errorf("keep_end is not valid for %s", rule.Kind)
	}
	if !localKeepStart && rule.LocalKeepStart != nil {
		return fmt.Errorf("local_keep_start is not valid for %s", rule.Kind)
	}
	if !preserveDomain && rule.PreserveDomain != nil {
		return fmt.Errorf("preserve_domain is not valid for %s", rule.Kind)
	}
	if !step && rule.Step != nil {
		return fmt.Errorf("step is not valid for %s", rule.Kind)
	}
	if !granularity && rule.Granularity != "" {
		return fmt.Errorf("granularity is not valid for %s", rule.Kind)
	}
	return nil
}
