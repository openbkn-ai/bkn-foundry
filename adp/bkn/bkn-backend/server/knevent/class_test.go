// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knevent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces/data_type"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
)

// definitionsStub keeps the object types and relation types of networks as
// the definition services would, and who wrote them.
type definitionsStub struct {
	mu        sync.Mutex
	versioned map[string]bool
	objects   map[string]*interfaces.ObjectType
	relations map[string]*interfaces.RelationType
	// writes lists what was saved and removed, in order.
	writes []string
	actors map[string]bool
	// err is what saving an object type is refused with, relationErr what
	// saving a relation type is.
	err, relationErr error
	// users are the definitions, other than relation types kept here, that use
	// an object type, by its key.
	users map[string][]string
	// saved and removed are called with the key of each object type saved or
	// removed, after it is.
	saved, removed func(key string)
}

func newDefinitionsStub() *definitionsStub {
	return &definitionsStub{
		versioned: map[string]bool{}, objects: map[string]*interfaces.ObjectType{},
		relations: map[string]*interfaces.RelationType{}, actors: map[string]bool{}, users: map[string][]string{},
	}
}

func definitionKey(knID, branch, id string) string { return knID + "/" + branch + "/" + id }

func (d *definitionsStub) as(ctx context.Context) {
	account, _ := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	d.actors[account.Type+":"+account.ID] = true
}

func (d *definitionsStub) branch(_ context.Context, knID string) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.versioned[knID] {
		return interfaces.DRAFT_BRANCH, nil
	}
	return interfaces.MAIN_BRANCH, nil
}

func (d *definitionsStub) objectTypes(_ context.Context, knID, branch string, ids []string) (map[string]*interfaces.ObjectType, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	found := map[string]*interfaces.ObjectType{}
	for _, id := range ids {
		if objectType := d.objects[definitionKey(knID, branch, id)]; objectType != nil {
			found[id] = objectType
		}
	}
	return found, nil
}

func (d *definitionsStub) saveObjectType(ctx context.Context, objectType *interfaces.ObjectType) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.err != nil {
		return d.err
	}
	d.as(ctx)
	key := definitionKey(objectType.KNID, objectType.Branch, objectType.OTID)
	d.objects[key] = objectType
	d.writes = append(d.writes, "object "+key)
	if d.saved != nil {
		d.saved(key)
	}
	return nil
}

func (d *definitionsStub) saveRelationType(ctx context.Context, relationType *interfaces.RelationType) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.relationErr != nil {
		return d.relationErr
	}
	d.as(ctx)
	key := definitionKey(relationType.KNID, relationType.Branch, relationType.RTID)
	d.relations[key] = relationType
	d.writes = append(d.writes, "relation "+key)
	return nil
}

func (d *definitionsStub) removeRelationType(ctx context.Context, knID, branch, id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	key := definitionKey(knID, branch, id)
	if d.relations[key] != nil {
		d.as(ctx)
		delete(d.relations, key)
		d.writes = append(d.writes, "no relation "+key)
	}
	return nil
}

func (d *definitionsStub) usersOf(_ context.Context, knID, branch, id, except string) ([]string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	users := append([]string{}, d.users[definitionKey(knID, branch, id)]...)
	for _, relation := range d.relations {
		if relation.KNID == knID && relation.Branch == branch && relation.RTID != except &&
			(relation.SourceObjectTypeID == id || relation.TargetObjectTypeID == id) {
			users = append(users, relation.RTName)
		}
	}
	sort.Strings(users)
	return users, nil
}

func (d *definitionsStub) removeObjectType(ctx context.Context, knID, branch, id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	key := definitionKey(knID, branch, id)
	if d.objects[key] == nil {
		return httperrors.NewHTTPError(ctx, http.StatusNotFound, berrors.BknBackend_ObjectType_ObjectTypeNotFound)
	}
	d.as(ctx)
	delete(d.objects, key)
	d.writes = append(d.writes, "no object "+key)
	if d.removed != nil {
		d.removed(key)
	}
	return nil
}

