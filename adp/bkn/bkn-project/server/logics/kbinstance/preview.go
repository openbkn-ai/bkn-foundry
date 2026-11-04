// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbinstance

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strings"

	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbdataset"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
)

var ErrAcceptanceHidden = errors.New("acceptance samples expose scores only")
var previewField = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)

type ResultField struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	Type  string `json:"type"`
}
type ResultGroup struct {
	ID     string        `json:"id"`
	Name   string        `json:"name"`
	Kind   string        `json:"kind"`
	Fields []ResultField `json:"fields"`
}
type ResultPage struct {
	Groups  []ResultGroup    `json:"groups"`
	Group   string           `json:"group"`
	Entries []map[string]any `json:"entries"`
	HasMore bool             `json:"has_more"`
}
type previewGroup struct {
	ResultGroup
	spec                   interfaces.DraftDatasetSpec
	object, source, target *interfaces.DraftObjectType
}

func resultLabel(ctx context.Context, key, fallback string) string {
	label, err := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknProject.KnowledgeBase."+key, nil)
	if err != nil {
		return fallback
	}
	return label
}
func previewGroups(ctx context.Context, draft *interfaces.Draft, plan *interfaces.DraftDatasetPlan) []previewGroup {
	groups := []previewGroup{}
	objects := map[string]*interfaces.DraftObjectType{}
	for _, object := range draft.Items.ObjectTypes {
		if object != nil {
			objects[object.ID] = object
		}
	}
	for _, spec := range plan.Datasets {
		if !spec.Ready {
			continue
		}
		if object := objects[spec.ObjectTypeID]; object != nil && object.Adopted && object.Document != nil && !strings.HasPrefix(object.Document.Role, "evidence_") && object.Document.Role != "entity" {
			group := previewGroup{ResultGroup: ResultGroup{ID: "object:" + object.ID, Name: object.Name, Kind: "object", Fields: []ResultField{}}, spec: spec, object: object}
			for _, p := range object.Properties {
				if ordinaryInstanceProperty(object, p) || (p != nil && (p.Name == "status" || p.Name == "recorded_at")) {
					group.Fields = append(group.Fields, ResultField{Name: p.Name, Label: p.DisplayName, Type: p.Type})
				}
			}
			groups = append(groups, group)
		}
		for _, relation := range draft.Items.RelationTypes {
			if relation == nil || relation.ID != spec.RelationTypeID || !relation.Adopted || relation.Document == nil {
				continue
			}
			object, target := objects[relation.Source], objects[relation.Target]
			if relation.Document.Role == "evidence_reference" && object != nil && object.Document != nil && object.Document.Role == "entity" && target != nil && target.Document != nil && target.Document.Role == "evidence_document" {
				groups = append(groups, previewGroup{ResultGroup: ResultGroup{ID: "claims:" + object.ID, Name: object.Name, Kind: "claims", Fields: []ResultField{{Name: "business_identity", Label: resultLabel(ctx, "ResultIdentity", "Business number"), Type: "json"}, {Name: "property_name", Label: resultLabel(ctx, "ResultAttribute", "Attribute"), Type: "string"}, {Name: "claim_value", Label: resultLabel(ctx, "ResultValue", "Source value"), Type: "json"}}}, spec: spec, object: object})
			}
			if relation.Document.Role == "relation" {
				groups = append(groups, previewGroup{ResultGroup: ResultGroup{ID: "relation:" + relation.ID, Name: relation.Name, Kind: "relation", Fields: []ResultField{{Name: "source_identity", Label: resultLabel(ctx, "ResultSource", "Source object"), Type: "json"}, {Name: "target_identity", Label: resultLabel(ctx, "ResultTarget", "Target object"), Type: "json"}}}, spec: spec, source: object, target: target})
			}
		}
	}
	slices.SortFunc(groups, func(a, b previewGroup) int {
		if a.Kind != b.Kind {
			return strings.Compare(a.Kind, b.Kind)
		}
		return strings.Compare(a.Name, b.Name)
	})
	return groups
}
func previewScope(spec interfaces.DraftDatasetSpec, build *interfaces.KBBuild, fileKey string) (map[string]interfaces.KBResourceFile, error) {
	for _, logical := range []string{"model_fingerprint", "document_id", "content_version_id"} {
		if !previewField.MatchString(spec.Layout.Mapping[logical]) {
			return nil, ErrInvalidBuild
		}
	}
	files := map[string]interfaces.KBResourceFile{}
	for _, step := range build.Files {
		file := step.File
		if fileKey != "" && file.Key() != fileKey {
			continue
		}
		if file.SampleRole == "acceptance" {
			if fileKey != "" {
				return nil, ErrAcceptanceHidden
			}
			continue
		}
		if step.State != "succeeded" {
			continue
		}
		id, err := DocumentID(file.KnowledgeBaseID, file.FileID)
		if err != nil {
			return nil, err
		}
		files[id] = file
	}
	if len(files) > 200 {
		return nil, &kb.SourceError{StatusCode: 413}
	}
	return files, nil
}

