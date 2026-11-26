// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knevent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces/data_type"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/vega_backend"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
)

// An event class is an object type of its network whose kinds include event, so events are
// queried, related and authorized like the network's other objects. The platform keeps what makes
// it an event class: its data source, a dataset that holds a copy of the ledger's events of the
// class, the properties and keys read from it, and a relation to the object type the events are
// about. The dataset is written as the platform's built-in administrator: the caller needs the
// permission to modify the network, and none on a dataset that the platform keeps.

const (
	// eventCatalogID is the catalog of the event datasets. It is a plain
	// catalog: the datasets of a built-in one cannot be read through a network.
	eventCatalogID   = "bkn_kn_events"
	eventCatalogName = "Knowledge network events"

	// datasetPrefix starts the ID of every event dataset.
	datasetPrefix = "kn_event_"
	// datasetLayout numbers the fields of the event datasets. A dataset's
	// fields cannot change once it holds data, so changing classFields takes a
	// new number, which gives every event class a new dataset.
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

// classLanguage is the language of what the platform writes into an event
// class. As with the audit ledger, the definitions keep one business language
// whoever writes them, so a class reads the same after anyone changes it.
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
// version it ran. Every check refreshes every open event of its class, and
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

// classFieldNamed is the standard property of that name, nil when there is none.
func classFieldNamed(name string) *classField {
	for index := range classFields {
		if classFields[index].name == name {
			return &classFields[index]
		}
	}
	return nil
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

// classDatasetID is the ID of the dataset of an event class. It follows from the network and the
// class, so the platform never has to store it, and a class made again finds its dataset.
func classDatasetID(knID, classID string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{knID, classID, datasetLayout}, "\x00")))
	return datasetPrefix + hex.EncodeToString(sum[:])[:24]
}

// asPlatform is ctx acting as the platform rather than as the caller.
func asPlatform(ctx context.Context) context.Context {
	return context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY,
		interfaces.AccountInfo{ID: interfaces.ADMIN_ACCOUNT_ID, Type: interfaces.ADMIN_ACCOUNT_TYPE})
}

// classes keeps the datasets of the event classes.
type classes struct {
	datasets interfaces.VegaBackendService
}

// errForeignDataset says the ID of a class's dataset is taken by a dataset of another catalog.
type errForeignDataset struct{ datasetID, catalogID string }

func (e errForeignDataset) Error() string {
	return "dataset " + e.datasetID + " belongs to catalog " + e.catalogID + ", not to the event catalog"
}

// ensure makes the catalog and the dataset of a class that are missing, and reports whether it
// made the dataset. A dataset of that ID outside the event catalog is refused: the platform
// writes only what it keeps.
func (c *classes) ensure(ctx context.Context, knID, classID string) (made bool, err error) {
	ctx = asPlatform(ctx)
	catalog, err := c.datasets.GetCatalogByID(ctx, eventCatalogID)
	if err != nil {
		return false, err
	}
	if catalog == nil {
		if _, err := c.datasets.CreateCatalog(ctx, &interfaces.CatalogRequest{
			ID: eventCatalogID, Name: eventCatalogName, Tags: []string{classTag}, Enabled: true,
			Description: "Copies of the knowledge networks' events, one dataset for each event class. Written by bkn-backend only.",
		}); err != nil {
			return false, err
		}
	}
	datasetID := classDatasetID(knID, classID)
	dataset, err := c.datasets.GetResourceByID(ctx, datasetID)
	if err != nil {
		return false, err
	}
	if dataset != nil {
		if dataset.CatalogID != eventCatalogID {
			return false, errForeignDataset{datasetID: datasetID, catalogID: dataset.CatalogID}
		}
		return false, nil
	}
	return true, c.datasets.CreateResource(ctx, classDataset(knID, classID, datasetID))
}

// clear empties the dataset of a class that is only now made. What it holds then was left by a
// class of the same ID deleted earlier, or by a network of the same ID: the datasets outlive
// both. It is called where no event of the class exists.
func (c *classes) clear(ctx context.Context, knID, classID string) error {
	return c.datasets.DeleteDatasetDocumentsByQuery(asPlatform(ctx), classDatasetID(knID, classID), map[string]any{
		"operation": "and",
		"sub_conditions": []map[string]any{
			{"field": fieldEventType, "operation": "==", "value": classID, "value_from": "const"},
		},
	})
}

// write puts events into the dataset of their class, each as it is now.
func (c *classes) write(ctx context.Context, datasetID string, events []*interfaces.KnEvent) error {
	documents := make([]vega_backend.DatasetDocument, 0, len(events))
	for _, event := range events {
		documents = append(documents, vega_backend.DatasetDocument{ID: event.ID, Document: eventDocument(event)})
	}
	ctx = vega_backend.WithDatasetWriteConcurrency(asPlatform(ctx), datasetWriters)
	return vega_backend.WriteDatasetDocuments(ctx, c.datasets, datasetID, documents)
}

// restore makes the dataset of a class again if it is gone, and reports whether it did; its
// events then have to be written once more.
func (c *classes) restore(ctx context.Context, knID, classID string) (bool, error) {
	ctx = asPlatform(ctx)
	datasetID := classDatasetID(knID, classID)
	dataset, err := c.datasets.GetResourceByID(ctx, datasetID)
	if err != nil || dataset != nil {
		return false, err
	}
	return true, c.datasets.CreateResource(ctx, classDataset(knID, classID, datasetID))
}

func classDataset(knID, classID, datasetID string) *interfaces.VegaResource {
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
		Description:      "Events of class " + classID + " of knowledge network " + knID,
		SchemaDefinition: schema,
		IndexConfig:      &interfaces.VegaResourceIndexConfig{},
	}
}

// classProperty is the standard property field as an event class has it.
func classProperty(field classField) *interfaces.DataProperty {
	return &interfaces.DataProperty{
		Name: field.name, DisplayName: field.label(), Type: field.kind, Semantic: field.semantic,
		MappedField: &interfaces.Field{Name: field.name},
	}
}

// classRelationType is the relation from an event class to the object type its events are about,
// whose single primary key an event's object key stands for. Without the key it is the relation
// as far as the class alone tells it, which names its ends but maps nothing.
func classRelationType(class *interfaces.ObjectType, subjectKey string) *interfaces.RelationType {
	relationType := &interfaces.RelationType{
		RelationTypeWithKeyField: interfaces.RelationTypeWithKeyField{
			RTID: class.OTID + relationSuffix, RTName: classText("Relation", nil, "subject"),
			SourceObjectTypeID: class.OTID, TargetObjectTypeID: class.Event.Subject,
			Type: interfaces.RELATION_TYPE_DIRECT,
		},
		CommonInfo: interfaces.CommonInfo{
			Tags:    []string{classTag},
			Comment: classText("RelationComment", map[string]any{"name": class.OTName}, ""),
		},
		KNID: class.KNID, Branch: class.Branch,
	}
	if subjectKey != "" {
		relationType.MappingRules = []interfaces.Mapping{{
			SourceProp: interfaces.SimpleProperty{Name: fieldObjectKey},
			TargetProp: interfaces.SimpleProperty{Name: subjectKey},
		}}
	}
	return relationType
}
