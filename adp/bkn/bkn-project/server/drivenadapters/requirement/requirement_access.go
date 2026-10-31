// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"

	sq "github.com/Masterminds/squirrel"
	libdb "github.com/openbkn-ai/bkn-foundry/comm-go/db"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

const (
	DOCUMENT_TABLE_NAME    = "t_bkn_project_requirement_document"
	REQUIREMENT_TABLE_NAME = "t_bkn_project_requirement"
)

var (
	raOnce   sync.Once
	raAccess interfaces.RequirementAccess
)

// documentColumns leave out the content and blocks, which only reads of whole documents select.
var documentColumns = []string{
	"f_id", "f_project_id", "f_name", "f_doc_type", "f_format", "f_sha256", "f_size",
	"f_uploader", "f_uploader_type", "f_create_time", "f_update_time",
}

var requirementColumns = []string{
	"f_project_id", "f_version", "f_status", "f_spec", "f_inputs", "f_error", "f_run_id",
	"f_agent_task_id", "f_drafter", "f_drafter_type", "f_create_time", "f_update_time",
}

type requirementAccess struct {
	db *sql.DB
}

func NewRequirementAccess(appSetting *common.AppSetting) interfaces.RequirementAccess {
	raOnce.Do(func() {
		raAccess = &requirementAccess{db: libdb.NewDB(&appSetting.DBSetting)}
	})
	return raAccess
}

func (ra *requirementAccess) ListDocuments(ctx context.Context, projectID string) ([]*interfaces.RequirementDocument, error) {
	return ra.listDocuments(ctx, "ListRequirementDocuments", projectID, false)
}

func (ra *requirementAccess) GetDocuments(ctx context.Context, projectID string) ([]*interfaces.RequirementDocument, error) {
	return ra.listDocuments(ctx, "GetRequirementDocuments", projectID, true)
}

func (ra *requirementAccess) listDocuments(ctx context.Context, name string, projectID string,
	withContent bool) ([]*interfaces.RequirementDocument, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, name)
	defer span.End()

	sqlStr, vals, err := sq.Select(selectedDocumentColumns(withContent)...).From(DOCUMENT_TABLE_NAME).
		Where(sq.Eq{"f_project_id": projectID}).OrderBy("f_create_time", "f_id").
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return nil, err
	}
	rows, err := ra.db.QueryContext(ctx, sqlStr, vals...)
	if err != nil {
		span.SetStatus(codes.Error, "Query data failed")
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	docs := []*interfaces.RequirementDocument{}
	for rows.Next() {
		doc, err := scanDocument(rows, withContent)
		if err != nil {
			span.SetStatus(codes.Error, "Scan data failed")
			return nil, err
		}
		docs = append(docs, doc)
	}
	if err = rows.Err(); err != nil {
		span.SetStatus(codes.Error, "Query data failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return docs, nil
}

func (ra *requirementAccess) GetDocument(ctx context.Context, projectID string,
	documentID string) (*interfaces.RequirementDocument, bool, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "GetRequirementDocument")
	defer span.End()

	sqlStr, vals, err := sq.Select(selectedDocumentColumns(true)...).From(DOCUMENT_TABLE_NAME).
		Where(sq.Eq{"f_id": documentID}).Where(sq.Eq{"f_project_id": projectID}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return nil, false, err
	}
	doc, err := scanDocument(ra.db.QueryRowContext(ctx, sqlStr, vals...), true)
	if errors.Is(err, sql.ErrNoRows) {
		span.SetStatus(codes.Ok, "")
		return nil, false, nil
	}
	if err != nil {
		span.SetStatus(codes.Error, "Query data failed")
		return nil, false, err
	}
	span.SetStatus(codes.Ok, "")
	return doc, true, nil
}

// CreateDocument relies on the unique key of project and content: a second copy of a document is
// not inserted, and the id stored for that content tells which document has it.
func (ra *requirementAccess) CreateDocument(ctx context.Context, doc *interfaces.RequirementDocument) (bool, string, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "CreateRequirementDocument")
	defer span.End()

	blocks := doc.Blocks
	if blocks == nil {
		blocks = []interfaces.RequirementBlock{}
	}
	blocksJSON, err := json.Marshal(blocks)
	if err != nil {
		span.SetStatus(codes.Error, "Marshal blocks failed")
		return false, "", err
	}
	sqlStr, vals, err := sq.Insert(DOCUMENT_TABLE_NAME).
		Columns("f_id", "f_project_id", "f_name", "f_doc_type", "f_format", "f_content", "f_blocks", "f_sha256",
			"f_size", "f_uploader", "f_uploader_type", "f_create_time", "f_update_time").
		Values(doc.ID, doc.ProjectID, doc.Name, doc.DocType, doc.Format, doc.Content, string(blocksJSON), doc.SHA256,
			doc.Size, doc.Uploader.ID, doc.Uploader.Type, doc.CreateTime, doc.UpdateTime).
		Suffix("ON DUPLICATE KEY UPDATE f_id = f_id").
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return false, "", err
	}
	if _, err = ra.db.ExecContext(ctx, sqlStr, vals...); err != nil {
		span.SetStatus(codes.Error, "Write data failed")
		return false, "", err
	}
	sqlStr, vals, err = sq.Select("f_id").From(DOCUMENT_TABLE_NAME).
		Where(sq.Eq{"f_project_id": doc.ProjectID}).Where(sq.Eq{"f_sha256": doc.SHA256}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return false, "", err
	}
	var storedID string
	if err = ra.db.QueryRowContext(ctx, sqlStr, vals...).Scan(&storedID); err != nil {
		span.SetStatus(codes.Error, "Query data failed")
		return false, "", err
	}
	span.SetStatus(codes.Ok, "")
	return storedID == doc.ID, storedID, nil
}

