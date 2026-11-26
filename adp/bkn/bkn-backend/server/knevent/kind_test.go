// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knevent

import (
	"errors"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces/data_type"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
)

func testKind() *eventKind {
	return &eventKind{available: func() bool { return true }}
}

// newClass is an event class as a caller writes it: the kind and its configuration, nothing the
// platform keeps.
func newClass(id string) *interfaces.ObjectType {
	return &interfaces.ObjectType{
		ObjectTypeWithKeyField: interfaces.ObjectTypeWithKeyField{OTID: id, OTName: "订单超期"},
		Kinds:                  []string{interfaces.ObjectTypeKindEvent},
		Event:                  &interfaces.ObjectTypeEvent{Source: "rule", Subject: "order", Severity: "high"},
		KNID:                   "kn1", Branch: interfaces.MAIN_BRANCH,
	}
}

func refusedAsInvalid(t *testing.T, err error, what string) {
	t.Helper()
	var httpErr *httperrors.HTTPError
	if !errors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusBadRequest ||
		httpErr.ErrorCode != berrors.BknBackend_ObjectType_InvalidParameter {
		t.Fatalf("%s: %v, want 400 %s", what, err, berrors.BknBackend_ObjectType_InvalidParameter)
	}
}

func TestCompleteFillsWhatThePlatformKeeps(t *testing.T) {
	class := newClass("late_order")
	if err := testKind().Complete(userContext(), class, nil); err != nil {
		t.Fatal(err)
	}
	if class.DataSource == nil || class.DataSource.Type != interfaces.DATA_SOURCE_TYPE_RESOURCE ||
		class.DataSource.ID != classDatasetID("kn1", "late_order") {
		t.Fatalf("data source %+v", class.DataSource)
	}
	if !slices.Equal(class.PrimaryKeys, []string{fieldEventID}) || class.DisplayKey != fieldTitle || class.IncrementalKey != "" {
		t.Fatalf("keys %v %q %q", class.PrimaryKeys, class.DisplayKey, class.IncrementalKey)
	}
	if len(class.DataProperties) != len(classFields) {
		t.Fatalf("%d properties, want %d", len(class.DataProperties), len(classFields))
	}
	for index, property := range class.DataProperties {
		field := classFields[index]
		if property.Name != field.name || property.Type != field.kind || property.MappedField == nil ||
			property.MappedField.Name != field.name || property.DisplayName != field.label() {
			t.Fatalf("property %d = %+v, want the standard %s", index, property, field.name)
		}
	}
}

func TestCompleteKeepsTheCallersLabelsOfTheStandardProperties(t *testing.T) {
	class := newClass("late_order")
	class.DataProperties = []*interfaces.DataProperty{{
		Name: "title", DisplayName: "超期说明", Comment: "给值班的人看", Type: data_type.DATATYPE_STRING,
		MappedField: &interfaces.Field{Name: "title"},
	}}
	if err := testKind().Complete(userContext(), class, nil); err != nil {
		t.Fatal(err)
	}
	for _, property := range class.DataProperties {
		if property.Name == "title" && (property.DisplayName != "超期说明" || property.Comment != "给值班的人看") {
			t.Fatalf("title = %+v, want the caller's label and comment", property)
		}
	}
}

