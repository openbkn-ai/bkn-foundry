// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"unicode"
)

// ignoredDiffFields are struct fields that must not produce a change of their own.
//
// RawContent is the file's whole text: every real edit already shows up as the field it touched,
// and reporting the raw text too would bury it. Summary is derived from Description. The Has*
// booleans record which markdown sections the parser saw, which says nothing about content. Type
// is the definition's kind and is already carried on the entry.
var ignoredDiffFields = map[string]bool{
	"RawContent":                   true,
	"SkillContent":                 true,
	"Summary":                      true,
	"Type":                         true,
	"HasDataPropertiesSection":     true,
	"HasKeysSection":               true,
	"HasScopeSection":              true,
	"HasMetricAttributesSection":   true,
	"HasCalculationFormulaSection": true,
	"HasTimeDimensionSection":      true,
	"HasAnalysisDimensionsSection": true,
}

// elementKeyFields are the fields tried, in order, when aligning a slice of structs. A data
// property table is a set keyed by property name, not a list: aligning it by position would report
// every row below an insertion as modified.
var elementKeyFields = []string{"Name", "Property", "SourceProperty", "Field", "ID"}

// diffDefinitionValues walks two definitions of the same kind and reports every field that differs.
func diffDefinitionValues(base, target any) []FieldChange {
	var changes []FieldChange
	diffValues("", reflect.ValueOf(base), reflect.ValueOf(target), &changes)
	sort.SliceStable(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes
}

func diffValues(path string, base, target reflect.Value, out *[]FieldChange) {
	base = unwrap(base)
	target = unwrap(target)

	switch {
	case !base.IsValid() && !target.IsValid():
		return
	case !base.IsValid():
		appendChange(out, path, DiffCreate, "", renderValue(target))
		return
	case !target.IsValid():
		appendChange(out, path, DiffDelete, renderValue(base), "")
		return
	}

	// A value that changed shape — a struct replaced by a scalar — is reported as a plain
	// replacement rather than walked, which would produce paths that exist on neither side.
	if base.Kind() != target.Kind() {
		appendChange(out, path, DiffUpdate, renderValue(base), renderValue(target))
		return
	}

	switch base.Kind() {
	case reflect.Struct:
		diffStructs(path, base, target, out)
	case reflect.Slice, reflect.Array:
		diffSlices(path, base, target, out)
	case reflect.Map:
		diffMaps(path, base, target, out)
	default:
		if renderValue(base) != renderValue(target) {
			appendChange(out, path, DiffUpdate, renderValue(base), renderValue(target))
		}
	}
}

func diffStructs(path string, base, target reflect.Value, out *[]FieldChange) {
	t := base.Type()
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if field.PkgPath != "" || ignoredDiffFields[field.Name] {
			continue
		}
		// Embedded frontmatter carries id, name and tags. It is part of the definition, not a
		// nested object, so its fields keep the top-level path.
		childPath := joinPath(path, snakeCase(field.Name))
		if field.Anonymous {
			childPath = path
		}
		diffValues(childPath, base.Field(i), target.Field(i), out)
	}
}

func diffSlices(path string, base, target reflect.Value, out *[]FieldChange) {
	keyField := sliceKeyField(base, target)
	if keyField == "" {
		// No stable key: compare the sequence as a whole. Tags and primary keys read as one value
		// to a person, and "tags: A, B, 已审核 → A, B, 已废止" is more useful than three index-keyed
		// entries.
		if renderValue(base) != renderValue(target) {
			appendChange(out, path, DiffUpdate, renderValue(base), renderValue(target))
		}
		return
	}

	baseByKey, baseOrder := indexByKey(base, keyField)
	targetByKey, targetOrder := indexByKey(target, keyField)

	for _, key := range baseOrder {
		baseItem := baseByKey[key]
		targetItem, ok := targetByKey[key]
		if !ok {
			appendChange(out, fmt.Sprintf("%s[%s]", path, key), DiffDelete, renderValue(baseItem), "")
			continue
		}
		diffValues(fmt.Sprintf("%s[%s]", path, key), baseItem, targetItem, out)
	}
	for _, key := range targetOrder {
		if _, ok := baseByKey[key]; !ok {
			appendChange(out, fmt.Sprintf("%s[%s]", path, key), DiffCreate, "", renderValue(targetByKey[key]))
		}
	}
}

func diffMaps(path string, base, target reflect.Value, out *[]FieldChange) {
	keys := map[string]bool{}
	baseByKey := map[string]reflect.Value{}
	targetByKey := map[string]reflect.Value{}
	for _, k := range base.MapKeys() {
		key := fmt.Sprint(k.Interface())
		baseByKey[key] = base.MapIndex(k)
		keys[key] = true
	}
	for _, k := range target.MapKeys() {
		key := fmt.Sprint(k.Interface())
		targetByKey[key] = target.MapIndex(k)
		keys[key] = true
	}

	ordered := make([]string, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)

	for _, key := range ordered {
		diffValues(joinPath(path, key), baseByKey[key], targetByKey[key], out)
	}
}

