// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// An object type's kinds give it platform behaviours, each configured by the section of the same
// name (SPECIFICATION.md §6.9, from 2.1). There is one kind, event: the object type is an event
// class, each of its objects an event of the network's event ledger. The platform keeps an event
// class's data source, properties and keys, and its relation to the object type its events are
// about.

// ObjectTypeKindEvent is the kind of an event class.
const ObjectTypeKindEvent = "event"

// objectTypeKinds are the kinds an object type may list.
var objectTypeKinds = []string{ObjectTypeKindEvent}

// Event source values: a rule finds the events, or people and applications report them.
const (
	EventSourceRule   = "rule"
	EventSourceReport = "report"
)

// Limits of an event class, the platform's own.
const (
	EventClassIDMax        = 64
	EventSeverityMax       = 16
	EventHandlingNoteMax   = 500
	eventSectionTitle      = "Event"
	eventSubjectRelationID = "_subject"
)

// ObjectTypeEvent is the ### Event section of an event class: the configuration of the event kind.
type ObjectTypeEvent struct {
	Source       string  `yaml:"source"`
	Subject      string  `yaml:"subject"`
	Severity     string  `yaml:"severity,omitempty"`
	MergeHours   float64 `yaml:"merge_hours,omitempty"`
	HandlingNote string  `yaml:"handling_note,omitempty"`
}

// HasKind reports whether the object type lists the kind.
func (ot *BknObjectType) HasKind(kind string) bool {
	return ot != nil && containsString(ot.Kinds, kind)
}

// IsEventClass reports whether the object type is an event class.
func (ot *BknObjectType) IsEventClass() bool { return ot.HasKind(ObjectTypeKindEvent) }

// EventSubjectRelationID is the ID of the relation the platform keeps from an event class to the
// object type its events are about. A package does not carry it.
func EventSubjectRelationID(classID string) string { return classID + eventSubjectRelationID }

// parseObjectTypeKinds reads the kinds of an object type of a 2.1 or 3.0 package and the sections
// that configure them.
func parseObjectTypeKinds(obj *BknObjectType, fm rawFrontmatter, sections map[string]string) error {
	kinds, err := parseKindsNode(fm.values["kinds"])
	if err != nil {
		return err
	}
	obj.Kinds = kinds
	section, hasSection := sections[eventSectionTitle]
	switch {
	case obj.IsEventClass() && !hasSection:
		return specErrorf("object type %s lists the event kind; write its configuration in \"### Event\"", obj.ID)
	case !obj.IsEventClass() && hasSection:
		return specErrorf("section \"### Event\" configures the event kind; list it in the frontmatter as kinds: [event]")
	case !hasSection:
		return nil
	}
	event, err := parseEventSection(section)
	if err != nil {
		return err
	}
	obj.Event = event
	return ValidateEventClass(obj)
}

func parseKindsNode(node *yaml.Node) ([]string, error) {
	if node == nil {
		return nil, nil
	}
	if node.Kind != yaml.SequenceNode {
		return nil, specErrorf("frontmatter kinds must be a list, such as [event]")
	}
	kinds := make([]string, 0, len(node.Content))
	for _, item := range node.Content {
		kind := strings.TrimSpace(item.Value)
		switch {
		case item.Kind != yaml.ScalarNode || item.Tag == "!!null":
			return nil, specErrorf("frontmatter kinds must list kinds, such as [event]")
		case !containsString(objectTypeKinds, kind):
			return nil, specErrorf("frontmatter kinds lists %q; the kinds are %s", kind, strings.Join(objectTypeKinds, ", "))
		case containsString(kinds, kind):
			return nil, specErrorf("frontmatter kinds lists %q more than once", kind)
		}
		kinds = append(kinds, kind)
	}
	return kinds, nil
}

