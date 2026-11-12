// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package semantic_understanding_task

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	verrors "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

type semanticSchemaField struct {
	Name                string         `json:"name"`
	Type                string         `json:"type"`
	OriginalName        string         `json:"original_name"`
	OriginalType        string         `json:"original_type"`
	OriginalDescription string         `json:"original_description"`
	Attributes          map[string]any `json:"attributes"`
}

// Apply 成功后标记结果已应用，包括无元数据变更；返回的布尔值仅表示本次实际修改了资源。
// 任务输入、原始结果与 dry-run 模式保持不变。
func (suts *semanticUnderstandingTaskService) Apply(ctx context.Context, id string, req *interfaces.ApplySemanticUnderstandingTaskRequest) (*interfaces.SemanticUnderstandingTask, bool, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "SemanticUnderstandingTaskService.Apply")
	defer span.End()
	if req == nil || (req.ApplyMode != interfaces.SemanticUnderstandingApplyModeFillEmpty && req.ApplyMode != interfaces.SemanticUnderstandingApplyModeForce) {
		return nil, false, httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_Format).
			WithErrorDetails("apply_mode must be fill_empty or force")
	}
	// 先复用详情权限与输入保护规则，避免写入成功后才发现无法构造安全响应。
	visible, err := suts.GetByID(ctx, id)
	if err != nil {
		return nil, false, err
	}
	if err := validateSemanticTaskApplicable(ctx, visible); err != nil {
		return nil, false, err
	}
	if err := suts.rs.CheckResourcePermission(ctx, visible.ResourceID, interfaces.OPERATION_TYPE_MODIFY); err != nil {
		return nil, false, err
	}
	if suts.db == nil {
		return nil, false, semanticApplyInternalError(ctx, fmt.Errorf("database is not configured"))
	}
	tx, err := suts.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, semanticApplyInternalError(ctx, err)
	}
	defer func() { _ = tx.Rollback() }()
	// 事务内普通读取，写入时通过资源版本与任务状态条件检查并发变更。
	resourceInfo, err := suts.rs.InternalGetByID(ctx, tx, visible.ResourceID)
	if err != nil {
		return nil, false, semanticApplyInternalError(ctx, err)
	}
	if resourceInfo == nil {
		return nil, false, httperrors.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Resource_NotFound)
	}
	task, err := suts.suta.GetByID(ctx, tx, id)
	if err != nil {
		return nil, false, semanticApplyInternalError(ctx, err)
	}
	if task == nil {
		return nil, false, httperrors.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_SemanticUnderstandingTask_NotFound)
	}
	if err := validateSemanticTaskApplicable(ctx, task); err != nil {
		return nil, false, err
	}
	if task.ResourceID != visible.ResourceID || task.CatalogID != visible.CatalogID {
		return nil, false, httperrors.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_SemanticUnderstandingTask_NotApplicable)
	}
	appliedNow := false
	if !task.Applied {
		if resourceInfo.Builtin && !interfaces.IsBuiltinAdmin(ctx) {
			return nil, false, httperrors.NewHTTPError(ctx, http.StatusForbidden, httperrors.PublicError_Forbidden)
		}
		if err := validateSemanticTaskSchema(task, resourceInfo); err != nil {
			return nil, false, httperrors.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_SemanticUnderstandingTask_SchemaMismatch)
		}
		if err := validateSemanticResultFields(task.ResultJSON, resourceInfo); err != nil {
			return nil, false, httperrors.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_SemanticUnderstandingTask_InvalidResult)
		}
		applicationTask := *task
		applicationTask.ApplyMode = req.ApplyMode
		applicationTask.Creator = accountInfoFromContext(ctx)
		result, err := ApplyResourceSemanticResult(ctx, tx, suts.rs, &applicationTask, task.ResultJSON, resourceInfo)
		if err != nil {
			var httpErr *httperrors.HTTPError
			if errors.As(err, &httpErr) {
				return nil, false, httpErr
			}
			return nil, false, semanticApplyInternalError(ctx, err)
		}
		var details map[string]any
		if err := common.UnmarshalPreciseJSON([]byte(result.DetailJSON), &details); err != nil {
			return nil, false, semanticApplyInternalError(ctx, err)
		}
		details["apply_mode"] = req.ApplyMode
		detailJSON, err := json.Marshal(details)
		if err != nil {
			return nil, false, semanticApplyInternalError(ctx, err)
		}
		updated, err := suts.suta.SetCompletedApplied(ctx, tx, task.ID, true, string(detailJSON))
		if err != nil {
			return nil, false, semanticApplyInternalError(ctx, err)
		}
		if !updated {
			return nil, false, httperrors.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_SemanticUnderstandingTask_NotApplicable)
		}
		appliedNow = result.Applied
		task.ResourceName = resourceInfo.Name
		task.Applied = true
		task.ApplyDetailJSON = string(detailJSON)
	}
	if err := tx.Commit(); err != nil {
		return nil, false, semanticApplyInternalError(ctx, err)
	}
	// 事务读取取得的原始 input 仅在调用方具备 query_data 时返回。
	response := *task
	if visible.Input == "" {
		response.Input = ""
	}
	response.CatalogName = visible.CatalogName
	if response.ResourceName == "" {
		response.ResourceName = visible.ResourceName
	}
	response.Creator = visible.Creator
	return &response, appliedNow, nil
}

