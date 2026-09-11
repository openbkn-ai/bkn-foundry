// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package authzmigration implements the versioned, offline authorization
// migration used for the four-edition permission model.
package authzmigration

import (
	"errors"
	"time"
)

const (
	// CurrentVersion is persisted only after every Core and Enterprise check has
	// succeeded. Changing authorization storage semantics requires a new value.
	CurrentVersion = "authz-edition-boundaries-v1"

	ActionKeep     = "keep"
	ActionClassify = "classify"
	ActionInsert   = "insert"
	ActionRemove   = "remove"

	EETableAbsent          = "absent"
	EETablePresentEmpty    = "present_empty"
	EETablePresentWithRows = "present_with_rows"

	EEClassificationPublished = "published_effective"
	EEClassificationDormant   = "dormant_experimental"
	EEClassificationInvalid   = "invalid_unclassified"
)

var ErrPlanBlocked = errors.New("authorization migration plan is blocked")

// LifecycleEvidence is an authoritative resource-lifecycle fact exported for
// the maintenance window. The migrator deliberately cannot infer ownership or
// creation intent from an operation set.
type LifecycleEvidence struct {
	Kind        string `json:"kind"`
	AccessorID  string `json:"accessor_id"`
	Object      string `json:"object"`
	Operation   string `json:"operation"`
	EvidenceRef string `json:"evidence_ref"`
}

// CorePolicyPlan describes one existing or lifecycle-derived Core projection.
// PolicyID is zero for a projection that will be inserted from evidence.
type CorePolicyPlan struct {
	PolicyID               uint     `json:"policy_id,omitempty"`
	AccessorID             string   `json:"accessor_id"`
	Object                 string   `json:"object"`
	Operation              string   `json:"operation"`
	Effect                 string   `json:"effect"`
	CurrentPolicySource    string   `json:"current_policy_source,omitempty"`
	CurrentAuthoritySource string   `json:"current_authority_source,omitempty"`
	PlannedPolicySource    string   `json:"planned_policy_source,omitempty"`
	PlannedAuthoritySource string   `json:"planned_authority_source,omitempty"`
	Action                 string   `json:"action"`
	Reason                 string   `json:"reason"`
	EvidenceRef            string   `json:"evidence_ref,omitempty"`
	ExistingGrantIDs       []string `json:"existing_grant_ids,omitempty"`
	CreateGrantIDs         []string `json:"create_grant_ids,omitempty"`
	Anomalies              []string `json:"anomalies,omitempty"`
}

type CoreSummary struct {
	Policies       int `json:"policies"`
	Kept           int `json:"kept"`
	Classified     int `json:"classified"`
	Inserted       int `json:"inserted"`
	Removed        int `json:"removed"`
	GrantsExisting int `json:"grants_existing"`
	GrantsToCreate int `json:"grants_to_create"`
}

// CoreReport is deterministic and safe to serialize as the administrator's
// dry-run artifact. Fatal anomalies prevent every write.
type CoreReport struct {
	MigrationVersion string           `json:"migration_version"`
	Summary          CoreSummary      `json:"summary"`
	Policies         []CorePolicyPlan `json:"policies"`
	Anomalies        []string         `json:"anomalies,omitempty"`
}

func (r CoreReport) Blocked() bool { return len(r.Anomalies) != 0 }

func (r CoreReport) HasChanges() bool {
	return r.Summary.Classified != 0 || r.Summary.Inserted != 0 ||
		r.Summary.Removed != 0 || r.Summary.GrantsToCreate != 0
}

// EEAssemblyEvidence distinguishes a genuinely Community-only database from
// a database whose Enterprise table disappeared unexpectedly.
type EEAssemblyEvidence struct {
	WasAssembled bool   `json:"was_assembled"`
	EvidenceRef  string `json:"evidence_ref,omitempty"`
}

// EERuleEvidence is the proof required to classify one historical row as a
// published rule. Table presence and intrinsic row validity are not evidence.
type EERuleEvidence struct {
	GrantID                 string `json:"grant_id"`
	ExpectedSubjectType     string `json:"expected_subject_type"`
	PublishedWriterEvidence string `json:"published_writer_evidence"`
	RuntimeUsageEvidence    string `json:"runtime_usage_evidence"`
}

// EEActivationConfirmation proves that an administrator reviewed the exact
// dry-run inventory. ConfirmedGrantIDs is explicit; an empty list activates
// nothing.
type EEActivationConfirmation struct {
	ConfirmedGrantIDs []string  `json:"confirmed_grant_ids"`
	InventoryDigest   string    `json:"inventory_digest"`
	OperatorID        string    `json:"operator_id"`
	EvidenceRef       string    `json:"evidence_ref"`
	ConfirmedAt       time.Time `json:"confirmed_at"`
}

type EEOptions struct {
	Now          time.Time                 `json:"-"`
	Assembly     EEAssemblyEvidence        `json:"assembly"`
	RuleEvidence []EERuleEvidence          `json:"rule_evidence,omitempty"`
	Activation   *EEActivationConfirmation `json:"activation,omitempty"`
}

type EERulePlan struct {
	GrantID                 string     `json:"grant_id"`
	AccessorID              string     `json:"accessor_id"`
	ResourceType            string     `json:"resource_type"`
	ResourceID              string     `json:"resource_id"`
	Operation               string     `json:"operation"`
	Effect                  string     `json:"effect"`
	ExpiresAt               *time.Time `json:"expires_at,omitempty"`
	CurrentSubjectType      string     `json:"current_subject_type,omitempty"`
	PlannedSubjectType      string     `json:"planned_subject_type"`
	CurrentClassification   string     `json:"current_classification,omitempty"`
	PlannedClassification   string     `json:"planned_classification"`
	CurrentActivation       string     `json:"current_activation,omitempty"`
	PlannedActivation       string     `json:"planned_activation"`
	PublishedWriterEvidence string     `json:"published_writer_evidence,omitempty"`
	RuntimeUsageEvidence    string     `json:"runtime_usage_evidence,omitempty"`
	ClassificationEvidence  string     `json:"classification_evidence,omitempty"`
	ActivationEligible      bool       `json:"activation_eligible"`
	ReviewPriority          string     `json:"review_priority"`
	Reason                  string     `json:"reason"`
	Anomalies               []string   `json:"anomalies,omitempty"`
}

type EESummary struct {
	Rows       int `json:"rows"`
	Published  int `json:"published_effective"`
	Dormant    int `json:"dormant_experimental"`
	Invalid    int `json:"invalid_unclassified"`
	Active     int `json:"active"`
	ToActivate int `json:"to_activate"`
	Deny       int `json:"deny"`
}

type EEReport struct {
	MigrationVersion string             `json:"migration_version"`
	TableState       string             `json:"ee_table_state"`
	Assembly         EEAssemblyEvidence `json:"assembly"`
	InventoryDigest  string             `json:"inventory_digest"`
	Summary          EESummary          `json:"summary"`
	Rules            []EERulePlan       `json:"rules"`
	Anomalies        []string           `json:"anomalies,omitempty"`
}

func (r EEReport) Blocked() bool { return len(r.Anomalies) != 0 }

func (r EEReport) HasChanges() bool {
	for _, rule := range r.Rules {
		if rule.CurrentSubjectType != rule.PlannedSubjectType ||
			rule.CurrentClassification != rule.PlannedClassification ||
			rule.CurrentActivation != rule.PlannedActivation {
			return true
		}
	}
	return false
}
