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

// CONVERSATION_TABLE_NAME holds the Ask AI conversations of the kinetic modeling and business
// scenarios pages, one row each.
const CONVERSATION_TABLE_NAME = "t_bkn_project_requirement_conversation"

var conversationColumns = []string{
	"f_id", "f_project_id", "f_page", "f_title", "f_turns", "f_creator", "f_creator_type",
	"f_create_time", "f_update_time",
}

func (ra *requirementAccess) GetSupplement(ctx context.Context, projectID string) (*interfaces.RequirementSupplement, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "GetRequirementSupplement")
	defer span.End()

	sqlStr, vals, err := sq.Select("f_supplement").From(REQUIREMENT_TABLE_NAME).
		Where(sq.Eq{"f_project_id": projectID}).ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return nil, err
	}
	var raw string
	supplement := &interfaces.RequirementSupplement{}
	err = ra.db.QueryRowContext(ctx, sqlStr, vals...).Scan(&raw)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		span.SetStatus(codes.Error, "Query data failed")
		return nil, err
	case raw != "":
		if err = json.Unmarshal([]byte(raw), supplement); err != nil {
			span.SetStatus(codes.Error, "Unmarshal supplement failed")
			return nil, err
		}
	}
	span.SetStatus(codes.Ok, "")
	return supplement, nil
}

func (ra *requirementAccess) SetSupplement(ctx context.Context, projectID string,
	supplement *interfaces.RequirementSupplement) error {
	_, span := oteltrace.StartNamedClientSpan(ctx, "SetRequirementSupplement")
	defer span.End()

	raw, err := json.Marshal(supplement)
	if err != nil {
		span.SetStatus(codes.Error, "Marshal supplement failed")
		return err
	}
	// A project without a specification gets a row of the note alone, which reads as no
	// specification until a run writes one.
	sqlStr, vals, err := sq.Insert(REQUIREMENT_TABLE_NAME).Columns("f_project_id", "f_spec", "f_inputs", "f_supplement").
		Values(projectID, "", "", string(raw)).Suffix("ON DUPLICATE KEY UPDATE f_supplement = VALUES(f_supplement)").
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return err
	}
	if _, err = ra.db.ExecContext(ctx, sqlStr, vals...); err != nil {
		span.SetStatus(codes.Error, "Write data failed")
		return err
	}
	span.SetStatus(codes.Ok, "")
	return nil
}

func (ra *requirementAccess) ListConversations(ctx context.Context, projectID string,
	page string) ([]*interfaces.RequirementConversation, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "ListRequirementConversations")
	defer span.End()

	sqlStr, vals, err := sq.Select(conversationColumns...).From(CONVERSATION_TABLE_NAME).
		Where(sq.Eq{"f_project_id": projectID, "f_page": page}).
		OrderBy("f_update_time DESC", "f_id DESC").ToSql()
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
	list := []*interfaces.RequirementConversation{}
	for rows.Next() {
		c, err := scanConversation(rows)
		if err != nil {
			span.SetStatus(codes.Error, "Scan row failed")
			return nil, err
		}
		list = append(list, c)
	}
	if err = rows.Err(); err != nil {
		span.SetStatus(codes.Error, "Read rows failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return list, nil
}

func (ra *requirementAccess) GetConversation(ctx context.Context, projectID string,
	conversationID string) (*interfaces.RequirementConversation, bool, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "GetRequirementConversation")
	defer span.End()

	sqlStr, vals, err := sq.Select(conversationColumns...).From(CONVERSATION_TABLE_NAME).
		Where(sq.Eq{"f_project_id": projectID, "f_id": conversationID}).ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return nil, false, err
	}
	c, err := scanConversation(ra.db.QueryRowContext(ctx, sqlStr, vals...))
	if errors.Is(err, sql.ErrNoRows) {
		span.SetStatus(codes.Ok, "")
		return nil, false, nil
	}
	if err != nil {
		span.SetStatus(codes.Error, "Query data failed")
		return nil, false, err
	}
	span.SetStatus(codes.Ok, "")
	return c, true, nil
}

func (ra *requirementAccess) CreateConversation(ctx context.Context, c *interfaces.RequirementConversation) error {
	_, span := oteltrace.StartNamedClientSpan(ctx, "CreateRequirementConversation")
	defer span.End()

	turns, err := json.Marshal(c.Turns)
	if err != nil {
		span.SetStatus(codes.Error, "Marshal turns failed")
		return err
	}
	sqlStr, vals, err := sq.Insert(CONVERSATION_TABLE_NAME).Columns(conversationColumns...).
		Values(c.ID, c.ProjectID, c.Page, c.Title, string(turns), c.Creator.ID, c.Creator.Type,
			c.CreateTime, c.UpdateTime).ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return err
	}
	if _, err = ra.db.ExecContext(ctx, sqlStr, vals...); err != nil {
		span.SetStatus(codes.Error, "Write data failed")
		return err
	}
	span.SetStatus(codes.Ok, "")
	return nil
}

func (ra *requirementAccess) UpdateConversation(ctx context.Context, c *interfaces.RequirementConversation,
	readUpdateTime int64) (bool, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "UpdateRequirementConversation")
	defer span.End()

	turns, err := json.Marshal(c.Turns)
	if err != nil {
		span.SetStatus(codes.Error, "Marshal turns failed")
		return false, err
	}
	sqlStr, vals, err := sq.Update(CONVERSATION_TABLE_NAME).
		Set("f_title", c.Title).Set("f_turns", string(turns)).Set("f_update_time", c.UpdateTime).
		Where(sq.Eq{"f_project_id": c.ProjectID, "f_id": c.ID, "f_update_time": readUpdateTime}).ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return false, err
	}
	return ra.execAffected(ctx, span, sqlStr, vals)
}

func (ra *requirementAccess) DeleteConversation(ctx context.Context, projectID string, conversationID string) (bool, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "DeleteRequirementConversation")
	defer span.End()

	sqlStr, vals, err := sq.Delete(CONVERSATION_TABLE_NAME).
		Where(sq.Eq{"f_project_id": projectID, "f_id": conversationID}).ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return false, err
	}
	return ra.execAffected(ctx, span, sqlStr, vals)
}

func scanConversation(row interface{ Scan(dest ...any) error }) (*interfaces.RequirementConversation, error) {
	c := &interfaces.RequirementConversation{}
	var turns string
	if err := row.Scan(&c.ID, &c.ProjectID, &c.Page, &c.Title, &turns, &c.Creator.ID, &c.Creator.Type,
		&c.CreateTime, &c.UpdateTime); err != nil {
		return nil, err
	}
	c.Turns = []*interfaces.RequirementConversationTurn{}
	if turns != "" {
		if err := json.Unmarshal([]byte(turns), &c.Turns); err != nil {
			return nil, err
		}
	}
	return c, nil
}
