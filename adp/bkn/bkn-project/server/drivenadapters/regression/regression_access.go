// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package regression

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

const REGRESSION_TABLE_NAME = "t_bkn_project_regression"

var (
	raOnce   sync.Once
	raAccess interfaces.RegressionAccess
)

var regressionColumns = []string{
	"f_network_id", "f_project_id", "f_version", "f_status", "f_run_id", "f_attempt", "f_runner", "f_runner_type",
	"f_questions", "f_results", "f_error", "f_start_time", "f_finish_time",
}

type regressionAccess struct {
	db *sql.DB
}

func NewRegressionAccess(appSetting *common.AppSetting) interfaces.RegressionAccess {
	raOnce.Do(func() {
		raAccess = &regressionAccess{db: libdb.NewDB(&appSetting.DBSetting)}
	})
	return raAccess
}

func (ra *regressionAccess) GetRegression(ctx context.Context, networkID string) (*interfaces.Regression, bool, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "GetRegression")
	defer span.End()

	sqlStr, vals, err := sq.Select(regressionColumns...).From(REGRESSION_TABLE_NAME).
		Where(sq.Eq{"f_network_id": networkID}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return nil, false, err
	}
	r := &interfaces.Regression{}
	var questions, results string
	err = ra.db.QueryRowContext(ctx, sqlStr, vals...).Scan(
		&r.NetworkID, &r.ProjectID, &r.Version, &r.Status, &r.RunID, &r.Attempt, &r.Runner.ID, &r.Runner.Type,
		&questions, &results, &r.Error, &r.StartTime, &r.FinishTime,
	)
	if errors.Is(err, sql.ErrNoRows) {
		span.SetStatus(codes.Ok, "")
		return nil, false, nil
	}
	if err != nil {
		span.SetStatus(codes.Error, "Query data failed")
		return nil, false, err
	}
	r.Questions, r.Results = []interfaces.RegressionQuestion{}, []interfaces.RegressionResult{}
	if err := readJSON(questions, &r.Questions); err != nil {
		span.SetStatus(codes.Error, "Unmarshal questions failed")
		return nil, false, err
	}
	if err := readJSON(results, &r.Results); err != nil {
		span.SetStatus(codes.Error, "Unmarshal results failed")
		return nil, false, err
	}
	span.SetStatus(codes.Ok, "")
	return r, true, nil
}

func (ra *regressionAccess) StartRegression(ctx context.Context, r *interfaces.Regression,
	staleBefore int64) (started bool, err error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "StartRegression")
	defer span.End()

	questions, err := writeJSON(r.Questions)
	if err != nil {
		span.SetStatus(codes.Error, "Marshal questions failed")
		return false, err
	}
	results, err := writeJSON(r.Results)
	if err != nil {
		span.SetStatus(codes.Error, "Marshal results failed")
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

	// The row lock makes two starts of one release take turns: the second sees the first running.
	// A release's first start has no row to lock, so the row is made sure of first.
	sqlStr, vals, err := sq.Insert(REGRESSION_TABLE_NAME).Columns("f_network_id", "f_questions", "f_results").
		Values(r.NetworkID, "", "").Suffix("ON DUPLICATE KEY UPDATE f_network_id = f_network_id").
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return false, err
	}
	if _, err = tx.ExecContext(ctx, sqlStr, vals...); err != nil {
		span.SetStatus(codes.Error, "Write data failed")
		return false, err
	}
	sqlStr, vals, err = sq.Select("f_status", "f_start_time").From(REGRESSION_TABLE_NAME).
		Where(sq.Eq{"f_network_id": r.NetworkID}).Suffix("FOR UPDATE").
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return false, err
	}
	var status string
	var startTime int64
	if err = tx.QueryRowContext(ctx, sqlStr, vals...).Scan(&status, &startTime); err != nil {
		span.SetStatus(codes.Error, "Query data failed")
		return false, err
	}
	if status == interfaces.REGRESSION_STATUS_RUNNING && startTime >= staleBefore {
		span.SetStatus(codes.Ok, "")
		return false, nil
	}
	sqlStr, vals, err = sq.Update(REGRESSION_TABLE_NAME).
		Set("f_project_id", r.ProjectID).Set("f_version", r.Version).Set("f_status", r.Status).
		Set("f_run_id", r.RunID).Set("f_attempt", r.Attempt).Set("f_runner", r.Runner.ID).
		Set("f_runner_type", r.Runner.Type).Set("f_questions", questions).Set("f_results", results).
		Set("f_error", r.Error).Set("f_start_time", r.StartTime).Set("f_finish_time", r.FinishTime).
		Where(sq.Eq{"f_network_id": r.NetworkID}).
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
	span.SetStatus(codes.Ok, "")
	return true, nil
}

func (ra *regressionAccess) FinishRegression(ctx context.Context, r *interfaces.Regression) (bool, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "FinishRegression")
	defer span.End()

	results, err := writeJSON(r.Results)
	if err != nil {
		span.SetStatus(codes.Error, "Marshal results failed")
		return false, err
	}
	sqlStr, vals, err := sq.Update(REGRESSION_TABLE_NAME).
		Set("f_status", r.Status).Set("f_attempt", r.Attempt).Set("f_results", results).
		Set("f_error", r.Error).Set("f_finish_time", r.FinishTime).
		Where(sq.Eq{
			"f_network_id": r.NetworkID,
			"f_run_id":     r.RunID,
			"f_status":     interfaces.REGRESSION_STATUS_RUNNING,
		}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return false, err
	}
	result, err := ra.db.ExecContext(ctx, sqlStr, vals...)
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

func (ra *regressionAccess) FailRunning(ctx context.Context, reason string, finishTime int64) (int64, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "FailRunningRegressions")
	defer span.End()

	sqlStr, vals, err := sq.Update(REGRESSION_TABLE_NAME).
		Set("f_status", interfaces.REGRESSION_STATUS_FAILED).Set("f_error", reason).Set("f_finish_time", finishTime).
		Where(sq.Eq{"f_status": interfaces.REGRESSION_STATUS_RUNNING}).
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

// writeJSON writes a list for a NOT NULL text column; no list is written as [].
func writeJSON[T any](list []T) (string, error) {
	if list == nil {
		list = []T{}
	}
	data, err := json.Marshal(list)
	return string(data), err
}

// readJSON reads a list column back; an empty column, as a row made sure of has, leaves it empty.
func readJSON(data string, into any) error {
	if data == "" {
		return nil
	}
	return json.Unmarshal([]byte(data), into)
}
