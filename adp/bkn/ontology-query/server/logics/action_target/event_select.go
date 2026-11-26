// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_target

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"

	cond "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common/condition"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/logics"
)

// Selector selects the targets of a record-event action: the objects of its bound object type
// that meet its rule at a check, read as the account in the context. The condition is read by the
// object query, a Cypher query by bkn-backend, a function by the execution factory and a metric
// by the object metric runtime, so the account's permissions and row filters apply as they do
// everywhere else.
type Selector struct {
	Models  interfaces.OntologyManagerAccess
	Objects interfaces.ObjectTypeService
	Queries interfaces.QueryAuthorizationService
	Rules   interfaces.EventRuleAccess
	Tools   interfaces.AgentOperatorAccess
	Metrics interfaces.ObjectMetricQueryServiceV1
	// Limit is the most targets one check selects; a selection past it is cut and marked.
	Limit int
}

// EventTarget is one object a check selected: its hit for the ledger, and its identity for the
// execution record.
type EventTarget struct {
	interfaces.EventHit
	Identity map[string]any
}

// Selection is what one check found. Covered, when not nil, names the only objects the check
// read: the selection was cut, or some metric groups had no value to judge.
type Selection struct {
	Subject          *interfaces.ObjectType
	Targets          []EventTarget
	Covered          []string
	Limited          bool
	RowFilterApplied *bool
	// Message explains a check that read less than every object, for the check record.
	Message string
}

// SelectionError is a selection that failed: the check cannot decide anything.
type SelectionError struct {
	Reason string
}

func (e *SelectionError) Error() string { return e.Reason }

func selectionFailed(format string, args ...any) error {
	return &SelectionError{Reason: fmt.Sprintf(format, args...)}
}

// DependencyReason writes a dependency's refusal as its code and what it said, not as the raw
// JSON of the error.
func DependencyReason(err error) string {
	var httpErr *httperrors.HTTPError
	if !errors.As(err, &httpErr) {
		return err.Error()
	}
	parts := []string{httpErr.ErrorCode}
	if httpErr.Description != "" {
		parts = append(parts, httpErr.Description)
	}
	if httpErr.ErrorDetails != nil {
		if text := interfaces.AttributeText(httpErr.ErrorDetails); text != "" && text != "null" && text != `""` {
			parts = append(parts, text)
		}
	}
	return fmt.Sprintf("%s (HTTP %d)", strings.Join(parts, ": "), httpErr.HTTPCode)
}

// Select runs the rule of a record-event action at the check, on a branch of the network.
func (s *Selector) Select(ctx context.Context, knID, branch string, action *interfaces.ActionType,
	clock CheckClock) (*Selection, error) {
	if err := ValidateDefinition(action); err != nil {
		return nil, &SelectionError{Reason: err.Error()}
	}
	if action.OperationConfig.Type != interfaces.ActionOperationEvent {
		return nil, selectionFailed("only a record-event action selects its targets")
	}
	if branch == "" {
		branch = interfaces.MAIN_BRANCH
	}
	subject, exists, err := s.Models.GetObjectType(ctx, knID, branch, action.ObjectTypeID)
	if err != nil {
		return nil, selectionFailed("read object type %s: %s", action.ObjectTypeID, DependencyReason(err))
	}
	if !exists {
		return nil, selectionFailed("the bound object type %s does not exist", action.ObjectTypeID)
	}
	if len(subject.PrimaryKeys) == 0 {
		return nil, selectionFailed("object type %s has no primary key to name its objects by", subject.OTID)
	}
	limit := min(max(s.Limit, 1), interfaces.EventRuleHitsMax)

	var selection *Selection
	targets := action.OperationConfig.Targets
	switch {
	case targets == nil:
		selection, err = s.byCondition(ctx, knID, branch, action.Condition, &subject, clock, limit)
	case targets.Cypher != nil:
		selection, err = s.byCypher(ctx, knID, branch, targets.Cypher, &subject, clock)
	case targets.Function != nil:
		selection, err = s.byFunction(ctx, targets.Function, &subject, clock)
	default:
		selection, err = s.byMetric(ctx, knID, branch, targets.Metric, &subject, clock)
	}
	if err != nil {
		return nil, err
	}
	selection.Subject = &subject
	s.finish(ctx, knID, branch, selection, limit)
	return selection, nil
}

