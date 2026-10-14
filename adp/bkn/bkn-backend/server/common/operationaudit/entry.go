// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.

package operationaudit

import "time"

var allowedActions = map[string]struct{}{
	"create": {}, "update": {}, "delete": {}, "import": {},
	"add_members": {}, "remove_members": {}, "enable": {}, "disable": {},
	"attach": {}, "detach": {},
}

var allowedTargetTypes = map[string]struct{}{
	"knowledge_network": {}, "object_type": {}, "relation_type": {}, "action_type": {},
	"metric": {}, "risk_type": {}, "concept_group": {}, "action_schedule": {},
	"kn_capability_binding": {},
}

var allowedOutcomes = map[string]struct{}{"success": {}, "failure": {}, "denied": {}}

// Entry is one bounded management fact emitted by BKN Backend. It contains
// neither request/response bodies nor credentials and is never written to a
// module-local audit table in 0.2.0.
type Entry struct {
	EventID            string
	EventTime          time.Time
	RecordedAt         time.Time
	KnowledgeNetworkID string
	ActorID            string
	ActorName          string
	ActorType          string
	AuthMethod         string
	CredentialID       string
	RequestID          string
	SourceChannel      string
	Method             string
	HTTPStatus         int
	Action             string
	TargetType         string
	TargetID           string
	TargetName         string
	Outcome            string
	FailureCode        string
	FailureMessage     string
	ChangeSummary      map[string]any
}
