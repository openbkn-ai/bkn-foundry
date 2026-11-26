// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

//go:build integration

package knevent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	corelogics "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/permission"
)

// relationsStub keeps relation types as the relation type service would, and refuses a write
// that is not the kind's own.
type relationsStub struct {
	interfaces.RelationTypeService
	mu      sync.Mutex
	saved   map[string]*interfaces.RelationType
	deleted []string
}

func relationKey(knID, branch, id string) string { return knID + "/" + branch + "/" + id }

func (r *relationsStub) CreateRelationTypes(ctx context.Context, tx *sql.Tx, relationTypes []*interfaces.RelationType,
	mode string, strict bool) ([]string, error) {
	if tx == nil || !corelogics.IsKindWrite(ctx) || !permission.KNImportPermissionPrechecked(ctx) ||
		mode != interfaces.ImportMode_Overwrite || strict {
		return nil, errors.New("the relation was not written as the kind's own, in the class's transaction")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := []string{}
	for _, relationType := range relationTypes {
		r.saved[relationKey(relationType.KNID, relationType.Branch, relationType.RTID)] = relationType
		ids = append(ids, relationType.RTID)
	}
	return ids, nil
}

func (r *relationsStub) CheckRelationTypeExistByID(_ context.Context, knID, branch, id string) (string, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return id, r.saved[relationKey(knID, branch, id)] != nil, nil
}

func (r *relationsStub) DeleteRelationTypesByIDs(ctx context.Context, tx *sql.Tx, knID, branch string, ids []string) error {
	if tx == nil || !corelogics.IsKindWrite(ctx) {
		return errors.New("the relation was not deleted as the kind's own, in the class's transaction")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, id := range ids {
		delete(r.saved, relationKey(knID, branch, id))
		r.deleted = append(r.deleted, id)
	}
	return nil
}

func (r *relationsStub) relation(knID, branch, id string) *interfaces.RelationType {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.saved[relationKey(knID, branch, id)]
}

// objectsStub holds the object types the classes are about.
type objectsStub struct {
	interfaces.ObjectTypeAccess
	objects map[string]*interfaces.ObjectType
}

func (o *objectsStub) GetObjectTypesByIDs(_ context.Context, _ *sql.Tx, knID, branch string, ids []string) ([]*interfaces.ObjectType, error) {
	found := []*interfaces.ObjectType{}
	for _, id := range ids {
		if objectType := o.objects[relationKey(knID, branch, id)]; objectType != nil {
			found = append(found, objectType)
		}
	}
	return found, nil
}

func (o *objectsStub) subject(knID, branch, id string, keys ...string) {
	o.objects[relationKey(knID, branch, id)] = &interfaces.ObjectType{
		ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: id, PrimaryKeys: keys}, KNID: knID, Branch: branch,
	}
}

// classService is the service with the event kind beside it, what the kind keeps at hand, and
// the worker that keeps the datasets.
type classService struct {
	*service
	db        *sql.DB
	clock     *clock
	kind      *eventKind
	datasets  *datasetsStub
	relations *relationsStub
	objects   *objectsStub
	projector *projector
	// at is the worker's time, which the tests move.
	at time.Time
}

func newClassService(t *testing.T) *classService {
	s, db, c := integrationService(t)
	classes, datasets := newTestClasses()
	cs := &classService{
		service: s, db: db, clock: c, datasets: datasets, at: time.Unix(1_800_000_000, 0),
		relations: &relationsStub{saved: map[string]*interfaces.RelationType{}},
		objects:   &objectsStub{objects: map[string]*interfaces.ObjectType{}},
	}
	cs.kind = &eventKind{store: s.store, classes: classes, relations: cs.relations, objects: cs.objects,
		available: func() bool { return true }}
	cs.projector = newProjector(s.store, classes)
	cs.projector.now = func() time.Time { return cs.at }
	for _, kn := range []string{"kn1", "kn2"} {
		cs.objects.subject(kn, interfaces.MAIN_BRANCH, "order", "order_id")
		cs.objects.subject(kn, interfaces.MAIN_BRANCH, "sku", "sku_id")
		cs.objects.subject(kn, interfaces.DRAFT_BRANCH, "order", "order_id")
	}
	return cs
}

func lateOrder(kn, branch string) *interfaces.ObjectType {
	return &interfaces.ObjectType{
		ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "late_order", OTName: "订单超期"},
		Kinds:                  []string{interfaces.ObjectTypeKindEvent},
		Event:                  &interfaces.ObjectTypeEvent{Source: "report", Subject: "order", MergeHours: 1},
		KNID:                   kn, Branch: branch,
	}
}

