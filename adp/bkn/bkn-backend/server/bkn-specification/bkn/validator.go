// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Aligned with adp bkn-backend/interfaces/common.go RegexPattern_NonBuiltin_ID
var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)

// Property names: adp RegexPattern_Property_Name
var propertyNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,39}$`)

// NAME_INVALID_CHARACTER from adp bkn-backend/interfaces/common.go
const nameInvalidChars = `/:?\"<>|：？''""！《》,#[]{}%&*$^!=.'`

const (
	objectNameMaxLength = 40
	tagsMaxNumber       = 5
	maxPropertyNum      = 1000
)

var validDataSourceTypes = map[string]bool{
	DATA_SOURCE_TYPE_DATA_VIEW: true,
	DATA_SOURCE_TYPE_RESOURCE:  true,
}

var validRelationMappingTypes = map[string]bool{
	RELATION_MAPPING_TYPE_DIRECT:              true,
	RELATION_MAPPING_TYPE_DATA_VIEW:           true,
	RELATION_MAPPING_TYPE_FILTERED_CROSS_JOIN: true,
}

const valueFromProperty = "property"

// actionCondMaxSub matches backend: max sub-conditions for action trigger (cond.MaxSubCondition)
const actionCondMaxSub = 100

// ValidationError represents a single validation problem.
type ValidationError struct {
	Table   string
	Row     *int
	Column  string
	Code    string
	Message string
}

// ValidationResult aggregates validation outcome.
type ValidationResult struct {
	Errors []ValidationError
}

// OK returns true if there are no errors.
func (r *ValidationResult) OK() bool {
	return len(r.Errors) == 0
}

func appendError(result *ValidationResult, table, column, code, message string) {
	result.Errors = append(result.Errors, ValidationError{
		Table:   table,
		Row:     nil,
		Column:  column,
		Code:    code,
		Message: message,
	})
}

