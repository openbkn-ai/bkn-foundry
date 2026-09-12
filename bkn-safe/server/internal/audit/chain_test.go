// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package audit

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

func chainTestStore(t *testing.T) (*Store, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.AuditLog{}); err != nil {
		t.Fatal(err)
	}
	return New(db), db
}

func record(t *testing.T, s *Store, action string) {
	t.Helper()
	if err := s.Record(context.Background(), Entry{
		ActorID: "admin-1", ActorType: "user", AuthMethod: "oauth", Method: "POST",
		Resource: "users", Action: action, Detail: `{"name":"` + action + `"}`, Status: 201,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRecordLinksRowsIntoChain(t *testing.T) {
	s, db := chainTestStore(t)
	record(t, s, "first")
	record(t, s, "second")
	record(t, s, "third")

	var rows []model.AuditLog
	if err := db.Order("seq ASC").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	prev := ""
	for i, row := range rows {
		if row.Seq == nil || *row.Seq != uint64(i+1) {
			t.Fatalf("row %d seq = %v, want %d", i, row.Seq, i+1)
		}
		if row.PrevHash != prev {
			t.Fatalf("row %d prev_hash = %q, want %q", i, row.PrevHash, prev)
		}
		if row.RowHash == "" || rowHash(row, *row.Seq, row.PrevHash) != row.RowHash {
			t.Fatalf("row %d hash does not recompute", i)
		}
		if row.CreatedAt.IsZero() || row.CreatedAt.Nanosecond()%int(time.Millisecond) != 0 {
			t.Fatalf("row %d created_at %v is not millisecond-truncated", i, row.CreatedAt)
		}
		prev = row.RowHash
	}
	head, found, err := s.Head(context.Background())
	if err != nil || !found || head.Seq != 3 || head.RowHash != prev {
		t.Fatalf("head = %+v found=%v err=%v, want seq 3 hash %s", head, found, err, prev)
	}
}

func TestVerifyPassesOnIntactChainAndSkipsLegacyRows(t *testing.T) {
	s, db := chainTestStore(t)
	// Two rows from before the chain existed: no seq, no hashes.
	for _, id := range []string{"legacy-a", "legacy-b"} {
		if err := db.Create(&model.AuditLog{ID: id, ActorID: "old", Action: "create"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	record(t, s, "first")
	record(t, s, "second")

	res, err := s.Verify(context.Background(), 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.Checked != 2 || res.FromSeq != 1 || res.ToSeq != 2 || res.UnchainedRows != 2 || res.Truncated {
		t.Fatalf("intact chain verify = %+v", res)
	}
	if res.Head == nil || res.Head.Seq != 2 {
		t.Fatalf("verify head = %+v", res.Head)
	}
}

func TestVerifyDetectsEditedRow(t *testing.T) {
	s, db := chainTestStore(t)
	record(t, s, "first")
	record(t, s, "second")
	record(t, s, "third")

	// Edit the second row's content in place: the classic "make it look like
	// nothing happened" change.
	if err := db.Model(&model.AuditLog{}).Where("seq = ?", 2).Update("detail", `{"name":"innocent"}`).Error; err != nil {
		t.Fatal(err)
	}
	res, err := s.Verify(context.Background(), 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || res.BrokenSeq == nil || *res.BrokenSeq != 2 || res.Reason != "hash_mismatch" {
		t.Fatalf("edited row not detected: %+v", res)
	}
	// Re-hashing the edited row to cover the edit still breaks the link from
	// the next row.
	var edited model.AuditLog
	if err := db.Where("seq = ?", 2).First(&edited).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.AuditLog{}).Where("seq = ?", 2).Update("row_hash", rowHash(edited, 2, edited.PrevHash)).Error; err != nil {
		t.Fatal(err)
	}
	res, err = s.Verify(context.Background(), 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || res.BrokenSeq == nil || *res.BrokenSeq != 3 || res.Reason != "prev_hash_mismatch" {
		t.Fatalf("re-hashed edit not detected at the successor: %+v", res)
	}
}

func TestVerifyDetectsDeletedRow(t *testing.T) {
	s, db := chainTestStore(t)
	record(t, s, "first")
	record(t, s, "second")
	record(t, s, "third")
	if err := db.Where("seq = ?", 2).Delete(&model.AuditLog{}).Error; err != nil {
		t.Fatal(err)
	}
	res, err := s.Verify(context.Background(), 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || res.BrokenSeq == nil || *res.BrokenSeq != 2 || res.Reason != "gap" {
		t.Fatalf("deleted row not detected: %+v", res)
	}
	// A window starting after the gap still links to its predecessor.
	res, err = s.Verify(context.Background(), 3, 3, 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || res.Reason != "gap" {
		t.Fatalf("window after a deleted predecessor verified: %+v", res)
	}
}

func TestVerifyWindowAndLimit(t *testing.T) {
	s, _ := chainTestStore(t)
	for i := 0; i < 5; i++ {
		record(t, s, "row")
	}
	res, err := s.Verify(context.Background(), 2, 4, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.FromSeq != 2 || res.ToSeq != 4 || res.Checked != 3 || res.Truncated {
		t.Fatalf("window verify = %+v", res)
	}
	res, err = s.Verify(context.Background(), 0, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.Checked != 2 || res.ToSeq != 2 || !res.Truncated {
		t.Fatalf("limited verify = %+v (want 2 checked, truncated)", res)
	}
	res, err = s.Verify(context.Background(), 3, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.FromSeq != 3 || res.ToSeq != 5 || res.Checked != 3 {
		t.Fatalf("resume verify = %+v", res)
	}
}

func TestRecordRetriesWhenAnotherWriterTookTheSeq(t *testing.T) {
	s, db := chainTestStore(t)
	record(t, s, "first")
	// Simulate a second replica that appended seq 2 between our head read and
	// insert: pre-insert a foreign row at seq 2 through a wrapped store whose
	// head read is stale.
	stale := &Store{db: db}
	head, _, err := stale.Head(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	seq := head.Seq + 1
	foreign := model.AuditLog{ID: "foreign", ActorID: "other-replica", Action: "create", CreatedAt: chainTimestamp(), Seq: &seq, PrevHash: head.RowHash}
	foreign.RowHash = rowHash(foreign, seq, head.RowHash)
	if err := db.Create(&foreign).Error; err != nil {
		t.Fatal(err)
	}
	// Our own append now finds seq 2 taken on the first attempt only if the
	// head were stale; Record re-reads the head under the lock, so it lands
	// at seq 3 linked to the foreign row.
	record(t, s, "second")
	res, err := s.Verify(context.Background(), 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.Checked != 3 {
		t.Fatalf("chain after concurrent append = %+v", res)
	}
	// And the duplicate-key path itself: inserting at an occupied seq must be
	// recognised as a race, not as a storage failure.
	dup := model.AuditLog{ID: "dup", Seq: &seq}
	err = db.Create(&dup).Error
	if err == nil || !isDuplicateKey(err) {
		t.Fatalf("duplicate seq error not recognised: %v", err)
	}
	if isDuplicateKey(errors.New("connection refused")) {
		t.Fatal("unrelated error treated as duplicate key")
	}
}
