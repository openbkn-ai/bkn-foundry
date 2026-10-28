// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package check

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

const CHECK_TABLE_NAME = "t_bkn_project_check"

var (
	caOnce   sync.Once
	caAccess interfaces.CheckAccess
)

var checkColumns = []string{
	"f_project_id", "f_commit", "f_status", "f_run_id", "f_attempt", "f_runner", "f_runner_type",
	"f_input", "f_results", "f_error", "f_start_time", "f_finish_time",
}

type checkAccess struct {
	db *sql.DB
}

func NewCheckAccess(appSetting *common.AppSetting) interfaces.CheckAccess {
	caOnce.Do(func() {
		caAccess = &checkAccess{db: libdb.NewDB(&appSetting.DBSetting)}
	})
	return caAccess
}

func (ca *checkAccess) GetCheck(ctx context.Context, projectID string) (*interfaces.Check, bool, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "GetCheck")
	defer span.End()

	sqlStr, vals, err := sq.Select(checkColumns...).From(CHECK_TABLE_NAME).
		Where(sq.Eq{"f_project_id": projectID}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return nil, false, err
	}
	check := &interfaces.Check{}
	var input, results string
	err = ca.db.QueryRowContext(ctx, sqlStr, vals...).Scan(
		&check.ProjectID, &check.Commit, &check.Status, &check.RunID, &check.Attempt, &check.Runner.ID,
		&check.Runner.Type, &input, &results, &check.Error, &check.StartTime, &check.FinishTime,
	)
	if errors.Is(err, sql.ErrNoRows) {
		span.SetStatus(codes.Ok, "")
		return nil, false, nil
	}
	if err != nil {
		span.SetStatus(codes.Error, "Query data failed")
		return nil, false, err
	}
	check.Input, check.Results = rawJSON(input), rawJSON(results)
	span.SetStatus(codes.Ok, "")
	return check, true, nil
}

func (ca *checkAccess) StartCheck(ctx context.Context, check *interfaces.Check,
	staleBefore int64) (started bool, err error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "StartCheck")
	defer span.End()

	tx, err := ca.db.BeginTx(ctx, nil)
	if err != nil {
		span.SetStatus(codes.Error, "Begin transaction failed")
		return false, err
	}
	defer func() {
		if err != nil || !started {
			_ = tx.Rollback()
		}
	}()

	// The row lock makes two starts of one project take turns: the second sees the first running.
	sqlStr, vals, err := sq.Select("f_commit", "f_status", "f_start_time").From(CHECK_TABLE_NAME).
		Where(sq.Eq{"f_project_id": check.ProjectID}).Suffix("FOR UPDATE").
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return false, err
	}
	var commit, status string
	var startTime int64
	switch err = tx.QueryRowContext(ctx, sqlStr, vals...).Scan(&commit, &status, &startTime); {
	case errors.Is(err, sql.ErrNoRows):
		sqlStr, vals, err = sq.Insert(CHECK_TABLE_NAME).Columns(checkColumns...).Values(
			check.ProjectID, check.Commit, check.Status, check.RunID, check.Attempt, check.Runner.ID,
			check.Runner.Type, text(check.Input), text(check.Results), check.Error, check.StartTime,
			check.FinishTime,
		).ToSql()
	case err != nil:
		span.SetStatus(codes.Error, "Query data failed")
		return false, err
	case status == interfaces.CHECK_STATUS_RUNNING && commit == check.Commit && startTime >= staleBefore:
		span.SetStatus(codes.Ok, "")
		return false, nil
	default:
		sqlStr, vals, err = sq.Update(CHECK_TABLE_NAME).
			Set("f_commit", check.Commit).Set("f_status", check.Status).Set("f_run_id", check.RunID).
			Set("f_attempt", check.Attempt).Set("f_runner", check.Runner.ID).
			Set("f_runner_type", check.Runner.Type).Set("f_input", text(check.Input)).
			Set("f_results", text(check.Results)).Set("f_error", check.Error).
			Set("f_start_time", check.StartTime).Set("f_finish_time", check.FinishTime).
			Where(sq.Eq{"f_project_id": check.ProjectID}).
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

func (ca *checkAccess) FinishCheck(ctx context.Context, check *interfaces.Check) (bool, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "FinishCheck")
	defer span.End()

	sqlStr, vals, err := sq.Update(CHECK_TABLE_NAME).
		Set("f_status", check.Status).Set("f_attempt", check.Attempt).Set("f_results", text(check.Results)).
		Set("f_error", check.Error).Set("f_finish_time", check.FinishTime).
		Where(sq.Eq{
			"f_project_id": check.ProjectID,
			"f_run_id":     check.RunID,
			"f_status":     interfaces.CHECK_STATUS_RUNNING,
		}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return false, err
	}
	result, err := ca.db.ExecContext(ctx, sqlStr, vals...)
	if err != nil {
		span.SetStatus(codes.Error, "Update data failed")
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

func (ca *checkAccess) FailRunning(ctx context.Context, reason string, finishTime int64) (int64, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "FailRunning")
	defer span.End()

	sqlStr, vals, err := sq.Update(CHECK_TABLE_NAME).
		Set("f_status", interfaces.CHECK_STATUS_FAILED).Set("f_error", reason).Set("f_finish_time", finishTime).
		Where(sq.Eq{"f_status": interfaces.CHECK_STATUS_RUNNING}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return 0, err
	}
	result, err := ca.db.ExecContext(ctx, sqlStr, vals...)
	if err != nil {
		span.SetStatus(codes.Error, "Update data failed")
		return 0, err
	}
	span.SetStatus(codes.Ok, "")
	return result.RowsAffected()
}

// text writes JSON for a NOT NULL text column; nothing is stored as an empty string.
func text(data json.RawMessage) string {
	return string(data)
}

// rawJSON reads a JSON column back; an empty column reads as no JSON.
func rawJSON(data string) json.RawMessage {
	if data == "" {
		return nil
	}
	return json.RawMessage(data)
}