// datasetsStub is Vega's catalogs and datasets in memory.
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

func newTestClasses() (*classes, *definitionsStub, *datasetsStub) {
	definitions, datasets := newDefinitionsStub(), newDatasetsStub()
	return &classes{definitions: definitions, datasets: datasets}, definitions, datasets
}

// subject puts an object type that events are about into a network.
func (d *definitionsStub) subject(knID, branch, id string, keys ...string) {
	d.objects[definitionKey(knID, branch, id)] = &interfaces.ObjectType{
		ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
			OTID: id, PrimaryKeys: keys, DataSource: &interfaces.ResourceInfo{ID: "orders_table"},
		},
	}
}

const platform = interfaces.ADMIN_ACCOUNT_TYPE + ":" + interfaces.ADMIN_ACCOUNT_ID

var lateOrder = interfaces.KnEventType{KNID: "kn1", Code: "order.late", Name: "订单超期", Source: "report", Subject: "order"}

func TestEnsureMakesTheDatasetTheObjectTypeAndTheRelation(t *testing.T) {
	classes, definitions, datasets := newTestClasses()
	definitions.subject("kn1", "main", "order", "order_id")
	class, err := classes.ensure(i18n.WithLanguage(userContext(), i18n.AmericanEnglish), lateOrder)
	if err != nil {
		t.Fatal(err)
	}
	// An object type ID takes no dot; the code keeps its own.
	if class.objectTypeID != "order_late" || !strings.HasPrefix(class.datasetID, "kn_event_") || class.leftover {
		t.Fatalf("class %+v", class)
	}
	if catalog := datasets.catalogs[eventCatalogID]; catalog == nil || !catalog.Enabled {
		t.Fatalf("catalog %+v", catalog)
	}
	dataset := datasets.resources[class.datasetID]
	if dataset == nil || dataset.CatalogID != eventCatalogID || dataset.Category != "dataset" || dataset.IndexConfig == nil ||
		len(dataset.SchemaDefinition) != len(classFields) {
		t.Fatalf("dataset %+v", dataset)
	}
	for _, field := range dataset.SchemaDefinition {
		// Text is matched as a whole, and not indexed beyond what Vega indexes.
		if text := field.Type == data_type.DATATYPE_STRING; text != (len(field.Features) == 1) ||
			text && field.Features[0].Config[interfaces.FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE] != keywordLimit {
			t.Fatalf("field %+v", field)
		}
	}

	objectType := definitions.objects["kn1/main/order_late"]
	if objectType == nil || objectType.OTName != "订单超期" || objectType.DataSource.ID != class.datasetID ||
		objectType.DataSource.Type != interfaces.DATA_SOURCE_TYPE_RESOURCE ||
		!reflect.DeepEqual(objectType.PrimaryKeys, []string{"event_id"}) || objectType.DisplayKey != "title" ||
		!reflect.DeepEqual(objectType.Tags, []string{"event"}) || len(objectType.DataProperties) != len(classFields) {
		t.Fatalf("object type %+v", objectType)
	}
	semantics := map[string]string{}
	for _, property := range objectType.DataProperties {
		if property.MappedField == nil || property.MappedField.Name != property.Name || property.DisplayName == "" {
			t.Fatalf("property %+v", property)
		}
		if property.Semantic != "" {
			semantics[property.Name] = property.Semantic
		}
	}
	if want := map[string]string{"status": "status", "severity": "severity", "occurred_at": "event_time", "ended_at": "end_time"}; !reflect.DeepEqual(semantics, want) {
		t.Fatalf("semantics %v", semantics)
	}
	// The texts are in Chinese, whatever language the caller reads.
	if property := objectType.DataProperties[0]; property.Name != "event_id" || property.DisplayName != "事件 ID" ||
		objectType.Comment != "事件类：每个对象是一条“订单超期”事件，由平台的事件模块写入。" {
		t.Fatalf("texts %q %q", property.DisplayName, objectType.Comment)
	}

	relation := definitions.relations["kn1/main/order_late_subject"]
	if relation == nil || relation.SourceObjectTypeID != "order_late" || relation.TargetObjectTypeID != "order" ||
		relation.Type != interfaces.RELATION_TYPE_DIRECT || relation.RTName != "关于" || relation.Comment != "“订单超期”事件所关于的对象。" {
		t.Fatalf("relation %+v", relation)
	}
	mapping := relation.MappingRules.([]interfaces.Mapping)
	if len(mapping) != 1 || mapping[0].SourceProp.Name != "object_key" || mapping[0].TargetProp.Name != "order_id" {
		t.Fatalf("mapping %+v", mapping)
	}

	// The caller needs no permission on what the platform keeps.
	if !reflect.DeepEqual(definitions.actors, map[string]bool{platform: true}) || !reflect.DeepEqual(datasets.actors, map[string]bool{platform: true}) {
		t.Fatalf("written as %v and %v", definitions.actors, datasets.actors)
	}
}

