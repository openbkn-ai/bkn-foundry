package sessionvo

import "time"

type IdempotencyRecord struct {
	Scope                   string
	Owner                   Owner
	ExternalConversationKey string
	IdempotencyKey          string
	RequestHash             string
	ResourceType            string
	ResourceID              string
	CreatedAt               time.Time
}
