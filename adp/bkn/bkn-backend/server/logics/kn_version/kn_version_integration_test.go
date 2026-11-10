// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

//go:build integration

// Runs the version service against a real MariaDB with the 0.2.2 schema. Point it at a
// throwaway server only:
//
//	BKN_IT_DB_HOST=127.0.0.1 BKN_IT_DB_PORT=13399 BKN_IT_DB_USER=root \
//	BKN_IT_DB_PASSWORD=... BKN_IT_DB_NAME=v22new \
//	go test -tags=integration ./logics/kn_version/
package kn_version

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	libdb "github.com/openbkn-ai/bkn-foundry/comm-go/db"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	versionaccess "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/drivenadapters/kn_version"
	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

type allowAll struct{ interfaces.PermissionService }

func (allowAll) CheckPermission(context.Context, interfaces.PermissionResource, []string) error {
	return nil
}

// txPublisher replaces main in a plain transaction, as the KN service does without a proxy.
type txPublisher struct{ db *sql.DB }

func (p txPublisher) PublishKNMain(ctx context.Context, _, _ string, mutate func(context.Context, *sql.Tx) error) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := mutate(ctx, tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (p txPublisher) ReplaceKNDraft(ctx context.Context, knID string, mutate func(context.Context, *sql.Tx) error) error {
	return p.PublishKNMain(ctx, knID, "", mutate)
}

type fixture struct {
	t    *testing.T
	db   *sql.DB
	svc  *service
	knID string
	ctx  context.Context
	full bool
}

func setup(t *testing.T) *fixture {
	t.Helper()
	host := os.Getenv("BKN_IT_DB_HOST")
	if host == "" {
		t.Skip("BKN_IT_DB_HOST is not set")
	}
	port, err := strconv.Atoi(os.Getenv("BKN_IT_DB_PORT"))
	if err != nil {
		t.Fatalf("BKN_IT_DB_PORT: %v", err)
	}
	setting := &common.AppSetting{DBSetting: libdb.DBSetting{
		Host: host, Port: port, Username: os.Getenv("BKN_IT_DB_USER"),
		Password: os.Getenv("BKN_IT_DB_PASSWORD"), DBName: os.Getenv("BKN_IT_DB_NAME"),
	}}
	db := libdb.NewDB(&setting.DBSetting)
	f := &fixture{t: t, db: db, knID: fmt.Sprintf("it_ver_%d", time.Now().UnixNano()), full: true,
		ctx: context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY,
			interfaces.AccountInfo{ID: "u1", Type: "user"})}
	f.svc = &service{
		db: db, va: versionaccess.NewKNVersionAccessWithDB(db), bc: versionaccess.NewBranchCopier(setting),
		ps: allowAll{}, pub: txPublisher{db: db},
		fullHistory: func() bool { return f.full },
		now:         func() int64 { return time.Now().UnixMilli() },
	}
	t.Cleanup(func() {
		for _, q := range []string{
			"DELETE FROM t_object_type_status WHERE f_kn_id = ?",
			"DELETE FROM t_object_type WHERE f_kn_id = ?",
			"DELETE FROM t_knowledge_network WHERE f_id = ?",
			"DELETE FROM t_kn_version_state WHERE f_kn_id = ?",
			"DELETE FROM t_kn_version WHERE f_kn_id = ?",
			"DELETE FROM t_kn_version_event WHERE f_kn_id = ?",
		} {
			if _, err := db.Exec(q, f.knID); err != nil {
				t.Errorf("%s: %v", q, err)
			}
		}
	})
	f.exec("INSERT INTO t_knowledge_network (f_id, f_name, f_branch, f_comment, f_bkn_raw_content, f_skill_content) VALUES (?, ?, 'main', '', '', 'skill-1')",
		f.knID, f.knID)
	f.exec("INSERT INTO t_object_type (f_kn_id, f_id, f_name, f_branch, f_comment, f_bkn_raw_content, f_data_source, f_revision) VALUES (?, 'ot1', 'name-1', 'main', '', '', '', 'r1')", f.knID)
	f.exec("INSERT INTO t_object_type_status (f_kn_id, f_id, f_branch) VALUES (?, 'ot1', 'main')", f.knID)
	return f
}

func (f *fixture) exec(query string, args ...any) {
	f.t.Helper()
	if _, err := f.db.Exec(query, args...); err != nil {
		f.t.Fatalf("%s: %v", query, err)
	}
}

func (f *fixture) objectName(branch string) string {
	f.t.Helper()
	var name string
	err := f.db.QueryRow("SELECT f_name FROM t_object_type WHERE f_kn_id = ? AND f_id = 'ot1' AND f_branch = ?", f.knID, branch).Scan(&name)
	if err == sql.ErrNoRows {
		return ""
	}
	if err != nil {
		f.t.Fatal(err)
	}
	return name
}

