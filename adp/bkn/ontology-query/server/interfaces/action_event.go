// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
)

// A record-event action (operation category event) selects, at each check, the objects of its
// bound object type that meet its rule, and hands them to the event ledger of bkn-backend in one
// rule check. Only its schedule runs it, as the schedule's execution subject.

// PermissionOperationModify is the operation that lets a person change a knowledge network's
// definitions, which trying an action type needs as saving it does.
const PermissionOperationModify = "modify"

// Limits of one rule check, the ledger's own (bkn-backend validates them).
const (
	EventRuleHitsMax       = 20000
	EventKeyMax            = 250
	EventNameMax           = 256
	EventAttributesMax     = 64
	EventAttributeKeyMax   = 250
	EventAttributeLabelMax = 250
	EventDetailMaxBytes    = 16 << 10
	EventTrialHitsMax      = 200
	EventRuleCheckStatusOK = "ok"
)

// EventAttribute is one attribute of a hit: its value as a key and as a label.
type EventAttribute struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

// EventHit is one object a check found: its identity key, its name and the attributes the event
// carries. Detail is written to the event only when the check opens it.
type EventHit struct {
	Key        string                    `json:"key"`
	Name       string                    `json:"name"`
	Attributes map[string]EventAttribute `json:"attributes"`
	Detail     map[string]any            `json:"detail,omitempty"`
}

// EventRuleCheckRequest is the body of POST .../rule-checks. Covered is nil when the check read
// every object it could; otherwise it names the objects it read, and the others are left alone.
type EventRuleCheckRequest struct {
	EventType    string     `json:"event_type"`
	ActionTypeID string     `json:"action_type_id"`
	RuleVersion  string     `json:"rule_version"`
	Status       string     `json:"status"`
	Message      string     `json:"message,omitempty"`
	Hits         []EventHit `json:"hits"`
	Covered      []string   `json:"covered"`
}

// EventRuleCheckResult is the ledger's answer to a rule check, kept as it came.
type EventRuleCheckResult struct {
	Events []json.RawMessage `json:"events"`
	Check  map[string]any    `json:"check"`
}

// CypherQueryRequest is the body of bkn-backend's internal Cypher query.
type CypherQueryRequest struct {
	Query      string         `json:"query"`
	Parameters map[string]any `json:"parameters"`
}

// CypherColumn is one result column of a Cypher query.
type CypherColumn struct {
	Name string `json:"name"`
	Type string `json:"type,omitempty"`
}

// CypherValueType says what a column holds when the query returns graph values.
type CypherValueType struct {
	Kind         string `json:"kind"`
	ObjectTypeID string `json:"object_type_id,omitempty"`
}

// CypherPermissionImpact names an object type whose access rules narrowed a query.
type CypherPermissionImpact struct {
	ObjectTypeID     string `json:"object_type_id"`
	RowFilterApplied bool   `json:"row_filter_applied,omitempty"`
}

// CypherQueryResult is the answer of bkn-backend's internal Cypher query. Numbers stay
// json.Number, so a 19-digit key is not rounded.
type CypherQueryResult struct {
	Columns           []CypherColumn              `json:"columns"`
	Entries           []map[string]any            `json:"entries"`
	ValueTypes        map[string]*CypherValueType `json:"value_types,omitempty"`
	PermissionImpacts []CypherPermissionImpact    `json:"permission_impacts,omitempty"`
	Limited           bool                        `json:"limited,omitempty"`
}

// CypherKindObject is the value kind of a node.
const CypherKindObject = "object"

// EventRuleAccess is what a record-event action needs from bkn-backend, each call made as the
// account in ctx: a Cypher query of the network, and the ledger's rule check.
//
//go:generate mockgen -source ../interfaces/action_event.go -destination ../interfaces/mock/mock_action_event.go
type EventRuleAccess interface {
	QueryCypher(ctx context.Context, knID, branch string, query *CypherQueryRequest) (*CypherQueryResult, error)
	RecordRuleCheck(ctx context.Context, knID, executionID string, check *EventRuleCheckRequest) (*EventRuleCheckResult, error)
}

// ActionTrialResponse is what trying an action type found. Only record-event actions can be
// tried in this version.
type ActionTrialResponse struct {
	Event *EventTrialResult `json:"event,omitempty"`
}

// EventTrialResult is what a record-event action would record if it checked now: at most
// EventTrialHitsMax of its hits, how many there are (up to the selection limit), and whether the
// selection was cut at that limit. RowFilterApplied is set only when it is known.
type EventTrialResult struct {
	Subject          string     `json:"subject"`
	Hits             []EventHit `json:"hits"`
	HitCount         int        `json:"hit_count"`
	Limited          bool       `json:"limited"`
	RowFilterApplied *bool      `json:"row_filter_applied,omitempty"`
}

// ActionTrialService tries an action type definition without saving or executing it.
type ActionTrialService interface {
	TrialAction(ctx context.Context, knID, branch string, action *ActionType) (*ActionTrialResponse, error)
}

// TargetIdentityKey is the identity key of an action target: the value of its single key as text,
// or, for an identity of several keys, its canonical JSON with the keys sorted. It is the key a
// record-event action gives the ledger for the object, and what the action-log filter target_key
// matches. Empty when the identity is empty or cannot be written.
func TargetIdentityKey(identity map[string]any) string {
	switch len(identity) {
	case 0:
		return ""
	case 1:
		for _, value := range identity {
			if text, ok := ScalarText(value); ok {
				return text
			}
		}
	}
	// encoding/json writes map keys sorted, and json.Number as its digits.
	encoded, err := json.Marshal(identity)
	if err != nil {
		return ""
	}
	return string(encoded)
}

// ScalarText writes a string, a number or a boolean as text, a number exactly as it was read;
// ok is false for any other value.
func ScalarText(value any) (string, bool) {
	switch v := value.(type) {
	case string:
		return v, true
	case json.Number:
		return v.String(), true
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), true
	case float32:
		return strconv.FormatFloat(float64(v), 'f', -1, 32), true
	case int:
		return strconv.Itoa(v), true
	case int64:
		return strconv.FormatInt(v, 10), true
	case int32:
		return strconv.FormatInt(int64(v), 10), true
	case uint64:
		return strconv.FormatUint(v, 10), true
	case bool:
		return strconv.FormatBool(v), true
	}
	return "", false
}

// AttributeText writes any value as attribute text: JSON for one that is not a scalar.
func AttributeText(value any) string {
	if text, ok := ScalarText(value); ok {
		return text
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(encoded)
}