// finish keeps what the ledger takes: the first hit of each key, at most limit hits, names and
// attributes clipped to the ledger's sizes. A cut selection covers only the hits it kept.
func (s *Selector) finish(ctx context.Context, knID, branch string, selection *Selection, limit int) {
	kept := make([]EventTarget, 0, min(len(selection.Targets), limit))
	seen := make(map[string]bool, len(selection.Targets))
	dropped := 0
	for _, target := range selection.Targets {
		switch {
		case target.Key == "" || seen[target.Key]:
			continue
		case utf8.RuneCountInString(target.Key) > interfaces.EventKeyMax:
			dropped++
			continue
		}
		if len(kept) == limit {
			selection.Limited = true
			break
		}
		seen[target.Key] = true
		kept = append(kept, target)
	}
	selection.Targets = kept
	if selection.Limited {
		selection.Covered = make([]string, 0, len(kept))
		for _, target := range kept {
			selection.Covered = append(selection.Covered, target.Key)
		}
		selection.Message = joinMessage(selection.Message, fmt.Sprintf("the selection was truncated at %d objects; only they were judged", len(kept)))
	}
	if dropped > 0 {
		selection.Message = joinMessage(selection.Message, fmt.Sprintf("%d objects had keys longer than %d characters and were left out", dropped, interfaces.EventKeyMax))
	}
	s.fillNames(ctx, knID, branch, selection)
	for index := range selection.Targets {
		target := &selection.Targets[index]
		if target.Name == "" {
			target.Name = target.Key
		}
		target.Name = clip(target.Name, interfaces.EventNameMax)
		target.Attributes = clipAttributes(target.Attributes)
		if target.Detail != nil {
			if encoded, err := json.Marshal(target.Detail); err != nil || len(encoded) > interfaces.EventDetailMaxBytes {
				target.Detail = nil
			}
		}
	}
	if selection.RowFilterApplied != nil && *selection.RowFilterApplied {
		selection.Message = joinMessage(selection.Message, "a row filter narrowed what the executor reads of "+selection.Subject.OTID)
	}
}

func joinMessage(message, more string) string {
	if message == "" {
		return more
	}
	return message + "; " + more
}

func clip(text string, max int) string {
	if utf8.RuneCountInString(text) <= max {
		return text
	}
	return string([]rune(text)[:max])
}