func lowStock(kn string) *interfaces.ObjectType {
	return &interfaces.ObjectType{
		ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: "low_stock", OTName: "低库存"},
		Kinds:                  []string{interfaces.ObjectTypeKindEvent},
		Event:                  &interfaces.ObjectTypeEvent{Source: "rule", Subject: "sku", Severity: "high"},
		KNID:                   kn, Branch: interfaces.MAIN_BRANCH,
	}
}

// save saves a class as the object type service does: the kind completes it, the definition is
// written, and the kind keeps what it has beside it, all in one transaction.
func (cs *classService) save(t *testing.T, class, stored *interfaces.ObjectType) error {
	t.Helper()
	ctx := userContext()
	if err := cs.kind.Complete(ctx, class, stored); err != nil {
		return err
	}
	tx, err := cs.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	config, _ := json.Marshal(class.Event)
	if _, err := tx.Exec("INSERT INTO "+objectTypeTable+
		" (f_id, f_name, f_comment, f_bkn_raw_content, f_kn_id, f_branch, f_data_source, f_kinds, f_event)"+
		" VALUES (?, ?, '', '', ?, ?, '{}', '[\"event\"]', ?) ON DUPLICATE KEY UPDATE f_name = VALUES(f_name), f_event = VALUES(f_event)",
		class.OTID, class.OTName, class.KNID, class.Branch, string(config)); err != nil {
		t.Fatal(err)
	}
	if err := cs.kind.Saved(corelogics.WithKindWrite(ctx), tx, class, stored); err != nil {
		return err
	}
	return tx.Commit()
}

// saveBoth saves the classes the tests write events of.
func (cs *classService) saveBoth(t *testing.T, kn string) {
	t.Helper()
	for _, class := range []*interfaces.ObjectType{lateOrder(kn, interfaces.MAIN_BRANCH), lowStock(kn)} {
		if err := cs.save(t, class, nil); err != nil {
			t.Fatal(err)
		}
	}
}

// remove deletes a class as the object type service does, in one transaction with what the kind
// removes beside it.
func (cs *classService) remove(t *testing.T, class *interfaces.ObjectType) error {
	t.Helper()
	tx, err := cs.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := cs.kind.Deleting(corelogics.WithKindWrite(userContext()), tx, class); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM "+objectTypeTable+" WHERE f_kn_id = ? AND f_branch = ? AND f_id = ?",
		class.KNID, class.Branch, class.OTID); err != nil {
		t.Fatal(err)
	}
	return tx.Commit()
}

func (cs *classService) round() { cs.projector.round(context.Background()) }

// checkRow reads a class's row of rule checks: whether it is there, the time of its last check,
// and whether its dataset holds the ledger's events.
func (cs *classService) checkRow(t *testing.T, kn, class string) (there bool, checked int64, synced bool) {
	t.Helper()
	err := cs.db.QueryRow("SELECT f_time, f_dataset_synced FROM "+checkTable+" WHERE f_kn_id = ? AND f_event_type = ?",
		kn, class).Scan(&checked, &synced)
	if errors.Is(err, sql.ErrNoRows) {
		return false, 0, false
	}
	if err != nil {
		t.Fatal(err)
	}
	return true, checked, synced
}

