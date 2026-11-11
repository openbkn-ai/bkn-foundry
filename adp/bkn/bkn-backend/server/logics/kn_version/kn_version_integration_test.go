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
	"slices"
	"strconv"
	"strings"
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
// problems, when set, is what CheckReferences reports for each branch.
type txPublisher struct {
	db       *sql.DB
	problems map[string][]*interfaces.KNVersionProblem
	// indexDeletes records each concept index deletion as "<branch>:<committed version status>".
	indexDeletes *[]string
}

func (txPublisher) BranchReferences(_ context.Context, _, branch string) (*interfaces.KNBranchReferences, error) {
	return &interfaces.KNBranchReferences{DataResources: []string{"res-" + branch, "shared", "shared"}}, nil
}

func (txPublisher) IndexVersion(context.Context, string, string, string) error { return nil }

func (p txPublisher) DeleteBranchIndex(_ context.Context, knID, branch string) error {
	if p.indexDeletes == nil {
		return nil
	}
	// A separate connection sees only what is committed.
	var status string
	err := p.db.QueryRow("SELECT f_status FROM t_kn_version WHERE f_kn_id = ? AND f_version = ?",
		knID, strings.TrimPrefix(branch, "v")).Scan(&status)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	*p.indexDeletes = append(*p.indexDeletes, branch+":"+status)
	return nil
}

func (p txPublisher) CheckReferences(_ context.Context, _, branch string) ([]*interfaces.KNVersionProblem, error) {
	return p.problems[branch], nil
}

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

	indexDeletes []string
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
		ps: allowAll{}, pub: txPublisher{db: db, problems: map[string][]*interfaces.KNVersionProblem{}, indexDeletes: &f.indexDeletes},
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

