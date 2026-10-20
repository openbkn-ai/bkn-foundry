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
			return specErrorf("property constraints: %q is not a data property of this object type", name)
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
			return nil, specErrorf("%s: value types %s", where, unsupportedYet)
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
	semantics := map[string]*DataProperty{}
	for _, p := range ot.DataProperties {
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
			return specErrorf("data properties %q and %q both declare Semantic %s; an object type declares each value once", other.Name, p.Name, p.Semantic)
		}
		semantics[p.Semantic] = p
	}
	if _, ok := semantics[SemanticEndTime]; ok {
		if _, ok := semantics[SemanticEventTime]; !ok {
			return specErrorf("Semantic end_time needs an event_time property in the same object type")
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
	for _, p := range ot.DataProperties {
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
	if c.Range != nil {
		if err := validateRange(where, typ, c.Range); err != nil {
			return err
		}
	}
	if c.Length != nil {
		if !containsString(stringTypes, typ) {
			return specErrorf("%s: length needs Type %s, got %q", where, strings.Join(stringTypes, ", "), p.Type)
		}
		if c.Length.Min != nil && c.Length.Max != nil && *c.Length.Min > *c.Length.Max {
			return specErrorf("%s: length min %d is greater than max %d", where, *c.Length.Min, *c.Length.Max)
		}
	}
	if c.Pattern != "" {
		if !containsString(stringTypes, typ) {
			return specErrorf("%s: pattern needs Type %s, got %q", where, strings.Join(stringTypes, ", "), p.Type)
		}
		if _, err := regexp.Compile(c.Pattern); err != nil {
			return specErrorf("%s: pattern is not a valid RE2 expression: %v", where, err)
		}
	}
	if c.Unit != "" && !containsString(numericTypes, typ) {
		return specErrorf("%s: unit needs a numeric Type, got %q", where, p.Type)
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

// hasPropertyDeclarations reports whether a property carries a 3.0 declaration that a 2.x export
// would drop. Required on a primary key is implied, so it alone is not one.
func hasPropertyDeclarations(p *DataProperty, primaryKeys []string) bool {
	if p == nil {
		return false
	}
	return p.Semantic != "" || p.Constraints != nil || (p.Required && !containsString(primaryKeys, p.Name))
}

// HasV3Declarations reports whether a network carries any 3.0 declaration, and so has to be
// exported under bkn_spec 3.0: a 2.x package cannot hold them, and importing it again would drop
// them.
func HasV3Declarations(doc *BknNetwork) bool {
	for _, ot := range doc.ObjectTypes {
		for _, p := range ot.DataProperties {
			if hasPropertyDeclarations(p, ot.PrimaryKeys) {
				return true
			}
		}
	}
	return false
}
