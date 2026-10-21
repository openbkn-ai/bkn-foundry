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
	"time"
	"unicode/utf8"
	// The timezone constraint names an IANA zone; embedding the database keeps the check the same on
	// a host or image that ships without zoneinfo.
	_ "time/tzdata"

	"gopkg.in/yaml.v3"
)

// Property declarations (SPECIFICATION.md §6.8) say what a property's data should be: its meaning,
// whether it is always present, and the constraints on its values. They are for understanding and
// display; nothing checks the source data against them. A declaration that is itself wrong is
// refused when it is imported or saved.

// Semantic values.
const (
	SemanticEventTime = "event_time"
	SemanticEndTime   = "end_time"
	SemanticValidFrom = "valid_from"
	SemanticValidTo   = "valid_to"
	SemanticLocation  = "location"
	SemanticLatitude  = "latitude"
	SemanticLongitude = "longitude"
	SemanticStatus    = "status"
	SemanticSeverity  = "severity"
)

var (
	dateTypes    = []string{"date", "datetime", "timestamp"}
	numericTypes = []string{"integer", "unsigned integer", "float", "decimal"}
	stringTypes  = []string{"string", "keyword", "text"}
)

// semanticTypes lists the property types each Semantic value allows.
var semanticTypes = map[string][]string{
	SemanticEventTime: dateTypes,
	SemanticEndTime:   dateTypes,
	SemanticValidFrom: dateTypes,
	SemanticValidTo:   dateTypes,
	SemanticLocation:  {"point", "shape"},
	SemanticLatitude:  {"float", "decimal"},
	SemanticLongitude: {"float", "decimal"},
	SemanticStatus:    {"string", "keyword", "integer"},
	SemanticSeverity:  {"string", "keyword", "integer"},
}

var semanticOrder = []string{
	SemanticEventTime, SemanticEndTime, SemanticValidFrom, SemanticValidTo,
	SemanticLocation, SemanticLatitude, SemanticLongitude, SemanticStatus, SemanticSeverity,
}

// Coordinate reference systems a crs constraint may name.
var validCRS = []string{"wgs84", "gcj02", "bd09"}

// constraintKeys is the order the keys of one property are written in.
var constraintKeys = []string{"value_type", "range", "length", "pattern", "unit", "unique", "crs", "timezone"}

// notUniqueTypes cannot carry a unique constraint.
var notUniqueTypes = []string{"json", "binary", "vector", "point", "shape"}

var isoDateRE = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// parseRequired reads a Required cell. explicitFalse reports a written false, which the caller
// refuses on a primary key: a primary key is required by definition.
func parseRequired(name, cell string) (required, explicitFalse bool, err error) {
	switch strings.TrimSpace(cell) {
	case "":
		return false, false, nil
	case "true":
		return true, false, nil
	case "false":
		return false, true, nil
	}
	return false, false, specErrorf("data property %q: Required must be true, false or empty, got %q", name, cell)
}

// parsePropertyConstraintsV3 reads the yaml block of ### Property Constraints and attaches each
// entry to its property.
func parsePropertyConstraintsV3(sectionText string, props []*DataProperty) error {
	content := scanContent(sectionText)
	if len(content.blocks) == 0 {
		return nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(content.blocks[0].body), &doc); err != nil {
		return specErrorf("section \"### Property Constraints\" yaml is invalid: %v", err)
	}
	if len(doc.Content) == 0 {
		return nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return specErrorf("section \"### Property Constraints\" must map data property names to their constraints")
	}
	byName := make(map[string]*DataProperty, len(props))
	for _, p := range props {
		byName[p.Name] = p
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		name := root.Content[i].Value
		prop, ok := byName[name]
		if !ok {
			return specErrorf("property constraints: %q is not a data property of this definition", name)
		}
		constraints, err := parseConstraintEntry(name, root.Content[i+1])
		if err != nil {
			return err
		}
		prop.Constraints = constraints
	}
	return nil
}