func TestEnsureAgainKeepsTheDatasetAndSaysItWasThere(t *testing.T) {
	classes, definitions, datasets := newTestClasses()
	definitions.subject("kn1", "main", "order", "order_id")
	first, _ := classes.ensure(userContext(), lateOrder)
	created := datasets.resources[first.datasetID]
	renamed := lateOrder
	renamed.Name = "订单延期"
	second, err := classes.ensure(userContext(), renamed)
	if err != nil {
		t.Fatal(err)
	}
	if second.datasetID != first.datasetID || !second.leftover || datasets.resources[first.datasetID] != created {
		t.Fatalf("second %+v", second)
	}
	if got := definitions.objects["kn1/main/order_late"].OTName; got != "订单延期" {
		t.Fatalf("name %s", got)
	}
}

func TestEnsureWritesAVersionedNetworksClassToItsDraft(t *testing.T) {
	classes, definitions, _ := newTestClasses()
	definitions.versioned["kn1"] = true
	definitions.subject("kn1", "draft", "order", "order_id")
	if _, err := classes.ensure(userContext(), lateOrder); err != nil {
		t.Fatal(err)
	}
	if want := []string{"object kn1/draft/order_late", "relation kn1/draft/order_late_subject"}; !reflect.DeepEqual(definitions.writes, want) {
		t.Fatalf("writes %v", definitions.writes)
	}
	if waits, err := classes.versioned(userContext(), "kn1"); err != nil || !waits {
		t.Fatalf("versioned = %v, %v", waits, err)
	}
	if waits, _ := classes.versioned(userContext(), "kn2"); waits {
		t.Fatal("a network without versions waits for none")
	}
}

func TestEnsureRefusesTheIDOfAnObjectTypeThatIsNoEventClass(t *testing.T) {
	classes, definitions, datasets := newTestClasses()
	definitions.subject("kn1", "main", "order_late", "id")
	_, err := classes.ensure(userContext(), lateOrder)
	if statusOf(t, err) != 409 {
		t.Fatalf("err %v", err)
	}
	if len(definitions.writes) != 0 || len(datasets.resources) != 0 {
		t.Fatalf("wrote %v, %d datasets", definitions.writes, len(datasets.resources))
	}
}

