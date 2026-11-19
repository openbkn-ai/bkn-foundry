// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package diagnosis

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

const DIAGNOSIS_TABLE_NAME = "t_bkn_project_diagnosis"

var (
	daOnce   sync.Once
	daAccess interfaces.DiagnosisAccess
)

var diagnosisColumns = []string{
	"f_project_id", "f_run_id", "f_state", "f_runner", "f_runner_type", "f_result", "f_error",
	"f_draft_update_time", "f_start_time", "f_finish_time",
}

type diagnosisAccess struct {
	db *sql.DB
}

func NewDiagnosisAccess(appSetting *common.AppSetting) interfaces.DiagnosisAccess {
	daOnce.Do(func() {
		daAccess = &diagnosisAccess{db: libdb.NewDB(&appSetting.DBSetting)}
	})
	return daAccess
}

// diagnosisResult is what the result column holds: the parts of a diagnosis without columns of their own.
type diagnosisResult struct {
	ComputedAt  int64                             `json:"computed_at"`
	Metrics     interfaces.DiagnosisMetrics       `json:"metrics"`
	Components  [][]string                        `json:"components"`
	Suggestions []*interfaces.DiagnosisSuggestion `json:"suggestions"`
	AI          bool                              `json:"ai"`
}

func resultOf(d *interfaces.Diagnosis) (string, error) {
	raw, err := json.Marshal(diagnosisResult{ComputedAt: d.ComputedAt, Metrics: d.Metrics, Components: d.Components,
		Suggestions: d.Suggestions, AI: d.AI})
	return string(raw), err
}

func (da *diagnosisAccess) GetDiagnosis(ctx context.Context, projectID string) (*interfaces.Diagnosis, bool, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "GetDiagnosis")
	defer span.End()

	sqlStr, vals, err := sq.Select(diagnosisColumns...).From(DIAGNOSIS_TABLE_NAME).
		Where(sq.Eq{"f_project_id": projectID}).ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return nil, false, err
	}
	d := &interfaces.Diagnosis{}
	var result string
	err = da.db.QueryRowContext(ctx, sqlStr, vals...).Scan(&d.ProjectID, &d.RunID, &d.State, &d.Runner.ID,
		&d.Runner.Type, &result, &d.Error, &d.DraftUpdateTime, &d.StartTime, &d.FinishTime)
	if errors.Is(err, sql.ErrNoRows) {
		span.SetStatus(codes.Ok, "")
		return nil, false, nil
	}
	if err != nil {
		span.SetStatus(codes.Error, "Query data failed")
		return nil, false, err
	}
	if result != "" {
		var r diagnosisResult
		if err := json.Unmarshal([]byte(result), &r); err != nil {
			span.SetStatus(codes.Error, "Unmarshal result failed")
			return nil, false, err
		}
		d.ComputedAt, d.Metrics, d.Components, d.Suggestions, d.AI = r.ComputedAt, r.Metrics, r.Components, r.Suggestions, r.AI
	}
	span.SetStatus(codes.Ok, "")
	return d, true, nil
}

func (da *diagnosisAccess) StartDiagnosis(ctx context.Context, d *interfaces.Diagnosis,
	staleBefore int64) (started bool, err error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "StartDiagnosis")
	defer span.End()

	result, err := resultOf(d)
	if err != nil {
		span.SetStatus(codes.Error, "Marshal result failed")
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
	// The row lock makes two starts of one project take turns, as a check's start does.
	sqlStr, vals, err := sq.Insert(DIAGNOSIS_TABLE_NAME).Columns("f_project_id", "f_result", "f_error").
		Values(d.ProjectID, "", "").Suffix("ON DUPLICATE KEY UPDATE f_project_id = f_project_id").ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return false, err
	}
	if _, err = tx.ExecContext(ctx, sqlStr, vals...); err != nil {
		span.SetStatus(codes.Error, "Write data failed")
		return false, err
	}
	sqlStr, vals, err = sq.Select("f_state", "f_start_time").From(DIAGNOSIS_TABLE_NAME).
		Where(sq.Eq{"f_project_id": d.ProjectID}).Suffix("FOR UPDATE").ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return false, err
	}
	var state string
	var startTime int64
	if err = tx.QueryRowContext(ctx, sqlStr, vals...).Scan(&state, &startTime); err != nil {
		span.SetStatus(codes.Error, "Query data failed")
		return false, err
	}
	if state == interfaces.DIAGNOSIS_STATE_RUNNING && startTime >= staleBefore {
		span.SetStatus(codes.Ok, "")
		return false, nil
	}
	sqlStr, vals, err = sq.Update(DIAGNOSIS_TABLE_NAME).
		Set("f_run_id", d.RunID).Set("f_state", d.State).Set("f_runner", d.Runner.ID).
		Set("f_runner_type", d.Runner.Type).Set("f_result", result).Set("f_error", d.Error).
		Set("f_draft_update_time", d.DraftUpdateTime).Set("f_start_time", d.StartTime).
		Set("f_finish_time", d.FinishTime).
		Where(sq.Eq{"f_project_id": d.ProjectID}).ToSql()
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

func (da *diagnosisAccess) SaveDiagnosis(ctx context.Context, d *interfaces.Diagnosis) (bool, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "SaveDiagnosis")
	defer span.End()

	result, err := resultOf(d)
	if err != nil {
		span.SetStatus(codes.Error, "Marshal result failed")
		return false, err
	}
	sqlStr, vals, err := sq.Update(DIAGNOSIS_TABLE_NAME).
		Set("f_state", d.State).Set("f_result", result).Set("f_error", d.Error).
		Set("f_draft_update_time", d.DraftUpdateTime).Set("f_finish_time", d.FinishTime).
		Where(sq.Eq{"f_project_id": d.ProjectID, "f_run_id": d.RunID}).ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return false, err
	}
	res, err := da.db.ExecContext(ctx, sqlStr, vals...)
	if err != nil {
		span.SetStatus(codes.Error, "Update data failed")
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		span.SetStatus(codes.Error, "Read affected rows failed")
		return false, err
	}
	span.SetStatus(codes.Ok, "")
	return n > 0, nil
}
