// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package project

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"

	sq "github.com/Masterminds/squirrel"
	libdb "github.com/openbkn-ai/bkn-foundry/comm-go/db"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

const PROJECT_TABLE_NAME = "t_bkn_project_project"

var (
	paOnce   sync.Once
	paAccess interfaces.ProjectAccess
)

var projectColumns = []string{
	"f_id", "f_name", "f_kn_id", "f_branch", "f_baseline_network_id", "f_catalog_ids",
	"f_archived", "f_owner", "f_owner_type", "f_create_time", "f_update_time",
}

// likeEscaper makes a name match literally inside a LIKE pattern.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

type projectAccess struct {
	db *sql.DB
}

func NewProjectAccess(appSetting *common.AppSetting) interfaces.ProjectAccess {
	paOnce.Do(func() {
		paAccess = &projectAccess{db: libdb.NewDB(&appSetting.DBSetting)}
	})
	return paAccess
}

func (pa *projectAccess) CreateProject(ctx context.Context, project *interfaces.Project) error {
	_, span := oteltrace.StartNamedClientSpan(ctx, "CreateProject")
	defer span.End()

	catalogIDs := project.CatalogIDs
	if catalogIDs == nil {
		catalogIDs = []string{}
	}
	catalogJSON, err := json.Marshal(catalogIDs)
	if err != nil {
		span.SetStatus(codes.Error, "Marshal catalog ids failed")
		return err
	}
	sqlStr, vals, err := sq.Insert(PROJECT_TABLE_NAME).
		Columns(projectColumns...).
		Values(
			project.ID, project.Name, project.KNID, project.Branch, project.BaselineNetworkID, string(catalogJSON),
			project.Archived, project.Owner.ID, project.Owner.Type, project.CreateTime, project.UpdateTime,
		).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return err
	}
	if _, err = pa.db.ExecContext(ctx, sqlStr, vals...); err != nil {
		span.SetStatus(codes.Error, "Insert data failed")
		return err
	}
	span.SetStatus(codes.Ok, "")
	return nil
}

func (pa *projectAccess) GetProject(ctx context.Context, id string) (*interfaces.Project, bool, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "GetProject")
	defer span.End()

	sqlStr, vals, err := sq.Select(projectColumns...).From(PROJECT_TABLE_NAME).
		Where(sq.Eq{"f_id": id}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return nil, false, err
	}
	project, err := scanProject(pa.db.QueryRowContext(ctx, sqlStr, vals...))
	if errors.Is(err, sql.ErrNoRows) {
		span.SetStatus(codes.Ok, "")
		return nil, false, nil
	}
	if err != nil {
		span.SetStatus(codes.Error, "Query data failed")
		return nil, false, err
	}
	span.SetStatus(codes.Ok, "")
	return project, true, nil
}

func (pa *projectAccess) ListProjects(ctx context.Context, query interfaces.ProjectsQuery) ([]*interfaces.Project, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "ListProjects")
	defer span.End()

	projects := []*interfaces.Project{}
	if len(query.KNIDs) == 0 {
		span.SetStatus(codes.Ok, "")
		return projects, nil
	}
	builder := filterProjects(sq.Select(projectColumns...).From(PROJECT_TABLE_NAME), query)
	if query.Cursor != nil {
		builder = builder.Where(sq.Or{
			sq.Lt{"f_create_time": query.Cursor.CreateTime},
			sq.And{sq.Eq{"f_create_time": query.Cursor.CreateTime}, sq.Lt{"f_id": query.Cursor.ID}},
		})
	}
	sqlStr, vals, err := builder.OrderBy("f_create_time DESC", "f_id DESC").
		Limit(uint64(query.Limit)).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return nil, err
	}
	rows, err := pa.db.QueryContext(ctx, sqlStr, vals...)
	if err != nil {
		span.SetStatus(codes.Error, "Query data failed")
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		project, err := scanProject(rows)
		if err != nil {
			span.SetStatus(codes.Error, "Scan data failed")
			return nil, err
		}
		projects = append(projects, project)
	}
	if err = rows.Err(); err != nil {
		span.SetStatus(codes.Error, "Iterate rows failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return projects, nil
}

func (pa *projectAccess) CountProjects(ctx context.Context, query interfaces.ProjectsQuery) (int64, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "CountProjects")
	defer span.End()

	if len(query.KNIDs) == 0 {
		span.SetStatus(codes.Ok, "")
		return 0, nil
	}
	sqlStr, vals, err := filterProjects(sq.Select("COUNT(f_id)").From(PROJECT_TABLE_NAME), query).ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return 0, err
	}
	var total int64
	if err = pa.db.QueryRowContext(ctx, sqlStr, vals...).Scan(&total); err != nil {
		span.SetStatus(codes.Error, "Query data failed")
		return 0, err
	}
	span.SetStatus(codes.Ok, "")
	return total, nil
}