func TestEnsureLeavesOutTheRelationToASubjectWithoutASingleKey(t *testing.T) {
	for name, keys := range map[string][]string{"no such object type": nil, "two keys": {"plant", "order_id"}} {
		t.Run(name, func(t *testing.T) {
			classes, definitions, _ := newTestClasses()
			definitions.subject("kn1", "main", "order", "order_id")
			if _, err := classes.ensure(userContext(), lateOrder); err != nil {
				t.Fatal(err)
			}
			// The subject changes; the relation that no longer holds goes.
			if keys == nil {
				delete(definitions.objects, "kn1/main/order")
			} else {
				definitions.subject("kn1", "main", "order", keys...)
			}
			definitions.writes = nil
			if _, err := classes.ensure(userContext(), lateOrder); err != nil {
				t.Fatal(err)
			}
			if want := []string{"object kn1/main/order_late", "no relation kn1/main/order_late_subject"}; !reflect.DeepEqual(definitions.writes, want) {
				t.Fatalf("writes %v", definitions.writes)
			}
		})
	}
	t.Run("no subject", func(t *testing.T) {
		classes, definitions, _ := newTestClasses()
		general := lateOrder
		general.Subject = ""
		if _, err := classes.ensure(userContext(), general); err != nil {
			t.Fatal(err)
		}
		if want := []string{"object kn1/main/order_late"}; !reflect.DeepEqual(definitions.writes, want) {
			t.Fatalf("writes %v", definitions.writes)
		}
	})
}

func TestEnsurePassesOnWhatTheDefinitionsRefuse(t *testing.T) {
	classes, definitions, _ := newTestClasses()
	definitions.err = errors.New("no draft may be written now")
	if _, err := classes.ensure(userContext(), lateOrder); !errors.Is(err, definitions.err) {
		t.Fatalf("err %v", err)
	}
	// An object type of the same name is refused by the platform as
	// forbidden, which says nothing to someone naming an event type.
	definitions.err = httperrors.NewHTTPError(userContext(), http.StatusForbidden, berrors.BknBackend_ObjectType_ObjectTypeNameExisted)
	_, err := classes.ensure(userContext(), lateOrder)
	var refused *httperrors.HTTPError
	if !errors.As(err, &refused) || refused.HTTPCode != http.StatusConflict || refused.ErrorCode != berrors.BknBackend_KnEvent_EventTypeExisted {
		t.Fatalf("a name that is taken: %v", err)
	}
}

func TestClearEmptiesOnlyADatasetThatWasThere(t *testing.T) {
	classes, _, datasets := newTestClasses()
	class, _ := classes.ensure(userContext(), lateOrder)
	if err := classes.clear(userContext(), lateOrder, class); err != nil || len(datasets.cleared) != 0 {
		t.Fatalf("a new dataset was cleared: %v %v", err, datasets.cleared)
	}
	// The network is deleted and one of the same ID gets the same event type.
	if err := classes.write(userContext(), class.datasetID, []*interfaces.KnEvent{{ID: "old", EventType: "order.late", Title: "x"}}); err != nil {
		t.Fatal(err)
	}
	again, _ := classes.ensure(userContext(), lateOrder)
	if err := classes.clear(userContext(), lateOrder, again); err != nil {
		t.Fatal(err)
	}
	if got := datasets.ids(class.datasetID); len(got) != 0 {
		t.Fatalf("left %v", got)
	}
}

func TestRestoreMakesOnlyADatasetThatIsGone(t *testing.T) {
	classes, _, datasets := newTestClasses()
	class, _ := classes.ensure(userContext(), lateOrder)
	stored := lateOrder
	stored.DatasetID = class.datasetID
	if made, err := classes.restore(userContext(), stored); err != nil || made {
		t.Fatalf("restore of a dataset that is there = %v, %v", made, err)
	}
	datasets.drop(class.datasetID)
	if made, err := classes.restore(userContext(), stored); err != nil || !made || datasets.resources[class.datasetID] == nil {
		t.Fatalf("restore = %v, %v", made, err)
	}
}

// stored is an event type as the ledger keeps it once it has its class.
func stored(eventType interfaces.KnEventType, class eventClass) interfaces.KnEventType {
	eventType.ObjectTypeID, eventType.DatasetID = class.objectTypeID, class.datasetID
	return eventType
}

