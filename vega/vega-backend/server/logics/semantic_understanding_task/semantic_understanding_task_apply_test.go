// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package semantic_understanding_task

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	verrors "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/errors"
	vmock "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/mock"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

type semanticApplyFixture struct {
	ctx       context.Context
	service   *semanticUnderstandingTaskService
	access    *vmock.MockSemanticUnderstandingTaskAccess
	resources *vmock.MockResourceService
	catalogs  *vmock.MockCatalogService
	accounts  *vmock.MockUserMgmtService
	sql       sqlmock.Sqlmock
	task      *interfaces.SemanticUnderstandingTask
	resource  *interfaces.Resource
}

func TestValidateSemanticTaskSchema(t *testing.T) {
	for _, tc := range []struct {
		name       string
		fields     string
		attributes map[string]any
		wantError  bool
	}{
		{name: "historical snapshot without attributes matches empty attributes", fields: `[{"name":"id","type":"integer"}]`},
		{name: "null attributes match empty attributes", fields: `[{"name":"id","type":"integer","attributes":null}]`, attributes: map[string]any{}},
		{name: "historical snapshot cannot borrow current attributes", fields: `[{"name":"id","type":"integer"}]`, attributes: map[string]any{"precision": 20}, wantError: true},
		{name: "object keys have no ordering and large integers retain precision", fields: `[{"name":"id","type":"integer","attributes":{"nested":{"z":true,"a":9007199254740993}}}]`, attributes: map[string]any{"nested": map[string]any{"a": json.Number("9007199254740993"), "z": true}}},
		{name: "different large integers do not match", fields: `[{"name":"id","type":"integer","attributes":{"precision":9007199254740993}}]`, attributes: map[string]any{"precision": json.Number("9007199254740992")}, wantError: true},
		{name: "array order is significant", fields: `[{"name":"id","type":"integer","attributes":{"path":["a","b"]}}]`, attributes: map[string]any{"path": []string{"b", "a"}}, wantError: true},
		{name: "added field invalidates entire result", fields: `[]`, wantError: true},
		{name: "type changes invalidate result", fields: `[{"name":"id","type":"string"}]`, wantError: true},
		{name: "original comment changes invalidate result", fields: `[{"name":"id","type":"integer","original_description":"old"}]`, wantError: true},
		{name: "business metadata is excluded", fields: `[{"name":"id","type":"integer","display_name":"old display","description":"old business description"}]`},
		{name: "duplicate fields are rejected", fields: `[{"name":"id","type":"integer"},{"name":"id","type":"integer"}]`, wantError: true},
		{name: "missing schema is rejected", fields: `null`, wantError: true},
		{name: "malformed schema is rejected", fields: `{}`, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task := &interfaces.SemanticUnderstandingTask{ResourceID: "resource-1", CatalogID: "catalog-1", Input: `{"resource":{"id":"resource-1","category":"table","schema":"public","source_identifier":"orders","schema_definition":` + tc.fields + `}}`}
			resource := &interfaces.Resource{ID: "resource-1", CatalogID: "catalog-1", Category: "table", Schema: "public", SourceIdentifier: "orders", SchemaDefinition: []*interfaces.Property{{Name: "id", Type: "integer", Attributes: tc.attributes, DisplayName: "Current display", Description: "Current description"}}}
			err := validateSemanticTaskSchema(task, resource)
			if tc.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
	t.Run("field ordering is ignored while full membership and origin are checked", func(t *testing.T) {
		task := &interfaces.SemanticUnderstandingTask{ResourceID: "r", CatalogID: "c", Input: `{"resource":{"id":"r","category":"table","schema":"public","source_identifier":"orders","schema_definition":[{"name":"id","type":"integer"},{"name":"title","type":"string"}]}}`}
		resource := &interfaces.Resource{ID: "r", CatalogID: "c", Category: "table", Schema: "public", SourceIdentifier: "orders", SchemaDefinition: []*interfaces.Property{{Name: "title", Type: "string"}, {Name: "id", Type: "integer"}}}
		require.NoError(t, validateSemanticTaskSchema(task, resource))
		resource.SchemaDefinition = resource.SchemaDefinition[:1]
		require.Error(t, validateSemanticTaskSchema(task, resource))
		resource.SchemaDefinition = append(resource.SchemaDefinition, &interfaces.Property{Name: "renamed_id", Type: "integer"})
		require.Error(t, validateSemanticTaskSchema(task, resource))
		resource.SchemaDefinition[1].Name = "id"
		resource.SourceIdentifier = "other_orders"
		require.Error(t, validateSemanticTaskSchema(task, resource))
		resource.SourceIdentifier = "orders"
		resource.CatalogID = "other_catalog"
		require.Error(t, validateSemanticTaskSchema(task, resource))
		resource.CatalogID = "c"
		resource.Schema = "another_schema"
		require.Error(t, validateSemanticTaskSchema(task, resource))
	})
	t.Run("explicit empty schema is valid and differs from absent or null schema", func(t *testing.T) {
		task := &interfaces.SemanticUnderstandingTask{ResourceID: "r", CatalogID: "c", Input: `{"resource":{"id":"r","category":"dataset","source_identifier":"","schema_definition":[]}}`}
		resource := &interfaces.Resource{ID: "r", CatalogID: "c", Category: "dataset"}
		require.NoError(t, validateSemanticTaskSchema(task, resource))
		task.Input = `{"resource":{"id":"r","category":"dataset"}}`
		require.Error(t, validateSemanticTaskSchema(task, resource))
	})
	t.Run("duplicate JSON attributes cannot hide structural differences", func(t *testing.T) {
		task := &interfaces.SemanticUnderstandingTask{ResourceID: "r", CatalogID: "c", Input: `{"resource":{"id":"r","category":"dataset","schema_definition":[{"name":"id","type":"integer","attributes":{"limit":1,"limit":2}}]}}`}
		resource := &interfaces.Resource{ID: "r", CatalogID: "c", Category: "dataset", SchemaDefinition: []*interfaces.Property{{Name: "id", Type: "integer", Attributes: map[string]any{"limit": 2}}}}
		require.Error(t, validateSemanticTaskSchema(task, resource))
	})

}

func TestSemanticUnderstandingTaskServiceApply(t *testing.T) {
	for _, mode := range []string{"fill_empty", "force"} {
		t.Run("uses saved result and preserves original task in "+mode, func(t *testing.T) {
			f := newSemanticApplyFixture(t)
			if mode == "fill_empty" {
				f.task.Confidence = f.task.ConfidenceThreshold
			}
			f.expectVisible(nil)
			f.resources.EXPECT().CheckResourcePermission(gomock.Any(), "resource-1", interfaces.OPERATION_TYPE_MODIFY).Return(nil)
			f.sql.ExpectBegin()
			resourceRead := f.resources.EXPECT().InternalGetByID(gomock.Any(), gomock.Any(), "resource-1").Return(f.resource, nil)
			taskRead := f.access.EXPECT().GetByID(gomock.Any(), gomock.Not(gomock.Nil()), "task-1").Return(f.task, nil).After(resourceRead)
			resourceWrite := f.resources.EXPECT().InternalUpdateSemanticMetadata(gomock.Any(), gomock.Any(), gomock.Any(), int64(10)).After(taskRead).
				DoAndReturn(func(_ context.Context, _ *sql.Tx, resource *interfaces.Resource, _ int64) error {
					assert.Equal(t, "actor-1", resource.Updater.ID)
					assert.Equal(t, "Business Orders", resource.Name)
					assert.Equal(t, "Identifier", resource.SchemaDefinition[0].DisplayName)
					if mode == "fill_empty" {
						assert.Equal(t, "Manual description", resource.Description)
					} else {
						assert.Equal(t, "Order descriptions", resource.Description)
					}
					return nil
				})
			f.access.EXPECT().SetCompletedApplied(gomock.Any(), gomock.Any(), "task-1", true, gomock.Any()).After(resourceWrite).
				DoAndReturn(func(_ context.Context, _ *sql.Tx, _ string, _ bool, detail string) (bool, error) {
					var parsed map[string]any
					require.NoError(t, json.Unmarshal([]byte(detail), &parsed))
					assert.Equal(t, mode, parsed["apply_mode"])
					return true, nil
				})
			f.sql.ExpectCommit()
			input, result := f.task.Input, f.task.ResultJSON
			got, appliedNow, err := f.service.Apply(f.ctx, "task-1", &interfaces.ApplySemanticUnderstandingTaskRequest{ApplyMode: mode})
			require.NoError(t, err)
			assert.True(t, got.Applied)
			assert.True(t, appliedNow)
			assert.Equal(t, "dry_run", got.ApplyMode)
			assert.Equal(t, input, got.Input)
			assert.Equal(t, result, got.ResultJSON)
			assert.Equal(t, "agent-task-1", got.AgentTaskID)
			assert.Equal(t, "creator-1", got.Creator.ID)
			assert.Equal(t, "completed", got.Status)
			assert.Equal(t, input, f.task.Input)
			assert.Equal(t, "dry_run", f.task.ApplyMode)
			require.NoError(t, f.sql.ExpectationsWereMet())
		})
	}
	t.Run("applied task is an authenticated idempotent no-op even with another mode", func(t *testing.T) {
		f := newSemanticApplyFixture(t)
		f.task.Applied = true
		f.task.ApplyDetailJSON = `{"apply_mode":"fill_empty"}`
		f.expectVisible(nil)
		f.resources.EXPECT().CheckResourcePermission(gomock.Any(), "resource-1", interfaces.OPERATION_TYPE_MODIFY).Return(nil)
		f.sql.ExpectBegin()
		resourceRead := f.resources.EXPECT().InternalGetByID(gomock.Any(), gomock.Not(gomock.Nil()), "resource-1").Return(f.resource, nil)
		f.access.EXPECT().GetByID(gomock.Any(), gomock.Not(gomock.Nil()), "task-1").Return(f.task, nil).After(resourceRead)
		f.sql.ExpectCommit()
		got, appliedNow, err := f.service.Apply(f.ctx, "task-1", &interfaces.ApplySemanticUnderstandingTaskRequest{ApplyMode: "force"})
		require.NoError(t, err)
		assert.True(t, got.Applied)
		assert.False(t, appliedNow)
		assert.JSONEq(t, `{"apply_mode":"fill_empty"}`, got.ApplyDetailJSON)
		require.NoError(t, f.sql.ExpectationsWereMet())
	})
	t.Run("input remains redacted without query_data while snapshot is used for validation", func(t *testing.T) {
		f := newSemanticApplyFixture(t)
		f.expectVisible(httperrors.NewHTTPError(f.ctx, http.StatusForbidden, httperrors.PublicError_Forbidden))
		f.resources.EXPECT().CheckResourcePermission(gomock.Any(), "resource-1", interfaces.OPERATION_TYPE_MODIFY).Return(nil)
		f.sql.ExpectBegin()
		f.access.EXPECT().GetByID(gomock.Any(), gomock.Not(gomock.Nil()), "task-1").Return(f.task, nil)
		f.resources.EXPECT().InternalGetByID(gomock.Any(), gomock.Any(), "resource-1").Return(f.resource, nil)
		f.resources.EXPECT().InternalUpdateSemanticMetadata(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		f.access.EXPECT().SetCompletedApplied(gomock.Any(), gomock.Any(), "task-1", true, gomock.Any()).Return(true, nil)
		f.sql.ExpectCommit()
		got, _, err := f.service.Apply(f.ctx, "task-1", &interfaces.ApplySemanticUnderstandingTaskRequest{ApplyMode: "force"})
		require.NoError(t, err)
		assert.Empty(t, got.Input)
		assert.NotEmpty(t, f.task.Input)
		require.NoError(t, f.sql.ExpectationsWereMet())
	})
	for _, status := range []string{"pending", "running", "failed", "cancelled"} {
		t.Run("rejects "+status+" before writes", func(t *testing.T) {
			f := newSemanticApplyFixture(t)
			f.task.Status = status
			f.expectVisible(nil)
			got, _, err := f.service.Apply(f.ctx, "task-1", &interfaces.ApplySemanticUnderstandingTaskRequest{ApplyMode: "force"})
			assert.Nil(t, got)
			assertSemanticApplyError(t, err, http.StatusConflict, verrors.VegaBackend_SemanticUnderstandingTask_NotApplicable)
			require.NoError(t, f.sql.ExpectationsWereMet())
		})
	}
	for _, test := range []struct {
		name   string
		change func(*semanticApplyFixture)
		code   string
	}{
		{"low confidence cannot be bypassed", func(f *semanticApplyFixture) { f.task.Confidence = 0.7 }, verrors.VegaBackend_SemanticUnderstandingTask_ConfidenceBelowThreshold},
		{"catalog task unavailable", func(f *semanticApplyFixture) { f.task.Scope = "catalog" }, verrors.VegaBackend_SemanticUnderstandingTask_NotApplicable},
		{"non-dry-run task unavailable", func(f *semanticApplyFixture) { f.task.ApplyMode = "fill_empty" }, verrors.VegaBackend_SemanticUnderstandingTask_NotApplicable},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newSemanticApplyFixture(t)
			test.change(f)
			f.expectVisible(nil)
			got, _, err := f.service.Apply(f.ctx, "task-1", &interfaces.ApplySemanticUnderstandingTaskRequest{ApplyMode: "force"})
			assert.Nil(t, got)
			assertSemanticApplyError(t, err, http.StatusConflict, test.code)
		})
	}
	for _, mode := range []string{"", "dry_run", "invalid"} {
		t.Run("rejects invalid mode "+mode, func(t *testing.T) {
			f := newSemanticApplyFixture(t)
			got, _, err := f.service.Apply(f.ctx, "task-1", &interfaces.ApplySemanticUnderstandingTaskRequest{ApplyMode: mode})
			assert.Nil(t, got)
			assertSemanticApplyError(t, err, http.StatusBadRequest, verrors.VegaBackend_InvalidParameter_Format)
		})
	}
	for _, test := range []struct {
		name   string
		change func(*semanticApplyFixture)
		code   string
	}{
		{"schema changes reject all writes", func(f *semanticApplyFixture) {
			f.resource.SchemaDefinition = append(f.resource.SchemaDefinition, &interfaces.Property{Name: "new_field", Type: "string"})
		}, verrors.VegaBackend_SemanticUnderstandingTask_SchemaMismatch},
		{"attributes changes reject all writes", func(f *semanticApplyFixture) {
			f.resource.SchemaDefinition[0].Attributes = map[string]any{"precision": 20}
		}, verrors.VegaBackend_SemanticUnderstandingTask_SchemaMismatch},
		{"unknown result field rejects all writes", func(f *semanticApplyFixture) { f.task.ResultJSON = `{"resource":{},"fields":[{"name":"unknown"}]}` }, verrors.VegaBackend_SemanticUnderstandingTask_InvalidResult},
		{"duplicate result field rejects all writes", func(f *semanticApplyFixture) {
			f.task.ResultJSON = `{"resource":{},"fields":[{"name":"id"},{"name":"id"}]}`
		}, verrors.VegaBackend_SemanticUnderstandingTask_InvalidResult},
		{"malformed saved snapshot rejects all writes", func(f *semanticApplyFixture) { f.task.Input = `{}` }, verrors.VegaBackend_SemanticUnderstandingTask_SchemaMismatch},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newSemanticApplyFixture(t)
			test.change(f)
			f.expectVisible(nil)
			f.resources.EXPECT().CheckResourcePermission(gomock.Any(), "resource-1", interfaces.OPERATION_TYPE_MODIFY).Return(nil)
			f.sql.ExpectBegin()
			f.access.EXPECT().GetByID(gomock.Any(), gomock.Not(gomock.Nil()), "task-1").Return(f.task, nil)
			f.resources.EXPECT().InternalGetByID(gomock.Any(), gomock.Any(), "resource-1").Return(f.resource, nil)
			f.sql.ExpectRollback()
			got, _, err := f.service.Apply(f.ctx, "task-1", &interfaces.ApplySemanticUnderstandingTaskRequest{ApplyMode: "force"})
			assert.Nil(t, got)
			assertSemanticApplyError(t, err, http.StatusConflict, test.code)
			assert.False(t, f.task.Applied)
			assert.Equal(t, "completed", f.task.Status)
			require.NoError(t, f.sql.ExpectationsWereMet())
		})
	}
	t.Run("resource update and application marker failure roll back together", func(t *testing.T) {
		f := newSemanticApplyFixture(t)
		f.expectVisible(nil)
		f.resources.EXPECT().CheckResourcePermission(gomock.Any(), "resource-1", interfaces.OPERATION_TYPE_MODIFY).Return(nil)
		f.sql.ExpectBegin()
		f.access.EXPECT().GetByID(gomock.Any(), gomock.Not(gomock.Nil()), "task-1").Return(f.task, nil)
		f.resources.EXPECT().InternalGetByID(gomock.Any(), gomock.Any(), "resource-1").Return(f.resource, nil)
		f.resources.EXPECT().InternalUpdateSemanticMetadata(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		f.access.EXPECT().SetCompletedApplied(gomock.Any(), gomock.Any(), "task-1", true, gomock.Any()).Return(false, errors.New("database failed"))
		f.sql.ExpectRollback()
		got, _, err := f.service.Apply(f.ctx, "task-1", &interfaces.ApplySemanticUnderstandingTaskRequest{ApplyMode: "force"})
		assert.Nil(t, got)
		assertSemanticApplyError(t, err, http.StatusInternalServerError, verrors.VegaBackend_SemanticUnderstandingTask_InternalError_ApplyFailed)
		assert.False(t, f.task.Applied)
		assert.Equal(t, "completed", f.task.Status)
		require.NoError(t, f.sql.ExpectationsWereMet())
	})
	t.Run("no-change application marks the task applied and cannot be replayed with force", func(t *testing.T) {
		f := newSemanticApplyFixture(t)
		f.resource.Name = "Manual orders"
		f.resource.Description = "Manual resource description"
		f.resource.SchemaDefinition[0].DisplayName = "Manual identifier"
		f.resource.SchemaDefinition[0].Description = "Manual field description"
		f.expectVisible(nil)
		f.resources.EXPECT().CheckResourcePermission(gomock.Any(), "resource-1", interfaces.OPERATION_TYPE_MODIFY).Return(nil)
		f.sql.ExpectBegin()
		f.access.EXPECT().GetByID(gomock.Any(), gomock.Not(gomock.Nil()), "task-1").Return(f.task, nil)
		f.resources.EXPECT().InternalGetByID(gomock.Any(), gomock.Not(gomock.Nil()), "resource-1").Return(f.resource, nil)
		f.access.EXPECT().SetCompletedApplied(gomock.Any(), gomock.Any(), "task-1", true, gomock.Any()).
			DoAndReturn(func(_ context.Context, _ *sql.Tx, _ string, _ bool, detail string) (bool, error) {
				assert.JSONEq(t, `{"reason":"no_resource_changes","apply_mode":"fill_empty","scope":"resource"}`, detail)
				return true, nil
			})
		f.sql.ExpectCommit()
		got, appliedNow, err := f.service.Apply(f.ctx, "task-1", &interfaces.ApplySemanticUnderstandingTaskRequest{ApplyMode: "fill_empty"})
		require.NoError(t, err)
		assert.True(t, got.Applied)
		assert.False(t, appliedNow)
		assert.Equal(t, "completed", got.Status)
		assert.Equal(t, "dry_run", got.ApplyMode)
		originalDetail := got.ApplyDetailJSON

		f.expectVisible(nil)
		f.resources.EXPECT().CheckResourcePermission(gomock.Any(), "resource-1", interfaces.OPERATION_TYPE_MODIFY).Return(nil)
		f.sql.ExpectBegin()
		f.resources.EXPECT().InternalGetByID(gomock.Any(), gomock.Not(gomock.Nil()), "resource-1").Return(f.resource, nil)
		f.access.EXPECT().GetByID(gomock.Any(), gomock.Not(gomock.Nil()), "task-1").Return(f.task, nil)
		f.sql.ExpectCommit()
		repeated, appliedNow, err := f.service.Apply(f.ctx, "task-1", &interfaces.ApplySemanticUnderstandingTaskRequest{ApplyMode: "force"})
		require.NoError(t, err)
		assert.True(t, repeated.Applied)
		assert.False(t, appliedNow)
		assert.Equal(t, originalDetail, repeated.ApplyDetailJSON)
		assert.Equal(t, "Manual orders", f.resource.Name)
		assert.Equal(t, "Manual resource description", f.resource.Description)
		assert.Equal(t, "Manual identifier", f.resource.SchemaDefinition[0].DisplayName)
		assert.Equal(t, "Manual field description", f.resource.SchemaDefinition[0].Description)
		require.NoError(t, f.sql.ExpectationsWereMet())
	})

	for _, conflict := range []string{"resource version changed", "task was already applied"} {
		t.Run(conflict+" rolls back without reporting another application", func(t *testing.T) {
			f := newSemanticApplyFixture(t)
			f.expectVisible(nil)
			f.resources.EXPECT().CheckResourcePermission(gomock.Any(), "resource-1", interfaces.OPERATION_TYPE_MODIFY).Return(nil)
			f.sql.ExpectBegin()
			f.resources.EXPECT().InternalGetByID(gomock.Any(), gomock.Not(gomock.Nil()), "resource-1").Return(f.resource, nil)
			f.access.EXPECT().GetByID(gomock.Any(), gomock.Not(gomock.Nil()), "task-1").Return(f.task, nil)
			code := verrors.VegaBackend_Resource_UpdateConflict
			var writeErr error
			if conflict == "resource version changed" {
				writeErr = httperrors.NewHTTPError(f.ctx, http.StatusConflict, code)
			} else {
				code = verrors.VegaBackend_SemanticUnderstandingTask_NotApplicable
				f.access.EXPECT().SetCompletedApplied(gomock.Any(), gomock.Any(), "task-1", true, gomock.Any()).Return(false, nil)
			}
			f.resources.EXPECT().InternalUpdateSemanticMetadata(gomock.Any(), gomock.Any(), gomock.Any(), int64(10)).Return(writeErr)
			f.sql.ExpectRollback()
			got, appliedNow, err := f.service.Apply(f.ctx, "task-1", &interfaces.ApplySemanticUnderstandingTaskRequest{ApplyMode: "force"})
			assert.Nil(t, got)
			assert.False(t, appliedNow)
			assert.False(t, f.task.Applied)
			assertSemanticApplyError(t, err, http.StatusConflict, code)
			require.NoError(t, f.sql.ExpectationsWereMet())
		})
	}
	t.Run("no-change application marker conflict rolls back instead of reporting completion", func(t *testing.T) {
		f := newSemanticApplyFixture(t)
		f.resource.Name = "Business Orders"
		f.resource.Description = "Order descriptions"
		f.resource.SchemaDefinition[0].DisplayName = "Identifier"
		f.resource.SchemaDefinition[0].Description = "Order identifier"
		f.expectVisible(nil)
		f.resources.EXPECT().CheckResourcePermission(gomock.Any(), "resource-1", interfaces.OPERATION_TYPE_MODIFY).Return(nil)
		f.sql.ExpectBegin()
		f.resources.EXPECT().InternalGetByID(gomock.Any(), gomock.Not(gomock.Nil()), "resource-1").Return(f.resource, nil)
		f.access.EXPECT().GetByID(gomock.Any(), gomock.Not(gomock.Nil()), "task-1").Return(f.task, nil)
		f.access.EXPECT().SetCompletedApplied(gomock.Any(), gomock.Any(), "task-1", true, gomock.Any()).Return(false, nil)
		f.sql.ExpectRollback()
		got, appliedNow, err := f.service.Apply(f.ctx, "task-1", &interfaces.ApplySemanticUnderstandingTaskRequest{ApplyMode: "force"})
		assert.Nil(t, got)
		assert.False(t, appliedNow)
		assert.False(t, f.task.Applied)
		assertSemanticApplyError(t, err, http.StatusConflict, verrors.VegaBackend_SemanticUnderstandingTask_NotApplicable)
		require.NoError(t, f.sql.ExpectationsWereMet())
	})

	t.Run("resource modify permission denial prevents transaction", func(t *testing.T) {
		f := newSemanticApplyFixture(t)
		f.expectVisible(nil)
		denied := httperrors.NewHTTPError(f.ctx, http.StatusForbidden, httperrors.PublicError_Forbidden)
		f.resources.EXPECT().CheckResourcePermission(gomock.Any(), "resource-1", interfaces.OPERATION_TYPE_MODIFY).Return(denied)
		got, _, err := f.service.Apply(f.ctx, "task-1", &interfaces.ApplySemanticUnderstandingTaskRequest{ApplyMode: "force"})
		assert.Nil(t, got)
		assert.Same(t, denied, err)
		require.NoError(t, f.sql.ExpectationsWereMet())
	})
	t.Run("begin failure leaves task unchanged", func(t *testing.T) {
		f := newSemanticApplyFixture(t)
		f.expectVisible(nil)
		f.resources.EXPECT().CheckResourcePermission(gomock.Any(), "resource-1", interfaces.OPERATION_TYPE_MODIFY).Return(nil)
		f.sql.ExpectBegin().WillReturnError(errors.New("begin failed"))
		got, appliedNow, err := f.service.Apply(f.ctx, "task-1", &interfaces.ApplySemanticUnderstandingTaskRequest{ApplyMode: "force"})
		assert.Nil(t, got)
		assert.False(t, appliedNow)
		assertSemanticApplyError(t, err, http.StatusInternalServerError, verrors.VegaBackend_SemanticUnderstandingTask_InternalError_ApplyFailed)
		assert.False(t, f.task.Applied)
		require.NoError(t, f.sql.ExpectationsWereMet())
	})
	t.Run("state is checked again within the transaction", func(t *testing.T) {
		f := newSemanticApplyFixture(t)
		f.expectVisible(nil)
		f.resources.EXPECT().CheckResourcePermission(gomock.Any(), "resource-1", interfaces.OPERATION_TYPE_MODIFY).Return(nil)
		locked := *f.task
		locked.Status = "running"
		f.sql.ExpectBegin()
		resourceRead := f.resources.EXPECT().InternalGetByID(gomock.Any(), gomock.Not(gomock.Nil()), "resource-1").Return(f.resource, nil)
		f.access.EXPECT().GetByID(gomock.Any(), gomock.Not(gomock.Nil()), "task-1").Return(&locked, nil).After(resourceRead)
		f.sql.ExpectRollback()
		got, appliedNow, err := f.service.Apply(f.ctx, "task-1", &interfaces.ApplySemanticUnderstandingTaskRequest{ApplyMode: "force"})
		assert.Nil(t, got)
		assert.False(t, appliedNow)
		assertSemanticApplyError(t, err, http.StatusConflict, verrors.VegaBackend_SemanticUnderstandingTask_NotApplicable)
		require.NoError(t, f.sql.ExpectationsWereMet())
	})
	t.Run("task already applied in the transactional snapshot is not applied again", func(t *testing.T) {
		f := newSemanticApplyFixture(t)
		f.expectVisible(nil)
		f.resources.EXPECT().CheckResourcePermission(gomock.Any(), "resource-1", interfaces.OPERATION_TYPE_MODIFY).Return(nil)
		locked := *f.task
		locked.Applied = true
		locked.ApplyDetailJSON = `{"apply_mode":"fill_empty"}`
		f.sql.ExpectBegin()
		var transaction *sql.Tx
		resourceRead := f.resources.EXPECT().InternalGetByID(gomock.Any(), gomock.Not(gomock.Nil()), "resource-1").
			DoAndReturn(func(_ context.Context, tx *sql.Tx, _ string) (*interfaces.Resource, error) {
				transaction = tx
				return f.resource, nil
			})
		f.access.EXPECT().GetByID(gomock.Any(), gomock.Not(gomock.Nil()), "task-1").
			DoAndReturn(func(_ context.Context, tx *sql.Tx, _ string) (*interfaces.SemanticUnderstandingTask, error) {
				assert.Same(t, transaction, tx)
				return &locked, nil
			}).After(resourceRead)
		f.sql.ExpectCommit()
		got, appliedNow, err := f.service.Apply(f.ctx, "task-1", &interfaces.ApplySemanticUnderstandingTaskRequest{ApplyMode: "force"})
		require.NoError(t, err)
		assert.True(t, got.Applied)
		assert.False(t, appliedNow)
		assert.JSONEq(t, `{"apply_mode":"fill_empty"}`, got.ApplyDetailJSON)
		require.NoError(t, f.sql.ExpectationsWereMet())
	})
	t.Run("commit failure does not report an applied result", func(t *testing.T) {
		f := newSemanticApplyFixture(t)
		f.expectVisible(nil)
		f.resources.EXPECT().CheckResourcePermission(gomock.Any(), "resource-1", interfaces.OPERATION_TYPE_MODIFY).Return(nil)
		f.sql.ExpectBegin()
		f.access.EXPECT().GetByID(gomock.Any(), gomock.Not(gomock.Nil()), "task-1").Return(f.task, nil)
		f.resources.EXPECT().InternalGetByID(gomock.Any(), gomock.Any(), "resource-1").Return(f.resource, nil)
		f.resources.EXPECT().InternalUpdateSemanticMetadata(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		f.access.EXPECT().SetCompletedApplied(gomock.Any(), gomock.Any(), "task-1", true, gomock.Any()).Return(true, nil)
		f.sql.ExpectCommit().WillReturnError(errors.New("commit failed"))
		got, appliedNow, err := f.service.Apply(f.ctx, "task-1", &interfaces.ApplySemanticUnderstandingTaskRequest{ApplyMode: "force"})
		assert.Nil(t, got)
		assert.False(t, appliedNow)
		assertSemanticApplyError(t, err, http.StatusInternalServerError, verrors.VegaBackend_SemanticUnderstandingTask_InternalError_ApplyFailed)
		require.NoError(t, f.sql.ExpectationsWereMet())
	})

}

func newSemanticApplyFixture(t *testing.T) *semanticApplyFixture {
	t.Helper()
	ctrl := gomock.NewController(t)
	db, sqlMock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	f := &semanticApplyFixture{ctx: context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{ID: "actor-1", Type: interfaces.ACCESSOR_TYPE_USER}), access: vmock.NewMockSemanticUnderstandingTaskAccess(ctrl), resources: vmock.NewMockResourceService(ctrl), catalogs: vmock.NewMockCatalogService(ctrl), accounts: vmock.NewMockUserMgmtService(ctrl), sql: sqlMock}
	f.service = &semanticUnderstandingTaskService{db: db, suta: f.access, rs: f.resources, cs: f.catalogs, ums: f.accounts}
	f.task = &interfaces.SemanticUnderstandingTask{ID: "task-1", Scope: "resource", ResourceID: "resource-1", CatalogID: "catalog-1", Status: "completed", ApplyMode: "dry_run", Confidence: 0.9, ConfidenceThreshold: 0.75, AgentTaskID: "agent-task-1", Creator: interfaces.AccountInfo{ID: "creator-1"}, Input: `{"resource":{"id":"resource-1","category":"table","schema":"public","source_identifier":"orders","schema_definition":[{"name":"id","type":"integer"}]}}`, ResultJSON: `{"resource":{"display_name":"Business Orders","description":"Order descriptions"},"fields":[{"name":"id","display_name":"Identifier","description":"Order identifier"}]}`}
	f.resource = &interfaces.Resource{ID: "resource-1", CatalogID: "catalog-1", Category: "table", Schema: "public", SourceIdentifier: "orders", Name: "orders", Description: "Manual description", UpdateTime: 10, SchemaDefinition: []*interfaces.Property{{Name: "id", Type: "integer"}}}
	return f
}