func normType(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

var validPrimaryKeyTypes = map[string]bool{
	"integer": true, "unsigned integer": true, "string": true, "text": true,
}

var validDisplayKeyTypes = map[string]bool{
	"integer": true, "unsigned integer": true, "float": true, "decimal": true,
	"string": true, "text": true, "date": true, "timestamp": true, "time": true,
	"datetime": true, "boolean": true,
}

var validDataPropertyTypes = map[string]bool{
	"integer": true, "unsigned integer": true, "string": true, "float": true, "decimal": true,
	"text": true, "date": true, "timestamp": true, "time": true, "datetime": true,
	"boolean": true, "binary": true, "json": true, "vector": true, "point": true, "shape": true, "ip": true,
}

var validIncrementalKeyTypes = map[string]bool{
	"integer": true, "datetime": true, "timestamp": true,
}

var validLogicPropertyTypes = map[string]bool{
	"metric": true, "tool": true,
}

var validLogicSourceTypes = map[string]bool{
	"metric": true, "tool": true,
}

var validActionKinds = map[string]bool{
	"add": true, "modify": true, "delete": true,
}

var validActionSourceTypes = map[string]bool{
	"tool": true, "mcp": true,
}

var validMetricAggregationAggr = map[string]bool{
	"count": true, "count_distinct": true, "sum": true, "max": true, "min": true, "avg": true,
}

// actionCondOps — adp ActionCondOperationMap keys + common aliases (==, !=)
var actionCondOps = map[string]bool{
	"and": true, "or": true,
	"eq": true, "not_eq": true, "gt": true, "gte": true, "lt": true, "lte": true,
	"in": true, "not_in": true,
	"empty": true, "not_empty": true, "true": true, "false": true,
	"range": true, "out_range": true, "before": true, "between": true,
	"exist": true, "not_exist": true,
	"like": true, "not_like": true, "prefix": true, "not_prefix": true,
	"null": true, "not_null": true,
	"regex": true, "contain": true, "not_contain": true, "current": true,
	"==": true, "!=": true, ">": true, ">=": true, "<": true, "<=": true,
}

// ValidateNetwork performs structural and business validation aligned with adp bkn-backend driveradapters/validate*.go
func ValidateNetwork(doc *BknNetwork) *ValidationResult {
	result := &ValidationResult{}

	// Root network.bkn
	if strings.TrimSpace(doc.Type) == "" {
		appendError(result, "network.bkn", "type", "missing_frontmatter_field", "frontmatter 'type' is required")
	}
	if strings.TrimSpace(doc.ID) == "" {
		appendError(result, "network.bkn", "id", "missing_frontmatter_field", "frontmatter 'id' is required")
	}
	if strings.TrimSpace(doc.Name) == "" {
		appendError(result, "network.bkn", "name", "missing_frontmatter_field", "frontmatter 'name' is required")
	}
	if doc.ID != "" {
		if err := validateIDString(doc.ID); err != "" {
			appendError(result, "network.bkn", "id", "invalid_id", err)
		}
	}
	if doc.Name != "" {
		if err := validateObjectName(doc.Name, "knowledge_network"); err != "" {
			appendError(result, "network.bkn", "name", "invalid_name", err)
		}
	}
	if err := validateTags(doc.Tags); err != nil {
		appendError(result, "network.bkn", "tags", "invalid_tags", err.Error())
	}

	duplicateIDs := func(ids []string, kind string) {
		seen := make(map[string]int)
		for _, id := range ids {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			seen[id]++
		}
		for id, n := range seen {
			if n > 1 {
				appendError(result, "network.bkn", "id", "duplicate_id", fmt.Sprintf("duplicate %s id %q in network", kind, id))
			}
		}
	}
	duplicateNames := func(names []string, kind string) {
		seen := make(map[string]int)
		for _, id := range names {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			seen[id]++
		}
		for id, n := range seen {
			if n > 1 {
				appendError(result, "network.bkn", "name", "duplicate_name", fmt.Sprintf("duplicate %s name %q in network", kind, id))
			}
		}
	}

	var otIDs, otNames []string
	for _, ot := range doc.ObjectTypes {
		otIDs = append(otIDs, ot.ID)
		otNames = append(otNames, ot.Name)
	}
	duplicateIDs(otIDs, "object_type")
	duplicateNames(otNames, "object_type")

	var rtIDs []string
	for _, rt := range doc.RelationTypes {
		rtIDs = append(rtIDs, rt.ID)
	}
	duplicateIDs(rtIDs, "relation_type")

	var atIDs, atNames []string
	for _, at := range doc.ActionTypes {
		atIDs = append(atIDs, at.ID)
		atNames = append(atNames, at.Name)
	}
	duplicateIDs(atIDs, "action_type")
	duplicateNames(atNames, "action_type")

	var rtRiskIDs, rtRiskNames []string
	for _, r := range doc.RiskTypes {
		rtRiskIDs = append(rtRiskIDs, r.ID)
		rtRiskNames = append(rtRiskNames, r.Name)
	}
	duplicateIDs(rtRiskIDs, "risk_type")
	duplicateNames(rtRiskNames, "risk_type")

	var cgIDs, cgNames []string
	for _, cg := range doc.ConceptGroups {
		cgIDs = append(cgIDs, cg.ID)
		cgNames = append(cgNames, cg.Name)
	}
	duplicateIDs(cgIDs, "concept_group")
	duplicateNames(cgNames, "concept_group")

	var metricIDs, metricNames []string
	for _, met := range doc.Metrics {
		metricIDs = append(metricIDs, met.ID)
		metricNames = append(metricNames, met.Name)
	}
	duplicateIDs(metricIDs, "metric")
	duplicateNames(metricNames, "metric")

	objectIDs := make(map[string]struct{})
	otByID := make(map[string]*BknObjectType)
	for _, ot := range doc.ObjectTypes {
		objectIDs[ot.ID] = struct{}{}
		otByID[ot.ID] = ot
	}

	for _, ot := range doc.ObjectTypes {
		t := tableName("object_type", ot.ID)
		validateDefFrontmatter(result, t, ot.Type, ot.ID, ot.Name)
		if err := validateTags(ot.Tags); err != nil {
			appendError(result, t, "tags", "invalid_tags", err.Error())
		}
		if !ot.HasDataPropertiesSection {
			appendError(result, t, "", "missing_section", "ObjectType must include a ### Data Properties section")
		}
		if !ot.HasKeysSection {
			appendError(result, t, "", "missing_section", "ObjectType must include a ### Keys section")
		}
		validateObjectTypeDeep(result, t, ot)
	}

	for _, rt := range doc.RelationTypes {
		t := tableName("relation_type", rt.ID)
		validateDefFrontmatter(result, t, rt.Type, rt.ID, rt.Name)
		if err := validateTags(rt.Tags); err != nil {
			appendError(result, t, "tags", "invalid_tags", err.Error())
		}
		validateRelationTypeDeep(result, t, rt)
		src := strings.TrimSpace(rt.Endpoint.Source)
		tgt := strings.TrimSpace(rt.Endpoint.Target)
		if src == "" && tgt == "" {
			appendError(result, t, "", "empty_endpoint", "RelationType must have at least one endpoint row under ### Endpoint")
		}
		if src != "" {
			if _, ok := objectIDs[src]; !ok {
				appendError(result, t, "Source", "invalid_endpoint_ref", fmt.Sprintf("endpoint source %q is not a defined object type id", src))
			}
		}
		if tgt != "" {
			if _, ok := objectIDs[tgt]; !ok {
				appendError(result, t, "Target", "invalid_endpoint_ref", fmt.Sprintf("endpoint target %q is not a defined object type id", tgt))
			}
		}
	}

	for _, at := range doc.ActionTypes {
		t := tableName("action_type", at.ID)
		validateDefFrontmatter(result, t, at.Type, at.ID, at.Name)
		if err := validateTags(at.Tags); err != nil {
			appendError(result, t, "tags", "invalid_tags", err.Error())
		}
		validateActionTypeDeep(result, t, at)
		bo := strings.TrimSpace(at.BoundObject)
		if bo != "" {
			if _, ok := objectIDs[bo]; !ok {
				appendError(result, t, "Bound Object", "invalid_bound_object_ref", fmt.Sprintf("bound object %q is not a defined object type id", bo))
			}
		}
		for i, ic := range at.ImpactContracts {
			if ic == nil {
				continue
			}
			prefix := fmt.Sprintf("impact_contracts[%d]", i)
			eo := strings.TrimSpace(ic.ExpectedOperation)
			if eo != "" && !validActionKinds[strings.ToLower(eo)] {
				appendError(result, t, prefix+".expected_operation", "invalid_impact_contract",
					fmt.Sprintf("expected_operation must be one of add, modify, delete, got %q", ic.ExpectedOperation))
			}
			oid := strings.TrimSpace(ic.ObjectTypeID)
			if oid != "" {
				if _, ok := objectIDs[oid]; !ok {
					appendError(result, t, prefix+".object_type_id", "invalid_impact_contract_ref",
						fmt.Sprintf("impact contract object_type_id %q is not a defined object type id", oid))
				}
			}
		}
	}

	for _, r := range doc.RiskTypes {
		t := tableName("risk_type", r.ID)
		validateDefFrontmatter(result, t, r.Type, r.ID, r.Name)
		if err := validateTags(r.Tags); err != nil {
			appendError(result, t, "tags", "invalid_tags", err.Error())
		}
	}

	for _, cg := range doc.ConceptGroups {
		t := tableName("concept_group", cg.ID)
		validateDefFrontmatter(result, t, cg.Type, cg.ID, cg.Name)
		if err := validateTags(cg.Tags); err != nil {
			appendError(result, t, "tags", "invalid_tags", err.Error())
		}
		for _, oid := range cg.ObjectTypes {
			oid = strings.TrimSpace(oid)
			if oid == "" {
				continue
			}
			if _, ok := objectIDs[oid]; !ok {
				appendError(result, t, "Object Types", "invalid_concept_group_ref", fmt.Sprintf("concept group lists unknown object type id %q", oid))
			}
		}
	}

	for _, met := range doc.Metrics {
		t := tableName("metric", met.ID)
		validateDefFrontmatter(result, t, met.Type, met.ID, met.Name)
		validateMetricDeep(result, t, met, otByID)
	}

	return result
}

func validateIDString(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	if !idPattern.MatchString(id) {
		return fmt.Sprintf("id %q must match %s", id, idPattern.String())
	}
	return ""
}

func validateObjectName(name string, _ string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "name must not be empty"
	}
	if utf8.RuneCountInString(name) > objectNameMaxLength {
		return fmt.Sprintf("name length exceeds %d characters", objectNameMaxLength)
	}
	return ""
}