func TestRemoveDeletesTheClassAndItsRelationAndDropTheDataset(t *testing.T) {
	classes, definitions, datasets := newTestClasses()
	definitions.subject("kn1", "main", "order", "order_id")
	class, _ := classes.ensure(userContext(), lateOrder)
	definitions.writes, definitions.actors = nil, map[string]bool{}

	versioned, changed, err := classes.remove(userContext(), stored(lateOrder, class))
	if err != nil || versioned || !changed {
		t.Fatalf("remove = %v, %v, %v", versioned, changed, err)
	}
	// The relation goes first: the platform keeps no object type a relation is bound to.
	want := []string{"no relation kn1/main/order_late_subject", "no object kn1/main/order_late"}
	if !reflect.DeepEqual(definitions.writes, want) || !reflect.DeepEqual(definitions.actors, map[string]bool{platform: true}) {
		t.Fatalf("writes %v by %v", definitions.writes, definitions.actors)
	}
	if definitions.objects["kn1/main/order"] == nil {
		t.Fatal("the subject was removed")
	}
	if err := classes.drop(userContext(), stored(lateOrder, class), versioned); err != nil || datasets.resources[class.datasetID] != nil {
		t.Fatalf("drop: %v", err)
	}
	// What is gone already is not an error, and nothing changes.
	if _, changed, err := classes.remove(userContext(), stored(lateOrder, class)); err != nil || changed {
		t.Fatalf("remove of a class that is gone = %v, %v", changed, err)
	}
	if err := classes.drop(userContext(), stored(lateOrder, class), versioned); err != nil {
		t.Fatal(err)
	}
	// A type from before events were object types has nothing to remove.
	if versioned, changed, err := classes.remove(userContext(), lateOrder); err != nil || versioned || changed {
		t.Fatalf("remove of a type without a class = %v, %v, %v", versioned, changed, err)
	}
}

func TestDiscardTakesBackOnlyWhatEnsureMade(t *testing.T) {
	classes, definitions, datasets := newTestClasses()
	definitions.subject("kn1", "main", "order", "order_id")
	class, err := classes.ensure(userContext(), lateOrder)
	if err != nil || !class.madeType || !class.madeDataset {
		t.Fatalf("ensure = %+v, %v", class, err)
	}
	classes.discard(userContext(), lateOrder, class)
	if definitions.objects["kn1/main/order_late"] != nil || definitions.relations["kn1/main/order_late_subject"] != nil ||
		datasets.resources[class.datasetID] != nil {
		t.Fatalf("left %v", definitions.writes)
	}
	if definitions.objects["kn1/main/order"] == nil {
		t.Fatal("the subject was removed")
	}

	// A class and a dataset that were there are kept.
	first, _ := classes.ensure(userContext(), lateOrder)
	again, _ := classes.ensure(userContext(), lateOrder)
	if again.madeType || again.madeDataset || !first.madeType {
		t.Fatalf("ensured again %+v", again)
	}
	classes.discard(userContext(), lateOrder, again)
	if definitions.objects["kn1/main/order_late"] == nil || datasets.resources[first.datasetID] == nil {
		t.Fatal("discard took what it found")
	}

	// What failed half way is taken back as far as it got.
	delete(definitions.objects, "kn1/main/order_late")
	datasets.drop(first.datasetID)
	definitions.err = errors.New("definitions are down")
	failed, err := classes.ensure(userContext(), lateOrder)
	if err == nil || !failed.madeDataset || datasets.resources[failed.datasetID] == nil {
		t.Fatalf("ensure = %+v, %v", failed, err)
	}
	classes.discard(userContext(), lateOrder, failed)
	if datasets.resources[failed.datasetID] != nil {
		t.Fatal("the dataset of a class that failed is left")
	}
}

