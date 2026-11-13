// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"context"
	"fmt"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
)

// CapabilitySkip is one declared capability bkn-backend could not bind on
// import (bkn-backend interfaces.CapabilitySkip).
type CapabilitySkip struct {
	CapabilityType string `json:"capability_type"`
	Name           string `json:"name,omitempty"`
	BoxName        string `json:"box_name,omitempty"`
	DeclaredID     string `json:"declared_id,omitempty"`
	DeclaredBoxID  string `json:"declared_box_id,omitempty"`
	Reason         string `json:"reason"`
	Detail         string `json:"detail,omitempty"`
}

// CapabilityImportReport is what an import did with the declared
// capabilities (bkn-backend interfaces.CapabilityImportReport).
type CapabilityImportReport struct {
	Bound   int               `json:"bound"`
	Skipped []*CapabilitySkip `json:"skipped"`
}

// BKNImportResult is the body of a successful bkn-backend import. A non-empty
// Capabilities.Skipped means the network was created without some of its
// Skills or functions bound, although the import returned 200.
type BKNImportResult struct {
	KNID         string                  `json:"kn_id"`
	Capabilities *CapabilityImportReport `json:"capabilities"`
}

// Schedule statuses (bkn-backend interfaces.ScheduleStatusActive and ScheduleStatusInactive).
const (
	SCHEDULE_STATUS_ACTIVE   = "active"
	SCHEDULE_STATUS_INACTIVE = "inactive"
)

// ActionSchedule is a schedule that runs an action type of a network
// (bkn-backend interfaces.ActionSchedule), with the fields a switch carries
// over to another network. Action type ids are the same in every network of a
// lineage, so a schedule copies as it is.
type ActionSchedule struct {
	ID                 string           `json:"id,omitempty"`
	Name               string           `json:"name"`
	ActionTypeID       string           `json:"action_type_id"`
	CronExpression     string           `json:"cron_expression"`
	InstanceIdentities []map[string]any `json:"_instance_identities"`
	DynamicParams      map[string]any   `json:"dynamic_params,omitempty"`
	Status             string           `json:"status,omitempty"`
}

// BknBackendError is a non-2xx reply from bkn-backend, kept whole so the
// caller can tell a refused permission from an unreachable service.
type BknBackendError struct {
	HTTPCode int
	httperrors.BaseError
}

func (e *BknBackendError) Error() string {
	return fmt.Sprintf("bkn-backend returned HTTP %d: %s %s", e.HTTPCode, e.ErrorCode, e.Description)
}

// BknBackendAccess exports and imports knowledge network definitions through
// the bkn-backend public BKN API. token is the caller's Authorization header
// (hydra.Visitor.TokenID): bkn-backend checks the caller's own permissions.
//
//go:generate mockgen -source ../interfaces/bkn_backend_access.go -destination ../interfaces/mock/mock_bkn_backend_access.go
type BknBackendAccess interface {
	// GetKN returns the network, or bkn-backend's 403 or 404 when the caller cannot see it.
	GetKN(ctx context.Context, token string, knID string) (*KnowledgeNetwork, error)
	// ListKNIDs returns the ids of every network the caller can see.
	ListKNIDs(ctx context.Context, token string) ([]string, error)

	// The implementations kinetic needs link to, read live from the network's main branch as the
	// caller. Object metrics are a professional feature: without it bkn-backend answers a plain 404.

	// ListObjectMetrics lists the network's object metrics, of one owner object type when it is set.
	ListObjectMetrics(ctx context.Context, token string, knID string, ownerObjectTypeID string) ([]*KNObjectMetric, error)
	// GetObjectMetric returns one object metric at its newest version.
	GetObjectMetric(ctx context.Context, token string, knID string, metricID string) (*KNObjectMetric, error)
	// ListCapabilities lists the capabilities bound to the network, of one type when it is set;
	// metadataType openapi narrows functions to APIs.
	ListCapabilities(ctx context.Context, token string, knID string, capabilityType string, metadataType string) ([]*KNCapability, error)
	// ListActionTypes lists the network's action types, of one object type when it is set.
	ListActionTypes(ctx context.Context, token string, knID string, objectTypeID string) ([]*KNActionType, error)
	// ListObjectTypeIDs lists the ids of the network's object types.
	ListObjectTypeIDs(ctx context.Context, token string, knID string) ([]string, error)
	// ListObjectTypeSources lists the network's object types with the data each reads.
	ListObjectTypeSources(ctx context.Context, token string, knID string) ([]*KNObjectTypeSource, error)
}

// KNObjectTypeSource is an object type of a network and the data it reads.
type KNObjectTypeSource struct {
	ID         string        `json:"id"`
	DataSource *KNDataSource `json:"data_source"`
}

// KNDataSource is the data an object type reads: a vega-backend resource (a table) when Type is
// KN_DATA_SOURCE_RESOURCE, or empty as definitions older than the type leave it.
type KNDataSource struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// KN_DATA_SOURCE_RESOURCE is the data source type of a vega-backend resource.
const KN_DATA_SOURCE_RESOURCE = "resource"

// KNObjectMetric is what bkn-project reads of an object metric of a network.
type KNObjectMetric struct {
	ID                  string
	Version             int
	Code                string
	Name                string
	Description         string
	OwnerObjectTypeID   string
	OwnerObjectTypeName string
	MetricType          string
	Status              string
}

// KNCapability is a capability bound to a network: a Skill, a function or API of a toolbox, or a
// tool of an MCP server (BoxID is the toolbox or MCP server).
type KNCapability struct {
	// ID is the binding's: empty for a capability the model uses without the network mounting it.
	ID             string `json:"id,omitempty"`
	CapabilityType string `json:"capability_type"`
	CapabilityID   string `json:"capability_id"`
	BoxID          string `json:"box_id,omitempty"`
	Name           string `json:"name,omitempty"`
	Description    string `json:"description,omitempty"`
	MetadataType   string `json:"metadata_type,omitempty"`
	Status         string `json:"status,omitempty"`
}

// KNActionType is what bkn-project reads of an action type of a network.
type KNActionType struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	ObjectTypeID string `json:"object_type_id"`
	// Comment is the action type's description.
	Comment    string             `json:"comment,omitempty"`
	ActionType string             `json:"action_type"`
	Source     KNActionTypeSource `json:"action_source"`
}

// KNActionTypeSource is the tool an action type runs: a toolbox tool or an MCP tool.
type KNActionTypeSource struct {
	Type     string `json:"type"`
	BoxID    string `json:"box_id,omitempty"`
	ToolID   string `json:"tool_id,omitempty"`
	McpID    string `json:"mcp_id,omitempty"`
	ToolName string `json:"tool_name,omitempty"`
}

// KnowledgeNetwork is what bkn-project reads of a bkn-backend network.
type KnowledgeNetwork struct {
	Operations []string `json:"operations,omitempty"`
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	// Comment is the network's description.
	Comment string `json:"comment,omitempty"`
}