// parseEventSection reads the yaml block of ### Event: the configuration under the single key event.
func parseEventSection(section string) (*ObjectTypeEvent, error) {
	content := scanContent(section)
	if len(content.blocks) == 0 {
		return nil, specErrorf("section \"### Event\" holds no yaml code block; write the configuration under event: in a ```yaml block")
	}
	var payload struct {
		Event *ObjectTypeEvent `yaml:"event"`
	}
	decoder := yaml.NewDecoder(strings.NewReader(content.blocks[0].body))
	decoder.KnownFields(true)
	if err := decoder.Decode(&payload); err != nil {
		return nil, specErrorf("section \"### Event\" is invalid: %v", err)
	}
	if payload.Event == nil {
		return nil, specErrorf("section \"### Event\" must hold the configuration under the single key event:")
	}
	event := payload.Event
	event.Source = strings.TrimSpace(event.Source)
	event.Subject = strings.TrimSpace(event.Subject)
	event.Severity = strings.TrimSpace(event.Severity)
	event.HandlingNote = strings.TrimSpace(event.HandlingNote)
	return event, nil
}

// ValidateEventClass checks an event class's configuration on its own. The platform checks the
// same on every save; whether the subject exists is a matter of the network (ValidateNetwork).
func ValidateEventClass(ot *BknObjectType) error {
	if !ot.IsEventClass() {
		if ot != nil && ot.Event != nil {
			return specErrorf("object type %s has an event configuration but does not list the event kind", ot.ID)
		}
		return nil
	}
	event := ot.Event
	switch {
	case event == nil:
		return specErrorf("object type %s lists the event kind; write its configuration in \"### Event\"", ot.ID)
	case utf8.RuneCountInString(ot.ID) > EventClassIDMax:
		return specErrorf("the id of event class %s exceeds %d characters", ot.ID, EventClassIDMax)
	case event.Source != EventSourceRule && event.Source != EventSourceReport:
		return specErrorf("event class %s: event.source must be %s or %s", ot.ID, EventSourceRule, EventSourceReport)
	case event.Subject == "":
		return specErrorf("event class %s: event.subject is required: the object type the events are about", ot.ID)
	case event.Subject == ot.ID:
		return specErrorf("event class %s cannot be its own subject", ot.ID)
	case utf8.RuneCountInString(event.Severity) > EventSeverityMax:
		return specErrorf("event class %s: event.severity exceeds %d characters", ot.ID, EventSeverityMax)
	case event.MergeHours < 0 || math.IsNaN(event.MergeHours) || math.IsInf(event.MergeHours, 0):
		return specErrorf("event class %s: event.merge_hours must be a non-negative number", ot.ID)
	case utf8.RuneCountInString(event.HandlingNote) > EventHandlingNoteMax:
		return specErrorf("event class %s: event.handling_note exceeds %d characters", ot.ID, EventHandlingNoteMax)
	}
	return nil
}

// HasEventClasses reports whether the network has an event class, which a package carries from
// 2.1 on.
func HasEventClasses(doc *BknNetwork) bool {
	for _, ot := range doc.ObjectTypes {
		if ot.IsEventClass() {
			return true
		}
	}
	return false
}

// kindsField is the kinds frontmatter field, or nothing for a plain object type. The 2.1 writer
// writes it too.
func kindsField(kinds []string) []frontmatterField {
	if len(kinds) == 0 {
		return nil
	}
	field := listField("kinds", kinds)
	field.legacy = true
	return []frontmatterField{field}
}

// writeEvent writes the ### Event section of an event class.
func writeEvent(sb *strings.Builder, ot *BknObjectType) {
	if !ot.IsEventClass() || ot.Event == nil {
		return
	}
	config := &yaml.Node{Kind: yaml.MappingNode}
	add := func(key string, value *yaml.Node) { config.Content = append(config.Content, strNode(key), value) }
	add("source", strNode(ot.Event.Source))
	add("subject", strNode(ot.Event.Subject))
	if ot.Event.Severity != "" {
		add("severity", strNode(ot.Event.Severity))
	}
	if ot.Event.MergeHours != 0 {
		add("merge_hours", hoursNode(ot.Event.MergeHours))
	}
	if ot.Event.HandlingNote != "" {
		add("handling_note", strNode(ot.Event.HandlingNote))
	}
	root := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{strNode("event"), config}}
	_, _ = fmt.Fprintf(sb, "### Event\n\n%s\n", encodeYAMLBlock(root))
}

// hoursNode writes a merge window as people write it: 24, not 24.0.
func hoursNode(hours float64) *yaml.Node {
	if hours == math.Trunc(hours) && math.Abs(hours) < 1<<53 {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: fmt.Sprintf("%d", int64(hours))}
	}
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!float", Value: fmt.Sprintf("%g", hours)}
}
