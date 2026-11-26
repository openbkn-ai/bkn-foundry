// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knevent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strings"

	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	corelogics "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/permission"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
)

// eventKind makes an object type an event class. It is the event module's part in a save of the
// object type: it checks the class's event configuration, fills the definitions the platform
// keeps, and keeps the dataset, the relation to the subject and the class's row of rule checks
// beside it.
type eventKind struct {
	store   *store
	classes *classes
	// relations writes the relation from a class to its subject as the relation type API does.
	relations interfaces.RelationTypeService
	// objects reads the subject of a class in the transaction that saves it.
	objects interfaces.ObjectTypeAccess
	// declarations checks the network's declarations with the relation the class keeps; nil
	// checks nothing.
	declarations *corelogics.NetworkRelations
	// available reports whether the license allows event classes to be created or changed.
	available func() bool
}

var _ interfaces.ObjectTypeKind = (*eventKind)(nil)

func invalidClass(ctx context.Context, format string, args ...any) error {
	return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ObjectType_InvalidParameter).
		WithErrorDetails(fmt.Sprintf(format, args...))
}

func (k *eventKind) Available() bool { return k.available() }

// Complete checks the event configuration of a class and fills what the platform keeps. The
// configuration may be left out of an update, which keeps the stored one; a new class needs it.
// What the caller wrote of the kept definitions must be what the platform keeps: their display
// names and comments are the caller's to change, nothing else is. A class whose ID is not given
// yet is given its data source once it has one.
func (k *eventKind) Complete(ctx context.Context, objectType, stored *interfaces.ObjectType) error {
	var before *interfaces.ObjectTypeEvent
	if stored != nil {
		before = stored.Event
	}
	if objectType.Event == nil {
		if before == nil {
			return invalidClass(ctx, "event class %s needs its event configuration", objectType.OTID)
		}
		kept := *before
		objectType.Event = &kept
	}
	if err := checkEventConfig(ctx, objectType, before); err != nil {
		return err
	}
	if err := completeDataSource(ctx, objectType); err != nil {
		return err
	}
	if err := completeProperties(ctx, objectType); err != nil {
		return err
	}
	return completeKeys(ctx, objectType)
}

func checkEventConfig(ctx context.Context, objectType *interfaces.ObjectType, before *interfaces.ObjectTypeEvent) error {
	event := objectType.Event
	event.Source = strings.TrimSpace(event.Source)
	event.Subject = strings.TrimSpace(event.Subject)
	event.Severity = strings.TrimSpace(event.Severity)
	event.HandlingNote = strings.TrimSpace(event.HandlingNote)
	switch {
	case len(objectType.OTID) > interfaces.KnEventClassIDMax:
		return invalidClass(ctx, "the ID of event class %s exceeds %d characters", objectType.OTID, interfaces.KnEventClassIDMax)
	case event.Source != interfaces.KnEventSourceRule && event.Source != interfaces.KnEventSourceReport:
		return invalidClass(ctx, "event.source must be rule or report")
	case before != nil && before.Source != "" && event.Source != before.Source:
		return invalidClass(ctx, "the source of event class %s is fixed once it exists: %s, not %s",
			objectType.OTID, before.Source, event.Source)
	case event.Subject == "":
		return invalidClass(ctx, "event.subject is required: the object type the events are about")
	case objectType.OTID != "" && event.Subject == objectType.OTID:
		return invalidClass(ctx, "event class %s cannot be its own subject", objectType.OTID)
	case longerThan(event.Severity, interfaces.KnEventSeverityMax):
		return invalidClass(ctx, "event.severity exceeds %d characters", interfaces.KnEventSeverityMax)
	case event.MergeHours < 0 || math.IsNaN(event.MergeHours) || math.IsInf(event.MergeHours, 0):
		return invalidClass(ctx, "event.merge_hours must be a non-negative number")
	case longerThan(event.HandlingNote, interfaces.KnEventHandlingNoteMax):
		return invalidClass(ctx, "event.handling_note exceeds %d characters", interfaces.KnEventHandlingNoteMax)
	}
	return nil
}

// completeDataSource binds a class to its dataset. A class without an ID yet is left unbound,
// unless the caller bound it, which is refused: its dataset follows from the ID.
func completeDataSource(ctx context.Context, objectType *interfaces.ObjectType) error {
	given := objectType.DataSource
	bound := given != nil && (given.Type != "" || given.ID != "")
	if objectType.OTID == "" {
		if bound {
			return invalidClass(ctx, "the platform keeps the data source of an event class")
		}
		return nil
	}
	datasetID := classDatasetID(objectType.KNID, objectType.OTID)
	if bound && (given.Type != interfaces.DATA_SOURCE_TYPE_RESOURCE || given.ID != datasetID) {
		return invalidClass(ctx, "the platform keeps the data source of event class %s: resource %s",
			objectType.OTID, datasetID)
	}
	objectType.DataSource = &interfaces.ResourceInfo{Type: interfaces.DATA_SOURCE_TYPE_RESOURCE, ID: datasetID}
	return nil
}