// Results reads verified owned Datasets under project ownership and current
// source permissions. Heldout contents are never exposed by this endpoint.
func (s *BuildService) Results(ctx context.Context, token, project, id, groupID, fileKey string, offset int, caller interfaces.AccountInfo, _ interfaces.VegaAccess) (*ResultPage, error) {
	if _, err := s.owner(ctx, token, project, caller); err != nil {
		return nil, err
	}
	if s.Drafts == nil || offset < 0 || offset > 9900 {
		return nil, &kb.SourceError{StatusCode: 400}
	}
	r, err := s.Store.Get(ctx, project, id)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, &kb.SourceError{StatusCode: 404}
	}
	if r.Build.Mode == "acceptance" {
		return nil, ErrAcceptanceHidden
	}
	files := []interfaces.KBResourceFile{}
	for _, file := range r.Build.Files {
		files = append(files, file.File)
	}
	if err = s.sourcePermission(ctx, token, files); err != nil {
		return nil, err
	}
	draft, found, err := s.Drafts.GetDraft(ctx, project)
	if err != nil {
		return nil, err
	}
	if !found || draft == nil {
		return nil, ErrBuildChanged
	}
	fingerprint, err := kbdataset.ModelFingerprint(draft)
	if err != nil || fingerprint != r.Build.ModelFingerprint {
		return nil, ErrBuildChanged
	}
	var plan *interfaces.DraftDatasetPlan
	for _, candidate := range draft.Items.DatasetPlans {
		if candidate != nil && candidate.ID == r.Build.GenerationID && candidate.ModelFingerprint == fingerprint && candidate.Owner == caller && candidate.State == "ready" {
			plan = candidate
		}
	}
	if plan == nil {
		return nil, ErrBuildChanged
	}
	groups := previewGroups(ctx, draft, plan)
	page := &ResultPage{Groups: []ResultGroup{}, Entries: []map[string]any{}}
	for _, group := range groups {
		page.Groups = append(page.Groups, group.ResultGroup)
	}
	if len(groups) == 0 {
		return page, nil
	}
	selected := groups[0]
	if groupID != "" {
		found := false
		for _, group := range groups {
			if group.ID == groupID {
				selected = group
				found = true
			}
		}
		if !found {
			return nil, &kb.SourceError{StatusCode: 404}
		}
	}
	page.Group = selected.ID
	spec := selected.spec
	if spec.ProjectID != project || spec.GenerationID != r.Build.GenerationID || spec.Owner != caller || len(spec.ResourceID) != 32 {
		return nil, ErrInvalidBuild
	}
	if _, err = hex.DecodeString(spec.ResourceID); err != nil {
		return nil, ErrInvalidBuild
	}
	scope, err := previewScope(spec, r.Build, fileKey)
	if err != nil {
		return nil, err
	}
	if len(scope) == 0 {
		return page, nil
	}
	providers, enabled := kb.Lookup()
	reader, supported := providers.Datasets.(kb.DatasetFileSnapshot)
	if !enabled || !supported {
		return nil, kb.ErrUnavailable
	}
	guard := func(ctx context.Context) error { return s.sourcePermission(ctx, token, files) }
	rows, err := previewDatasetRows(ctx, reader, spec, r.Build, scope, offset, guard)
	if err != nil {
		return nil, err
	}
	if len(rows) > 51 {
		return nil, ErrInvalidBuild
	}
	page.HasMore = len(rows) > 50
	if len(rows) > 50 {
		rows = rows[:50]
	}
	for _, row := range rows {
		document, ok := row["document_id"].(string)
		file, exists := scope[document]
		if !ok || !exists || row["content_version_id"] != file.ContentVersionID || row["model_fingerprint"] != fingerprint {
			return nil, ErrInvalidBuild
		}
		row["file_name"], row["record_id"] = file.Name, row["_id"]
		if !file.IncludeOriginal {
			row["source_quote"] = ""
			row["evidence"] = redactEvidenceQuotes(row["evidence"])
		}
		page.Entries = append(page.Entries, row)
	}
	for _, row := range page.Entries {
		switch selected.Kind {
		case "claims":
			row["business_identity"] = displayIdentity(ctx, row["source_identity"], selected.object)
			if name, ok := row["property_name"].(string); ok {
				if property := instanceProperty(selected.object, name); property != nil {
					row["property_name"] = property.DisplayName
				} else {
					return nil, ErrInvalidBuild
				}
			}
		case "relation":
			row["source_identity"] = displayIdentity(ctx, row["source_identity"], selected.source)
			row["target_identity"] = displayIdentity(ctx, row["target_identity"], selected.target)
		}
	}
	return page, nil
}

