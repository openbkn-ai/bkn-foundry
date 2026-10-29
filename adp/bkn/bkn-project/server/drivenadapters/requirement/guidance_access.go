// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	sq "github.com/Masterminds/squirrel"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

const GUIDANCE_TABLE_NAME = "t_bkn_project_question_guidance"

var guidanceColumns = []string{
	"f_project_id", "f_question_id", "f_spec_version", "f_question", "f_status", "f_tests", "f_guidance",
	"f_ontology_signature", "f_out_of_scope", "f_no_tests_reason", "f_left_out_tests", "f_deleted_tests", "f_error", "f_run_id", "f_agent_task_id",
	"f_runner", "f_runner_type", "f_create_time", "f_update_time",
}

func (ra *requirementAccess) GetGuidance(ctx context.Context, projectID string,
	questionID string) (*interfaces.QuestionGuidance, bool, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "GetQuestionGuidance")
	defer span.End()

	sqlStr, vals, err := sq.Select(guidanceColumns...).From(GUIDANCE_TABLE_NAME).
		Where(sq.Eq{"f_project_id": projectID}).Where(sq.Eq{"f_question_id": questionID}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return nil, false, err
	}
	g, err := scanGuidance(ra.db.QueryRowContext(ctx, sqlStr, vals...))
	if errors.Is(err, sql.ErrNoRows) {
		span.SetStatus(codes.Ok, "")
		return nil, false, nil
	}
	if err != nil {
		span.SetStatus(codes.Error, "Query data failed")
		return nil, false, err
	}
	span.SetStatus(codes.Ok, "")
	return g, true, nil
}

func (ra *requirementAccess) ListGuidance(ctx context.Context, projectID string) ([]*interfaces.QuestionGuidance, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "ListQuestionGuidance")
	defer span.End()

	sqlStr, vals, err := sq.Select(guidanceColumns...).From(GUIDANCE_TABLE_NAME).
		Where(sq.Eq{"f_project_id": projectID}).OrderBy("f_question_id").
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
	list := []*interfaces.QuestionGuidance{}
	for rows.Next() {
		g, err := scanGuidance(rows)
		if err != nil {
			span.SetStatus(codes.Error, "Scan data failed")
			return nil, err
		}
		list = append(list, g)
	}
	if err = rows.Err(); err != nil {
		span.SetStatus(codes.Error, "Query data failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return list, nil
}

// StartGenerating takes turns on the row as StartDrafting does on the specification's.
func (ra *requirementAccess) StartGenerating(ctx context.Context, g *interfaces.QuestionGuidance,
	staleBefore int64) (started bool, err error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "StartQuestionGuidance")
	defer span.End()

	testsJSON, outOfScopeJSON, err := marshalGuidance(g)
	if err != nil {
		span.SetStatus(codes.Error, "Marshal guidance failed")
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

	sqlStr, vals, err := sq.Insert(GUIDANCE_TABLE_NAME).
		Columns("f_project_id", "f_question_id", "f_question", "f_tests", "f_guidance", "f_out_of_scope").
		Values(g.ProjectID, g.QuestionID, "", "", "", "").
		Suffix("ON DUPLICATE KEY UPDATE f_project_id = f_project_id").
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return false, err
	}
	if _, err = tx.ExecContext(ctx, sqlStr, vals...); err != nil {
		span.SetStatus(codes.Error, "Write data failed")
		return false, err
	}
	sqlStr, vals, err = sq.Select("f_status", "f_update_time").From(GUIDANCE_TABLE_NAME).
		Where(sq.Eq{"f_project_id": g.ProjectID}).Where(sq.Eq{"f_question_id": g.QuestionID}).Suffix("FOR UPDATE").
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return false, err
	}
	var status string
	var updateTime int64
	if err = tx.QueryRowContext(ctx, sqlStr, vals...).Scan(&status, &updateTime); err != nil {
		span.SetStatus(codes.Error, "Query data failed")
		return false, err
	}
	if status == interfaces.GUIDANCE_STATUS_GENERATING && updateTime >= staleBefore {
		span.SetStatus(codes.Ok, "")
		return false, nil
	}
	sqlStr, vals, err = sq.Update(GUIDANCE_TABLE_NAME).
		Set("f_spec_version", g.SpecVersion).Set("f_question", g.Question).Set("f_status", g.Status).
		Set("f_tests", testsJSON).Set("f_guidance", g.Guidance).Set("f_ontology_signature", g.OntologySignature).
		Set("f_out_of_scope", outOfScopeJSON).Set("f_no_tests_reason", g.NoTestsReason).
		Set("f_left_out_tests", g.LeftOutTests).Set("f_deleted_tests", g.DeletedTests).Set("f_error", g.Error).
		Set("f_run_id", g.RunID).Set("f_agent_task_id", g.AgentTaskID).
		Set("f_runner", g.Runner.ID).Set("f_runner_type", g.Runner.Type).
		Set("f_create_time", g.CreateTime).Set("f_update_time", g.UpdateTime).
		Where(sq.Eq{"f_project_id": g.ProjectID}).Where(sq.Eq{"f_question_id": g.QuestionID}).
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

