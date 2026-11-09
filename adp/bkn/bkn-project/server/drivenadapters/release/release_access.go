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
)

var (
	raOnce   sync.Once
	raAccess interfaces.ReleaseAccess
)

var releaseColumns = []string{
	"f_id", "f_lineage_id", "f_network_id", "f_version", "f_base_version", "f_project_id",
	"f_publisher", "f_publisher_type", "f_notes", "f_state", "f_kn_version", "f_branch_commit",
	"f_draft_revision", "f_capabilities", "f_create_time", "f_update_time",
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
	if query.State != "" {
		builder = builder.Where(sq.Eq{"f_state": query.State})
	}
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

func (ra *releaseAccess) GetRelease(ctx context.Context, lineageID string, version string) (*interfaces.Release, bool, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "GetRelease")
	defer span.End()

	sqlStr, vals, err := sq.Select(releaseColumns...).From(RELEASE_TABLE_NAME).
		Where(sq.Eq{"f_lineage_id": lineageID, "f_version": version}).
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

// PrepareRelease keeps the id of a prepared release it replaces, so the regression recorded under
// that id stays the release's.
func (ra *releaseAccess) PrepareRelease(ctx context.Context, release *interfaces.Release) (bool, error) {
	_, span := oteltrace.StartNamedClientSpan(ctx, "PrepareRelease")
	defer span.End()

	capabilities, err := json.Marshal(release.Capabilities)
	if err != nil {
		span.SetStatus(codes.Error, "Marshal capabilities failed")
		return false, err
	}
	existing, found, err := ra.GetRelease(ctx, release.LineageID, release.Version)
	if err != nil {
		span.SetStatus(codes.Error, "Query data failed")
		return false, err
	}
	if found && existing.State != interfaces.RELEASE_STATE_PREPARED {
		span.SetStatus(codes.Ok, "")
		return false, nil
	}
	if found {
		release.ID, release.CreateTime = existing.ID, existing.CreateTime
		sqlStr, vals, err := sq.Update(RELEASE_TABLE_NAME).SetMap(map[string]any{
			"f_network_id": release.NetworkID, "f_base_version": release.BaseVersion, "f_project_id": release.ProjectID,
			"f_publisher": release.Publisher.ID, "f_publisher_type": release.Publisher.Type, "f_notes": release.Notes,
			"f_branch_commit": release.BranchCommit, "f_draft_revision": release.DraftRevision,
			"f_capabilities": string(capabilities), "f_update_time": release.UpdateTime,
		}).Where(sq.Eq{"f_id": existing.ID, "f_state": interfaces.RELEASE_STATE_PREPARED}).ToSql()
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
		release.State = interfaces.RELEASE_STATE_PREPARED
		span.SetStatus(codes.Ok, "")
		return err == nil && n == 1, err
	}
	sqlStr, vals, err := sq.Insert(RELEASE_TABLE_NAME).
		Columns(releaseColumns...).
		Values(
			release.ID, release.LineageID, release.NetworkID, release.Version, release.BaseVersion, release.ProjectID,
			release.Publisher.ID, release.Publisher.Type, release.Notes, interfaces.RELEASE_STATE_PREPARED, 0,
			release.BranchCommit, release.DraftRevision, string(capabilities), release.CreateTime, release.UpdateTime,
		).
		ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return false, err
	}
	if _, err = ra.db.ExecContext(ctx, sqlStr, vals...); err != nil {
		// A row of the version written meanwhile, by a concurrent preparing, means it is taken.
		if _, found, readErr := ra.GetRelease(ctx, release.LineageID, release.Version); readErr == nil && found {
			span.SetStatus(codes.Ok, "")
			return false, nil
		}
		span.SetStatus(codes.Error, "Insert data failed")
		return false, err
	}
	release.State = interfaces.RELEASE_STATE_PREPARED
	span.SetStatus(codes.Ok, "")
	return true, nil
}

func (ra *releaseAccess) MarkPublished(ctx context.Context, releaseID string, knVersion int, notes string, updateTime int64) error {
	_, span := oteltrace.StartNamedClientSpan(ctx, "MarkPublished")
	defer span.End()

	sqlStr, vals, err := sq.Update(RELEASE_TABLE_NAME).SetMap(map[string]any{
		"f_state": interfaces.RELEASE_STATE_PUBLISHED, "f_kn_version": knVersion, "f_notes": notes, "f_update_time": updateTime,
	}).Where(sq.Eq{"f_id": releaseID, "f_state": interfaces.RELEASE_STATE_PREPARED}).ToSql()
	if err != nil {
		span.SetStatus(codes.Error, "Build sql failed")
		return err
	}
	if _, err = ra.db.ExecContext(ctx, sqlStr, vals...); err != nil {
		span.SetStatus(codes.Error, "Update data failed")
		return err
	}
	span.SetStatus(codes.Ok, "")
	return nil
}

// scanRelease reads one row selected with releaseColumns.
func scanRelease(row interface{ Scan(dest ...any) error }) (*interfaces.Release, error) {
	release := &interfaces.Release{}
	var capabilities string
	err := row.Scan(
		&release.ID, &release.LineageID, &release.NetworkID, &release.Version, &release.BaseVersion, &release.ProjectID,
		&release.Publisher.ID, &release.Publisher.Type, &release.Notes, &release.State, &release.KNVersion,
		&release.BranchCommit, &release.DraftRevision, &capabilities, &release.CreateTime, &release.UpdateTime,
	)
	if err != nil {
		return nil, err
	}
	if capabilities != "" {
		if err := json.Unmarshal([]byte(capabilities), &release.Capabilities); err != nil {
			return nil, fmt.Errorf("read functions and Skills of release %s: %w", release.ID, err)
		}
	}
	return release, nil
}
