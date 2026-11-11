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
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// classService is the service with what its classes are made of at hand, and
// the worker that keeps their datasets.
type classService struct {
	*service
	db          *sql.DB
	clock       *clock
	definitions *definitionsStub
	datasets    *datasetsStub
	projector   *projector
	// at is the worker's time, which the tests move.
	at time.Time
}

func newClassService(t *testing.T) *classService {
	s, db, c := integrationService(t)
	cs := &classService{
		service: s, db: db, clock: c, at: time.Unix(1_800_000_000, 0),
		definitions: s.classes.definitions.(*definitionsStub), datasets: s.classes.datasets.(*datasetsStub),
	}
	cs.projector = newProjector(s.store, s.classes, func() bool { return true })
	cs.projector.now = func() time.Time { return cs.at }
	return cs
}

func (cs *classService) round() { cs.projector.round(context.Background()) }

func (cs *classService) eventType(t *testing.T, kn, code string) interfaces.KnEventType {
	t.Helper()
	return must(cs.GetEventType(userContext(), kn, code))
}

// state reads how far the copy of a network has got: the feed's end, the
// datasets' position, and the event types whose datasets hold the ledger's events.
func (cs *classService) state(t *testing.T, kn string) (last, projected int64, synced []string) {
	t.Helper()
	err := cs.db.QueryRow("SELECT f_last_seq, f_projected_seq FROM "+headTable+" WHERE f_kn_id = ?", kn).Scan(&last, &projected)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	rows, err := cs.db.Query("SELECT f_code FROM "+typeTable+" WHERE f_kn_id = ? AND f_dataset_synced = 1 ORDER BY f_code", kn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	synced = []string{}
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			t.Fatal(err)
		}
		synced = append(synced, code)
	}
	return last, projected, synced
}