func TestCompleteRefusesWhatDiffersFromWhatThePlatformKeeps(t *testing.T) {
	for name, change := range map[string]func(*interfaces.ObjectType){
		"another data source": func(class *interfaces.ObjectType) {
			class.DataSource = &interfaces.ResourceInfo{Type: interfaces.DATA_SOURCE_TYPE_RESOURCE, ID: "orders"}
		},
		"a property of its own": func(class *interfaces.ObjectType) {
			class.DataProperties = []*interfaces.DataProperty{{Name: "amount", Type: data_type.DATATYPE_STRING}}
		},
		"a standard property of another type": func(class *interfaces.ObjectType) {
			class.DataProperties = []*interfaces.DataProperty{{Name: "title", Type: data_type.DATATYPE_DATETIME}}
		},
		"a standard property mapped elsewhere": func(class *interfaces.ObjectType) {
			class.DataProperties = []*interfaces.DataProperty{{Name: "title", MappedField: &interfaces.Field{Name: "subject"}}}
		},
		"a logic property": func(class *interfaces.ObjectType) {
			class.LogicProperties = []*interfaces.LogicProperty{{Name: "overdue_days"}}
		},
		"another primary key": func(class *interfaces.ObjectType) { class.PrimaryKeys = []string{"title"} },
		"another display key": func(class *interfaces.ObjectType) { class.DisplayKey = "status" },
		"an incremental key":  func(class *interfaces.ObjectType) { class.IncrementalKey = "recorded_at" },
		"no source":           func(class *interfaces.ObjectType) { class.Event.Source = "" },
		"an unknown source":   func(class *interfaces.ObjectType) { class.Event.Source = "sensor" },
		"no subject":          func(class *interfaces.ObjectType) { class.Event.Subject = " " },
		"itself as subject":   func(class *interfaces.ObjectType) { class.Event.Subject = class.OTID },
		"a long severity":     func(class *interfaces.ObjectType) { class.Event.Severity = strings.Repeat("s", 17) },
		"a negative window":   func(class *interfaces.ObjectType) { class.Event.MergeHours = -1 },
		"a long handling note": func(class *interfaces.ObjectType) {
			class.Event.HandlingNote = strings.Repeat("处", interfaces.KnEventHandlingNoteMax+1)
		},
		"a long ID": func(class *interfaces.ObjectType) { class.OTID = strings.Repeat("e", interfaces.KnEventClassIDMax+1) },
		"a handling action without its action type": func(class *interfaces.ObjectType) {
			class.Event.HandlingAction = &interfaces.ObjectTypeEventHandling{ActionTypeID: " ", Mode: interfaces.KnEventHandlingAuto}
		},
		"a handling action of an unknown mode": func(class *interfaces.ObjectType) {
			class.Event.HandlingAction = &interfaces.ObjectTypeEventHandling{ActionTypeID: "notify_buyer", Mode: "later"}
		},
		"a reported class handling its events by itself": func(class *interfaces.ObjectType) {
			class.Event.Source = interfaces.KnEventSourceReport
			class.Event.HandlingAction = &interfaces.ObjectTypeEventHandling{ActionTypeID: "notify_buyer", Mode: interfaces.KnEventHandlingAuto}
		},
	} {
		class := newClass("late_order")
		change(class)
		refusedAsInvalid(t, testKind().Complete(userContext(), class, nil), name)
	}
}

func TestCompleteTakesTheStoredConfigurationAndKeepsTheSourceFixed(t *testing.T) {
	stored := newClass("late_order")
	if err := testKind().Complete(userContext(), stored, nil); err != nil {
		t.Fatal(err)
	}
	// An update that leaves the configuration out keeps it.
	update := newClass("late_order")
	update.Event = nil
	if err := testKind().Complete(userContext(), update, stored); err != nil {
		t.Fatal(err)
	}
	if update.Event == nil || *update.Event != *stored.Event || update.Event == stored.Event {
		t.Fatalf("event = %+v, want a copy of the stored one", update.Event)
	}
	// Written back as it was read, the class is as before.
	again := *stored
	if err := testKind().Complete(userContext(), &again, stored); err != nil {
		t.Fatalf("the class as it was read: %v", err)
	}
	changed := newClass("late_order")
	changed.Event.Source = "report"
	refusedAsInvalid(t, testKind().Complete(userContext(), changed, stored), "a changed source")
	missing := newClass("late_order")
	missing.Event = nil
	refusedAsInvalid(t, testKind().Complete(userContext(), missing, nil), "a new class without its configuration")
}

func TestCompleteLeavesAClassWithoutAnIDUnbound(t *testing.T) {
	class := newClass("")
	if err := testKind().Complete(userContext(), class, nil); err != nil {
		t.Fatal(err)
	}
	if class.DataSource != nil || len(class.DataProperties) != len(classFields) {
		t.Fatalf("data source %+v, %d properties", class.DataSource, len(class.DataProperties))
	}
	bound := newClass("")
	bound.DataSource = &interfaces.ResourceInfo{Type: interfaces.DATA_SOURCE_TYPE_RESOURCE, ID: "kn_event_x"}
	refusedAsInvalid(t, testKind().Complete(userContext(), bound, nil), "a data source before the ID")
}

func TestOwnedRelationIsTheRelationToTheSubject(t *testing.T) {
	class := newClass("late_order")
	owned := testKind().OwnedRelationTypes(class)
	if len(owned) != 1 || owned[0].RTID != "late_order"+relationSuffix || owned[0].SourceObjectTypeID != "late_order" ||
		owned[0].TargetObjectTypeID != "order" || owned[0].Type != interfaces.RELATION_TYPE_DIRECT {
		t.Fatalf("owned = %+v", owned)
	}
	for _, without := range []*interfaces.ObjectType{nil, newClass(""), {Kinds: []string{interfaces.ObjectTypeKindEvent}}} {
		if owned := testKind().OwnedRelationTypes(without); owned != nil {
			t.Fatalf("owned of %+v = %+v", without, owned)
		}
	}
	written := classRelationType(class, "order_id")
	mapping, _ := written.MappingRules.([]interfaces.Mapping)
	if len(mapping) != 1 || mapping[0].SourceProp.Name != fieldObjectKey || mapping[0].TargetProp.Name != "order_id" {
		t.Fatalf("mapping %+v", written.MappingRules)
	}
}