// editDraft renames the object type on the draft the way a draft write does.
func (f *fixture) editDraft(name string) int64 {
	f.t.Helper()
	tx, err := f.db.Begin()
	if err != nil {
		f.t.Fatal(err)
	}
	revision, ok, err := f.svc.va.MarkDraftWritten(f.ctx, tx, f.knID, nil)
	if err != nil || !ok {
		f.t.Fatalf("mark draft written: %v %v", ok, err)
	}
	if _, err := tx.Exec("UPDATE t_object_type SET f_name = ? WHERE f_kn_id = ? AND f_branch = 'draft'", name, f.knID); err != nil {
		f.t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		f.t.Fatal(err)
	}
	return revision
}

func (f *fixture) state() *interfaces.KNVersioningView {
	f.t.Helper()
	view, err := f.svc.GetState(f.ctx, f.knID)
	if err != nil {
		f.t.Fatal(err)
	}
	return view
}

func (f *fixture) status(version int) string {
	f.t.Helper()
	v, err := f.svc.GetVersion(f.ctx, f.knID, version)
	if err != nil {
		f.t.Fatal(err)
	}
	return v.Status
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	var httpErr *httperrors.HTTPError
	if !errors.As(err, &httpErr) || httpErr.ErrorCode != code {
		t.Fatalf("error = %v, want %s", err, code)
	}
}

func ptr[T any](v T) *T { return &v }

func (f *fixture) publish(running int, revision int64, key string) (*interfaces.KNPublishResult, error) {
	return f.svc.Publish(f.ctx, f.knID, &interfaces.KNPublishRequest{
		ExpectedRunningVersion: ptr(running), ExpectedDraftRevision: ptr(revision), IdempotencyKey: key,
	})
}

func TestVersionLifecycle(t *testing.T) {
	f := setup(t)

	view, err := f.svc.Enable(f.ctx, f.knID, &interfaces.KNEnableVersioningRequest{Label: "first"})
	if err != nil {
		t.Fatal(err)
	}
	if view.RunningVersion != 1 || view.Draft.HasChanges || view.Draft.Revision != 0 {
		t.Fatalf("after enable: %+v %+v", view, view.Draft)
	}
	if f.objectName("v1") != "name-1" || f.objectName("draft") != "name-1" {
		t.Fatal("enable did not copy main to v1 and draft")
	}
	_, err = f.svc.Enable(f.ctx, f.knID, &interfaces.KNEnableVersioningRequest{})
	wantCode(t, err, berrors.BknBackend_KNVersion_AlreadyVersioned)

	_, err = f.publish(1, 0, "")
	wantCode(t, err, berrors.BknBackend_KNVersion_NothingToPublish)

	revision := f.editDraft("name-2")
	_, err = f.publish(1, revision-1, "")
	wantCode(t, err, berrors.BknBackend_KNVersion_DraftRevisionMismatch)
	_, err = f.publish(0, revision, "")
	wantCode(t, err, berrors.BknBackend_KNVersion_RunningVersionMismatch)

	result, err := f.publish(1, revision, "pub-2")
	if err != nil {
		t.Fatal(err)
	}
	if result.Version != 2 || result.BaseVersion != 1 || f.objectName("main") != "name-2" || f.objectName("v2") != "name-2" {
		t.Fatalf("publish: version %d base %d main %q", result.Version, result.BaseVersion, f.objectName("main"))
	}
	if f.status(1) != interfaces.KNVersionHistory || f.status(2) != interfaces.KNVersionRunning {
		t.Fatal("publish did not move v1 to history")
	}
	// The same key returns the first result, even though the running version has moved.
	again, err := f.publish(1, revision, "pub-2")
	if err != nil || again.Version != 2 {
		t.Fatalf("idempotent publish: %v %v", again, err)
	}
	_, err = f.svc.Activate(f.ctx, f.knID, 1, &interfaces.KNActivateRequest{ExpectedRunningVersion: ptr(2), IdempotencyKey: "pub-2"})
	wantCode(t, err, berrors.BknBackend_KNVersion_IdempotencyKeyReused)

	// Roll back to v1: main follows, the draft stays and is outdated.
	view, err = f.svc.Activate(f.ctx, f.knID, 1, &interfaces.KNActivateRequest{ExpectedRunningVersion: ptr(2)})
	if err != nil {
		t.Fatal(err)
	}
	if view.RunningVersion != 1 || view.PreviousVersion != 2 || !view.Draft.Outdated || !view.Draft.HasChanges {
		t.Fatalf("after activate: %+v %+v", view, view.Draft)
	}
	if f.objectName("main") != "name-1" || f.objectName("draft") != "name-2" {
		t.Fatal("activate did not switch main only")
	}
	_, err = f.svc.Activate(f.ctx, f.knID, 1, &interfaces.KNActivateRequest{ExpectedRunningVersion: ptr(1)})
	wantCode(t, err, berrors.BknBackend_KNVersion_VersionNotActivatable)

	_, err = f.publish(1, revision, "")
	wantCode(t, err, berrors.BknBackend_KNVersion_DraftBaseOutdated)

	// Copy v1 into the draft: refused without overwrite, then the draft follows v1.
	_, err = f.svc.ResetDraft(f.ctx, f.knID, &interfaces.KNResetDraftRequest{FromVersion: ptr(1), ExpectedDraftRevision: ptr(revision)})
	wantCode(t, err, berrors.BknBackend_KNVersion_DraftHasChanges)
	view, err = f.svc.ResetDraft(f.ctx, f.knID, &interfaces.KNResetDraftRequest{
		FromVersion: ptr(1), ExpectedDraftRevision: ptr(revision), OverwriteChanges: true})
	if err != nil {
		t.Fatal(err)
	}
	if view.Draft.HasChanges || view.Draft.Outdated || view.Draft.Revision != revision+1 || f.objectName("draft") != "name-1" {
		t.Fatalf("after reset: %+v", view.Draft)
	}

	// Professional status changes.
	_, err = f.svc.Purge(f.ctx, f.knID, 1, &interfaces.KNVersionStatusRequest{})
	wantCode(t, err, berrors.BknBackend_KNVersion_VersionProtected)
	if _, err := f.svc.Deprecate(f.ctx, f.knID, 2, &interfaces.KNVersionStatusRequest{}); err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.Activate(f.ctx, f.knID, 2, &interfaces.KNActivateRequest{ExpectedRunningVersion: ptr(1)})
	wantCode(t, err, berrors.BknBackend_KNVersion_VersionNotActivatable)
	if _, err := f.svc.Restore(f.ctx, f.knID, 2, &interfaces.KNVersionStatusRequest{}); err != nil {
		t.Fatal(err)
	}
	v, err := f.svc.Purge(f.ctx, f.knID, 2, &interfaces.KNVersionStatusRequest{})
	if err != nil || v.Status != interfaces.KNVersionPurged || f.objectName("v2") != "" {
		t.Fatalf("purge: %v %v %q", v, err, f.objectName("v2"))
	}
	if f.state().PreviousVersion != 0 {
		t.Fatal("purging the previous version did not clear it")
	}
	_, err = f.svc.ResetDraft(f.ctx, f.knID, &interfaces.KNResetDraftRequest{FromVersion: ptr(2), ExpectedDraftRevision: ptr(revision + 1)})
	wantCode(t, err, berrors.BknBackend_KNVersion_VersionPurged)

	events, total, err := f.svc.ListEvents(f.ctx, f.knID, 0, 100)
	if err != nil || total != 7 || len(events) != 7 {
		t.Fatalf("events: %d %d %v", total, len(events), err)
	}
}

