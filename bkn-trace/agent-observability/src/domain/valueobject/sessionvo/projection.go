package sessionvo

import (
	"sort"
	"strings"
)

type ProjectionMutation struct {
	EventID          string
	AggregateType    string
	AggregateID      string
	AggregateVersion uint64
	EventType        string
	Payload          []byte
}

// ReceiptProjectionDocument keeps the Core read model queryable by the same
// immutable knowledge-network scope used for final authorization.
type ReceiptProjectionDocument struct {
	Receipt
	KnowledgeNetworkIDs []string `json:"knowledge_network_ids,omitempty"`
}

func NewReceiptProjectionDocument(receipt Receipt) ReceiptProjectionDocument {
	return ReceiptProjectionDocument{
		Receipt:             receipt,
		KnowledgeNetworkIDs: KnowledgeNetworkIDs(receipt.BusinessRefs),
	}
}

func KnowledgeNetworkIDs(refs []BusinessRef) []string {
	set := map[string]struct{}{}
	for _, ref := range refs {
		parts := strings.Split(ref.RefID, ":")
		if len(parts) >= 2 && parts[0] != "resource" && parts[1] != "" {
			set[parts[1]] = struct{}{}
		}
	}
	values := make([]string, 0, len(set))
	for value := range set {
		values = append(values, value)
	}
	sort.Strings(values)
	return values
}