func TestDisableKeepsRunningVersionOnMain(t *testing.T) {
	f := setup(t)
	if _, err := f.svc.Enable(f.ctx, f.knID, &interfaces.KNEnableVersioningRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.publish(1, f.editDraft("name-2"), ""); err != nil {
		t.Fatal(err)
	}
	f.editDraft("name-3")
	// Unpublished changes are kept unless the operator discards them.
	_, err := f.svc.Disable(f.ctx, f.knID, &interfaces.KNDisableVersioningRequest{})
	wantCode(t, err, berrors.BknBackend_KNVersion_DraftHasChanges)
	if !f.state().Enabled {
		t.Fatal("a refused disable changed the state")
	}
	view, err := f.svc.Disable(f.ctx, f.knID, &interfaces.KNDisableVersioningRequest{DiscardDraft: true, Notes: "rollback"})
	if err != nil || view.Enabled {
		t.Fatalf("disable: %+v %v", view, err)
	}
	tx, err := f.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	branches, err := f.svc.bc.BranchesWithData(f.ctx, tx, f.knID)
	_ = tx.Rollback()
	if err != nil || len(branches) != 1 || branches[0] != interfaces.MAIN_BRANCH || f.objectName("main") != "name-2" {
		t.Fatalf("after disable: branches %v main %q %v", branches, f.objectName("main"), err)
	}
	if f.status(1) != interfaces.KNVersionPurged || f.status(2) != interfaces.KNVersionPurged {
		t.Fatalf("versions: %s %s", f.status(1), f.status(2))
	}
	// Disabling again reports the network as not versioned; enabling again continues numbering.
	_, err = f.svc.Disable(f.ctx, f.knID, &interfaces.KNDisableVersioningRequest{})
	wantCode(t, err, berrors.BknBackend_KNVersion_NotVersioned)
	view, err = f.svc.Enable(f.ctx, f.knID, &interfaces.KNEnableVersioningRequest{})
	if err != nil || view.RunningVersion != 3 {
		t.Fatalf("enable again: %+v %v", view, err)
	}
}

// The concept syncer leaves versioned networks to the version service; it learns which
// they are from this list, so a disabled network must drop out of it.
func TestListVersionedKNIDsFollowsEnableAndDisable(t *testing.T) {
	f := setup(t)
	listed := func() bool {
		ids, err := f.svc.va.ListVersionedKNIDs(f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		return slices.Contains(ids, f.knID)
	}
	if listed() {
		t.Fatal("listed before enable")
	}
	if _, err := f.svc.Enable(f.ctx, f.knID, &interfaces.KNEnableVersioningRequest{}); err != nil {
		t.Fatal(err)
	}
	if !listed() {
		t.Fatal("not listed after enable")
	}
	if _, err := f.svc.Disable(f.ctx, f.knID, &interfaces.KNDisableVersioningRequest{}); err != nil {
		t.Fatal(err)
	}
	if listed() {
		t.Fatal("still listed after disable")
	}
}

// Disabling removes only the branches versioning added. An ordinary branch the network had
// before versioning was enabled survives.
func TestDisableKeepsOrdinaryBranches(t *testing.T) {
	f := setup(t)
	f.exec("INSERT INTO t_object_type (f_kn_id, f_id, f_name, f_branch, f_comment, f_bkn_raw_content, f_data_source) VALUES (?, 'ot1', 'dev-name', 'dev', '', '', '')", f.knID)
	if _, err := f.svc.Enable(f.ctx, f.knID, &interfaces.KNEnableVersioningRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.publish(1, f.editDraft("name-2"), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Disable(f.ctx, f.knID, &interfaces.KNDisableVersioningRequest{DiscardDraft: true}); err != nil {
		t.Fatal(err)
	}
	tx, err := f.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	branches, err := f.svc.bc.BranchesWithData(f.ctx, tx, f.knID)
	_ = tx.Rollback()
	if err != nil || !slices.Equal(branches, []string{"dev", interfaces.MAIN_BRANCH}) {
		t.Fatalf("after disable: branches %v %v, want [dev main]", branches, err)
	}
	if name := f.objectName("dev"); name != "dev-name" {
		t.Fatalf("dev branch object = %q, want dev-name", name)
	}
}

// Broken references in the branch going live refuse publish and activate; a replay of a
// publish that already succeeded is not checked again.
func TestBrokenReferencesRefuseGoingLive(t *testing.T) {
	f := setup(t)
	if _, err := f.svc.Enable(f.ctx, f.knID, &interfaces.KNEnableVersioningRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.publish(1, f.editDraft("name-2"), "pub-2"); err != nil {
		t.Fatal(err)
	}
	problems := f.svc.pub.(txPublisher).problems
	problem := &interfaces.KNVersionProblem{Category: interfaces.KNProblemBrokenReference,
		DefinitionType: interfaces.MODULE_TYPE_RELATION_TYPE, DefinitionID: "rt1"}
	problems[interfaces.DRAFT_BRANCH] = []*interfaces.KNVersionProblem{problem}
	problems[interfaces.VersionBranch(1)] = []*interfaces.KNVersionProblem{problem}

	if _, err := f.publish(1, f.state().Draft.Revision, "pub-2"); err != nil {
		t.Fatalf("replay checked again: %v", err)
	}
	_, err := f.publish(2, f.editDraft("name-3"), "")
	wantCode(t, err, berrors.BknBackend_KNVersion_ValidationFailed)
	_, err = f.svc.Activate(f.ctx, f.knID, 1, &interfaces.KNActivateRequest{ExpectedRunningVersion: ptr(2)})
	wantCode(t, err, berrors.BknBackend_KNVersion_BrokenReferences)
	if got := f.state().RunningVersion; got != 2 {
		t.Fatalf("running version after refusals = %d", got)
	}
}

func TestCheckReadEnd(t *testing.T) {
	f := setup(t)
	if _, err := f.svc.Enable(f.ctx, f.knID, &interfaces.KNEnableVersioningRequest{}); err != nil {
		t.Fatal(err)
	}
	draft, err := f.svc.ResolveBranch(f.ctx, f.knID, interfaces.KNBranchRequest{KNVersion: interfaces.DRAFT_BRANCH})
	if err != nil {
		t.Fatal(err)
	}
	v1, err := f.svc.ResolveBranch(f.ctx, f.knID, interfaces.KNBranchRequest{KNVersion: "1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.CheckReadEnd(f.ctx, f.knID, draft); err != nil {
		t.Fatalf("unchanged draft: %v", err)
	}
	if err := f.svc.CheckReadEnd(f.ctx, f.knID, v1); err != nil {
		t.Fatalf("unchanged version: %v", err)
	}
	f.editDraft("name-2")
	wantCode(t, f.svc.CheckReadEnd(f.ctx, f.knID, draft), berrors.BknBackend_KNVersion_DraftRevisionMismatch)
	f.exec("UPDATE t_kn_version SET f_status = 'purged' WHERE f_kn_id = ? AND f_version = 1", f.knID)
	wantCode(t, f.svc.CheckReadEnd(f.ctx, f.knID, v1), berrors.BknBackend_KNVersion_VersionPurged)
}

func TestReferencedResources(t *testing.T) {
	f := setup(t)
	if _, err := f.svc.Enable(f.ctx, f.knID, &interfaces.KNEnableVersioningRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.publish(1, f.editDraft("name-2"), ""); err != nil {
		t.Fatal(err)
	}
	got, err := f.svc.ReferencedResources(f.ctx, f.knID)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string][]any{}
	for _, r := range got.DataResources {
		byID[r.ResourceID] = r.Versions
	}
	want := map[string]string{"shared": "[draft 2 1]", "res-draft": "[draft]", "res-v2": "[2]", "res-v1": "[1]"}
	for id, versions := range want {
		if fmt.Sprint(byID[id]) != versions {
			t.Errorf("%s used by %v, want %s", id, byID[id], versions)
		}
	}
	if len(byID) != len(want) {
		t.Errorf("resources %v", byID)
	}
}

// fakeMetricPublisher stands in for the Enterprise publisher: apply changes the draft inside the
// publish transaction the way marking metrics published does.
type fakeMetricPublisher struct {
	f       *fixture
	plan    *interfaces.MetricPublishPlan
	planned int
	applied int
	fail    bool
}

func (p *fakeMetricPublisher) PlanPublish(_ context.Context, _, branch string) (*interfaces.MetricPublishPlan, error) {
	p.planned++
	if branch != interfaces.DRAFT_BRANCH {
		p.f.t.Errorf("planned on %s", branch)
	}
	return p.plan, nil
}

func (p *fakeMetricPublisher) ApplyPublish(ctx context.Context, tx *sql.Tx, knID, branch string, _ *interfaces.MetricPublishPlan) error {
	p.applied++
	if _, err := tx.ExecContext(ctx, "UPDATE t_object_type SET f_name = 'metrics-applied' WHERE f_kn_id = ? AND f_branch = ?", knID, branch); err != nil {
		return err
	}
	if p.fail {
		return errors.New("apply failed")
	}
	return nil
}

func TestPublishPublishesMetricsWithTheNetwork(t *testing.T) {
	f := setup(t)
	if _, err := f.svc.Enable(f.ctx, f.knID, &interfaces.KNEnableVersioningRequest{}); err != nil {
		t.Fatal(err)
	}
	mp := &fakeMetricPublisher{f: f, plan: &interfaces.MetricPublishPlan{
		Publish:  []*interfaces.MetricVersionRef{{MetricID: "m1", Version: 3}},
		Warnings: []*interfaces.KNPublishWarning{{Category: interfaces.KNWarningUnvalidatedMetric, DefinitionID: "m2", Description: "latest version is a draft"}},
	}}
	f.svc.mp = mp
	revision := f.editDraft("name-2")

	// A failing apply rolls the publish back: the draft change and version numbers stay put.
	mp.fail = true
	if _, err := f.publish(1, revision, ""); err == nil {
		t.Fatal("publish succeeded although the metrics could not be published")
	}
	if f.objectName("draft") != "name-2" || f.objectName("main") != "name-1" || f.state().RunningVersion != 1 {
		t.Fatal("a failed apply leaked into the draft, main or the state")
	}

	mp.fail = false
	result, err := f.publish(1, revision, "")
	if err != nil {
		t.Fatal(err)
	}
	if mp.applied != 2 || mp.planned != 2 {
		t.Fatalf("planned %d applied %d", mp.planned, mp.applied)
	}
	// The version carries what the apply wrote, and the response reports the plan.
	if f.objectName("v2") != "metrics-applied" || f.objectName("main") != "metrics-applied" {
		t.Fatal("v2 and main do not carry the published metrics")
	}
	if len(result.PublishedMetrics) != 1 || result.PublishedMetrics[0].Version != 3 ||
		len(result.Warnings) != 1 || result.Warnings[0].DefinitionID != "m2" {
		t.Fatalf("response: %+v %+v", result.PublishedMetrics, result.Warnings)
	}
	// The apply changed the draft, so its revision moved once; the version records the one published.
	view := f.state()
	if view.Draft.Revision != revision+1 || result.DraftRevision != revision || view.Draft.HasChanges {
		t.Fatalf("draft revision %d (published %d, was %d) changes %v", view.Draft.Revision, result.DraftRevision, revision, view.Draft.HasChanges)
	}

	// An empty plan leaves the revision alone.
	mp.plan = &interfaces.MetricPublishPlan{}
	revision = f.editDraft("name-3")
	if _, err := f.publish(2, revision, ""); err != nil {
		t.Fatal(err)
	}
	if got := f.state().Draft.Revision; got != revision || mp.applied != 2 {
		t.Fatalf("empty plan: revision %d want %d, applied %d", got, revision, mp.applied)
	}
}

// The concept index of a purged version is deleted only once the purge is committed, so a purge
// that rolls back leaves the version searchable.
func TestPurgeDeletesTheIndexAfterCommit(t *testing.T) {
	f := setup(t)
	if _, err := f.svc.Enable(f.ctx, f.knID, &interfaces.KNEnableVersioningRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.publish(1, f.editDraft("name-2"), ""); err != nil {
		t.Fatal(err)
	}
	// v2 is running and the draft's base, so v1 is an ordinary history version.
	if _, err := f.svc.Purge(f.ctx, f.knID, 1, &interfaces.KNVersionStatusRequest{}); err != nil {
		t.Fatal(err)
	}
	if len(f.indexDeletes) != 1 || f.indexDeletes[0] != "v1:"+interfaces.KNVersionPurged {
		t.Fatalf("index deletions = %v, want [v1:%s] (deleted after the purge committed)", f.indexDeletes, interfaces.KNVersionPurged)
	}
}