func (ra *requirementAccess) UpdateGuidance(ctx context.Context, g *interfaces.QuestionGuidance,
	readUpdateTime int64) (bool, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "UpdateQuestionGuidance")
	defer span.End()

	testsJSON, _, err := marshalGuidance(g)
	if err != nil {
		span.SetStatus(codes.Error, "Marshal guidance failed")
		return false, err
	}
	sqlStr, vals, err := sq.Update(GUIDANCE_TABLE_NAME).
		Set("f_tests", testsJSON).Set("f_deleted_tests", g.DeletedTests).Set("f_update_time", g.UpdateTime).
		Where(sq.Eq{"f_project_id": g.ProjectID}).Where(sq.Eq{"f_question_id": g.QuestionID}).
		Where(sq.Eq{"f_status": interfaces.GUIDANCE_STATUS_READY}).
		Where(sq.Eq{"f_update_time": readUpdateTime}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return false, err
	}
	return ra.execAffected(ctx, span, sqlStr, vals)
}

func (ra *requirementAccess) FinishGenerating(ctx context.Context, g *interfaces.QuestionGuidance) (bool, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "FinishQuestionGuidance")
	defer span.End()

	testsJSON, outOfScopeJSON, err := marshalGuidance(g)
	if err != nil {
		span.SetStatus(codes.Error, "Marshal guidance failed")
		return false, err
	}
	sqlStr, vals, err := sq.Update(GUIDANCE_TABLE_NAME).
		Set("f_status", g.Status).Set("f_tests", testsJSON).Set("f_guidance", g.Guidance).
		Set("f_ontology_signature", g.OntologySignature).Set("f_out_of_scope", outOfScopeJSON).
		Set("f_no_tests_reason", g.NoTestsReason).Set("f_left_out_tests", g.LeftOutTests).
		Set("f_error", g.Error).Set("f_agent_task_id", g.AgentTaskID).Set("f_update_time", g.UpdateTime).
		Where(sq.Eq{"f_project_id": g.ProjectID}).Where(sq.Eq{"f_question_id": g.QuestionID}).
		Where(sq.Eq{"f_status": interfaces.GUIDANCE_STATUS_GENERATING}).
		Where(sq.Eq{"f_run_id": g.RunID}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return false, err
	}
	return ra.execAffected(ctx, span, sqlStr, vals)
}

func (ra *requirementAccess) FailGenerating(ctx context.Context, reason string, updateTime int64) (int64, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "FailQuestionGuidance")
	defer span.End()

	sqlStr, vals, err := sq.Update(GUIDANCE_TABLE_NAME).
		Set("f_status", interfaces.GUIDANCE_STATUS_FAILED).Set("f_error", reason).Set("f_update_time", updateTime).
		Where(sq.Eq{"f_status": interfaces.GUIDANCE_STATUS_GENERATING}).
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

// marshalGuidance writes the tests and the questions out of scope as JSON arrays, empty when none.
func marshalGuidance(g *interfaces.QuestionGuidance) (string, string, error) {
	tests := g.Tests
	if tests == nil {
		tests = []*interfaces.QuestionTest{}
	}
	outOfScope := g.OutOfScope
	if outOfScope == nil {
		outOfScope = []string{}
	}
	testsJSON, err := json.Marshal(tests)
	if err != nil {
		return "", "", err
	}
	outOfScopeJSON, err := json.Marshal(outOfScope)
	if err != nil {
		return "", "", err
	}
	return string(testsJSON), string(outOfScopeJSON), nil
}

func scanGuidance(row interface{ Scan(dest ...any) error }) (*interfaces.QuestionGuidance, error) {
	g := &interfaces.QuestionGuidance{}
	var testsJSON, outOfScopeJSON string
	err := row.Scan(
		&g.ProjectID, &g.QuestionID, &g.SpecVersion, &g.Question, &g.Status, &testsJSON, &g.Guidance,
		&g.OntologySignature, &outOfScopeJSON, &g.NoTestsReason, &g.LeftOutTests, &g.DeletedTests, &g.Error, &g.RunID, &g.AgentTaskID,
		&g.Runner.ID, &g.Runner.Type, &g.CreateTime, &g.UpdateTime,
	)
	if err != nil {
		return nil, err
	}
	g.Tests, g.OutOfScope = []*interfaces.QuestionTest{}, []string{}
	// A row a first start made sure of but never wrote holds empty JSON until it is written.
	if testsJSON != "" {
		if err = json.Unmarshal([]byte(testsJSON), &g.Tests); err != nil {
			return nil, err
		}
	}
	if outOfScopeJSON != "" {
		if err = json.Unmarshal([]byte(outOfScopeJSON), &g.OutOfScope); err != nil {
			return nil, err
		}
	}
	return g, nil
}