func (pa *projectAccess) CountUnarchivedProjects(ctx context.Context, knIDs []string) (map[string]int64, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "CountUnarchivedProjects")
	defer span.End()

	counts := map[string]int64{}
	if len(knIDs) == 0 {
		span.SetStatus(codes.Ok, "")
		return counts, nil
	}
	sqlStr, vals, err := sq.Select("f_kn_id", "COUNT(f_id)").From(PROJECT_TABLE_NAME).
		Where(sq.Eq{"f_kn_id": knIDs, "f_archived": false}).
		GroupBy("f_kn_id").
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return nil, err
	}
	rows, err := pa.db.QueryContext(ctx, sqlStr, vals...)
	if err != nil {
		span.SetStatus(codes.Error, "Query data failed")
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var knID string
		var count int64
		if err = rows.Scan(&knID, &count); err != nil {
			span.SetStatus(codes.Error, "Scan data failed")
			return nil, err
		}
		counts[knID] = count
	}
	if err = rows.Err(); err != nil {
		span.SetStatus(codes.Error, "Iterate rows failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return counts, nil
}

func (pa *projectAccess) SetProjectArchived(ctx context.Context, id string, archived bool, updateTime int64) error {
	_, span := oteltrace.StartNamedClientSpan(ctx, "SetProjectArchived")
	defer span.End()

	sqlStr, vals, err := sq.Update(PROJECT_TABLE_NAME).
		Set("f_archived", archived).Set("f_update_time", updateTime).
		Where(sq.Eq{"f_id": id}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return err
	}
	if _, err = pa.db.ExecContext(ctx, sqlStr, vals...); err != nil {
		span.SetStatus(codes.Error, "Update data failed")
		return err
	}
	span.SetStatus(codes.Ok, "")
	return nil
}

func (pa *projectAccess) SetProjectCatalogs(ctx context.Context, id string, catalogIDs []string,
	updateTime int64) error {
	_, span := oteltrace.StartNamedClientSpan(ctx, "SetProjectCatalogs")
	defer span.End()

	if catalogIDs == nil {
		catalogIDs = []string{}
	}
	catalogJSON, err := json.Marshal(catalogIDs)
	if err != nil {
		span.SetStatus(codes.Error, "Marshal catalog ids failed")
		return err
	}
	sqlStr, vals, err := sq.Update(PROJECT_TABLE_NAME).
		Set("f_catalog_ids", string(catalogJSON)).Set("f_update_time", updateTime).
		Where(sq.Eq{"f_id": id}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return err
	}
	if _, err = pa.db.ExecContext(ctx, sqlStr, vals...); err != nil {
		span.SetStatus(codes.Error, "Update data failed")
		return err
	}
	span.SetStatus(codes.Ok, "")
	return nil
}

// filterProjects adds the networks, archive state and name of query.
func filterProjects(builder sq.SelectBuilder, query interfaces.ProjectsQuery) sq.SelectBuilder {
	builder = builder.Where(sq.Eq{"f_kn_id": query.KNIDs})
	if query.Archived != nil {
		builder = builder.Where(sq.Eq{"f_archived": *query.Archived})
	}
	if query.Name != "" {
		builder = builder.Where(sq.Expr(`f_name LIKE ? ESCAPE '\\'`, "%"+likeEscaper.Replace(query.Name)+"%"))
	}
	return builder
}

// scanProject reads one row selected with projectColumns.
func scanProject(row interface{ Scan(dest ...any) error }) (*interfaces.Project, error) {
	project := &interfaces.Project{}
	var catalogJSON string
	err := row.Scan(
		&project.ID, &project.Name, &project.KNID, &project.Branch, &project.BaselineNetworkID, &catalogJSON,
		&project.Archived, &project.Owner.ID, &project.Owner.Type, &project.CreateTime, &project.UpdateTime,
	)
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal([]byte(catalogJSON), &project.CatalogIDs); err != nil {
		return nil, err
	}
	return project, nil
}
