// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knevent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces/data_type"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/vega_backend"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
)

// Every kind of event is an object type of its network, the event class, so
// events are queried, related and authorized like the network's other
// objects. The class is bound to a dataset that holds a copy of the ledger's
// events of the type, and has a relation to the object type the events are
// about.
//
// The module makes the class with the event type and owns it from then on.
// The dataset and the definitions are written as the platform's built-in
// administrator: the caller needs the permission to modify the network, which
// the service checks, and none on a dataset that the platform keeps.

const (
	// eventCatalogID is the catalog of the event datasets. It is a plain
	// catalog: the datasets of a built-in one cannot be read through a network.
	eventCatalogID   = "bkn_kn_events"
	eventCatalogName = "Knowledge network events"

	// datasetPrefix starts the ID of every event dataset, which is how an
	// object type is known to be an event class this module made.
	datasetPrefix = "kn_event_"
	// datasetLayout numbers the fields of the event datasets. A dataset's
	// fields cannot change once it holds data, so changing classFields takes a
	// new number, which gives every event type a new dataset.
	datasetLayout = "1"

	classTag = "event"
	// relationSuffix follows the class ID in the ID of its relation to the
	// object type the events are about.
	relationSuffix = "_subject"

	fieldEventID   = "event_id"
	fieldEventType = "event_type"
	fieldTitle     = "title"
	fieldObjectKey = "object_key"

	// keywordLimit is the longest text indexed for equality, as Vega's default.
	keywordLimit = 256
	// datasetWriters is how many documents are written to a dataset at a time.
	datasetWriters = 4
)

// classField is one property of every event class and the dataset field
// behind it. value is what an event has for it; nil leaves the field out, so
// a time that has not come is not written as the epoch.
type classField struct {
	name     string
	kind     string
	semantic string
	value    func(*interfaces.KnEvent) any
}

// classLanguage is the language of what an event class shows. As with the
// audit ledger, the definitions keep one business language whoever writes
// them, so a class reads the same after anyone changes its type.
const classLanguage = i18n.SimplifiedChinese

// classText is a text the event classes show, or fallback if it is missing.
func classText(id string, data map[string]any, fallback string) string {
	rendered, err := i18n.Render(classLanguage, "BknBackend.KnEvent.Class."+id, data)
	if err != nil {
		logger.Errorf("knowledge-network event: render class text %s: %v", id, err)
		return fallback
	}
	return rendered
}

// label is what people read for the property.
func (f classField) label() string { return classText("Field."+f.name, nil, f.name) }

