// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package semantic_understanding_task

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/bytedance/sonic"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

func skippedApplyResult(detail interfaces.SemanticUnderstandingSkippedApplyDetail) (*interfaces.SemanticUnderstandingApplyResult, error) {
	detailStr, err := sonic.MarshalString(detail)
	if err != nil {
		return nil, fmt.Errorf("marshal semantic understanding skipped apply detail failed: %w", err)
	}
	return &interfaces.SemanticUnderstandingApplyResult{
		Applied:    false,
		DetailJSON: detailStr,
	}, nil
}

// ApplyResourceSemanticResult 将已校验的语义结果投影到调用方提供的资源，并通过同一事务保存。
func ApplyResourceSemanticResult(ctx context.Context, tx *sql.Tx, rs interfaces.ResourceService,
	task *interfaces.SemanticUnderstandingTask, resultJSON string, resourceInfo *interfaces.Resource) (*interfaces.SemanticUnderstandingApplyResult, error) {
	if task.ResourceID == "" {
		return nil, fmt.Errorf("resource_id is required for resource semantic understanding task")
	}

	var result interfaces.SemanticUnderstandingResourceResult
	if err := sonic.Unmarshal([]byte(resultJSON), &result); err != nil {
		return nil, fmt.Errorf("unmarshal resource semantic understanding result failed: %w", err)
	}
	if err := validateConfidence(result.Resource.Confidence, "resource.confidence"); err != nil {
		return nil, err
	}
	if utf8.RuneCountInString(result.Resource.DisplayName) > interfaces.NAME_MAX_LENGTH {
		return nil, fmt.Errorf("resource display_name exceeds max length")
	}

	fieldByName := make(map[string]*interfaces.Property, len(resourceInfo.SchemaDefinition))
	for _, property := range resourceInfo.SchemaDefinition {
		if property != nil {
			fieldByName[property.Name] = property
		}
	}
	displayNameCandidates, acceptedDisplayNames := planSemanticDisplayNameUpdates(task, result.Fields, resourceInfo.SchemaDefinition, fieldByName)

	seenFields := make(map[string]struct{}, len(result.Fields))
	updatedFields := make([]string, 0)
	skippedFields := make([]string, 0)
	fieldDetails := make([]interfaces.SemanticUnderstandingFieldApplyDetail, 0, len(result.Fields))
	for _, field := range result.Fields {
		if field.Name == "" {
			skippedFields = append(skippedFields, "<empty>: missing name")
			fieldDetails = append(fieldDetails, interfaces.SemanticUnderstandingFieldApplyDetail{Name: field.Name, Status: "skipped", Reasons: []string{"missing name"}})
			continue
		}
		if _, ok := seenFields[field.Name]; ok {
			skippedFields = append(skippedFields, fmt.Sprintf("%s: duplicate", field.Name))
			fieldDetails = append(fieldDetails, interfaces.SemanticUnderstandingFieldApplyDetail{Name: field.Name, Status: "skipped", Reasons: []string{"duplicate"}})
			continue
		}
		seenFields[field.Name] = struct{}{}

		property, ok := fieldByName[field.Name]
		if !ok {
			skippedFields = append(skippedFields, fmt.Sprintf("%s: not found", field.Name))
			fieldDetails = append(fieldDetails, interfaces.SemanticUnderstandingFieldApplyDetail{Name: field.Name, Status: "skipped", Reasons: []string{"not found"}})
			continue
		}
		displayName := strings.TrimSpace(field.DisplayName)
		if utf8.RuneCountInString(displayName) > interfaces.MaxLength_PropertyDisplayName {
			skippedFields = append(skippedFields, fmt.Sprintf("%s: display_name exceeds max length", field.Name))
			fieldDetails = append(fieldDetails, interfaces.SemanticUnderstandingFieldApplyDetail{Name: field.Name, Status: "skipped", Reasons: []string{"display_name exceeds max length"}})
			continue
		}
		// Only a missing display name is absent. Whitespace-only and
		// punctuation-only values are present but not meaningful semantic names,
		// so they must be rejected even in force mode rather than being allowed
		// to overwrite an existing display name.
		missingDisplayName := field.DisplayName == ""
		invalidDisplayName := !missingDisplayName && IsTechnicalFieldName(field.Name, field.DisplayName)
		reasons := make([]string, 0, 1)
		if invalidDisplayName {
			skippedFields = append(skippedFields, fmt.Sprintf("%s: display_name equals technical field name", field.Name))
			reasons = append(reasons, "display_name equals technical field name")
		}
		if utf8.RuneCountInString(field.Description) > interfaces.MaxLength_PropertyDescription {
			skippedFields = append(skippedFields, fmt.Sprintf("%s: description exceeds max length", field.Name))
			fieldDetails = append(fieldDetails, interfaces.SemanticUnderstandingFieldApplyDetail{Name: field.Name, Status: "skipped", Reasons: []string{"description exceeds max length"}})
			continue
		}
		if err := validateConfidence(field.Confidence, fmt.Sprintf("fields[%s].confidence", field.Name)); err != nil {
			skippedFields = append(skippedFields, fmt.Sprintf("%s: invalid confidence", field.Name))
			fieldDetails = append(fieldDetails, interfaces.SemanticUnderstandingFieldApplyDetail{Name: field.Name, Status: "skipped", Reasons: []string{"invalid confidence"}})
			continue
		}
		if field.Confidence != nil && *field.Confidence < task.ConfidenceThreshold {
			skippedFields = append(skippedFields, fmt.Sprintf("%s: confidence below threshold", field.Name))
			fieldDetails = append(fieldDetails, interfaces.SemanticUnderstandingFieldApplyDetail{Name: field.Name, Status: "skipped", Reasons: []string{"confidence below threshold"}})
			continue
		}

		updated := make([]string, 0, 2)
		if candidateDisplayName, hasCandidate := displayNameCandidates[field.Name]; hasCandidate {
			if !acceptedDisplayNames[field.Name] {
				skippedFields = append(skippedFields, fmt.Sprintf("%s: display_name duplicates another field", field.Name))
				reasons = append(reasons, "display_name duplicates another field")
			} else {
				property.DisplayName = candidateDisplayName
				updated = append(updated, "display_name")
			}
		}
		if applyStringByMode(task.ApplyMode, &property.Description, field.Description, property.Description == property.OriginalDescription) {
			updated = append(updated, "description")
		}
		status := "unchanged"
		if len(updated) > 0 {
			status = "updated"
			if len(reasons) > 0 {
				status = "partial"
			}
			updatedFields = append(updatedFields, field.Name)
		}
		fieldDetails = append(fieldDetails, interfaces.SemanticUnderstandingFieldApplyDetail{Name: field.Name, Status: status, Updated: updated, Reasons: reasons})
	}

	updatedResource := make([]string, 0, 2)
	if applyStringByMode(task.ApplyMode, &resourceInfo.Name, result.Resource.DisplayName,
		isDefaultResourceName(resourceInfo)) {
		updatedResource = append(updatedResource, "name")
	}
	if applyStringByMode(task.ApplyMode, &resourceInfo.Description, result.Resource.Description,
		resourceInfo.Description == SourceOriginalDescription(resourceInfo.SourceMetadata)) {
		updatedResource = append(updatedResource, "description")
	}
	resourceUpdated := len(updatedResource) > 0
	if !resourceUpdated && len(updatedFields) == 0 {
		if len(skippedFields) > 0 {
			detailStr, err := sonic.MarshalString(
				interfaces.SemanticUnderstandingResourceApplyDetail{
					SkippedFields: skippedFields,
					FieldDetails:  fieldDetails,
				})
			if err != nil {
				return nil, fmt.Errorf("marshal resource semantic understanding apply detail failed: %w", err)
			}
			return &interfaces.SemanticUnderstandingApplyResult{
				Applied:    false,
				DetailJSON: detailStr,
			}, nil
		}
		return skippedApplyResult(interfaces.SemanticUnderstandingSkippedApplyDetail{
			Reason:    "no_resource_changes",
			ApplyMode: task.ApplyMode,
			Scope:     task.Scope,
		})
	}

	expectedUpdateTime := resourceInfo.UpdateTime
	resourceInfo.Updater = task.Creator
	resourceInfo.UpdateTime = time.Now().UnixMilli()
	err := rs.InternalUpdateSemanticMetadata(ctx, tx, resourceInfo, expectedUpdateTime)
	if err != nil {
		return nil, err
	}

	detailStr, err := sonic.MarshalString(interfaces.SemanticUnderstandingResourceApplyDetail{
		ResourceUpdated: resourceUpdated,
		UpdatedResource: updatedResource,
		UpdatedFields:   updatedFields,
		SkippedFields:   skippedFields,
		FieldDetails:    fieldDetails,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal resource semantic understanding apply detail failed: %w", err)
	}

	return &interfaces.SemanticUnderstandingApplyResult{
		Applied:    true,
		DetailJSON: detailStr,
	}, nil
}

// planSemanticDisplayNameUpdates checks the names that would exist after the whole
// result is applied, so a rename can reuse a name freed by another field.
func planSemanticDisplayNameUpdates(task *interfaces.SemanticUnderstandingTask,
	fields []interfaces.SemanticUnderstandingResourceResultField, schema []*interfaces.Property,
	fieldByName map[string]*interfaces.Property) (map[string]string, map[string]bool) {
	candidates := make(map[string]string, len(fields))
	accepted := make(map[string]bool, len(fields))
	order := make([]string, 0, len(fields))
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		if field.Name == "" {
			continue
		}
		if _, duplicate := seen[field.Name]; duplicate {
			continue
		}
		seen[field.Name] = struct{}{}
		property := fieldByName[field.Name]
		displayName := strings.TrimSpace(field.DisplayName)
		if property == nil || utf8.RuneCountInString(displayName) > interfaces.MaxLength_PropertyDisplayName ||
			(field.DisplayName != "" && IsTechnicalFieldName(field.Name, field.DisplayName)) ||
			utf8.RuneCountInString(field.Description) > interfaces.MaxLength_PropertyDescription ||
			validateConfidence(field.Confidence, "") != nil ||
			(field.Confidence != nil && *field.Confidence < task.ConfidenceThreshold) {
			continue
		}
		candidate := property.DisplayName
		if !applyStringByMode(task.ApplyMode, &candidate, displayName, property.DisplayName == property.Name) {
			continue
		}
		candidates[field.Name] = candidate
		accepted[field.Name] = true
		order = append(order, field.Name)
	}

	counts := make(map[string]int, len(schema))
	for _, property := range schema {
		if property == nil {
			continue
		}
		counts[strings.TrimSpace(property.DisplayName)]++
	}
	for _, name := range order {
		counts[strings.TrimSpace(fieldByName[name].DisplayName)]--
		counts[strings.TrimSpace(candidates[name])]++
	}
	// Reject later suggestions first when several final names collide. Repeat
	// because rejecting one rename may restore a name needed by another.
	for changed := true; changed; {
		changed = false
		for i := len(order) - 1; i >= 0; i-- {
			name := order[i]
			candidateKey := strings.TrimSpace(candidates[name])
			if !accepted[name] || counts[candidateKey] <= 1 {
				continue
			}
			accepted[name] = false
			counts[candidateKey]--
			counts[strings.TrimSpace(fieldByName[name].DisplayName)]++
			changed = true
		}
	}
	return candidates, accepted
}

