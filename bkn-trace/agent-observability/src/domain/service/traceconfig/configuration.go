package traceconfig

import "time"

type Configuration struct {
	DesiredEnabled     bool            `json:"desired_enabled"`
	EffectiveEnabled   bool            `json:"effective_enabled"`
	LastStableRevision uint64          `json:"last_stable_revision"`
	Operation          *Operation      `json:"operation,omitempty"`
	Revision           uint64          `json:"revision"`
	Services           []ServiceStatus `json:"services"`
}

type Operation struct {
	ID          string             `json:"id"`
	Phase       ConfigurationPhase `json:"phase"`
	RequestedAt time.Time          `json:"requested_at"`
	RequestedBy string             `json:"requested_by"`
}

type ServiceStatus struct {
	AppliedRevision  uint64 `json:"applied_revision"`
	DesiredRevision  uint64 `json:"desired_revision"`
	Name             string `json:"name"`
	Phase            string `json:"phase"`
	ReadyReplicas    int    `json:"ready_replicas"`
	RequiredReplicas int    `json:"required_replicas"`
}

type ConfigurationPhase string

const (
	PhasePending     ConfigurationPhase = "pending"
	PhaseRollingOut  ConfigurationPhase = "rolling_out"
	PhaseEnabled     ConfigurationPhase = "enabled"
	PhaseDisabled    ConfigurationPhase = "disabled"
	PhaseRollingBack ConfigurationPhase = "rolling_back"
	PhaseFailed      ConfigurationPhase = "failed"
)