// Reuse complete native snapshots, in journaled file order and stable row order.
// Stop once one display page is available; a failed read never becomes a partial
// successful page. The provider rechecks ownership and the guard on every cursor.
func previewDatasetRows(ctx context.Context, reader kb.DatasetFileSnapshot, spec interfaces.DraftDatasetSpec, build *interfaces.KBBuild, scope map[string]interfaces.KBResourceFile, offset int, guard func(context.Context) error) ([]map[string]any, error) {
	result := []map[string]any{}
	for _, step := range build.Files {
		document, err := DocumentID(step.File.KnowledgeBaseID, step.File.FileID)
		if err != nil {
			return nil, err
		}
		file, selected := scope[document]
		if !selected || file != step.File || file.SampleRole == "acceptance" || step.State != "succeeded" {
			continue
		}
		rows, err := reader.ReadFileRows(ctx, spec, kb.DatasetFileScope{KnowledgeBaseID: file.KnowledgeBaseID, FileID: file.FileID, ContentVersionID: file.ContentVersionID, DocumentID: document, ModelFingerprint: build.ModelFingerprint}, guard)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			if id, ok := row["_id"].(string); !ok || id == "" {
				return nil, ErrInvalidBuild
			}
		}
		slices.SortFunc(rows, func(a, b map[string]any) int { return strings.Compare(a["_id"].(string), b["_id"].(string)) })
		if offset >= len(rows) {
			offset -= len(rows)
			continue
		}
		for _, original := range rows[offset:] {
			row := map[string]any{}
			for key, value := range original {
				row[key] = value
			}
			result = append(result, row)
			if len(result) == 51 {
				return result, nil
			}
		}
		offset = 0
	}
	return result, nil
}

func displayIdentity(ctx context.Context, value any, object *interfaces.DraftObjectType) map[string]any {
	if object == nil {
		return nil
	}
	if text, ok := value.(string); ok {
		var decoded map[string]any
		if json.Unmarshal([]byte(text), &decoded) == nil {
			value = decoded
		}
	}
	fields, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	labels := map[string]string{}
	for _, p := range object.Properties {
		if p != nil {
			labels[p.Name] = p.DisplayName
		}
	}
	result := map[string]any{}
	for name, v := range fields {
		label := labels[name]
		if label == "" {
			label = resultLabel(ctx, "ResultIdentity", "Business number")
		}
		result[label] = v
	}
	return result
}

// Redact only evidence metadata; ordinary business properties can legitimately
// contain text. Return a copy so a shared query result cannot retain a leak.
func redactEvidenceQuotes(value any) any {
	if text, ok := value.(string); ok {
		var decoded any
		if json.Unmarshal([]byte(text), &decoded) != nil {
			return nil
		}
		value = decoded
	}
	return redactEvidenceValue(value)
}
func redactEvidenceValue(value any) any {
	switch v := value.(type) {
	case []any:
		result := make([]any, len(v))
		for i, item := range v {
			result[i] = redactEvidenceValue(item)
		}
		return result
	case map[string]any:
		result := map[string]any{}
		for key, item := range v {
			if key == "source_quote" || key == "quote" {
				result[key] = ""
			} else {
				result[key] = redactEvidenceValue(item)
			}
		}
		return result
	default:
		return value
	}
}
