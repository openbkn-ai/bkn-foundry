package sessionvo

import "time"

type SubjectType string

const (
	SubjectUser    SubjectType = "user"
	SubjectService SubjectType = "service"
)

type ConversationStatus string

const (
	ConversationActive  ConversationStatus = "active"
	ConversationClosed  ConversationStatus = "closed"
	ConversationExpired ConversationStatus = "expired"
)

type Owner struct {
	TenantID               string      `json:"tenant_id" binding:"required"`
	BusinessDomainID       string      `json:"business_domain_id" binding:"required"`
	ApplicationPrincipalID string      `json:"application_principal_id" binding:"required"`
	EffectiveSubjectType   SubjectType `json:"effective_subject_type" binding:"required"`
	EffectiveSubjectID     string      `json:"effective_subject_id" binding:"required"`
	DelegationID           string      `json:"delegation_id,omitempty"`
}

func (o Owner) Equal(other Owner) bool {
	return o == other
}

func (o Owner) Key() string {
	return o.TenantID + "\x00" + o.BusinessDomainID + "\x00" +
		o.ApplicationPrincipalID + "\x00" + string(o.EffectiveSubjectType) +
		"\x00" + o.EffectiveSubjectID + "\x00" + o.DelegationID
}

type Conversation struct {
	ID                      string             `json:"conversation_id" binding:"required"`
	Owner                   Owner              `json:"owner" binding:"required"`
	ExternalConversationKey string             `json:"external_conversation_key" binding:"required"`
	Generation              uint64             `json:"generation" binding:"required"`
	Status                  ConversationStatus `json:"status" binding:"required"`
	OneShot                 bool               `json:"one_shot" binding:"required"`
	RowVersion              uint64             `json:"row_version" binding:"required"`
	CreatedAt               time.Time          `json:"created_at" binding:"required"`
	UpdatedAt               time.Time          `json:"updated_at" binding:"required"`
	ClosedAt                *time.Time         `json:"closed_at,omitempty"`
}
