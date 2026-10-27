// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package release

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	sq "github.com/Masterminds/squirrel"
	libdb "github.com/openbkn-ai/bkn-foundry/comm-go/db"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

const (
	RELEASE_TABLE_NAME = "t_bkn_project_release"
	SWITCH_TABLE_NAME  = "t_bkn_project_switch"
)

var (
	raOnce   sync.Once
	raAccess interfaces.ReleaseAccess
)

// f_state is left out: a release's state is worked out when it is read (see
// interfaces.Release), and the column keeps its default.
var releaseColumns = []string{
	"f_id", "f_lineage_id", "f_network_id", "f_version", "f_base_version", "f_project_id",
	"f_publisher", "f_publisher_type", "f_notes", "f_create_time", "f_update_time",
}

var switchColumns = []string{
	"f_id", "f_lineage_id", "f_from_network_id", "f_to_network_id",
	"f_operator", "f_operator_type", "f_schedules", "f_create_time",
}

type releaseAccess struct {
	db *sql.DB
}

func NewReleaseAccess(appSetting *common.AppSetting) interfaces.ReleaseAccess {
	raOnce.Do(func() {
		raAccess = &releaseAccess{db: libdb.NewDB(&appSetting.DBSetting)}
	})
	return raAccess
}

func (ra *releaseAccess) ListReleases(ctx context.Context, query interfaces.ReleasesQuery) ([]*interfaces.Release, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "ListReleases")
	defer span.End()

	builder := sq.Select(releaseColumns...).From(RELEASE_TABLE_NAME).
		Where(sq.Eq{"f_lineage_id": query.LineageID})
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
	rows, err := ra.db.QueryContext(ctx, sqlStr, vals...)
	if err != nil {
		span.SetStatus(codes.Error, "Query data failed")
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	releases := []*interfaces.Release{}
	for rows.Next() {
		release, err := scanRelease(rows)
		if err != nil {
			span.SetStatus(codes.Error, "Scan data failed")
			return nil, err
		}
		releases = append(releases, release)
	}
	if err = rows.Err(); err != nil {
		span.SetStatus(codes.Error, "Iterate rows failed")
		return nil, err
	}
	span.SetStatus(codes.Ok, "")
	return releases, nil
}

func (ra *releaseAccess) CountReleases(ctx context.Context, lineageID string) (int64, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "CountReleases")
	defer span.End()

	sqlStr, vals, err := sq.Select("COUNT(f_id)").From(RELEASE_TABLE_NAME).
		Where(sq.Eq{"f_lineage_id": lineageID}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return 0, err
	}
	var total int64
	if err = ra.db.QueryRowContext(ctx, sqlStr, vals...).Scan(&total); err != nil {
		span.SetStatus(codes.Error, "Query data failed")
		return 0, err
	}
	span.SetStatus(codes.Ok, "")
	return total, nil
}

func (ra *releaseAccess) GetReleaseByNetworkID(ctx context.Context, networkID string) (*interfaces.Release, bool, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "GetReleaseByNetworkID")
	defer span.End()

	sqlStr, vals, err := sq.Select(releaseColumns...).From(RELEASE_TABLE_NAME).
		Where(sq.Eq{"f_network_id": networkID}).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return nil, false, err
	}
	release, err := scanRelease(ra.db.QueryRowContext(ctx, sqlStr, vals...))
	if errors.Is(err, sql.ErrNoRows) {
		span.SetStatus(codes.Ok, "")
		return nil, false, nil
	}
	if err != nil {
		span.SetStatus(codes.Error, "Query data failed")
		return nil, false, err
	}
	span.SetStatus(codes.Ok, "")
	return release, true, nil
}

func (ra *releaseAccess) CreateRelease(ctx context.Context, release *interfaces.Release) error {
	_, span := oteltrace.StartNamedClientSpan(ctx, "CreateRelease")
	defer span.End()

	sqlStr, vals, err := sq.Insert(RELEASE_TABLE_NAME).
		Columns(releaseColumns...).
		Values(
			release.ID, release.LineageID, release.NetworkID, release.Version, release.BaseVersion, release.ProjectID,
			release.Publisher.ID, release.Publisher.Type, release.Notes, release.CreateTime, release.UpdateTime,
		).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return err
	}
	if _, err = ra.db.ExecContext(ctx, sqlStr, vals...); err != nil {
		span.SetStatus(codes.Error, "Insert data failed")
		return err
	}
	span.SetStatus(codes.Ok, "")
	return nil
}

func (ra *releaseAccess) GetLatestSwitch(ctx context.Context, lineageID string) (*interfaces.Switch, bool, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "GetLatestSwitch")
	defer span.End()

	sqlStr, vals, err := sq.Select(switchColumns...).From(SWITCH_TABLE_NAME).
		Where(sq.Eq{"f_lineage_id": lineageID}).
		OrderBy("f_create_time DESC", "f_id DESC").
		Limit(1).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return nil, false, err
	}
	sw := &interfaces.Switch{}
	var schedules string
	err = ra.db.QueryRowContext(ctx, sqlStr, vals...).Scan(
		&sw.ID, &sw.LineageID, &sw.FromNetworkID, &sw.ToNetworkID,
		&sw.Operator.ID, &sw.Operator.Type, &schedules, &sw.CreateTime,
	)
	if errors.Is(err, sql.ErrNoRows) {
		span.SetStatus(codes.Ok, "")
		return nil, false, nil
	}
	if err != nil {
		span.SetStatus(codes.Error, "Query data failed")
		return nil, false, err
	}
	if err := json.Unmarshal([]byte(schedules), &sw.Schedules); err != nil {
		span.SetStatus(codes.Error, "Unmarshal schedules failed")
		return nil, false, fmt.Errorf("unmarshal schedules of switch %s: %w", sw.ID, err)
	}
	span.SetStatus(codes.Ok, "")
	return sw, true, nil
}

func (ra *releaseAccess) RecordSwitch(ctx context.Context, sw *interfaces.Switch) error {
	_, span := oteltrace.StartNamedClientSpan(ctx, "RecordSwitch")
	defer span.End()

	schedules := sw.Schedules
	if schedules == nil {
		schedules = []interfaces.SwitchedSchedule{}
	}
	scheduleJSON, err := json.Marshal(schedules)
	if err != nil {
		span.SetStatus(codes.Error, "Marshal schedules failed")
		return err
	}
	sqlStr, vals, err := sq.Insert(SWITCH_TABLE_NAME).Columns(switchColumns...).Values(
		sw.ID, sw.LineageID, sw.FromNetworkID, sw.ToNetworkID,
		sw.Operator.ID, sw.Operator.Type, string(scheduleJSON), sw.CreateTime,
	).ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return err
	}
	if _, err = ra.db.ExecContext(ctx, sqlStr, vals...); err != nil {
		span.SetStatus(codes.Error, "Insert data failed")
		return err
	}
	span.SetStatus(codes.Ok, "")
	return nil
}

// scanRelease reads one row selected with releaseColumns.
func scanRelease(row interface{ Scan(dest ...any) error }) (*interfaces.Release, error) {
	release := &interfaces.Release{}
	err := row.Scan(
		&release.ID, &release.LineageID, &release.NetworkID, &release.Version, &release.BaseVersion, &release.ProjectID,
		&release.Publisher.ID, &release.Publisher.Type, &release.Notes, &release.CreateTime, &release.UpdateTime,
	)
	if err != nil {
		return nil, err
	}
	return release, nil
}