func TestRemoveKeepsAClassThatOtherDefinitionsUse(t *testing.T) {
	classes, definitions, _ := newTestClasses()
	definitions.subject("kn1", "main", "order", "order_id")
	class, _ := classes.ensure(userContext(), lateOrder)
	definitions.users["kn1/main/order_late"] = []string{"催办"}
	definitions.relations["kn1/main/escalated_to"] = &interfaces.RelationType{
		RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{RTID: "escalated_to", RTName: "升级给", SourceObjectTypeID: "order_late", TargetObjectTypeID: "person"},
		KNID:                     "kn1", Branch: "main",
	}
	definitions.writes = nil

	_, changed, err := classes.remove(userContext(), stored(lateOrder, class))
	var refused *httperrors.HTTPError
	// Its own code: the type may have no events, so "has events" would mislead.
	if !errors.As(err, &refused) || refused.HTTPCode != http.StatusConflict || refused.ErrorCode != berrors.BknBackend_KnEvent_EventClassInUse || changed {
		t.Fatalf("a class in use: %v, %v", changed, err)
	}
	details, _ := refused.ErrorDetails.(map[string]any)
	if details["event_class"] != "order_late" || !reflect.DeepEqual(details["used_by"], []string{"催办", "升级给"}) {
		t.Fatalf("details of a class in use = %#v", refused.ErrorDetails)
	}
	if text := refused.Error(); !strings.Contains(text, "被其他定义引用") {
		t.Fatalf("a class in use is described as %s", text)
	}
	if len(definitions.writes) != 0 {
		t.Fatalf("removed %v of a class in use", definitions.writes)
	}
}

func TestRemoveLeavesAnObjectTypeThatIsNoEventClass(t *testing.T) {
	classes, definitions, _ := newTestClasses()
	class, _ := classes.ensure(userContext(), lateOrder)
	// Someone replaced the class with an object type of their own.
	definitions.subject("kn1", "main", "order_late", "id")
	definitions.writes = nil
	if _, changed, err := classes.remove(userContext(), stored(lateOrder, class)); err != nil || changed || len(definitions.writes) != 0 {
		t.Fatalf("removed %v: %v", definitions.writes, err)
	}
}

func TestDropKeepsTheDatasetOfAVersionedNetwork(t *testing.T) {
	classes, definitions, datasets := newTestClasses()
	definitions.versioned["kn1"] = true
	class, _ := classes.ensure(userContext(), lateOrder)
	versioned, _, err := classes.remove(userContext(), stored(lateOrder, class))
	if err != nil || !versioned || definitions.objects["kn1/draft/order_late"] != nil {
		t.Fatalf("remove = %v, %v", versioned, err)
	}
	// The published version still reads the class.
	if err := classes.drop(userContext(), stored(lateOrder, class), versioned); err != nil || datasets.resources[class.datasetID] == nil {
		t.Fatalf("drop: %v", err)
	}
}

func TestClassIDs(t *testing.T) {
	if got := classObjectTypeID("a.b.c_d-e"); got != "a_b_c_d-e" {
		t.Fatalf("object type ID %s", got)
	}
	first, same, otherType, otherNetwork := classDatasetID("kn1", "t"), classDatasetID("kn1", "t"), classDatasetID("kn1", "u"), classDatasetID("kn2", "t")
	if first != same || first == otherType || first == otherNetwork || len(first) != len(datasetPrefix)+24 {
		t.Fatalf("dataset IDs %s %s %s %s", first, same, otherType, otherNetwork)
	}
	// The parts are told apart: "a"+"bc" is not "ab"+"c".
	if classDatasetID("a", "bc") == classDatasetID("ab", "c") {
		t.Fatal("dataset IDs of different networks and codes agree")
	}
	if isEventClass(nil) || isEventClass(&interfaces.ObjectType{}) ||
		!isEventClass(&interfaces.ObjectType{ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{DataSource: &interfaces.ResourceInfo{ID: first}}}) {
		t.Fatal("an event class is known by its dataset")
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