func parseConstraintEntry(name string, node *yaml.Node) (*PropertyConstraints, error) {
	if node.Kind != yaml.MappingNode {
		return nil, specErrorf("property constraints %q must be a mapping of constraint keys", name)
	}
	c := &PropertyConstraints{}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key, value := node.Content[i].Value, node.Content[i+1]
		where := fmt.Sprintf("property constraints %q %s", name, key)
		switch key {
		case "value_type":
			if value.Kind != yaml.ScalarNode || value.Tag != "!!str" || value.Value == "" {
				return nil, specErrorf("%s must be the id of a value type", where)
			}
			c.ValueType = value.Value
		case "range":
			r, err := parseRangeNode(where, value)
			if err != nil {
				return nil, err
			}
			c.Range = r
		case "length":
			l, err := parseLengthNode(where, value)
			if err != nil {
				return nil, err
			}
			c.Length = l
		case "pattern", "unit", "crs", "timezone":
			if value.Kind != yaml.ScalarNode || value.Tag != "!!str" || value.Value == "" {
				return nil, specErrorf("%s must be a non-empty string", where)
			}
			switch key {
			case "pattern":
				c.Pattern = value.Value
			case "unit":
				c.Unit = value.Value
			case "crs":
				c.CRS = value.Value
			case "timezone":
				c.Timezone = value.Value
			}
		case "unique":
			if value.Kind != yaml.ScalarNode || value.Tag != "!!bool" {
				return nil, specErrorf("%s must be true or false", where)
			}
			c.Unique = value.Value == "true"
		default:
			return nil, specErrorf("%s is not a constraint; the keys are %s", where, strings.Join(constraintKeys, ", "))
		}
	}
	return c, nil
}

func parseRangeNode(where string, node *yaml.Node) (*ValueRange, error) {
	if node.Kind != yaml.MappingNode || len(node.Content) == 0 {
		return nil, specErrorf("%s must be a mapping with min, max or both", where)
	}
	r := &ValueRange{}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key, value := node.Content[i].Value, node.Content[i+1]
		if value.Kind != yaml.ScalarNode {
			return nil, specErrorf("%s %s must be a number or an ISO 8601 date", where, key)
		}
		var bound any
		switch value.Tag {
		case "!!int":
			n, err := strconv.ParseInt(value.Value, 0, 64)
			if err != nil {
				return nil, specErrorf("%s %s: %v", where, key, err)
			}
			bound = n
		case "!!float":
			f, err := strconv.ParseFloat(value.Value, 64)
			if err != nil {
				return nil, specErrorf("%s %s: %v", where, key, err)
			}
			bound = f
		case "!!str", "!!timestamp":
			bound = value.Value
		default:
			return nil, specErrorf("%s %s must be a number or an ISO 8601 date", where, key)
		}
		switch key {
		case "min":
			r.Min = bound
		case "max":
			r.Max = bound
		default:
			return nil, specErrorf("%s key %q is unknown; write min, max or both", where, key)
		}
	}
	return r, nil
}

func parseLengthNode(where string, node *yaml.Node) (*LengthRange, error) {
	if node.Kind != yaml.MappingNode || len(node.Content) == 0 {
		return nil, specErrorf("%s must be a mapping with min, max or both", where)
	}
	l := &LengthRange{}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key, value := node.Content[i].Value, node.Content[i+1]
		n, err := strconv.Atoi(value.Value)
		if value.Kind != yaml.ScalarNode || value.Tag != "!!int" || err != nil || n < 0 {
			return nil, specErrorf("%s %s must be a non-negative integer", where, key)
		}
		switch key {
		case "min":
			l.Min = &n
		case "max":
			l.Max = &n
		default:
			return nil, specErrorf("%s key %q is unknown; write min, max or both", where, key)
		}
	}
	return l, nil
}

// ValidateObjectTypeDeclarations checks the property declarations of an object type against each
// other and against the property types: the Semantic rules and every constraint's fit. Import runs
// it on a 3.0 definition; a save through the API runs the same check.
func ValidateObjectTypeDeclarations(ot *BknObjectType) error {
	if err := validateAliases("object type aliases", ot.Aliases, ot.Name); err != nil {
		return err
	}
	if err := validateLifecycle("object type lifecycle", ot.Lifecycle, ot.ID); err != nil {
		return err
	}
	return validatePropertyDeclarations(ot.DataProperties)
}