func (ra *requirementAccess) SetDocumentType(ctx context.Context, projectID string, documentID string,
	docType string, updateTime int64) (bool, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "SetRequirementDocumentType")
	defer span.End()

	sqlStr, vals, err := sq.Update(DOCUMENT_TABLE_NAME).
		Set("f_doc_type", docType).Set("f_update_time", updateTime).
		Where(sq.Eq{"f_id": documentID}).Where(sq.Eq{"f_project_id": projectID}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return false, err
	}
	return ra.execAffected(ctx, span, sqlStr, vals)
}

func (ra *requirementAccess) SuggestDocumentType(ctx context.Context, projectID string, documentID string,
	docType string, updateTime int64) (bool, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "SuggestRequirementDocumentType")
	defer span.End()

	sqlStr, vals, err := sq.Update(DOCUMENT_TABLE_NAME).
		Set("f_doc_type", docType).Set("f_update_time", updateTime).
		Where(sq.Eq{"f_id": documentID}).Where(sq.Eq{"f_project_id": projectID}).
		Where(sq.Eq{"f_doc_type": interfaces.REQUIREMENT_DOC_OTHER}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return false, err
	}
	return ra.execAffected(ctx, span, sqlStr, vals)
}

func (ra *requirementAccess) DeleteDocument(ctx context.Context, projectID string, documentID string) (bool, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "DeleteRequirementDocument")
	defer span.End()

	sqlStr, vals, err := sq.Delete(DOCUMENT_TABLE_NAME).
		Where(sq.Eq{"f_id": documentID}).Where(sq.Eq{"f_project_id": projectID}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return false, err
	}
	return ra.execAffected(ctx, span, sqlStr, vals)
}

func (ra *requirementAccess) GetRequirement(ctx context.Context, projectID string) (*interfaces.Requirement, bool, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "GetRequirement")
	defer span.End()

	sqlStr, vals, err := sq.Select(requirementColumns...).From(REQUIREMENT_TABLE_NAME).
		Where(sq.Eq{"f_project_id": projectID}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return nil, false, err
	}
	req, err := scanRequirement(ra.db.QueryRowContext(ctx, sqlStr, vals...))
	if errors.Is(err, sql.ErrNoRows) {
		span.SetStatus(codes.Ok, "")
		return nil, false, nil
	}
	if err != nil {
		span.SetStatus(codes.Error, "Query data failed")
		return nil, false, err
	}
	span.SetStatus(codes.Ok, "")
	return req, true, nil
}

