// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knevent

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"sync"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// datasetsStub keeps catalogs, datasets and their documents as Vega would, and who wrote them.
type datasetsStub struct {
	interfaces.VegaBackendService
	mu        sync.Mutex
	catalogs  map[string]*interfaces.CatalogRequest
	resources map[string]*interfaces.VegaResource
	// documents are a dataset's documents by ID.
	documents map[string]map[string]map[string]any
	// written counts the writes of each document.
	written  map[string]int
	cleared  []string
	actors   map[string]bool
	writeErr error
}

func newDatasetsStub() *datasetsStub {
	return &datasetsStub{
		catalogs: map[string]*interfaces.CatalogRequest{}, resources: map[string]*interfaces.VegaResource{},
		documents: map[string]map[string]map[string]any{}, written: map[string]int{}, actors: map[string]bool{},
	}
}

func (d *datasetsStub) as(ctx context.Context) {
	account, _ := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	d.actors[account.Type+":"+account.ID] = true
}

func (d *datasetsStub) GetCatalogByID(_ context.Context, id string) (*interfaces.Catalog, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.catalogs[id] == nil {
		return nil, nil
	}
	return &interfaces.Catalog{}, nil
}

func (d *datasetsStub) CreateCatalog(ctx context.Context, request *interfaces.CatalogRequest) (*interfaces.Catalog, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.as(ctx)
	d.catalogs[request.ID] = request
	return &interfaces.Catalog{}, nil
}

func (d *datasetsStub) GetResourceByID(_ context.Context, id string) (*interfaces.VegaResource, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.resources[id], nil
}

func (d *datasetsStub) CreateResource(ctx context.Context, resource *interfaces.VegaResource) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.as(ctx)
	d.resources[resource.ID] = resource
	if d.documents[resource.ID] == nil {
		d.documents[resource.ID] = map[string]map[string]any{}
	}
	return nil
}

func (d *datasetsStub) WriteDatasetDocument(ctx context.Context, datasetID, docID string, document map[string]any) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.writeErr != nil {
		return d.writeErr
	}
	if d.resources[datasetID] == nil {
		return errors.New("WriteDatasetDocument returned HTTP 404")
	}
	d.as(ctx)
	d.documents[datasetID][docID] = document
	d.written[datasetID+"/"+docID]++
	return nil
}

func (d *datasetsStub) DeleteDatasetDocumentsByQuery(ctx context.Context, datasetID string, filter map[string]any) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.as(ctx)
	condition := filter["sub_conditions"].([]map[string]any)[0]
	for id, document := range d.documents[datasetID] {
		if document[condition["field"].(string)] == condition["value"] {
			delete(d.documents[datasetID], id)
		}
	}
	d.cleared = append(d.cleared, datasetID)
	return nil
}

func (d *datasetsStub) DeleteResource(ctx context.Context, id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.resources[id] == nil {
		return errors.New("DeleteResource returned HTTP 404")
	}
	d.as(ctx)
	delete(d.resources, id)
	delete(d.documents, id)
	return nil
}

// drop loses a dataset, as deleting it in Vega would.
func (d *datasetsStub) drop(datasetID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.resources, datasetID)
	delete(d.documents, datasetID)
}