// state reads how far the copy of a network has got: the feed's end, the
// datasets' position, and the classes whose datasets hold the ledger's events.
func (cs *classService) state(t *testing.T, kn string) (last, projected int64, synced []string) {
	t.Helper()
	err := cs.db.QueryRow("SELECT f_last_seq, f_projected_seq FROM "+headTable+" WHERE f_kn_id = ?", kn).Scan(&last, &projected)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	rows, err := cs.db.Query("SELECT f_event_type FROM "+checkTable+" WHERE f_kn_id = ? AND f_dataset_synced = 1 ORDER BY f_event_type", kn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	synced = []string{}
	for rows.Next() {
		var class string
		if err := rows.Scan(&class); err != nil {
			t.Fatal(err)
		}
		synced = append(synced, class)
	}
	return last, projected, synced
}

// agrees fails unless a class's dataset holds exactly the ledger's events of
// the class, each as it is now.
func (cs *classService) agrees(t *testing.T, kn, class string) {
	t.Helper()
	dataset := classDatasetID(kn, class)
	events := must(cs.store.eventsOfClass(context.Background(), kn, class, "", 100000))
	want := make([]string, 0, len(events))
	for _, event := range events {
		want = append(want, event.ID)
		if got := cs.datasets.document(dataset, event.ID); !reflect.DeepEqual(got, eventDocument(event)) {
			t.Fatalf("event %s of %s in the dataset\n got %v\nwant %v", event.ID, class, got, eventDocument(event))
		}
	}
	if got := cs.datasets.ids(dataset); !reflect.DeepEqual(got, want) {
		t.Fatalf("dataset of %s holds %v, the ledger %v", class, got, want)
	}
}

func hitsOf(keys ...string) []interfaces.KnEventRuleHit {
	hits := make([]interfaces.KnEventRuleHit, 0, len(keys))
	for _, key := range keys {
		hits = append(hits, interfaces.KnEventRuleHit{Key: key})
	}
	return hits
}

func (cs *classService) check(t *testing.T, kn string, keys ...string) interfaces.KnEventRuleCheckResult {
	t.Helper()
	cs.clock.tick(60 * 1000)
	return must(cs.RecordRuleCheck(userContext(), kn, interfaces.KnEventRuleCheckInput{EventType: "low_stock", Status: "ok", Hits: hitsOf(keys...)}))
}

func (cs *classService) report(t *testing.T, kn, key string) interfaces.KnEvent {
	t.Helper()
	cs.clock.tick(1)
	return must(cs.ReportEvent(userContext(), kn, interfaces.KnEventReportInput{EventType: "late_order", Title: key + " 超期", ObjectKey: key,
		Detail: json.RawMessage(`{"days":3}`), Verify: true})).Events[0]
}

func TestIntegrationAClassIsSavedWithItsDatasetItsRowAndItsRelation(t *testing.T) {
	cs := newClassService(t)
	class := lateOrder("kn1", interfaces.MAIN_BRANCH)
	if err := cs.save(t, class, nil); err != nil {
		t.Fatal(err)
	}
	dataset := classDatasetID("kn1", "late_order")
	if cs.datasets.resources[dataset] == nil || !reflect.DeepEqual(cs.datasets.actors, map[string]bool{platform: true}) {
		t.Fatalf("dataset %v written by %v", cs.datasets.resources[dataset], cs.datasets.actors)
	}
	if there, checked, synced := cs.checkRow(t, "kn1", "late_order"); !there || checked != 0 || !synced {
		t.Fatalf("row there=%v checked=%d synced=%v, want an unchecked row of a dataset that holds what the ledger has",
			there, checked, synced)
	}
	relation := cs.relations.relation("kn1", interfaces.MAIN_BRANCH, "late_order"+relationSuffix)
	mapping, _ := relation.MappingRules.([]interfaces.Mapping)
	if relation == nil || relation.SourceObjectTypeID != "late_order" || relation.TargetObjectTypeID != "order" ||
		len(mapping) != 1 || mapping[0].SourceProp.Name != fieldObjectKey || mapping[0].TargetProp.Name != "order_id" {
		t.Fatalf("relation %+v", relation)
	}
	if checks := must(cs.ListRuleChecks(userContext(), "kn1")); len(checks) != 0 {
		t.Fatalf("checks of a class never checked %+v", checks)
	}

	// Its events reach the dataset; saved again, the class keeps them.
	first := cs.report(t, "kn1", "PO-1")
	cs.round()
	cs.agrees(t, "kn1", "late_order")
	stored := *class
	update := lateOrder("kn1", interfaces.MAIN_BRANCH)
	update.Event.Subject = "sku"
	if err := cs.save(t, update, &stored); err != nil {
		t.Fatal(err)
	}
	if got := cs.datasets.ids(dataset); !reflect.DeepEqual(got, []string{first.ID}) || len(cs.datasets.cleared) != 0 {
		t.Fatalf("the dataset of a saved class holds %v, cleared %v", got, cs.datasets.cleared)
	}
	if relation := cs.relations.relation("kn1", interfaces.MAIN_BRANCH, "late_order"+relationSuffix); relation.TargetObjectTypeID != "sku" {
		t.Fatalf("the relation follows the subject: %+v", relation)
	}
}

func TestIntegrationTheSubjectOfAClassHasOneKey(t *testing.T) {
	cs := newClassService(t)
	cs.objects.subject("kn1", interfaces.MAIN_BRANCH, "line", "order_id", "line_no")
	for subject, what := range map[string]string{"missing": "a subject that is not there", "line": "a subject of two keys"} {
		class := lateOrder("kn1", interfaces.MAIN_BRANCH)
		class.Event.Subject = subject
		refusedAsInvalid(t, cs.save(t, class, nil), what)
	}
	if len(cs.datasets.resources) != 0 || len(cs.relations.saved) != 0 {
		t.Fatalf("a refused class left %v and %v", cs.datasets.resources, cs.relations.saved)
	}
	if there, _, _ := cs.checkRow(t, "kn1", "late_order"); there {
		t.Fatal("a refused class left its row")
	}
}

// The datasets outlive their classes and networks, and their IDs follow from the class's and the
// network's: a class made again under the ID of a deleted one must not show the old one's events,
// nor take its checks.
func TestIntegrationANewClassDoesNotInheritWhatAnEarlierOneLeft(t *testing.T) {
	cs := newClassService(t)
	cs.saveBoth(t, "kn1")
	cs.report(t, "kn1", "PO-1")
	cs.check(t, "kn1", "A")
	cs.round()
	tx, err := cs.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := cs.store.DeleteKnEvents(context.Background(), tx, "kn1"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	// What a deleted class of the ID left: its row, as an upgrade or an outage may leave one.
	if _, err := cs.db.Exec("INSERT INTO " + checkTable + " (f_kn_id, f_event_type, f_status, f_last_ok, f_time) VALUES ('kn1', 'low_stock', 'ok', 5, 5)"); err != nil {
		t.Fatal(err)
	}

	cs.saveBoth(t, "kn1")
	for _, class := range []string{"late_order", "low_stock"} {
		if got := cs.datasets.ids(classDatasetID("kn1", class)); len(got) != 0 {
			t.Fatalf("the new class %s shows %v", class, got)
		}
		if there, checked, synced := cs.checkRow(t, "kn1", class); !there || checked != 0 || !synced {
			t.Fatalf("row of %s there=%v checked=%d synced=%v, want one started over", class, there, checked, synced)
		}
	}
	// Its first decision finds a backlog again.
	if found := cs.check(t, "kn1", "A"); !found.Events[0].Backlog {
		t.Fatalf("the first check of the new class found %+v", found.Events)
	}
	second := cs.report(t, "kn1", "PO-2")
	cs.round()
	if got := cs.datasets.ids(classDatasetID("kn1", "late_order")); !reflect.DeepEqual(got, []string{second.ID}) {
		t.Fatalf("dataset holds %v", got)
	}
}

// A class made where the ledger still has events of its ID, as in another branch of the network,
// keeps them and their checks.
func TestIntegrationAClassMadeWhereTheLedgerHasItsEventsKeepsThem(t *testing.T) {
	cs := newClassService(t)
	cs.saveBoth(t, "kn1")
	first := cs.report(t, "kn1", "PO-1")
	cs.check(t, "kn1", "A")
	cs.round()
	if err := cs.save(t, lateOrder("kn1", interfaces.DRAFT_BRANCH), nil); err != nil {
		t.Fatal(err)
	}
	if got := cs.datasets.ids(classDatasetID("kn1", "late_order")); !reflect.DeepEqual(got, []string{first.ID}) {
		t.Fatalf("dataset holds %v", got)
	}
	_, beforeChecked, _ := cs.checkRow(t, "kn1", "low_stock")
	if err := cs.save(t, lowStock("kn1"), nil); err != nil {
		t.Fatal(err)
	}
	if _, checked, _ := cs.checkRow(t, "kn1", "low_stock"); checked != beforeChecked || checked == 0 {
		t.Fatalf("the row of a class with events was started over: %d, was %d", checked, beforeChecked)
	}
}

func TestIntegrationALostDatasetIsWrittenAgainWhenTheClassIsSaved(t *testing.T) {
	cs := newClassService(t)
	class := lateOrder("kn1", interfaces.MAIN_BRANCH)
	if err := cs.save(t, class, nil); err != nil {
		t.Fatal(err)
	}
	cs.report(t, "kn1", "PO-1")
	cs.report(t, "kn1", "PO-2")
	cs.round()
	cs.datasets.drop(classDatasetID("kn1", "late_order"))
	stored := *class
	if err := cs.save(t, lateOrder("kn1", interfaces.MAIN_BRANCH), &stored); err != nil {
		t.Fatal(err)
	}
	if _, _, synced := cs.checkRow(t, "kn1", "late_order"); synced {
		t.Fatal("a dataset made again is said to hold the ledger's events")
	}
	cs.round()
	cs.agrees(t, "kn1", "late_order")
	if _, _, synced := cs.checkRow(t, "kn1", "late_order"); !synced {
		t.Fatal("the ledger's events were written, yet the row says they were not")
	}
}

func TestIntegrationAClassWithEventsIsNotDeleted(t *testing.T) {
	cs := newClassService(t)
	class := lateOrder("kn1", interfaces.MAIN_BRANCH)
	if err := cs.save(t, class, nil); err != nil {
		t.Fatal(err)
	}
	cs.report(t, "kn1", "PO-1")
	err := cs.remove(t, class)
	if statusOf(t, err) != http.StatusConflict {
		t.Fatalf("deleting a class with events: %v", err)
	}
	if cs.relations.relation("kn1", interfaces.MAIN_BRANCH, "late_order"+relationSuffix) == nil {
		t.Fatal("the refused deletion took the relation")
	}
	if there, _, _ := cs.checkRow(t, "kn1", "late_order"); !there {
		t.Fatal("the refused deletion took the row")
	}
}

func TestIntegrationADeletedClassTakesItsRelationAndRowAndLeavesItsDataset(t *testing.T) {
	cs := newClassService(t)
	class := lateOrder("kn1", interfaces.MAIN_BRANCH)
	draft := lateOrder("kn1", interfaces.DRAFT_BRANCH)
	for _, saved := range []*interfaces.ObjectType{class, draft} {
		if err := cs.save(t, saved, nil); err != nil {
			t.Fatal(err)
		}
	}
	// In the draft of a versioned network the running version still has the class: its row stays.
	if err := cs.remove(t, draft); err != nil {
		t.Fatal(err)
	}
	if there, _, _ := cs.checkRow(t, "kn1", "late_order"); !there {
		t.Fatal("deleting the class in a draft took its row")
	}
	if err := cs.remove(t, class); err != nil {
		t.Fatal(err)
	}
	if there, _, _ := cs.checkRow(t, "kn1", "late_order"); there {
		t.Fatal("the deleted class left its row")
	}
	if want := []string{"late_order" + relationSuffix, "late_order" + relationSuffix}; !reflect.DeepEqual(cs.relations.deleted, want) {
		t.Fatalf("deleted relations %v, want the class's in each branch", cs.relations.deleted)
	}
	if cs.datasets.resources[classDatasetID("kn1", "late_order")] == nil {
		t.Fatal("the dataset went with the class")
	}
	// Deleting a class whose relation is gone already is no failure.
	if err := cs.save(t, lateOrder("kn1", interfaces.MAIN_BRANCH), nil); err != nil {
		t.Fatal(err)
	}
	delete(cs.relations.saved, relationKey("kn1", interfaces.MAIN_BRANCH, "late_order"+relationSuffix))
	if err := cs.remove(t, class); err != nil {
		t.Fatal(err)
	}
}

// A deletion holds the network's head row: an event write waits for it, and finds the class gone.
func TestIntegrationAReportDuringTheDeletionOfItsClassIsRefused(t *testing.T) {
	cs := newClassService(t)
	class := lateOrder("kn1", interfaces.MAIN_BRANCH)
	if err := cs.save(t, class, nil); err != nil {
		t.Fatal(err)
	}
	tx, err := cs.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := cs.kind.Deleting(corelogics.WithKindWrite(userContext()), tx, class); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("DELETE FROM " + objectTypeTable + " WHERE f_kn_id = 'kn1' AND f_id = 'late_order'"); err != nil {
		t.Fatal(err)
	}
	reported := make(chan error, 1)
	go func() {
		_, err := cs.ReportEvent(userContext(), "kn1", interfaces.KnEventReportInput{EventType: "late_order", Title: "late"})
		reported <- err
	}()
	waitForHead(t, cs.db)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-reported; statusOf(t, err) != http.StatusNotFound {
		t.Fatalf("report during the deletion of its class: %v", err)
	}
}

func TestIntegrationDatasetsFollowTheLedger(t *testing.T) {
	cs := newClassService(t)
	ctx := userContext()
	cs.saveBoth(t, "kn1")
	cs.saveBoth(t, "kn2")
	first := cs.report(t, "kn1", "PO-1")
	cs.report(t, "kn1", "PO-2")
	cs.check(t, "kn1", "A", "B")
	cs.report(t, "kn2", "PO-9")

	cs.round()
	for _, at := range [][2]string{{"kn1", "late_order"}, {"kn1", "low_stock"}, {"kn2", "late_order"}, {"kn2", "low_stock"}} {
		cs.agrees(t, at[0], at[1])
	}
	last, projected, synced := cs.state(t, "kn1")
	if last == 0 || projected != last || !reflect.DeepEqual(synced, []string{"late_order", "low_stock"}) {
		t.Fatalf("kn1 at %d of %d, synced %v", projected, last, synced)
	}
	dataset := classDatasetID("kn1", "late_order")
	if got := cs.datasets.document(dataset, first.ID); got["status"] != "verified" || got["occurred_at"] == nil ||
		!reflect.DeepEqual(got["detail"], map[string]any{"days": float64(3)}) {
		t.Fatalf("document %v", got)
	}
	if !reflect.DeepEqual(cs.datasets.actors, map[string]bool{platform: true}) {
		t.Fatalf("written as %v", cs.datasets.actors)
	}

	// Only what changed is written again, as it is now: one event closed, one
	// recovered and one found by the next check. The event the check only
	// refreshed is not.
	written := func() map[string]int {
		counts := map[string]int{}
		for key, count := range cs.datasets.written {
			counts[key] = count
		}
		return counts
	}
	before := written()
	cs.clock.tick(1)
	must(cs.ChangeEvent(ctx, "kn1", first.ID, "close", interfaces.KnEventReasonInput{Reason: "已到货"}))
	cs.check(t, "kn1", "A", "C")
	cs.round()
	changed := 0
	for key, count := range written() {
		changed += count - before[key]
	}
	// PO-1, and B and C of the check; A, PO-2 and the other network are as they were.
	if changed != 3 {
		t.Fatalf("%d documents written, want 3", changed)
	}
	if got := cs.datasets.document(dataset, first.ID); got["status"] != "closed" || got["ended_at"] == nil {
		t.Fatalf("document after closing %v", got)
	}
	cs.agrees(t, "kn1", "late_order")
	cs.agrees(t, "kn1", "low_stock")

	before = written()
	cs.round()
	if !reflect.DeepEqual(written(), before) {
		t.Fatal("a round with nothing new wrote")
	}

	// A check that finds what the last one found writes nothing, and the
	// datasets still agree with the ledger.
	cs.check(t, "kn1", "A", "C")
	cs.round()
	if !reflect.DeepEqual(written(), before) {
		t.Fatal("a check that only refreshed wrote")
	}
	cs.agrees(t, "kn1", "low_stock")
	if last, projected, _ := cs.state(t, "kn1"); projected != last {
		t.Fatalf("kn1 at %d of %d after the refreshes", projected, last)
	}
}

// The events of a class the module keeps no dataset for are passed over, and the feed moves on.
func TestIntegrationEventsOfAClassWithoutADatasetArePassedOver(t *testing.T) {
	cs := newClassService(t)
	defineClasses(t, cs.db, "kn1")
	cs.report(t, "kn1", "PO-1")
	cs.round()
	if len(cs.datasets.written) != 0 {
		t.Fatalf("wrote %v", cs.datasets.written)
	}
	if last, projected, _ := cs.state(t, "kn1"); projected != last {
		t.Fatalf("at %d of %d", projected, last)
	}
}

func TestIntegrationALongFeedIsFollowedPageByPage(t *testing.T) {
	cs := newClassService(t)
	cs.saveBoth(t, "kn1")
	keys := make([]string, 2*feedPage+300)
	for index := range keys {
		keys[index] = fmt.Sprintf("SKU-%05d", index)
	}
	cs.check(t, "kn1", keys...)
	cs.round()
	cs.agrees(t, "kn1", "low_stock")
	if last, projected, _ := cs.state(t, "kn1"); projected != last || last != int64(len(keys)) {
		t.Fatalf("at %d of %d", projected, last)
	}

	// The ledger's events are written again page by page to a dataset that lost them.
	cs.datasets.drop(classDatasetID("kn1", "low_stock"))
	cs.check(t, "kn1", keys[:1]...)
	cs.round()
	if _, _, synced := cs.state(t, "kn1"); !reflect.DeepEqual(synced, []string{"late_order"}) {
		t.Fatalf("synced after the dataset was lost: %v", synced)
	}
	cs.round()
	cs.agrees(t, "kn1", "low_stock")
	if last, projected, synced := cs.state(t, "kn1"); projected != last || !reflect.DeepEqual(synced, []string{"late_order", "low_stock"}) {
		t.Fatalf("at %d of %d, synced %v", projected, last, synced)
	}
}

func TestIntegrationAWriteThatFailsIsTriedAgainLater(t *testing.T) {
	cs := newClassService(t)
	cs.saveBoth(t, "kn1")
	cs.saveBoth(t, "kn2")
	cs.round()
	cs.report(t, "kn1", "PO-1")
	cs.report(t, "kn2", "PO-9")
	cs.datasets.writeErr = errors.New("vega is down")
	cs.round()
	if last, projected, _ := cs.state(t, "kn1"); projected >= last {
		t.Fatalf("moved on to %d of %d past events that were not written", projected, last)
	}
	// What failed is left alone for a while, so an outage is not hammered.
	cs.datasets.writeErr = nil
	cs.round()
	if last, projected, _ := cs.state(t, "kn1"); projected >= last {
		t.Fatal("tried again at once")
	}
	cs.at = cs.at.Add(retryAfter)
	cs.round()
	cs.agrees(t, "kn1", "late_order")
	cs.agrees(t, "kn2", "late_order")
	if last, projected, _ := cs.state(t, "kn1"); projected != last {
		t.Fatalf("at %d of %d", projected, last)
	}
}

// Refreshes that expired before they were followed are gone from the feed;
// the events they refreshed are in the ledger as they are.
func TestIntegrationTheEndOfAFeedThatWasPurgedIsPassed(t *testing.T) {
	cs := newClassService(t)
	cs.saveBoth(t, "kn1")
	cs.check(t, "kn1", "A", "B")
	cs.round()
	cs.check(t, "kn1", "A", "B")
	if deleted := must(cs.store.purgeChecked(context.Background(), cs.clock.tick(1))); deleted != 2 {
		t.Fatalf("purged %d", deleted)
	}
	cs.round()
	if last, projected, _ := cs.state(t, "kn1"); projected != last || last != 4 {
		t.Fatalf("at %d of %d", projected, last)
	}
	if behind := must(cs.store.behind(context.Background(), 10)); len(behind) != 0 {
		t.Fatalf("still behind: %+v", behind)
	}
}

func TestIntegrationOneReplicaWritesTheDatasetsAtATime(t *testing.T) {
	cs := newClassService(t)
	cs.saveBoth(t, "kn1")
	cs.report(t, "kn1", "PO-1")
	entered, leave, left := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		_, err := cs.store.alone(context.Background(), projectLock, func() error {
			close(entered)
			<-leave
			return nil
		})
		left <- err
	}()
	<-entered
	cs.round()
	if _, projected, _ := cs.state(t, "kn1"); projected != 0 {
		t.Fatal("wrote while another replica was writing")
	}
	close(leave)
	if err := <-left; err != nil {
		t.Fatal(err)
	}
	cs.round()
	cs.agrees(t, "kn1", "late_order")
}