func validateTags(tags []string) error {
	if len(tags) > tagsMaxNumber {
		return fmt.Errorf("at most %d tags allowed", tagsMaxNumber)
	}
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			return fmt.Errorf("tag must not be empty")
		}
		if utf8.RuneCountInString(tag) > objectNameMaxLength {
			return fmt.Errorf("tag %q exceeds max length %d", tag, objectNameMaxLength)
		}
		if strings.ContainsAny(tag, nameInvalidChars) {
			return fmt.Errorf("tag %q contains invalid characters", tag)
		}
	}
	return nil
}

func validateObjectTypeDeep(result *ValidationResult, table string, ot *BknObjectType) {
	if ot.DataSource != nil && strings.TrimSpace(ot.DataSource.Type) != "" {
		if !validDataSourceTypes[normType(ot.DataSource.Type)] {
			appendError(result, table, "data_source", "invalid_data_source",
				fmt.Sprintf("data_source.type must be %q or %q when set, got %q", DATA_SOURCE_TYPE_DATA_VIEW, DATA_SOURCE_TYPE_RESOURCE, ot.DataSource.Type))
		}
	}
	if len(ot.DataProperties) > maxPropertyNum {
		appendError(result, table, "data_properties", "invalid_object_type",
			fmt.Sprintf("data_properties count %d exceeds max %d", len(ot.DataProperties), maxPropertyNum))
	}
	if len(ot.LogicProperties) > maxPropertyNum {
		appendError(result, table, "logic_properties", "invalid_object_type",
			fmt.Sprintf("logic_properties count %d exceeds max %d", len(ot.LogicProperties), maxPropertyNum))
	}

	dataPropMap := make(map[string]*DataProperty)
	for _, dp := range ot.DataProperties {
		if dp == nil {
			continue
		}
		if err := validatePropertyName(dp.Name); err != nil {
			appendError(result, table, "data_properties", "invalid_property_name", err.Error())
		}
		if msg := validateObjectName(dp.DisplayName, ""); msg != "" {
			appendError(result, table, "data_properties", "invalid_display_name", fmt.Sprintf("property %q: %s", dp.Name, msg))
		}
		if strings.TrimSpace(dp.Type) != "" {
			nt := normType(dp.Type)
			if !validDataPropertyTypes[nt] {
				appendError(result, table, "data_properties", "invalid_property_type",
					fmt.Sprintf("data property %q has invalid type %q", dp.Name, dp.Type))
			}
		}
		if dp.MappedField != "" && strings.TrimSpace(dp.MappedField) == "" {
			appendError(result, table, "data_properties", "invalid_object_type", fmt.Sprintf("mapped_field for %q must not be empty when set", dp.Name))
		}
		dataPropMap[dp.Name] = dp
	}

	if len(ot.PrimaryKeys) == 0 {
		appendError(result, table, "primary_keys", "null_primary_keys", "primary_keys must not be empty")
	} else {
		for _, pk := range ot.PrimaryKeys {
			prop, ok := dataPropMap[pk]
			if !ok {
				appendError(result, table, "primary_keys", "invalid_object_type",
					fmt.Sprintf("primary key %q is not a defined data property", pk))
				continue
			}
			nt := normType(prop.Type)
			if !validPrimaryKeyTypes[nt] {
				appendError(result, table, "primary_keys", "invalid_object_type",
					fmt.Sprintf("primary key %q type %q must be integer, unsigned integer, string, or text", pk, prop.Type))
			}
		}
	}

	if strings.TrimSpace(ot.DisplayKey) == "" {
		appendError(result, table, "display_key", "null_display_key", "display_key must not be empty")
	} else {
		prop, ok := dataPropMap[ot.DisplayKey]
		if !ok {
			appendError(result, table, "display_key", "invalid_object_type",
				fmt.Sprintf("display_key %q is not a defined data property", ot.DisplayKey))
		} else {
			nt := normType(prop.Type)
			if !validDisplayKeyTypes[nt] {
				appendError(result, table, "display_key", "invalid_object_type",
					fmt.Sprintf("display_key %q type %q is not valid for display", ot.DisplayKey, prop.Type))
			}
		}
	}

	if strings.TrimSpace(ot.IncrementalKey) != "" {
		prop, ok := dataPropMap[ot.IncrementalKey]
		if !ok {
			appendError(result, table, "incremental_key", "invalid_object_type",
				fmt.Sprintf("incremental_key %q is not a defined data property", ot.IncrementalKey))
		} else {
			nt := normType(prop.Type)
			if !validIncrementalKeyTypes[nt] {
				appendError(result, table, "incremental_key", "invalid_object_type",
					fmt.Sprintf("incremental_key %q type %q must be integer, datetime, or timestamp", ot.IncrementalKey, prop.Type))
			}
		}
	}

	for _, lp := range ot.LogicProperties {
		if lp == nil {
			continue
		}
		if err := validatePropertyName(lp.Name); err != nil {
			appendError(result, table, "logic_properties", "invalid_property_name", err.Error())
		}
		if msg := validateObjectName(lp.DisplayName, ""); msg != "" {
			appendError(result, table, "logic_properties", "invalid_display_name", fmt.Sprintf("logic property %q: %s", lp.Name, msg))
		}
		if strings.TrimSpace(lp.Type) != "" {
			nt := normType(lp.Type)
			if !validLogicPropertyTypes[nt] {
				appendError(result, table, "logic_properties", "invalid_object_type",
					fmt.Sprintf("logic property %q type must be metric or tool", lp.Name))
			}
		}
		if lp.DataSource != nil {
			dst := normType(lp.DataSource.Type)
			if !validLogicSourceTypes[dst] {
				appendError(result, table, "logic_properties", "invalid_object_type",
					fmt.Sprintf("logic property %q data_source.type must be metric or tool", lp.Name))
			}
			if strings.TrimSpace(lp.Type) != "" && normType(lp.Type) != dst {
				appendError(result, table, "logic_properties", "invalid_object_type",
					fmt.Sprintf("logic property %q type must match data_source.type", lp.Name))
			}
		}
		for _, p := range lp.Parameters {
			if strings.TrimSpace(p.Name) == "" {
				appendError(result, table, "logic_properties", "invalid_object_type",
					fmt.Sprintf("logic property %q has parameter with empty name", lp.Name))
			}
		}
	}
}