// clipAttributes keeps at most the ledger's number of attributes, by name, each clipped.
func clipAttributes(attributes map[string]interfaces.EventAttribute) map[string]interfaces.EventAttribute {
	if attributes == nil {
		return map[string]interfaces.EventAttribute{}
	}
	names := make([]string, 0, len(attributes))
	for name := range attributes {
		if name != "" && utf8.RuneCountInString(name) <= interfaces.EventAttributeKeyMax {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) > interfaces.EventAttributesMax {
		names = names[:interfaces.EventAttributesMax]
	}
	clipped := make(map[string]interfaces.EventAttribute, len(names))
	for _, name := range names {
		attribute := attributes[name]
		clipped[name] = interfaces.EventAttribute{
			Key:   clip(attribute.Key, interfaces.EventAttributeKeyMax),
			Label: clip(attribute.Label, interfaces.EventAttributeLabelMax),
		}
	}
	return clipped
}

func textAttribute(value any) interfaces.EventAttribute {
	text := interfaces.AttributeText(value)
	return interfaces.EventAttribute{Key: text, Label: text}
}

// nameLookupBatch is how many objects one name lookup reads.
const nameLookupBatch = 1000

// fillNames names the targets that have no name by their object's display property. A lookup
// that fails leaves them named by their key.
func (s *Selector) fillNames(ctx context.Context, knID, branch string, selection *Selection) {
	subject := selection.Subject
	if subject.DisplayKey == "" || s.Objects == nil {
		return
	}
	missing := map[string][]int{}
	identities := []map[string]any{}
	for index, target := range selection.Targets {
		if target.Name != "" || len(target.Identity) == 0 {
			continue
		}
		if _, known := missing[target.Key]; !known {
			identities = append(identities, target.Identity)
		}
		missing[target.Key] = append(missing[target.Key], index)
	}
	if len(identities) == 0 {
		return
	}
	if s.Queries != nil {
		if err := s.Queries.AuthorizeObjectTypeQuery(ctx, knID, branch, subject.OTID); err != nil {
			return
		}
	}
	properties := append(append([]string(nil), subject.PrimaryKeys...), subject.DisplayKey)
	for start := 0; start < len(identities); start += nameLookupBatch {
		batch := identities[start:min(start+nameLookupBatch, len(identities))]
		objects, err := s.Objects.ScanObjectsByObjectTypeID(ctx, &interfaces.ObjectQueryBaseOnObjectType{
			KNID: knID, Branch: branch, ObjectTypeID: subject.OTID,
			ActualCondition:       identitiesCondition(subject, batch),
			Properties:            properties,
			CommonQueryParameters: interfaces.CommonQueryParameters{RequiredFullProperties: subject.PrimaryKeys},
		}, len(batch))
		if err != nil {
			return
		}
		for _, data := range objects.Datas {
			identity, _ := data[interfaces.SYSTEM_PROPERTY_INSTANCE_IDENTITY].(map[string]any)
			name, ok := interfaces.ScalarText(data[interfaces.SYSTEM_PROPERTY_DISPLAY])
			if !ok || name == "" {
				continue
			}
			for _, index := range missing[interfaces.TargetIdentityKey(identity)] {
				selection.Targets[index].Name = name
			}
		}
	}
}

// identitiesCondition matches the objects of the identities: by in on a single primary key.
func identitiesCondition(subject *interfaces.ObjectType, identities []map[string]any) *cond.CondCfg {
	if len(subject.PrimaryKeys) == 1 {
		key := subject.PrimaryKeys[0]
		values := make([]any, 0, len(identities))
		for _, identity := range identities {
			values = append(values, identity[key])
		}
		return &cond.CondCfg{Name: key, Operation: cond.OperationIn, ValueFrom: cond.ValueFrom_Const, Value: values}
	}
	return logics.BuildInstanceIdentitiesCondition(identities)
}

// identityOfKey is the identity of the subject's object whose key is key, each key value of the
// type of its property; nil when the key does not name one.
func identityOfKey(subject *interfaces.ObjectType, key string) map[string]any {
	if len(subject.PrimaryKeys) == 1 {
		name := subject.PrimaryKeys[0]
		return map[string]any{name: typedKeyValue(subject, name, key)}
	}
	decoder := json.NewDecoder(strings.NewReader(key))
	decoder.UseNumber()
	var identity map[string]any
	if decoder.Decode(&identity) != nil || len(identity) != len(subject.PrimaryKeys) {
		return nil
	}
	for _, name := range subject.PrimaryKeys {
		if identity[name] == nil {
			return nil
		}
	}
	return identity
}

// typedKeyValue writes a key value read as text with the type of its property.
func typedKeyValue(subject *interfaces.ObjectType, property, text string) any {
	for _, field := range subject.DataProperties {
		if field.Name != property {
			continue
		}
		switch schemaForType(field.Type)["type"] {
		case "integer", "number":
			var number json.Number
			if json.Unmarshal([]byte(text), &number) == nil {
				return number
			}
		case "boolean":
			switch text {
			case "true":
				return true
			case "false":
				return false
			}
		}
	}
	return text
}

// byCondition selects the objects of the bound object type that meet the condition, through the
// object query, page by page. The check time and day become constants first, and a comparison
// with another property becomes a comparison of two fields.
func (s *Selector) byCondition(ctx context.Context, knID, branch string, condition *cond.CondCfg,
	subject *interfaces.ObjectType, clock CheckClock, limit int) (*Selection, error) {
	if err := validateTargetCondition(condition, subject); err != nil {
		return nil, &SelectionError{Reason: err.Error()}
	}
	resolved, err := resolveEventCondition(condition, clock)
	if err != nil {
		return nil, &SelectionError{Reason: err.Error()}
	}
	if s.Queries != nil {
		if err := s.Queries.AuthorizeObjectTypeQuery(ctx, knID, branch, subject.OTID); err != nil {
			return nil, selectionFailed("read %s: %s", subject.OTID, DependencyReason(err))
		}
	}
	attributes := conditionProperties(condition, subject)
	properties := append(append([]string(nil), subject.PrimaryKeys...), attributes...)
	if subject.DisplayKey != "" {
		properties = append(properties, subject.DisplayKey)
	}
	objects, err := s.Objects.ScanObjectsByObjectTypeID(ctx, &interfaces.ObjectQueryBaseOnObjectType{
		KNID: knID, Branch: branch, ObjectTypeID: subject.OTID,
		ActualCondition:       resolved,
		Properties:            dedupe(properties),
		CommonQueryParameters: interfaces.CommonQueryParameters{RequiredFullProperties: dedupe(append(append([]string(nil), subject.PrimaryKeys...), attributes...))},
	}, limit)
	if err != nil {
		return nil, selectionFailed("query %s by the condition: %s", subject.OTID, DependencyReason(err))
	}
	rowFilter := objects.RowFilterApplied
	selection := &Selection{Limited: objects.Incomplete || len(objects.Datas) > limit, RowFilterApplied: &rowFilter}
	for _, data := range objects.Datas {
		identity, _ := data[interfaces.SYSTEM_PROPERTY_INSTANCE_IDENTITY].(map[string]any)
		if len(identity) == 0 {
			return nil, selectionFailed("the executor cannot read the primary key of %s, so its objects cannot be told apart", subject.OTID)
		}
		target := EventTarget{Identity: identity, EventHit: interfaces.EventHit{
			Key: interfaces.TargetIdentityKey(identity), Attributes: map[string]interfaces.EventAttribute{},
		}}
		if name, ok := interfaces.ScalarText(data[interfaces.SYSTEM_PROPERTY_DISPLAY]); ok {
			target.Name = name
		}
		for _, property := range attributes {
			if value, exists := data[property]; exists && value != nil {
				target.Attributes[property] = textAttribute(value)
			}
		}
		selection.Targets = append(selection.Targets, target)
	}
	return selection, nil
}

// conditionProperties are the properties a condition reads, other than the primary keys: they
// become the attributes of the events, so an event shows why it was found.
func conditionProperties(condition *cond.CondCfg, subject *interfaces.ObjectType) []string {
	primary := make(map[string]bool, len(subject.PrimaryKeys))
	for _, key := range subject.PrimaryKeys {
		primary[key] = true
	}
	var names []string
	for _, name := range targetProperties(subject, condition, nil) {
		if !primary[name] {
			names = append(names, name)
		}
	}
	return names
}

func dedupe(names []string) []string {
	seen := make(map[string]bool, len(names))
	result := make([]string, 0, len(names))
	for _, name := range names {
		if name != "" && !seen[name] {
			seen[name] = true
			result = append(result, name)
		}
	}
	return result
}

// resolveEventCondition writes the condition as the object query reads it: the check time and
// day as constants, a property reference as a comparison of two fields.
func resolveEventCondition(cfg *cond.CondCfg, clock CheckClock) (*cond.CondCfg, error) {
	if cfg == nil {
		return nil, nil
	}
	result := *cfg
	if cfg.Operation == cond.OperationAnd || cfg.Operation == cond.OperationOr {
		result.SubConds = make([]*cond.CondCfg, len(cfg.SubConds))
		for index, child := range cfg.SubConds {
			resolved, err := resolveEventCondition(child, clock)
			if err != nil {
				return nil, err
			}
			result.SubConds[index] = resolved
		}
		return &result, nil
	}
	switch cfg.ValueFrom {
	case "", cond.ValueFrom_Const:
	case interfaces.LOGIC_PARAMS_VALUE_FROM_PROP:
		if _, noValue := cond.NotRequiredValueOperationMap[cfg.Operation]; noValue {
			return nil, fmt.Errorf("condition operator %q does not accept a reference", cfg.Operation)
		}
		name, ok := cfg.Value.(string)
		if !ok || name == "" {
			return nil, fmt.Errorf("condition property reference requires a name")
		}
		result.ValueFrom, result.Value = cond.ValueFrom_Field, name
	case interfaces.ActionValueFromCheckTime, interfaces.ActionValueFromCheckDate:
		value, err := clock.Value(cfg.ValueFrom, cfg.Value)
		if err != nil {
			return nil, fmt.Errorf("condition on %q: %w", cfg.Name, err)
		}
		result.ValueFrom, result.Value = cond.ValueFrom_Const, value
	default:
		return nil, fmt.Errorf("unsupported value_from %q in a record-event condition", cfg.ValueFrom)
	}
	return &result, nil
}
