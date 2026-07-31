package sessionvo

import (
	"strings"
	"time"
)

type BusinessRefType string

const (
	BusinessRefKnowledgeNetwork BusinessRefType = "knowledge_network"
	BusinessRefObjectType       BusinessRefType = "object_type"
	BusinessRefObjectInstance   BusinessRefType = "object_instance"
	BusinessRefProperty         BusinessRefType = "property"
	BusinessRefRelationType     BusinessRefType = "relation_type"
	BusinessRefDataResource     BusinessRefType = "data_resource"
	BusinessRefMetric           BusinessRefType = "metric"
	BusinessRefLogic            BusinessRefType = "logic"
	BusinessRefFunction         BusinessRefType = "function"
	BusinessRefActionType       BusinessRefType = "action_type"
	BusinessRefActionInstance   BusinessRefType = "action_instance"
)

// CanonicalRefPrefix returns the stable RefID prefix for a BKN Trace 3.0 business reference type.
func (value BusinessRefType) CanonicalRefPrefix() string {
	switch value {
	case BusinessRefKnowledgeNetwork:
		return "kn"
	case BusinessRefObjectType:
		return "object"
	case BusinessRefObjectInstance:
		return "object_instance"
	case BusinessRefProperty:
		return "property"
	case BusinessRefRelationType:
		return "relation"
	case BusinessRefDataResource:
		return "resource"
	case BusinessRefMetric:
		return "metric"
	case BusinessRefLogic:
		return "logic"
	case BusinessRefFunction:
		return "function"
	case BusinessRefActionType:
		return "action_type"
	case BusinessRefActionInstance:
		return "action_instance"
	default:
		return ""
	}
}

func (value BusinessRefType) MatchesCanonicalRefID(refID string) bool {
	parts := strings.Split(refID, ":")
	if len(parts) != value.canonicalRefSegmentCount() || parts[0] != value.CanonicalRefPrefix() {
		return false
	}
	for _, part := range parts[1:] {
		if part == "" {
			return false
		}
	}
	return true
}

func (value BusinessRefType) canonicalRefSegmentCount() int {
	switch value {
	case BusinessRefKnowledgeNetwork, BusinessRefDataResource:
		return 2
	case BusinessRefObjectType, BusinessRefRelationType, BusinessRefMetric,
		BusinessRefFunction, BusinessRefActionType:
		return 3
	case BusinessRefObjectInstance, BusinessRefProperty, BusinessRefLogic,
		BusinessRefActionInstance:
		return 4
	default:
		return 0
	}
}

type EvidenceRefType string

const (
	EvidenceRefEvent            EvidenceRefType = "event"
	EvidenceRefArtifact         EvidenceRefType = "artifact"
	EvidenceRefArtifactFragment EvidenceRefType = "artifact_fragment"
	EvidenceRefOperationOutput  EvidenceRefType = "operation_output"
	EvidenceRefClaim            EvidenceRefType = "claim"
)

type EvidenceRef struct {
	Ref                 string          `json:"evidence_ref" binding:"required"`
	RefType             EvidenceRefType `json:"ref_type" binding:"required"`
	SourceInteractionID string          `json:"source_interaction_id" binding:"required"`
	SourceRevisionID    string          `json:"source_revision_id" binding:"required"`
	SourceOperationID   string          `json:"source_operation_id,omitempty"`
	ArtifactRef         string          `json:"artifact_ref,omitempty"`
	FragmentSelector    string          `json:"fragment_selector,omitempty"`
	Version             string          `json:"version" binding:"required"`
	ContentHash         string          `json:"content_hash" binding:"required"`
	AsOf                *time.Time      `json:"as_of,omitempty"`
}

type ClaimMateriality string
type ClaimStatus string
type SupportTargetType string
type SupportStatus string

const (
	ClaimMaterial   ClaimMateriality = "material"
	ClaimSupporting ClaimMateriality = "supporting"

	ClaimAsserted  ClaimStatus = "asserted"
	ClaimWithdrawn ClaimStatus = "withdrawn"

	SupportEvidence         SupportTargetType = "evidence"
	SupportClaim            SupportTargetType = "claim"
	SupportArtifactFragment SupportTargetType = "artifact_fragment"
	SupportOperationOutput  SupportTargetType = "operation_output"

	SupportAdopted  SupportStatus = "adopted"
	SupportRejected SupportStatus = "rejected"
)

type ClaimSupport struct {
	TargetRef           string            `json:"target_ref" binding:"required"`
	TargetType          SupportTargetType `json:"target_type" binding:"required"`
	SourceInteractionID string            `json:"source_interaction_id" binding:"required"`
	SourceRevisionID    string            `json:"source_revision_id" binding:"required"`
	SourceOperationID   string            `json:"source_operation_id,omitempty"`
	Version             string            `json:"version" binding:"required"`
	ContentHash         string            `json:"content_hash" binding:"required"`
	FragmentSelector    string            `json:"fragment_selector,omitempty"`
	Role                string            `json:"role" binding:"required"`
	Status              SupportStatus     `json:"status" binding:"required"`
	Reason              string            `json:"reason,omitempty"`
}

type Claim struct {
	ID          string           `json:"claim_id" binding:"required"`
	Type        string           `json:"claim_type" binding:"required"`
	Materiality ClaimMateriality `json:"materiality" binding:"required"`
	// Status withdrawn preserves history but does not participate in current revision completeness.
	Status               ClaimStatus    `json:"claim_status" binding:"required"`
	ContentArtifactRef   string         `json:"content_artifact_ref" binding:"required"`
	RequiredSupportRoles []string       `json:"required_support_roles" binding:"required"`
	Supports             []ClaimSupport `json:"supports" binding:"required"`
}

type OperationBusinessRole string

const (
	OperationRoleRead      OperationBusinessRole = "read"
	OperationRoleFilter    OperationBusinessRole = "filter"
	OperationRoleGroup     OperationBusinessRole = "group"
	OperationRoleAggregate OperationBusinessRole = "aggregate"
	OperationRoleInput     OperationBusinessRole = "input"
	OperationRoleOutput    OperationBusinessRole = "output"
	OperationRoleModify    OperationBusinessRole = "modify"
	OperationRoleRecommend OperationBusinessRole = "recommend"
	OperationRoleExecute   OperationBusinessRole = "execute"
)

type OperationBusinessEdge struct {
	OperationID string                `json:"operation_id" binding:"required"`
	BusinessRef BusinessRef           `json:"business_ref" binding:"required"`
	Role        OperationBusinessRole `json:"role" binding:"required"`
	ObservedAt  time.Time             `json:"observed_at" binding:"required"`
}