func applyStringByMode(mode string, current *string, next string, treatCurrentAsEmpty bool) bool {
	if next == "" {
		return false
	}
	switch mode {
	case interfaces.SemanticUnderstandingApplyModeFillEmpty:
		if *current != "" && !treatCurrentAsEmpty {
			return false
		}
	case interfaces.SemanticUnderstandingApplyModeForce:
	default:
		return false
	}
	if *current == next {
		return false
	}
	*current = next
	return true
}

// isTechnicalFieldName reports whether displayName is empty or merely a formatting
// variation of the technical field name. Such a value is not a semantic enhancement
// and must never overwrite a business display name, including in force mode.
func IsTechnicalFieldName(name, displayName string) bool {
	normalizedDisplayName := normalizeSemanticFieldName(displayName)
	return normalizedDisplayName == "" || normalizedDisplayName == normalizeSemanticFieldName(name)
}

func normalizeSemanticFieldName(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, value)
}

// isDefaultResourceName 识别发现生成的默认名称，保留人工设置的业务名称。
func isDefaultResourceName(resource *interfaces.Resource) bool {
	if resource.Name == resource.SourceIdentifier {
		return true
	}
	for _, key := range []string{"name", "original_name"} {
		original, _ := resource.SourceMetadata[key].(string)
		if original != "" && resource.Name == original {
			return true
		}
	}
	// 旧表名包含 namespace；刷新后 original_name 已被完整 TableMeta 替代。
	if resource.Category == interfaces.ResourceCategoryTable {
		name, _ := resource.SourceMetadata["name"].(string)
		namespace, _ := resource.SourceMetadata["schema"].(string)
		if namespace == "" {
			namespace, _ = resource.SourceMetadata["database"].(string)
		}
		if name != "" && namespace != "" {
			unqualified, qualified := strings.CutPrefix(resource.Name, namespace+".")
			return qualified && unqualified == name
		}
	}
	return false
}

func SourceOriginalDescription(metadata map[string]any) string {
	if metadata == nil {
		return ""
	}
	if description, ok := metadata["description"].(string); ok {
		return description
	}
	// 文件集等资源仍使用 original_description。
	description, _ := metadata["original_description"].(string)
	return description
}

func validateConfidence(confidence *float64, path string) error {
	if confidence == nil {
		return nil
	}
	if *confidence < 0 || *confidence > 1 {
		return fmt.Errorf("%s must be between 0 and 1", path)
	}
	return nil
}