func TestPlatformResourceIsTheClassesOwnDataset(t *testing.T) {
	kind := testKind()
	if !kind.PlatformResource("kn1", "late_order", classDatasetID("kn1", "late_order")) {
		t.Fatal("the class's dataset is not kept")
	}
	for _, other := range [][3]string{
		{"kn1", "late_order", classDatasetID("kn1", "other")},
		{"kn1", "late_order", classDatasetID("kn2", "late_order")},
		{"kn1", "late_order", "orders"},
		{"kn1", "", classDatasetID("kn1", "")},
	} {
		if kind.PlatformResource(other[0], other[1], other[2]) {
			t.Fatalf("%v is kept", other)
		}
	}
}

func TestCompleteTakesAHandlingAction(t *testing.T) {
	for _, source := range []string{interfaces.KnEventSourceRule, interfaces.KnEventSourceReport} {
		class := newClass("late_order")
		class.Event.Source = source
		class.Event.HandlingAction = &interfaces.ObjectTypeEventHandling{ActionTypeID: " notify_buyer ", Mode: " suggest "}
		if err := testKind().Complete(userContext(), class, nil); err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		want := interfaces.ObjectTypeEventHandling{ActionTypeID: "notify_buyer", Mode: interfaces.KnEventHandlingSuggest}
		if !reflect.DeepEqual(*class.Event.HandlingAction, want) {
			t.Fatalf("%s: %+v, want %+v", source, *class.Event.HandlingAction, want)
		}
	}
}

// A handling action acts on the subject and records no events; run by the platform, it asks for
// no input, since nobody is there to give it.
func TestHandlingRefusal(t *testing.T) {
	onOrder := handlingAction{objectTypeID: "order"}
	asksForReason := handlingAction{objectTypeID: "order", inputs: []string{"reason"}}
	fixed := map[string]interfaces.ObjectTypeEventHandlingParameter{"reason": {ValueFrom: "const", Value: "late"}}
	fromBuyer := map[string]interfaces.ObjectTypeEventHandlingParameter{"reason": {ValueFrom: "property", Value: "buyer"}}
	fromMissing := map[string]interfaces.ObjectTypeEventHandlingParameter{"reason": {ValueFrom: "property", Value: "nowhere"}}
	notAnInput := map[string]interfaces.ObjectTypeEventHandlingParameter{"other": {ValueFrom: "const", Value: 1}}
	for name, c := range map[string]struct {
		mode       string
		action     handlingAction
		parameters map[string]interfaces.ObjectTypeEventHandlingParameter
		refused    string
	}{
		"auto on the subject":         {interfaces.KnEventHandlingAuto, onOrder, nil, ""},
		"another object type":         {interfaces.KnEventHandlingSuggest, handlingAction{objectTypeID: "buyer"}, nil, `acts on "buyer"`},
		"a recognition action":        {interfaces.KnEventHandlingAuto, handlingAction{objectTypeID: "order", recordsEvents: true}, nil, "records events"},
		"input left open, run itself": {interfaces.KnEventHandlingAuto, asksForReason, nil, "reason"},
		"input offered to a person":   {interfaces.KnEventHandlingSuggest, asksForReason, nil, ""},
		"input fixed, run itself":     {interfaces.KnEventHandlingAuto, asksForReason, fixed, ""},
		"input from a property":       {interfaces.KnEventHandlingAuto, asksForReason, fromBuyer, ""},
		"input from no property":      {interfaces.KnEventHandlingAuto, asksForReason, fromMissing, "nowhere"},
		"a bound name not an input":   {interfaces.KnEventHandlingSuggest, asksForReason, notAnInput, "other"},
	} {
		event := &interfaces.ObjectTypeEvent{Source: "rule", Subject: "order",
			HandlingAction: &interfaces.ObjectTypeEventHandling{ActionTypeID: "notify_buyer", Mode: c.mode, Parameters: c.parameters}}
		got := handlingRefusal(event, c.action, []string{"order_id", "buyer"})
		if c.refused == "" && got != "" || c.refused != "" && !strings.Contains(got, c.refused) {
			t.Fatalf("%s: %q, want %q", name, got, c.refused)
		}
	}
}