func (d *datasetsStub) ids(datasetID string) []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	ids := make([]string, 0, len(d.documents[datasetID]))
	for id := range d.documents[datasetID] {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (d *datasetsStub) document(datasetID, id string) map[string]any {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.documents[datasetID][id]
}

func newTestClasses() (*classes, *datasetsStub) {
	datasets := newDatasetsStub()
	return &classes{datasets: datasets}, datasets
}

const platform = interfaces.ADMIN_ACCOUNT_TYPE + ":" + interfaces.ADMIN_ACCOUNT_ID

func TestEnsureMakesTheCatalogAndTheDatasetAsThePlatform(t *testing.T) {
	classes, datasets := newTestClasses()
	made, err := classes.ensure(userContext(), "kn1", "late_order")
	if err != nil || !made {
		t.Fatalf("ensure = %v, %v", made, err)
	}
	datasetID := classDatasetID("kn1", "late_order")
	dataset := datasets.resources[datasetID]
	if datasets.catalogs[eventCatalogID] == nil || dataset == nil || dataset.CatalogID != eventCatalogID ||
		len(dataset.SchemaDefinition) != len(classFields) {
		t.Fatalf("catalog %v, dataset %+v", datasets.catalogs, dataset)
	}
	if len(datasets.actors) != 1 || !datasets.actors[platform] {
		t.Fatalf("written by %v, want the platform alone", datasets.actors)
	}
	if made, err := classes.ensure(userContext(), "kn1", "late_order"); err != nil || made {
		t.Fatalf("ensure again = %v, %v, want the dataset that is there", made, err)
	}
}

func TestEnsureRefusesADatasetOfAnotherCatalog(t *testing.T) {
	classes, datasets := newTestClasses()
	datasetID := classDatasetID("kn1", "late_order")
	datasets.resources[datasetID] = &interfaces.VegaResource{ID: datasetID, CatalogID: "someone_elses"}
	_, err := classes.ensure(userContext(), "kn1", "late_order")
	var foreign errForeignDataset
	if !errors.As(err, &foreign) {
		t.Fatalf("ensure = %v, want the foreign dataset refused", err)
	}
}

func TestClearEmptiesTheDatasetOfTheClass(t *testing.T) {
	classes, datasets := newTestClasses()
	if _, err := classes.ensure(userContext(), "kn1", "late_order"); err != nil {
		t.Fatal(err)
	}
	datasetID := classDatasetID("kn1", "late_order")
	// A network of the same ID, deleted earlier, left an event of the class.
	if err := classes.write(userContext(), datasetID, []*interfaces.KnEvent{{ID: "old", EventType: "late_order", Title: "x"}}); err != nil {
		t.Fatal(err)
	}
	if err := classes.clear(userContext(), "kn1", "late_order"); err != nil {
		t.Fatal(err)
	}
	if got := datasets.ids(datasetID); len(got) != 0 {
		t.Fatalf("left %v", got)
	}
}

func TestRestoreMakesOnlyADatasetThatIsGone(t *testing.T) {
	classes, datasets := newTestClasses()
	if _, err := classes.ensure(userContext(), "kn1", "late_order"); err != nil {
		t.Fatal(err)
	}
	if made, err := classes.restore(userContext(), "kn1", "late_order"); err != nil || made {
		t.Fatalf("restore of a dataset that is there = %v, %v", made, err)
	}
	datasetID := classDatasetID("kn1", "late_order")
	datasets.drop(datasetID)
	if made, err := classes.restore(userContext(), "kn1", "late_order"); err != nil || !made || datasets.resources[datasetID] == nil {
		t.Fatalf("restore = %v, %v", made, err)
	}
}

func TestClassDatasetIDs(t *testing.T) {
	first, same, otherClass, otherNetwork := classDatasetID("kn1", "t"), classDatasetID("kn1", "t"),
		classDatasetID("kn1", "u"), classDatasetID("kn2", "t")
	if first != same || first == otherClass || first == otherNetwork || len(first) != len(datasetPrefix)+24 {
		t.Fatalf("dataset IDs %s %s %s %s", first, same, otherClass, otherNetwork)
	}
	// The parts are told apart: "a"+"bc" is not "ab"+"c".
	if classDatasetID("a", "bc") == classDatasetID("ab", "c") {
		t.Fatal("dataset IDs of different networks and classes agree")
	}
}

func TestEventDocument(t *testing.T) {
	document := eventDocument(&interfaces.KnEvent{
		ID: "e1", KNID: "kn1", EventType: "order.late", Title: "PO-1 超期", Status: "verified", Source: "report",
		ObjectType: "order", ObjectKey: "PO-1", ObjectName: "采购单 1",
		OccurredAt: 1_794_400_000_123, RecordedAt: 1_794_400_001_000,
		Detail: json.RawMessage(`{"days":3,"lines":[1,2]}`), ReportedBy: "u1", ReportedByName: "Alice",
	})
	want := map[string]any{
		"event_id": "e1", "event_type": "order.late", "title": "PO-1 超期", "status": "verified", "source": "report",
		"object_key": "PO-1", "object_name": "采购单 1",
		"occurred_at": "2026-11-11T12:26:40.123Z", "recorded_at": "2026-11-11T12:26:41.000Z",
		"backlog": false, "reported_by": "u1", "reported_by_name": "Alice",
		"detail": map[string]any{"days": float64(3), "lines": []any{float64(1), float64(2)}},
	}
	// What an event has not is left out: a time that has not come is no date,
	// and an empty text is no value.
	if !reflect.DeepEqual(document, want) {
		t.Fatalf("document\n got %v\nwant %v", document, want)
	}
	for _, detail := range []string{"", "{}", "not json"} {
		if _, there := eventDocument(&interfaces.KnEvent{ID: "e", Detail: json.RawMessage(detail)})["detail"]; there {
			t.Fatalf("detail %q was written", detail)
		}
	}
	names := map[string]bool{}
	for _, field := range classFields {
		// A label that is missing from the locale falls back to the name.
		if names[field.name] || field.label() == field.name {
			t.Fatalf("field %+v", field)
		}
		names[field.name] = true
	}
}
