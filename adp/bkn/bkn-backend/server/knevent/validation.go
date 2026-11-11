// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knevent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

var eventTypeCodePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,63}$`)

// Limits the contract leaves to the columns that hold them.
const (
	subjectMax       = 128
	attributeNameMax = 128
	objectNameMax    = 256
	messageMax       = 1024
)

// invalidInput is a 400 with the field that is wrong.
type invalidInput struct{ detail string }

func (e invalidInput) Error() string { return e.detail }

// tooLarge is a 413 for input past a size limit.
type tooLarge struct{ detail string }

func (e tooLarge) Error() string { return e.detail }

func invalidf(format string, args ...any) error {
	return invalidInput{detail: fmt.Sprintf(format, args...)}
}

func longerThan(value string, limit int) bool {
	return utf8.RuneCountInString(value) > limit
}

// normalizeJSON keeps an optional JSON value, writing an absent one as {}.
func normalizeJSON(field string, raw json.RawMessage, limit int) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return json.RawMessage("{}"), nil
	}
	if len(trimmed) > limit {
		return nil, tooLarge{detail: fmt.Sprintf("%s exceeds %d bytes", field, limit)}
	}
	return json.RawMessage(trimmed), nil
}

func validateEventTypeInput(input *interfaces.KnEventTypeInput) error {
	input.Name = strings.TrimSpace(input.Name)
	switch {
	case input.Name == "":
		return invalidf("name is required")
	case longerThan(input.Name, interfaces.KnEventNameMax):
		return invalidf("name exceeds %d characters", interfaces.KnEventNameMax)
	case input.Source != interfaces.KnEventSourceRule && input.Source != interfaces.KnEventSourceReport:
		return invalidf("source must be rule or report")
	case longerThan(input.Subject, subjectMax):
		return invalidf("subject exceeds %d characters", subjectMax)
	case longerThan(input.Severity, interfaces.KnEventSeverityMax):
		return invalidf("severity exceeds %d characters", interfaces.KnEventSeverityMax)
	case input.MergeHours < 0 || math.IsNaN(input.MergeHours) || math.IsInf(input.MergeHours, 0):
		return invalidf("merge_hours must be a non-negative number")
	case longerThan(input.RuleVersion, interfaces.KnEventRuleVersionMax):
		return invalidf("rule_version exceeds %d characters", interfaces.KnEventRuleVersionMax)
	}
	rule, err := normalizeJSON("rule", input.Rule, interfaces.KnEventRuleMaxBytes)
	if err != nil {
		return err
	}
	input.Rule = rule
	return nil
}

func validateAttributes(field string, attributes map[string]interfaces.KnEventAttribute) error {
	if len(attributes) > interfaces.KnEventAttributesMax {
		return invalidf("%s has more than %d entries", field, interfaces.KnEventAttributesMax)
	}
	for name, value := range attributes {
		switch {
		case name == "" || longerThan(name, attributeNameMax):
			return invalidf("%s names must be 1 to %d characters", field, attributeNameMax)
		case longerThan(value.Key, interfaces.KnEventKeyMax):
			return invalidf("%s.%s.key exceeds %d characters", field, name, interfaces.KnEventKeyMax)
		case longerThan(value.Label, interfaces.KnEventLabelMax):
			return invalidf("%s.%s.label exceeds %d characters", field, name, interfaces.KnEventLabelMax)
		}
	}
	return nil
}

func validateReportInput(input *interfaces.KnEventReportInput) error {
	input.Title = strings.TrimSpace(input.Title)
	switch {
	case input.EventType == "":
		return invalidf("event_type is required")
	case input.Title == "":
		return invalidf("title is required")
	case longerThan(input.Title, interfaces.KnEventTitleMax):
		return invalidf("title exceeds %d characters", interfaces.KnEventTitleMax)
	case input.OccurredAt < 0:
		return invalidf("occurred_at must not be negative")
	case longerThan(input.ObjectKey, interfaces.KnEventKeyMax):
		return invalidf("object_key exceeds %d characters", interfaces.KnEventKeyMax)
	case longerThan(input.ObjectName, objectNameMax):
		return invalidf("object_name exceeds %d characters", objectNameMax)
	}
	if err := validateAttributes("object_attributes", input.ObjectAttributes); err != nil {
		return err
	}
	detail, err := normalizeJSON("detail", input.Detail, interfaces.KnEventDetailMaxBytes)
	if err != nil {
		return err
	}
	input.Detail = detail
	return nil
}

func validateReason(input *interfaces.KnEventReasonInput) error {
	input.Reason = strings.TrimSpace(input.Reason)
	if longerThan(input.Reason, interfaces.KnEventReasonMax) {
		return invalidf("reason exceeds %d characters", interfaces.KnEventReasonMax)
	}
	return nil
}

func validateRuleCheckInput(input *interfaces.KnEventRuleCheckInput) error {
	switch {
	case input.EventType == "":
		return invalidf("event_type is required")
	case input.Status != interfaces.KnEventCheckOK && input.Status != interfaces.KnEventCheckUnknown:
		return invalidf("status must be ok or unknown")
	case longerThan(input.RuleVersion, interfaces.KnEventRuleVersionMax):
		return invalidf("rule_version exceeds %d characters", interfaces.KnEventRuleVersionMax)
	case longerThan(input.Message, messageMax):
		return invalidf("message exceeds %d characters", messageMax)
	case len(input.Hits) > interfaces.KnEventRuleHitsMax:
		return tooLarge{detail: fmt.Sprintf("hits exceed %d", interfaces.KnEventRuleHitsMax)}
	}
	for index, hit := range input.Hits {
		switch {
		case hit.Key == "" || longerThan(hit.Key, interfaces.KnEventKeyMax):
			return invalidf("hits[%d].key must be 1 to %d characters", index, interfaces.KnEventKeyMax)
		case longerThan(hit.Name, objectNameMax):
			return invalidf("hits[%d].name exceeds %d characters", index, objectNameMax)
		}
		if err := validateAttributes(fmt.Sprintf("hits[%d].attributes", index), hit.Attributes); err != nil {
			return err
		}
	}
	return nil
}