// agrees fails unless a type's dataset holds exactly the ledger's events of
// the type, each as it is now.
func (cs *classService) agrees(t *testing.T, kn, code string) {
	t.Helper()
	dataset := cs.eventType(t, kn, code).DatasetID
	events := must(cs.store.eventsOfType(context.Background(), kn, code, "", 100000))
	want := make([]string, 0, len(events))
	for _, event := range events {
		want = append(want, event.ID)
		if got := cs.datasets.document(dataset, event.ID); !reflect.DeepEqual(got, eventDocument(event)) {
			t.Fatalf("event %s of %s in the dataset\n got %v\nwant %v", event.ID, code, got, eventDocument(event))
		}
	}
	if got := cs.datasets.ids(dataset); !reflect.DeepEqual(got, want) {
		t.Fatalf("dataset of %s holds %v, the ledger %v", code, got, want)
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

func TestIntegrationAnEventTypeIsMadeWithItsClass(t *testing.T) {
	cs := newClassService(t)
	ctx := userContext()
	cs.definitions.subject("kn1", "main", "order", "order_id")
	createTypes(t, cs.service, ctx, "kn1")

	stored := cs.eventType(t, "kn1", "late_order")
	if stored.ObjectTypeID != "late_order" || stored.DatasetID != classDatasetID("kn1", "late_order") {
		t.Fatalf("stored %+v", stored)
	}
	if listed := must(cs.ListEventTypes(ctx, "kn1")); listed[1].ObjectTypeID != "low_stock" || listed[1].DatasetID == "" {
		t.Fatalf("listed %+v", listed[1])
	}
	want := []string{"object kn1/main/late_order", "relation kn1/main/late_order_subject", "object kn1/main/low_stock"}
	if !reflect.DeepEqual(cs.definitions.writes, want) || cs.datasets.resources[stored.DatasetID] == nil {
		t.Fatalf("writes %v", cs.definitions.writes)
	}

	// A code that is taken is refused with the class of its type untouched.
	if _, err := cs.CreateEventType(ctx, "kn1", interfaces.KnEventTypeInput{Code: "late_order", Name: "again", Source: "report"}); statusOf(t, err) != 409 {
		t.Fatalf("duplicate code: %v", err)
	}
	if len(cs.definitions.writes) != len(want) || cs.definitions.objects["kn1/main/late_order"].OTName != "订单超期" {
		t.Fatalf("a refused creation wrote %v", cs.definitions.writes)
	}

	// A kind of event that cannot be an object type of the network is not created.
	cs.definitions.subject("kn1", "main", "stock_alert", "id")
	if _, err := cs.CreateEventType(ctx, "kn1", interfaces.KnEventTypeInput{Code: "stock.alert", Name: "库存告警", Source: "report"}); statusOf(t, err) != 409 {
		t.Fatalf("an object type ID that is taken: %v", err)
	}
	if _, err := cs.GetEventType(ctx, "kn1", "stock.alert"); statusOf(t, err) != 404 {
		t.Fatalf("the refused type was stored: %v", err)
	}
	cs.definitions.err = errors.New("definitions are down")
	if _, err := cs.CreateEventType(ctx, "kn1", interfaces.KnEventTypeInput{Code: "other", Name: "其他", Source: "report"}); statusOf(t, err) != 500 {
		t.Fatalf("a class that could not be made: %v", err)
	}
	if _, err := cs.GetEventType(ctx, "kn1", "other"); statusOf(t, err) != 404 {
		t.Fatalf("a type without its class was stored: %v", err)
	}
}

func TestIntegrationTheClassFollowsItsTypesNameAndSubject(t *testing.T) {
	cs := newClassService(t)
	ctx := userContext()
	cs.definitions.subject("kn1", "main", "order", "order_id")
	cs.definitions.subject("kn1", "main", "shipment", "shipment_no")
	createTypes(t, cs.service, ctx, "kn1")
	cs.report(t, "kn1", "PO-1")
	cs.round()
	if _, _, synced := cs.state(t, "kn1"); !reflect.DeepEqual(synced, []string{"late_order", "low_stock"}) {
		t.Fatalf("synced %v", synced)
	}
	cs.definitions.writes = nil

	// What the class does not show changes without it.
	must(cs.UpdateEventType(ctx, "kn1", "late_order", interfaces.KnEventTypeInput{Name: "订单超期", Subject: "order", MergeHours: 2, Severity: "high"}))
	if len(cs.definitions.writes) != 0 {
		t.Fatalf("writes %v", cs.definitions.writes)
	}
	renamed := must(cs.UpdateEventType(ctx, "kn1", "late_order", interfaces.KnEventTypeInput{Name: "订单延期", Subject: "shipment", MergeHours: 2}))
	if renamed.ObjectTypeID != "late_order" || renamed.DatasetID == "" {
		t.Fatalf("renamed %+v", renamed)
	}
	relation := cs.definitions.relations["kn1/main/late_order_subject"]
	if cs.definitions.objects["kn1/main/late_order"].OTName != "订单延期" || relation.TargetObjectTypeID != "shipment" ||
		relation.MappingRules.([]interfaces.Mapping)[0].TargetProp.Name != "shipment_no" {
		t.Fatalf("class after the update: %v", cs.definitions.writes)
	}
	// The dataset is the same one and still holds the type's events.
	if _, _, synced := cs.state(t, "kn1"); !reflect.DeepEqual(synced, []string{"late_order", "low_stock"}) || len(cs.datasets.cleared) != 0 {
		t.Fatalf("synced %v, cleared %v", synced, cs.datasets.cleared)
	}
	cs.agrees(t, "kn1", "late_order")

	cs.definitions.err = errors.New("definitions are down")
	if _, err := cs.UpdateEventType(ctx, "kn1", "late_order", interfaces.KnEventTypeInput{Name: "又改名"}); statusOf(t, err) != 500 {
		t.Fatalf("a class that could not be written: %v", err)
	}
	if got := cs.eventType(t, "kn1", "late_order").Name; got != "订单延期" {
		t.Fatalf("the type changed without its class: %s", got)
	}
}

func TestIntegrationDatasetsFollowTheLedger(t *testing.T) {
	cs := newClassService(t)
	ctx := userContext()
	createTypes(t, cs.service, ctx, "kn1")
	createTypes(t, cs.service, ctx, "kn2")
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
	dataset := cs.eventType(t, "kn1", "late_order").DatasetID
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

func TestIntegrationALongFeedIsFollowedPageByPage(t *testing.T) {
	cs := newClassService(t)
	createTypes(t, cs.service, userContext(), "kn1")
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
	dataset := cs.eventType(t, "kn1", "low_stock").DatasetID
	cs.datasets.drop(dataset)
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

// Event types from before events were object types have no class. The worker
// gives them one, except in a versioned network, and writes their events.
func TestIntegrationEarlierEventTypesAreGivenTheirClasses(t *testing.T) {
	cs := newClassService(t)
	ctx := userContext()
	createTypes(t, cs.service, ctx, "kn1")
	createTypes(t, cs.service, ctx, "kn2")
	cs.report(t, "kn1", "PO-1")
	cs.check(t, "kn1", "A")
	cs.report(t, "kn2", "PO-9")
	// As the upgrade leaves them: no class, and nothing in any dataset.
	if _, err := cs.db.Exec("UPDATE " + typeTable + " SET f_object_type_id = '', f_dataset_id = '', f_dataset_synced = 0"); err != nil {
		t.Fatal(err)
	}
	cs.classes.definitions, cs.classes.datasets = newDefinitionsStub(), newDatasetsStub()
	cs.definitions, cs.datasets = cs.classes.definitions.(*definitionsStub), cs.classes.datasets.(*datasetsStub)
	cs.definitions.versioned["kn2"] = true

	cs.round()
	for _, code := range []string{"late_order", "low_stock"} {
		if given := cs.eventType(t, "kn1", code); given.ObjectTypeID != code || given.DatasetID == "" {
			t.Fatalf("%s was not given its class: %+v", code, given)
		}
		cs.agrees(t, "kn1", code)
	}
	if last, projected, synced := cs.state(t, "kn1"); projected != last || !reflect.DeepEqual(synced, []string{"late_order", "low_stock"}) {
		t.Fatalf("kn1 at %d of %d, synced %v", projected, last, synced)
	}
	// The draft of a versioned network is not written to behind its people's backs.
	if waiting := cs.eventType(t, "kn2", "late_order"); waiting.ObjectTypeID != "" || len(cs.definitions.objects) != 2 {
		t.Fatalf("kn2 was given a class: %+v", waiting)
	}
	// Its feed is followed all the same, so it does not come up every round.
	if last, projected, _ := cs.state(t, "kn2"); projected != last {
		t.Fatalf("kn2 at %d of %d", projected, last)
	}

	// The type gets its class when someone updates it; the events it already
	// had are then written from the ledger.
	must(cs.UpdateEventType(ctx, "kn2", "late_order", interfaces.KnEventTypeInput{Name: "订单超期", Subject: "order"}))
	if cs.definitions.objects["kn2/draft/late_order"] == nil {
		t.Fatalf("the class was not written to the draft: %v", cs.definitions.writes)
	}
	cs.round()
	cs.agrees(t, "kn2", "late_order")

	// Types without a class are looked for now and then, not every round.
	if _, err := cs.db.Exec("UPDATE " + typeTable + " SET f_object_type_id = '', f_dataset_id = '', f_dataset_synced = 0 WHERE f_kn_id = 'kn1' AND f_code = 'low_stock'"); err != nil {
		t.Fatal(err)
	}
	cs.round()
	if cs.eventType(t, "kn1", "low_stock").ObjectTypeID != "" {
		t.Fatal("types without a class were looked for again at once")
	}
	cs.at = cs.at.Add(backfillPeriod)
	cs.round()
	if cs.eventType(t, "kn1", "low_stock").ObjectTypeID == "" {
		t.Fatal("the type was not given its class after the period")
	}
}

func TestIntegrationAWriteThatFailsIsTriedAgainLater(t *testing.T) {
	cs := newClassService(t)
	createTypes(t, cs.service, userContext(), "kn1")
	createTypes(t, cs.service, userContext(), "kn2")
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
	createTypes(t, cs.service, userContext(), "kn1")
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

// The datasets outlive their networks, and their IDs follow from the
// network's: a network made again under the ID of a deleted one must not
// show the old one's events.
func TestIntegrationANewTypeDoesNotInheritTheEventsOfADeletedNetwork(t *testing.T) {
	cs := newClassService(t)
	ctx := userContext()
	createTypes(t, cs.service, ctx, "kn1")
	cs.report(t, "kn1", "PO-1")
	cs.round()
	dataset := cs.eventType(t, "kn1", "late_order").DatasetID
	if len(cs.datasets.ids(dataset)) != 1 {
		t.Fatal("the event was not written")
	}
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

	createTypes(t, cs.service, ctx, "kn1")
	if got := cs.datasets.ids(dataset); len(got) != 0 {
		t.Fatalf("the new type shows %v", got)
	}
	second := cs.report(t, "kn1", "PO-2")
	cs.round()
	if got := cs.datasets.ids(dataset); !reflect.DeepEqual(got, []string{second.ID}) {
		t.Fatalf("dataset holds %v", got)
	}
}

func TestIntegrationADeletedEventTypeTakesItsClassAndDataset(t *testing.T) {
	cs := newClassService(t)
	ctx := userContext()
	cs.definitions.subject("kn1", "main", "order", "order_id")
	createTypes(t, cs.service, ctx, "kn1")
	late, low := cs.eventType(t, "kn1", "late_order"), cs.eventType(t, "kn1", "low_stock")

	// A type with events keeps its class and its dataset.
	cs.report(t, "kn1", "PO-1")
	if err := cs.DeleteEventType(ctx, "kn1", "late_order"); statusOf(t, err) != 409 {
		t.Fatalf("delete of a type with events: %v", err)
	}
	if cs.definitions.objects["kn1/main/late_order"] == nil || cs.definitions.relations["kn1/main/late_order_subject"] == nil ||
		cs.datasets.resources[late.DatasetID] == nil {
		t.Fatal("a type with events lost its class")
	}

	// So does a type whose class other definitions use.
	cs.definitions.users["kn1/main/low_stock"] = []string{"补货"}
	if err := cs.DeleteEventType(ctx, "kn1", "low_stock"); statusOf(t, err) != 409 {
		t.Fatalf("delete of a type whose class is used: %v", err)
	}
	if cs.eventType(t, "kn1", "low_stock").ObjectTypeID != "low_stock" || cs.definitions.objects["kn1/main/low_stock"] == nil {
		t.Fatal("a type whose class is used lost it")
	}

	delete(cs.definitions.users, "kn1/main/low_stock")
	must(0, cs.DeleteEventType(ctx, "kn1", "low_stock"))
	if _, err := cs.GetEventType(ctx, "kn1", "low_stock"); statusOf(t, err) != 404 {
		t.Fatalf("the type is still there: %v", err)
	}
	if cs.definitions.objects["kn1/main/low_stock"] != nil || cs.datasets.resources[low.DatasetID] != nil {
		t.Fatal("the class or the dataset of a deleted type is left")
	}

	// Made again, the type has its class and dataset again.
	createLow := interfaces.KnEventTypeInput{Code: "low_stock", Name: "低库存", Source: "rule", Subject: "sku",
		Rule: json.RawMessage(`{"where":"qty < 10"}`), RuleVersion: "v1"}
	if again := must(cs.CreateEventType(ctx, "kn1", createLow)); again.DatasetID != low.DatasetID ||
		cs.definitions.objects["kn1/main/low_stock"] == nil || cs.datasets.resources[low.DatasetID] == nil {
		t.Fatalf("made again %+v", again)
	}
}

// A dot in a code is an underscore in its class ID, so stock.low and
// stock_low stand for one class. Only one of them may have it.
func TestIntegrationTwoCodesOfOneClassAreNotBothMade(t *testing.T) {
	cs := newClassService(t)
	ctx := userContext()
	low := must(cs.CreateEventType(ctx, "kn1", interfaces.KnEventTypeInput{Code: "stock.low", Name: "低库存", Source: "report"}))
	cs.definitions.writes = nil

	_, err := cs.CreateEventType(ctx, "kn1", interfaces.KnEventTypeInput{Code: "stock_low", Name: "库存偏低", Source: "report"})
	if statusOf(t, err) != 409 || !strings.Contains(fmt.Sprint(err), "stock.low") {
		t.Fatalf("a code whose class is another type's: %v", err)
	}
	class := cs.definitions.objects["kn1/main/stock_low"]
	if len(cs.definitions.writes) != 0 || class.OTName != "低库存" || class.DataSource.ID != low.DatasetID ||
		cs.datasets.resources[classDatasetID("kn1", "stock_low")] != nil {
		t.Fatalf("the refused code wrote %v; the class reads %s", cs.definitions.writes, class.DataSource.ID)
	}
	if _, err := cs.GetEventType(ctx, "kn1", "stock_low"); statusOf(t, err) != 404 {
		t.Fatalf("the refused type was stored: %v", err)
	}
	// Other networks have classes of their own.
	must(cs.CreateEventType(ctx, "kn2", interfaces.KnEventTypeInput{Code: "stock_low", Name: "库存偏低", Source: "report"}))

	// A type without a class yet stands for its class all the same.
	if _, err := cs.db.Exec("UPDATE " + typeTable + " SET f_object_type_id = '', f_dataset_id = '' WHERE f_kn_id = 'kn1'"); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.CreateEventType(ctx, "kn1", interfaces.KnEventTypeInput{Code: "stock_low", Name: "库存偏低", Source: "report"}); statusOf(t, err) != 409 {
		t.Fatalf("a code whose class an earlier type is to be given: %v", err)
	}

	// Once the type is deleted, the other code can have the class.
	must(0, cs.DeleteEventType(ctx, "kn1", "stock.low"))
	if made := must(cs.CreateEventType(ctx, "kn1", interfaces.KnEventTypeInput{Code: "stock_low", Name: "库存偏低", Source: "report"})); made.ObjectTypeID != "stock_low" ||
		cs.definitions.objects["kn1/main/stock_low"].DataSource.ID != made.DatasetID {
		t.Fatalf("made %+v", made)
	}
}

// Two event types from before events were object types can stand for one
// class. The first to be given it keeps it.
func TestIntegrationOfTwoEarlierTypesOfOneClassTheFirstHasIt(t *testing.T) {
	cs := newClassService(t)
	ctx := userContext()
	createTypes(t, cs.service, ctx, "kn1")
	columns := strings.Join(typeColumns, ", ")
	if _, err := cs.db.Exec("INSERT INTO " + typeTable + " (" + columns + ") SELECT " + strings.Replace(columns, "f_code", "'late.order'", 1) +
		" FROM " + typeTable + " WHERE f_kn_id = 'kn1' AND f_code = 'late_order'"); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.db.Exec("UPDATE " + typeTable + " SET f_object_type_id = '', f_dataset_id = '', f_dataset_synced = 0"); err != nil {
		t.Fatal(err)
	}
	cs.classes.definitions = newDefinitionsStub()
	cs.definitions = cs.classes.definitions.(*definitionsStub)

	cs.round()
	first, second := cs.eventType(t, "kn1", "late.order"), cs.eventType(t, "kn1", "late_order")
	if first.ObjectTypeID != "late_order" || second.ObjectTypeID != "" ||
		cs.definitions.objects["kn1/main/late_order"].DataSource.ID != first.DatasetID {
		t.Fatalf("given %+v and %+v", first, second)
	}
	// The other is told why it gets no class when it is changed.
	if _, err := cs.UpdateEventType(ctx, "kn1", "late_order", interfaces.KnEventTypeInput{Name: "订单超期"}); statusOf(t, err) != 409 {
		t.Fatalf("update of the type whose class the other has: %v", err)
	}
	cs.at = cs.at.Add(backfillPeriod)
	cs.round()
	if cs.definitions.objects["kn1/main/late_order"].DataSource.ID != first.DatasetID {
		t.Fatal("the class was given to the other type")
	}
}

// Whatever makes, changes or removes a network's classes waits for whatever
// else does, so that a class is written by one of them at a time.
func TestIntegrationChangesOfANetworksClassesWaitForEachOther(t *testing.T) {
	cs := newClassService(t)
	ctx := userContext()
	cs.store.classWait = 1
	createTypes(t, cs.service, ctx, "kn1")
	if _, err := cs.db.Exec("UPDATE " + typeTable + " SET f_object_type_id = '', f_dataset_id = '' WHERE f_code = 'low_stock'"); err != nil {
		t.Fatal(err)
	}
	cs.definitions.writes = nil
	entered, leave, left := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		left <- cs.store.exclusive(context.Background(), classLock("kn1"), func() error {
			close(entered)
			<-leave
			return nil
		})
	}()
	<-entered

	input := interfaces.KnEventTypeInput{Code: "other", Name: "其他", Source: "report"}
	if _, err := cs.CreateEventType(ctx, "kn1", input); statusOf(t, err) != 500 {
		t.Fatalf("create while the classes are changed elsewhere: %v", err)
	}
	if _, err := cs.UpdateEventType(ctx, "kn1", "late_order", interfaces.KnEventTypeInput{Name: "订单延期"}); statusOf(t, err) != 500 {
		t.Fatalf("update while the classes are changed elsewhere: %v", err)
	}
	if err := cs.DeleteEventType(ctx, "kn1", "late_order"); statusOf(t, err) != 500 {
		t.Fatalf("delete while the classes are changed elsewhere: %v", err)
	}
	cs.round()
	if len(cs.definitions.writes) != 0 || cs.eventType(t, "kn1", "low_stock").ObjectTypeID != "" {
		t.Fatalf("wrote %v while the classes were changed elsewhere", cs.definitions.writes)
	}
	// Other networks do not wait.
	must(cs.CreateEventType(ctx, "kn2", input))

	close(leave)
	if err := <-left; err != nil {
		t.Fatal(err)
	}
	must(cs.CreateEventType(ctx, "kn1", input))
	cs.at = cs.at.Add(backfillPeriod)
	cs.round()
	if cs.eventType(t, "kn1", "low_stock").ObjectTypeID != "low_stock" {
		t.Fatal("the type was not given its class once the lock was free")
	}
}

// A class made for a type that is then not stored is taken back, so that no
// class is left in the network that no type stands for.
func TestIntegrationAClassMadeForATypeThatWasNotStoredIsTakenBack(t *testing.T) {
	cs := newClassService(t)
	ctx := userContext()
	cs.definitions.subject("kn1", "main", "order", "order_id")
	input := interfaces.KnEventTypeInput{Code: "late_order", Name: "订单超期", Source: "report", Subject: "order"}
	gone := func() {
		t.Helper()
		if cs.definitions.objects["kn1/main/late_order"] != nil || cs.definitions.relations["kn1/main/late_order_subject"] != nil ||
			cs.datasets.resources[classDatasetID("kn1", "late_order")] != nil {
			t.Fatalf("left %v", cs.definitions.writes)
		}
		if _, err := cs.GetEventType(ctx, "kn1", "late_order"); statusOf(t, err) != 404 {
			t.Fatalf("the type was stored: %v", err)
		}
	}

	// The network is deleted while the class is made.
	networks := cs.service.networks.(networksStub)
	cs.definitions.saved = func(string) { networks["kn1"] = false }
	if _, err := cs.CreateEventType(ctx, "kn1", input); statusOf(t, err) != 404 {
		t.Fatalf("create in a network deleted meanwhile: %v", err)
	}
	cs.definitions.saved, networks["kn1"] = nil, true
	gone()

	// The class fails half way.
	cs.definitions.relationErr = errors.New("relations are down")
	if _, err := cs.CreateEventType(ctx, "kn1", input); statusOf(t, err) != 500 {
		t.Fatalf("create whose relation failed: %v", err)
	}
	cs.definitions.relationErr = nil
	gone()

	// What was there before is kept.
	made := must(cs.CreateEventType(ctx, "kn1", input))
	cs.definitions.saved = func(string) { networks["kn1"] = false }
	if _, err := cs.UpdateEventType(ctx, "kn1", "late_order", interfaces.KnEventTypeInput{Name: "订单延期", Subject: "order"}); statusOf(t, err) != 404 {
		t.Fatalf("update in a network deleted meanwhile: %v", err)
	}
	cs.definitions.saved, networks["kn1"] = nil, true
	if cs.definitions.objects["kn1/main/late_order"] == nil || cs.datasets.resources[made.DatasetID] == nil {
		t.Fatal("an update took back the class it found")
	}
}

// A deletion that fails after the class is gone leaves the type without one,
// and the type is then given its class again with its events.
func TestIntegrationATypeWhoseDeletionFailedIsGivenItsClassAgain(t *testing.T) {
	cs := newClassService(t)
	createTypes(t, cs.service, userContext(), "kn1")
	ctx, cancel := context.WithCancel(userContext())
	defer cancel()
	cs.definitions.removed = func(string) { cancel() }
	if err := cs.DeleteEventType(ctx, "kn1", "low_stock"); statusOf(t, err) != 500 {
		t.Fatalf("delete that failed after the class was removed: %v", err)
	}
	cs.definitions.removed = nil
	left := cs.eventType(t, "kn1", "low_stock")
	if left.ObjectTypeID != "" || left.DatasetID != "" || cs.definitions.objects["kn1/main/low_stock"] != nil {
		t.Fatalf("left %+v", left)
	}

	cs.check(t, "kn1", "A", "B")
	cs.round()
	if given := cs.eventType(t, "kn1", "low_stock"); given.ObjectTypeID != "low_stock" || given.DatasetID != classDatasetID("kn1", "low_stock") ||
		cs.definitions.objects["kn1/main/low_stock"] == nil {
		t.Fatalf("given %+v", given)
	}
	cs.agrees(t, "kn1", "low_stock")
}

func TestIntegrationOneReplicaWritesTheDatasetsAtATime(t *testing.T) {
	cs := newClassService(t)
	createTypes(t, cs.service, userContext(), "kn1")
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

	// Without the licence nothing is written; events go on being recorded.
	cs.projector.allowed = func() bool { return false }
	cs.report(t, "kn1", "PO-2")
	cs.round()
	if last, projected, _ := cs.state(t, "kn1"); projected >= last {
		t.Fatal("wrote without the licence")
	}
}
