// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

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

const DRAFT_TABLE_NAME = "t_bkn_project_draft"

var (
	daOnce   sync.Once
	daAccess interfaces.DraftAccess
)

var draftColumns = []string{
	"f_project_id", "f_status", "f_items", "f_error", "f_drafter", "f_drafter_type",
	"f_create_time", "f_update_time",
}

type draftAccess struct {
	db *sql.DB
}

func NewDraftAccess(appSetting *common.AppSetting) interfaces.DraftAccess {
	daOnce.Do(func() {
		daAccess = &draftAccess{db: libdb.NewDB(&appSetting.DBSetting)}
	})
	return daAccess
}

func (da *draftAccess) GetDraft(ctx context.Context, projectID string) (*interfaces.Draft, bool, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "GetDraft")
	defer span.End()

	sqlStr, vals, err := sq.Select(draftColumns...).From(DRAFT_TABLE_NAME).
		Where(sq.Eq{"f_project_id": projectID}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return nil, false, err
	}
	draft, err := scanDraft(da.db.QueryRowContext(ctx, sqlStr, vals...))
	if errors.Is(err, sql.ErrNoRows) {
		span.SetStatus(codes.Ok, "")
		return nil, false, nil
	}
	if err != nil {
		span.SetStatus(codes.Error, "Query data failed")
		return nil, false, err
	}
	span.SetStatus(codes.Ok, "")
	return draft, true, nil
}

func (da *draftAccess) StartDraft(ctx context.Context, draft *interfaces.Draft,
	staleBefore int64) (started bool, err error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "StartDraft")
	defer span.End()

	itemsJSON, err := json.Marshal(draft.Items)
	if err != nil {
		span.SetStatus(codes.Error, "Marshal items failed")
		return false, err
	}
	tx, err := da.db.BeginTx(ctx, nil)
	if err != nil {
		span.SetStatus(codes.Error, "Begin transaction failed")
		return false, err
	}
	defer func() {
		if err != nil || !started {
			_ = tx.Rollback()
		}
	}()

	// The row lock makes two starts of one project take turns: the second sees drafting.
	sqlStr, vals, err := sq.Select("f_status", "f_update_time").From(DRAFT_TABLE_NAME).
		Where(sq.Eq{"f_project_id": draft.ProjectID}).Suffix("FOR UPDATE").
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return false, err
	}
	var status string
	var updateTime int64
	switch err = tx.QueryRowContext(ctx, sqlStr, vals...).Scan(&status, &updateTime); {
	case errors.Is(err, sql.ErrNoRows):
		sqlStr, vals, err = sq.Insert(DRAFT_TABLE_NAME).Columns(draftColumns...).Values(
			draft.ProjectID, draft.Status, string(itemsJSON), draft.Error, draft.Drafter.ID, draft.Drafter.Type,
			draft.CreateTime, draft.UpdateTime,
		).ToSql()
	case err != nil:
		span.SetStatus(codes.Error, "Query data failed")
		return false, err
	case status == interfaces.DRAFT_STATUS_DRAFTING && updateTime >= staleBefore:
		span.SetStatus(codes.Ok, "")
		return false, nil
	default:
		sqlStr, vals, err = sq.Update(DRAFT_TABLE_NAME).
			Set("f_status", draft.Status).Set("f_items", string(itemsJSON)).Set("f_error", draft.Error).
			Set("f_drafter", draft.Drafter.ID).Set("f_drafter_type", draft.Drafter.Type).
			Set("f_create_time", draft.CreateTime).Set("f_update_time", draft.UpdateTime).
			Where(sq.Eq{"f_project_id": draft.ProjectID}).
			ToSql()
	}
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
	span.SetStatus(codes.Ok, "")
	return true, nil
}

func (da *draftAccess) FinishDraft(ctx context.Context, draft *interfaces.Draft) error {
	_, span := oteltrace.StartNamedClientSpan(ctx, "FinishDraft")
	defer span.End()

	itemsJSON, err := json.Marshal(draft.Items)
	if err != nil {
		span.SetStatus(codes.Error, "Marshal items failed")
		return err
	}
	sqlStr, vals, err := sq.Update(DRAFT_TABLE_NAME).
		Set("f_status", draft.Status).Set("f_items", string(itemsJSON)).Set("f_error", draft.Error).
		Set("f_update_time", draft.UpdateTime).
		Where(sq.Eq{"f_project_id": draft.ProjectID, "f_status": interfaces.DRAFT_STATUS_DRAFTING}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return err
	}
	if _, err = da.db.ExecContext(ctx, sqlStr, vals...); err != nil {
		span.SetStatus(codes.Error, "Update data failed")
		return err
	}
	span.SetStatus(codes.Ok, "")
	return nil
}

func (da *draftAccess) FailDrafting(ctx context.Context, reason string, updateTime int64) (int64, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "FailDrafting")
	defer span.End()

	sqlStr, vals, err := sq.Update(DRAFT_TABLE_NAME).
		Set("f_status", interfaces.DRAFT_STATUS_FAILED).Set("f_error", reason).Set("f_update_time", updateTime).
		Where(sq.Eq{"f_status": interfaces.DRAFT_STATUS_DRAFTING}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return 0, err
	}
	result, err := da.db.ExecContext(ctx, sqlStr, vals...)
	if err != nil {
		span.SetStatus(codes.Error, "Update data failed")
		return 0, err
	}
	span.SetStatus(codes.Ok, "")
	return result.RowsAffected()
}

// scanDraft reads one row selected with draftColumns.
func scanDraft(row interface{ Scan(dest ...any) error }) (*interfaces.Draft, error) {
	draft := &interfaces.Draft{}
	var itemsJSON string
	err := row.Scan(
		&draft.ProjectID, &draft.Status, &itemsJSON, &draft.Error, &draft.Drafter.ID, &draft.Drafter.Type,
		&draft.CreateTime, &draft.UpdateTime,
	)
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal([]byte(itemsJSON), &draft.Items); err != nil {
		return nil, err
	}
	return draft, nil
}