func validatePropertyName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("property name must not be empty")
	}
	if !propertyNamePattern.MatchString(name) {
		return fmt.Errorf("property name %q must match %s", name, propertyNamePattern.String())
	}
	return nil
}

func dataPropertyNames(ot *BknObjectType) map[string]struct{} {
	m := make(map[string]struct{})
	if ot == nil {
		return m
	}
	for _, dp := range ot.DataProperties {
		if dp == nil || strings.TrimSpace(dp.Name) == "" {
			continue
		}
		m[dp.Name] = struct{}{}
	}
	return m
}

// isMetricTimeDimPlaceholder matches example files that use an em dash or "none" when no time column exists.
func isMetricTimeDimPlaceholder(prop string) bool {
	p := strings.TrimSpace(prop)
	if p == "" || strings.EqualFold(p, "none") || strings.EqualFold(p, "n/a") {
		return true
	}
	// em dash / en dash / hyphen placeholders in tables
	if p == "—" || p == "–" || p == "-" {
		return true
	}
	return false
}

func validateMetricConditionField(result *ValidationResult, table, column, field string) {
	field = strings.TrimSpace(field)
	if field == "" {
		return
	}
	if strings.EqualFold(field, "object_type_id") {
		appendError(result, table, column, "invalid_metric_field", "metric condition.field must not be object_type_id")
		return
	}
	if err := validatePropertyName(field); err != nil {
		appendError(result, table, column, "invalid_property_name", err.Error())
	}
}