func TestCommunityEditionKeepsOnePreviousVersion(t *testing.T) {
	f := setup(t)
	f.full = false
	if _, err := f.svc.Enable(f.ctx, f.knID, &interfaces.KNEnableVersioningRequest{}); err != nil {
		t.Fatal(err)
	}
	revision := f.editDraft("name-2")
	if _, err := f.publish(1, revision, ""); err != nil {
		t.Fatal(err)
	}
	revision = f.editDraft("name-3")
	result, err := f.publish(2, revision, "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Version != 3 || result.PurgedVersion != 1 || f.status(1) != interfaces.KNVersionPurged || f.objectName("v1") != "" {
		t.Fatalf("publish v3: %+v status(1) %s", result, f.status(1))
	}
	view := f.state()
	if view.FullHistory || view.RunningVersion != 3 || view.PreviousVersion != 2 {
		t.Fatalf("state: %+v", view)
	}
	_, err = f.svc.Deprecate(f.ctx, f.knID, 2, &interfaces.KNVersionStatusRequest{})
	wantCode(t, err, berrors.BknBackend_KNVersion_EditionRestricted)
	_, err = f.svc.Activate(f.ctx, f.knID, 1, &interfaces.KNActivateRequest{ExpectedRunningVersion: ptr(3)})
	wantCode(t, err, berrors.BknBackend_KNVersion_EditionRestricted)
	// Switching swaps running and previous without purging.
	view, err = f.svc.Activate(f.ctx, f.knID, 2, &interfaces.KNActivateRequest{ExpectedRunningVersion: ptr(3)})
	if err != nil || view.RunningVersion != 2 || view.PreviousVersion != 3 || f.objectName("main") != "name-2" {
		t.Fatalf("activate previous: %+v %v", view, err)
	}
}

func TestEnableRefusesReservedBranchData(t *testing.T) {
	f := setup(t)
	f.exec("INSERT INTO t_object_type (f_kn_id, f_id, f_name, f_branch, f_comment, f_bkn_raw_content, f_data_source) VALUES (?, 'ot9', 'x', 'v3', '', '', '')", f.knID)
	_, err := f.svc.Enable(f.ctx, f.knID, &interfaces.KNEnableVersioningRequest{})
	wantCode(t, err, berrors.BknBackend_KNVersion_ReservedBranchInUse)
	if state := f.state(); state.Enabled {
		t.Fatal("enable left a state row behind")
	}
}