func validateSemanticTaskApplicable(ctx context.Context, task *interfaces.SemanticUnderstandingTask) error {
	if task.Scope != interfaces.SemanticUnderstandingTaskScopeResource || task.ApplyMode != interfaces.SemanticUnderstandingApplyModeDryRun ||
		task.Status != interfaces.SemanticUnderstandingTaskStatusCompleted || task.ResourceID == "" || task.ResultJSON == "" {
		return httperrors.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_SemanticUnderstandingTask_NotApplicable)
	}
	if math.IsNaN(task.Confidence) || math.IsInf(task.Confidence, 0) || math.IsNaN(task.ConfidenceThreshold) ||
		task.ConfidenceThreshold < 0 || task.ConfidenceThreshold > 1 || task.Confidence < task.ConfidenceThreshold || task.Confidence > 1 {
		return httperrors.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_SemanticUnderstandingTask_ConfidenceBelowThreshold)
	}
	return nil
}

func semanticApplyInternalError(ctx context.Context, err error) error {
	logger.Errorf("Apply semantic understanding result failed: %v", err)
	return httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_SemanticUnderstandingTask_InternalError_ApplyFailed)
}

func validateSemanticTaskSchema(task *interfaces.SemanticUnderstandingTask, resourceInfo *interfaces.Resource) error {
	var snapshot struct {
		Resource struct {
			ID               string                `json:"id"`
			Category         string                `json:"category"`
			Schema           string                `json:"schema"`
			SourceIdentifier string                `json:"source_identifier"`
			Fields           []semanticSchemaField `json:"schema_definition"`
		} `json:"resource"`
	}
	if err := validateSemanticJSON(task.Input); err != nil {
		return err
	}
	if err := common.UnmarshalPreciseJSON([]byte(task.Input), &snapshot); err != nil {
		return err
	}
	if snapshot.Resource.ID != task.ResourceID || resourceInfo.ID != task.ResourceID || resourceInfo.CatalogID != task.CatalogID ||
		snapshot.Resource.Category != resourceInfo.Category || snapshot.Resource.Schema != resourceInfo.Schema ||
		snapshot.Resource.SourceIdentifier != resourceInfo.SourceIdentifier || snapshot.Resource.Fields == nil {
		return fmt.Errorf("resource identity or schema snapshot does not match")
	}
	current := make([]semanticSchemaField, 0, len(resourceInfo.SchemaDefinition))
	for _, field := range resourceInfo.SchemaDefinition {
		if field == nil {
			return fmt.Errorf("schema contains a null field")
		}
		current = append(current, semanticSchemaField{Name: field.Name, Type: field.Type, OriginalName: field.OriginalName, OriginalType: field.OriginalType, OriginalDescription: field.OriginalDescription, Attributes: field.Attributes})
	}
	savedHash, err := semanticSchemaFingerprint(snapshot.Resource.Fields)
	if err != nil {
		return err
	}
	currentHash, err := semanticSchemaFingerprint(current)
	if err != nil {
		return err
	}
	if savedHash != currentHash {
		return fmt.Errorf("schema fingerprints do not match")
	}
	return nil
}