func validateMetricPropertyRef(result *ValidationResult, table, column, field string) {
	field = strings.TrimSpace(field)
	if field == "" {
		return
	}
	if field == "__value" {
		return
	}
	if err := validatePropertyName(field); err != nil {
		appendError(result, table, column, "invalid_property_name", err.Error())
	}
}

func validateMetricDeep(result *ValidationResult, table string, met *BknMetric, otByID map[string]*BknObjectType) {
	if err := validateTags(met.Tags); err != nil {
		appendError(result, table, "tags", "invalid_tags", err.Error())
	}
	if !met.HasScopeSection {
		appendError(result, table, "", "missing_section", "Metric must include a ### Scope section")
	}
	if !met.HasCalculationFormulaSection {
		appendError(result, table, "", "missing_section", "Metric must include a ### Calculation Formula section")
	}
	if met.Formula == nil {
		appendError(result, table, "Calculation Formula", "invalid_metric", "metric must contain a fenced yaml block under ### Calculation Formula")
		return
	}
	kind := normType(met.Formula.Kind)
	if kind == "" {
		appendError(result, table, "kind", "invalid_metric", "calculation formula kind is required")
		return
	}
	if kind != "atomic" {
		appendError(result, table, "kind", "unsupported_metric_kind", fmt.Sprintf("only atomic metrics are supported, got %q", met.Formula.Kind))
		return
	}
	if mt := strings.TrimSpace(met.MetricAttributes.MetricType); mt != "" && normType(mt) != kind {
		appendError(result, table, "metric_type", "metric_type_kind_mismatch", fmt.Sprintf("effective metric_type %q must match formula kind %q when both are set", met.MetricAttributes.MetricType, met.Formula.Kind))
	}
	if met.Formula.Atomic == nil {
		appendError(result, table, "atomic", "invalid_metric", "atomic kind requires non-nil atomic subtree")
		return
	}
	a := met.Formula.Atomic
	if a.Aggregation == nil || strings.TrimSpace(a.Aggregation.Property) == "" || strings.TrimSpace(a.Aggregation.Aggr) == "" {
		appendError(result, table, "aggregation", "invalid_metric", "atomic metric requires aggregation.property and aggregation.aggr")
	} else {
		validateMetricPropertyRef(result, table, "aggregation.property", a.Aggregation.Property)
		if !validMetricAggregationAggr[normType(a.Aggregation.Aggr)] {
			appendError(result, table, "aggregation.aggr", "invalid_metric_aggregation", fmt.Sprintf("unsupported aggregation %q", a.Aggregation.Aggr))
		}
	}
	if a.Condition != nil {
		validateMetricConditionField(result, table, "condition.field", a.Condition.Field)
	}
	for i, g := range a.GroupBy {
		col := fmt.Sprintf("group_by[%d].property", i)
		validateMetricPropertyRef(result, table, col, g.Property)
	}
	for i, o := range a.OrderBy {
		col := fmt.Sprintf("order_by[%d].property", i)
		validateMetricPropertyRef(result, table, col, o.Property)
	}
	if a.Having != nil {
		validateMetricPropertyRef(result, table, "having.field", a.Having.Field)
	}

	st := normType(met.ScopeType)
	sref := strings.TrimSpace(met.ScopeRef)
	if met.HasScopeSection {
		if st == "" {
			appendError(result, table, "Scope", "invalid_metric_scope_type", "Scope Type is required and must be object_type")
		} else if st != "object_type" {
			if st == "subgraph" {
				appendError(result, table, "Scope", "unsupported_metric_scope", "subgraph scope is not supported")
			} else {
				appendError(result, table, "Scope", "unsupported_metric_scope", fmt.Sprintf("only object_type scope is supported, got %q", met.ScopeType))
			}
		} else if sref == "" {
			appendError(result, table, "Scope", "invalid_metric_scope_ref", "scope_ref is required when scope type is object_type")
		} else if _, ok := otByID[sref]; !ok {
			appendError(result, table, "Scope", "invalid_metric_scope_ref", fmt.Sprintf("scope references unknown object type id %q", sref))
		}
	}

	if st == "object_type" && sref != "" {
		if ot, ok := otByID[sref]; ok {
			names := dataPropertyNames(ot)
			checkAgainstObject := func(field, col string) {
				field = strings.TrimSpace(field)
				if field == "" || field == "__value" {
					return
				}
				if _, ok := names[field]; !ok {
					appendError(result, table, col, "invalid_metric_field_ref", fmt.Sprintf("property %q is not a data property of object_type %q", field, sref))
				}
			}
			if a.Condition != nil {
				f := strings.TrimSpace(a.Condition.Field)
				if f != "" && !strings.EqualFold(f, "object_type_id") {
					checkAgainstObject(f, "condition.field")
				}
			}
			if a.Aggregation != nil {
				checkAgainstObject(a.Aggregation.Property, "aggregation.property")
			}
			for i, g := range a.GroupBy {
				checkAgainstObject(g.Property, fmt.Sprintf("group_by[%d].property", i))
			}
			for i, o := range a.OrderBy {
				checkAgainstObject(o.Property, fmt.Sprintf("order_by[%d].property", i))
			}
			if a.Having != nil {
				checkAgainstObject(a.Having.Field, "having.field")
			}
			for i, td := range met.TimeDimensions {
				if isMetricTimeDimPlaceholder(td.Property) {
					continue
				}
				checkAgainstObject(td.Property, fmt.Sprintf("time_dimension[%d].property", i))
			}
			for i, ad := range met.AnalysisDimensions {
				if err := validatePropertyName(strings.TrimSpace(ad.Name)); err != nil {
					appendError(result, table, fmt.Sprintf("analysis_dimensions[%d].name", i), "invalid_property_name", err.Error())
				}
			}
		}
	}
}