// validatePropertyDeclarations checks the declarations of the data properties of one object type or
// one relation against each other and against the property types: the Semantic rules, aliases and
// every constraint's fit.
func validatePropertyDeclarations(props []*DataProperty) error {
	if err := validatePropertyAliases(props); err != nil {
		return err
	}
	semantics := map[string]*DataProperty{}
	for _, p := range props {
		if p == nil || p.Semantic == "" {
			continue
		}
		allowed, ok := semanticTypes[p.Semantic]
		if !ok {
			return specErrorf("data property %q: Semantic %q is unknown; the values are %s", p.Name, p.Semantic, strings.Join(semanticOrder, ", "))
		}
		if !containsString(allowed, normType(p.Type)) {
			return specErrorf("data property %q: Semantic %s needs Type %s, got %q", p.Name, p.Semantic, strings.Join(allowed, ", "), p.Type)
		}
		if other, dup := semantics[p.Semantic]; dup {
			return specErrorf("data properties %q and %q both declare Semantic %s; each value is declared once", other.Name, p.Name, p.Semantic)
		}
		semantics[p.Semantic] = p
	}
	if _, ok := semantics[SemanticEndTime]; ok {
		if _, ok := semantics[SemanticEventTime]; !ok {
			return specErrorf("Semantic end_time needs an event_time property alongside")
		}
	}
	_, hasLat := semantics[SemanticLatitude]
	_, hasLon := semantics[SemanticLongitude]
	if hasLat != hasLon {
		return specErrorf("Semantic latitude and longitude are declared in pairs")
	}
	if _, ok := semantics[SemanticLocation]; ok && hasLat {
		return specErrorf("Semantic location and latitude / longitude describe the same position; declare one or the other")
	}
	for _, p := range props {
		if p == nil || p.Constraints == nil {
			continue
		}
		if err := validateConstraints(p); err != nil {
			return err
		}
	}
	if hasLat {
		lat, lon := semantics[SemanticLatitude].Constraints, semantics[SemanticLongitude].Constraints
		if lat != nil && lon != nil && lat.CRS != "" && lon.CRS != "" && lat.CRS != lon.CRS {
			return specErrorf("latitude and longitude must use the same crs, got %s and %s", lat.CRS, lon.CRS)
		}
	}
	return nil
}

func validateConstraints(p *DataProperty) error {
	c, typ := p.Constraints, normType(p.Type)
	where := fmt.Sprintf("property constraints %q", p.Name)
	if c.ValueType != "" && (c.Range != nil || c.Length != nil || c.Pattern != "" || c.Unit != "") {
		return specErrorf("%s: the property references value type %q, which holds its value rules; "+
			"write range, length, pattern and unit in the value type, or use another value type", where, c.ValueType)
	}
	if err := validateValueRules(where, typ, p.Type, c.Range, c.Length, c.Pattern, c.Unit); err != nil {
		return err
	}
	if c.Unique && containsString(notUniqueTypes, typ) {
		return specErrorf("%s: unique does not apply to Type %q", where, p.Type)
	}
	if c.CRS != "" {
		if !containsString([]string{SemanticLocation, SemanticLatitude, SemanticLongitude}, p.Semantic) {
			return specErrorf("%s: crs applies to a property whose Semantic is location, latitude or longitude", where)
		}
		if !containsString(validCRS, c.CRS) {
			return specErrorf("%s: crs must be %s, got %q", where, strings.Join(validCRS, ", "), c.CRS)
		}
	}
	if c.Timezone != "" {
		if typ != "datetime" {
			return specErrorf("%s: timezone applies to Type datetime, got %q", where, p.Type)
		}
		if _, err := time.LoadLocation(c.Timezone); err != nil || c.Timezone == "Local" {
			return specErrorf("%s: timezone %q is not an IANA time zone name such as Asia/Shanghai", where, c.Timezone)
		}
	}
	return nil
}

// validateValueRules checks range, length, pattern and unit against a type: the property Type, or a
// value type's base_type. written is the type as written, for the message.
func validateValueRules(where, typ, written string, rng *ValueRange, length *LengthRange, pattern, unit string) error {
	if rng != nil {
		if err := validateRange(where, typ, rng); err != nil {
			return err
		}
	}
	if length != nil {
		if !containsString(stringTypes, typ) {
			return specErrorf("%s: length needs Type %s, got %q", where, strings.Join(stringTypes, ", "), written)
		}
		if length.Min != nil && length.Max != nil && *length.Min > *length.Max {
			return specErrorf("%s: length min %d is greater than max %d", where, *length.Min, *length.Max)
		}
	}
	if pattern != "" {
		if !containsString(stringTypes, typ) {
			return specErrorf("%s: pattern needs Type %s, got %q", where, strings.Join(stringTypes, ", "), written)
		}
		if _, err := regexp.Compile(pattern); err != nil {
			return specErrorf("%s: pattern is not a valid RE2 expression: %v", where, err)
		}
	}
	if unit != "" && !containsString(numericTypes, typ) {
		return specErrorf("%s: unit needs a numeric Type, got %q", where, written)
	}
	return nil
}

