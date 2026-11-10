// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

//go:build integration

package kn_version

import (
	"context"
	"os"
	"slices"
	"strconv"
	"sync"
	"testing"

	libdb "github.com/openbkn-ai/bkn-foundry/comm-go/db"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/drivenadapters/knowledge_network"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/permission"
)

// deletedPolicies records the child policies a cleanup removes.
type deletedPolicies struct {
	allowAll
	mu  sync.Mutex
	ids []string
}

func (p *deletedPolicies) DeleteResources(_ context.Context, resourceType string, ids []string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, id := range ids {
		p.ids = append(p.ids, resourceType+":"+id)
	}
	return nil
}

func (p *deletedPolicies) take() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	ids := p.ids
	p.ids = nil
	slices.Sort(ids)
	return ids
}

// trackOrphans wires the presence check and the branch copier that registers what a version
// operation overwrites or deletes, as the server does.
func (f *fixture) trackOrphans() *deletedPolicies {
	f.t.Helper()
	port, _ := strconv.Atoi(os.Getenv("BKN_IT_DB_PORT"))
	kna := knowledge_network.NewKNAccess(&common.AppSetting{DBSetting: libdb.DBSetting{
		Host: os.Getenv("BKN_IT_DB_HOST"), Port: port, Username: os.Getenv("BKN_IT_DB_USER"),
		Password: os.Getenv("BKN_IT_DB_PASSWORD"), DBName: os.Getenv("BKN_IT_DB_NAME"),
	}})
	previous := logics.KNA
	logics.SetKNAccess(kna)
	permission.SetKNChildPresence(kna.(interfaces.KNChildPresence))
	f.t.Cleanup(func() {
		logics.SetKNAccess(previous)
		permission.SetKNChildPresence(nil)
	})
	policies := &deletedPolicies{}
	f.svc.ps = policies
	f.svc.bc = permission.TrackBranchChildren(f.svc.bc)
	return policies
}

// addToDraft creates object type id on the draft the way a draft write does.
func (f *fixture) addToDraft(id string) int64 {
	f.t.Helper()
	tx, err := f.db.Begin()
	if err != nil {
		f.t.Fatal(err)
	}
	revision, ok, err := f.svc.va.MarkDraftWritten(f.ctx, tx, f.knID, nil)
	if err != nil || !ok {
		f.t.Fatalf("mark draft written: %v %v", ok, err)
	}
	if _, err := tx.Exec("INSERT INTO t_object_type (f_kn_id, f_id, f_name, f_branch, f_comment, f_bkn_raw_content, f_data_source, f_revision) VALUES (?, ?, ?, 'draft', '', '', '', 'r1')",
		f.knID, id, id); err != nil {
		f.t.Fatal(err)
	}
	if _, err := tx.Exec("INSERT INTO t_object_type_status (f_kn_id, f_id, f_branch) VALUES (?, ?, 'draft')", f.knID, id); err != nil {
		f.t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		f.t.Fatal(err)
	}
	return revision
}

// removeFromDraft deletes object type id from the draft without any cleanup, as a delete
// before this change did.
func (f *fixture) removeFromDraft(id string) int64 {
	f.t.Helper()
	tx, err := f.db.Begin()
	if err != nil {
		f.t.Fatal(err)
	}
	revision, ok, err := f.svc.va.MarkDraftWritten(f.ctx, tx, f.knID, nil)
	if err != nil || !ok {
		f.t.Fatalf("mark draft written: %v %v", ok, err)
	}
	for _, table := range []string{"t_object_type_status", "t_object_type"} {
		if _, err := tx.Exec("DELETE FROM "+table+" WHERE f_kn_id = ? AND f_id = ? AND f_branch = 'draft'", f.knID, id); err != nil {
			f.t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		f.t.Fatal(err)
	}
	return revision
}

// A definition's policy outlives the draft and main while a kept version still has it, and goes
// once the last branch that has it is purged or overwritten.
func TestVersionOperationsReclaimPoliciesOfDefinitionsNoBranchHas(t *testing.T) {
	f := setup(t)
	f.full = false // Community: running plus one previous version
	policies := f.trackOrphans()
	gone := "object_type:" + f.knID + "/ot2"

	if _, err := f.svc.Enable(f.ctx, f.knID, &interfaces.KNEnableVersioningRequest{}); err != nil {
		t.Fatal(err)
	}
	if got := policies.take(); len(got) != 0 {
		t.Fatalf("enable removed %v", got)
	}
	if _, err := f.publish(1, f.addToDraft("ot2"), ""); err != nil { // v2: ot1, ot2
		t.Fatal(err)
	}
	if got := policies.take(); len(got) != 0 {
		t.Fatalf("publish v2 removed %v", got)
	}
	// main and the draft drop ot2, but v2 is kept as the previous version and still has it.
	if _, err := f.publish(2, f.removeFromDraft("ot2"), ""); err != nil { // v3: ot1; purges v1
		t.Fatal(err)
	}
	if got := policies.take(); len(got) != 0 {
		t.Fatalf("publish v3 removed %v while v2 still has ot2", got)
	}
	// Publishing v4 purges v2, the last branch with ot2.
	if _, err := f.publish(3, f.editDraft("name-4"), ""); err != nil {
		t.Fatal(err)
	}
	if got := policies.take(); !slices.Equal(got, []string{gone}) {
		t.Fatalf("publish v4 removed %v, want %v", got, []string{gone})
	}

	// A definition made and discarded in the draft leaves no policy behind.
	revision := f.addToDraft("ot3")
	if _, err := f.svc.ResetDraft(f.ctx, f.knID, &interfaces.KNResetDraftRequest{
		ExpectedDraftRevision: ptr(revision), OverwriteChanges: true}); err != nil {
		t.Fatal(err)
	}
	if got, want := policies.take(), []string{"object_type:" + f.knID + "/ot3"}; !slices.Equal(got, want) {
		t.Fatalf("reset removed %v, want %v", got, want)
	}
}