func text(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func moment(millis int64) any {
	if millis <= 0 {
		return nil
	}
	return time.UnixMilli(millis).UTC().Format("2006-01-02T15:04:05.000Z07:00")
}

// classFields are the standard properties of an event class. What a kind of
// event has of its own stays in detail. What only a rule check's refresh
// changes is left out: the time of the last check, its status and the rule
// version it ran. Every check refreshes every open event of its type, and
// copying that would rewrite them all each time; the event API has them.
var classFields = []classField{
	{fieldEventID, data_type.DATATYPE_STRING, "", func(e *interfaces.KnEvent) any { return e.ID }},
	{fieldEventType, data_type.DATATYPE_STRING, "", func(e *interfaces.KnEvent) any { return e.EventType }},
	{fieldTitle, data_type.DATATYPE_STRING, "", func(e *interfaces.KnEvent) any { return e.Title }},
	{"status", data_type.DATATYPE_STRING, "status", func(e *interfaces.KnEvent) any { return text(e.Status) }},
	{"severity", data_type.DATATYPE_STRING, "severity", func(e *interfaces.KnEvent) any { return text(e.Severity) }},
	{"source", data_type.DATATYPE_STRING, "", func(e *interfaces.KnEvent) any { return text(e.Source) }},
	{fieldObjectKey, data_type.DATATYPE_STRING, "", func(e *interfaces.KnEvent) any { return text(e.ObjectKey) }},
	{"object_name", data_type.DATATYPE_STRING, "", func(e *interfaces.KnEvent) any { return text(e.ObjectName) }},
	{"occurred_at", data_type.DATATYPE_DATETIME, "event_time", func(e *interfaces.KnEvent) any { return moment(e.OccurredAt) }},
	{"ended_at", data_type.DATATYPE_DATETIME, "end_time", func(e *interfaces.KnEvent) any { return moment(e.EndedAt) }},
	{"recorded_at", data_type.DATATYPE_DATETIME, "", func(e *interfaces.KnEvent) any { return moment(e.RecordedAt) }},
	{"first_detected_at", data_type.DATATYPE_DATETIME, "", func(e *interfaces.KnEvent) any { return moment(e.FirstDetectedAt) }},
	{"backlog", data_type.DATATYPE_BOOLEAN, "", func(e *interfaces.KnEvent) any { return e.Backlog }},
	{"reported_by", data_type.DATATYPE_STRING, "", func(e *interfaces.KnEvent) any { return text(e.ReportedBy) }},
	{"reported_by_name", data_type.DATATYPE_STRING, "", func(e *interfaces.KnEvent) any { return text(e.ReportedByName) }},
	{"verified_by", data_type.DATATYPE_STRING, "", func(e *interfaces.KnEvent) any { return text(e.VerifiedBy) }},
	{"verified_by_name", data_type.DATATYPE_STRING, "", func(e *interfaces.KnEvent) any { return text(e.VerifiedByName) }},
	{"verified_at", data_type.DATATYPE_DATETIME, "", func(e *interfaces.KnEvent) any { return moment(e.VerifiedAt) }},
	{"merged_into", data_type.DATATYPE_STRING, "", func(e *interfaces.KnEvent) any { return text(e.MergedInto) }},
	{"detail", data_type.DATATYPE_JSON, "", detailOf},
}

// detailOf is an event's detail as the JSON it is, left out when empty.
func detailOf(event *interfaces.KnEvent) any {
	var detail any
	if len(event.Detail) == 0 || json.Unmarshal(event.Detail, &detail) != nil {
		return nil
	}
	if object, ok := detail.(map[string]any); ok && len(object) == 0 {
		return nil
	}
	return detail
}

// eventDocument is an event as its class's dataset keeps it.
func eventDocument(event *interfaces.KnEvent) map[string]any {
	document := make(map[string]any, len(classFields))
	for _, field := range classFields {
		if value := field.value(event); value != nil {
			document[field.name] = value
		}
	}
	return document
}

// classObjectTypeID is the ID of an event type's class: its code, which names
// the kind of event everywhere else. An object type ID takes no dot, so two
// codes can stand for one class; claim keeps it to one of them.
func classObjectTypeID(code string) string {
	return strings.ReplaceAll(code, ".", "_")
}

// claim refuses the class of an event type when another type of the network
// stands for the same class ID, as stock.low and stock_low do. A new type is
// refused for any such type, which has the class or is to be given it. A type
// that is only now given its class is refused for one that has it, so the
// first of two earlier types to be given the class keeps it. It is called
// holding the network's class lock.
func claim(ctx context.Context, store *store, knID, code string, newType bool) error {
	classes, err := store.classesOf(ctx, knID)
	if err != nil {
		return err
	}
	id := classObjectTypeID(code)
	others := []string{}
	for other, has := range classes {
		if other != code && classObjectTypeID(other) == id && (newType || has != "") {
			others = append(others, other)
		}
	}
	if len(others) == 0 {
		return nil
	}
	sort.Strings(others)
	return httperrors.NewHTTPError(ctx, http.StatusConflict, berrors.BknBackend_KnEvent_EventTypeExisted).
		WithErrorDetails("the event class " + id + " is the class of the event type " + strings.Join(others, ", "))
}

// classDatasetID is the ID of the dataset of an event type.
func classDatasetID(knID, code string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{knID, code, datasetLayout}, "\x00")))
	return datasetPrefix + hex.EncodeToString(sum[:])[:24]
}

// isEventClass reports whether an object type is a class this module made.
func isEventClass(objectType *interfaces.ObjectType) bool {
	return objectType != nil && objectType.DataSource != nil &&
		strings.HasPrefix(objectType.DataSource.ID, datasetPrefix)
}

// definitions writes the object type and the relation type of an event
// class, as the definition API does.
type definitions interface {
	// branch is the branch a network's definitions are written to: its draft
	// when the network is versioned, where a class waits to be published like
	// any other change to the model.
	branch(ctx context.Context, knID string) (string, error)
	// objectTypes reads object types by ID; one that does not exist is left out.
	objectTypes(ctx context.Context, knID, branch string, ids []string) (map[string]*interfaces.ObjectType, error)
	saveObjectType(ctx context.Context, objectType *interfaces.ObjectType) error
	saveRelationType(ctx context.Context, relationType *interfaces.RelationType) error
	// removeRelationType deletes a relation type if it exists.
	removeRelationType(ctx context.Context, knID, branch, id string) error
	// usersOf names the relation types and the action types that use an
	// object type, leaving out the relation type except.
	usersOf(ctx context.Context, knID, branch, id, except string) ([]string, error)
	removeObjectType(ctx context.Context, knID, branch, id string) error
}