func validateRange(where, typ string, r *ValueRange) error {
	switch {
	case containsString(numericTypes, typ):
		lo, loOK := numericBound(r.Min)
		hi, hiOK := numericBound(r.Max)
		if (r.Min != nil && !loOK) || (r.Max != nil && !hiOK) {
			return specErrorf("%s: range of a numeric property takes numbers", where)
		}
		if loOK && hiOK && lo > hi {
			return specErrorf("%s: range min %v is greater than max %v", where, r.Min, r.Max)
		}
	case containsString(dateTypes, typ):
		lo, loOK := timeBound(typ, r.Min)
		hi, hiOK := timeBound(typ, r.Max)
		if (r.Min != nil && !loOK) || (r.Max != nil && !hiOK) {
			if typ == "date" {
				return specErrorf("%s: range of a date property takes ISO 8601 dates such as \"2020-01-01\"", where)
			}
			return specErrorf("%s: range of a %s property takes ISO 8601 times such as \"2020-01-01T00:00:00Z\"", where, typ)
		}
		if loOK && hiOK && lo.After(hi) {
			return specErrorf("%s: range min %v is later than max %v", where, r.Min, r.Max)
		}
	default:
		return specErrorf("%s: range needs a numeric, date, datetime or timestamp Type, got %q", where, typ)
	}
	return nil
}