func (ra *requirementAccess) StartDrafting(ctx context.Context, req *interfaces.Requirement,
	staleBefore int64, readUpdateTime int64) (started bool, err error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "StartRequirementDrafting")
	defer span.End()

	specJSON, inputsJSON, err := marshalRequirement(req)
	if err != nil {
		span.SetStatus(codes.Error, "Marshal requirement failed")
		return false, err
	}
	tx, err := ra.db.BeginTx(ctx, nil)
	if err != nil {
		span.SetStatus(codes.Error, "Begin transaction failed")
		return false, err
	}
	defer func() {
		if err != nil || !started {
			_ = tx.Rollback()
		}
	}()

	// As with drafts: the row lock makes two starts of one project take turns, and a project's
	// first start makes sure of the row before locking it.
	sqlStr, vals, err := sq.Insert(REQUIREMENT_TABLE_NAME).Columns("f_project_id", "f_spec", "f_inputs").
		Values(req.ProjectID, "", "").Suffix("ON DUPLICATE KEY UPDATE f_project_id = f_project_id").
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return false, err
	}
	if _, err = tx.ExecContext(ctx, sqlStr, vals...); err != nil {
		span.SetStatus(codes.Error, "Write data failed")
		return false, err
	}
	sqlStr, vals, err = sq.Select("f_status", "f_update_time").From(REQUIREMENT_TABLE_NAME).
		Where(sq.Eq{"f_project_id": req.ProjectID}).Suffix("FOR UPDATE").
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return false, err
	}
	var status string
	var updateTime int64
	switch err = tx.QueryRowContext(ctx, sqlStr, vals...).Scan(&status, &updateTime); {
	case err != nil:
		span.SetStatus(codes.Error, "Query data failed")
		return false, err
	case status == interfaces.REQUIREMENT_STATUS_DRAFTING && updateTime >= staleBefore:
		span.SetStatus(codes.Ok, "")
		return false, nil
	case readUpdateTime != 0 && updateTime != readUpdateTime:
		span.SetStatus(codes.Ok, "")
		return false, interfaces.ErrRequirementChanged
	}
	sqlStr, vals, err = sq.Update(REQUIREMENT_TABLE_NAME).
		Set("f_version", req.Version).Set("f_status", req.Status).Set("f_spec", specJSON).
		Set("f_inputs", inputsJSON).Set("f_error", req.Error).Set("f_run_id", req.RunID).
		Set("f_agent_task_id", req.AgentTaskID).
		Set("f_drafter", req.Drafter.ID).Set("f_drafter_type", req.Drafter.Type).
		Set("f_create_time", req.CreateTime).Set("f_update_time", req.UpdateTime).
		Where(sq.Eq{"f_project_id": req.ProjectID}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return false, err
	}
	if _, err = tx.ExecContext(ctx, sqlStr, vals...); err != nil {
		span.SetStatus(codes.Error, "Write data failed")
		return false, err
	}
	if err = tx.Commit(); err != nil {
		span.SetStatus(codes.Error, "Commit transaction failed")
		return false, err
	}
	started = true
	span.SetStatus(codes.Ok, "")
	return true, nil
}

func (ra *requirementAccess) UpdateRequirement(ctx context.Context, req *interfaces.Requirement,
	readUpdateTime int64) (bool, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "UpdateRequirement")
	defer span.End()

	specJSON, err := json.Marshal(req.Spec)
	if err != nil {
		span.SetStatus(codes.Error, "Marshal spec failed")
		return false, err
	}
	sqlStr, vals, err := sq.Update(REQUIREMENT_TABLE_NAME).
		Set("f_spec", string(specJSON)).Set("f_update_time", req.UpdateTime).
		Where(sq.Eq{"f_project_id": req.ProjectID}).
		Where(sq.Eq{"f_status": interfaces.REQUIREMENT_STATUS_READY}).
		Where(sq.Eq{"f_update_time": readUpdateTime}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return false, err
	}
	return ra.execAffected(ctx, span, sqlStr, vals)
}