// eventClass is where the events of a type are read. leftover says the
// dataset was there before the class was asked for.
type eventClass struct {
	objectTypeID string
	datasetID    string
	leftover     bool
	// madeType and madeDataset say ensure made the object type and the dataset
	// rather than finding them, so that taking the class back deletes them.
	madeType, madeDataset bool
}

// classes makes the event classes and their datasets.
type classes struct {
	definitions definitions
	datasets    interfaces.VegaBackendService
}

// asPlatform is ctx acting as the platform rather than as the caller.
func asPlatform(ctx context.Context) context.Context {
	return context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY,
		interfaces.AccountInfo{ID: interfaces.ADMIN_ACCOUNT_ID, Type: interfaces.ADMIN_ACCOUNT_TYPE})
}

// versioned reports whether a network's classes wait in its draft.
func (c *classes) versioned(ctx context.Context, knID string) (bool, error) {
	branch, err := c.definitions.branch(asPlatform(ctx), knID)
	return branch == interfaces.DRAFT_BRANCH, err
}

// ensure makes or completes the class of an event type. Every step keeps
// what is already as it should be, so a call that failed half way is made
// again.
func (c *classes) ensure(ctx context.Context, eventType interfaces.KnEventType) (eventClass, error) {
	ctx = asPlatform(ctx)
	class := eventClass{
		objectTypeID: classObjectTypeID(eventType.Code),
		datasetID:    classDatasetID(eventType.KNID, eventType.Code),
	}
	branch, err := c.definitions.branch(ctx, eventType.KNID)
	if err != nil {
		return class, err
	}
	ids := []string{class.objectTypeID}
	if eventType.Subject != "" {
		ids = append(ids, eventType.Subject)
	}
	found, err := c.definitions.objectTypes(ctx, eventType.KNID, branch, ids)
	if err != nil {
		return class, err
	}
	if taken := found[class.objectTypeID]; taken != nil && !isEventClass(taken) {
		return class, httperrors.NewHTTPError(ctx, http.StatusConflict, berrors.BknBackend_KnEvent_EventTypeExisted).
			WithErrorDetails("the knowledge network has an object type with the ID " + class.objectTypeID)
	}
	if class.leftover, err = c.ensureDataset(ctx, eventType, class.datasetID); err != nil {
		return class, err
	}
	class.madeDataset = !class.leftover
	class.madeType = found[class.objectTypeID] == nil
	if err := c.definitions.saveObjectType(ctx, classObjectType(eventType, class, branch)); err != nil {
		// The platform refuses a second object type of one name as forbidden.
		// To whoever names the event type, it is a name that is taken.
		var refused *httperrors.HTTPError
		if errors.As(err, &refused) && refused.ErrorCode == berrors.BknBackend_ObjectType_ObjectTypeNameExisted {
			return class, httperrors.NewHTTPError(ctx, http.StatusConflict, berrors.BknBackend_KnEvent_EventTypeExisted).
				WithErrorDetails("the knowledge network has an object type named " + eventType.Name)
		}
		return class, err
	}
	relationID := class.objectTypeID + relationSuffix
	if key := subjectKey(found[eventType.Subject]); key != "" {
		return class, c.definitions.saveRelationType(ctx, classRelationType(eventType, class, branch, relationID, key))
	}
	if eventType.Subject != "" {
		// Events of the type still name their objects; only the path from an
		// event to its object is missing from the network.
		logger.Infof("knowledge-network event: class %s of network %s has no relation: subject %q is missing or has no single primary key",
			class.objectTypeID, eventType.KNID, eventType.Subject)
	}
	return class, c.definitions.removeRelationType(ctx, eventType.KNID, branch, relationID)
}

// subjectKey is the primary key an event's object key stands for, or "" when
// the subject does not exist or has not exactly one: an event names its object
// by a single key.
func subjectKey(subject *interfaces.ObjectType) string {
	if subject == nil || len(subject.PrimaryKeys) != 1 {
		return ""
	}
	return subject.PrimaryKeys[0]
}

