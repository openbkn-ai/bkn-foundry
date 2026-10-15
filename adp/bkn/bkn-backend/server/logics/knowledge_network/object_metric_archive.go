// Copyright openbkn.ai

package knowledge_network

import (
	"fmt"
	"sort"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

func validateObjectMetricArchive(plan *NormalizedImportPlan) error {
	if len(plan.ObjectMetrics) == 0 {
		return nil
	}
	owners := make(map[string]struct{}, len(plan.ObjectTypes))
	for _, objectType := range plan.ObjectTypes {
		if objectType != nil {
			owners[objectType.OTID] = struct{}{}
		}
	}
	relations := make(map[string]*interfaces.RelationType, len(plan.RelationTypes))
	for _, relation := range plan.RelationTypes {
		if relation != nil {
			relations[relation.RTID] = relation
		}
	}
	versions := make(map[string]*interfaces.ObjectMetricRecordV1, len(plan.ObjectMetrics))
	edges := make(map[string][]string, len(plan.ObjectMetrics))
	codes := make(map[string]string)
	identities := make(map[string]string)
	for _, record := range plan.ObjectMetrics {
		if record == nil {
			return fmt.Errorf("object metric archive contains a null record")
		}
		def := &record.Definition
		if strings.TrimSpace(def.ID) == "" || def.Version < 1 || strings.TrimSpace(def.Code) == "" ||
			strings.TrimSpace(def.Name) == "" || strings.TrimSpace(def.OwnerObjectTypeID) == "" {
			return fmt.Errorf("object metric archive contains an incomplete definition")
		}
		if _, ok := owners[def.OwnerObjectTypeID]; !ok {
			return fmt.Errorf("object metric %s owner object type %s is missing from the archive", def.ID, def.OwnerObjectTypeID)
		}
		switch def.Lifecycle.Status {
		case "draft", "validated", "published", "deprecated":
		default:
			return fmt.Errorf("object metric %s has invalid lifecycle status %q", def.ID, def.Lifecycle.Status)
		}
		key := fmt.Sprintf("%s@%d", def.ID, def.Version)
		if _, exists := versions[key]; exists {
			return fmt.Errorf("duplicate object metric version %s", key)
		}
		versions[key] = record
		if id, exists := codes[def.Code]; exists && id != def.ID {
			return fmt.Errorf("object metric code %s is used by multiple IDs", def.Code)
		}
		codes[def.Code] = def.ID
		identity := def.Code + "\x00" + def.OwnerObjectTypeID + "\x00" + def.MetricType
		if previous, exists := identities[def.ID]; exists && previous != identity {
			return fmt.Errorf("object metric %s changes immutable identity between versions", def.ID)
		}
		identities[def.ID] = identity
	}
	for _, record := range plan.ObjectMetrics {
		def := &record.Definition
		metricKey := fmt.Sprintf("%s@%d", def.ID, def.Version)
		// Drafts can intentionally contain unresolved references while being edited.
		// Published definitions must be self-contained in a transferable archive.
		if def.Lifecycle.Status != "published" && def.Lifecycle.Status != "deprecated" {
			continue
		}
		for _, objectTypeID := range objectMetricReferencedObjectTypes(def) {
			if _, ok := owners[objectTypeID]; !ok {
				return fmt.Errorf("object metric %s@%d references object type %s missing from the archive",
					def.ID, def.Version, objectTypeID)
			}
		}
		if qualifiers := def.Specification.BusinessQualifiers; qualifiers != nil && qualifiers.RelationPath != nil {
			if err := validateArchiveMetricRelationPath(def.OwnerObjectTypeID, "",
				qualifiers.RelationPath.Steps, relations); err != nil {
				return fmt.Errorf("object metric %s relation path: %w", metricKey, err)
			}
		}
		for _, dependency := range def.Specification.Dependencies {
			if len(dependency.ObjectAlignment.RelationPath) == 0 {
				continue
			}
			key := fmt.Sprintf("%s@%d", dependency.MetricRef.MetricID, dependency.MetricRef.Version)
			target := ""
			if referenced := versions[key]; referenced != nil {
				target = referenced.Definition.OwnerObjectTypeID
			}
			if err := validateArchiveMetricRelationPath(def.OwnerObjectTypeID, target,
				dependency.ObjectAlignment.RelationPath, relations); err != nil {
				return fmt.Errorf("object metric %s dependency %s relation path: %w", metricKey, key, err)
			}
		}
		refs := make([]interfaces.ObjectMetricRefV1, 0, len(def.Specification.Dependencies)+1)
		if def.Specification.BaseMetric != nil {
			refs = append(refs, *def.Specification.BaseMetric)
		}
		for _, dependency := range def.Specification.Dependencies {
			refs = append(refs, dependency.MetricRef)
		}
		for _, ref := range refs {
			key := fmt.Sprintf("%s@%d", ref.MetricID, ref.Version)
			edges[metricKey] = append(edges[metricKey], key)
			dependency, ok := versions[key]
			if !ok {
				return fmt.Errorf("object metric %s@%d references missing version %s", def.ID, def.Version, key)
			}
			if def.Lifecycle.Status == "published" || def.Lifecycle.Status == "deprecated" {
				status := dependency.Definition.Lifecycle.Status
				if status != "published" && status != "deprecated" {
					return fmt.Errorf("published object metric %s@%d references %s in status %s", def.ID, def.Version, key, status)
				}
			}
		}
	}
	visiting := make(map[string]bool, len(versions))
	visited := make(map[string]bool, len(versions))
	var visit func(string) error
	visit = func(key string) error {
		if visiting[key] {
			return fmt.Errorf("object metric dependency cycle includes %s", key)
		}
		if visited[key] {
			return nil
		}
		visiting[key] = true
		for _, dependency := range edges[key] {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		visiting[key] = false
		visited[key] = true
		return nil
	}
	for key := range versions {
		if err := visit(key); err != nil {
			return err
		}
	}
	return nil
}

func validateArchiveMetricRelationPath(source, expectedTarget string, steps []interfaces.ObjectMetricRelationStepV1,
	relations map[string]*interfaces.RelationType) error {
	current := source
	for _, step := range steps {
		relation := relations[step.RelationTypeID]
		if relation == nil {
			return fmt.Errorf("relation type %s is missing from the archive", step.RelationTypeID)
		}
		next := ""
		switch {
		case step.Direction == "outgoing" && relation.SourceObjectTypeID == current:
			next = relation.TargetObjectTypeID
		case step.Direction == "incoming" && relation.TargetObjectTypeID == current:
			next = relation.SourceObjectTypeID
		default:
			return fmt.Errorf("relation type %s is disconnected from object type %s", step.RelationTypeID, current)
		}
		if step.TargetObjectTypeID != next {
			return fmt.Errorf("relation type %s target object type does not match the archive", step.RelationTypeID)
		}
		current = next
	}
	if expectedTarget != "" && current != expectedTarget {
		return fmt.Errorf("relation path ends at %s instead of %s", current, expectedTarget)
	}
	return nil
}

func objectMetricReferencedObjectTypes(def *interfaces.ObjectMetricDefinitionV1) []string {
	if def == nil {
		return nil
	}
	ids := make(map[string]struct{})
	addID := func(id string) {
		if id != "" {
			ids[id] = struct{}{}
		}
	}
	addRef := func(ref *interfaces.ObjectMetricPropertyRef) {
		if ref != nil {
			addID(ref.ObjectTypeID)
		}
	}
	addSteps := func(steps []interfaces.ObjectMetricRelationStepV1) {
		for _, step := range steps {
			addID(step.TargetObjectTypeID)
		}
	}
	var addExpression func(*interfaces.ObjectMetricExpressionV1)
	addExpression = func(expr *interfaces.ObjectMetricExpressionV1) {
		if expr == nil {
			return
		}
		addRef(expr.PropertyRef)
		for i := range expr.Arguments {
			addExpression(&expr.Arguments[i])
		}
		addExpression(expr.Left)
		addExpression(expr.Right)
	}
	var addCondition func(*interfaces.ObjectMetricConditionV1)
	addCondition = func(condition *interfaces.ObjectMetricConditionV1) {
		if condition == nil {
			return
		}
		if condition.Left != nil {
			addRef(condition.Left.PropertyRef)
		}
		for i := range condition.Right {
			addRef(condition.Right[i].PropertyRef)
		}
		for i := range condition.Children {
			addCondition(&condition.Children[i])
		}
	}
	addID(def.OwnerObjectTypeID)
	for i := range def.ResultSemantics.StatisticalGrain.GroupByDimensions {
		addRef(&def.ResultSemantics.StatisticalGrain.GroupByDimensions[i])
	}
	for i := range def.QueryCapabilities.AnalysisDimensions {
		addRef(&def.QueryCapabilities.AnalysisDimensions[i])
	}
	for i := range def.QueryCapabilities.TimeDimensions {
		addRef(&def.QueryCapabilities.TimeDimensions[i])
	}
	spec := &def.Specification
	if spec.Measure != nil {
		addExpression(spec.Measure.Expression)
	}
	addExpression(spec.Expression)
	if spec.BusinessQualifiers != nil {
		qualifiers := spec.BusinessQualifiers
		addCondition(qualifiers.Filters)
		addRef(qualifiers.EventTime)
		if qualifiers.RelationPath != nil {
			addSteps(qualifiers.RelationPath.Steps)
			addCondition(qualifiers.RelationPath.RelatedFilters)
		}
	}
	for _, dependency := range spec.Dependencies {
		addSteps(dependency.ObjectAlignment.RelationPath)
		addRef(dependency.EvaluationContext.FilterContext.Dimension)
	}
	if spec.Alignment != nil {
		addID(spec.Alignment.ResultOwnerObjectTypeID)
		for i := range spec.Alignment.GroupByDimensions {
			addRef(&spec.Alignment.GroupByDimensions[i])
		}
		addRef(spec.Alignment.TimeKey)
	}
	result := make([]string, 0, len(ids))
	for id := range ids {
		result = append(result, id)
	}
	sort.Strings(result)
	return result
}