// sliceKeyField picks the field both sides can be aligned on, if the elements are structs.
func sliceKeyField(base, target reflect.Value) string {
	elem := firstStructElem(base)
	if !elem.IsValid() {
		elem = firstStructElem(target)
	}
	if !elem.IsValid() {
		return ""
	}
	for _, name := range elementKeyFields {
		field := elem.FieldByName(name)
		if field.IsValid() && field.Kind() == reflect.String {
			return name
		}
	}
	return ""
}

func firstStructElem(v reflect.Value) reflect.Value {
	if !v.IsValid() || v.Len() == 0 {
		return reflect.Value{}
	}
	elem := unwrap(v.Index(0))
	if !elem.IsValid() || elem.Kind() != reflect.Struct {
		return reflect.Value{}
	}
	return elem
}

// indexByKey groups a slice by its key field, preserving first-seen order for stable output.
func indexByKey(v reflect.Value, keyField string) (map[string]reflect.Value, []string) {
	byKey := make(map[string]reflect.Value, v.Len())
	var order []string
	for i := 0; i < v.Len(); i++ {
		elem := unwrap(v.Index(i))
		if !elem.IsValid() || elem.Kind() != reflect.Struct {
			continue
		}
		key := elem.FieldByName(keyField).String()
		if key == "" {
			key = fmt.Sprintf("#%d", i)
		}
		if _, seen := byKey[key]; seen {
			// A duplicate key cannot be aligned; keep both by qualifying the second one.
			key = fmt.Sprintf("%s#%d", key, i)
		}
		byKey[key] = elem
		order = append(order, key)
	}
	return byKey, order
}

func appendChange(out *[]FieldChange, path string, kind DiffAction, oldVal, newVal string) {
	if path == "" {
		path = "."
	}
	*out = append(*out, FieldChange{Path: path, Kind: kind, Old: oldVal, New: newVal})
}

// unwrap follows pointers and interfaces, and reports a nil pointer as an absent value so that
// setting or clearing an optional section reads as a create or a delete.
func unwrap(v reflect.Value) reflect.Value {
	for v.IsValid() && (v.Kind() == reflect.Ptr || v.Kind() == reflect.Interface) {
		if v.IsNil() {
			return reflect.Value{}
		}
		v = v.Elem()
	}
	if v.IsValid() && (v.Kind() == reflect.Slice || v.Kind() == reflect.Map) && v.IsNil() {
		// A nil slice and an empty slice mean the same thing to a reader; treating them
		// differently would report a change whenever a parser produced one instead of the other.
		return reflect.MakeSlice(sliceOrMapType(v), 0, 0)
	}
	return v
}

func sliceOrMapType(v reflect.Value) reflect.Type {
	if v.Kind() == reflect.Map {
		return reflect.SliceOf(v.Type().Elem())
	}
	return v.Type()
}

// renderValue prints a value the way it should read in a diff, not the way Go prints it.
func renderValue(v reflect.Value) string {
	v = unwrap(v)
	if !v.IsValid() {
		return ""
	}
	switch v.Kind() {
	case reflect.Slice, reflect.Array:
		parts := make([]string, 0, v.Len())
		for i := 0; i < v.Len(); i++ {
			parts = append(parts, renderValue(v.Index(i)))
		}
		return strings.Join(parts, ", ")
	case reflect.Struct:
		t := v.Type()
		parts := make([]string, 0, t.NumField())
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			if field.PkgPath != "" || ignoredDiffFields[field.Name] {
				continue
			}
			rendered := renderValue(v.Field(i))
			if rendered == "" {
				continue
			}
			parts = append(parts, snakeCase(field.Name)+"="+rendered)
		}
		return strings.Join(parts, " ")
	case reflect.Map:
		keys := make([]string, 0, v.Len())
		values := map[string]string{}
		for _, k := range v.MapKeys() {
			key := fmt.Sprint(k.Interface())
			keys = append(keys, key)
			values[key] = renderValue(v.MapIndex(k))
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			parts = append(parts, key+"="+values[key])
		}
		return strings.Join(parts, " ")
	default:
		return fmt.Sprint(v.Interface())
	}
}

func joinPath(parent, child string) string {
	if parent == "" {
		return child
	}
	return parent + "." + child
}

func snakeCase(name string) string {
	var sb strings.Builder
	runes := []rune(name)
	for i, r := range runes {
		if unicode.IsUpper(r) {
			// Keep acronyms together: "KNID" becomes "kn_id", not "k_n_i_d".
			if i > 0 && (!unicode.IsUpper(runes[i-1]) || (i+1 < len(runes) && !unicode.IsUpper(runes[i+1]))) {
				sb.WriteRune('_')
			}
			sb.WriteRune(unicode.ToLower(r))
			continue
		}
		sb.WriteRune(r)
	}
	return sb.String()
}
