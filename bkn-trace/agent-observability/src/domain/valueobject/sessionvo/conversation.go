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
	TenantID               string      `json:"tenant_id"`
	BusinessDomainID       string      `json:"business_domain_id"`
	ApplicationPrincipalID string      `json:"application_principal_id"`
	EffectiveSubjectType   SubjectType `json:"effective_subject_type"`
	EffectiveSubjectID     string      `json:"effective_subject_id"`
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
	ID                      string             `json:"conversation_id"`
	Owner                   Owner              `json:"owner"`
	ExternalConversationKey string             `json:"external_conversation_key"`
	Generation              uint64             `json:"generation"`
	Status                  ConversationStatus `json:"status"`
	OneShot                 bool               `json:"one_shot"`
	RowVersion              uint64             `json:"row_version"`
	CreatedAt               time.Time          `json:"created_at"`
	UpdatedAt               time.Time          `json:"updated_at"`
	ClosedAt                *time.Time         `json:"closed_at,omitempty"`
}
