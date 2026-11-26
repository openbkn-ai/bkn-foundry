// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"fmt"
	"math"
	"sort"
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

// How an event class handles its events with an action type: auto runs it on the object of each
// event a check newly finds; suggest only offers it to the person handling an event.
const (
	EventHandlingAuto    = "auto"
	EventHandlingSuggest = "suggest"
)

// Where the value of a bound input of a handling action is from: a constant, or a property of the
// object the event is about.
const (
	EventHandlingFromConst    = "const"
	EventHandlingFromProperty = "property"
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
	// HandlingAction is the action type that handles an event of the class, if any.
	HandlingAction *EventHandlingAction `yaml:"handling_action,omitempty"`
}

// EventHandlingAction is the action type that handles the events of a class, and how.
type EventHandlingAction struct {
	ActionTypeID string `yaml:"action_type_id"`
	Mode         string `yaml:"mode"`
	// Parameters gives inputs of the action type a value, by the input's name. Run by itself, the
	// action needs each of its inputs given here; suggested, these are the values offered.
	Parameters map[string]EventHandlingParameter `yaml:"parameters,omitempty"`
}

// EventHandlingParameter is the value of an input of a handling action: a constant (value_from
// const), or the property of the object the event is about that value names (value_from property).
type EventHandlingParameter struct {
	ValueFrom string `yaml:"value_from"`
	Value     any    `yaml:"value"`
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
	if event.HandlingAction != nil {
		event.HandlingAction.ActionTypeID = strings.TrimSpace(event.HandlingAction.ActionTypeID)
		event.HandlingAction.Mode = strings.TrimSpace(event.HandlingAction.Mode)
		event.HandlingAction.Parameters = TrimHandlingParameters(event.HandlingAction.Parameters)
	}
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
	if handling := event.HandlingAction; handling != nil {
		switch {
		case handling.ActionTypeID == "":
			return specErrorf("event class %s: event.handling_action.action_type_id is required: the action type that handles its events", ot.ID)
		case handling.Mode != EventHandlingAuto && handling.Mode != EventHandlingSuggest:
			return specErrorf("event class %s: event.handling_action.mode must be %s or %s", ot.ID, EventHandlingAuto, EventHandlingSuggest)
		case handling.Mode == EventHandlingAuto && event.Source != EventSourceRule:
			return specErrorf("event class %s: only a class whose source is rule runs its handling action by itself; use mode %s",
				ot.ID, EventHandlingSuggest)
		}
		if problem := HandlingParameterProblem(handling.Parameters); problem != "" {
			return specErrorf("event class %s: event.handling_action.parameters: %s", ot.ID, problem)
		}
	}
	return nil
}

// TrimHandlingParameters trims the names of bound inputs, where their values are from and the
// properties they name.
func TrimHandlingParameters(parameters map[string]EventHandlingParameter) map[string]EventHandlingParameter {
	if len(parameters) == 0 {
		return nil
	}
	trimmed := make(map[string]EventHandlingParameter, len(parameters))
	for name, parameter := range parameters {
		parameter.ValueFrom = strings.TrimSpace(parameter.ValueFrom)
		if property, ok := parameter.Value.(string); ok && parameter.ValueFrom == EventHandlingFromProperty {
			parameter.Value = strings.TrimSpace(property)
		}
		trimmed[strings.TrimSpace(name)] = parameter
	}
	return trimmed
}

// HandlingParameterProblem says what is wrong with the bound inputs of a handling action on their
// own, or "" when nothing is: each names an input, says where its value is from, and has one.
func HandlingParameterProblem(parameters map[string]EventHandlingParameter) string {
	for _, name := range sortedParameterNames(parameters) {
		parameter := parameters[name]
		switch {
		case name == "":
			return "an input needs its name"
		case parameter.ValueFrom != EventHandlingFromConst && parameter.ValueFrom != EventHandlingFromProperty:
			return fmt.Sprintf("value_from of input %s must be %s or %s", name, EventHandlingFromConst, EventHandlingFromProperty)
		case parameter.ValueFrom == EventHandlingFromConst && parameter.Value == nil:
			return fmt.Sprintf("input %s needs a value", name)
		case parameter.ValueFrom == EventHandlingFromProperty:
			if property, _ := parameter.Value.(string); property == "" {
				return fmt.Sprintf("input %s needs the name of the property it takes", name)
			}
		}
	}
	return ""
}

func sortedParameterNames(parameters map[string]EventHandlingParameter) []string {
	names := make([]string, 0, len(parameters))
	for name := range parameters {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// EventHandlingRefusal says why an action type cannot handle the events of a class, or "" when
// it can. The action type must act on the class's subject and must not record events; one the
// platform runs by itself takes no input, since nobody is there to give it.
func EventHandlingRefusal(event *ObjectTypeEvent, boundObject string, recordsEvents bool, inputs []string) string {
	handling := event.HandlingAction
	switch {
	case recordsEvents:
		return fmt.Sprintf("action type %q records events; a handling action acts on the subject", handling.ActionTypeID)
	case boundObject != event.Subject:
		return fmt.Sprintf("action type %q acts on %q; the events are about %q", handling.ActionTypeID, boundObject, event.Subject)
	}
	asked := make(map[string]bool, len(inputs))
	for _, input := range inputs {
		asked[input] = true
	}
	for _, name := range sortedParameterNames(handling.Parameters) {
		if !asked[name] {
			return fmt.Sprintf("action type %q asks for no input %q; only its inputs take a value here", handling.ActionTypeID, name)
		}
	}
	if handling.Mode == EventHandlingAuto {
		var missing []string
		for _, input := range inputs {
			if _, ok := handling.Parameters[input]; !ok {
				missing = append(missing, input)
			}
		}
		if len(missing) > 0 {
			return fmt.Sprintf("action type %q asks for input (%s), which nobody gives when it runs by itself: "+
				"give each a constant or a property of the object, or use mode %s",
				handling.ActionTypeID, strings.Join(missing, ", "), EventHandlingSuggest)
		}
	}
	return ""
}

// EventHandlingPropertyRefusal says which bound input takes a property the subject does not have,
// or "" when none does.
func EventHandlingPropertyRefusal(event *ObjectTypeEvent, properties []string) string {
	have := make(map[string]bool, len(properties))
	for _, property := range properties {
		have[property] = true
	}
	handling := event.HandlingAction
	for _, name := range sortedParameterNames(handling.Parameters) {
		parameter := handling.Parameters[name]
		if parameter.ValueFrom != EventHandlingFromProperty {
			continue
		}
		if property, _ := parameter.Value.(string); !have[property] {
			return fmt.Sprintf("input %q takes property %q, which %q does not have", name, property, event.Subject)
		}
	}
	return ""
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
	if handling := ot.Event.HandlingAction; handling != nil {
		node := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
			strNode("action_type_id"), strNode(handling.ActionTypeID), strNode("mode"), strNode(handling.Mode),
		}}
		if len(handling.Parameters) > 0 {
			parameters := &yaml.Node{Kind: yaml.MappingNode}
			for _, name := range sortedParameterNames(handling.Parameters) {
				parameter := handling.Parameters[name]
				value := &yaml.Node{}
				if err := value.Encode(parameter.Value); err != nil {
					value = strNode(fmt.Sprint(parameter.Value))
				}
				parameters.Content = append(parameters.Content, strNode(name), &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
					strNode("value_from"), strNode(parameter.ValueFrom), strNode("value"), value,
				}})
			}
			node.Content = append(node.Content, strNode("parameters"), parameters)
		}
		add("handling_action", node)
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