func semanticSchemaFingerprint(fields []semanticSchemaField) (string, error) {
	canonical := append([]semanticSchemaField{}, fields...)
	sort.Slice(canonical, func(i, j int) bool { return canonical[i].Name < canonical[j].Name })
	for i := range canonical {
		if canonical[i].Name == "" || canonical[i].Type == "" || (i > 0 && canonical[i-1].Name == canonical[i].Name) {
			return "", fmt.Errorf("schema contains an invalid or duplicate field")
		}
		if canonical[i].Attributes == nil {
			canonical[i].Attributes = map[string]any{}
		}
	}
	// encoding/json 递归排序对象键，保留数组顺序以及 json.Number 的原始数值精度。
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func validateSemanticResultFields(resultJSON string, resourceInfo *interfaces.Resource) error {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(resultJSON), &envelope); err != nil {
		return err
	}
	resourceJSON := strings.TrimSpace(string(envelope["resource"]))
	if len(resourceJSON) == 0 || resourceJSON[0] != '{' {
		return fmt.Errorf("result must contain a resource object")
	}
	var result interfaces.SemanticUnderstandingResourceResult
	if err := validateSemanticJSON(resultJSON); err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(resultJSON), &result); err != nil {
		return err
	}
	if strings.TrimSpace(resultJSON) == "null" {
		return fmt.Errorf("result must be an object")
	}
	if err := validateConfidence(result.Resource.Confidence, "resource.confidence"); err != nil {
		return err
	}
	if utf8.RuneCountInString(result.Resource.DisplayName) > interfaces.NAME_MAX_LENGTH {
		return fmt.Errorf("resource display name is too long")
	}
	known := make(map[string]bool, len(resourceInfo.SchemaDefinition))
	for _, field := range resourceInfo.SchemaDefinition {
		if field != nil {
			known[field.Name] = true
		}
	}
	seen := make(map[string]bool, len(result.Fields))
	for _, field := range result.Fields {
		if !known[field.Name] || seen[field.Name] {
			return fmt.Errorf("result contains an unknown or duplicate field")
		}
		if err := validateConfidence(field.Confidence, "field.confidence"); err != nil {
			return err
		}
		if utf8.RuneCountInString(strings.TrimSpace(field.DisplayName)) > interfaces.MaxLength_PropertyDisplayName || utf8.RuneCountInString(field.Description) > interfaces.MaxLength_PropertyDescription {
			return fmt.Errorf("field metadata is too long")
		}
		seen[field.Name] = true
	}
	return nil
}

// validateSemanticJSON 拒绝重复键，避免快照或模型结果通过后值覆盖前值改变校验语义。
func validateSemanticJSON(payload string) error {
	decoder := json.NewDecoder(strings.NewReader(payload))
	decoder.UseNumber()
	if err := consumeSemanticJSONValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("semantic JSON contains trailing data")
	}
	return nil
}

func consumeSemanticJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		keys := map[string]bool{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok || keys[key] {
				return fmt.Errorf("semantic JSON contains an invalid or duplicate key")
			}
			keys[key] = true
			if err := consumeSemanticJSONValue(decoder); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := consumeSemanticJSONValue(decoder); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unexpected semantic JSON delimiter")
	}
	_, err = decoder.Token()
	return err
}