// ensureDataset makes the catalog and the dataset that are missing, and
// reports whether the dataset was there already.
func (c *classes) ensureDataset(ctx context.Context, eventType interfaces.KnEventType, datasetID string) (existed bool, err error) {
	catalog, err := c.datasets.GetCatalogByID(ctx, eventCatalogID)
	if err != nil {
		return false, err
	}
	if catalog == nil {
		if _, err := c.datasets.CreateCatalog(ctx, &interfaces.CatalogRequest{
			ID: eventCatalogID, Name: eventCatalogName, Tags: []string{classTag}, Enabled: true,
			Description: "Copies of the knowledge networks' events, one dataset for each kind of event. Written by bkn-backend only.",
		}); err != nil {
			return false, err
		}
	}
	dataset, err := c.datasets.GetResourceByID(ctx, datasetID)
	if err != nil || dataset != nil {
		return dataset != nil, err
	}
	return false, c.datasets.CreateResource(ctx, classDataset(eventType, datasetID))
}

// clear empties the dataset of an event type that is only now given it. What
// it holds then was left by a network of the same ID deleted earlier: the
// datasets outlive their networks, and their IDs follow from the network's.
// It is called where no event of the type can have been written yet.
func (c *classes) clear(ctx context.Context, eventType interfaces.KnEventType, class eventClass) error {
	if !class.leftover {
		return nil
	}
	return c.datasets.DeleteDatasetDocumentsByQuery(asPlatform(ctx), class.datasetID, map[string]any{
		"operation": "and",
		"sub_conditions": []map[string]any{
			{"field": fieldEventType, "operation": "==", "value": eventType.Code, "value_from": "const"},
		},
	})
}

// remove deletes the class of an event type that is being deleted, with its
// relation to the subject. A class that other definitions use is kept, and so
// is the type: they would be left pointing at nothing. A class that is gone,
// or that this module did not make, is left alone. versioned says the network
// is versioned, where the removal waits in the draft. changed says the
// definitions may have been changed, even when err says the removal did not
// finish.
func (c *classes) remove(ctx context.Context, eventType interfaces.KnEventType) (versioned, changed bool, err error) {
	id := eventType.ObjectTypeID
	if id == "" {
		return false, false, nil
	}
	ctx = asPlatform(ctx)
	branch, err := c.definitions.branch(ctx, eventType.KNID)
	if err != nil {
		return false, false, err
	}
	versioned = branch == interfaces.DRAFT_BRANCH
	found, err := c.definitions.objectTypes(ctx, eventType.KNID, branch, []string{id})
	if err != nil || !isEventClass(found[id]) {
		return versioned, false, err
	}
	relationID := id + relationSuffix
	users, err := c.definitions.usersOf(ctx, eventType.KNID, branch, id, relationID)
	if err != nil {
		return versioned, false, err
	}
	if len(users) > 0 {
		return versioned, false, httperrors.NewHTTPError(ctx, http.StatusConflict, berrors.BknBackend_KnEvent_EventClassInUse).
			WithErrorDetails(map[string]any{"event_class": id, "used_by": users})
	}
	if err := c.definitions.removeRelationType(ctx, eventType.KNID, branch, relationID); err != nil {
		return versioned, true, err
	}
	return versioned, true, c.definitions.removeObjectType(ctx, eventType.KNID, branch, id)
}

// discard takes back what ensure made for an event type that was then not
// stored, so that no class is left that no type stands for. What ensure found
// is left as it is. It is called holding the network's class lock, and does
// what it can: a class it leaves is taken over by the next type of the code.
func (c *classes) discard(ctx context.Context, eventType interfaces.KnEventType, class eventClass) {
	ctx = context.WithoutCancel(ctx)
	if class.madeType {
		eventType.ObjectTypeID = class.objectTypeID
		if _, _, err := c.remove(ctx, eventType); err != nil {
			logger.Warnf("knowledge-network event: take back class %s of network %s, made for event type %s that was not stored: %v",
				class.objectTypeID, eventType.KNID, eventType.Code, err)
		}
	}
	if class.madeDataset {
		if err := c.datasets.DeleteResource(asPlatform(ctx), class.datasetID); err != nil {
			logger.Warnf("knowledge-network event: delete dataset %s, made for event type %s of network %s that was not stored: %v",
				class.datasetID, eventType.Code, eventType.KNID, err)
		}
	}
}

// drop deletes the dataset of a deleted event type, which holds nothing: a
// type with events cannot be deleted. A versioned network keeps it, as the
// published version reads the class until a version without it is published;
// the dataset is cleared if the type is made again.
func (c *classes) drop(ctx context.Context, eventType interfaces.KnEventType, versioned bool) error {
	if eventType.DatasetID == "" || versioned {
		return nil
	}
	ctx = asPlatform(ctx)
	dataset, err := c.datasets.GetResourceByID(ctx, eventType.DatasetID)
	if err != nil || dataset == nil {
		return err
	}
	return c.datasets.DeleteResource(ctx, eventType.DatasetID)
}