// completeProperties gives a class the standard properties, in their order. A property the caller
// wrote is one of them, of its type and mapped to its field; its display name and comment stay.
func completeProperties(ctx context.Context, objectType *interfaces.ObjectType) error {
	if len(objectType.LogicProperties) > 0 {
		return invalidClass(ctx, "an event class has no logic properties")
	}
	given := make(map[string]*interfaces.DataProperty, len(objectType.DataProperties))
	for _, property := range objectType.DataProperties {
		if property == nil {
			continue
		}
		field := classFieldNamed(property.Name)
		switch {
		case field == nil:
			return invalidClass(ctx, "property %s is not one of an event class", property.Name)
		case property.Type != "" && property.Type != field.kind:
			return invalidClass(ctx, "property %s of an event class is of type %s, not %s", property.Name, field.kind, property.Type)
		case property.MappedField != nil && property.MappedField.Name != "" && property.MappedField.Name != field.name:
			return invalidClass(ctx, "property %s of an event class is mapped to field %s, not %s",
				property.Name, field.name, property.MappedField.Name)
		}
		given[property.Name] = property
	}
	properties := make([]*interfaces.DataProperty, 0, len(classFields))
	for _, field := range classFields {
		property := classProperty(field)
		if written := given[field.name]; written != nil {
			if written.DisplayName != "" {
				property.DisplayName = written.DisplayName
			}
			property.Comment = written.Comment
		}
		properties = append(properties, property)
	}
	objectType.DataProperties = properties
	return nil
}

// completeKeys gives a class its keys: an event is known by its ID and shown by its title, and its
// dataset is copied whole rather than by increments.
func completeKeys(ctx context.Context, objectType *interfaces.ObjectType) error {
	switch {
	case len(objectType.PrimaryKeys) > 0 && !slices.Equal(objectType.PrimaryKeys, []string{fieldEventID}):
		return invalidClass(ctx, "the primary key of an event class is %s", fieldEventID)
	case objectType.DisplayKey != "" && objectType.DisplayKey != fieldTitle:
		return invalidClass(ctx, "the display key of an event class is %s", fieldTitle)
	case objectType.IncrementalKey != "":
		return invalidClass(ctx, "an event class has no incremental key")
	}
	objectType.PrimaryKeys = []string{fieldEventID}
	objectType.DisplayKey = fieldTitle
	return nil
}

// Check has nothing to look at before the save: the subject is read in its transaction, where an
// import has written the object types it brings.
func (k *eventKind) Check(context.Context, *interfaces.ObjectType, *interfaces.ObjectType) error {
	return nil
}

// Saved keeps what a class has beside its definition, in the transaction that saved it: the
// dataset, the class's row of rule checks, and the relation to its subject.
func (k *eventKind) Saved(ctx context.Context, tx *sql.Tx, objectType, stored *interfaces.ObjectType) error {
	if stored != nil && stored.Event != nil && stored.Event.Subject != objectType.Event.Subject {
		if err := k.recognizedBy(ctx, tx, objectType, "subject"); err != nil {
			return err
		}
	}
	subjectKey, err := k.subjectKey(ctx, tx, objectType)
	if err != nil {
		return err
	}
	made, err := k.classes.ensure(ctx, objectType.KNID, objectType.OTID)
	if err != nil {
		return k.datasetError(ctx, objectType, err)
	}
	hasEvents, err := k.store.classHasEvents(ctx, tx, objectType.KNID, objectType.OTID)
	if err != nil {
		return classInternalError(ctx, err)
	}
	// A class only now made starts with no events, and nothing else of the ID it was given before:
	// its dataset and its row of checks are what a class of that ID, or a network of that ID, left.
	// A class made again where the ledger still has events of it, as in another branch of the
	// network, keeps them.
	fresh := stored == nil && !hasEvents
	if fresh && !made {
		if err := k.classes.clear(ctx, objectType.KNID, objectType.OTID); err != nil {
			return k.datasetError(ctx, objectType, err)
		}
	}
	// A dataset only now made holds none of the ledger's events: they are written to it again.
	if err := k.store.keepCheckRow(ctx, tx, objectType.KNID, objectType.OTID, fresh, !hasEvents, made && hasEvents); err != nil {
		return classInternalError(ctx, err)
	}
	return k.saveRelation(ctx, tx, objectType, subjectKey)
}

// subjectKey is the single primary key of the subject of a class, which an event's object key
// stands for.
func (k *eventKind) subjectKey(ctx context.Context, tx *sql.Tx, objectType *interfaces.ObjectType) (string, error) {
	subject := objectType.Event.Subject
	found, err := k.objects.GetObjectTypesByIDs(ctx, tx, objectType.KNID, objectType.Branch, []string{subject})
	if err != nil {
		return "", httperrors.NewHTTPError(ctx, http.StatusInternalServerError,
			berrors.BknBackend_ObjectType_InternalError_GetObjectTypesByIDsFailed).WithErrorDetails(err.Error())
	}
	if len(found) == 0 || found[0] == nil {
		return "", invalidClass(ctx, "the subject %s of event class %s is not an object type of the network",
			subject, objectType.OTID)
	}
	if len(found[0].PrimaryKeys) != 1 {
		return "", invalidClass(ctx, "the subject %s of event class %s must have exactly one primary key: an event names its object by one key",
			subject, objectType.OTID)
	}
	return found[0].PrimaryKeys[0], nil
}