func (f *semanticApplyFixture) expectVisible(queryPermissionError error) {
	f.access.EXPECT().GetByID(gomock.Any(), nil, "task-1").DoAndReturn(func(context.Context, *sql.Tx, string) (*interfaces.SemanticUnderstandingTask, error) {
		visible := *f.task
		return &visible, nil
	})
	f.catalogs.EXPECT().CheckCatalogPermission(gomock.Any(), "catalog-1", []string{interfaces.OPERATION_TYPE_TASK_MANAGE}, true).Return(true, &interfaces.Catalog{ID: "catalog-1"}, nil)
	f.resources.EXPECT().CheckResourcePermission(gomock.Any(), "resource-1", interfaces.OPERATION_TYPE_QUERY_DATA).Return(queryPermissionError)
	f.resources.EXPECT().InternalGetByIDs(gomock.Any(), []string{"resource-1"}).Return(map[string]*interfaces.Resource{"resource-1": f.resource}, nil)
	f.catalogs.EXPECT().InternalGetByIDs(gomock.Any(), []string{"catalog-1"}).Return(map[string]*interfaces.Catalog{"catalog-1": {ID: "catalog-1", Name: "Sales"}}, nil)
	f.accounts.EXPECT().GetAccountNames(gomock.Any(), gomock.Any()).Return(nil)
}

func assertSemanticApplyError(t *testing.T, err error, status int, code string) {
	t.Helper()
	var httpErr *httperrors.HTTPError
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, status, httpErr.HTTPCode)
	assert.Equal(t, code, httpErr.ErrorCode)
}

func TestValidateSemanticResultFields(t *testing.T) {
	resource := &interfaces.Resource{SchemaDefinition: []*interfaces.Property{{Name: "id", Type: "integer"}}}
	for _, payload := range []string{`null`, ` null `, `{}`, `{"resource":null}`, `{"resource":{},"fields":[{"name":"id"},{"name":"id"}]}`, `{"resource":{},"fields":[{"name":"missing"}]}`, `{"resource":{},"resource":{}}`, `{"resource":{},"fields":[{"name":"id","confidence":2}]}`} {
		t.Run(payload, func(t *testing.T) { require.Error(t, validateSemanticResultFields(payload, resource)) })
	}
	require.NoError(t, validateSemanticResultFields(`{"resource":{},"fields":[{"name":"id"}]}`, resource))
}