func validateRelationTypeDeep(result *ValidationResult, table string, rt *BknRelationType) {
	epType := strings.TrimSpace(rt.Endpoint.Type)
	if epType != "" && !validRelationMappingTypes[epType] {
		appendError(result, table, "endpoint.type", "invalid_relation_type",
			fmt.Sprintf("relation type must be %q, %q, or %q, got %q", RELATION_MAPPING_TYPE_DIRECT, RELATION_MAPPING_TYPE_DATA_VIEW, RELATION_MAPPING_TYPE_FILTERED_CROSS_JOIN, epType))
	}
	if strings.TrimSpace(rt.Endpoint.Source) == "" {
		appendError(result, table, "Source", "invalid_relation_type", "endpoint source_object_type_id must not be empty")
	}
	if strings.TrimSpace(rt.Endpoint.Target) == "" {
		appendError(result, table, "Target", "invalid_relation_type", "endpoint target_object_type_id must not be empty")
	}
	if epType == "" {
		return
	}
	if rt.MappingRules == nil {
		appendError(result, table, "mapping_rules", "invalid_relation_type", "mapping_rules must not be empty when endpoint type is set")
		return
	}

	switch epType {
	case RELATION_MAPPING_TYPE_DIRECT:
		rules, ok := rt.MappingRules.(DirectMappingRule)
		if !ok {
			appendError(result, table, "mapping_rules", "invalid_relation_type", "direct relation requires direct mapping rules array")
			return
		}
		if len(rules) == 0 {
			appendError(result, table, "mapping_rules", "invalid_relation_type", "direct mapping_rules must not be empty")
			return
		}
		seen := make(map[string]bool)
		for i, r := range rules {
			if strings.TrimSpace(r.SourceProperty) == "" {
				appendError(result, table, "mapping_rules", "invalid_relation_type", fmt.Sprintf("direct mapping_rules[%d] source property must not be empty", i))
			}
			if strings.TrimSpace(r.TargetProperty) == "" {
				appendError(result, table, "mapping_rules", "invalid_relation_type", fmt.Sprintf("direct mapping_rules[%d] target property must not be empty", i))
			}
			key := r.SourceProperty + ":" + r.TargetProperty
			if seen[key] {
				appendError(result, table, "mapping_rules", "invalid_relation_type", fmt.Sprintf("duplicate mapping rule %q", key))
			}
			seen[key] = true
		}
	case RELATION_MAPPING_TYPE_DATA_VIEW:
		ind, ok := rt.MappingRules.(*InDirectMappingRule)
		if !ok {
			appendError(result, table, "mapping_rules", "invalid_relation_type", "data_view relation requires InDirectMappingRule")
			return
		}
		if ind.BackingDataSource == nil {
			appendError(result, table, "mapping_rules", "invalid_relation_type", "backing_data_source must not be empty")
			return
		}
		if strings.TrimSpace(ind.BackingDataSource.Type) == "" {
			appendError(result, table, "backing_data_source", "invalid_relation_type", "backing_data_source.type must not be empty")
		} else if normType(ind.BackingDataSource.Type) != RELATION_MAPPING_TYPE_DATA_VIEW {
			appendError(result, table, "backing_data_source", "invalid_relation_type",
				fmt.Sprintf("backing_data_source.type must be %q", RELATION_MAPPING_TYPE_DATA_VIEW))
		}
		if strings.TrimSpace(ind.BackingDataSource.ID) == "" {
			appendError(result, table, "backing_data_source", "invalid_relation_type", "backing_data_source.id must not be empty")
		}
		if len(ind.SourceMappingRules) == 0 {
			appendError(result, table, "source_mapping_rules", "invalid_relation_type", "source_mapping_rules must not be empty")
		}
		seenS := make(map[string]bool)
		for i, r := range ind.SourceMappingRules {
			if strings.TrimSpace(r.SourceProperty) == "" || strings.TrimSpace(r.TargetProperty) == "" {
				appendError(result, table, "source_mapping_rules", "invalid_relation_type", fmt.Sprintf("source_mapping_rules[%d] properties must not be empty", i))
			}
			key := r.SourceProperty + ":" + r.TargetProperty
			if seenS[key] {
				appendError(result, table, "source_mapping_rules", "invalid_relation_type", fmt.Sprintf("duplicate mapping %q", key))
			}
			seenS[key] = true
		}
		if len(ind.TargetMappingRules) == 0 {
			appendError(result, table, "target_mapping_rules", "invalid_relation_type", "target_mapping_rules must not be empty")
		}
		seenT := make(map[string]bool)
		for i, r := range ind.TargetMappingRules {
			if strings.TrimSpace(r.SourceProperty) == "" || strings.TrimSpace(r.TargetProperty) == "" {
				appendError(result, table, "target_mapping_rules", "invalid_relation_type", fmt.Sprintf("target_mapping_rules[%d] properties must not be empty", i))
			}
			key := r.SourceProperty + ":" + r.TargetProperty
			if seenT[key] {
				appendError(result, table, "target_mapping_rules", "invalid_relation_type", fmt.Sprintf("duplicate mapping %q", key))
			}
			seenT[key] = true
		}
	case RELATION_MAPPING_TYPE_FILTERED_CROSS_JOIN:
		fcj, ok := rt.MappingRules.(*FilteredCrossJoinMapping)
		if !ok {
			appendError(result, table, "mapping_rules", "invalid_relation_type", "filtered_cross_join relation requires FilteredCrossJoinMapping")
			return
		}
		if fcj.SourceCondition == nil {
			appendError(result, table, "source_condition", "invalid_relation_type", "source_condition must not be empty")
		}
		if fcj.TargetCondition == nil {
			appendError(result, table, "target_condition", "invalid_relation_type", "target_condition must not be empty")
		}
	}
}