func numericBound(v any) (float64, bool) {
	switch n := v.(type) {
	case int64:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}

func timeBound(typ string, v any) (time.Time, bool) {
	s, ok := v.(string)
	if !ok {
		return time.Time{}, false
	}
	if typ == "date" {
		if !isoDateRE.MatchString(s) {
			return time.Time{}, false
		}
		t, err := time.Parse("2006-01-02", s)
		return t, err == nil
	}
	t, err := time.Parse(time.RFC3339, s)
	return t, err == nil
}

// Aliases are other names of an object type, a property or an action type (SPECIFICATION.md §6.8,
// §8.2), for concept search and understanding; they take no part in queries.
const (
	maxAliases     = 10
	maxAliasRunes  = 40
	aliasSeparator = ","
	fullWidthComma = "\uff0c"
)

// parseAliasesNode reads the aliases of a frontmatter, which checkFrontmatterV3 has made sure is a
// list. Each alias is trimmed.
func parseAliasesNode(node *yaml.Node) ([]string, error) {
	if node == nil {
		return nil, nil
	}
	aliases := make([]string, 0, len(node.Content))
	for _, item := range node.Content {
		if item.Kind != yaml.ScalarNode || item.Tag == "!!null" {
			return nil, specErrorf("frontmatter aliases must list names, such as [patient, inpatient]")
		}
		aliases = append(aliases, strings.TrimSpace(item.Value))
	}
	return aliases, nil
}

// parseAliasesCell reads an Aliases cell: names separated by a half-width comma, each trimmed.
func parseAliasesCell(cell string) []string {
	if strings.TrimSpace(cell) == "" {
		return nil
	}
	aliases := strings.Split(cell, aliasSeparator)
	for i := range aliases {
		aliases[i] = strings.TrimSpace(aliases[i])
	}
	return aliases
}

// validateAliases checks one group of aliases on its own: each alias, the count, duplicates, and
// the names the group belongs to.
func validateAliases(where string, aliases []string, own ...string) error {
	if len(aliases) > maxAliases {
		return specErrorf("%s: %d aliases, at most %d", where, len(aliases), maxAliases)
	}
	seen := map[string]bool{}
	for _, alias := range aliases {
		switch {
		case alias == "":
			return specErrorf("%s: an alias is empty", where)
		case strings.Contains(alias, fullWidthComma):
			return specErrorf("%s: alias %q holds a full-width comma; separate aliases with a half-width comma ,", where, alias)
		case strings.Contains(alias, aliasSeparator):
			return specErrorf("%s: alias %q holds a comma", where, alias)
		case utf8.RuneCountInString(alias) > maxAliasRunes:
			return specErrorf("%s: alias %q is longer than %d characters", where, alias, maxAliasRunes)
		case seen[alias]:
			return specErrorf("%s: alias %q appears twice", where, alias)
		case containsString(own, alias):
			return specErrorf("%s: alias %q is the same as its own name", where, alias)
		}
		seen[alias] = true
	}
	return nil
}

// validatePropertyAliases checks the aliases of each property, and that none is the Name or Display
// Name of another property of the object type.
func validatePropertyAliases(props []*DataProperty) error {
	owner := map[string]string{}
	for _, p := range props {
		if p == nil {
			continue
		}
		for _, name := range []string{p.Name, p.DisplayName} {
			if _, taken := owner[name]; !taken && name != "" {
				owner[name] = p.Name
			}
		}
	}
	for _, p := range props {
		if p == nil {
			continue
		}
		where := fmt.Sprintf("data property %q aliases", p.Name)
		if err := validateAliases(where, p.Aliases, p.Name, p.DisplayName); err != nil {
			return err
		}
		for _, alias := range p.Aliases {
			if other, taken := owner[alias]; taken && other != p.Name {
				return specErrorf("%s: alias %q is the name of data property %q", where, alias, other)
			}
		}
	}
	return nil
}

// ValidateActionTypeDeclarations checks the declarations of an action type on its own: its aliases.
// Import runs it on a 3.0 definition; a save through the API runs the same check.
func ValidateActionTypeDeclarations(at *BknActionType) error {
	if err := validateAliases("action type aliases", at.Aliases, at.Name); err != nil {
		return err
	}
	return validateLifecycle("action type lifecycle", at.Lifecycle, at.ID)
}

// ValidateNetworkDeclarations checks the 3.0 declarations that span definitions: value type names,
// the value types properties reference, aliases against the names of other definitions of the same
// kind, lifecycle replaced_by references, deprecated endpoints and fallback paths. Import runs it once every file of a 3.0 package is read; a save through the API runs
// the same check on the network it would produce.
func ValidateNetworkDeclarations(doc *BknNetwork) error {
	valueTypes := map[string]*BknValueType{}
	named := map[string]string{}
	for _, vt := range doc.ValueTypes {
		if _, dup := valueTypes[vt.ID]; dup {
			return specErrorf("value type id %q appears twice", vt.ID)
		}
		if other, dup := named[vt.Name]; dup {
			return specErrorf("value types %q and %q are both named %q; a value type name is unique in the network", other, vt.ID, vt.Name)
		}
		valueTypes[vt.ID], named[vt.Name] = vt, vt.ID
	}
	checkReferences := func(owner string, props []*DataProperty) error {
		for _, p := range props {
			if p == nil || p.Constraints == nil || p.Constraints.ValueType == "" {
				continue
			}
			vt, ok := valueTypes[p.Constraints.ValueType]
			if !ok {
				return specErrorf("%s data property %q: value type %q is not defined in this network", owner, p.Name, p.Constraints.ValueType)
			}
			if normType(p.Type) != vt.BaseType {
				return specErrorf("%s data property %q: value type %q has base_type %s, the property Type is %q",
					owner, p.Name, vt.ID, vt.BaseType, p.Type)
			}
		}
		return nil
	}
	for _, ot := range doc.ObjectTypes {
		if err := checkReferences(fmt.Sprintf("object type %q", ot.ID), ot.DataProperties); err != nil {
			return err
		}
	}
	for _, rt := range doc.RelationTypes {
		if err := checkReferences(fmt.Sprintf("relation type %q", rt.ID), rt.DataProperties); err != nil {
			return err
		}
	}
	objectNames := map[string]string{}
	for _, ot := range doc.ObjectTypes {
		objectNames[ot.Name] = ot.ID
	}
	for _, ot := range doc.ObjectTypes {
		for _, alias := range ot.Aliases {
			if other, taken := objectNames[alias]; taken && other != ot.ID {
				return specErrorf("object type %q alias %q is the name of object type %q", ot.ID, alias, other)
			}
		}
	}
	actionNames := map[string]string{}
	for _, at := range doc.ActionTypes {
		actionNames[at.Name] = at.ID
	}
	for _, at := range doc.ActionTypes {
		for _, alias := range at.Aliases {
			if other, taken := actionNames[alias]; taken && other != at.ID {
				return specErrorf("action type %q alias %q is the name of action type %q", at.ID, alias, other)
			}
		}
	}
	return validateNetworkRelations(doc)
}
