// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// A package declares the specification it is written in with bkn_spec in network.bkn. The
// declaration exists so the format can be fixed without changing how any existing package reads:
// a package without it is read by the 2.x rules forever, and only a package that opts in gets the
// corrected 3.0 rules. A version this parser does not know is refused rather than read by a guess.
const (
	// BknSpecLegacy is the version of a package that declares none.
	BknSpecLegacy = ""
	// BknSpecV3 is the 3.0 specification.
	BknSpecV3 = "3.0"
)

// SpecError is a package refused by its declared specification: an unsupported bkn_spec, or a
// definition that breaks a 3.0 rule. Its message names what to fix, so an importer can show it
// as-is instead of a generic load failure.
type SpecError struct {
	msg string

	rule     string
	property string
	params   map[string]any
}

func (e *SpecError) Error() string { return e.msg }

// Rule is the stable code of the declaration rule the error reports, one of the Rule constants; it is
// empty for an error that is not a declaration rule, such as a file that cannot be read.
func (e *SpecError) Rule() string { return e.rule }

// Property is the data property the rule concerns, empty for a rule of the definition itself.
func (e *SpecError) Property() string { return e.property }

// Params are the values the message is built from, such as the Semantic and the Type it needs: a
// caller words the refusal in its own language from them.
func (e *SpecError) Params() map[string]any { return e.params }

func specErrorf(format string, args ...any) error {
	return &SpecError{msg: fmt.Sprintf(format, args...)}
}

// ruleErrorf is specErrorf for a declaration rule: rule is its stable code, and params the values of
// the message. A params map written with withProperty names the data property too.
func ruleErrorf(rule string, params map[string]any, format string, args ...any) error {
	e := &SpecError{msg: fmt.Sprintf(format, args...), rule: rule}
	if len(params) > 0 {
		e.params = make(map[string]any, len(params))
		for key, value := range params {
			if key == propertyParam {
				e.property, _ = value.(string)
				continue
			}
			e.params[key] = value
		}
		if len(e.params) == 0 {
			e.params = nil
		}
	}
	return e
}

// propertyParam is the params key withProperty uses to carry the data property of a rule.
const propertyParam = "property"

// withProperty builds the params of a rule that concerns a data property: its name, then key and value
// pairs.
func withProperty(property string, keyValues ...any) map[string]any {
	params := map[string]any{propertyParam: property}
	for i := 0; i+1 < len(keyValues); i += 2 {
		params[keyValues[i].(string)] = keyValues[i+1]
	}
	return params
}

// inProperty names the data property of a rule error that does not name one yet.
func inProperty(err error, property string) error {
	if e, ok := err.(*SpecError); ok && e.rule != "" && e.property == "" {
		e.property = property
	}
	return err
}

var bknSpecPattern = regexp.MustCompile(`^(\d+)(?:\.(\d+))?(?:\.\d+)?$`)

// normalizeBknSpec turns a declared bkn_spec into the major.minor form the parser switches on.
//
// The value is read from parsed YAML, so 3.0 written bare arrives as the number 3 and is accepted
// as "3.0". A 2.x declaration names the rules an undeclared package already gets and normalizes to
// BknSpecLegacy.
func normalizeBknSpec(raw any) (string, error) {
	var text string
	switch v := raw.(type) {
	case nil:
		return BknSpecLegacy, nil
	case string:
		text = strings.TrimSpace(v)
	case int:
		text = strconv.Itoa(v)
	case float64:
		text = strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return "", specErrorf("bkn_spec must be a version such as \"3.0\", got %v", raw)
	}
	if text == "" {
		return BknSpecLegacy, nil
	}
	m := bknSpecPattern.FindStringSubmatch(text)
	if m == nil {
		return "", specErrorf("bkn_spec must be a version such as \"3.0\", got %q", text)
	}
	minor := m[2]
	if minor == "" {
		minor = "0"
	}
	switch m[1] + "." + minor {
	case "2.0":
		return BknSpecLegacy, nil
	case BknSpecV3:
		return BknSpecV3, nil
	}
	return "", specErrorf("unsupported bkn_spec %q: this parser reads 2.0 and 3.0", text)
}

// parseOptions carries what a definition file is parsed under. The zero value is the 2.x rules,
// which is what every exported Parse*File function uses.
type parseOptions struct {
	spec string
}