func validateActionTypeDeep(result *ValidationResult, table string, at *BknActionType) {
	atKind := strings.TrimSpace(at.ActionType)
	intentKind := strings.TrimSpace(at.ActionIntent)
	if atKind != "" && !validActionKinds[strings.ToLower(atKind)] {
		appendError(result, table, "action_type", "invalid_action_type",
			fmt.Sprintf("action_type must be one of add, modify, delete, got %q", at.ActionType))
	}
	if intentKind != "" && !validActionKinds[strings.ToLower(intentKind)] {
		appendError(result, table, "action_intent", "invalid_action_intent",
			fmt.Sprintf("action_intent must be one of add, modify, delete, got %q", at.ActionIntent))
	}

	bound := strings.TrimSpace(at.BoundObject)
	if bound == "" {
		if at.TriggerCondition != nil {
			appendError(result, table, "trigger_condition", "invalid_action_type", "when bound object is empty, trigger condition must be empty")
		}
		for _, p := range at.Parameters {
			if strings.EqualFold(strings.TrimSpace(p.ValueFrom), valueFromProperty) {
				appendError(result, table, "parameters", "invalid_action_type", "when bound object is empty, parameter value_from must not be property")
				break
			}
		}
	}

	if at.ActionSource != nil && strings.TrimSpace(at.ActionSource.Type) != "" {
		t := strings.TrimSpace(at.ActionSource.Type)
		if !validActionSourceTypes[strings.ToLower(t)] {
			appendError(result, table, "action_source", "invalid_action_type",
				fmt.Sprintf("action_source.type must be tool or mcp, got %q", at.ActionSource.Type))
		} else {
			switch strings.ToLower(t) {
			case "tool":
				if strings.TrimSpace(at.ActionSource.McpID) != "" || strings.TrimSpace(at.ActionSource.ToolName) != "" {
					appendError(result, table, "action_source", "invalid_action_type", "tool type must not set mcp_id or tool_name")
				}
			case "mcp":
				if strings.TrimSpace(at.ActionSource.BoxID) != "" || strings.TrimSpace(at.ActionSource.ToolID) != "" {
					appendError(result, table, "action_source", "invalid_action_type", "mcp type must not set box_id or tool_id")
				}
			}
		}
	}

	for _, p := range at.Parameters {
		if strings.TrimSpace(p.Name) == "" {
			appendError(result, table, "parameters", "invalid_action_type", "parameter name must not be empty")
		}
	}

	if at.TriggerCondition != nil {
		validateActionCondition(result, table, at.TriggerCondition, 0)
	}
}