// saveRelation writes the relation from a class to its subject as the relation type API does, as
// the kind's own write. The network's declarations are checked with the class, which is not
// committed yet.
func (k *eventKind) saveRelation(ctx context.Context, tx *sql.Tx, objectType *interfaces.ObjectType, subjectKey string) error {
	relationType := classRelationType(objectType, subjectKey)
	// The save of the class is authorized; the relation is part of it.
	relationCtx := permission.WithKNImportPermissionPrechecked(ctx)
	relationCtx, saved, owner := corelogics.DeferNetworkRelations(relationCtx)
	if _, err := k.relations.CreateRelationTypes(relationCtx, tx, []*interfaces.RelationType{relationType},
		interfaces.ImportMode_Overwrite, false); err != nil {
		return err
	}
	if !owner {
		saved.ObjectTypes = append(saved.ObjectTypes, objectType)
		return nil
	}
	return k.declarations.CheckRefusal(ctx, objectType.KNID, objectType.Branch,
		corelogics.SavedDefinitions{ObjectTypes: []*interfaces.ObjectType{objectType}, RelationTypes: saved.RelationTypes},
		berrors.BknBackend_RelationType_InvalidParameter_Declarations, berrors.BknBackend_RelationType_InternalError)
}

func (k *eventKind) datasetError(ctx context.Context, objectType *interfaces.ObjectType, err error) error {
	var foreign errForeignDataset
	if errors.As(err, &foreign) {
		return httperrors.NewHTTPError(ctx, http.StatusConflict, berrors.BknBackend_ObjectType_InvalidParameter).
			WithErrorDetails(fmt.Sprintf("the dataset of event class %s cannot be kept: %v", objectType.OTID, err))
	}
	return classInternalError(ctx, err)
}

func classInternalError(ctx context.Context, err error) error {
	var httpErr *httperrors.HTTPError
	if errors.As(err, &httpErr) {
		return err
	}
	return httperrors.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_ObjectType_InternalError).
		WithErrorDetails(err.Error())
}

// Deleting refuses to delete a class that has events or that a record-event action type records,
// and deletes its relation to the subject. Events come first: deleting the action type would not
// let the class go. It holds the network's head row, so no event of the class is written
// meanwhile. The dataset stays: it holds nothing of the class, and a class made again with the ID
// finds it. A versioned network deletes the class in its draft while the running version still
// has it, so only an unversioned network lets its row of rule checks go.
func (k *eventKind) Deleting(ctx context.Context, tx *sql.Tx, objectType *interfaces.ObjectType) error {
	if err := k.store.holdHead(ctx, tx, objectType.KNID); err != nil {
		return classInternalError(ctx, err)
	}
	hasEvents, err := k.store.classHasEvents(ctx, tx, objectType.KNID, objectType.OTID)
	if err != nil {
		return classInternalError(ctx, err)
	}
	if hasEvents {
		return httperrors.NewHTTPError(ctx, http.StatusConflict, berrors.BknBackend_KnEvent_EventTypeInUse).
			WithErrorDetails(objectType.OTID)
	}
	if err := k.recognizedBy(ctx, tx, objectType, "delete"); err != nil {
		return err
	}
	relationID := objectType.OTID + relationSuffix
	_, exists, err := k.relations.CheckRelationTypeExistByID(ctx, objectType.KNID, objectType.Branch, relationID)
	if err != nil {
		return err
	}
	if exists {
		if err := k.relations.DeleteRelationTypesByIDs(ctx, tx, objectType.KNID, objectType.Branch,
			[]string{relationID}); err != nil {
			return err
		}
	}
	if objectType.Branch != interfaces.MAIN_BRANCH {
		return nil
	}
	if err := k.store.deleteCheckRow(ctx, tx, objectType.KNID, objectType.OTID); err != nil {
		return classInternalError(ctx, err)
	}
	return nil
}

// OwnedRelationTypes is the relation from a class to its subject. It names its ends; the mapping
// to the subject's key is filled when it is written.
func (k *eventKind) OwnedRelationTypes(objectType *interfaces.ObjectType) []*interfaces.RelationType {
	if objectType == nil || objectType.OTID == "" || objectType.Event == nil || objectType.Event.Subject == "" {
		return nil
	}
	return []*interfaces.RelationType{classRelationType(objectType, "")}
}

// PlatformResource reports whether the resource is the dataset of the class.
func (k *eventKind) PlatformResource(knID, objectTypeID, resourceID string) bool {
	return objectTypeID != "" && resourceID == classDatasetID(knID, objectTypeID)
}