func (ra *requirementAccess) FinishDrafting(ctx context.Context, req *interfaces.Requirement) (bool, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "FinishRequirementDrafting")
	defer span.End()

	specJSON, inputsJSON, err := marshalRequirement(req)
	if err != nil {
		span.SetStatus(codes.Error, "Marshal requirement failed")
		return false, err
	}
	sqlStr, vals, err := sq.Update(REQUIREMENT_TABLE_NAME).
		Set("f_status", req.Status).Set("f_spec", specJSON).Set("f_inputs", inputsJSON).
		Set("f_error", req.Error).Set("f_agent_task_id", req.AgentTaskID).Set("f_update_time", req.UpdateTime).
		Where(sq.Eq{"f_project_id": req.ProjectID}).
		Where(sq.Eq{"f_status": interfaces.REQUIREMENT_STATUS_DRAFTING}).
		Where(sq.Eq{"f_run_id": req.RunID}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return false, err
	}
	return ra.execAffected(ctx, span, sqlStr, vals)
}

func (ra *requirementAccess) FailDrafting(ctx context.Context, reason string, updateTime int64) (int64, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "FailRequirementDrafting")
	defer span.End()

	sqlStr, vals, err := sq.Update(REQUIREMENT_TABLE_NAME).
		Set("f_status", interfaces.REQUIREMENT_STATUS_FAILED).Set("f_error", reason).Set("f_update_time", updateTime).
		Where(sq.Eq{"f_status": interfaces.REQUIREMENT_STATUS_DRAFTING}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return 0, err
	}
	result, err := ra.db.ExecContext(ctx, sqlStr, vals...)
	if err != nil {
		span.SetStatus(codes.Error, "Update data failed")
		return 0, err
	}
	span.SetStatus(codes.Ok, "")
	return result.RowsAffected()
}

type spanStatus interface {
	SetStatus(code codes.Code, description string)
}

// execAffected runs a write and reports whether it changed a row.
func (ra *requirementAccess) execAffected(ctx context.Context, span spanStatus, sqlStr string, vals []any) (bool, error) {
	result, err := ra.db.ExecContext(ctx, sqlStr, vals...)
	if err != nil {
		span.SetStatus(codes.Error, "Write data failed")
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		span.SetStatus(codes.Error, "Read affected rows failed")
		return false, err
	}
	span.SetStatus(codes.Ok, "")
	return n > 0, nil
}

func selectedDocumentColumns(withContent bool) []string {
	if !withContent {
		return documentColumns
	}
	return append(append([]string{}, documentColumns...), "f_content", "f_blocks")
}

func scanDocument(row interface{ Scan(dest ...any) error }, withContent bool) (*interfaces.RequirementDocument, error) {
	doc := &interfaces.RequirementDocument{}
	dest := []any{
		&doc.ID, &doc.ProjectID, &doc.Name, &doc.DocType, &doc.Format, &doc.SHA256, &doc.Size,
		&doc.Uploader.ID, &doc.Uploader.Type, &doc.CreateTime, &doc.UpdateTime,
	}
	var blocksJSON string
	if withContent {
		dest = append(dest, &doc.Content, &blocksJSON)
	}
	if err := row.Scan(dest...); err != nil {
		return nil, err
	}
	if withContent {
		doc.Blocks = []interfaces.RequirementBlock{}
		if err := json.Unmarshal([]byte(blocksJSON), &doc.Blocks); err != nil {
			return nil, err
		}
	}
	return doc, nil
}

func scanRequirement(row interface{ Scan(dest ...any) error }) (*interfaces.Requirement, error) {
	req := &interfaces.Requirement{}
	var specJSON, inputsJSON string
	err := row.Scan(
		&req.ProjectID, &req.Version, &req.Status, &specJSON, &inputsJSON, &req.Error, &req.RunID,
		&req.AgentTaskID, &req.Drafter.ID, &req.Drafter.Type, &req.CreateTime, &req.UpdateTime,
	)
	if err != nil {
		return nil, err
	}
	// A row a first start made sure of but never wrote holds empty JSON until it is written.
	if specJSON != "" {
		if err = json.Unmarshal([]byte(specJSON), &req.Spec); err != nil {
			return nil, err
		}
	}
	if inputsJSON != "" {
		if err = json.Unmarshal([]byte(inputsJSON), &req.Inputs); err != nil {
			return nil, err
		}
	}
	return req, nil
}

func marshalRequirement(req *interfaces.Requirement) (string, string, error) {
	specJSON, err := json.Marshal(req.Spec)
	if err != nil {
		return "", "", err
	}
	inputsJSON, err := json.Marshal(req.Inputs)
	if err != nil {
		return "", "", err
	}
	return string(specJSON), string(inputsJSON), nil
}