func validateActionCondition(result *ValidationResult, table string, cfg *ActionCondCfg, depth int) {
	if cfg == nil {
		return
	}
	op := strings.TrimSpace(strings.ToLower(cfg.Operation))
	if op == "" {
		appendError(result, table, "condition", "invalid_action_condition", "operation must not be empty")
		return
	}
	if op == "and" || op == "or" {
		if len(cfg.SubConds) > actionCondMaxSub {
			appendError(result, table, "condition", "invalid_action_condition", fmt.Sprintf("too many sub conditions (max %d)", actionCondMaxSub))
			return
		}
		for _, sub := range cfg.SubConds {
			validateActionCondition(result, table, sub, depth+1)
		}
		return
	}
	if !actionCondOps[op] {
		appendError(result, table, "condition", "invalid_action_condition", fmt.Sprintf("unsupported operation %q", cfg.Operation))
	}
	if strings.TrimSpace(cfg.Field) == "" {
		appendError(result, table, "condition", "invalid_action_condition", "field must not be empty for leaf condition")
	}
}

func tableName(kind, id string) string {
	if strings.TrimSpace(id) == "" {
		return kind + ":<unknown>"
	}
	return fmt.Sprintf("%s:%s", kind, id)
}

func validateDefFrontmatter(result *ValidationResult, table, typ, id, name string) {
	if strings.TrimSpace(typ) == "" {
		appendError(result, table, "type", "missing_frontmatter_field", "frontmatter 'type' is required")
	}
	if strings.TrimSpace(id) == "" {
		appendError(result, table, "id", "missing_frontmatter_field", "frontmatter 'id' is required")
	}
	if strings.TrimSpace(name) == "" {
		appendError(result, table, "name", "missing_frontmatter_field", "frontmatter 'name' is required")
	}
	if strings.TrimSpace(id) != "" {
		if err := validateIDString(id); err != "" {
			appendError(result, table, "id", "invalid_id", err)
		}
	}
	if strings.TrimSpace(name) != "" {
		if err := validateObjectName(name, ""); err != "" {
			appendError(result, table, "name", "invalid_name", err)
		}
	}
}
