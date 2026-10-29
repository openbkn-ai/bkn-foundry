// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package indexbuild

import (
	"context"
	"database/sql"
	"sync"

	sq "github.com/Masterminds/squirrel"
	libdb "github.com/openbkn-ai/bkn-foundry/comm-go/db"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

const INDEX_BUILD_TABLE_NAME = "t_bkn_project_index_build"

// batchSize keeps a statement well under the 65535 placeholders MySQL allows.
const batchSize = 500

var (
	ibaOnce   sync.Once
	ibaAccess interfaces.IndexBuildAccess
)

var buildColumns = []string{
	"f_project_id", "f_commit", "f_resource_id", "f_object_type_id", "f_task_id", "f_outcome", "f_reason",
	"f_runner", "f_runner_type", "f_create_time",
}

// onDuplicate replaces what an earlier build of the same commit and table recorded.
const onDuplicate = "ON DUPLICATE KEY UPDATE f_object_type_id = VALUES(f_object_type_id), f_task_id = VALUES(f_task_id), " +
	"f_outcome = VALUES(f_outcome), f_reason = VALUES(f_reason), f_runner = VALUES(f_runner), " +
	"f_runner_type = VALUES(f_runner_type), f_create_time = VALUES(f_create_time)"

type indexBuildAccess struct {
	db *sql.DB
}

func NewIndexBuildAccess(appSetting *common.AppSetting) interfaces.IndexBuildAccess {
	ibaOnce.Do(func() {
		ibaAccess = &indexBuildAccess{db: libdb.NewDB(&appSetting.DBSetting)}
	})
	return ibaAccess
}

func (iba *indexBuildAccess) RecordBuilds(ctx context.Context, builds []*interfaces.IndexBuild) error {
	_, span := oteltrace.StartNamedClientSpan(ctx, "RecordBuilds")
	defer span.End()

	for start := 0; start < len(builds); start += batchSize {
		builder := sq.Insert(INDEX_BUILD_TABLE_NAME).Columns(buildColumns...).Suffix(onDuplicate)
		for _, b := range builds[start:min(start+batchSize, len(builds))] {
			builder = builder.Values(b.ProjectID, b.Commit, b.ResourceID, b.ObjectTypeID, b.TaskID, b.Outcome, b.Reason,
				b.Runner.ID, b.Runner.Type, b.CreateTime)
		}
		sqlStr, vals, err := builder.ToSql()
		if err != nil {
			span.SetStatus(codes.Error, "Build sql failed")
			return err
		}
		if _, err := iba.db.ExecContext(ctx, sqlStr, vals...); err != nil {
			span.SetStatus(codes.Error, "Insert data failed")
			return err
		}
	}
	span.SetStatus(codes.Ok, "")
	return nil
}

func (iba *indexBuildAccess) ListBuilds(ctx context.Context, projectID string, commit string) ([]*interfaces.IndexBuild, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "ListBuilds")
	defer span.End()

	sqlStr, vals, err := sq.Select(buildColumns...).From(INDEX_BUILD_TABLE_NAME).
		Where(sq.Eq{"f_project_id": projectID, "f_commit": commit}).
		OrderBy("f_resource_id").
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return nil, err
	}
	rows, err := iba.db.QueryContext(ctx, sqlStr, vals...)
	if err != nil {
		span.SetStatus(codes.Error, "Query data failed")
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	builds := []*interfaces.IndexBuild{}
	for rows.Next() {
		b := &interfaces.IndexBuild{}
		if err := rows.Scan(&b.ProjectID, &b.Commit, &b.ResourceID, &b.ObjectTypeID, &b.TaskID, &b.Outcome, &b.Reason,
			&b.Runner.ID, &b.Runner.Type, &b.CreateTime); err != nil {
			span.SetStatus(codes.Error, "Scan row failed")
			return nil, err
		}
		builds = append(builds, b)
	}
	if err := rows.Err(); err != nil {
		span.SetStatus(codes.Error, "Read rows failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return builds, nil
}