// write puts events into the dataset of their type, each as it is now.
func (c *classes) write(ctx context.Context, datasetID string, events []*interfaces.KnEvent) error {
	documents := make([]vega_backend.DatasetDocument, 0, len(events))
	for _, event := range events {
		documents = append(documents, vega_backend.DatasetDocument{ID: event.ID, Document: eventDocument(event)})
	}
	ctx = vega_backend.WithDatasetWriteConcurrency(asPlatform(ctx), datasetWriters)
	return vega_backend.WriteDatasetDocuments(ctx, c.datasets, datasetID, documents)
}

// restore makes the dataset of an event type again if it is gone, and
// reports whether it did; its events then have to be written once more.
func (c *classes) restore(ctx context.Context, eventType interfaces.KnEventType) (bool, error) {
	ctx = asPlatform(ctx)
	dataset, err := c.datasets.GetResourceByID(ctx, eventType.DatasetID)
	if err != nil || dataset != nil {
		return false, err
	}
	return true, c.datasets.CreateResource(ctx, classDataset(eventType, eventType.DatasetID))
}

func classDataset(eventType interfaces.KnEventType, datasetID string) *interfaces.VegaResource {
	schema := make([]*interfaces.Property, 0, len(classFields))
	for _, field := range classFields {
		// A dataset's display names differ from each other and are not what
		// people read, so the names stand in; the labels are on the class.
		property := &interfaces.Property{Name: field.name, DisplayName: field.name, Type: field.kind, Description: field.label()}
		if field.kind == data_type.DATATYPE_STRING {
			property.Features = []interfaces.PropertyFeature{{
				FeatureName: "keyword", FeatureType: interfaces.FieldFeatureType_Keyword, IsDefault: true,
				Config: map[string]any{interfaces.FIELD_KEYWORD_PROPERTY_IGNORE_ABOVE: keywordLimit},
			}}
		}
		schema = append(schema, property)
	}
	return &interfaces.VegaResource{
		ID: datasetID, CatalogID: eventCatalogID, Name: datasetID, Category: "dataset", Tags: []string{classTag},
		Description:      "Events of type " + eventType.Code + " of knowledge network " + eventType.KNID,
		SchemaDefinition: schema,
		IndexConfig:      &interfaces.VegaResourceIndexConfig{},
	}
}

func classObjectType(eventType interfaces.KnEventType, class eventClass, branch string) *interfaces.ObjectType {
	properties := make([]*interfaces.DataProperty, 0, len(classFields))
	for _, field := range classFields {
		properties = append(properties, &interfaces.DataProperty{
			Name: field.name, DisplayName: field.label(), Type: field.kind, Semantic: field.semantic,
			MappedField: &interfaces.Field{Name: field.name},
		})
	}
	return &interfaces.ObjectType{
		ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{
			OTID: class.objectTypeID, OTName: eventType.Name,
			DataSource:     &interfaces.ResourceInfo{Type: interfaces.DATA_SOURCE_TYPE_RESOURCE, ID: class.datasetID},
			DataProperties: properties,
			PrimaryKeys:    []string{fieldEventID},
			DisplayKey:     fieldTitle,
		},
		CommonInfo: interfaces.CommonInfo{
			Tags:    []string{classTag},
			Comment: classText("Comment", map[string]any{"name": eventType.Name}, ""),
		},
		KNID: eventType.KNID, Branch: branch,
	}
}

func classRelationType(eventType interfaces.KnEventType, class eventClass, branch, id, subjectKey string) *interfaces.RelationType {
	return &interfaces.RelationType{
		RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{
			RTID: id, RTName: classText("Relation", nil, "subject"),
			SourceObjectTypeID: class.objectTypeID, TargetObjectTypeID: eventType.Subject,
			Type: interfaces.RELATION_TYPE_DIRECT,
			MappingRules: []interfaces.Mapping{{
				SourceProp: interfaces.SimpleProperty{Name: fieldObjectKey},
				TargetProp: interfaces.SimpleProperty{Name: subjectKey},
			}},
		},
		CommonInfo: interfaces.CommonInfo{
			Tags:    []string{classTag},
			Comment: classText("RelationComment", map[string]any{"name": eventType.Name}, ""),
		},
		KNID: eventType.KNID, Branch: branch,
	}
}