func (o parseOptions) v3() bool { return o.spec == BknSpecV3 }

// knownActionSections returns the action type sections that are parsed, and so kept out of the
// description. Under 3.0 the older Tool Configuration title is one of them; under 2.x it is still
// parsed as the action source but also copied into the description, as it always was.
func (o parseOptions) knownActionSections() map[string]bool {
	if !o.v3() {
		return knownActionTypeSections
	}
	known := make(map[string]bool, len(knownActionTypeSections)+1)
	for title := range knownActionTypeSections {
		known[title] = true
	}
	known["Tool Configuration"] = true
	return known
}

// knownRelationSections returns the relation type sections that are parsed. 3.0 adds Relation
// Constraints and the relation properties; under 2.x they are unknown sections and go into the
// description, as they always did.
func (o parseOptions) knownRelationSections() map[string]bool {
	if !o.v3() {
		return knownRelationTypeSections
	}
	known := make(map[string]bool, len(knownRelationTypeSections)+3)
	for title := range knownRelationTypeSections {
		known[title] = true
	}
	for _, title := range []string{"Relation Constraints", "Data Properties", "Property Constraints"} {
		known[title] = true
	}
	return known
}

// knownObjectSections returns the object type sections that are parsed. 3.0 adds Property
// Constraints; under 2.x it is an unknown section and goes into the description, as it always did.
func (o parseOptions) knownObjectSections() map[string]bool {
	if !o.v3() {
		return knownObjectTypeSections
	}
	known := make(map[string]bool, len(knownObjectTypeSections)+1)
	for title := range knownObjectTypeSections {
		known[title] = true
	}
	known["Property Constraints"] = true
	return known
}

var parameterValueSources = map[string]bool{"property": true, "input": true, "const": true}

// isRelationEndIdentity tells the value sources a relation action takes its two ends' identities
// from (§8.9); only action parameters may use them.
func isRelationEndIdentity(valueFrom string) bool {
	return valueFrom == "source_identity" || valueFrom == "target_identity"
}

// checkParameterValueSource is the 3.0 rule for a parameter row. Execution switches on ValueFrom
// and silently drops a parameter whose ValueFrom is anything but property, input or const, so a
// 3.0 package refuses such a row at import instead. The common mistake — the value source written
// in Source — gets its own message.
func checkParameterValueSource(owner string, p Parameter) error {
	if owner == "action" && isRelationEndIdentity(p.ValueFrom) {
		return nil
	}
	if parameterValueSources[strings.ToLower(strings.TrimSpace(p.ValueFrom))] {
		return nil
	}
	if parameterValueSources[strings.ToLower(strings.TrimSpace(p.Source))] {
		return specErrorf("%s parameter %q: the value source %q belongs in ValueFrom, not Source; Source is where the tool takes the parameter, such as Query or Body",
			owner, p.Name, p.Source)
	}
	return specErrorf("%s parameter %q: ValueFrom must be property, input or const, got %q",
		owner, p.Name, p.ValueFrom)
}

// legacyBindingParameter rewrites a row of the legacy parameter table
// (Parameter | Type | Source | Binding | Description) to the current meaning.
//
// That table put the value source — property, input or const — in Source and the property name or
// constant in Binding. The 2.x rules map Binding onto ValueFrom, which leaves the property name
// where the value source belongs, and execution then drops the parameter. 3.0 refuses the layout and
// shows this rewrite as the fix; the network check suggests the same rewrite for parameters already
// stored. A row is rewritten only when the table has a Binding column and no ValueFrom column and
// its Source holds one of the three value sources.
func legacyBindingParameter(row map[string]string, name string) (Parameter, bool) {
	if _, ok := row["Binding"]; !ok {
		return Parameter{}, false
	}
	for _, column := range []string{"ValueFrom", "Value From"} {
		if _, ok := row[column]; ok {
			return Parameter{}, false
		}
	}
	valueFrom := strings.ToLower(strings.TrimSpace(row["Source"]))
	if !parameterValueSources[valueFrom] {
		return Parameter{}, false
	}
	value := strings.TrimSpace(row["Binding"])
	if valueFrom == "input" || value == "-" {
		value = ""
	}
	return Parameter{
		Name:        name,
		Type:        row["Type"],
		Operation:   row["Operation"],
		ValueFrom:   valueFrom,
		Value:       value,
		Description: row["Description"],
	}, true
}
